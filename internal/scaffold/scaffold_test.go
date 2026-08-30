package scaffold

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/codcod/snowball/internal/config"
	"github.com/codcod/snowball/internal/toolchain"
)

func contains(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

// --- sanitizeProjectName: a project name is interpolated, unquoted, into both
// a filesystem path and a plain YAML scalar, so it must survive both uses ---

func TestSanitizeProjectNameTable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "demo", "demo"},
		{"internal space", "my project", "my-project"},
		{"path separators", "a/b\\c", "a-b-c"},
		{"parent traversal", "../../etc/pwned", "etc-pwned"},
		{"yaml colon-space", "foo: bar", "foo-bar"},
		{"yaml comment hash", "a #comment", "a-comment"},
		{"yaml quote", `"quoted"`, "quoted"},
		{"embedded newline", "a\nb: c", "a-b-c"},
		{"all whitespace", "   ", fallbackProject},
		{"empty", "", fallbackProject},
		{"all disallowed", "###", fallbackProject},
		{"leading/trailing dash and dot", "-.demo.-", "demo"},
		{"double dash preserved as one", "a--b", "a-b"},
		{"dots and underscores allowed", "a_b.c", "a_b.c"},
		// Non-ASCII letters are legal in both a filename and a plain YAML
		// scalar and were never part of the hazard this function guards
		// against — an earlier ASCII-only version of it discarded them
		// anyway, mangling the scaffolded book's own cover title.
		{"accented latin preserved", "café", "café"},
		{"cyrillic preserved", "проект", "проект"},
		{"cjk preserved", "日本語", "日本語"},
		{"unicode mixed with a dangerous character", "café: bar", "café-bar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeProjectName(c.in); got != c.want {
				t.Errorf("sanitizeProjectName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestScaffoldSurvivesYAMLUnsafeProjectName: a project name containing YAML
// metacharacters must not produce a config that either fails to load, or
// — worse — loads with a truncated
// theme reference that silently reintroduces the theme-file-does-not-exist
// defect (asciidoctor-pdf falls back to its default theme and still exits
// non-zero, invisible to a check that only inspects the config, not a build).
func TestScaffoldSurvivesYAMLUnsafeProjectName(t *testing.T) {
	unsafe := []string{"foo: bar", "a #comment", `"quoted"`, "a\nb: c", "../../etc/pwned"}
	for _, name := range unsafe {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := Docs(root, Options{ProjectName: name}); err != nil {
				t.Fatalf("Docs(%q): %v", name, err)
			}
			cfg, err := config.Load(filepath.Join(root, "snowball.yaml"))
			if err != nil {
				t.Fatalf("config with project name %q does not load: %v", name, err)
			}
			dir, themeName, ok := cfg.ThemeDirName()
			if !ok {
				t.Fatalf("project name %q: config sets no theme", name)
			}
			reloaded := filepath.Join(dir, themeName+"-theme.yml")
			if _, err := os.Stat(reloaded); err != nil {
				t.Errorf("project name %q: theme does not round-trip, asciidoctor-pdf would reload %s: %v",
					name, reloaded, err)
			}
		})
	}
}

// --- Tier 1: hermetic structural invariants, one per defect guarded against ---

// defect 2: the theme filename and the config's theme: line must round-trip
// through snowball's own ThemeDirName the way asciidoctor-pdf resolves it.
func TestScaffoldThemeFilenameRoundTrips(t *testing.T) {
	root := t.TempDir()
	if _, err := Docs(root, Options{ProjectName: "demo"}); err != nil {
		t.Fatalf("Docs: %v", err)
	}
	cfg, err := config.Load(filepath.Join(root, "snowball.yaml"))
	if err != nil {
		t.Fatalf("scaffolded config does not load: %v", err)
	}
	dir, name, ok := cfg.ThemeDirName()
	if !ok {
		t.Fatal("scaffolded config sets no theme, want one")
	}
	reloaded := filepath.Join(dir, name+"-theme.yml")
	if _, err := os.Stat(reloaded); err != nil {
		t.Errorf("theme does not round-trip: asciidoctor-pdf would reload %s: %v", reloaded, err)
	}
}

// defect 1: every book the scaffolded config declares must actually exist.
func TestScaffoldConfigBooksAllExist(t *testing.T) {
	root := t.TempDir()
	if _, err := Docs(root, Options{ProjectName: "demo"}); err != nil {
		t.Fatalf("Docs: %v", err)
	}
	cfg, err := config.Load(filepath.Join(root, "snowball.yaml"))
	if err != nil {
		t.Fatalf("scaffolded config does not load: %v", err)
	}
	if len(cfg.Books) == 0 {
		t.Fatal("scaffolded config has no books")
	}
	for _, b := range cfg.Books {
		p := cfg.Path(b.Src)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("book src %s does not exist: %v", p, err)
		}
	}
}

// defects 1, 2, 7: plain `snowball init`'s config (StarterConfig(name, false))
// must describe exactly one book and set no theme: key at all.
func TestInitConfigHasNoDanglingReferences(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, config.DefaultFile)
	if err := os.WriteFile(p, StarterConfig("demo", false), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("init config does not load: %v", err)
	}
	if len(cfg.Books) != 1 {
		t.Fatalf("init config has %d books, want exactly 1", len(cfg.Books))
	}
	if _, _, ok := cfg.ThemeDirName(); ok {
		t.Error("init config sets a theme, want none — init writes no theme file")
	}
}

