package toolchain

import (
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
	}
	for _, bin := range []string{"ruby", "bundle", "node", "mmdc"} {
		if !seen[bin] {
			t.Errorf("Required is missing %q", bin)
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
	out := captureStdout(t, func() { PrintReport(reports) })

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
	if out := captureStdout(t, func() { PrintReport(nil) }); out != "" {
		t.Errorf("PrintReport(nil) printed %q, want nothing", out)
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
	if !strings.Contains(calls, "pwd="+dir) {
		t.Errorf("installers should run in the cache dir %q\ngot:\n%s", dir, calls)
	}
}

func TestSetupFailsWhenBundleInstallFails(t *testing.T) {
	bin := isolatedPath(t)
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
}

// captureStdoutErr runs fn with stdout discarded and returns its error.
func captureStdoutErr(t *testing.T, fn func() error) error {
	t.Helper()
	var err error
	captureStdout(t, func() { err = fn() })
	return err
}
