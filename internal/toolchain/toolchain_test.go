package toolchain

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// shimBin writes dir/<name> as an executable POSIX shell script.
func shimBin(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// isolatedPath replaces PATH with an empty temp dir for the duration of the
// test and returns it, so lookups only see shims the test installs.
func isolatedPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell shims are not portable to windows")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	return bin
}

// shimSetupPrereqs shims the tools setup's own preflight (SetupNeeds) needs
// but that Setup itself never installs — ruby, gem and node — in bin. Tests
// exercising Setup's own installers (bundler, npm) shim those separately, so
// they can make one of them fail without the preflight masking it.
func shimSetupPrereqs(t *testing.T, bin string) {
	t.Helper()
	for _, name := range []string{"ruby", "gem", "node"} {
		shimBin(t, bin, name, "echo '"+name+" 1.0.0'")
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

func TestRequiredToolsAreWellFormed(t *testing.T) {
	if len(Required) == 0 {
		t.Fatal("Required is empty")
	}
	seen := map[string]bool{}
	providers := map[string]Provider{}
	for _, tool := range Required {
		if tool.Name == "" || tool.Bin == "" || tool.Hint == "" {
			t.Errorf("tool %+v has an empty Name, Bin or Hint", tool)
		}
		if len(tool.VersionArgs) == 0 {
			t.Errorf("tool %q has no VersionArgs", tool.Name)
		}
		if seen[tool.Bin] {
			t.Errorf("duplicate Bin %q in Required", tool.Bin)
		}
		seen[tool.Bin] = true
		providers[tool.Bin] = tool.Provider
	}
	for _, bin := range []string{"ruby", "bundle", "node", "mmdc"} {
		if !seen[bin] {
			t.Errorf("Required is missing %q", bin)
		}
	}
	// D2's boundary made explicit: bundler and mermaid-cli are language-level
	// installs setup owns; ruby and node stay the environment's job.
	for _, bin := range []string{"bundle", "mmdc"} {
		if providers[bin] != Snowball {
			t.Errorf("%q Provider = %v, want Snowball", bin, providers[bin])
		}
	}
	for _, bin := range []string{"ruby", "node"} {
		if providers[bin] != Environment {
			t.Errorf("%q Provider = %v, want Environment", bin, providers[bin])
		}
	}
}

func TestSetupNeedsAreWellFormed(t *testing.T) {
	if len(SetupNeeds) == 0 {
		t.Fatal("SetupNeeds is empty")
	}
	seen := map[string]bool{}
	for _, tool := range SetupNeeds {
		if tool.Name == "" || tool.Bin == "" || tool.Hint == "" {
			t.Errorf("tool %+v has an empty Name, Bin or Hint", tool)
		}
		if tool.Provider != Environment {
			t.Errorf("%q Provider = %v, want Environment — setup must not need anything it provisions itself", tool.Bin, tool.Provider)
		}
		seen[tool.Bin] = true
	}
	for _, bin := range []string{"ruby", "gem", "node", "npm"} {
		if !seen[bin] {
			t.Errorf("SetupNeeds is missing %q", bin)
		}
	}
}

func TestProbeVersion(t *testing.T) {
	bin := isolatedPath(t)

	t.Run("returns only the first line", func(t *testing.T) {
		p := shimBin(t, bin, "multi", "echo 'ruby 3.4.1 (2025-01-01)'; echo 'second line'")
		if got := probeVersion(p, []string{"--version"}); got != "ruby 3.4.1 (2025-01-01)" {
			t.Errorf("probeVersion = %q, want the first line only", got)
		}
	})

	t.Run("trims surrounding whitespace", func(t *testing.T) {
		p := shimBin(t, bin, "padded", "printf '\\n  v1.0.0  \\n\\n'")
		if got := probeVersion(p, []string{"--version"}); got != "v1.0.0" {
			t.Errorf("probeVersion = %q, want v1.0.0", got)
		}
	})

	t.Run("includes stderr output", func(t *testing.T) {
		p := shimBin(t, bin, "onstderr", "echo 'v1.2.3' >&2")
		if got := probeVersion(p, []string{"--version"}); got != "v1.2.3" {
			t.Errorf("probeVersion = %q, want v1.2.3", got)
		}
	})

	t.Run("passes the version args through", func(t *testing.T) {
		p := shimBin(t, bin, "echoargs", `echo "$@"`)
		if got := probeVersion(p, []string{"-v", "--long"}); got != "-v --long" {
			t.Errorf("probeVersion = %q, want the args echoed back", got)
		}
	})

	t.Run("reports ? on a non-zero exit", func(t *testing.T) {
		p := shimBin(t, bin, "failing", "echo 'boom' >&2; exit 1")
		if got := probeVersion(p, []string{"--version"}); got != "?" {
			t.Errorf("probeVersion = %q, want ?", got)
		}
	})

	t.Run("reports ? when the binary does not exist", func(t *testing.T) {
		if got := probeVersion(filepath.Join(bin, "nope"), []string{"--version"}); got != "?" {
			t.Errorf("probeVersion = %q, want ?", got)
		}
	})

	t.Run("trims trailing whitespace on the first line", func(t *testing.T) {
		// Trimming the whole output before splitting left this padding behind,
		// because the trim only ever reached the end of the *last* line.
		p := shimBin(t, bin, "paddedfirst", "printf 'v1.0  \\nmore\\n'")
		if got := probeVersion(p, []string{"--version"}); got != "v1.0" {
			t.Errorf("probeVersion = %q, want v1.0 with no trailing spaces", got)
		}
	})
}

func TestDoctorAllToolsPresent(t *testing.T) {
	bin := isolatedPath(t)
	for _, tool := range Required {
		shimBin(t, bin, tool.Bin, "echo '"+tool.Bin+" 1.0.0'")
	}

	reports, ok := Doctor()
	if !ok {
		t.Fatal("Doctor reported not ok with every tool shimmed")
	}
	if len(reports) != len(Required) {
		t.Fatalf("got %d reports, want %d", len(reports), len(Required))
	}
	for i, r := range reports {
		if r.Tool.Bin != Required[i].Bin {
			t.Errorf("report[%d] is for %q, want %q", i, r.Tool.Bin, Required[i].Bin)
		}
		if !r.Found {
			t.Errorf("%s reported as missing", r.Tool.Name)
		}
		if r.Version != r.Tool.Bin+" 1.0.0" {
			t.Errorf("%s version = %q", r.Tool.Name, r.Version)
		}
	}
}

func TestDoctorAllToolsMissing(t *testing.T) {
	isolatedPath(t)

	reports, ok := Doctor()
	if ok {
		t.Fatal("Doctor reported ok with an empty PATH")
	}
	for _, r := range reports {
		if r.Found {
			t.Errorf("%s reported as found on an empty PATH", r.Tool.Name)
		}
		if r.Version != "" {
			t.Errorf("%s has version %q, want empty when missing", r.Tool.Name, r.Version)
		}
	}
}

func TestDoctorPartialToolchain(t *testing.T) {
	bin := isolatedPath(t)
	shimBin(t, bin, "ruby", "echo 'ruby 3.4.1'")

	reports, ok := Doctor()
	if ok {
		t.Fatal("Doctor reported ok with only one tool installed")
	}
	var found int
	for _, r := range reports {
		if r.Found {
			found++
			if r.Tool.Bin != "ruby" {
				t.Errorf("unexpected tool found: %q", r.Tool.Bin)
			}
		}
	}
	if found != 1 {
		t.Errorf("%d tools found, want 1", found)
	}
}

func TestPrintReport(t *testing.T) {
	reports := []Report{
		{Tool: Tool{Name: "Ruby", Bin: "ruby", Hint: "brew install ruby"}, Found: true, Version: "ruby 3.4.1"},
		{Tool: Tool{Name: "mermaid-cli", Bin: "mmdc", Hint: "run snowball setup"}, Found: false},
	}
	var buf bytes.Buffer
	PrintReport(&buf, reports)
	out := buf.String()

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("printed %d lines, want 2:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "ok") || !strings.Contains(lines[0], "Ruby") || !strings.Contains(lines[0], "ruby 3.4.1") {
		t.Errorf("found line = %q, want ok/name/version", lines[0])
	}
	if !strings.Contains(lines[1], "MISS") || !strings.Contains(lines[1], "run snowball setup") {
		t.Errorf("missing line = %q, want MISS and the install hint", lines[1])
	}
}

func TestPrintReportEmpty(t *testing.T) {
	var buf bytes.Buffer
	PrintReport(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("PrintReport(nil) printed %q, want nothing", buf.String())
	}
}

func TestCacheDirIsCreatedUnderTheUserCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	base, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache dir on this platform: %v", err)
	}

	dir, err := cacheDir()
	if err != nil {
		t.Fatalf("cacheDir: %v", err)
	}
	want := filepath.Join(base, "snowball", "toolchain")
	if dir != want {
		t.Errorf("cacheDir = %q, want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("cacheDir did not create the directory: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", dir)
	}
}

func TestCacheDirIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	first, err := cacheDir()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(first, "marker")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := cacheDir()
	if err != nil {
		t.Fatalf("second cacheDir call failed: %v", err)
	}
	if second != first {
		t.Errorf("cacheDir returned %q then %q", first, second)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("existing cache contents were clobbered: %v", err)
	}
}

func TestBundleDirMatchesCacheDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	want, err := cacheDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := BundleDir()
	if err != nil {
		t.Fatalf("BundleDir: %v", err)
	}
	if got != want {
		t.Errorf("BundleDir = %q, want %q", got, want)
	}
}

func TestSetupWritesGemfileAndRunsInstallers(t *testing.T) {
	bin := isolatedPath(t)
	shimSetupPrereqs(t, bin)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	log := filepath.Join(t.TempDir(), "calls.log")
	shimBin(t, bin, "bundle", `echo "bundle $* (pwd=$(pwd))" >> `+log)
	shimBin(t, bin, "npm", `echo "npm $*" >> `+log)

	if err := captureStdoutErr(t, Setup); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	dir, err := BundleDir()
	if err != nil {
		t.Fatal(err)
	}
	gemfile := filepath.Join(dir, "Gemfile")
	body, err := os.ReadFile(gemfile)
	if err != nil {
		t.Fatalf("Setup did not write the embedded Gemfile: %v", err)
	}
	if !strings.Contains(string(body), "asciidoctor-pdf") {
		t.Errorf("Gemfile does not look like the embedded one:\n%s", body)
	}

	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("no installer was invoked: %v", err)
	}
	calls := string(raw)
	for _, want := range []string{
		"bundle install",
		"bundle lock --add-platform x86_64-linux",
		"npm install -g @mermaid-js/mermaid-cli",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("Setup did not run %q\ngot:\n%s", want, calls)
		}
	}
	// $(pwd) inside the shim's shell prints the physical (symlink-resolved)
	// path, which differs from dir's logical form wherever a parent component
	// is a symlink (e.g. macOS's /var -> /private/var); resolve dir the same
	// way before comparing so this holds on every platform, not just Linux CI.
	wantDir := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		wantDir = resolved
	}
	if !strings.Contains(calls, "pwd="+wantDir) {
		t.Errorf("installers should run in the cache dir %q\ngot:\n%s", wantDir, calls)
	}
}

