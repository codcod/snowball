package revision

import (
	"testing"

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
