package scaffold

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/codcod/snowball/internal/config"
)

// Options configures a single `snowball scaffold` run.
type Options struct {
	// ProjectName substitutes projectNameToken in every template file and
	// names the scaffolded theme file. Defaults to filepath.Base(root).
	ProjectName string
	// Force overwrites files that already exist. Without it, an existing
	// file is left untouched and reported in Skipped.
	Force bool
	// DryRun reports what would happen and writes nothing at all — no
	// directories, no justfile append, no config.
	DryRun bool
	// NoWorkflow skips writing the GitHub release-attach workflow (docs-release.yml).
	NoWorkflow bool
	// NoReleaseWorkflow skips ci.yml, release.yml and .goreleaser.yaml as one
	// bundle. The guarantee this preserves is an end-state property, not a
	// per-run one: a goreleaser-check job in ci.yml never exists without a
	// .goreleaser.yaml for it to validate. A single run can still write ci.yml
	// while leaving a pre-existing .goreleaser.yaml untouched (exists-skip) —
	// the invariant holds because that file is already there, not because
	// this option enforces all-or-nothing within one invocation.
	NoReleaseWorkflow bool
	// Homebrew appends a brews: (homebrew tap) block to the scaffolded
	// .goreleaser.yaml. Opt-in, not opt-out: it assumes a homebrew-tap repo and
	// a HOMEBREW_TAP_GITHUB_TOKEN secret already exist, which a brand-new
	// adopter is unlikely to have yet. A no-op (with a note) if NoReleaseWorkflow
	// is also set, since there is then no .goreleaser.yaml to append to.
	Homebrew bool
}

// Result records what a run created, left in place, or wants the caller to
// know about, for the CLI summary.
type Result struct {
	Created []string
	Skipped []string
	// Notes carries longer, human-directed guidance (dry-run previews).
	Notes []string
}

func (r *Result) created(f string) { r.Created = append(r.Created, f) }
func (r *Result) skipped(f string) { r.Skipped = append(r.Skipped, f) }
func (r *Result) note(n string)    { r.Notes = append(r.Notes, n) }

// scaffoldFile pairs an embedded template asset with the path it is written
// to, relative to the target root.
type scaffoldFile struct {
	asset     string
	installed string
}

// Docs performs a scaffold run into root: the AsciiDoc docs skeleton, a
// matching PDF theme, a generated snowball.yaml (via StarterConfig), (unless
// NoWorkflow) a GitHub release-attach workflow, and (unless NoReleaseWorkflow)
// the ci.yml/release.yml workflows plus a .goreleaser.yaml — the last
// optionally carrying a homebrew tap block (Homebrew). Justfile recipes are
// appended separately (see appendJustfileRecipes) since they never create a
// justfile that does not already exist.
func Docs(root string, opts Options) (Result, error) {
	var res Result

	name := opts.ProjectName
	if name == "" {
		name = filepath.Base(root)
	}
	name = sanitizeProjectName(name)

	files := []scaffoldFile{
		{"templates/" + attributesPath, attributesPath},
		{"templates/" + bookMasterPath, bookMasterPath},
		{"templates/" + chapterPath, chapterPath},
		{"templates/docs/pdf-theme/theme.yml", themePath(name)},
	}
	if !opts.NoWorkflow {
		files = append(files, scaffoldFile{"templates/github/workflows/docs-release.yml", workflowPath})
	}
	if !opts.NoReleaseWorkflow {
		files = append(files,
			scaffoldFile{"templates/github/workflows/ci.yml", ciWorkflowPath},
			scaffoldFile{"templates/github/workflows/release.yml", releaseWorkflowPath},
		)
	}

	for _, f := range files {
		data, err := templatesFS.ReadFile(f.asset)
		if err != nil {
			return res, fmt.Errorf("read embedded %s: %w", f.asset, err)
		}
		data = substitute(data, name)
		if _, err := writeScaffoldFile(root, f.installed, data, opts, &res); err != nil {
			return res, err
		}
	}

	cfg := StarterConfig(name, true)
	if _, err := writeScaffoldFile(root, config.DefaultFile, cfg, opts, &res); err != nil {
		return res, err
	}

	if opts.NoReleaseWorkflow {
		if opts.Homebrew {
			res.note("--homebrew had nothing to attach to: --no-release-workflow skips .goreleaser.yaml")
		}
	} else {
		if err := writeGoreleaserConfig(root, name, opts, &res); err != nil {
			return res, err
		}
	}

	if err := appendJustfileRecipes(root, opts.DryRun, &res); err != nil {
		return res, err
	}

	return res, nil
}

