// Package revision resolves the revnumber and revdate attributes injected into
// every rendered book. revnumber comes from git (or an override); revdate is a
// strftime-formatted timestamp.
package revision

import (
	"os/exec"
	"strings"
	"time"

	"github.com/codcod/snowball/internal/config"
)

// Resolve returns (revnumber, revdate). Explicit overrides win; otherwise
// revnumber follows cfg.Revision and revdate is now, formatted per cfg.
func Resolve(cfg *config.Config, revOverride, dateOverride string) (rev, date string) {
	rev = revOverride
	if rev == "" {
		switch cfg.Revision.From {
		case "static":
			rev = cfg.Revision.Static
		default:
			rev = gitDescribe(cfg.Dir)
		}
	}
	if rev == "" {
		rev = "dev"
	}

	date = dateOverride
	if date == "" {
		date = time.Now().Format(goLayout(cfg.Revision.DateFormat))
	}
	return rev, date
}

func gitDescribe(dir string) string {
	cmd := exec.Command("git", "describe", "--tags", "--always", "--dirty")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// goLayout converts the small subset of strftime tokens we support into a Go
// reference-time layout. Unknown tokens pass through unchanged.
func goLayout(f string) string {
	r := strings.NewReplacer(
		"%d", "02",
		"%m", "01",
		"%Y", "2006",
		"%y", "06",
		"%B", "January",
		"%b", "Jan",
		"%H", "15",
		"%M", "04",
		"%S", "05",
	)
	if f == "" {
		return "02 January 2006"
	}
	return r.Replace(f)
}
