// Package render orchestrates the native asciidoctor toolchain to produce PDF and
// EPUB books, and to validate them (check). It reproduces the invocations a
// hand-written justfile/CI would use: a mermaid puppeteer config passed as a
// document attribute, an mmdc smoke-render preflight, per-format failure levels,
// and PDF-only theme wiring.
package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/codcod/snowball/assets"
	"github.com/codcod/snowball/internal/config"
	"github.com/codcod/snowball/internal/revision"
	"github.com/codcod/snowball/internal/toolchain"
)

// Options are the resolved, flag-overridden render inputs.
type Options struct {
	Formats []string // subset of {"pdf","epub"}; empty means cfg.Formats
	OutDir  string   // directory for outputs; defaults to each book's dir
	Rev     string   // revnumber override
	Date    string   // revdate override
	Books   []string // filter by out/src stem; empty means all

	Jobs    int       // books rendered concurrently; 0 picks a default, 1 is serial
	Quiet   bool      // suppress progress; child output only on failure
	Verbose bool      // additionally log every command line
	Out     io.Writer // progress + child stdout; nil means os.Stdout
	ErrOut  io.Writer // child stderr; nil means os.Stderr
}

// defaultJobs caps concurrency well below NumCPU: every render can spawn a
// headless Chrome for diagrams, so the ceiling is memory, not CPU.
func defaultJobs() int {
	if n := runtime.NumCPU(); n < 4 {
		return n
	}
	return 4
}

// ui is the output policy for one render run. Keeping stdout and stderr
// distinct lets the parallel path swap in per-job buffers (so concurrent
// renders cannot interleave) without collapsing the two streams into one.
type ui struct {
	out, errOut    io.Writer
	quiet, verbose bool
}

func newUI(opts Options) *ui {
	u := &ui{out: opts.Out, errOut: opts.ErrOut, quiet: opts.Quiet, verbose: opts.Verbose}
	if u.out == nil {
		u.out = os.Stdout
	}
	if u.errOut == nil {
		u.errOut = os.Stderr
	}
	return u
}

// logf writes a progress line, unless quiet.
func (u *ui) logf(format string, a ...any) {
	if u.quiet {
		return
	}
	fmt.Fprintf(u.out, format, a...)
}

// buffered returns a copy writing into memory, plus a flush that replays it to
// the parent in one go. Used by the parallel path to keep each job's output
// contiguous.
func (u *ui) buffered() (*ui, func()) {
	var outBuf, errBuf bytes.Buffer
	child := &ui{out: &outBuf, errOut: &errBuf, quiet: u.quiet, verbose: u.verbose}
	return child, func() {
		_, _ = u.out.Write(outBuf.Bytes())
		_, _ = u.errOut.Write(errBuf.Bytes())
	}
}

// plan is the validated work a build will do.
type plan struct {
	books   []config.Book
	formats []string
}

// resolvePlan resolves and validates the selection. It runs before the mermaid
// preflight so that a bad --book or --format fails immediately with the real
// reason, instead of after — or behind — a Chrome launch that may not even be
// possible on this machine.
func resolvePlan(cfg *config.Config, opts Options) (plan, error) {
	formats := opts.Formats
	if len(formats) == 0 {
		formats = cfg.Formats
	}
	for _, f := range formats {
		if _, err := formatExt(f); err != nil {
			return plan{}, err
		}
	}
	books := selectBooks(cfg, opts.Books)
	if len(books) == 0 {
		return plan{}, fmt.Errorf("no books matched")
	}
	return plan{books: books, formats: formats}, nil
}

// Build renders every selected book to every selected format.
func Build(cfg *config.Config, opts Options) error {
	p, err := resolvePlan(cfg, opts)
	if err != nil {
		return err
	}
	u := newUI(opts)
	work, cleanup, err := prepMermaid(cfg, u)
	if err != nil {
		return err
	}
	defer cleanup()
	return build(cfg, opts, p, work, u)
}

