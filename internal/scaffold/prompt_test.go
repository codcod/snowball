package scaffold

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// backtickSpan matches one backtick-delimited span confined to a single physical
// line. Deliberately narrow — not a general markdown parser: excluding "\n" from
// the character class means a code span an editor has wrapped across two lines
// fails to match at all — the mechanism the two tests below rely on to catch a
// marker or path silently broken by line-wrapping.
var backtickSpan = regexp.MustCompile("`([^`\n]+)`")

// promptSection returns the docs-prompt asset's text for the named "## " section,
// from just after its heading up to (but not including) the next "## " heading, or
// EOF if it is the last section. t.Fatalf if the heading is not found at all — a
// renamed or removed section must fail loudly, not silently parse zero items.
func promptSection(t *testing.T, heading string) string {
	t.Helper()
	data, err := Prompt()
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	full := string(data)
	marker := "## " + heading
	start := strings.Index(full, marker)
	if start == -1 {
		t.Fatalf("docs-prompt.md has no %q section — has the prompt been restructured?", marker)
	}
	rest := full[start+len(marker):]
	if end := strings.Index(rest, "\n## "); end != -1 {
		return rest[:end]
	}
	return rest
}

// scaffoldTree scaffolds into a fresh temp dir and returns its root.
func scaffoldTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, err := Docs(root, Options{ProjectName: "demo"}); err != nil {
		t.Fatalf("Docs: %v", err)
	}
	return root
}

// concatDocs returns the concatenated bytes of every file under the scaffolded
// docs/ subtree of root, as one string.
func concatDocs(t *testing.T, root string) string {
	t.Helper()
	var combined strings.Builder
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		combined.Write(data)
		combined.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatalf("walking scaffolded docs/: %v", err)
	}
	return combined.String()
}

// TestDocsPromptPathsExistInScaffoldedTree parses the "Files to check" section's
// bullet-list paths straight out of the shipped docs-prompt asset — not a
// hand-maintained duplicate table — and asserts each one actually exists in a real
// scaffolded tree. A path the prompt names but scaffold no longer writes (or vice
// versa) fails here instead of shipping silently.
func TestDocsPromptPathsExistInScaffoldedTree(t *testing.T) {
	section := promptSection(t, "Files to check")

	var paths []string
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		m := backtickSpan.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		paths = append(paths, m[1])
	}
	if len(paths) != 3 {
		t.Fatalf("parsed %d path(s) from the prompt's \"Files to check\" list, want 3 — "+
			"a code span wrapped across a line, or the list itself changed shape: %v", len(paths), paths)
	}

	// Distinct, or the prompt can silently stop naming one of the three files
	// while the count still reads 3 and every Stat below still succeeds.
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if seen[p] {
			t.Fatalf("docs-prompt names %q twice in its \"Files to check\" list — "+
				"one of the three files it should name is missing: %v", p, paths)
		}
		seen[p] = true
	}

	root := scaffoldTree(t)
	for _, p := range paths {
		// IsRegular, not merely "exists": os.Stat is satisfied by a directory, so a
		// path that silently became a directory name would otherwise pass.
		fi, err := os.Stat(filepath.Join(root, p))
		if err != nil {
			t.Errorf("docs-prompt names %q but the scaffolded tree does not have it: %v", p, err)
			continue
		}
		if !fi.Mode().IsRegular() {
			t.Errorf("docs-prompt names %q as a file to check, but the scaffolded tree has "+
				"a non-regular entry there (mode %s)", p, fi.Mode())
		}
	}
}

// TestDocsPromptMarkersAppearVerbatimInScaffoldedTree parses the "Placeholder
// markers to find and remove" section's backtick spans straight out of the
// shipped docs-prompt asset, and asserts each one appears verbatim somewhere
// under a real scaffolded docs/ tree — matching the prompt's own instruction to
// check every .adoc file under docs/, not one hardcoded chapter path.
//
// The count assertion below is what makes a line-wrapped marker fail loudly:
// wrapping a marker's code span across two lines makes backtickSpan fail to
// match it at all, so the parsed count drops from 2 and this test fails
// immediately — instead of silently checking fewer markers than the prompt
// names. Not a hypothetical: this asset has shipped exactly that defect once,
// wrapping two spans so the marker it told agents to match verbatim did not
// occur in its own output.
func TestDocsPromptMarkersAppearVerbatimInScaffoldedTree(t *testing.T) {
	section := promptSection(t, "Placeholder markers to find and remove")

	matches := backtickSpan.FindAllStringSubmatch(section, -1)
	if len(matches) != 2 {
		t.Fatalf("parsed %d marker(s) from the prompt's placeholder-marker section, want 2 — "+
			"a code span wrapped across a line, or the section itself changed shape", len(matches))
	}

	tree := concatDocs(t, scaffoldTree(t))
	for _, m := range matches {
		marker := m[1]
		if !strings.Contains(tree, marker) {
			t.Errorf("docs-prompt names marker %q verbatim but it does not appear anywhere "+
				"under the scaffolded docs/ tree", marker)
		}
	}
}