// defect: the commented-out `theme:` example used to trail its explanatory
// prose off the end of the same line, so uncommenting just that line left a
// sentence cut mid-clause instead of a bare `theme: <path>`.
func TestInitConfigThemeExampleUncommentsCleanly(t *testing.T) {
	cfg := string(StarterConfig("demo", false))
	var exampleLine string
	for _, line := range strings.Split(cfg, "\n") {
		if strings.HasPrefix(line, "# theme: ") {
			exampleLine = line
			break
		}
	}
	if exampleLine == "" {
		t.Fatalf("init config = %q, want a commented-out \"# theme: \" example line", cfg)
	}
	uncommented := strings.TrimPrefix(exampleLine, "# ")
	want := "theme: " + themePath("demo")
	if uncommented != want {
		t.Errorf("uncommenting %q gives %q, want %q", exampleLine, uncommented, want)
	}
}

// defect 4: a chapter included with leveloffset=+1 must open one level below
// where it renders, or the including document's own section-ordering check
// (`snowball check`) fails on it.
func TestScaffoldChapterOpensBelowItsLeveloffset(t *testing.T) {
	root := t.TempDir()
	if _, err := Docs(root, Options{ProjectName: "demo"}); err != nil {
		t.Fatalf("Docs: %v", err)
	}
	master, err := os.ReadFile(filepath.Join(root, bookMasterPath))
	if err != nil {
		t.Fatal(err)
	}
	includeRe := regexp.MustCompile(`include::([^\[\]]+)\[[^\]]*leveloffset=\+1[^\]]*\]`)
	matches := includeRe.FindAllStringSubmatch(string(master), -1)
	if len(matches) == 0 {
		t.Fatal("book master has no leveloffset=+1 include to check")
	}
	for _, m := range matches {
		chapterFile := filepath.Join(root, filepath.Dir(bookMasterPath), m[1])
		data, err := os.ReadFile(chapterFile)
		if err != nil {
			t.Fatalf("included chapter %s: %v", chapterFile, err)
		}
		heading := firstHeadingLine(string(data))
		if heading == "" {
			t.Fatalf("%s: no heading line found", chapterFile)
		}
		if !regexp.MustCompile(`^= [^=]`).MatchString(heading) {
			t.Errorf("%s: first heading %q does not open at level 0 (=), "+
				"which fails `snowball check`'s section-ordering validation once "+
				"included with leveloffset=+1", chapterFile, heading)
		}
	}
}

// firstHeadingLine returns the first non-blank, non-comment line of an
// AsciiDoc file — the line a leveloffset check cares about.
func firstHeadingLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		return trimmed
	}
	return ""
}

// defect 3: no scaffolded or generated byte may name a foreign product.
func TestNoForeignProductNameInScaffoldOutput(t *testing.T) {
	root := t.TempDir()
	res, err := Docs(root, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "ai-sdlc") { // leakguard:allow
			t.Errorf("%s contains a foreign product name (ai-sdlc)", path) // leakguard:allow
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = res
}

// leakGuardExempt marks a line in this file as legitimately naming a
// forbidden term — the vocabulary table below, and any diagnostic built from
// it — so the line-wise scan in scanForLeaks does not trip on itself while
// still catching a real leak appended anywhere else in this file.
const leakGuardExempt = "// leakguard:allow"

// forbiddenTerm pairs a human label (used in failure messages) with the
// pattern it matches. Every line below carries leakGuardExempt because the
// label text itself names the forbidden term it describes.
type forbiddenTerm struct {
	label string
	re    *regexp.Regexp
}

// forbiddenVocabulary is deliberately narrower than every leak this
// workspace's conventions forbid: it covers only the axes a mechanical scan
// can enforce without judgement — workspace names, ticket-tracking paths, and
// ticket ids of any registered prefix (never exempt, per the workspace's own
// child conventions doc — "never in shipped code, help text, README, or
// CHANGELOG prose"). Sibling product names (rick/morty/summer) are
// deliberately absent: that same doc forbids them "unless it is a genuine
// product fact," a call this scan cannot make, so they stay a manual-review
// concern. A bare "../" rule is absent too — it would false-positive on
// legitimate relative paths already in this repo's own tests (see
// TestScaffoldSurvivesYAMLUnsafeProjectName and config_test.go) — the actual
// danger, a path escaping into the workspace, is already caught by the two
// path-fragment entries below without it.
var forbiddenVocabulary = []forbiddenTerm{ // leakguard:allow
	{"foreign product name (ai-sdlc)", regexp.MustCompile(`ai-sdlc`)},               // leakguard:allow
	{"workspace name (unity)", regexp.MustCompile(`(?i)\bunity\b`)},                 // leakguard:allow
	{"workspace name (translator)", regexp.MustCompile(`(?i)\btranslator\b`)},       // leakguard:allow
	{"ticket-tracking path (tickets/)", regexp.MustCompile(`(?i)tickets/`)},         // leakguard:allow
	{"ticket-tracking path (development/)", regexp.MustCompile(`(?i)development/`)}, // leakguard:allow
	{"ticket-tracking path (BOARD.md)", regexp.MustCompile(`(?i)board\.md`)},        // leakguard:allow
	{"ticket id", regexp.MustCompile(`(?i)\b(?:SNOW|RICK|MRTY|SUMR|T)-\d+\b`)},      // leakguard:allow
}

// leakFinding is one line in one file matching forbiddenVocabulary.
type leakFinding struct {
	path, text, label string
	line              int
}

// isProbablyBinary reports whether data looks like a binary file (a NUL byte
// in its first 8000 bytes) rather than text. This replaces an extension
// allow-list as the leakage guard's file filter: an allow-list silently skips
// every extensionless file (justfile, Gemfile, LICENSE) and any future one,
// while a binary/text distinction closes that blind spot regardless of what
// the file happens to be named.
func isProbablyBinary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(data[:n], 0) != -1
}

