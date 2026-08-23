package scaffold

import "embed"

// templatesFS embeds this package's static assets. Most are files scaffold
// writes into a target repo, aside from the generated snowball.yaml (which has
// no fixed content — see StarterConfig) and the theme file (embedded generically
// as templates/docs/pdf-theme/theme.yml and renamed to "<project>-theme.yml"
// when it is written — see Docs).
//
// One asset is deliberately never written anywhere: templates/docs-prompt.md is
// printed to stdout by `snowball docs-prompt` (see Prompt) for the user to hand
// to an AI coding agent. Do not assume everything under templates/ is scaffold
// output.
//
//go:embed templates
var templatesFS embed.FS