func TestSetupFailsWhenBundleInstallFails(t *testing.T) {
	bin := isolatedPath(t)
	shimSetupPrereqs(t, bin)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	shimBin(t, bin, "bundle", "exit 1")
	shimBin(t, bin, "npm", "exit 0")

	err := captureStdoutErr(t, Setup)
	if err == nil {
		t.Fatal("expected Setup to fail when bundle install fails")
	}
	if !strings.Contains(err.Error(), "bundle install") {
		t.Errorf("error = %q, want it to name the failing step", err)
	}
}

func TestSetupFailsWhenNpmFails(t *testing.T) {
	bin := isolatedPath(t)
	shimSetupPrereqs(t, bin)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	shimBin(t, bin, "bundle", "exit 0")
	shimBin(t, bin, "npm", "exit 1")

	err := captureStdoutErr(t, Setup)
	if err == nil {
		t.Fatal("expected Setup to fail when npm install fails")
	}
	if !strings.Contains(err.Error(), "mermaid-cli") {
		t.Errorf("error = %q, want it to name the failing step", err)
	}
	if !strings.Contains(err.Error(), "snowball doctor") {
		t.Errorf("error = %q, want it to point at `snowball doctor` for environment-level causes", err)
	}
}

// TestSetupFailsWithNamedHintNotRawExecError is the ticket's headline
// regression: a missing prerequisite must fail with the tool's name and hint,
// never a wrapped "executable file not found in $PATH" from a bare exec.
func TestSetupFailsWithNamedHintNotRawExecError(t *testing.T) {
	bin := isolatedPath(t)
	// Everything Setup itself would exec is present; only a SetupNeeds
	// prerequisite (ruby) is missing, so the preflight must be what fails it.
	for _, name := range []string{"gem", "node", "npm", "bundle"} {
		shimBin(t, bin, name, "echo '"+name+" 1.0.0'")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	err := captureStdoutErr(t, Setup)
	if err == nil {
		t.Fatal("expected Setup to fail when ruby is missing")
	}
	if !strings.Contains(err.Error(), "Ruby") {
		t.Errorf("error = %q, want it to name Ruby", err)
	}
	if !strings.Contains(err.Error(), "brew install ruby") {
		t.Errorf("error = %q, want Ruby's install hint", err)
	}
	if strings.Contains(err.Error(), "executable file not found") {
		t.Errorf("error = %q, must not leak the raw exec error this ticket replaces", err)
	}
}

// TestSetupBootstrapsBundlerIntoTheGemHome exercises the case this ticket
// exists for: bundle is missing from PATH entirely, so Setup must install it
// itself, with GEM_HOME pointed at snowball's own cache, and then run `bundle
// install` from the binstub it just installed — not from a bare "bundle" that
// resolves to nothing.
func TestSetupBootstrapsBundlerIntoTheGemHome(t *testing.T) {
	// Unlike every other shim in this file, the gem shim below has to actually
	// vendor an executable binstub. It reaches mkdir/chmod by absolute path
	// rather than needing them on PATH, because PATH here stays exclusively the
	// shim dir — macOS ships a real bundle/gem at /usr/bin, so widening PATH to
	// reach coreutils would silently defeat the isolation this test depends on.
	bin := isolatedPath(t)
	shimSetupPrereqs(t, bin)
	shimBin(t, bin, "npm", "exit 0")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	gemHome, err := GemDir()
	if err != nil {
		t.Fatal(err)
	}

	log := filepath.Join(t.TempDir(), "gem.log")
	// The gem shim logs its argv and $GEM_HOME, then vendors an executable
	// bundle binstub into $GEM_HOME/bin that logs its own invocations too —
	// standing in for what `gem install bundler` really does.
	bundleLog := filepath.Join(t.TempDir(), "bundle.log")
	shimBin(t, bin, "gem", `echo "gem $* (GEM_HOME=$GEM_HOME)" >> `+log+`
`+
		`/bin/mkdir -p "$GEM_HOME/bin"
`+
		`/bin/cat > "$GEM_HOME/bin/bundle" <<'EOF'
#!/bin/sh
echo "bundle $*" >> `+bundleLog+`
EOF
`+
		`/bin/chmod +x "$GEM_HOME/bin/bundle"`)

	if err := captureStdoutErr(t, Setup); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	gemCalls, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("gem was never invoked: %v", err)
	}
	if !strings.Contains(string(gemCalls), "gem install --no-document bundler") {
		t.Errorf("Setup did not bootstrap bundler:\n%s", gemCalls)
	}
	if !strings.Contains(string(gemCalls), "GEM_HOME="+gemHome) {
		t.Errorf("gem install did not run with GEM_HOME=%s:\n%s", gemHome, gemCalls)
	}

	bundleCalls, err := os.ReadFile(bundleLog)
	if err != nil {
		t.Fatalf("the vendored bundle binstub was never invoked: %v", err)
	}
	if !strings.Contains(string(bundleCalls), "bundle install") {
		t.Errorf("Setup did not run bundle install from the vendored binstub:\n%s", bundleCalls)
	}
}

