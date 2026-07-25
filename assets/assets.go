// Package assets embeds the pinned toolchain manifests snowball installs and the
// mermaid/puppeteer smoke-render fixtures used by the renderer.
package assets

import _ "embed"

// Gemfile is the pinned asciidoctor gem set snowball setup installs via bundler.
// Kept as version constraints (not a platform-locked Gemfile.lock) so bundler can
// resolve for whatever OS/arch runs setup (macOS locally, linux in CI).
//
//go:embed Gemfile
var Gemfile string

// SmokeMermaid is a trivial diagram rendered by `mmdc` before a real build to
// surface Chrome launch failures with a clear error instead of a generic
// "mmdc failed" from asciidoctor-diagram.
//
//go:embed smoke.mmd
var SmokeMermaid string
