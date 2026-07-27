package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, DefaultFile)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	p := writeConfig(t, "books:\n  - src: docs/manual.adoc\n")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Books[0].Out; got != "manual" {
		t.Errorf("Out default = %q, want manual", got)
	}
	if len(c.Formats) != 2 {
		t.Errorf("Formats default = %v, want [pdf epub]", c.Formats)
	}
	if c.FailureLevel.EPUB != "ERROR" {
		t.Errorf("EPUB failure default = %q, want ERROR", c.FailureLevel.EPUB)
	}
	if c.Revision.From != "git-describe" {
		t.Errorf("Revision.From default = %q", c.Revision.From)
	}
	if len(c.Mermaid.PuppeteerArgs) != 3 {
		t.Errorf("PuppeteerArgs default = %v", c.Mermaid.PuppeteerArgs)
	}
}

func TestLoadNoBooks(t *testing.T) {
	p := writeConfig(t, "formats: [pdf]\n")
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for config with no books")
	}
}

func TestLoadExplicitValuesWin(t *testing.T) {
	p := writeConfig(t, `books:
  - src: docs/manual.adoc
    out: the-manual
formats: [epub]
revision:
  from: static
  static: "1.0.0"
  date-format: "%Y-%m-%d"
mermaid:
  format: svg
  puppeteer-args: ["--no-sandbox"]
failure-level:
  pdf: ERROR
  epub: FATAL
  check: INFO
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Books[0].Out != "the-manual" {
		t.Errorf("Out = %q, want the-manual (explicit out must not be replaced)", c.Books[0].Out)
	}
	if len(c.Formats) != 1 || c.Formats[0] != "epub" {
		t.Errorf("Formats = %v, want [epub]", c.Formats)
	}
	if c.Revision.From != "static" || c.Revision.Static != "1.0.0" || c.Revision.DateFormat != "%Y-%m-%d" {
		t.Errorf("Revision = %+v", c.Revision)
	}
	if c.Mermaid.Format != "svg" || len(c.Mermaid.PuppeteerArgs) != 1 {
		t.Errorf("Mermaid = %+v", c.Mermaid)
	}
	if c.FailureLevel.PDF != "ERROR" || c.FailureLevel.EPUB != "FATAL" || c.FailureLevel.Check != "INFO" {
		t.Errorf("FailureLevel = %+v", c.FailureLevel)
	}
}

func TestLoadSetsDirToTheConfigDirectory(t *testing.T) {
	p := writeConfig(t, "books:\n  - src: docs/m.adoc\n")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if c.Dir != want {
		t.Errorf("Dir = %q, want %q", c.Dir, want)
	}
	if !filepath.IsAbs(c.Dir) {
		t.Errorf("Dir = %q, want an absolute path", c.Dir)
	}
}

func TestLoadDefaultsToCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DefaultFile), []byte("books:\n  - src: m.adoc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	c, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") should read %s from the cwd: %v", DefaultFile, err)
	}
	if c.Books[0].Out != "m" {
		t.Errorf("Out = %q, want m", c.Books[0].Out)
	}
}

func TestLoadWalksUpToFindConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, DefaultFile), []byte("books:\n  - src: docs/m.adoc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "docs", "chapters")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	c, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") should find %s in a parent directory: %v", DefaultFile, err)
	}
	// Dir must anchor to the directory holding the config, not the cwd, so
	// relative book paths keep resolving against the repo root.
	if resolved, want := c.Path(c.Books[0].Src), filepath.Join(root, "docs/m.adoc"); resolved != want {
		t.Errorf("Path = %q, want %q", resolved, want)
	}
}

func TestLoadNoConfigAnywhere(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := Load("")
	if err == nil {
		t.Fatal("expected an error when no config exists in the cwd or any parent")
	}
	if !strings.Contains(err.Error(), "any parent directory") {
		t.Errorf("error = %q, want it to say the parent directories were searched", err)
	}
}

func TestLoadExplicitPathDoesNotWalkUp(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, DefaultFile), []byte("books:\n  - src: m.adoc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	// An explicit --config must be taken literally: discovery is opt-in via
	// the empty path, otherwise a typo'd path would silently load a parent's.
	if _, err := Load(DefaultFile); err == nil {
		t.Fatal("explicit path should not fall back to a parent directory")
	}
}

func TestAttributesArgs(t *testing.T) {
	cases := []struct {
		name string
		attr Attributes
		want []string
	}{
		{"nil is no args", nil, nil},
		{"string value", Attributes{"toc": "left"}, []string{"-a", "toc=left"}},
		{"empty string sets with no value", Attributes{"sectnums": ""}, []string{"-a", "sectnums"}},
		{"nil value sets with no value", Attributes{"sectnums": nil}, []string{"-a", "sectnums"}},
		{"true sets the attribute", Attributes{"sectnums": true}, []string{"-a", "sectnums"}},
		{"false unsets the attribute", Attributes{"toc": false}, []string{"-a", "toc!"}},
		{"int is stringified", Attributes{"toclevels": 3}, []string{"-a", "toclevels=3"}},
		{"float keeps its fraction", Attributes{"v": 2026.1}, []string{"-a", "v=2026.1"}},
		{"value containing = is preserved", Attributes{"k": "a=b"}, []string{"-a", "k=a=b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.attr.Args()
			if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
				t.Errorf("Args() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAttributesArgsAreSorted(t *testing.T) {
	attr := Attributes{"zulu": "1", "alpha": "2", "mike": "3"}
	want := "-a alpha=2 -a mike=3 -a zulu=1"
	// Go randomises map iteration, so repeat: an unsorted implementation
	// passes a single run with high probability.
	for i := 0; i < 50; i++ {
		if got := strings.Join(attr.Args(), " "); got != want {
			t.Fatalf("Args() = %q, want %q", got, want)
		}
	}
}

func TestLoadAttributes(t *testing.T) {
	p := writeConfig(t, "books:\n  - src: m.adoc\nattributes:\n  toc: left\n  sectnums: \"\"\n  toclevels: 3\n")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.Attributes.Args(), " "); got != "-a sectnums -a toc=left -a toclevels=3" {
		t.Errorf("Args() = %q", got)
	}
}

func TestLoadAttributesAsPathIsRejected(t *testing.T) {
	p := writeConfig(t, "books:\n  - src: m.adoc\nattributes: docs/attributes.adoc\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("the pre-0.2 scalar `attributes` form must be rejected, not ignored")
	}
	for _, want := range []string{"must be a map", "docs/attributes.adoc", "include::"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestLoadAttributesEmptyIsAllowed(t *testing.T) {
	p := writeConfig(t, "books:\n  - src: m.adoc\nattributes:\n")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("an empty attributes key should be allowed: %v", err)
	}
	if len(c.Attributes) != 0 {
		t.Errorf("Attributes = %v, want empty", c.Attributes)
	}
}

func TestLoadRejectsSnowballManagedAttributes(t *testing.T) {
	for attr, owner := range map[string]string{
		"revnumber":                "revision",
		"revdate":                  "revision",
		"mermaid-format":           "mermaid.format",
		"mermaid-puppeteer-config": "mermaid.puppeteer-args",
		"pdf-theme":                "theme",
		"pdf-themesdir":            "theme",
	} {
		t.Run(attr, func(t *testing.T) {
			p := writeConfig(t, "books:\n  - src: m.adoc\nattributes:\n  "+attr+": x\n")
			_, err := Load(p)
			if err == nil {
				t.Fatalf("attributes.%s is overwritten by snowball and must be rejected", attr)
			}
			if !strings.Contains(err.Error(), attr) || !strings.Contains(err.Error(), owner) {
				t.Errorf("error %q should name both %q and %q", err, attr, owner)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected an error for a missing config file")
	}
	if !strings.Contains(err.Error(), "read config") {
		t.Errorf("error = %q, want it to mention reading the config", err)
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	p := writeConfig(t, "books:\n  - src: [unclosed\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected an error for malformed YAML")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error = %q, want it to mention parsing", err)
	}
}

func TestLoadWrongYAMLShape(t *testing.T) {
	p := writeConfig(t, "books: not-a-list\n")
	if _, err := Load(p); err == nil {
		t.Fatal("expected an error when books is not a list")
	}
}

func TestLoadBookMissingSrc(t *testing.T) {
	p := writeConfig(t, "books:\n  - out: manual\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected an error for a book entry with no src")
	}
	if !strings.Contains(err.Error(), "missing src") {
		t.Errorf("error = %q, want it to mention the missing src", err)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	p := writeConfig(t, "")
	if _, err := Load(p); err == nil {
		t.Fatal("expected an error for an empty config")
	}
}

func TestOutDefaultStripsOnlyTheExtension(t *testing.T) {
	p := writeConfig(t, `books:
  - src: docs/v1.2/user-manual.adoc
  - src: plain
  - src: docs/archive.tar.gz
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"user-manual", "plain", "archive.tar"}
	for i, w := range want {
		if c.Books[i].Out != w {
			t.Errorf("Books[%d].Out = %q, want %q", i, c.Books[i].Out, w)
		}
	}
}

