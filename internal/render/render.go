// Package render orchestrates the native asciidoctor toolchain to produce PDF and
// EPUB books, and to validate them (check). It reproduces the exact invocations
// ai-sdlc's justfile/CI used today: a mermaid puppeteer config passed as a
// document attribute, an mmdc smoke-render preflight, per-format failure levels,
// and PDF-only theme wiring.
package render

import (
	"encoding/json"
	"fmt"
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

	work, cleanup, err := prepMermaid(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	for _, b := range books {
		for _, f := range formats {
			var err error
			switch strings.ToLower(f) {
			case "pdf":
				err = renderPDF(cfg, b, opts.OutDir, rev, date, work)
			case "epub":
				err = renderEPUB(cfg, b, opts.OutDir, rev, date)
			default:
				err = fmt.Errorf("unknown format %q", f)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Check validates every selected master by rendering to a null sink with the
// configured check failure level. Mirrors the MR-time ci-docs job.
func Check(cfg *config.Config, opts Options) error {
	books := selectBooks(cfg, opts.Books)
	if len(books) == 0 {
		return fmt.Errorf("no books matched")
	}
	work, cleanup, err := prepMermaid(cfg)
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
		fmt.Printf("snowball: check %s\n", b.Src)
		if err := bundleExec(cfg, args...); err != nil {
			return fmt.Errorf("check %s: %w", b.Src, err)
		}
	}
	return nil
}

func renderPDF(cfg *config.Config, b config.Book, outDir, rev, date string, work mermaidWork) error {
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
	fmt.Printf("snowball: pdf  %s -> %s\n", b.Src, out)
	return bundleExec(cfg, args...)
}

func renderEPUB(cfg *config.Config, b config.Book, outDir, rev, date string) error {
	// Matches today's invocation exactly: no theme, no -r asciidoctor-diagram,
	// error-only failure level. See PLAN.md open item 3.
	out := outputPath(cfg, b, outDir, ".epub")
	args := []string{"asciidoctor-epub3"}
	args = append(args, cfg.Attributes.Args()...)
	args = append(args,
		"-a", "mermaid-format="+cfg.Mermaid.Format,
		"-a", "revnumber="+rev,
		"-a", "revdate="+date,
		"--failure-level="+cfg.FailureLevel.EPUB,
		"-o", out, cfg.Path(b.Src))
	fmt.Printf("snowball: epub %s -> %s\n", b.Src, out)
	return bundleExec(cfg, args...)
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
func prepMermaid(cfg *config.Config) (mermaidWork, func(), error) {
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
	fmt.Println("snowball: mmdc smoke render")
	cmd := exec.Command("mmdc", "-p", pupPath, "-i", smokeIn, "-o", smokeOut)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = renderEnv(cfg)
	if err := cmd.Run(); err != nil {
		cleanup()
		return mermaidWork{}, func() {}, fmt.Errorf("mmdc smoke render failed — Chrome could not launch: %w", err)
	}
	return mermaidWork{puppeteer: pupPath}, cleanup, nil
}

// bundleExec runs `bundle exec <args...>` with the snowball toolchain env.
func bundleExec(cfg *config.Config, args ...string) error {
	full := append([]string{"exec"}, args...)
	cmd := exec.Command("bundle", full...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = renderEnv(cfg)
	return cmd.Run()
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
