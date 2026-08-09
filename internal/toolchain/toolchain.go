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

// Provider says who is responsible for a Tool: the environment (the user
// installs it; doctor only reports it) or snowball (setup installs it itself).
// This is the D2 boundary made explicit — language-level installs are
// snowball's job, OS-level ones are the environment's.
type Provider int

const (
	Environment Provider = iota // the user's job; doctor reports, never installs
	Snowball                    // setup's job; doctor reports what setup already did
)

// Tool is a single required executable and how to probe its version.
type Tool struct {
	Name        string   // display name
	Bin         string   // executable to look up on PATH (or snowball's own bin dir)
	VersionArgs []string // args that print a version
	Hint        string   // how to get it: an install command, or `snowball setup`
	Provider    Provider // who installs it — the environment or setup itself
}

var (
	toolRuby    = Tool{"Ruby", "ruby", []string{"--version"}, "install ruby >= 3 (brew install ruby)", Environment}
	toolBundler = Tool{"Bundler", "bundle", []string{"--version"}, "`snowball setup`", Snowball}
	toolNode    = Tool{"Node.js", "node", []string{"--version"}, "install node >= 22 (brew install node)", Environment}
	toolMermaid = Tool{"mermaid-cli", "mmdc", []string{"--version"}, "`snowball setup` (npm i -g @mermaid-js/mermaid-cli)", Snowball}
	toolGem     = Tool{"RubyGems", "gem", []string{"--version"}, "ships with ruby (install ruby >= 3, brew install ruby)", Environment}
	toolNpm     = Tool{"npm", "npm", []string{"--version"}, "ships with node (install node >= 22, brew install node)", Environment}
)

// Required is the toolchain snowball needs on PATH to render. It decides
// doctor's pass/fail and is probed before every build (requireToolchain).
var Required = []Tool{toolRuby, toolBundler, toolNode, toolMermaid}

// SetupNeeds is what `setup` itself needs already present to do its job —
// language-level tooling that snowball does not (and, per D2, must not) try
// to install on the user's behalf. Probed only by setup's own preflight, so a
// missing npm (say) cannot fail `doctor` on a machine that can otherwise
// render fine.
var SetupNeeds = []Tool{toolRuby, toolGem, toolNode, toolNpm}

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
		if path, err := LookPath(t.Bin); err == nil {
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

// GemDir is the gem home snowball owns end to end: bundler (when vendored) and
// the pinned gem set both install here via GEM_HOME, so `setup` never writes
// outside its own cache and never needs sudo.
func GemDir() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	gems := filepath.Join(dir, "gems")
	return gems, os.MkdirAll(gems, 0o755)
}

// binDirs lists snowball's own executable directories, in lookup order.
// Best-effort: any error resolving them yields an empty list rather than a
// failure — a lookup that cannot find the cache dir should behave exactly as
// if snowball had never provisioned anything there.
func binDirs() []string {
	gems, err := GemDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(gems, "bin")}
}