// build is Build without the preflight and validation, so watch mode can pay
// those costs once instead of on every rebuild.
func build(cfg *config.Config, opts Options, p plan, work mermaidWork, u *ui) error {
	rev, date := revision.Resolve(cfg, opts.Rev, opts.Date)
	books, formats := p.books, p.formats

	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = defaultJobs()
	}
	if jobs > len(books) {
		jobs = len(books)
	}

	// Concurrency is per book, never per format. asciidoctor-diagram writes its
	// generated images and .asciidoctor cache next to the source, so the two
	// formats of one book share those files — rendering them at once is a
	// write-write race. It is also pointless: the second format reuses the
	// diagram cache the first populated and costs a fraction of it, whereas
	// running both at once makes each re-render every diagram.
	if jobs == 1 {
		for _, b := range books {
			if err := renderBook(cfg, b, formats, opts, rev, date, work, u); err != nil {
				return err
			}
		}
		return nil
	}

	var g errgroup.Group
	g.SetLimit(jobs)
	var mu sync.Mutex
	for _, b := range books {
		g.Go(func() error {
			// Buffer this book's output and replay it in one piece, so
			// concurrent books cannot interleave mid-line.
			bu, flush := u.buffered()
			err := renderBook(cfg, b, formats, opts, rev, date, work, bu)
			mu.Lock()
			flush()
			mu.Unlock()
			return err
		})
	}
	// Unlike the serial path this lets in-flight books finish rather than
	// aborting them, then reports the first failure.
	return g.Wait()
}

// renderBook renders one book to every format, in order.
func renderBook(cfg *config.Config, b config.Book, formats []string, opts Options, rev, date string, work mermaidWork, u *ui) error {
	for _, f := range formats {
		if err := renderOne(cfg, b, f, opts, rev, date, work, u); err != nil {
			return err
		}
	}
	return nil
}

// renderOne dispatches a single book/format pair.
func renderOne(cfg *config.Config, b config.Book, format string, opts Options, rev, date string, work mermaidWork, u *ui) error {
	switch strings.ToLower(format) {
	case "pdf":
		return renderPDF(cfg, b, opts.OutDir, rev, date, work, u)
	case "epub":
		return renderEPUB(cfg, b, opts.OutDir, rev, date, work, u)
	default:
		return fmt.Errorf("unknown format %q", format)
	}
}

// formatExt maps a format name to the extension Build gives its output.
func formatExt(format string) (string, error) {
	switch strings.ToLower(format) {
	case "pdf":
		return ".pdf", nil
	case "epub":
		return ".epub", nil
	default:
		return "", fmt.Errorf("unknown format %q", format)
	}
}

// Clean removes the outputs Build would produce. It deliberately does not touch
// generated diagram images: those are written next to the sources and cannot be
// told apart from hand-authored ones. The .asciidoctor cache is unambiguous, so
// it is removed when withCache is set.
func Clean(cfg *config.Config, opts Options, withCache bool) error {
	books := selectBooks(cfg, opts.Books)
	if len(books) == 0 {
		return fmt.Errorf("no books matched")
	}
	formats := opts.Formats
	if len(formats) == 0 {
		formats = cfg.Formats
	}
	u := newUI(opts)

	for _, b := range books {
		for _, f := range formats {
			ext, err := formatExt(f)
			if err != nil {
				return err
			}
			if err := removePath(outputPath(cfg, b, opts.OutDir, ext), u); err != nil {
				return err
			}
		}
		if withCache {
			cache := filepath.Join(filepath.Dir(cfg.Path(b.Src)), ".asciidoctor")
			if err := removePath(cache, u); err != nil {
				return err
			}
		}
	}
	return nil
}

// removePath deletes p, reporting it. Missing paths are skipped silently, so
// clean is idempotent and does not claim to have removed what was never there.
func removePath(p string, u *ui) error {
	if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := os.RemoveAll(p); err != nil {
		return fmt.Errorf("remove %s: %w", p, err)
	}
	u.logf("snowball: removed %s\n", p)
	return nil
}

// Check validates every selected master by rendering to a null sink with the
// configured check failure level. Mirrors the MR-time ci-docs job.
func Check(cfg *config.Config, opts Options) error {
	books := selectBooks(cfg, opts.Books)
	if len(books) == 0 {
		return fmt.Errorf("no books matched")
	}
	u := newUI(opts)
	work, cleanup, err := prepMermaid(cfg, u)
	if err != nil {
		return err
	}
	defer cleanup()

	for _, b := range books {
		args := []string{"asciidoctor", "-r", "asciidoctor-diagram"}
		args = append(args, cfg.Attributes.Args()...)
		args = append(args,
			"-a", "mermaid-format="+cfg.Mermaid.Format,
			"-a", "mermaid-puppeteer-config="+work.puppeteer,
			"--failure-level="+cfg.FailureLevel.Check,
			"-o", os.DevNull, cfg.Path(b.Src))
		u.logf("snowball: check %s\n", b.Src)
		if err := bundleExec(cfg, u, args...); err != nil {
			return fmt.Errorf("check %s: %w", b.Src, err)
		}
	}
	return nil
}

