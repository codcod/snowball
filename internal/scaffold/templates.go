package scaffold

import "embed"

// templatesFS embeds every file scaffold writes into a target repo, aside from
// the generated snowball.yaml (which has no fixed content — see StarterConfig)
// and the theme file (embedded generically as templates/docs/pdf-theme/theme.yml
// and renamed to "<project>-theme.yml" when it is written — see Docs).
//
//go:embed templates
var templatesFS embed.FS
