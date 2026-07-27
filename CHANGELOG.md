# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
While the version is below `1.0.0`, breaking changes may land in a minor release.

## [Unreleased]

## [0.2.1] - 2026-07-27

### Fixed

- **`snowball -v` printed help instead of the version.** 0.2.0 gave the `-v`
  shorthand to its new `--verbose` flag, taking it from `--version` — cobra
  assigns `-v` to `--version` whenever the shorthand is free, which is what
  0.1.x shipped. The result was silent: `snowball -v` emitted help text and
  still exited 0, so a script reading the version captured the help output
  instead of failing. `--verbose` is now long-form only.

  Anyone on 0.2.0 should upgrade; anyone coming from 0.1.x can skip it.

## [0.2.0] - 2026-07-27

### Breaking

- **`attributes` in `snowball.yaml` is now a map of AsciiDoc attributes, not a
  path to a shared `.adoc` file.** The old form was documented and written by
  `snowball init`, but was never passed to asciidoctor — setting it did nothing.
  It is now a load error carrying a migration hint. To keep including a shared
  file, use `include::docs/attributes.adoc[]` in the book master instead:

  ```yaml
  # before (silently ignored)
  attributes: docs/attributes.adoc

  # after
  attributes:
    toc: left
    sectnums: ""
  ```

### Migrating from 0.1.x

`attributes` is the only key that changed. `books`, `theme`, `formats`,
`revision`, `mermaid` and `failure-level` carry over untouched, with the same
defaults, so a 0.1.x config needs at most one edit. It fails loudly at load,
so nothing can slip through silently:

```
Error: parse snowball.yaml: `attributes` must be a map of AsciiDoc attributes, not a path.
```

**1. Convert `attributes`.** Either inline the contents of the file:

```yaml
attributes:
  toc: left              # was :toc: left
  sectnums: ""           # was a bare :sectnums:
  toc-title: false       # was :!toc-title: (unset)
  product-version: "2026.1"
```

…or delete the `attributes:` key entirely and include the file from each book
master, which is the idiomatic AsciiDoc route and better for a large or shared
attribute set:

```adoc
= My Manual
:doctype: book

include::attributes.adoc[]
```

Both produce identical output.

**2. Expect your documents to change.** Because 0.1.x never passed the
attributes file to asciidoctor, attributes you set there were silently dropped.
The same project rendering `Version is {product-version} here.`:

| | output |
|---|---|
| 0.1.4 | `Version is {product-version} here.` |
| 0.2.0 | `Version is 2026.1 here.` |

Attributes set long ago will take effect for the first time. Diff your output
before shipping.

**3. Expect larger EPUBs.** Mermaid diagrams now render into EPUB instead of
being emitted as literal source, so books with diagrams need a working Chrome
for EPUB builds, not just PDF.

**4. Builds are parallel by default.** Progress output is grouped per book
rather than strictly ordered. Pass `-j 1` to restore the 0.1.x serial
behaviour if anything parses that output.

**5. Upgrade to 0.2.1, not 0.2.0.** Command-line flags are otherwise unchanged
from 0.1.x, but 0.2.0 briefly reassigned `-v` from `--version` to `--verbose`,
so `snowball -v` printed help instead of the version. 0.2.1 restores it.

### Added

- `attributes` are now passed through to every render as `-a` flags, in all of
  PDF, EPUB and `check`. `key: value` becomes `-a key=value`, `key: ""` becomes
  a bare `-a key`, and `key: false` becomes `-a key!` (explicitly unset). Keys
  are emitted in sorted order so the invocation is reproducible. Attributes
  snowball derives from other settings — `revnumber`, `revdate`,
  `mermaid-format`, `mermaid-puppeteer-config`, `pdf-theme`, `pdf-themesdir` —
  are rejected at load rather than silently overwritten.
- `snowball watch` re-renders whenever a source changes, running the toolchain
  check and the mermaid preflight once at startup instead of per rebuild. It
  watches each book's whole source tree, so edits to `include::`d chapters
  trigger a rebuild. A failed build is reported without stopping the watch.
- `snowball clean` removes the `.pdf`/`.epub` files a build would produce,
  honouring `--book`, `--pdf`/`--epub` and `-o`, and does not require the
  toolchain to be installed. `--cache` additionally drops `.asciidoctor` cache
  directories. Generated diagram images are deliberately left alone: they sit
  next to the sources and cannot be told apart from hand-authored ones.
- `-j`/`--jobs` renders multiple books concurrently, defaulting to at most 4.
  `-j 1` forces the previous serial behaviour. On a 4-book, 2-format project
  this took a cold build from 12.4s to 4.8s.
- `-q`/`--quiet` suppresses progress output, holding each command's output and
  replaying it only if that render fails — silent on success without hiding the
  cause of a failure.
- `-v`/`--verbose` logs every command snowball runs. (Claiming `-v` was a
  mistake — it belonged to `--version`. Corrected in 0.2.1.)

### Changed

- `snowball.yaml` is now discovered by walking up from the working directory,
  the way git, cargo and npm locate their manifests, so `cd docs/ && snowball
  build` works. An explicit `--config` is still taken literally.
- Concurrency is per book, never per format. asciidoctor-diagram writes
  generated images and its `.asciidoctor` cache next to the source, so the two
  formats of one book share those files and must not render simultaneously.
  Serialising them is also faster, because the second format reuses the diagram
  cache the first populated.

### Fixed

- **EPUB output silently omitted mermaid diagrams.** `renderEPUB` passed
  `mermaid-format` but never loaded `-r asciidoctor-diagram`, leaving the
  attribute inert: the diagram was written into the EPUB as its literal source
  text, with no image and a zero exit code, while the same book rendered
  correctly to PDF.
- `snowball doctor` no longer reports tool versions with trailing whitespace.
  The probe trimmed its whole output before splitting on the first newline, so
  the trim only ever reached the end of the last line.

## [0.1.4] - 2026-07-27

### Added

- Windows binaries to the release matrix.

### Changed

- Homebrew distribution switched from a cask to a formula.

## [0.1.3] - 2026-07-25

### Changed

- CI runners bumped to the Node.js 24 based actions.
- Dependabot now tracks GitHub Actions and Go modules.
- The release workflow can be triggered manually via `workflow_dispatch`.

## [0.1.2] - 2026-07-25

### Changed

- Homebrew distribution switched from a formula to a cask. (Reverted in 0.1.4.)

## [0.1.1] - 2026-07-25

### Added

- Homebrew tap publishing.

## [0.1.0] - 2026-07-25

Initial release. A single Go binary that renders AsciiDoc book masters to PDF
and EPUB by orchestrating the native asciidoctor toolchain, configured through
`snowball.yaml`, with `build`, `check`, `doctor`, `setup`, `init` and `version`
commands.

[Unreleased]: https://github.com/codcod/snowball/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/codcod/snowball/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/codcod/snowball/compare/v0.1.4...v0.2.0
[0.1.4]: https://github.com/codcod/snowball/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/codcod/snowball/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/codcod/snowball/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/codcod/snowball/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/codcod/snowball/releases/tag/v0.1.0