// writeOutcome reports what writeScaffoldFile actually did, so a caller that
// cares — currently only writeGoreleaserConfig — can tell a real write from a
// dry-run preview of one, or from an existing file left untouched.
type writeOutcome int

const (
	// writeOutcomeSkipped means an existing file was left untouched (no
	// --force): nothing about this run's inputs reached it.
	writeOutcomeSkipped writeOutcome = iota
	// writeOutcomeDryRun means --dry-run reported what a create/overwrite
	// would do, without writing anything.
	writeOutcomeDryRun
	// writeOutcomeWritten means the file was actually created or overwritten
	// on disk this run.
	writeOutcomeWritten
)

// writeGoreleaserConfig assembles and writes the scaffolded .goreleaser.yaml:
// the base template, both tokens substituted, with the homebrew brews:
// fragment appended when opts.Homebrew is set. Kept out of the files table
// above because, unlike every other scaffoldFile, its content depends on an
// option rather than being a fixed 1:1 asset-to-path mapping. When
// detectGitHubOwner fails, or resolves an owner from a repository other than
// root itself, whether it notes that — and in what tense — depends on
// writeScaffoldFile's writeOutcome: see the switch below.
func writeGoreleaserConfig(root, name string, opts Options, res *Result) error {
	data, err := templatesFS.ReadFile("templates/goreleaser.yaml")
	if err != nil {
		return fmt.Errorf("read embedded templates/goreleaser.yaml: %w", err)
	}

	owner, ok := detectGitHubOwner(root)
	if !ok {
		owner = unknownGitHubOwner
	}

	// crossRepo is only meaningful once an owner was actually resolved: git's
	// own upward search (mirrored by gitToplevel) means the repository that
	// answered may not be root itself — e.g. root is a plain subdirectory of
	// an unrelated checkout. Both sides go through EvalSymlinks so a
	// symlinked temp dir (e.g. macOS's /tmp -> /private/tmp) never produces a
	// false positive; a resolution error is treated as "not cross-repo" —
	// best-effort, same spirit as the rest of this function.
	var crossRepo bool
	var toplevel string
	if ok {
		if top, topOK := gitToplevel(root); topOK {
			absRoot, errRoot := filepath.Abs(root)
			resolvedRoot, errResolveRoot := filepath.EvalSymlinks(absRoot)
			resolvedTop, errResolveTop := filepath.EvalSymlinks(top)
			if errRoot == nil && errResolveRoot == nil && errResolveTop == nil && resolvedRoot != resolvedTop {
				crossRepo = true
				toplevel = top
			}
		}
	}

	if opts.Homebrew {
		fragment, err := templatesFS.ReadFile("templates/goreleaser-brews.fragment.yaml")
		if err != nil {
			return fmt.Errorf("read embedded templates/goreleaser-brews.fragment.yaml: %w", err)
		}
		data = append(data, fragment...)
	}

	data = substitute(data, name)
	data = substituteToken(data, githubOwnerToken, owner)

	outcome, err := writeScaffoldFile(root, goreleaserConfigPath, data, opts, res)
	if err != nil {
		return err
	}

	if opts.Homebrew && outcome == writeOutcomeSkipped {
		res.note("--homebrew had nothing to attach to: " + goreleaserConfigPath +
			" already exists — pass --force to append the brews: block")
	}

	// Both the placeholder owner and a cross-repository owner are only worth
	// narrating against what this run actually did: a real write means the
	// file on disk now reads it; a dry-run preview of a create/overwrite
	// means it *would*, once run for real; a skipped existing file was never
	// examined, so nothing here says anything about its current,
	// possibly-already-correct, contents. unknownOwner (!ok) and crossRepo
	// are mutually exclusive, since crossRepo is only computed when ok.
	if !ok {
		switch outcome {
		case writeOutcomeWritten:
			res.note("could not determine a GitHub owner from `git remote get-url origin` — " +
				goreleaserConfigPath + "'s release.github.owner (and the homebrew tap owner, if " +
				"--homebrew) reads \"" + unknownGitHubOwner + "\"; fix it before the first tag")
		case writeOutcomeDryRun:
			res.note("could not determine a GitHub owner from `git remote get-url origin` — " +
				"a scaffolded " + goreleaserConfigPath + "'s release.github.owner (and the homebrew " +
				"tap owner, if --homebrew) would read \"" + unknownGitHubOwner +
				"\"; fix it before the first tag")
		case writeOutcomeSkipped:
			// nothing to say — the existing file's owner is untouched.
		}
		return nil
	}
	if crossRepo {
		switch outcome {
		case writeOutcomeWritten:
			res.note("owner \"" + owner + "\" resolved from the enclosing repository at " +
				toplevel + ", not the scaffold root " + root + " — verify it matches before " +
				"the first tag")
		case writeOutcomeDryRun:
			res.note("owner \"" + owner + "\" would resolve from the enclosing repository at " +
				toplevel + ", not the scaffold root " + root + " — verify it matches before " +
				"the first tag")
		case writeOutcomeSkipped:
			// nothing to say — the existing file's owner is untouched.
		}
	}
	return nil
}

