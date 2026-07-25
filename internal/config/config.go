// Package config loads and validates snowball.yaml, the per-repo declaration of
// which book masters to render, the theme, shared attributes, formats, and the
// mermaid/revision/failure-level knobs. Flags on the command line override it.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultFile is the config filename looked up in the working directory when
// --config is not given.
const DefaultFile = "snowball.yaml"

// Book is a single AsciiDoc master document and the base name of its outputs.
type Book struct {
	Src string `yaml:"src"` // path to the .adoc master, relative to the repo root
	Out string `yaml:"out"` // output basename (no extension); defaults to src stem
}

// Revision controls how revnumber/revdate are resolved when not passed as flags.
type Revision struct {
	From       string `yaml:"from"`        // "git-describe" (default) or "static"
	Static     string `yaml:"static"`      // used when From == "static"
	DateFormat string `yaml:"date-format"` // strftime-style, e.g. "%d %B %Y"
}

// Mermaid controls diagram rendering passed through to asciidoctor-diagram/mmdc.
type Mermaid struct {
	Format        string   `yaml:"format"`         // png (default)
	PuppeteerArgs []string `yaml:"puppeteer-args"` // Chrome launch flags for mmdc
}

// FailureLevel is the --failure-level passed per render mode. Mirrors today's
// justfile/CI: PDF and check warn, EPUB errors only.
type FailureLevel struct {
	PDF   string `yaml:"pdf"`
	EPUB  string `yaml:"epub"`
	Check string `yaml:"check"`
}

// Config is the fully-resolved snowball.yaml.
type Config struct {
	Books        []Book       `yaml:"books"`
	Theme        string       `yaml:"theme"`      // path to <name>-theme.yml (PDF only)
	Attributes   string       `yaml:"attributes"` // shared attributes .adoc (informational)
	Formats      []string     `yaml:"formats"`    // default formats when none passed
	Revision     Revision     `yaml:"revision"`
	Mermaid      Mermaid      `yaml:"mermaid"`
	FailureLevel FailureLevel `yaml:"failure-level"`

	// Dir is the directory the config was loaded from; relative paths in the
	// config are interpreted against it.
	Dir string `yaml:"-"`
}

// Load reads the config at path (or DefaultFile in cwd when path is empty),
// applies defaults, and validates it.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultFile
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	c.Dir = filepath.Dir(abs)
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if len(c.Formats) == 0 {
		c.Formats = []string{"pdf", "epub"}
	}
	if c.Revision.From == "" {
		c.Revision.From = "git-describe"
	}
	if c.Revision.DateFormat == "" {
		c.Revision.DateFormat = "%d %B %Y"
	}
	if c.Mermaid.Format == "" {
		c.Mermaid.Format = "png"
	}
	if len(c.Mermaid.PuppeteerArgs) == 0 {
		c.Mermaid.PuppeteerArgs = []string{"--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"}
	}
	if c.FailureLevel.PDF == "" {
		c.FailureLevel.PDF = "WARN"
	}
	if c.FailureLevel.EPUB == "" {
		c.FailureLevel.EPUB = "ERROR"
	}
	if c.FailureLevel.Check == "" {
		c.FailureLevel.Check = "WARN"
	}
	for i := range c.Books {
		if c.Books[i].Out == "" {
			base := filepath.Base(c.Books[i].Src)
			c.Books[i].Out = strings.TrimSuffix(base, filepath.Ext(base))
		}
	}
}

func (c *Config) validate() error {
	if len(c.Books) == 0 {
		return fmt.Errorf("config has no books")
	}
	for _, b := range c.Books {
		if b.Src == "" {
			return fmt.Errorf("book entry missing src")
		}
	}
	return nil
}

// Path resolves p against the config directory when p is relative.
func (c *Config) Path(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Dir, p)
}

// ThemeDirName splits the theme path into the (dir, name) pair asciidoctor-pdf
// expects: pdf-themesdir=<dir> and pdf-theme=<name>, where the theme file is
// <dir>/<name>-theme.yml. Returns ok=false when no theme is configured.
//
//	docs/pdf-theme/ai-sdlc-theme.yml -> ("docs/pdf-theme", "ai-sdlc", true)
func (c *Config) ThemeDirName() (dir, name string, ok bool) {
	if c.Theme == "" {
		return "", "", false
	}
	p := c.Path(c.Theme)
	dir = filepath.Dir(p)
	base := filepath.Base(p)
	name = strings.TrimSuffix(base, ".yml")
	name = strings.TrimSuffix(name, "-theme")
	return dir, name, true
}
