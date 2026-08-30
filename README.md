# snowball

`snowball` is a single Go binary that renders AsciiDoc book masters to PDF and
EPUB using the native asciidoctor toolchain.

## Install

```sh
brew install codcod/tap/snowball
```

## Initialize

New repo, no docs yet:

```sh
snowball scaffold --project-name my-project
snowball setup
snowball doctor
snowball build
snowball docs-prompt |pi -p  # supplement initial docs content 
```

By default, `scaffold` also writes `.github/workflows/ci.yml`, `release.yml` and
`.goreleaser.yaml` — cross-compiled builds, `actionlint`/`goreleaser check` validation, and a
tag-triggered release job. These assume a **Go** project laid out as `./cmd/<project-name>`
with a `go.mod` at the repo root; pass `--no-release-workflow` to skip the trio if that is not
you. Pass `--homebrew` to additionally scaffold a `brews:` (homebrew tap) block (needs a
`homebrew-tap` repo and a `HOMEBREW_TAP_GITHUB_TOKEN` secret before the first tag); if
`.goreleaser.yaml` already exists, `--homebrew` prints a note rather than silently doing
nothing — pass `--force` to append the block. The GitHub owner in `.goreleaser.yaml` is read
from the `origin` remote of the enclosing git repository (git searches upward from the current
directory) when possible; without one, it's written as `TODO-owner` for you to fill in. When
the enclosing repository isn't the directory you scaffolded in, a note names which repository
the owner came from, so it's never a silent surprise. Unlike every other command, `scaffold`
and `init` always write into the current directory — they never walk upward looking for an
existing `snowball.yaml`.

Existing docs tree:

```sh
snowball init --project-name my-project
snowball setup
```

## Read More

Read the user manual: [`docs/user-manual.adoc`](docs/user-manual.adoc)