func renderPDF(cfg *config.Config, b config.Book, outDir, rev, date string, work mermaidWork, u *ui) error {
	out := outputPath(cfg, b, outDir, ".pdf")
	args := []string{"asciidoctor-pdf", "-r", "asciidoctor-diagram"}
	if dir, name, ok := cfg.ThemeDirName(); ok {
		args = append(args, "-a", "pdf-themesdir="+dir, "-a", "pdf-theme="+name)
	}
	// User attributes go before snowball's own: asciidoctor takes the last -a
	// for a given key, so this keeps --rev/--date/theme authoritative.
	args = append(args, cfg.Attributes.Args()...)
	args = append(args,
		"-a", "mermaid-format="+cfg.Mermaid.Format,
		"-a", "mermaid-puppeteer-config="+work.puppeteer,
		"-a", "revnumber="+rev,
		"-a", "revdate="+date,
		"--failure-level="+cfg.FailureLevel.PDF,
		"-o", out, cfg.Path(b.Src))
	u.logf("snowball: pdf  %s -> %s\n", b.Src, out)
	return bundleExec(cfg, u, args...)
}

func renderEPUB(cfg *config.Config, b config.Book, outDir, rev, date string, work mermaidWork, u *ui) error {
	// No theme (asciidoctor-pdf only), error-only failure level. Unlike PDF this
	// used to omit -r asciidoctor-diagram, which made mermaid-format inert: the
	// diagram was emitted as its literal source with no image and a zero exit,
	// so EPUBs silently shipped without diagrams. The extension is required.
	out := outputPath(cfg, b, outDir, ".epub")
	args := []string{"asciidoctor-epub3", "-r", "asciidoctor-diagram"}
	args = append(args, cfg.Attributes.Args()...)
	args = append(args,
		"-a", "mermaid-format="+cfg.Mermaid.Format,
		"-a", "mermaid-puppeteer-config="+work.puppeteer,
		"-a", "revnumber="+rev,
		"-a", "revdate="+date,
		"--failure-level="+cfg.FailureLevel.EPUB,
		"-o", out, cfg.Path(b.Src))
	u.logf("snowball: epub %s -> %s\n", b.Src, out)
	return bundleExec(cfg, u, args...)
}

func outputPath(cfg *config.Config, b config.Book, outDir, ext string) string {
	if outDir == "" {
		outDir = filepath.Dir(cfg.Path(b.Src))
	}
	return filepath.Join(outDir, b.Out+ext)
}

func selectBooks(cfg *config.Config, filter []string) []config.Book {
	if len(filter) == 0 {
		return cfg.Books
	}
	want := make(map[string]bool, len(filter))
	for _, f := range filter {
		want[f] = true
	}
	var out []config.Book
	for _, b := range cfg.Books {
		stem := strings.TrimSuffix(filepath.Base(b.Src), filepath.Ext(b.Src))
		if want[b.Out] || want[stem] {
			out = append(out, b)
		}
	}
	return out
}

// mermaidWork holds the temp puppeteer config path.
type mermaidWork struct{ puppeteer string }

// prepMermaid writes the puppeteer config and runs the mmdc smoke render so a
// Chrome launch failure surfaces with a clear error before the real build.
func prepMermaid(cfg *config.Config, u *ui) (mermaidWork, func(), error) {
	tmp, err := os.MkdirTemp("", "snowball-mermaid-")
	if err != nil {
		return mermaidWork{}, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }

	pupPath := filepath.Join(tmp, "puppeteer.json")
	pup, err := json.Marshal(map[string]any{"args": cfg.Mermaid.PuppeteerArgs})
	if err != nil {
		cleanup()
		return mermaidWork{}, func() {}, err
	}
	if err := os.WriteFile(pupPath, pup, 0o644); err != nil {
		cleanup()
		return mermaidWork{}, func() {}, err
	}

	smokeIn := filepath.Join(tmp, "smoke.mmd")
	smokeOut := filepath.Join(tmp, "smoke.png")
	if err := os.WriteFile(smokeIn, []byte(assets.SmokeMermaid), 0o644); err != nil {
		cleanup()
		return mermaidWork{}, func() {}, err
	}
	u.logf("snowball: mmdc smoke render\n")
	if err := u.run(cfg, "mmdc", "-p", pupPath, "-i", smokeIn, "-o", smokeOut); err != nil {
		cleanup()
		return mermaidWork{}, func() {}, fmt.Errorf("mmdc smoke render failed — Chrome could not launch: %w", err)
	}
	return mermaidWork{puppeteer: pupPath}, cleanup, nil
}

