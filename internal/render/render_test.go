package render

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/codcod/snowball/internal/config"
)

// testConfig builds a config rooted at a temp dir with the given books, with
// defaults applied the way config.Load would.
func testConfig(t *testing.T, books ...config.Book) *config.Config {
	t.Helper()
	c := &config.Config{
		Books:   books,
		Formats: []string{"pdf", "epub"},
		Dir:     t.TempDir(),
	}
	c.Mermaid.Format = "png"
	c.Mermaid.PuppeteerArgs = []string{"--no-sandbox"}
	c.FailureLevel.PDF = "WARN"
	c.FailureLevel.EPUB = "ERROR"
	c.FailureLevel.Check = "WARN"
	c.Revision.From = "static"
	c.Revision.Static = "v1.2.3"
	c.Revision.DateFormat = "%Y"
	return c
}

// shimBin creates dir/<name> as an executable script and returns the dir.
// The script body is a POSIX shell snippet.
func shimBin(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// shimPath creates a temp bin dir, prepends it to PATH for the test, and
// returns it. exec.Command resolves names against the process PATH, so shims
// placed here win over any real toolchain.
func shimPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell shims are not portable to windows")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

// isolatedPath replaces PATH with an empty directory so that no real toolchain
// is reachable, making tests independent of what the host has installed.
func isolatedPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell shims are not portable to windows")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	return bin
}

// argLog installs a shim that appends its argv (one arg per line, records
// separated by a blank line) to a log file, and returns the log path.
func argLog(t *testing.T, bin, name string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), name+".log")
	shimBin(t, bin, name, `for a in "$@"; do echo "$a" >> `+log+`; done; echo "" >> `+log)
	return log
}

// argLogDir installs a shim that writes each invocation's argv to its own file
// in a fresh directory, and returns that directory. Unlike argLog this is safe
// under concurrency: appending to one shared log from several processes
// interleaves their lines and destroys the record boundaries.
func argLogDir(t *testing.T, bin, name string) string {
	t.Helper()
	dir := t.TempDir()
	shimBin(t, bin, name, `printf '%s\n' "$@" > "$(mktemp `+dir+`/inv.XXXXXX)"`)
	return dir
}

// readInvocationSet reads an argLogDir into one []string per invocation. The
// order is unspecified, so assert on the set, not the sequence.
func readInvocationSet(t *testing.T, dir string) [][]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read arg log dir: %v", err)
	}
	var out [][]string
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n"))
	}
	return out
}

// readInvocations splits an argLog file into one []string per invocation.
func readInvocations(t *testing.T, log string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read arg log: %v", err)
	}
	var out [][]string
	for _, block := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n\n") {
		if block == "" {
			continue
		}
		out = append(out, strings.Split(block, "\n"))
	}
	return out
}

func TestOutputPath(t *testing.T) {
	cfg := testConfig(t, config.Book{Src: "docs/manual.adoc", Out: "manual"})
	b := cfg.Books[0]

	t.Run("defaults to the book's directory", func(t *testing.T) {
		want := filepath.Join(cfg.Dir, "docs", "manual.pdf")
		if got := outputPath(cfg, b, "", ".pdf"); got != want {
			t.Errorf("outputPath = %q, want %q", got, want)
		}
	})

	t.Run("honours an explicit out dir", func(t *testing.T) {
		want := filepath.Join("/tmp/out", "manual.epub")
		if got := outputPath(cfg, b, "/tmp/out", ".epub"); got != want {
			t.Errorf("outputPath = %q, want %q", got, want)
		}
	})

	t.Run("uses Out not the src stem", func(t *testing.T) {
		renamed := config.Book{Src: "docs/manual.adoc", Out: "users-manual"}
		want := filepath.Join(cfg.Dir, "docs", "users-manual.pdf")
		if got := outputPath(cfg, renamed, "", ".pdf"); got != want {
			t.Errorf("outputPath = %q, want %q", got, want)
		}
	})

	t.Run("absolute src is not rejoined to the config dir", func(t *testing.T) {
		abs := config.Book{Src: "/elsewhere/book.adoc", Out: "book"}
		want := filepath.Join("/elsewhere", "book.pdf")
		if got := outputPath(cfg, abs, "", ".pdf"); got != want {
			t.Errorf("outputPath = %q, want %q", got, want)
		}
	})
}

