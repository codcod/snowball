// Package render orchestrates the native asciidoctor toolchain to produce PDF and
// EPUB books, and to validate them (check). It reproduces the exact invocations
// ai-sdlc's justfile/CI used today: a mermaid puppeteer config passed as a
// document attribute, an mmdc smoke-render preflight, per-format failure levels,
// and PDF-only theme wiring.
package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

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

	Quiet   bool      // suppress progress; child output only on failure
	Verbose bool      // additionally log every command line
	Out     io.Writer // progress + child stdout; nil means os.Stdout
	ErrOut  io.Writer // child stderr; nil means os.Stderr
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

// Build renders every selected book to every selected format.
func Build(cfg *config.Config, opts Options) error {
	rev, date := revision.Resolve(cfg, opts.Rev, opts.Date)
	formats := opts.Formats
	if len(formats) == 0 {
		formats = cfg.Formats
	}
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
		for _, f := range formats {
			if err := renderOne(cfg, b, f, opts, rev, date, work, u); err != nil {
				return err
			}
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
func bundleExec(cfg *config.Config, u *ui, args ...string) error {
	return u.run(cfg, "bundle", append([]string{"exec"}, args...)...)
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
// over the system 2.6 shim — the same PATH shim the old justfile used.
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
	return env
}

func prependPath(env []string, dir string) []string {
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			env[i] = "PATH=" + dir + string(os.PathListSeparator) + kv[len("PATH="):]
			return env
		}
	}
	return append(env, "PATH="+dir)
}
