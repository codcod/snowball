package assets

import (
	"strings"
	"testing"
)

func TestGemfilePinsTheRenderingToolchain(t *testing.T) {
	if strings.TrimSpace(Gemfile) == "" {
		t.Fatal("embedded Gemfile is empty")
	}
	if !strings.Contains(Gemfile, "source 'https://rubygems.org'") {
		t.Error("Gemfile has no rubygems source")
	}
	// Every gem the renderer shells out to must be declared, or `bundle exec`
	// will fail at render time rather than at setup time.
	for _, gem := range []string{
		"asciidoctor",
		"asciidoctor-pdf",
		"asciidoctor-epub3",
		"asciidoctor-diagram",
	} {
		if !strings.Contains(Gemfile, "gem '"+gem+"'") {
			t.Errorf("Gemfile does not declare %q", gem)
		}
	}
}

func TestSmokeMermaidIsARenderableDiagram(t *testing.T) {
	if strings.TrimSpace(SmokeMermaid) == "" {
		t.Fatal("embedded smoke diagram is empty")
	}
	first := strings.Fields(strings.TrimSpace(SmokeMermaid))[0]
	if first != "graph" && first != "flowchart" {
		t.Errorf("smoke diagram starts with %q, want a mermaid graph declaration", first)
	}
	if !strings.Contains(SmokeMermaid, "-->") {
		t.Error("smoke diagram has no edges, so it may not exercise a real render")
	}
}
