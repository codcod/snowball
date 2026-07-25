package config

import (
	"os"
	"path/filepath"
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