// TestSetupIsIdempotentAboutBundler asserts a second (or first, on a machine
// that already has bundler) Setup does not re-bootstrap it.
func TestSetupIsIdempotentAboutBundler(t *testing.T) {
	bin := isolatedPath(t)
	shimSetupPrereqs(t, bin)
	shimBin(t, bin, "npm", "exit 0")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	gemCalls := filepath.Join(t.TempDir(), "gem.log")
	shimBin(t, bin, "gem", `echo "gem $*" >> `+gemCalls)
	shimBin(t, bin, "bundle", "exit 0") // already on PATH

	if err := captureStdoutErr(t, Setup); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if _, err := os.ReadFile(gemCalls); err == nil {
		t.Error("Setup ran gem install bundler even though bundle was already on PATH")
	}
}

// TestLookPathFindsAVendoredBundler guards the exact contradiction this
// ticket describes: doctor reporting MISS right after a successful setup.
// With bundle present only in snowball's own gem-home bin dir (never on
// PATH), LookPath — and therefore Doctor — must still find it.
func TestLookPathFindsAVendoredBundler(t *testing.T) {
	isolatedPath(t) // bundle is nowhere on PATH
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	gemHome, err := GemDir()
	if err != nil {
		t.Fatal(err)
	}
	vendoredBinDir := filepath.Join(gemHome, "bin")
	if err := os.MkdirAll(vendoredBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shimBin(t, vendoredBinDir, "bundle", "echo 'bundle 2.5.0'")

	path, err := LookPath("bundle")
	if err != nil {
		t.Fatalf("LookPath(bundle) = %v, want it to find the vendored binstub", err)
	}
	if path != filepath.Join(vendoredBinDir, "bundle") {
		t.Errorf("LookPath(bundle) = %q, want the vendored path", path)
	}

	// The other three Required tools are not shimmed on this isolated PATH, so
	// Doctor's overall ok is expected to be false here; only Bundler's own
	// report matters for this test.
	reports, _ := Doctor()
	var bundlerReport Report
	for _, r := range reports {
		if r.Tool.Bin == "bundle" {
			bundlerReport = r
		}
	}
	if !bundlerReport.Found {
		t.Error("Doctor reported Bundler as MISS right after it was vendored — the exact setup/doctor contradiction this ticket fixes")
	}
}

// captureStdoutErr runs fn with stdout discarded and returns its error.
func captureStdoutErr(t *testing.T, fn func() error) error {
	t.Helper()
	var err error
	captureStdout(t, func() { err = fn() })
	return err
}
