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