// LookPath resolves a toolchain binary the same way everywhere it is needed
// (doctor, setup, and the renderer): PATH first, then snowball's own bin dirs.
// The joined candidate is still checked with exec.LookPath, not os.Stat, so
// PATHEXT resolution (e.g. bundle.bat on Windows) applies there too.
func LookPath(bin string) (string, error) {
	if path, err := exec.LookPath(bin); err == nil {
		return path, nil
	}
	var lastErr error
	for _, dir := range binDirs() {
		if path, err := exec.LookPath(filepath.Join(dir, bin)); err == nil {
			return path, nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return exec.LookPath(bin) // re-derive the original PATH error verbatim
}

// Preflight checks that every one of tools resolves via LookPath, and fails
// with each missing tool's name and hint — never a bare wrapped exec error.
func Preflight(tools []Tool) error {
	var missing []string
	for _, t := range tools {
		if _, err := LookPath(t.Bin); err != nil {
			missing = append(missing, fmt.Sprintf("%s (%s)", t.Name, t.Hint))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("setup prerequisites missing: %s", strings.Join(missing, "; "))
}

// Setup installs the pinned language-level toolchain: preflights the tools it
// needs already present, bootstraps bundler into snowball's own gem home when
// it is missing, bundle installs the embedded Gemfile there, installs
// mermaid-cli globally, and lets Puppeteer download its Chrome. Node, ruby and
// OS libraries must already be present — setup reports them, never installs.
func Setup() error {
	if err := Preflight(SetupNeeds); err != nil {
		return err
	}

	dir, err := cacheDir()
	if err != nil {
		return err
	}
	gemfile := filepath.Join(dir, "Gemfile")
	if err := os.WriteFile(gemfile, []byte(assets.Gemfile), 0o644); err != nil {
		return fmt.Errorf("write embedded Gemfile: %w", err)
	}

	gemHome, err := GemDir()
	if err != nil {
		return fmt.Errorf("prepare gem home: %w", err)
	}
	fmt.Printf("snowball: installing gems into %s\n", gemHome)

	bundle, err := ensureBundler(gemHome)
	if err != nil {
		return fmt.Errorf("bundler: %w", err)
	}

	env := setupEnv(gemHome)
	if err := runEnv(dir, env, bundle, "install"); err != nil {
		return fmt.Errorf("bundle install: %w", err)
	}
	// Add the linux platform so a Gemfile.lock generated on macOS still resolves
	// in CI (best-effort; ignored if bundler predates the flag).
	_ = runEnv(dir, env, bundle, "lock", "--add-platform", "x86_64-linux")

	fmt.Println("snowball: installing mermaid-cli")
	if err := run(dir, "npm", "install", "-g", "@mermaid-js/mermaid-cli"); err != nil {
		return fmt.Errorf("npm install mermaid-cli: mermaid-cli failed to install — check your "+
			"network/proxy and CA trust configuration (OS-level, see README § Toolchain "+
			"boundary), then `snowball doctor`: %w", err)
	}
	fmt.Println("snowball: setup complete — run `snowball doctor` to verify")
	return nil
}

// ensureBundler returns bundler's path, bootstrapping it into gemHome via `gem
// install` when it cannot be found on PATH or in snowball's own bin dir.
// Idempotent: a bundler already resolvable is returned as-is, with no gem
// install run.
func ensureBundler(gemHome string) (string, error) {
	if path, err := LookPath("bundle"); err == nil {
		return path, nil
	}
	fmt.Printf("snowball: installing bundler into %s\n", gemHome)
	if err := runEnv(gemHome, setupEnv(gemHome), "gem", "install", "--no-document", "bundler"); err != nil {
		return "", fmt.Errorf("gem install bundler: %w", err)
	}
	path, err := LookPath("bundle")
	if err != nil {
		return "", fmt.Errorf("bundler installed into %s but is still not found on PATH: %w", gemHome, err)
	}
	return path, nil
}

// setupEnv builds the environment setup's own child processes (gem, bundle)
// run with: GEM_HOME pinned to gemHome, GEM_PATH and PATH extended so a
// bundler that itself shells out (or bundler's own binstubs) can find things
// there too.
func setupEnv(gemHome string) []string {
	env := os.Environ()
	env = setEnvVar(env, "GEM_HOME", gemHome)
	env = prependEnvVar(env, "GEM_PATH", gemHome)
	env = prependEnvVar(env, "PATH", filepath.Join(gemHome, "bin"))
	return env
}

// setEnvVar sets key=value in env, overwriting any existing entry.
func setEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// prependEnvVar prepends value to key's existing list value (PATH-style,
// os.PathListSeparator-joined) in env, preserving whatever was already there,
// or sets key=value when it was absent.
func prependEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			existing := kv[len(prefix):]
			if existing == "" {
				env[i] = prefix + value
			} else {
				env[i] = prefix + value + string(os.PathListSeparator) + existing
			}
			return env
		}
	}
	return append(env, prefix+value)
}

// BundleDir returns the cache directory holding the installed Gemfile, so the
// renderer can point `bundle exec` at the snowball-owned gem set via BUNDLE_GEMFILE.
func BundleDir() (string, error) { return cacheDir() }

// run executes name in dir, inheriting the parent process's environment.
func run(dir, name string, args ...string) error {
	return runEnv(dir, nil, name, args...)
}

// runEnv executes name in dir with an explicit child environment. A nil env
// makes exec.Command inherit the parent's environment, same as run.
func runEnv(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
