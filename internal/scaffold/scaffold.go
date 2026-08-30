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
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// projectNameToken is substituted, verbatim, for a project name in every template
// file. Plain byte substitution — not text/template — because the GitHub Actions
// template contains `${{ }}` expressions of its own, which would collide with Go's
// default template delimiters.
const projectNameToken = "__PROJECT_NAME__"

// githubOwnerToken is substituted, verbatim, for a GitHub owner/org login in
// goreleaser.yaml and its optional brews fragment. Same plain-byte-substitution
// rationale as projectNameToken.
const githubOwnerToken = "__GITHUB_OWNER__"

// unknownGitHubOwner is written in place of githubOwnerToken when
// detectGitHubOwner cannot determine a real one. It is a syntactically valid,
// harmless YAML string — goreleaser check does not verify that a GitHub
// owner/repo actually exists — so it never fails validation; it only needs
// fixing before the first real tag.
const unknownGitHubOwner = "TODO-owner"

// Layout paths, relative to the scaffold root. Declared once so the config
// generator and the file writer can never disagree about where something lives.
const (
	bookMasterPath       = "docs/user-manual.adoc"
	chapterPath          = "docs/user-manual/introduction.adoc"
	attributesPath       = "docs/attributes.adoc"
	themeDir             = "docs/pdf-theme"
	workflowPath         = ".github/workflows/docs-release.yml"
	ciWorkflowPath       = ".github/workflows/ci.yml"
	releaseWorkflowPath  = ".github/workflows/release.yml"
	goreleaserConfigPath = ".goreleaser.yaml"
	defaultBookOut       = "user-manual" // suffix appended to the project name for `out:`
	fallbackProject      = "project"     // used when a sanitised project name would be empty
)

// githubRemoteOwner matches an `origin` remote URL's owner segment for both the
// SSH (`git@github.com:owner/repo.git`) and HTTPS (`https://github.com/owner/repo`)
// forms, with or without a trailing `.git`.
var githubRemoteOwner = regexp.MustCompile(`github\.com[:/]([^/]+)/[^/]+?(\.git)?$`)

// disallowedInProjectName matches any run of characters that may not survive
// into a filesystem path segment or a plain (unquoted) YAML scalar. A
// project name is interpolated, unquoted, straight into generated YAML
// (StarterConfig) and into a generated filename (themeFileName) — a ":",
// "#", quote, newline or path separator in either position produces a
// snowball.yaml that either fails to parse, or — worse — parses with a
// truncated value and silently reintroduces the theme-file-does-not-exist
// failure: a project name of `a #comment` turns the rest of the theme: line
// into a YAML comment, so the config loads, `check` passes, and only
// `build` fails with asciidoctor-pdf's silent fall-back-but-exit-non-zero
// behaviour.
//
// The allowed set is deliberately Unicode-aware (\p{L} letters, \p{N}
// numbers, \p{M} combining marks, plus ".", "_", "-") rather than
// ASCII-only: an earlier, ASCII-only version of this pattern was safe but
// over-corrected, silently mangling every non-ASCII project name ("café"
// → "caf", "проект" → the empty-name fallback) even though a name is not
// merely a filename here — it is also substituted into the scaffolded book
// master's title, so the mangling landed on the rendered manual's own cover
// page. None of the YAML/filesystem metacharacters this pattern exists to
// stop (":", "#", quotes, newlines, "/", "\\") are letters, numbers or
// marks, so widening the allowed set to Unicode text does not reopen either
// failure mode — it only stops discarding text that was never the problem.
var disallowedInProjectName = regexp.MustCompile(`[^\p{L}\p{N}\p{M}._-]+`)

// collapseDashes folds any run of two or more "-" (pre-existing or produced
// by disallowedInProjectName above) into one, so a name like "a  b" or
// "a--b" does not leave a visually confusing multi-dash scar.
var collapseDashes = regexp.MustCompile(`-{2,}`)

// sanitizeProjectName makes name safe to use both as a filesystem path
// segment and as an unquoted plain YAML scalar: Unicode letters, numbers,
// marks, ".", "_" and "-" survive; runs of anything else (path separators,
// YAML metacharacters, whitespace, control characters) collapse to a single
// "-"; leading/trailing "-" and "." are trimmed (a leading "." would
// scaffold a hidden file; a leading "-" reads like a flag in some
// contexts); and an all-invalid or all-whitespace name never produces an
// empty result.
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

// substituteToken replaces every occurrence of token with value. Shares
// substitute's plain-byte-replacement rationale; kept as a separate, generic
// helper rather than folding into substitute so a template that needs neither
// token (or only one of the two) never pays for the other's substitution.
func substituteToken(data []byte, token, value string) []byte {
	return bytes.ReplaceAll(data, []byte(token), []byte(value))
}

// detectGitHubOwner best-effort resolves the GitHub owner/org login that
// root's `origin` remote points at, for use in the scaffolded
// .goreleaser.yaml's release.github.owner (and, if requested, its homebrew
// tap owner). It never errors: git not being installed, no `origin` remote,
// or a remote that isn't a recognisable github.com URL all report ok=false so
// the caller can fall back to a placeholder instead — mirrors the
// not-installed handling in justfileParses (docs.go), which treats "nothing
// to ask" the same as "consulted and inconclusive" rather than as an error.
func detectGitHubOwner(root string) (owner string, ok bool) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", false
	}
	cmd := exec.Command("git", "-C", root, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	m := githubRemoteOwner.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return "", false
	}
	return m[1], true
}