// scanForLeaks walks root and returns every line matching forbiddenVocabulary,
// skipping directories named in skipDirs and files that look binary. When
// selfPath is non-empty, lines in that one file carrying leakGuardExempt are
// skipped too — a line-wise exclusion, not a whole-file one, so a real leak
// appended anywhere else in that file still trips the scan.
func scanForLeaks(root, selfPath string, skipDirs map[string]bool) ([]leakFinding, error) {
	var findings []leakFinding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isProbablyBinary(data) {
			return nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		isSelf := selfPath != "" && abs == selfPath
		for i, line := range strings.Split(string(data), "\n") {
			if isSelf && strings.Contains(line, leakGuardExempt) {
				continue
			}
			for _, term := range forbiddenVocabulary {
				if term.re.MatchString(line) {
					findings = append(findings, leakFinding{path, strings.TrimSpace(line), term.label, i + 1})
				}
			}
		}
		return nil
	})
	return findings, err
}

// TestNoWorkspaceLeakageAnywhereInRepo widens the invariant above: a
// scaffold-output-only scan can stay green while a leak survives elsewhere in
// this repository's own source, including a package doc comment. This test
// scans the whole tree, not only what one command happens to generate.
func TestNoWorkspaceLeakageAnywhereInRepo(t *testing.T) {
	root := repoRoot(t)
	selfPath, err := filepath.Abs("scaffold_test.go")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := scanForLeaks(root, selfPath, map[string]bool{".git": true, "dist": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		t.Errorf("%s:%d: forbidden %s: %q", f.path, f.line, f.label, f.text)
	}
}

// defect: an extension allow-list left extensionless files (justfile,
// Gemfile, LICENSE) invisible to the leakage guard — demonstrated during
// review by appending a foreign product name to this repo's own justfile
// and finding the guard still reported clean.
func TestScanForLeaksCatchesExtensionlessFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "justfile"), []byte("# borrowed from ai-sdlc\n"), 0o644); err != nil { // leakguard:allow
		t.Fatal(err)
	}
	findings, err := scanForLeaks(root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %v, want exactly 1 (the justfile leak)", findings)
	}
}

// defect: whole-file self-exclusion meant a genuine leak appended anywhere in
// the guard's own source file also passed, since the file was skipped
// entirely rather than only the lines that legitimately name the terms.
func TestScanForLeaksSelfExclusionIsLineWise(t *testing.T) {
	root := t.TempDir()
	self := filepath.Join(root, "guard.go")
	content := "// exempt line naming ai-sdlc " + leakGuardExempt + "\n" + // leakguard:allow
		"var leaked = \"ai-sdlc\"\n" // leakguard:allow
	if err := os.WriteFile(self, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := scanForLeaks(root, self, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].line != 2 {
		t.Fatalf("findings = %v, want exactly 1 finding on line 2 (the exempt line 1 must not trip)", findings)
	}
}

// TestForbiddenVocabularyPatterns guards the vocabulary itself against both
// false negatives (a real leak the patterns should catch) and false
// positives (ordinary text the ticket-id and word-boundary patterns must not
// mistake for a leak) — mirroring the reasoning already documented for the
// narrower docs-prompt guard at internal/cli/cli_test.go's leakPatterns.
func TestForbiddenVocabularyPatterns(t *testing.T) {
	cases := []struct {
		text      string
		wantMatch bool
	}{
		{"see SNOW-42 for context", true},                      // leakguard:allow
		{"see RICK-7 for context", true},                       // leakguard:allow
		{"tracked as MRTY-3", true},                            // leakguard:allow
		{"tracked as SUMR-9", true},                            // leakguard:allow
		{"the legacy T-101 ticket", true},                      // leakguard:allow
		{"a unity build", true},                                // leakguard:allow
		{"the translator tool", true},                          // leakguard:allow
		{"read tickets/BOARD.md", true},                        // leakguard:allow
		{"see development/rick/conventions.md", true},          // leakguard:allow
		{"Tickets/history moved to a new tracker", true},       // leakguard:allow
		{"Development/staging split is documented here", true}, // leakguard:allow
		{"see BOARD.md for status", true},                      // leakguard:allow
		{"UTF-8 output", false},
		{"EPUB-3 format", false},
		{"ISO-8601 date", false},
		{"community effort", false},
		{"opportunity knocks", false},
		{"a translation layer", false},
	}
	for _, c := range cases {
		got := false
		for _, term := range forbiddenVocabulary {
			if term.re.MatchString(c.text) {
				got = true
				break
			}
		}
		if got != c.wantMatch {
			t.Errorf("forbiddenVocabulary match(%q) = %v, want %v", c.text, got, c.wantMatch)
		}
	}
}

// --- detectGitHubOwner: best-effort owner resolution for .goreleaser.yaml ---

func TestDetectGitHubOwnerTable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH — detectGitHubOwner has nothing to consult")
	}
	cases := []struct {
		name      string
		remoteURL string // empty means "add no origin remote at all"
		wantOwner string
		wantOK    bool
	}{
		{"https with .git suffix", "https://github.com/codcod/demo.git", "codcod", true},
		{"https without .git suffix", "https://github.com/codcod/demo", "codcod", true},
		{"ssh with .git suffix", "git@github.com:codcod/demo.git", "codcod", true},
		{"ssh without .git suffix", "git@github.com:codcod/demo", "codcod", true},
		{"non-github remote", "https://gitlab.com/codcod/demo.git", "", false},
		{"no origin remote", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			runGit(t, root, "init", "-q")
			if c.remoteURL != "" {
				runGit(t, root, "remote", "add", "origin", c.remoteURL)
			}
			owner, ok := detectGitHubOwner(root)
			if ok != c.wantOK || owner != c.wantOwner {
				t.Errorf("detectGitHubOwner(%q) = (%q, %v), want (%q, %v)",
					c.remoteURL, owner, ok, c.wantOwner, c.wantOK)
			}
		})
	}
}

func TestDetectGitHubOwnerNotAGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH — detectGitHubOwner has nothing to consult")
	}
	root := t.TempDir() // no `git init` at all
	if owner, ok := detectGitHubOwner(root); ok {
		t.Errorf("detectGitHubOwner on a non-git directory = (%q, true), want ok=false", owner)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// --- Behavioural coverage ---

func TestDocsDefaultsProjectNameToRootBasename(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "my-project")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Docs(root, Options{}); err != nil {
		t.Fatalf("Docs: %v", err)
	}
	attrs, err := os.ReadFile(filepath.Join(root, attributesPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(attrs), ":product: my-project") {
		t.Errorf("attributes.adoc = %q, want project name defaulted to root basename", attrs)
	}
}

func TestDocsRerunWithoutForceSkipsExisting(t *testing.T) {
	root := t.TempDir()
	if _, err := Docs(root, Options{ProjectName: "demo"}); err != nil {
		t.Fatalf("first Docs: %v", err)
	}
	res, err := Docs(root, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("second Docs: %v", err)
	}
	if len(res.Created) != 0 {
		t.Errorf("Created = %v, want none on a re-run without --force", res.Created)
	}
	for _, f := range []string{attributesPath, bookMasterPath, chapterPath, "snowball.yaml", workflowPath} {
		if !contains(res.Skipped, f) {
			t.Errorf("Skipped = %v, want it to contain %q", res.Skipped, f)
		}
	}
}

func TestDocsForceOverwrites(t *testing.T) {
	root := t.TempDir()
	if _, err := Docs(root, Options{ProjectName: "first"}); err != nil {
		t.Fatalf("first Docs: %v", err)
	}
	if _, err := Docs(root, Options{ProjectName: "second", Force: true}); err != nil {
		t.Fatalf("second Docs: %v", err)
	}
	attrs, err := os.ReadFile(filepath.Join(root, attributesPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(attrs), ":product: second") {
		t.Errorf("attributes.adoc = %q, want overwritten with the second project name", attrs)
	}
}

func TestDocsJustfileMissingIsSkippedNeverCreated(t *testing.T) {
	root := t.TempDir()
	res, err := Docs(root, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "justfile")); !os.IsNotExist(err) {
		t.Errorf("justfile was created (err=%v), want scaffold to never invent one", err)
	}
	if !contains(res.Skipped, "justfile") {
		t.Errorf("Skipped = %v, want a justfile note", res.Skipped)
	}
}

// TestDocsJustfileDetectsRecipeVariants: any legal `just` construct binding a
// name scaffold also wants to append — a parameterised recipe, a "@" quiet
// recipe, or an alias — must be detected as "already defined", or scaffold
// appends a colliding second definition and `just` refuses to parse the file
// at all, while scaffold still reports success. Three variants found this
// way, one per review round, which is why detection is now backed by
// TestDocsJustfileRolledBackWhenAppendWouldBreakIt below rather than trusted
// on its own.
func TestDocsJustfileDetectsRecipeVariants(t *testing.T) {
	cases := []struct {
		name     string
		justfile string
		contains string // a fragment of the original that must survive verbatim
	}{
		{"parameterised with default", "docs-build DIR=\"dist\":\n    echo body\n", `DIR="dist"`},
		{"quiet modifier", "@docs-build:\n    echo body\n", "@docs-build:"},
		{"alias", "some-other-recipe:\n    echo real\n\nalias docs-build := some-other-recipe\n", "alias docs-build :="},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			original := c.justfile
			if err := os.WriteFile(filepath.Join(root, "justfile"), []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			res, err := Docs(root, Options{ProjectName: "demo"})
			if err != nil {
				t.Fatalf("Docs: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(root, "justfile"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(got), "docs-build:") > 1 {
				t.Errorf("justfile = %q, want the existing docs-build left exactly as-is, not duplicated", got)
			}
			if !contains(res.Skipped, "docs-build") {
				t.Errorf("Skipped = %v, want docs-build reported as already defined", res.Skipped)
			}
			if !strings.Contains(string(got), c.contains) {
				t.Errorf("justfile = %q, want the original header preserved verbatim", got)
			}
		})
	}
}

// TestDocsJustfileRolledBackWhenAppendWouldBreakIt is the backstop for the
// whole class of collisions the name detector may not recognise. It does not
// go through definesName at all: it appends a recipe whose name is bound by a
// construct chosen to collide, and asserts that when the result does not
// parse, the user's original file comes back byte-identical and the caller is
// told, instead of being left with a justfile in which *no* recipe runs.
//
// Requires `just` on PATH — the guarantee is best-effort by design (snowball
// does not depend on a task runner), so without it there is nothing to assert.
func TestDocsJustfileRolledBackWhenAppendWouldBreakIt(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just not on PATH — the rollback backstop consults it, so there is nothing to verify")
	}
	root := t.TempDir()
	path := filepath.Join(root, "justfile")

	// The collision is introduced by an *imported* file, so the name never
	// appears in the justfile definesName reads. No amount of pattern-
	// matching on the main file's text can find it — which is the point:
	// this exercises the backstop, not the detector.
	if err := os.WriteFile(filepath.Join(root, "other.just"),
		[]byte("docs-build:\n    echo from-import\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	original := []byte("import 'other.just'\n\nmain-recipe:\n    echo main\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if !justfileParses(path) {
		t.Fatal("fixture is wrong: the justfile must parse before scaffold runs")
	}

	res, err := Docs(root, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !justfileParses(path) {
		t.Errorf("justfile no longer parses after scaffold — the user's whole task runner is dead:\n%s", got)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("justfile = %q, want it restored byte-identical to %q", got, original)
	}
	if !contains(res.Skipped, "justfile") {
		t.Errorf("Skipped = %v, want the caller told the justfile was left unchanged", res.Skipped)
	}
}

func TestDocsJustfileAppendsMissingRecipesOnly(t *testing.T) {
	root := t.TempDir()
	original := "default:\n\t@just --list\n\ndocs-check:\n\techo already-here\n"
	if err := os.WriteFile(filepath.Join(root, "justfile"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Docs(root, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "justfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "echo already-here") {
		t.Errorf("justfile = %q, want the existing docs-check recipe preserved verbatim", got)
	}
	if !strings.Contains(string(got), "docs-build:") {
		t.Errorf("justfile = %q, want docs-build appended", got)
	}
	if strings.Count(string(got), "docs-check:") != 1 {
		t.Errorf("justfile = %q, want exactly one docs-check recipe (not duplicated)", got)
	}
	if !contains(res.Skipped, "docs-check") {
		t.Errorf("Skipped = %v, want docs-check reported as already defined", res.Skipped)
	}
	if !contains(res.Created, "docs-build") {
		t.Errorf("Created = %v, want docs-build reported as appended", res.Created)
	}
}

// defect: the appended docs-build recipe's comment claimed dist/docs/ is
// "never committed", an assertion about .gitignore state scaffold never
// creates.
func TestDocsJustfileCommentDoesNotClaimGitignoreState(t *testing.T) {
	root := t.TempDir()
	original := "default:\n\t@just --list\n"
	if err := os.WriteFile(filepath.Join(root, "justfile"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Docs(root, Options{ProjectName: "demo"}); err != nil {
		t.Fatalf("Docs: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "justfile"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "committed") {
		t.Errorf("justfile = %q, want the docs-build comment to not claim .gitignore state", got)
	}
}

func TestDocsNoWorkflowSkipsWorkflow(t *testing.T) {
	root := t.TempDir()
	res, err := Docs(root, Options{ProjectName: "demo", NoWorkflow: true})
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, workflowPath)); !os.IsNotExist(err) {
		t.Errorf("workflow exists (err=%v), want --no-workflow to skip it", err)
	}
	if contains(res.Created, workflowPath) {
		t.Errorf("Created = %v, want it to not contain the workflow", res.Created)
	}
}

func TestDocsDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	original := "default:\n\t@just --list\n"
	if err := os.WriteFile(filepath.Join(root, "justfile"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Docs(root, Options{ProjectName: "demo", DryRun: true})
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs")); !os.IsNotExist(err) {
		t.Errorf("docs/ exists after --dry-run (err=%v), want no such file", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".github")); !os.IsNotExist(err) {
		t.Errorf(".github/ exists after --dry-run (err=%v), want no such file", err)
	}
	if _, err := os.Stat(filepath.Join(root, "snowball.yaml")); !os.IsNotExist(err) {
		t.Errorf("snowball.yaml exists after --dry-run (err=%v), want no such file", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "justfile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("justfile changed under --dry-run: got %q, want %q", got, original)
	}
	if len(res.Created) != 0 {
		t.Errorf("Created = %v, want none under --dry-run", res.Created)
	}
	if !contains(res.Notes, "would create") {
		t.Errorf("Notes = %v, want a dry-run preview", res.Notes)
	}
}

// --- ci.yml, release.yml, .goreleaser.yaml: the release scaffold bundle ---

func TestDocsScaffoldsReleaseFilesByDefault(t *testing.T) {
	root := t.TempDir()
	runGitIfAvailable(t, root, "https://github.com/codcod/demo.git")
	res, err := Docs(root, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}
	for _, f := range []string{ciWorkflowPath, releaseWorkflowPath, goreleaserConfigPath} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("%s not written by default: %v", f, err)
		}
		if !contains(res.Created, f) {
			t.Errorf("Created = %v, want it to contain %q", res.Created, f)
		}
	}
	gr, err := os.ReadFile(filepath.Join(root, goreleaserConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gr), projectNameToken) || strings.Contains(string(gr), githubOwnerToken) {
		t.Errorf(".goreleaser.yaml = %q, want no unsubstituted tokens", gr)
	}
	if !strings.Contains(string(gr), "project_name: demo") {
		t.Errorf(".goreleaser.yaml = %q, want project_name substituted", gr)
	}
	if strings.Contains(string(gr), "brews:") {
		t.Errorf(".goreleaser.yaml = %q, want no brews: block without --homebrew", gr)
	}
}

func TestDocsNoReleaseWorkflowSkipsAllThree(t *testing.T) {
	root := t.TempDir()
	res, err := Docs(root, Options{ProjectName: "demo", NoReleaseWorkflow: true})
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}
	for _, f := range []string{ciWorkflowPath, releaseWorkflowPath, goreleaserConfigPath} {
		if _, err := os.Stat(filepath.Join(root, f)); !os.IsNotExist(err) {
			t.Errorf("%s exists (err=%v), want --no-release-workflow to skip it", f, err)
		}
		if contains(res.Created, f) {
			t.Errorf("Created = %v, want it to not contain %q", res.Created, f)
		}
	}
	// docs-release.yml (governed by the separate, pre-existing --no-workflow) is unaffected.
	if _, err := os.Stat(filepath.Join(root, workflowPath)); err != nil {
		t.Errorf("docs-release.yml missing (err=%v), --no-release-workflow must not touch --no-workflow's file", err)
	}
}

func TestDocsHomebrewAppendsBrewsBlock(t *testing.T) {
	root := t.TempDir()
	runGitIfAvailable(t, root, "https://github.com/codcod/demo.git")
	if _, err := Docs(root, Options{ProjectName: "demo", Homebrew: true}); err != nil {
		t.Fatalf("Docs: %v", err)
	}
	gr, err := os.ReadFile(filepath.Join(root, goreleaserConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gr), "brews:") {
		t.Errorf(".goreleaser.yaml = %q, want a brews: block with --homebrew", gr)
	}
	if strings.Contains(string(gr), projectNameToken) || strings.Contains(string(gr), githubOwnerToken) {
		t.Errorf(".goreleaser.yaml = %q, want no unsubstituted tokens in the brews block either", gr)
	}
}

func TestDocsHomebrewWithoutReleaseWorkflowIsANoOp(t *testing.T) {
	root := t.TempDir()
	res, err := Docs(root, Options{ProjectName: "demo", NoReleaseWorkflow: true, Homebrew: true})
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, goreleaserConfigPath)); !os.IsNotExist(err) {
		t.Errorf(".goreleaser.yaml exists (err=%v), want --no-release-workflow to still win", err)
	}
	if !contains(res.Notes, "--homebrew had nothing to attach to") {
		t.Errorf("Notes = %v, want a note explaining --homebrew was a no-op", res.Notes)
	}
}

func TestDocsUnknownGitHubOwnerFallsBackToPlaceholder(t *testing.T) {
	root := t.TempDir() // no git remote at all
	res, err := Docs(root, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}
	gr, err := os.ReadFile(filepath.Join(root, goreleaserConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gr), "owner: "+unknownGitHubOwner) {
		t.Errorf(".goreleaser.yaml = %q, want the placeholder owner when no remote is configured", gr)
	}
	if !contains(res.Notes, unknownGitHubOwner) {
		t.Errorf("Notes = %v, want a note about the placeholder owner", res.Notes)
	}
}

// defect: the owner-detection note used to fire whenever detection failed,
// regardless of whether .goreleaser.yaml was actually going to be written —
// including under --dry-run, where the present-tense "reads" wording claimed
// a state that did not exist yet.
func TestDocsUnknownGitHubOwnerNoteIsProspectiveUnderDryRun(t *testing.T) {
	root := t.TempDir() // no git remote, no pre-existing .goreleaser.yaml
	res, err := Docs(root, Options{ProjectName: "demo", DryRun: true})
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, goreleaserConfigPath)); !os.IsNotExist(err) {
		t.Fatalf(".goreleaser.yaml exists after --dry-run (err=%v), want no such file", err)
	}
	if !contains(res.Notes, "would read \""+unknownGitHubOwner+"\"") {
		t.Errorf("Notes = %v, want a prospective (\"would read\") note under --dry-run", res.Notes)
	}
	if contains(res.Notes, "reads \""+unknownGitHubOwner+"\"") {
		t.Errorf("Notes = %v, want no present-tense \"reads\" note under --dry-run", res.Notes)
	}
}

