// Package scaffold owns both the scaffolded docs tree `snowball scaffold` writes and
// every starter snowball.yaml snowball ever writes — including the config-only one
// `snowball init` writes. That sharing is the point: init and scaffold used to each
// carry their own idea of what a starter config says, and letting those drift apart
// is exactly how a starter config ended up referencing a book and a theme file that
// were never created. There is only one description of the starter layout, in this
// package, and both commands render from it.
package scaffold

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// projectNameToken is substituted, verbatim, for a project name in every template
// file. Plain byte substitution — not text/template — because the GitHub Actions
// template contains `${{ }}` expressions of its own, which would collide with Go's
// default template delimiters.
const projectNameToken = "__PROJECT_NAME__"

// Layout paths, relative to the scaffold root. Declared once so the config
// generator and the file writer can never disagree about where something lives.
const (
	bookMasterPath  = "docs/user-manual.adoc"
	chapterPath     = "docs/user-manual/introduction.adoc"
	attributesPath  = "docs/attributes.adoc"
	themeDir        = "docs/pdf-theme"
	workflowPath    = ".github/workflows/docs-release.yml"
	defaultBookOut  = "user-manual" // suffix appended to the project name for `out:`
	fallbackProject = "project"     // used when a sanitised project name would be empty
)

// disallowedInProjectName matches any run of characters that may not survive
// into a filesystem path segment or a plain (unquoted) YAML scalar. A
// project name is interpolated, unquoted, straight into generated YAML
// (StarterConfig) and into a generated filename (themeFileName) — a ":",
// "#", quote or newline in either position produces a snowball.yaml that
// either fails to parse, or — worse — parses with a truncated value and
// silently reintroduces the theme-file-does-not-exist failure: a
// project name of `a #comment` turns the rest of the theme: line into a
// YAML comment, so the config loads, `check` passes, and only `build` fails
// with asciidoctor-pdf's silent fall-back-but-exit-non-zero behaviour.
// Restricting to a conservative, always-safe set closes both failure modes
// at once, rather than chasing individual metacharacters for each consumer
// (path vs. YAML) separately.
var disallowedInProjectName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// collapseDashes folds any run of two or more "-" (pre-existing or produced
// by disallowedInProjectName above) into one, so a name like "a  b" or
// "a--b" does not leave a visually confusing multi-dash scar.
var collapseDashes = regexp.MustCompile(`-{2,}`)

// sanitizeProjectName makes name safe to use both as a filesystem path
// segment and as an unquoted plain YAML scalar: only [A-Za-z0-9._-] survive,
// runs of anything else collapse to a single "-", leading/trailing "-" and
// "." are trimmed (a leading "." would scaffold a hidden file; a leading
// "-" reads like a flag in some contexts), and an all-invalid or
// all-whitespace name never produces an empty result.
func sanitizeProjectName(name string) string {
	name = strings.TrimSpace(name)
	name = disallowedInProjectName.ReplaceAllString(name, "-")
	name = collapseDashes.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-.")
	if name == "" {
		return fallbackProject
	}
	return name
}

// themeFileName is the theme file snowball scaffolds for a given (already
// sanitised) project name. It must end in "-theme.yml": snowball's own
// config.ThemeDirName derives a theme *name* by stripping ".yml" then "-theme"
// from the configured file, then reloads "<dir>/<name>-theme.yml" — a
// differently-suffixed file does not round-trip, and asciidoctor-pdf's failure
// mode is silent (it falls back to its default theme and still exits non-zero).
func themeFileName(name string) string {
	return name + "-theme.yml"
}

// themePath is the scaffolded theme file's path relative to the scaffold root.
func themePath(name string) string {
	return filepath.ToSlash(filepath.Join(themeDir, themeFileName(name)))
}

// StarterConfig generates the snowball.yaml every starter command writes.
// withTheme selects between the two variants:
//
//   - false (plain `snowball init`): no theme: key at all — init writes no
//     theme file, so pointing at one would recreate the exact "theme file
//     does not exist" failure described above. A commented-out example
//     documents the naming rule instead.
//   - true (`snowball scaffold`): a real theme: line pointing at the theme
//     file scaffold also writes, both derived from the same name so they
//     cannot disagree.
//
// Exactly one book is described; a second is offered only as a commented-out
// example, never as a live reference to a file nothing created.
func StarterConfig(name string, withTheme bool) []byte {
	name = sanitizeProjectName(name)
	out := name + "-" + defaultBookOut

	var theme string
	if withTheme {
		theme = fmt.Sprintf("theme: %s\n", themePath(name))
	} else {
		theme = fmt.Sprintf(
			"# theme: %s   # optional; PDF only. The filename MUST end in\n"+
				"# \"-theme.yml\": snowball derives the theme name by stripping \".yml\" then\n"+
				"# \"-theme\", then asciidoctor-pdf reloads \"<dir>/<name>-theme.yml\". A theme\n"+
				"# file that does not exist still renders a PDF (falling back to the\n"+
				"# built-in default theme) but the command still exits non-zero.\n",
			themePath(name),
		)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "books:\n  - src: %s\n    out: %s\n", bookMasterPath, out)
	fmt.Fprintf(&b, "# a second book is only ever needed if you actually have one:\n")
	fmt.Fprintf(&b, "#  - src: docs/developer-handbook.adoc\n#    out: %s-developer-handbook\n", name)
	b.WriteString(theme)
	b.WriteString("attributes:\n  toc: left\n  sectnums: \"\"\n")
	b.WriteString("formats: [pdf, epub]\n")
	b.WriteString("revision:\n  from: git-describe\n  date-format: \"%d %B %Y\"\n")
	b.WriteString("mermaid:\n  format: png\n" +
		"  puppeteer-args: [\"--no-sandbox\", \"--disable-dev-shm-usage\", \"--disable-gpu\"]\n")
	b.WriteString("failure-level:\n  pdf: WARN\n  epub: ERROR\n  check: WARN\n")
	return []byte(b.String())
}

// substitute replaces every occurrence of projectNameToken with name.
func substitute(data []byte, name string) []byte {
	return bytes.ReplaceAll(data, []byte(projectNameToken), []byte(name))
}
