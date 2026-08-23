package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// NoWorkflow skips writing the GitHub release-attach workflow.
	NoWorkflow bool
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
// matching PDF theme, a generated snowball.yaml (via StarterConfig), and
// (unless NoWorkflow) a GitHub release-attach workflow. Justfile recipes are
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

	for _, f := range files {
		data, err := templatesFS.ReadFile(f.asset)
		if err != nil {
			return res, fmt.Errorf("read embedded %s: %w", f.asset, err)
		}
		data = substitute(data, name)
		if err := writeScaffoldFile(root, f.installed, data, opts, &res); err != nil {
			return res, err
		}
	}

	cfg := StarterConfig(name, true)
	if err := writeScaffoldFile(root, "snowball.yaml", cfg, opts, &res); err != nil {
		return res, err
	}

	if err := appendJustfileRecipes(root, opts.DryRun, &res); err != nil {
		return res, err
	}

	return res, nil
}

// writeScaffoldFile writes data to root/installed, honouring Force, DryRun and
// the exists-skip default. DryRun never touches the filesystem, including
// MkdirAll — a dry run that creates an empty directory is a dry run that lied.
func writeScaffoldFile(root, installed string, data []byte, opts Options, res *Result) error {
	dst := filepath.Join(root, filepath.FromSlash(installed))
	if _, err := os.Lstat(dst); err == nil {
		if !opts.Force {
			res.skipped(installed + " (exists — pass --force to overwrite)")
			return nil
		}
		if opts.DryRun {
			res.note(installed + " (dry-run) would overwrite")
			return nil
		}
	} else if opts.DryRun {
		res.note(installed + " (dry-run) would create")
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	res.created(installed)
	return nil
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
		Comment: "# Render the user manual to PDF + EPUB into dist/docs/ (never committed)",
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
		if hasRecipe(string(data), rec.Name) {
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

	var b strings.Builder
	b.WriteString(string(data))
	if !strings.HasSuffix(string(data), "\n") {
		b.WriteString("\n")
	}
	for _, rec := range toAppend {
		fmt.Fprintf(&b, "\n%s\n%s:\n    %s\n", rec.Comment, rec.Name, rec.Body)
		res.created(fmt.Sprintf("justfile: %s recipe appended", rec.Name))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// hasRecipe reports whether justfile content already defines a recipe named
// name — matched as a line starting with "<name>:", the same shape `just`
// itself parses a recipe header as.
func hasRecipe(content, name string) bool {
	prefix := name + ":"
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