// bundleExec runs `bundle exec <args...>` with the snowball toolchain env.
// The program name is resolved through toolchain.LookPath so a bundler
// vendored into snowball's own cache (because it was missing from PATH at
// `setup` time) is actually found — exec.Command resolves the program name
// against the parent process's PATH, not against the child env renderEnv
// builds, so a bare "bundle" here would not see it. When the lookup itself
// fails, fall back to the bare name so the error the user sees is still
// requireToolchain's "toolchain incomplete", not a lookup failure from here.
func bundleExec(cfg *config.Config, u *ui, args ...string) error {
	bundle := "bundle"
	if path, err := toolchain.LookPath("bundle"); err == nil {
		bundle = path
	}
	return u.run(cfg, bundle, append([]string{"exec"}, args...)...)
}

// run executes a child process against the render environment, routing its
// output according to the ui policy.
func (u *ui) run(cfg *config.Config, name string, args ...string) error {
	if u.verbose {
		fmt.Fprintf(u.out, "snowball: exec %s %s\n", name, strings.Join(args, " "))
	}
	cmd := exec.Command(name, args...)
	cmd.Env = renderEnv(cfg)

	// Under --quiet, hold the child's output and replay it only on failure:
	// silent on success, but never silent about *why* something broke.
	var held bytes.Buffer
	if u.quiet {
		cmd.Stdout, cmd.Stderr = &held, &held
	} else {
		cmd.Stdout, cmd.Stderr = u.out, u.errOut
	}
	err := cmd.Run()
	if err != nil && u.quiet {
		_, _ = u.errOut.Write(held.Bytes())
	}
	return err
}

// renderEnv builds the child environment: point bundler at the snowball-owned
// Gemfile (installed by `snowball setup`) and, on macOS, prefer Homebrew Ruby
// over the system 2.6 shim — the same PATH shim the old justfile used. When
// `setup` vendored bundler and the gem set into its own gem home (because
// bundler was missing from PATH), also point GEM_HOME/GEM_PATH/PATH at it —
// the same shared gem home toolchain.Setup owns end to end — but only when
// the caller has not already set GEM_HOME themselves (an escape hatch, the
// same idiom this function already applies to BUNDLE_GEMFILE), and preserving
// (by prepending to, never replacing) any inherited GEM_PATH.
func renderEnv(cfg *config.Config) []string {
	env := os.Environ()
	if _, err := os.Stat("/opt/homebrew/opt/ruby/bin"); err == nil && runtime.GOOS == "darwin" {
		env = prependPath(env, "/opt/homebrew/opt/ruby/bin")
	}
	if dir, err := toolchain.BundleDir(); err == nil {
		gemfile := filepath.Join(dir, "Gemfile")
		if _, err := os.Stat(gemfile); err == nil && os.Getenv("BUNDLE_GEMFILE") == "" {
			env = append(env, "BUNDLE_GEMFILE="+gemfile)
		}
	}
	if gemDir, err := toolchain.GemDir(); err == nil {
		// GemDir creates an empty "gems" dir on demand (mirroring cacheDir), so
		// check for the bin/ subdirectory setup actually populates — the real
		// signal that something was vendored there, the same way the
		// BUNDLE_GEMFILE check above looks for the Gemfile rather than just the
		// cache dir.
		binDir := filepath.Join(gemDir, "bin")
		if info, err := os.Stat(binDir); err == nil && info.IsDir() {
			env = prependPath(env, binDir)
			if os.Getenv("GEM_HOME") == "" {
				env = append(env, "GEM_HOME="+gemDir)
			}
			env = prependEnvVar(env, "GEM_PATH", gemDir)
		}
	}
	return env
}

func prependPath(env []string, dir string) []string {
	return prependEnvVar(env, "PATH", dir)
}

// prependEnvVar prepends value to key's existing PATH-style (list) value in
// env, preserving whatever was already there, or sets key=value when key was
// absent.
func prependEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			env[i] = prefix + value + string(os.PathListSeparator) + kv[len(prefix):]
			return env
		}
	}
	return append(env, prefix+value)
}
