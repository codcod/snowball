package scaffold

import (
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
		if strings.Contains(string(data), "ai-sdlc") {
			t.Errorf("%s contains a foreign product name (ai-sdlc)", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = res
}

// TestNoForeignProductNameAnywhereInRepo widens the invariant above: a
// scaffold-output-only scan can stay green while a foreign product's own
// name survives elsewhere in this repository's own source, including a
// package doc comment. This test scans the whole tree's source/doc files,
// not only what one command happens to generate.
func TestNoForeignProductNameAnywhereInRepo(t *testing.T) {
	root := repoRoot(t)

	// This file's own diagnostic strings ("contains a foreign product name
	// (ai-sdlc)", directly above and in this doc comment) name the term to
	// talk about it, not to ship it, so this test excludes its own source
	// file from the scan rather than tripping on itself.
	selfPath, err := filepath.Abs("scaffold_test.go")
	if err != nil {
		t.Fatal(err)
	}

	extensions := map[string]bool{".go": true, ".md": true, ".yml": true, ".yaml": true, ".adoc": true}
	skipDirs := map[string]bool{".git": true, "dist": true}

	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !extensions[filepath.Ext(path)] {
			return nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if abs == selfPath {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "ai-sdlc") {
			t.Errorf("%s contains a foreign product name (ai-sdlc) — conventions.md § no workspace leakage", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
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

// TestDocsJustfileDetectsRecipeVariants: any legal `just` recipe header form
// under a name scaffold also wants to append — with parameters, a default
// value, or the "@" quiet modifier — must be detected as "already defined",
// or scaffold appends a colliding second definition under the same name and
// `just` refuses to parse the file at all, while still reporting success.
// Two variants found this way, in two separate rounds: the parameterised
// form first, then "@"-prefixed recipes once the parameterised fix shipped.
func TestDocsJustfileDetectsRecipeVariants(t *testing.T) {
	cases := []struct {
		name     string
		header   string
		contains string // a fragment of the original header that must survive verbatim
	}{
		{"parameterised with default", `docs-build DIR="dist":`, `DIR="dist"`},
		{"quiet modifier", `@docs-build:`, `@docs-build:`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			original := c.header + "\n    echo body\n"
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
			if strings.Count(string(got), "docs-build") != 1 {
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