// defect: the same premature note also fired on a re-run without --force,
// where the existing .goreleaser.yaml is left untouched and may already hold
// a perfectly good owner.
func TestDocsUnknownGitHubOwnerNoteSuppressedWhenSkippingExisting(t *testing.T) {
	root := t.TempDir() // no git remote
	existing := "release:\n  github:\n    owner: realowner\n    name: demo\n"
	if err := os.WriteFile(filepath.Join(root, goreleaserConfigPath), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Docs(root, Options{ProjectName: "demo"})
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}
	gr, err := os.ReadFile(filepath.Join(root, goreleaserConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(gr) != existing {
		t.Errorf(".goreleaser.yaml changed on a re-run without --force: got %q, want unchanged %q", gr, existing)
	}
	if contains(res.Notes, unknownGitHubOwner) {
		t.Errorf("Notes = %v, want no owner note when the existing file was left untouched", res.Notes)
	}
}

// runGitIfAvailable best-effort configures a github.com origin remote so
// detectGitHubOwner has something real to resolve; tests that call it accept
// git's absence the same way detectGitHubOwner itself does; assertions that
// depend on a resolved owner are skipped in that case.
func runGitIfAvailable(t *testing.T, root, remoteURL string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH — needed to exercise the owner-detection path")
	}
	runGit(t, root, "init", "-q")
	runGit(t, root, "remote", "add", "origin", remoteURL)
}

// requireActionlintAndGoreleaser skips the calling test when either tool is
// missing from PATH — both are needed only to validate the scaffolded output
// itself, not by the scaffold code, so their absence is a skip, not a
// failure, mirroring the toolchain.Doctor()-gated tests below.
func requireActionlintAndGoreleaser(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("actionlint"); err != nil {
		t.Skip("actionlint not on PATH — skipping scaffolded-workflow validation")
	}
	if _, err := exec.LookPath("goreleaser"); err != nil {
		t.Skip("goreleaser not on PATH — skipping scaffolded-config validation")
	}
}

// TestScaffoldedWorkflowsValidate is the mechanical guard against the exact
// defect this ticket exists to fix: a scaffolded ci.yml/release.yml that
// *looks* right but was never actually run through the tools that validate
// GitHub Actions workflows and goreleaser configs. Covers both the default
// output and --homebrew, since goreleaser check's tolerated exit code differs
// between them (2, "deprecated properties", only once brews: exists).
func TestScaffoldedWorkflowsValidate(t *testing.T) {
	requireActionlintAndGoreleaser(t)

	t.Run("default", func(t *testing.T) {
		root := t.TempDir()
		runGitIfAvailable(t, root, "https://github.com/codcod/demo.git")
		if _, err := Docs(root, Options{ProjectName: "demo"}); err != nil {
			t.Fatalf("Docs: %v", err)
		}
		runActionlint(t, root, ciWorkflowPath, releaseWorkflowPath)
		runGoreleaserCheck(t, root, false)
	})

	t.Run("homebrew", func(t *testing.T) {
		root := t.TempDir()
		runGitIfAvailable(t, root, "https://github.com/codcod/demo.git")
		if _, err := Docs(root, Options{ProjectName: "demo", Homebrew: true}); err != nil {
			t.Fatalf("Docs: %v", err)
		}
		runActionlint(t, root, ciWorkflowPath, releaseWorkflowPath)
		runGoreleaserCheck(t, root, true)
	})
}

// TestScaffoldedCIMatchesGolden guards the ci-surface actionlint incident this
// whole feature exists to prevent (and any other unreviewed edit to the
// generated ci.yml) by comparing the file snowball actually writes,
// byte-for-byte, against a checked-in golden copy — rather than re-deriving
// and enumerating every semantic property a workflow author could violate.
//
// Round 3 replaced a semantic ordering guard (step-position comparison via a
// parsed YAML walk) with this. That guard was simultaneously evadable —
// `GOBIN=` redirecting the install off $PATH, an `if:`-gated install, an
// unguarded second job, and a bare `echo` standing in for the real invocation
// all passed it — and false-positive on harmless edits, such as a comment
// banner between steps or merging two steps into one idiomatic `run: |`
// block. Both failure modes trace to the same cause: the guard was checking
// an unbounded invariant ("this is a semantically valid, differently-shaped
// workflow") when the one that actually matters is bounded and exact: "the
// one ci.yml snowball generates is the file we intend it to be". A byte-diff
// against a golden fixture covers that exactly, with no evasion enumeration
// and no blind spot shaped like whatever the author thought to test for.
//
// A deliberate template edit updates testdata/golden/ci.yml in the same
// commit; the *reviewer* then sees the semantic diff, which is where that
// judgement belongs — not baked into a runtime assertion.
func TestScaffoldedCIMatchesGolden(t *testing.T) {
	root := t.TempDir()
	if _, err := Docs(root, Options{ProjectName: "demo"}); err != nil {
		t.Fatalf("Docs: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, ciWorkflowPath))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "golden", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("scaffolded ci.yml no longer matches testdata/golden/ci.yml.\n"+
			"If this edit to templates/github/workflows/ci.yml is intentional, update "+
			"the golden file in the same commit so the change is reviewed as the diff "+
			"it actually is.\n\ngot:\n%s\n\nwant:\n%s", got, want)
	}
}

// TestScaffoldedGoreleaserCanActuallyRelease is the guard whose absence let a
// broken archives.files ship. `goreleaser check` validates the config's
// *schema*, so it passes a config that cannot archive a real build and can
// never fail for the reason this test exists. This runs an actual snapshot
// release in a repo with no README.md and no CHANGELOG.md — the state
// scaffold itself leaves behind, since it writes neither.
//
// `--skip=before` skips the generated `before.hooks` (`go mod tidy`, `go test
// ./...`): they exercise the adopter's own project, not the scaffolded
// config, and `go mod tidy` would make this test network-dependent.
func TestScaffoldedGoreleaserCanActuallyRelease(t *testing.T) {
	if _, err := exec.LookPath("goreleaser"); err != nil {
		t.Skip("goreleaser not on PATH — skipping the real-release guard")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH — goreleaser needs a repo with a remote to resolve")
	}

	root := newScaffoldedGoRepo(t, "demo")

	// The precondition that makes this test meaningful: scaffold created
	// neither file, and the archive must not demand them.
	for _, f := range []string{"README.md", "CHANGELOG.md"} {
		if _, err := os.Stat(filepath.Join(root, f)); !os.IsNotExist(err) {
			t.Fatalf("fixture is wrong: %s exists, so this test cannot catch the defect", f)
		}
	}

	runSnapshotRelease(t, root)
}

// newScaffoldedGoRepo builds a minimal but real Go module in the layout the
// scaffolded .goreleaser.yaml assumes (`./cmd/<name>`, root go.mod), inside a
// git repo with a GitHub origin so owner detection and goreleaser both have
// something to resolve, and scaffolds into it. Shared by the two archive
// tests so the setup exists once.
//
// The go directive is deliberately an undemanding version rather than the
// toolchain's current one: the fixture is `func main() {}` and needs nothing
// recent, while a high pin makes the test fail red on an older toolchain (and,
// under GOTOOLCHAIN=auto, download one — the network dependency `--skip=before`
// exists to avoid).
func newScaffoldedGoRepo(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "remote", "add", "origin", "https://github.com/codcod/"+name+".git")

	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module github.com/codcod/"+name+"\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", name, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Docs(root, Options{ProjectName: name}); err != nil {
		t.Fatalf("Docs: %v", err)
	}
	return root
}

