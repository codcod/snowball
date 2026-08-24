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

Existing docs tree:

```sh
snowball init --project-name my-project
snowball setup
```

## Read More

Read the user manual: [`docs/user-manual.adoc`](docs/user-manual.adoc)
