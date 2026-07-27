// Package toolchain verifies (doctor) and installs (setup) the native rendering
// toolchain snowball drives: the asciidoctor gem set, mermaid-cli, and the
// Puppeteer-managed Chrome. It owns language-level deps only — OS packages (Node,
// Chrome shared libraries) are the environment's responsibility and are reported,
// never installed.
package toolchain

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/codcod/snowball/assets"
)

// Tool is a single required executable and how to probe its version.
type Tool struct {
	Name        string   // display name
	Bin         string   // executable to look up on PATH
	VersionArgs []string // args that print a version
	Hint        string   // how to install when missing
}

// Required is the toolchain snowball needs on PATH to render.
var Required = []Tool{
	{"Ruby", "ruby", []string{"--version"}, "install ruby >= 3 (brew install ruby)"},
	{"Bundler", "bundle", []string{"--version"}, "gem install bundler, then `snowball setup`"},
	{"Node.js", "node", []string{"--version"}, "install node >= 22 (brew install node)"},
	{"mermaid-cli", "mmdc", []string{"--version"}, "`snowball setup` (npm i -g @mermaid-js/mermaid-cli)"},
}

// Report is the result of a single tool probe.
type Report struct {
	Tool    Tool
	Found   bool
	Version string
}

// Doctor probes every required tool. Returns the reports and whether all passed.
func Doctor() (reports []Report, ok bool) {
	ok = true
	for _, t := range Required {
		r := Report{Tool: t}
		if path, err := exec.LookPath(t.Bin); err == nil {
			r.Found = true
			r.Version = probeVersion(path, t.VersionArgs)
		} else {
			ok = false
		}
		reports = append(reports, r)
	}
	return reports, ok
}

// PrintReport writes a human-readable doctor summary to w.
func PrintReport(w io.Writer, reports []Report) {
	for _, r := range reports {
		if r.Found {
			fmt.Fprintf(w, "  ok   %-12s %s\n", r.Tool.Name, r.Version)
		} else {
			fmt.Fprintf(w, "  MISS %-12s — %s\n", r.Tool.Name, r.Tool.Hint)
		}
	}
}

func probeVersion(path string, args []string) string {
	out, err := exec.Command(path, args...).CombinedOutput()
	if err != nil {
		return "?"
	}
	// Trim, split, trim again. The leading trim discards blank lines before the
	// version; the trailing one reaches whitespace at the end of the first line,
	// which trimming the whole output cannot ("v1.0  \nmore" -> "v1.0").
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

// cacheDir is where the embedded Gemfile is materialized and gems are installed.
func cacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "snowball", "toolchain")
	return dir, os.MkdirAll(dir, 0o755)
}

// Setup installs the pinned language-level toolchain: bundle install the embedded
// Gemfile into the snowball cache, install mermaid-cli globally, and let
// Puppeteer download its Chrome. Node and OS libraries must already be present.
func Setup() error {
	dir, err := cacheDir()
	if err != nil {
		return err
	}
	gemfile := filepath.Join(dir, "Gemfile")
	if err := os.WriteFile(gemfile, []byte(assets.Gemfile), 0o644); err != nil {
		return fmt.Errorf("write embedded Gemfile: %w", err)
	}
	fmt.Printf("snowball: installing gems into %s\n", dir)
	if err := run(dir, "bundle", "install"); err != nil {
		return fmt.Errorf("bundle install: %w", err)
	}
	// Add the linux platform so a Gemfile.lock generated on macOS still resolves
	// in CI (best-effort; ignored if bundler predates the flag).
	_ = run(dir, "bundle", "lock", "--add-platform", "x86_64-linux")

	fmt.Println("snowball: installing mermaid-cli")
	if err := run(dir, "npm", "install", "-g", "@mermaid-js/mermaid-cli"); err != nil {
		return fmt.Errorf("npm install mermaid-cli: %w", err)
	}
	fmt.Println("snowball: setup complete — run `snowball doctor` to verify")
	return nil
}

// BundleDir returns the cache directory holding the installed Gemfile, so the
// renderer can point `bundle exec` at the snowball-owned gem set via BUNDLE_GEMFILE.
func BundleDir() (string, error) { return cacheDir() }

func run(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