// runSnapshotRelease runs a real, unpublished goreleaser release in root and
// fails the test on a non-zero exit. `--skip=before` omits the generated
// before.hooks: they exercise the adopter's own project, not the scaffolded
// config, and `go mod tidy` would make this network-dependent.
func runSnapshotRelease(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command("goreleaser", "release", "--snapshot", "--clean", "--skip=before")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the scaffolded .goreleaser.yaml cannot complete a snapshot release "+
			"in a repo scaffold itself produced (or goreleaser rejected the invocation): "+
			"%v\n%s", err, out)
	}
}

// TestScaffoldedArchiveShipsOnlyIntendedFiles is the other half of the
// archive question, and the half that was missed first time round. Fixing the
// original defect (a literal `README.md` that fails the release when absent)
// only asked "does it still release?" — never "what does the fix now let
// through?". The answer was: `README*` also matches `README.md.bak`,
// `README.old` and a `README/` directory, all of which then ship to end users.
//
// Round 3 (B6) added LICENSE litter and a `LICENSES/` directory: round 1
// narrowed README/CHANGELOG to brace patterns but left `LICENSE*` as a bare
// prefix glob one line below the comment condemning that exact form, and this
// test's own whitelisting of "LICENSE" as intended — with no litter neighbour
// to trip over — is why that went unnoticed until the next review round.
//
// So this asserts both directions: the intended files are included, and
// plausible neighbouring litter is not.
func TestScaffoldedArchiveShipsOnlyIntendedFiles(t *testing.T) {
	if _, err := exec.LookPath("goreleaser"); err != nil {
		t.Skip("goreleaser not on PATH — skipping the archive-contents guard")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH — goreleaser needs a repo with a remote to resolve")
	}
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar not on PATH — nothing to inspect the archive with")
	}

	root := newScaffoldedGoRepo(t, "demo")

	// Files a real adopter plausibly has lying around, none of which belongs
	// in a published release archive. LICENSE litter is included here (round
	// 3, B6): round 1 whitelisted "LICENSE" as intended and never gave it a
	// litter neighbour, so the archive glob's un-narrowed `LICENSE*` for that
	// one name went unnoticed by this very test.
	intended := map[string]bool{"README.md": true, "CHANGELOG.md": true, "LICENSE": true}
	litter := []string{
		"README.md.bak", "README.old", "CHANGELOG.md.orig", "READMEX.md",
		"LICENSE-THIRD-PARTY.txt", "LICENSE.bak", "LICENSE.old",
	}
	for f := range intended {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range litter {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Directories whose names share a prefix — plausible docs/legal layouts.
	for _, dir := range []struct{ name, file string }{
		{"README", "notes.txt"},
		{"LICENSES", "mit.txt"},
	} {
		if err := os.MkdirAll(filepath.Join(root, dir.name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir.name, dir.file), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	runSnapshotRelease(t, root)

	matches, err := filepath.Glob(filepath.Join(root, "dist", "*_linux_amd64.tar.gz"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no linux/amd64 archive produced (err=%v)", err)
	}
	out, err := exec.Command("tar", "-tzf", matches[0]).Output()
	if err != nil {
		t.Fatalf("tar -tzf %s: %v", matches[0], err)
	}
	var shipped []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			shipped = append(shipped, line)
		}
	}

	for _, name := range shipped {
		if strings.HasPrefix(name, "README/") || strings.HasPrefix(name, "LICENSES/") {
			t.Errorf("archive ships %q: a directory sharing an intended name's prefix must not be swept in", name)
			continue
		}
		if intended[name] || name == "demo" {
			continue
		}
		t.Errorf("archive ships an unintended file %q — the archive globs are too wide; "+
			"full contents: %v", name, shipped)
	}
	// And the fix must not have gone so narrow that it ships nothing.
	for want := range intended {
		if !contains(shipped, want) {
			t.Errorf("archive is missing %q, which is present in the repo; contents: %v", want, shipped)
		}
	}
}

func runActionlint(t *testing.T, root string, workflows ...string) {
	t.Helper()
	args := make([]string, len(workflows))
	for i, w := range workflows {
		args[i] = filepath.Join(root, w)
	}
	cmd := exec.Command("actionlint", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("actionlint %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// runGoreleaserCheck runs `goreleaser check` against root's .goreleaser.yaml.
// Exit code 2 ("valid, but uses deprecated properties") is tolerated only
// once withBrews is true — that is the only scaffolded construct expected to
// trigger it; a plain scaffold should exit 0.
func runGoreleaserCheck(t *testing.T, root string, withBrews bool) {
	t.Helper()
	cmd := exec.Command("goreleaser", "check")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	exitErr, ok := err.(*exec.ExitError)
	if ok && withBrews && exitErr.ExitCode() == 2 {
		return
	}
	t.Errorf("goreleaser check (withBrews=%v): %v\n%s", withBrews, err, out)
}

// --- Tier 2: end-to-end, skipped when the toolchain is not present ---

func TestScaffoldEndToEndCheckAndBuild(t *testing.T) {
	if _, ok := toolchain.Doctor(); !ok {
		t.Skip("toolchain not present — skipping end-to-end scaffold check/build")
	}

	bin := buildSnowball(t)
	root := t.TempDir()
	if _, err := Docs(root, Options{ProjectName: "demo"}); err != nil {
		t.Fatalf("Docs: %v", err)
	}

	runSnowball(t, bin, root, "check")

	out := filepath.Join(root, "dist", "docs")
	runSnowball(t, bin, root, "build", "-o", out)

	if _, err := os.Stat(filepath.Join(out, "demo-user-manual.pdf")); err != nil {
		t.Errorf("no PDF produced: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "demo-user-manual.epub")); err != nil {
		t.Errorf("no EPUB produced: %v", err)
	}
}

// TestScaffoldEndToEndNonASCIIProjectName confirms a non-ASCII project name
// survives all the way through a real check/build, not merely through
// sanitizeProjectName in isolation — the failure this guards against would
// have shown as a mangled output filename, not a non-zero exit.
func TestScaffoldEndToEndNonASCIIProjectName(t *testing.T) {
	if _, ok := toolchain.Doctor(); !ok {
		t.Skip("toolchain not present — skipping end-to-end scaffold check/build")
	}

	bin := buildSnowball(t)
	root := t.TempDir()
	name := "café"
	if _, err := Docs(root, Options{ProjectName: name}); err != nil {
		t.Fatalf("Docs: %v", err)
	}

	runSnowball(t, bin, root, "check")

	out := filepath.Join(root, "dist", "docs")
	runSnowball(t, bin, root, "build", "-o", out)

	if _, err := os.Stat(filepath.Join(out, name+"-user-manual.pdf")); err != nil {
		t.Errorf("no PDF produced with the expected non-ASCII filename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, name+"-user-manual.epub")); err != nil {
		t.Errorf("no EPUB produced with the expected non-ASCII filename: %v", err)
	}
}

// repoRoot resolves the module root (two levels up from this package) so the
// end-to-end test can `go build` the real cmd/snowball binary regardless of
// the working directory `go test` invokes it from.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..")
}

func buildSnowball(t *testing.T) string {
	t.Helper()
	name := "snowball"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/snowball")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build snowball: %v\n%s", err, out)
	}
	return bin
}

// runSnowball runs the built binary with args in dir and fails the test with
// the command's combined output on a non-zero exit — the assertion that
// matters for defect 2, since a PDF is written even when a theme fails to
// load.
func runSnowball(t *testing.T, bin, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", bin, strings.Join(args, " "), err, out)
	}
}
