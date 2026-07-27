package render

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/codcod/snowball/internal/config"
)

// WatchSettle is how long the watcher waits for the filesystem to go quiet
// before rebuilding. Editors emit several events per save (write, rename,
// chmod), and a burst should produce one build, not four.
const WatchSettle = 300 * time.Millisecond

// Watch renders once, then re-renders whenever a source document changes, until
// ctx is cancelled. The mermaid preflight and the toolchain check happen once up
// front rather than per rebuild.
//
// A failed build does not stop the watch — recovering from an error is exactly
// when the author most wants the loop to keep running.
func Watch(ctx context.Context, cfg *config.Config, opts Options) error {
	// Validate up front: starting a watcher on a selection that can never
	// render is worse than failing now.
	p, err := resolvePlan(cfg, opts)
	if err != nil {
		return err
	}
	u := newUI(opts)
	work, cleanup, err := prepMermaid(cfg, u)
	if err != nil {
		return err
	}
	defer cleanup()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("start watcher: %w", err)
	}
	defer func() { _ = w.Close() }()

	roots := watchRoots(cfg)
	for _, r := range roots {
		if err := addTree(w, r); err != nil {
			return fmt.Errorf("watch %s: %w", r, err)
		}
	}

	rebuild := func(reason string) {
		if reason != "" {
			u.logf("snowball: %s changed, rebuilding\n", reason)
		}
		if err := build(cfg, opts, p, work, u); err != nil {
			// Reported, not returned: the watch must survive a broken document.
			fmt.Fprintf(u.errOut, "snowball: build failed: %v\n", err)
		}
	}

	rebuild("")
	u.logf("snowball: watching %s — press ctrl-c to stop\n", strings.Join(relRoots(cfg, roots), ", "))

	settle := time.NewTimer(0)
	if !settle.Stop() {
		<-settle.C
	}
	var pending string
	for {
		select {
		case <-ctx.Done():
			u.logf("snowball: stopped watching\n")
			return nil

		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(u.errOut, "snowball: watch error: %v\n", err)

		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			// Pick up directories created after the watch started.
			if ev.Has(fsnotify.Create) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					_ = addTree(w, ev.Name)
				}
			}
			if !triggersRebuild(cfg, ev.Name) {
				continue
			}
			pending = filepath.Base(ev.Name)
			settle.Reset(WatchSettle)

		case <-settle.C:
			if pending == "" {
				continue
			}
			reason := pending
			pending = ""
			rebuild(reason)
		}
	}
}

// triggersRebuild decides whether a changed path should start a build.
//
// This is the load-bearing part of watch mode. A build writes its PDFs, EPUBs
// and generated diagram images *into* the directories being watched, so an
// indiscriminate watcher would retrigger itself forever. Restricting the trigger
// to AsciiDoc sources and the theme file breaks that cycle by construction:
// snowball never writes either.
func triggersRebuild(cfg *config.Config, path string) bool {
	// Anything inside a dot-directory (.git, .asciidoctor) is not a source.
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		if len(seg) > 1 && strings.HasPrefix(seg, ".") {
			return false
		}
	}
	base := filepath.Base(path)
	// Editor scratch files: .foo.adoc.swp, foo.adoc~, foo.adoc.tmp.
	if strings.HasPrefix(base, ".") || strings.HasSuffix(base, "~") {
		return false
	}
	if strings.EqualFold(filepath.Ext(base), ".adoc") {
		return true
	}
	if cfg.Theme != "" {
		if abs, err := filepath.Abs(path); err == nil && abs == cfg.Path(cfg.Theme) {
			return true
		}
		if path == cfg.Path(cfg.Theme) {
			return true
		}
	}
	return false
}

// watchRoots is the deduplicated set of directories to watch: each book's source
// directory, plus the theme's directory when one is configured.
func watchRoots(cfg *config.Config) []string {
	seen := map[string]bool{}
	for _, b := range cfg.Books {
		seen[filepath.Dir(cfg.Path(b.Src))] = true
	}
	if dir, _, ok := cfg.ThemeDirName(); ok {
		seen[dir] = true
	}
	roots := make([]string, 0, len(seen))
	for d := range seen {
		roots = append(roots, d)
	}
	sort.Strings(roots)
	return dropNested(roots)
}

// dropNested removes directories already covered by an ancestor in the list, so
// a tree is not registered twice.
func dropNested(dirs []string) []string {
	var out []string
	for _, d := range dirs {
		nested := false
		for _, kept := range out {
			if d == kept || strings.HasPrefix(d, kept+string(os.PathSeparator)) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, d)
		}
	}
	return out
}

// relRoots renders the watch roots relative to the config dir, for logging.
func relRoots(cfg *config.Config, roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if rel, err := filepath.Rel(cfg.Dir, r); err == nil {
			out = append(out, rel)
			continue
		}
		out = append(out, r)
	}
	return out
}

// addTree registers root and every non-hidden directory beneath it. fsnotify
// watches a single directory at a time, so recursion is ours to do.
func addTree(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerate races and unreadable dirs
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		return w.Add(p)
	})
}
