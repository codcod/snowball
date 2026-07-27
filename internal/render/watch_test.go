package render

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codcod/snowball/internal/config"
)

func TestTriggersRebuild(t *testing.T) {
	cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
	cfg.Theme = "docs/pdf-theme/mybook-theme.yml"
	in := func(rel string) string { return filepath.Join(cfg.Dir, rel) }

	cases := []struct {
		path string
		want bool
		why  string
	}{
		{in("docs/a.adoc"), true, "the book master"},
		{in("docs/chapters/one.adoc"), true, "an included chapter"},
		{in("docs/A.ADOC"), true, "extension match is case-insensitive"},
		{in("docs/pdf-theme/mybook-theme.yml"), true, "the configured theme"},

		// Everything below is written *by* a build. Rebuilding on these would
		// make watch mode loop forever.
		{in("docs/a.pdf"), false, "a rendered PDF"},
		{in("docs/a.epub"), false, "a rendered EPUB"},
		{in("docs/diagram.png"), false, "a generated diagram"},
		{in("docs/.asciidoctor/diagram/x.cache"), false, "the diagram cache"},

		{in("docs/.a.adoc.swp"), false, "a vim swap file"},
		{in("docs/a.adoc~"), false, "an editor backup"},
		{in(".git/index"), false, "git internals"},
		{in("docs/notes.txt"), false, "an unrelated file"},
		{in("docs/other-theme.yml"), false, "a yml that is not the theme"},
	}
	for _, tc := range cases {
		t.Run(tc.why, func(t *testing.T) {
			if got := triggersRebuild(cfg, tc.path); got != tc.want {
				t.Errorf("triggersRebuild(%s) = %v, want %v (%s)", tc.path, got, tc.want, tc.why)
			}
		})
	}
}

func TestWatchRoots(t *testing.T) {
	t.Run("dedupes shared directories", func(t *testing.T) {
		cfg := testConfig(t,
			config.Book{Src: "docs/a.adoc", Out: "a"},
			config.Book{Src: "docs/b.adoc", Out: "b"},
		)
		if got := watchRoots(cfg); len(got) != 1 {
			t.Errorf("watchRoots = %v, want a single docs/ entry", got)
		}
	})

	t.Run("includes the theme directory", func(t *testing.T) {
		cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
		cfg.Theme = "themes/mybook-theme.yml"
		got := watchRoots(cfg)
		if len(got) != 2 {
			t.Fatalf("watchRoots = %v, want the book dir and the theme dir", got)
		}
	})

	t.Run("drops directories covered by an ancestor", func(t *testing.T) {
		cfg := testConfig(t,
			config.Book{Src: "docs/a.adoc", Out: "a"},
			config.Book{Src: "docs/nested/b.adoc", Out: "b"},
		)
		got := watchRoots(cfg)
		if len(got) != 1 || filepath.Base(got[0]) != "docs" {
			t.Errorf("watchRoots = %v, want only the docs/ ancestor", got)
		}
	})
}

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// syncBuffer is an io.Writer safe for the watcher goroutine and the test to
// share.
type syncBuffer struct {
	mu  chan struct{}
	buf bytes.Buffer
}

func newSyncBuffer() *syncBuffer {
	return &syncBuffer{mu: make(chan struct{}, 1)}
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu <- struct{}{}
	defer func() { <-s.mu }()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu <- struct{}{}
	defer func() { <-s.mu }()
	return s.buf.String()
}

func TestWatchRebuildsOnSourceChange(t *testing.T) {
	bin := shimPath(t)
	shimBin(t, bin, "mmdc", "exit 0")
	counter := filepath.Join(t.TempDir(), "builds")
	shimBin(t, bin, "bundle", `echo x >> `+counter+`; exit 0`)

	cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
	cfg.Formats = []string{"pdf"}
	src := filepath.Join(cfg.Dir, "docs", "a.adoc")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("= A\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	builds := func() int {
		raw, err := os.ReadFile(counter)
		if err != nil {
			return 0
		}
		return strings.Count(string(raw), "x")
	}

	out := newSyncBuffer()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, cfg, Options{Out: out, ErrOut: out}) }()

	if !waitFor(t, 5*time.Second, func() bool { return builds() == 1 }) {
		cancel()
		t.Fatalf("watch did not do its initial build (builds=%d)\n%s", builds(), out.String())
	}
	if !waitFor(t, 5*time.Second, func() bool { return strings.Contains(out.String(), "watching") }) {
		cancel()
		t.Fatalf("watch never announced itself:\n%s", out.String())
	}

	// Editing the source must trigger exactly one more build.
	if err := os.WriteFile(src, []byte("= A\n\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 5*time.Second, func() bool { return builds() == 2 }) {
		cancel()
		t.Fatalf("watch did not rebuild after a source edit (builds=%d)\n%s", builds(), out.String())
	}

	// A build artifact appearing in the watched tree must NOT retrigger.
	if err := os.WriteFile(filepath.Join(cfg.Dir, "docs", "a.pdf"), []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Dir, "docs", "diagram.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * WatchSettle)
	if n := builds(); n != 2 {
		cancel()
		t.Fatalf("build outputs retriggered the watch (builds=%d, want 2) — this is the infinite-loop bug\n%s", n, out.String())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Watch returned %v, want nil on cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Watch did not return after its context was cancelled")
	}
}

func TestWatchSurvivesAFailedBuild(t *testing.T) {
	bin := shimPath(t)
	shimBin(t, bin, "mmdc", "exit 0")
	// Fail only while the flag file is absent, so we can heal it mid-watch.
	flag := filepath.Join(t.TempDir(), "ok")
	counter := filepath.Join(t.TempDir(), "builds")
	shimBin(t, bin, "bundle", `echo x >> `+counter+`; [ -f `+flag+` ] || exit 1; exit 0`)

	cfg := testConfig(t, config.Book{Src: "docs/a.adoc", Out: "a"})
	cfg.Formats = []string{"pdf"}
	src := filepath.Join(cfg.Dir, "docs", "a.adoc")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("= A\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := newSyncBuffer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, cfg, Options{Out: out, ErrOut: out}) }()

	if !waitFor(t, 5*time.Second, func() bool { return strings.Contains(out.String(), "build failed") }) {
		t.Fatalf("expected the first build to fail and be reported:\n%s", out.String())
	}
	// The watcher must still be alive: fix the build and edit again.
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 5*time.Second, func() bool { return strings.Contains(out.String(), "watching") }) {
		t.Fatalf("watch did not reach its event loop:\n%s", out.String())
	}
	if err := os.WriteFile(src, []byte("= A\n\nfixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 5*time.Second, func() bool {
		raw, _ := os.ReadFile(counter)
		return strings.Count(string(raw), "x") >= 2
	}) {
		t.Fatalf("watch stopped after a failed build:\n%s", out.String())
	}
}
