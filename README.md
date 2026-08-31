# snowball

`snowball` is a single Go binary that renders AsciiDoc book masters to PDF and
EPUB using the native asciidoctor toolchain.

## Install

```sh
brew tap codcod/tap
brew trust --formula codcod/tap/morty
brew install codcod/tap/snowball
```

## Initialize

New repo, no docs yet:

```sh
snowball scaffold  # --project-name my-project
snowball setup
snowball doctor
snowball build
snowball docs-prompt |pi -p  # supplement initial docs content 
```

Existing docs tree:

```sh
snowball init --project-name my-project
snowball setup
```

## Caveat: scaffolding non-Go projects

By default, `scaffold` also writes `.github/workflows/ci.yml`, `release.yml` and
`.goreleaser.yaml`. These assume a **Go** project layout. Pass
`--no-release-workflow` to skip the trio if that is not you.

Pass `--homebrew` to additionally scaffold a `brews:` (homebrew tap) block. If
`.goreleaser.yaml` already exists, `--homebrew` prints a note rather than
silently doing nothing; pass `--force` to append the block.

The GitHub owner in `.goreleaser.yaml` is read from the `origin` remote of the
enclosing git repository (git searches upward from the current directory) when
possible; without one, it's written as `TODO-owner` for you to fill in. When the
enclosing repository isn't the directory you scaffolded in, a note names which
repository the owner came from, so it's never a silent surprise. Unlike every
other command, `scaffold` and `init` always write into the current directory —
they never walk upward looking for an existing `snowball.yaml`.

## Read more

Read the user manual: [`docs/user-manual.adoc`](docs/user-manual.adoc)
