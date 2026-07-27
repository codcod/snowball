package revision

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/codcod/snowball/internal/config"
)

func TestGoLayout(t *testing.T) {
	cases := map[string]string{
		"%d %B %Y":  "02 January 2006",
		"%Y-%m-%d":  "2006-01-02",
		"":          "02 January 2006",
		"%b %d, %Y": "Jan 02, 2006",
	}
	for in, want := range cases {
		if got := goLayout(in); got != want {
			t.Errorf("goLayout(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveOverrides(t *testing.T) {
	cfg := &config.Config{}
	cfg.Revision.From = "git-describe"
	rev, date := Resolve(cfg, "v9.9.9", "01 January 2000")
	if rev != "v9.9.9" {
		t.Errorf("rev = %q, want v9.9.9", rev)
	}
	if date != "01 January 2000" {
		t.Errorf("date = %q", date)
	}
}

func TestResolveStatic(t *testing.T) {
	cfg := &config.Config{}
	cfg.Revision.From = "static"
	cfg.Revision.Static = "1.2.3"
	cfg.Revision.DateFormat = "%Y"
	rev, _ := Resolve(cfg, "", "")
	if rev != "1.2.3" {
		t.Errorf("rev = %q, want 1.2.3", rev)
	}
}

func TestGoLayoutUnknownTokensPassThrough(t *testing.T) {
	cases := map[string]string{
		"%Q":            "%Q",
		"built %Y (%Q)": "built 2006 (%Q)",
		"no tokens":     "no tokens",
		"%H:%M:%S":      "15:04:05",
		"%y":            "06",
	}
	for in, want := range cases {
		if got := goLayout(in); got != want {
			t.Errorf("goLayout(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveDevFallback(t *testing.T) {
	cfg := &config.Config{}
	cfg.Revision.From = "static" // static with no value, and no override
	rev, _ := Resolve(cfg, "", "1999")
	if rev != "dev" {
		t.Errorf("rev = %q, want dev", rev)
	}
}

func TestResolveDevFallbackOutsideAGitRepo(t *testing.T) {
	dir := t.TempDir() // not a git repo
	cfg := &config.Config{Dir: dir}
	cfg.Revision.From = "git-describe"

	rev, _ := Resolve(cfg, "", "1999")
	if rev != "dev" {
		t.Errorf("rev = %q, want dev when git describe fails", rev)
	}
}

func TestResolveDateUsesConfiguredFormat(t *testing.T) {
	cfg := &config.Config{}
	cfg.Revision.From = "static"
	cfg.Revision.Static = "1.0.0"
	cfg.Revision.DateFormat = "%Y-%m-%d"

	_, date := Resolve(cfg, "", "")
	want := time.Now().Format("2006-01-02")
	if date != want {
		t.Errorf("date = %q, want today as %q", date, want)
	}
}

func TestResolveDateDefaultFormat(t *testing.T) {
	cfg := &config.Config{}
	cfg.Revision.From = "static"
	cfg.Revision.Static = "1.0.0"

	_, date := Resolve(cfg, "", "")
	want := time.Now().Format("02 January 2006")
	if date != want {
		t.Errorf("date = %q, want %q", date, want)
	}
}

func TestResolveDateOverrideIsVerbatim(t *testing.T) {
	cfg := &config.Config{}
	cfg.Revision.From = "static"
	cfg.Revision.Static = "1.0.0"
	cfg.Revision.DateFormat = "%Y"

	_, date := Resolve(cfg, "", "whenever you like")
	if date != "whenever you like" {
		t.Errorf("date = %q, want the override passed through unchanged", date)
	}
}

func TestGitDescribe(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "f.txt")
	git("commit", "-qm", "initial")
	git("tag", "v4.5.6")

	t.Run("reads the tag", func(t *testing.T) {
		if got := gitDescribe(dir); got != "v4.5.6" {
			t.Errorf("gitDescribe = %q, want v4.5.6", got)
		}
	})

	t.Run("Resolve uses it when From is git-describe", func(t *testing.T) {
		cfg := &config.Config{Dir: dir}
		cfg.Revision.From = "git-describe"
		rev, _ := Resolve(cfg, "", "1999")
		if rev != "v4.5.6" {
			t.Errorf("rev = %q, want v4.5.6", rev)
		}
	})

	t.Run("marks a dirty tree", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("changed"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := gitDescribe(dir); got != "v4.5.6-dirty" {
			t.Errorf("gitDescribe = %q, want v4.5.6-dirty", got)
		}
	})

	t.Run("an explicit override still wins", func(t *testing.T) {
		cfg := &config.Config{Dir: dir}
		cfg.Revision.From = "git-describe"
		rev, _ := Resolve(cfg, "v0.0.1", "1999")
		if rev != "v0.0.1" {
			t.Errorf("rev = %q, want the override v0.0.1", rev)
		}
	})
}

func TestGitDescribeOutsideARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if got := gitDescribe(t.TempDir()); got != "" {
		t.Errorf("gitDescribe = %q, want an empty string outside a repo", got)
	}
}
