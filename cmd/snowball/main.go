// Command snowball renders AsciiDoc book masters to PDF and EPUB by orchestrating
// the native asciidoctor + mermaid toolchain on PATH. See PLAN.md.
package main

import (
	"os"

	"github.com/codcod/snowball/internal/cli"
)

// Version is injected at build time via -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if err := cli.Execute(Version); err != nil {
		os.Exit(1)
	}
}
