// Package config loads and validates snowball.yaml, the per-repo declaration of
// which book masters to render, the theme, shared attributes, formats, and the
// mermaid/revision/failure-level knobs. Flags on the command line override it.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// Attributes are AsciiDoc attributes passed to every render as -a flags:
//
//	attributes:
//	  toc: left          # -a toc=left
//	  sectnums: ""       # -a sectnums        (set, no value)
//	  toc-title: false   # -a toc-title!      (explicitly unset)
type Attributes map[string]any

// managedAttributes are set by snowball itself from dedicated config keys.
// Letting them through would silently lose: snowball appends its own -a after
// the user's, and asciidoctor resolves duplicate -a flags last-one-wins.
var managedAttributes = map[string]string{
	"revnumber":                "revision (or --rev)",
	"revdate":                  "revision (or --date)",
	"mermaid-format":           "mermaid.format",
	"mermaid-puppeteer-config": "mermaid.puppeteer-args",
	"pdf-theme":                "theme",
	"pdf-themesdir":            "theme",
}

// UnmarshalYAML accepts a mapping and rejects the pre-0.2 scalar form with a
// migration hint. `attributes` used to be a path to a shared .adoc that nothing
// ever read; failing loudly beats silently ignoring it a second time.
func (a *Attributes) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		if node.Tag == "!!null" {
			return nil
		}
		return fmt.Errorf("`attributes` must be a map of AsciiDoc attributes, not a path.\n"+
			"It previously took a filename (%q) but was never passed to asciidoctor.\n"+
			"Replace it with the attributes themselves:\n\n"+
			"attributes:\n  toc: left\n  sectnums: \"\"\n\n"+
			"To keep including a shared file, use `include::%s[]` in your book master.",
			node.Value, node.Value)
	}
	var m map[string]any
	if err := node.Decode(&m); err != nil {
		return err
	}
	*a = m
	return nil
}

// Args renders the attributes as asciidoctor -a flags, sorted by key. Go
// randomises map iteration, so without the sort the invocation would differ
// between runs.
func (a Attributes) Args() []string {
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		args = append(args, "-a", formatAttribute(k, a[k]))
	}
	return args
}

func formatAttribute(key string, val any) string {
	switch v := val.(type) {
	case nil:
		return key
	case bool:
		if v {
			return key
		}
		return key + "!" // asciidoctor's "unset this attribute" syntax
	case string:
		if v == "" {
			return key
		}
		return key + "=" + v
	default:
		return fmt.Sprintf("%s=%v", key, v)
	}
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
	Attributes   Attributes   `yaml:"attributes"` // -a key=value passed to every render
	Formats      []string     `yaml:"formats"`    // default formats when none passed
	Revision     Revision     `yaml:"revision"`
	Mermaid      Mermaid      `yaml:"mermaid"`
	FailureLevel FailureLevel `yaml:"failure-level"`

	// Dir is the directory the config was loaded from; relative paths in the
	// config are interpreted against it.
	Dir string `yaml:"-"`
}

// Load reads the config at path, applies defaults, and validates it. When path
// is empty, DefaultFile is discovered by walking up from the working directory,
// so snowball works from anywhere inside the repo.
func Load(path string) (*Config, error) {
	if path == "" {
		found, err := discover()
		if err != nil {
			return nil, err
		}
		path = found
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

// discover walks up from the working directory looking for DefaultFile, the way
// git, cargo and npm locate their manifests, so that `cd docs/ && snowball
// build` behaves the same as running from the repo root.
func discover() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	start := dir
	for {
		candidate := filepath.Join(dir, DefaultFile)
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached the filesystem root
			return "", fmt.Errorf("no %s found in %s or any parent directory", DefaultFile, start)
		}
		dir = parent
	}
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
	keys := make([]string, 0, len(c.Attributes))
	for k := range c.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if owner, ok := managedAttributes[k]; ok {
			return fmt.Errorf("attributes.%s is set by snowball itself and would be ignored — use the `%s` setting instead", k, owner)
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
//	docs/pdf-theme/my-project-theme.yml -> ("docs/pdf-theme", "my-project", true)
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