func TestSelectBooks(t *testing.T) {
	manual := config.Book{Src: "docs/user-manual.adoc", Out: "users-manual"}
	handbook := config.Book{Src: "docs/developer-handbook.adoc", Out: "developers-handbook"}
	cfg := testConfig(t, manual, handbook)

	cases := []struct {
		name   string
		filter []string
		want   []string // expected Out values, in order
	}{
		{"empty filter selects all", nil, []string{"users-manual", "developers-handbook"}},
		{"match by out name", []string{"users-manual"}, []string{"users-manual"}},
		{"match by src stem", []string{"developer-handbook"}, []string{"developers-handbook"}},
		{"mixed out and stem", []string{"user-manual", "developers-handbook"}, []string{"users-manual", "developers-handbook"}},
		{"preserves config order, not filter order", []string{"developers-handbook", "users-manual"}, []string{"users-manual", "developers-handbook"}},
		{"duplicate filter yields one book", []string{"users-manual", "user-manual"}, []string{"users-manual"}},
		{"no match yields nothing", []string{"nope"}, nil},
		{"src path does not match", []string{"docs/user-manual.adoc"}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := selectBooks(cfg, tc.filter)
			if len(got) != len(tc.want) {
				t.Fatalf("selected %d books %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for i, w := range tc.want {
				if got[i].Out != w {
					t.Errorf("book[%d].Out = %q, want %q", i, got[i].Out, w)
				}
			}
		})
	}
}

func TestPrependPath(t *testing.T) {
	sep := string(os.PathListSeparator)

	t.Run("prepends to an existing PATH", func(t *testing.T) {
		env := []string{"HOME=/home/x", "PATH=/usr/bin" + sep + "/bin", "LANG=C"}
		got := prependPath(env, "/opt/ruby/bin")
		want := "PATH=/opt/ruby/bin" + sep + "/usr/bin" + sep + "/bin"
		if got[1] != want {
			t.Errorf("PATH = %q, want %q", got[1], want)
		}
		if len(got) != 3 {
			t.Errorf("env grew to %d entries, want 3", len(got))
		}
		if got[0] != "HOME=/home/x" || got[2] != "LANG=C" {
			t.Errorf("unrelated entries mutated: %v", got)
		}
	})

	t.Run("appends PATH when absent", func(t *testing.T) {
		got := prependPath([]string{"HOME=/home/x"}, "/opt/ruby/bin")
		if len(got) != 2 || got[1] != "PATH=/opt/ruby/bin" {
			t.Errorf("prependPath = %v, want [HOME=/home/x PATH=/opt/ruby/bin]", got)
		}
	})

	t.Run("handles an empty PATH value", func(t *testing.T) {
		got := prependPath([]string{"PATH="}, "/opt/ruby/bin")
		if got[0] != "PATH=/opt/ruby/bin"+sep {
			t.Errorf("PATH = %q", got[0])
		}
	})

	t.Run("does not match a PATH-suffixed variable", func(t *testing.T) {
		env := []string{"GOPATH=/go", "PATH=/bin"}
		got := prependPath(env, "/opt/ruby/bin")
		if got[0] != "GOPATH=/go" {
			t.Errorf("GOPATH was rewritten: %q", got[0])
		}
		if !strings.HasPrefix(got[1], "PATH=/opt/ruby/bin") {
			t.Errorf("PATH = %q", got[1])
		}
	})
}

func TestRenderEnv(t *testing.T) {
	cfg := testConfig(t, config.Book{Src: "a.adoc", Out: "a"})

	t.Run("inherits the process environment", func(t *testing.T) {
		t.Setenv("SNOWBALL_TEST_MARKER", "present")
		env := renderEnv(cfg)
		var found bool
		for _, kv := range env {
			if kv == "SNOWBALL_TEST_MARKER=present" {
				found = true
			}
		}
		if !found {
			t.Error("renderEnv dropped an inherited variable")
		}
	})

	t.Run("does not override an explicit BUNDLE_GEMFILE", func(t *testing.T) {
		t.Setenv("BUNDLE_GEMFILE", "/custom/Gemfile")
		var count int
		for _, kv := range renderEnv(cfg) {
			if strings.HasPrefix(kv, "BUNDLE_GEMFILE=") {
				count++
				if kv != "BUNDLE_GEMFILE=/custom/Gemfile" {
					t.Errorf("BUNDLE_GEMFILE = %q, want the caller's value", kv)
				}
			}
		}
		if count != 1 {
			t.Errorf("found %d BUNDLE_GEMFILE entries, want 1", count)
		}
	})
}