func TestPath(t *testing.T) {
	c := &Config{Dir: "/repo"}
	cases := []struct{ in, want string }{
		{"docs/m.adoc", filepath.Join("/repo", "docs/m.adoc")},
		{"/abs/m.adoc", "/abs/m.adoc"},
		{"", ""},
		{"./docs/m.adoc", filepath.Join("/repo", "docs/m.adoc")},
		{"../sibling/m.adoc", filepath.Join("/sibling", "m.adoc")},
	}
	for _, tc := range cases {
		if got := c.Path(tc.in); got != tc.want {
			t.Errorf("Path(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestThemeDirNameVariants(t *testing.T) {
	cases := []struct {
		theme    string
		wantName string
	}{
		{"docs/pdf-theme/ai-sdlc-theme.yml", "ai-sdlc"},
		{"themes/basic.yml", "basic"},
		{"themes/plain-theme.yml", "plain"},
		{"themes/my-theme-theme.yml", "my-theme"},
		{"theme.yml", "theme"},
	}
	for _, tc := range cases {
		c := &Config{Dir: "/repo", Theme: tc.theme}
		dir, name, ok := c.ThemeDirName()
		if !ok {
			t.Errorf("ThemeDirName(%q) reported no theme", tc.theme)
			continue
		}
		if name != tc.wantName {
			t.Errorf("ThemeDirName(%q) name = %q, want %q", tc.theme, name, tc.wantName)
		}
		if !filepath.IsAbs(dir) {
			t.Errorf("ThemeDirName(%q) dir = %q, want an absolute path", tc.theme, dir)
		}
	}
}

func TestThemeDirNameAbsoluteTheme(t *testing.T) {
	c := &Config{Dir: "/repo", Theme: "/opt/themes/book-theme.yml"}
	dir, name, ok := c.ThemeDirName()
	if !ok || dir != "/opt/themes" || name != "book" {
		t.Errorf("ThemeDirName = (%q, %q, %v), want (/opt/themes, book, true)", dir, name, ok)
	}
}

func TestThemeDirName(t *testing.T) {
	p := writeConfig(t, "books:\n  - src: docs/m.adoc\ntheme: docs/pdf-theme/ai-sdlc-theme.yml\n")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	dir, name, ok := c.ThemeDirName()
	if !ok {
		t.Fatal("expected theme")
	}
	if name != "ai-sdlc" {
		t.Errorf("theme name = %q, want ai-sdlc", name)
	}
	if filepath.Base(dir) != "pdf-theme" {
		t.Errorf("theme dir = %q, want .../pdf-theme", dir)
	}
}

func TestThemeDirNameEmpty(t *testing.T) {
	p := writeConfig(t, "books:\n  - src: docs/m.adoc\n")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := c.ThemeDirName(); ok {
		t.Error("expected no theme")
	}
}