// writeScaffoldFile writes data to root/installed, honouring Force, DryRun and
// the exists-skip default. DryRun never touches the filesystem, including
// MkdirAll — a dry run that creates an empty directory is a dry run that lied.
// It reports which of the three outcomes above it took.
func writeScaffoldFile(root, installed string, data []byte, opts Options, res *Result) (writeOutcome, error) {
	dst := filepath.Join(root, filepath.FromSlash(installed))
	if _, err := os.Lstat(dst); err == nil {
		if !opts.Force {
			res.skipped(installed + " (exists — pass --force to overwrite)")
			return writeOutcomeSkipped, nil
		}
		if opts.DryRun {
			res.note(installed + " (dry-run) would overwrite")
			return writeOutcomeDryRun, nil
		}
	} else if opts.DryRun {
		res.note(installed + " (dry-run) would create")
		return writeOutcomeDryRun, nil
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return writeOutcomeWritten, err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return writeOutcomeWritten, err
	}
	res.created(installed)
	return writeOutcomeWritten, nil
}

// justfileRecipe is one docs recipe appendJustfileRecipes may append.
type justfileRecipe struct {
	Name    string // matched as a "<name>:" line prefix
	Comment string
	Body    string
}

var justfileRecipes = []justfileRecipe{
	{
		Name:    "docs-check",
		Comment: "# Validate the AsciiDoc manual via snowball (broken includes/xrefs fail the check)",
		Body:    "snowball check",
	},
	{
		Name:    "docs-build",
		Comment: "# Render the user manual to PDF + EPUB into dist/docs/",
		Body:    "snowball build -o dist/docs",
	},
}