func TestPrepMermaid(t *testing.T) {
	bin := shimPath(t)
	log := argLog(t, bin, "mmdc")
	cfg := testConfig(t, config.Book{Src: "a.adoc", Out: "a"})
	cfg.Mermaid.PuppeteerArgs = []string{"--no-sandbox", "--disable-gpu"}

	work, cleanup, err := prepMermaid(cfg, newUI(Options{}))
	if err != nil {
		t.Fatalf("prepMermaid: %v", err)
	}

	raw, err := os.ReadFile(work.puppeteer)
	if err != nil {
		t.Fatalf("puppeteer config not written: %v", err)
	}
	var pup struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(raw, &pup); err != nil {
		t.Fatalf("puppeteer config is not valid JSON: %v", err)
	}
	if strings.Join(pup.Args, ",") != "--no-sandbox,--disable-gpu" {
		t.Errorf("puppeteer args = %v, want the configured args", pup.Args)
	}

	inv := readInvocations(t, log)
	if len(inv) != 1 {
		t.Fatalf("mmdc invoked %d times, want 1", len(inv))
	}
	args := inv[0]
	if args[0] != "-p" || args[1] != work.puppeteer {
		t.Errorf("mmdc args = %v, want -p <puppeteer config> first", args)
	}
	smokeIn := args[3]
	if filepath.Base(smokeIn) != "smoke.mmd" {
		t.Errorf("mmdc input = %q, want the smoke fixture", smokeIn)
	}
	if body, err := os.ReadFile(smokeIn); err != nil || !strings.Contains(string(body), "graph") {
		t.Errorf("smoke fixture missing or empty: %v", err)
	}

	dir := filepath.Dir(work.puppeteer)
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("cleanup left %s behind (err=%v)", dir, err)
	}
}

func TestPrepMermaidChromeFailure(t *testing.T) {
	bin := shimPath(t)
	shimBin(t, bin, "mmdc", "echo 'could not launch chrome' >&2; exit 1")
	cfg := testConfig(t, config.Book{Src: "a.adoc", Out: "a"})

	work, cleanup, err := prepMermaid(cfg, newUI(Options{}))
	defer cleanup()
	if err == nil {
		t.Fatal("expected an error when mmdc fails")
	}
	if !strings.Contains(err.Error(), "Chrome could not launch") {
		t.Errorf("error = %q, want it to name the Chrome launch failure", err)
	}
	if work.puppeteer != "" {
		t.Errorf("work = %+v, want the zero value on failure", work)
	}
}

// These three assert that a bad selection is reported before the mermaid
// preflight runs. They use isolatedPath so no real toolchain is reachable: with
// the ambient PATH they would pass on a machine that happens to have mmdc
// installed even if the ordering were wrong, which is exactly how a regression
// reached CI once.

func TestBuildNoBooksMatched(t *testing.T) {
	isolatedPath(t)
	cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
	err := Build(cfg, Options{Books: []string{"missing"}})
	if err == nil || !strings.Contains(err.Error(), "no books matched") {
		t.Fatalf("Build error = %v, want \"no books matched\"", err)
	}
}

func TestCheckNoBooksMatched(t *testing.T) {
	isolatedPath(t)
	cfg := testConfig(t)
	err := Check(cfg, Options{})
	if err == nil || !strings.Contains(err.Error(), "no books matched") {
		t.Fatalf("Check error = %v, want \"no books matched\"", err)
	}
}

func TestBuildUnknownFormat(t *testing.T) {
	isolatedPath(t)
	cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})

	err := Build(cfg, Options{Formats: []string{"mobi"}})
	if err == nil || !strings.Contains(err.Error(), `unknown format "mobi"`) {
		t.Fatalf("Build error = %v, want unknown format", err)
	}
}

