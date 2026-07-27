package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/codcod/snowball/internal/config"
)

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

// runCmd executes a single command with args, capturing its cobra output.
func runCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// withArgs sets os.Args for the duration of the test; Execute reads it via cobra.
func withArgs(t *testing.T, args ...string) {
	t.Helper()
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })
	os.Args = args
}

// emptyPath points PATH at an empty dir so requireToolchain always fails,
// letting tests assert command wiring without a real toolchain installed.
func emptyPath(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("PATH isolation via shell shims is not portable to windows")
	}
	t.Setenv("PATH", t.TempDir())
}

// shimBin writes dir/<name> as an executable POSIX shell script.
func shimBin(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeToolchain replaces PATH with a dir holding stubs for every tool doctor
// requires, so commands get past requireToolchain without a real install.
// It returns the bin dir so callers can override individual stubs.
func fakeToolchain(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("PATH isolation via shell shims is not portable to windows")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for _, name := range []string{"ruby", "bundle", "node", "mmdc", "npm"} {
		shimBin(t, bin, name, "echo '"+name+" 1.0.0'")
	}
	return bin
}

func TestStarterConfigIsValid(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, config.DefaultFile)
	if err := os.WriteFile(p, []byte(starterConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("the starter config does not load: %v", err)
	}
	if len(cfg.Books) != 2 {
		t.Fatalf("starter config has %d books, want 2", len(cfg.Books))
	}
	if cfg.Books[0].Out != "users-manual" || cfg.Books[1].Out != "developers-handbook" {
		t.Errorf("unexpected book outputs: %+v", cfg.Books)
	}
	if len(cfg.Formats) != 2 {
		t.Errorf("Formats = %v, want [pdf epub]", cfg.Formats)
	}
	dirName, name, ok := cfg.ThemeDirName()
	if !ok || name != "ai-sdlc" || filepath.Base(dirName) != "pdf-theme" {
		t.Errorf("theme resolved to (%q, %q, %v)", dirName, name, ok)
	}
	if cfg.FailureLevel.PDF != "WARN" || cfg.FailureLevel.EPUB != "ERROR" || cfg.FailureLevel.Check != "WARN" {
		t.Errorf("failure levels = %+v", cfg.FailureLevel)
	}
	if cfg.Mermaid.Format != "png" || len(cfg.Mermaid.PuppeteerArgs) != 3 {
		t.Errorf("mermaid config = %+v", cfg.Mermaid)
	}
}

func TestInitWritesStarterConfig(t *testing.T) {
	t.Chdir(t.TempDir())

	out := captureStdout(t, func() {
		if _, err := runCmd(t, initCmd()); err != nil {
			t.Fatalf("init: %v", err)
		}
	})
	if !strings.Contains(out, config.DefaultFile) {
		t.Errorf("init output = %q, want it to name the written file", out)
	}

	body, err := os.ReadFile(config.DefaultFile)
	if err != nil {
		t.Fatalf("init did not write %s: %v", config.DefaultFile, err)
	}
	if string(body) != starterConfig {
		t.Error("written file does not match the starter config")
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile(config.DefaultFile, []byte("books: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runCmd(t, initCmd())
	if err == nil {
		t.Fatal("expected init to refuse to overwrite an existing config")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error = %q, want it to mention --force", err)
	}

	body, _ := os.ReadFile(config.DefaultFile)
	if string(body) != "books: []\n" {
		t.Error("init clobbered the existing config despite erroring")
	}
}

func TestInitForceOverwrites(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile(config.DefaultFile, []byte("books: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	captureStdout(t, func() {
		if _, err := runCmd(t, initCmd(), "--force"); err != nil {
			t.Fatalf("init --force: %v", err)
		}
	})

	body, err := os.ReadFile(config.DefaultFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != starterConfig {
		t.Error("init --force did not overwrite the existing config")
	}
}

func TestVersionCommandPrintsVersion(t *testing.T) {
	out := captureStdout(t, func() {
		if _, err := runCmd(t, versionCmd("v1.2.3")); err != nil {
			t.Fatalf("version: %v", err)
		}
	})
	if strings.TrimSpace(out) != "v1.2.3" {
		t.Errorf("version output = %q, want v1.2.3", out)
	}
}

func TestBuildCommandFlags(t *testing.T) {
	cmd := buildCmd(&globals{})
	for _, name := range []string{"pdf", "epub", "out", "rev", "date", "book"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("build is missing the --%s flag", name)
		}
	}
	if f := cmd.Flags().ShorthandLookup("o"); f == nil || f.Name != "out" {
		t.Error("build --out should have the -o shorthand")
	}
	if f := cmd.Flags().Lookup("book"); f != nil && f.Value.Type() != "stringArray" {
		t.Errorf("--book is %s, want a repeatable stringArray", f.Value.Type())
	}
}

func TestRootPersistentFlags(t *testing.T) {
	root, g := newRoot("v0.0.0")
	for name, short := range map[string]string{"config": "c", "quiet": "q", "verbose": ""} {
		f := root.PersistentFlags().Lookup(name)
		if f == nil {
			t.Fatalf("root is missing the --%s flag", name)
		}
		if f.Shorthand != short {
			t.Errorf("--%s shorthand = %q, want %q", name, f.Shorthand, short)
		}
	}
	if err := root.PersistentFlags().Parse([]string{"--quiet", "--verbose", "-c", "x.yaml"}); err != nil {
		t.Fatal(err)
	}
	if !g.quiet || !g.verbose || g.configPath != "x.yaml" {
		t.Errorf("parsed globals = %+v, want quiet/verbose set and configPath x.yaml", g)
	}
}

func TestShorthandVIsVersionNotVerbose(t *testing.T) {
	// cobra assigns -v to --version only when the shorthand is free. 0.1.x
	// shipped `snowball -v` printing the version, so claiming -v for --verbose
	// would silently turn it into help output while still exiting 0.
	root, _ := newRoot("v1.2.3")
	root.SetArgs([]string{"-v"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("snowball -v returned %v, want it to print the version", err)
	}
	if !strings.Contains(out.String(), "v1.2.3") {
		t.Errorf("snowball -v printed %q, want the version", out.String())
	}
	if f := root.PersistentFlags().Lookup("verbose"); f != nil && f.Shorthand != "" {
		t.Errorf("--verbose has shorthand %q; -v belongs to --version", f.Shorthand)
	}
}

func TestNewRootTreesAreIndependent(t *testing.T) {
	// The flag state used to live at package scope, so building a second
	// command tree clobbered the first.
	_, a := newRoot("v0.0.0")
	rootB, b := newRoot("v0.0.0")
	if err := rootB.PersistentFlags().Parse([]string{"-c", "b.yaml"}); err != nil {
		t.Fatal(err)
	}
	if a.configPath != "" {
		t.Errorf("first tree's configPath = %q, want it untouched by the second", a.configPath)
	}
	if b.configPath != "b.yaml" {
		t.Errorf("second tree's configPath = %q, want b.yaml", b.configPath)
	}
}

func TestCheckCommandFlags(t *testing.T) {
	cmd := checkCmd(&globals{})
	if cmd.Flags().Lookup("book") == nil {
		t.Error("check is missing the --book flag")
	}
	if cmd.Flags().Lookup("pdf") != nil {
		t.Error("check should not expose format flags")
	}
}

func TestBuildFailsOnMissingConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runCmd(t, buildCmd(&globals{}))
	if err == nil {
		t.Fatal("expected build to fail with no snowball.yaml present")
	}
	if !strings.Contains(err.Error(), config.DefaultFile) {
		t.Errorf("error = %q, want it to name the missing config", err)
	}
}

func TestBuildHonoursConfigPath(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "custom.yaml")
	if err := os.WriteFile(custom, []byte("books: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	_, err := runCmd(t, buildCmd(&globals{configPath: custom}))
	if err == nil {
		t.Fatal("expected build to fail: the custom config declares no books")
	}
	if !strings.Contains(err.Error(), "no books") {
		t.Errorf("error = %q, want the validation error from the custom config", err)
	}
}

func TestBuildStopsAtToolchainCheck(t *testing.T) {
	emptyPath(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(config.DefaultFile, []byte("books:\n  - src: a.adoc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	captureStdout(t, func() { _, err = runCmd(t, buildCmd(&globals{})) })
	if err == nil {
		t.Fatal("expected build to fail when the toolchain is incomplete")
	}
	if !strings.Contains(err.Error(), "snowball setup") {
		t.Errorf("error = %q, want it to point at `snowball setup`", err)
	}
}

func TestCheckStopsAtToolchainCheck(t *testing.T) {
	emptyPath(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(config.DefaultFile, []byte("books:\n  - src: a.adoc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	captureStdout(t, func() { _, err = runCmd(t, checkCmd(&globals{})) })
	if err == nil {
		t.Fatal("expected check to fail when the toolchain is incomplete")
	}
	if !strings.Contains(err.Error(), "snowball setup") {
		t.Errorf("error = %q, want it to point at `snowball setup`", err)
	}
}

func TestDoctorReportsIncompleteToolchain(t *testing.T) {
	emptyPath(t)

	var err error
	out := captureStdout(t, func() { _, err = runCmd(t, doctorCmd()) })
	if err == nil {
		t.Fatal("expected doctor to fail on an empty PATH")
	}
	if !strings.Contains(out, "MISS") {
		t.Errorf("doctor output = %q, want it to list missing tools", out)
	}
	if strings.Contains(out, "toolchain ok") {
		t.Error("doctor reported ok despite an empty PATH")
	}
}

func TestDoctorReportsCompleteToolchain(t *testing.T) {
	fakeToolchain(t)

	var err error
	out := captureStdout(t, func() { _, err = runCmd(t, doctorCmd()) })
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "toolchain ok") {
		t.Errorf("doctor output = %q, want the ok summary", out)
	}
	if strings.Contains(out, "MISS") {
		t.Errorf("doctor reported a missing tool: %q", out)
	}
}

func TestRequireToolchainPassesWithEveryTool(t *testing.T) {
	fakeToolchain(t)

	var err error
	captureStdout(t, func() { err = requireToolchain() })
	if err != nil {
		t.Errorf("requireToolchain = %v, want nil", err)
	}
}

func TestSetupCommandRunsTheInstaller(t *testing.T) {
	bin := fakeToolchain(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	log := filepath.Join(t.TempDir(), "calls.log")
	shimBin(t, bin, "bundle", `echo "bundle $*" >> `+log)
	shimBin(t, bin, "npm", `echo "npm $*" >> `+log)

	var err error
	captureStdout(t, func() { _, err = runCmd(t, setupCmd()) })
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	raw, readErr := os.ReadFile(log)
	if readErr != nil {
		t.Fatalf("setup invoked no installer: %v", readErr)
	}
	if !strings.Contains(string(raw), "bundle install") {
		t.Errorf("setup did not run bundle install:\n%s", raw)
	}
}

func TestBuildFormatFlagsMapToOptions(t *testing.T) {
	bin := fakeToolchain(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(config.DefaultFile, []byte("books:\n  - src: a.adoc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Log render invocations only; doctor's `bundle --version` probe must still
	// answer normally or requireToolchain would fail.
	log := filepath.Join(t.TempDir(), "bundle.log")
	shimBin(t, bin, "bundle", `case "$1" in exec) echo "$*" >> `+log+`;; *) echo 'bundle 1.0.0';; esac`)

	var err error
	captureStdout(t, func() { _, err = runCmd(t, buildCmd(&globals{}), "--pdf", "--rev", "v1.0.0", "--date", "today") })
	if err != nil {
		t.Fatalf("build --pdf: %v", err)
	}

	raw, readErr := os.ReadFile(log)
	if readErr != nil {
		t.Fatalf("build invoked no renderer: %v", readErr)
	}
	calls := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(calls) != 1 {
		t.Fatalf("--pdf produced %d renders, want 1:\n%s", len(calls), raw)
	}
	if !strings.Contains(calls[0], "asciidoctor-pdf") {
		t.Errorf("--pdf did not select the PDF renderer: %q", calls[0])
	}
	if !strings.Contains(calls[0], "revnumber=v1.0.0") || !strings.Contains(calls[0], "revdate=today") {
		t.Errorf("--rev/--date were not passed through: %q", calls[0])
	}
}

func TestBuildBookFilterIsPassedThrough(t *testing.T) {
	fakeToolchain(t)
	dir := t.TempDir()
	t.Chdir(dir)
	body := "books:\n  - src: a.adoc\n  - src: b.adoc\n"
	if err := os.WriteFile(config.DefaultFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	captureStdout(t, func() { _, err = runCmd(t, buildCmd(&globals{}), "--book", "nonexistent") })
	if err == nil || !strings.Contains(err.Error(), "no books matched") {
		t.Fatalf("build error = %v, want \"no books matched\"", err)
	}
}

func TestRequireToolchainFailsOnEmptyPath(t *testing.T) {
	emptyPath(t)

	var err error
	captureStdout(t, func() { err = requireToolchain() })
	if err == nil {
		t.Fatal("expected requireToolchain to fail on an empty PATH")
	}
}

func TestExecuteRegistersEveryCommand(t *testing.T) {
	t.Chdir(t.TempDir())

	out := captureStdout(t, func() {
		withArgs(t, "snowball", "--help")
		if err := Execute("v0.0.0-test"); err != nil {
			t.Fatalf("Execute --help: %v", err)
		}
	})
	for _, name := range []string{"build", "check", "doctor", "setup", "init", "version"} {
		if !strings.Contains(out, name) {
			t.Errorf("--help does not list the %q command\n%s", name, out)
		}
	}
	if !strings.Contains(out, "--config") {
		t.Error("--help does not list the persistent --config flag")
	}
}

func TestExecuteVersionFlag(t *testing.T) {
	t.Chdir(t.TempDir())

	out := captureStdout(t, func() {
		withArgs(t, "snowball", "--version")
		if err := Execute("v9.9.9"); err != nil {
			t.Fatalf("Execute --version: %v", err)
		}
	})
	if !strings.Contains(out, "v9.9.9") {
		t.Errorf("--version output = %q, want it to contain v9.9.9", out)
	}
}

func TestExecuteUnknownCommand(t *testing.T) {
	t.Chdir(t.TempDir())

	// Cobra prints the usage error itself; keep it out of the test log.
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	origErr := os.Stderr
	os.Stderr = devnull
	defer func() { os.Stderr = origErr }()

	captureStdout(t, func() {
		withArgs(t, "snowball", "nope")
		if err := Execute("v0.0.0-test"); err == nil {
			t.Error("expected an error for an unknown command")
		}
	})
}
