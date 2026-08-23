package scaffold

// promptAsset is the embedded agent-prompt asset's path within templatesFS.
const promptAsset = "templates/docs-prompt.md"

// Prompt returns the self-contained prompt an AI coding agent can use to
// replace a scaffolded docs tree's placeholder content with real,
// project-specific content. Unlike the other embedded templates, it is never
// written into the target repo and carries no projectNameToken substitution
// — the agent consuming it discovers the actual project from the repo it is
// pointed at, so the text stays generic on purpose.
func Prompt() ([]byte, error) {
	return templatesFS.ReadFile(promptAsset)
}