func TestWatchValidatesBeforePreflight(t *testing.T) {
	isolatedPath(t)
	cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
	err := Watch(context.Background(), cfg, Options{Books: []string{"missing"}, Out: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "no books matched") {
		t.Fatalf("Watch error = %v, want \"no books matched\" before any watcher starts", err)
	}
}

func TestBuildInvokesRenderers(t *testing.T) {
	bin := shimPath(t)
	shimBin(t, bin, "mmdc", "exit 0")
	log := argLog(t, bin, "bundle")

	cfg := testConfig(t,
		config.Book{Src: "docs/a.adoc", Out: "a"},
		config.Book{Src: "docs/b.adoc", Out: "b"},
	)
	cfg.Theme = "docs/pdf-theme/mybook-theme.yml"
	outDir := t.TempDir()

	// Jobs: 1 because this test asserts on invocation order, which is only
	// defined for a serial build.
	if err := Build(cfg, Options{Jobs: 1, OutDir: outDir, Rev: "v9.9.9", Date: "01 January 2000"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	inv := readInvocations(t, log)
	// 2 books x 2 formats (pdf, epub from cfg.Formats).
	if len(inv) != 4 {
		t.Fatalf("bundle invoked %d times, want 4", len(inv))
	}

	pdf := inv[0]
	if pdf[0] != "exec" || pdf[1] != "asciidoctor-pdf" {
		t.Fatalf("first invocation = %v, want `exec asciidoctor-pdf ...`", pdf)
	}
	joined := strings.Join(pdf, " ")
	for _, want := range []string{
		"revnumber=v9.9.9",
		"revdate=01 January 2000",
		"pdf-theme=mybook",
		"mermaid-format=png",
		"--failure-level=WARN",
		filepath.Join(outDir, "a.pdf"),
		filepath.Join(cfg.Dir, "docs/a.adoc"),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("pdf invocation missing %q\ngot: %v", want, pdf)
		}
	}

	epub := inv[1]
	if epub[1] != "asciidoctor-epub3" {
		t.Fatalf("second invocation = %v, want asciidoctor-epub3", epub)
	}
	epubJoined := strings.Join(epub, " ")
	if !strings.Contains(epubJoined, "--failure-level=ERROR") {
		t.Errorf("epub failure level not applied: %v", epub)
	}
	if strings.Contains(epubJoined, "pdf-theme") {
		t.Errorf("epub invocation must not carry PDF theme flags: %v", epub)
	}
	// Without the extension loaded, mermaid-format is inert and diagrams are
	// emitted as literal source with a zero exit — a silently broken EPUB.
	if !strings.Contains(epubJoined, "asciidoctor-diagram") {
		t.Errorf("epub invocation must load asciidoctor-diagram: %v", epub)
	}
	if !strings.Contains(epubJoined, "mermaid-puppeteer-config=") {
		t.Errorf("epub invocation must pass the puppeteer config: %v", epub)
	}
	if !strings.Contains(epubJoined, filepath.Join(outDir, "a.epub")) {
		t.Errorf("epub output path wrong: %v", epub)
	}

	if inv[2][1] != "asciidoctor-pdf" || !strings.Contains(strings.Join(inv[2], " "), "b.pdf") {
		t.Errorf("third invocation should render book b to pdf: %v", inv[2])
	}
}

func TestConfiguredAttributesReachEveryRenderer(t *testing.T) {
	bin := shimPath(t)
	shimBin(t, bin, "mmdc", "exit 0")
	log := argLog(t, bin, "bundle")

	cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
	cfg.Attributes = config.Attributes{"toc": "left", "sectnums": "", "toc-title": false}

	if err := Build(cfg, Options{OutDir: t.TempDir()}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := Check(cfg, Options{}); err != nil {
		t.Fatalf("Check: %v", err)
	}

	inv := readInvocations(t, log)
	if len(inv) != 3 { // pdf, epub, check
		t.Fatalf("bundle invoked %d times, want 3", len(inv))
	}
	for _, in := range inv {
		joined := strings.Join(in, " ")
		for _, want := range []string{"-a toc=left", "-a sectnums", "-a toc-title!"} {
			if !strings.Contains(joined, want) {
				t.Errorf("%s invocation missing %q\ngot: %v", in[1], want, in)
			}
		}
	}
}

func TestConfiguredAttributesCannotOverrideManagedOnes(t *testing.T) {
	bin := shimPath(t)
	shimBin(t, bin, "mmdc", "exit 0")
	log := argLog(t, bin, "bundle")

	cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
	cfg.Formats = []string{"pdf"}
	// config.validate rejects these, but if one ever slips through, snowball's
	// own value must still win. asciidoctor takes the last -a for a key, so the
	// user's must be emitted first.
	cfg.Attributes = config.Attributes{"revnumber": "sneaky"}

	if err := Build(cfg, Options{Rev: "v9.9.9", Date: "01 January 2000"}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	args := readInvocations(t, log)[0]
	last := ""
	for i, a := range args {
		if a == "-a" && i+1 < len(args) && strings.HasPrefix(args[i+1], "revnumber=") {
			last = args[i+1]
		}
	}
	if last != "revnumber=v9.9.9" {
		t.Errorf("last revnumber = %q, want revnumber=v9.9.9 (snowball's value must win)", last)
	}
}

func TestBuildOutputVerbosity(t *testing.T) {
	newCfg := func(t *testing.T) *config.Config {
		cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
		cfg.Formats = []string{"pdf"}
		return cfg
	}

	t.Run("normal writes progress to the sink", func(t *testing.T) {
		bin := shimPath(t)
		shimBin(t, bin, "mmdc", "exit 0")
		shimBin(t, bin, "bundle", "exit 0")

		var out, errOut bytes.Buffer
		if err := Build(newCfg(t), Options{Out: &out, ErrOut: &errOut}); err != nil {
			t.Fatalf("Build: %v", err)
		}
		if !strings.Contains(out.String(), "snowball: pdf") {
			t.Errorf("progress missing from sink: %q", out.String())
		}
	})

	t.Run("quiet suppresses progress on success", func(t *testing.T) {
		bin := shimPath(t)
		shimBin(t, bin, "mmdc", "exit 0")
		shimBin(t, bin, "bundle", "echo chatter; exit 0")

		var out, errOut bytes.Buffer
		if err := Build(newCfg(t), Options{Quiet: true, Out: &out, ErrOut: &errOut}); err != nil {
			t.Fatalf("Build: %v", err)
		}
		if out.Len() != 0 || errOut.Len() != 0 {
			t.Errorf("--quiet should print nothing on success, got out=%q err=%q", out.String(), errOut.String())
		}
	})

	t.Run("quiet still reports why a failure happened", func(t *testing.T) {
		bin := shimPath(t)
		shimBin(t, bin, "mmdc", "exit 0")
		shimBin(t, bin, "bundle", "echo 'asciidoctor exploded' >&2; exit 1")

		var out, errOut bytes.Buffer
		if err := Build(newCfg(t), Options{Quiet: true, Out: &out, ErrOut: &errOut}); err == nil {
			t.Fatal("expected the render to fail")
		}
		if !strings.Contains(errOut.String(), "asciidoctor exploded") {
			t.Errorf("--quiet swallowed the failure output: %q", errOut.String())
		}
	})

	t.Run("verbose logs the command line", func(t *testing.T) {
		bin := shimPath(t)
		shimBin(t, bin, "mmdc", "exit 0")
		shimBin(t, bin, "bundle", "exit 0")

		var out, errOut bytes.Buffer
		if err := Build(newCfg(t), Options{Verbose: true, Out: &out, ErrOut: &errOut}); err != nil {
			t.Fatalf("Build: %v", err)
		}
		if !strings.Contains(out.String(), "exec bundle exec asciidoctor-pdf") {
			t.Errorf("--verbose did not log the invocation: %q", out.String())
		}
	})

	t.Run("child stdout and stderr stay on their own streams", func(t *testing.T) {
		bin := shimPath(t)
		shimBin(t, bin, "mmdc", "exit 0")
		shimBin(t, bin, "bundle", "echo to-stdout; echo to-stderr >&2; exit 0")

		var out, errOut bytes.Buffer
		if err := Build(newCfg(t), Options{Out: &out, ErrOut: &errOut}); err != nil {
			t.Fatalf("Build: %v", err)
		}
		if !strings.Contains(out.String(), "to-stdout") || strings.Contains(out.String(), "to-stderr") {
			t.Errorf("stdout sink = %q, want only the child's stdout", out.String())
		}
		if !strings.Contains(errOut.String(), "to-stderr") {
			t.Errorf("stderr sink = %q, want the child's stderr", errOut.String())
		}
	})
}

func TestBuildParallel(t *testing.T) {
	books := []config.Book{
		{Src: "docs/a.adoc", Out: "a"},
		{Src: "docs/b.adoc", Out: "b"},
		{Src: "docs/c.adoc", Out: "c"},
	}

	t.Run("renders every book and format exactly once", func(t *testing.T) {
		bin := shimPath(t)
		shimBin(t, bin, "mmdc", "exit 0")
		log := argLogDir(t, bin, "bundle")

		cfg := testConfig(t, books...)
		var out, errOut bytes.Buffer
		if err := Build(cfg, Options{Jobs: 3, OutDir: t.TempDir(), Out: &out, ErrOut: &errOut}); err != nil {
			t.Fatalf("Build: %v", err)
		}
		inv := readInvocationSet(t, log)
		if len(inv) != 6 { // 3 books x 2 formats
			t.Fatalf("bundle invoked %d times, want 6", len(inv))
		}
		seen := map[string]int{}
		for _, in := range inv {
			for _, a := range in {
				if strings.HasSuffix(a, ".pdf") || strings.HasSuffix(a, ".epub") {
					seen[filepath.Base(a)]++
				}
			}
		}
		for _, want := range []string{"a.pdf", "a.epub", "b.pdf", "b.epub", "c.pdf", "c.epub"} {
			if seen[want] != 1 {
				t.Errorf("%s rendered %d times, want 1 (all: %v)", want, seen[want], seen)
			}
		}
	})

	t.Run("each book's output stays contiguous", func(t *testing.T) {
		bin := shimPath(t)
		shimBin(t, bin, "mmdc", "exit 0")
		// Sleep between the two writes: with unbuffered concurrent output the
		// books' lines would interleave.
		shimBin(t, bin, "bundle", `echo "start $*"; sleep 0.05; echo "end $*"; exit 0`)

		cfg := testConfig(t, books...)
		cfg.Formats = []string{"pdf"}
		var out, errOut bytes.Buffer
		if err := Build(cfg, Options{Jobs: 3, OutDir: t.TempDir(), Out: &out, ErrOut: &errOut}); err != nil {
			t.Fatalf("Build: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		for i := 0; i < len(lines)-1; i++ {
			if strings.HasPrefix(lines[i], "start ") && !strings.HasPrefix(lines[i+1], "end ") {
				t.Errorf("output interleaved: %q followed by %q\nfull:\n%s", lines[i], lines[i+1], out.String())
			}
		}
	})

	t.Run("reports a failure", func(t *testing.T) {
		bin := shimPath(t)
		shimBin(t, bin, "mmdc", "exit 0")
		shimBin(t, bin, "bundle", `case "$*" in *b.pdf*) echo boom >&2; exit 1;; esac; exit 0`)

		cfg := testConfig(t, books...)
		var out, errOut bytes.Buffer
		err := Build(cfg, Options{Jobs: 3, OutDir: t.TempDir(), Out: &out, ErrOut: &errOut})
		if err == nil {
			t.Fatal("expected Build to fail when a book fails to render")
		}
	})

	t.Run("jobs=1 is serial and ordered", func(t *testing.T) {
		bin := shimPath(t)
		shimBin(t, bin, "mmdc", "exit 0")
		log := argLog(t, bin, "bundle")

		cfg := testConfig(t, books...)
		cfg.Formats = []string{"pdf"}
		var out, errOut bytes.Buffer
		if err := Build(cfg, Options{Jobs: 1, OutDir: t.TempDir(), Out: &out, ErrOut: &errOut}); err != nil {
			t.Fatalf("Build: %v", err)
		}
		inv := readInvocations(t, log)
		for i, want := range []string{"a.pdf", "b.pdf", "c.pdf"} {
			if !strings.Contains(strings.Join(inv[i], " "), want) {
				t.Errorf("invocation %d = %v, want %s", i, inv[i], want)
			}
		}
	})

	t.Run("a book's formats never run concurrently", func(t *testing.T) {
		bin := shimPath(t)
		shimBin(t, bin, "mmdc", "exit 0")
		// asciidoctor-diagram writes generated images next to the source, so
		// the formats of one book must not overlap. Each render records its
		// own start/end around a sleep; overlap would show up as interleaving.
		log := filepath.Join(t.TempDir(), "order.log")
		shimBin(t, bin, "bundle", `n=$(echo "$*" | tr ' ' '\n' | grep -c .); echo "IN $2" >> `+log+`; sleep 0.05; echo "OUT $2" >> `+log+`; exit 0`)

		cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
		var out, errOut bytes.Buffer
		if err := Build(cfg, Options{Jobs: 4, OutDir: t.TempDir(), Out: &out, ErrOut: &errOut}); err != nil {
			t.Fatalf("Build: %v", err)
		}
		raw, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		depth := 0
		for _, l := range lines {
			if strings.HasPrefix(l, "IN") {
				depth++
			} else {
				depth--
			}
			if depth > 1 {
				t.Fatalf("two formats of one book overlapped:\n%s", strings.Join(lines, "\n"))
			}
		}
	})
}

func TestClean(t *testing.T) {
	// touch creates the file, making parent dirs as needed.
	touch := func(t *testing.T, p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(p string) bool { _, err := os.Lstat(p); return err == nil }

	t.Run("removes the configured outputs", func(t *testing.T) {
		cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"}, config.Book{Src: "docs/b.adoc", Out: "b"})
		pdf := filepath.Join(cfg.Dir, "docs", "a.pdf")
		epub := filepath.Join(cfg.Dir, "docs", "a.epub")
		bpdf := filepath.Join(cfg.Dir, "docs", "b.pdf")
		src := filepath.Join(cfg.Dir, "docs", "a.adoc")
		for _, p := range []string{pdf, epub, bpdf, src} {
			touch(t, p)
		}

		var out bytes.Buffer
		if err := Clean(cfg, Options{Out: &out}, false); err != nil {
			t.Fatalf("Clean: %v", err)
		}
		for _, p := range []string{pdf, epub, bpdf} {
			if exists(p) {
				t.Errorf("%s should have been removed", filepath.Base(p))
			}
		}
		if !exists(src) {
			t.Error("Clean must never remove a source document")
		}
		if !strings.Contains(out.String(), "removed") {
			t.Errorf("Clean should report what it removed, got %q", out.String())
		}
	})

	t.Run("is idempotent and silent about absent files", func(t *testing.T) {
		cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
		var out bytes.Buffer
		if err := Clean(cfg, Options{Out: &out}, false); err != nil {
			t.Fatalf("Clean on a clean tree should succeed: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("Clean claimed to remove files that never existed: %q", out.String())
		}
	})

	t.Run("honours the format filter", func(t *testing.T) {
		cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
		pdf := filepath.Join(cfg.Dir, "docs", "a.pdf")
		epub := filepath.Join(cfg.Dir, "docs", "a.epub")
		touch(t, pdf)
		touch(t, epub)

		if err := Clean(cfg, Options{Formats: []string{"pdf"}, Out: io.Discard}, false); err != nil {
			t.Fatalf("Clean: %v", err)
		}
		if exists(pdf) {
			t.Error("a.pdf should have been removed")
		}
		if !exists(epub) {
			t.Error("a.epub should have survived --pdf")
		}
	})

	t.Run("honours the out dir", func(t *testing.T) {
		cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
		outDir := t.TempDir()
		built := filepath.Join(outDir, "a.pdf")
		beside := filepath.Join(cfg.Dir, "docs", "a.pdf")
		touch(t, built)
		touch(t, beside)

		if err := Clean(cfg, Options{Formats: []string{"pdf"}, OutDir: outDir, Out: io.Discard}, false); err != nil {
			t.Fatalf("Clean: %v", err)
		}
		if exists(built) {
			t.Error("the output in --out should have been removed")
		}
		if !exists(beside) {
			t.Error("only the --out copy should be removed")
		}
	})

	t.Run("cache removal is opt-in", func(t *testing.T) {
		cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
		cache := filepath.Join(cfg.Dir, "docs", ".asciidoctor", "diagram", "x.cache")
		touch(t, cache)

		if err := Clean(cfg, Options{Out: io.Discard}, false); err != nil {
			t.Fatalf("Clean: %v", err)
		}
		if !exists(cache) {
			t.Fatal("the cache must survive a plain clean")
		}
		if err := Clean(cfg, Options{Out: io.Discard}, true); err != nil {
			t.Fatalf("Clean --cache: %v", err)
		}
		if exists(filepath.Join(cfg.Dir, "docs", ".asciidoctor")) {
			t.Error("--cache should remove the .asciidoctor directory")
		}
	})

	t.Run("rejects an unknown format", func(t *testing.T) {
		cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
		if err := Clean(cfg, Options{Formats: []string{"mobi"}, Out: io.Discard}, false); err == nil {
			t.Fatal("expected an error for an unknown format")
		}
	})

	t.Run("errors when no book matches", func(t *testing.T) {
		cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
		if err := Clean(cfg, Options{Books: []string{"missing"}, Out: io.Discard}, false); err == nil {
			t.Fatal("expected an error when the filter matches nothing")
		}
	})
}

func TestDefaultJobsIsCapped(t *testing.T) {
	if got := defaultJobs(); got < 1 || got > 4 {
		t.Errorf("defaultJobs() = %d, want between 1 and 4", got)
	}
}

func TestBuildWithoutThemeOmitsThemeFlags(t *testing.T) {
	bin := shimPath(t)
	shimBin(t, bin, "mmdc", "exit 0")
	log := argLog(t, bin, "bundle")

	cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
	cfg.Formats = []string{"pdf"}

	if err := Build(cfg, Options{}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	inv := readInvocations(t, log)
	if len(inv) != 1 {
		t.Fatalf("bundle invoked %d times, want 1", len(inv))
	}
	if strings.Contains(strings.Join(inv[0], " "), "pdf-themesdir") {
		t.Errorf("theme flags emitted with no theme configured: %v", inv[0])
	}
}

func TestBuildFormatsOverrideConfig(t *testing.T) {
	bin := shimPath(t)
	shimBin(t, bin, "mmdc", "exit 0")
	log := argLog(t, bin, "bundle")

	cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})

	if err := Build(cfg, Options{Formats: []string{"EPUB"}}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	inv := readInvocations(t, log)
	if len(inv) != 1 {
		t.Fatalf("bundle invoked %d times, want 1 (formats override config)", len(inv))
	}
	if inv[0][1] != "asciidoctor-epub3" {
		t.Errorf("format matching should be case-insensitive: %v", inv[0])
	}
}

func TestBuildPropagatesRendererFailure(t *testing.T) {
	bin := shimPath(t)
	shimBin(t, bin, "mmdc", "exit 0")
	shimBin(t, bin, "bundle", "exit 3")

	cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
	cfg.Formats = []string{"pdf"}

	if err := Build(cfg, Options{}); err == nil {
		t.Fatal("expected Build to fail when the renderer exits non-zero")
	}
}

func TestCheckRendersToDevNull(t *testing.T) {
	bin := shimPath(t)
	shimBin(t, bin, "mmdc", "exit 0")
	log := argLog(t, bin, "bundle")

	cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})

	if err := Check(cfg, Options{}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	inv := readInvocations(t, log)
	if len(inv) != 1 {
		t.Fatalf("bundle invoked %d times, want 1", len(inv))
	}
	joined := strings.Join(inv[0], " ")
	if inv[0][1] != "asciidoctor" {
		t.Errorf("check should run plain asciidoctor: %v", inv[0])
	}
	if !strings.Contains(joined, os.DevNull) {
		t.Errorf("check should discard output to %s: %v", os.DevNull, inv[0])
	}
	if !strings.Contains(joined, "--failure-level=WARN") {
		t.Errorf("check failure level not applied: %v", inv[0])
	}
	if !strings.Contains(joined, "asciidoctor-diagram") {
		t.Errorf("check must load asciidoctor-diagram: %v", inv[0])
	}
}

func TestCheckWrapsRendererError(t *testing.T) {
	bin := shimPath(t)
	shimBin(t, bin, "mmdc", "exit 0")
	shimBin(t, bin, "bundle", "exit 1")

	cfg := testConfig(t, config.Book{Src: "docs/broken.adoc", Out: "broken"})

	err := Check(cfg, Options{})
	if err == nil {
		t.Fatal("expected Check to fail")
	}
	if !strings.Contains(err.Error(), "check docs/broken.adoc") {
		t.Errorf("error = %q, want it to name the failing book", err)
	}
}