// appendJustfileRecipes appends docs-check/docs-build recipes to an existing
// justfile — additive only, and it never creates a justfile: a repo with no
// task runner does not get one invented for it.
func appendJustfileRecipes(root string, dryRun bool, res *Result) error {
	path := filepath.Join(root, "justfile")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			res.skipped("justfile (does not exist — skipped; scaffold never creates a task runner)")
			return nil
		}
		return err
	}

	var toAppend []justfileRecipe
	for _, rec := range justfileRecipes {
		if definesName(string(data), rec.Name) {
			res.skipped(fmt.Sprintf("justfile: %s (already defined)", rec.Name))
			continue
		}
		toAppend = append(toAppend, rec)
	}
	if len(toAppend) == 0 {
		return nil
	}
	if dryRun {
		for _, rec := range toAppend {
			res.note(fmt.Sprintf("justfile: %s (dry-run) would append", rec.Name))
		}
		return nil
	}

	// Did the file parse before we touched it? Only then can a parse failure
	// afterwards be blamed on this append — see restoreIfUnparseable below.
	parsedBefore := justfileParses(path)

	var b strings.Builder
	b.WriteString(string(data))
	if !strings.HasSuffix(string(data), "\n") {
		b.WriteString("\n")
	}
	var appended []string
	for _, rec := range toAppend {
		fmt.Fprintf(&b, "\n%s\n%s:\n    %s\n", rec.Comment, rec.Name, rec.Body)
		appended = append(appended, rec.Name)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return err
	}

	if restored, err := restoreIfUnparseable(path, data, parsedBefore); err != nil {
		return err
	} else if restored {
		res.skipped("justfile (left unchanged — appending the docs recipes would have made it " +
			"unparseable, most likely a name collision `just` reports that this scaffold did not " +
			"recognise; add the recipes by hand)")
		return nil
	}
	for _, name := range appended {
		res.created(fmt.Sprintf("justfile: %s recipe appended", name))
	}
	return nil
}

// justfileParses reports whether `just` can parse the justfile at path.
// It is a best-effort probe: when `just` is not installed there is nothing
// to ask, and it reports true so the caller proceeds on its own detection
// rather than refusing to work on a machine without the task runner
// installed. snowball does not depend on `just`; this only consults it when
// it happens to be there.
func justfileParses(path string) bool {
	bin, err := exec.LookPath("just")
	if err != nil {
		return true
	}
	cmd := exec.Command(bin, "--justfile", path, "--working-directory", filepath.Dir(path), "--summary")
	return cmd.Run() == nil
}

// restoreIfUnparseable rewrites original back to path when appending broke
// the file's syntax, reporting whether it did.
//
// This is the backstop for a defect that recurred three times while the
// detector below was extended form by form: a name already bound by a
// justfile construct the pattern did not recognise (a parameterised recipe,
// a quiet `@` recipe, an alias) got a second, colliding definition appended,
// leaving `just` unable to parse *any* recipe in the user's file — silently,
// since scaffold reported success. Enumerating legal forms is what kept
// failing, so this asks `just` itself instead: whatever the collision, the
// user's justfile is restored rather than left broken. parsedBefore guards
// against blaming this append for a file that was already unparseable.
func restoreIfUnparseable(path string, original []byte, parsedBefore bool) (bool, error) {
	if !parsedBefore || justfileParses(path) {
		return false, nil
	}
	if err := os.WriteFile(path, original, 0o644); err != nil {
		return false, fmt.Errorf("restore %s after a failed append: %w", path, err)
	}
	return true, nil
}

// definesName reports whether justfile content already binds name, by either
// of the two constructs that would collide with a recipe scaffold wants to
// append:
//
//   - a recipe header: an optional "@" quiet modifier, then the name at
//     column 0, followed immediately by ":" (no parameters) or whitespace
//     (parameters, optionally with defaults) — `docs-build:`,
//     `@docs-build:`, `docs-build DIR="dist":`;
//   - an alias: `alias docs-build := some-recipe`.
//
// Anchoring to the start of the line avoids a false match on a recipe that
// merely lists name as a dependency (e.g. `build: docs-check`).
//
// This is best-effort detection, and deliberately no longer the only line of
// defence: each time it was extended to cover one more legal form, another
// turned up. restoreIfUnparseable above is the backstop that does not depend
// on enumerating them correctly.
func definesName(content, name string) bool {
	quoted := regexp.QuoteMeta(name)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^@?` + quoted + `(:|\s)`),
		regexp.MustCompile(`^alias\s+` + quoted + `\s*:=`),
	}
	for _, line := range strings.Split(content, "\n") {
		for _, p := range patterns {
			if p.MatchString(line) {
				return true
			}
		}
	}
	return false
}
