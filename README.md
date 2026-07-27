# snowball

A single Go binary that renders AsciiDoc book masters to **PDF** and **EPUB**,
replacing a pile of `justfile` and CI recipes with one config-driven tool.

snowball is a **native orchestrator**: it shells out to the asciidoctor toolchain
(`asciidoctor-pdf`, `asciidoctor-epub3`, `asciidoctor-diagram`) and `mermaid-cli`
(`mmdc`) on your `PATH` — same engine, same output fidelity (themes, diagrams,
EPUB), just one command instead of many. No docker.

## Quick start

```sh
snowball init        # write a starter snowball.yaml
snowball setup       # install the pinned toolchain (gems + mermaid-cli + Chrome)
snowball doctor      # verify everything is present
snowball build       # render PDFs + EPUBs per snowball.yaml
snowball check       # validate masters without keeping artifacts (MR pipelines)
```

## Configuration — `snowball.yaml`

```yaml
books:
  - src: docs/user-manual.adoc
    out: users-manual
  - src: docs/developer-handbook.adoc
    out: developers-handbook
theme: docs/pdf-theme/ai-sdlc-theme.yml   # PDF only; -> pdf-themesdir + pdf-theme
attributes:                                # passed to every render as -a flags
  toc: left                                #   -a toc=left
  sectnums: ""                             #   -a sectnums       (set, no value)
  product-version: "2026.1"                #   -a product-version=2026.1
formats: [pdf, epub]
revision:
  from: git-describe                       # or: static (+ static: "1.2.3")
  date-format: "%d %B %Y"
mermaid:
  format: png
  puppeteer-args: ["--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"]
failure-level:
  pdf: WARN
  epub: ERROR
  check: WARN
```

Flags override config. `--rev`/`--date` set the revision; `--book NAME` limits to
specific books; `--pdf`/`--epub` pick formats; `-o DIR` sets the output directory.

Global: `-q`/`--quiet` silences progress (tool output is still shown if a render
fails), `-v`/`--verbose` logs every command snowball runs, and `-c FILE` points at
a specific config.

`snowball.yaml` is discovered by walking up from the working directory, so
`cd docs/ && snowball build` works the same as running from the repo root.

### Attributes

Every key under `attributes` becomes an `-a` flag on each render, in all three of
PDF, EPUB and `check`. Values map as `key: value` -> `-a key=value`, `key: ""` ->
`-a key` (set with no value), and `key: false` -> `-a key!` (explicitly unset).

Attributes snowball derives from other settings — `revnumber`, `revdate`,
`mermaid-format`, `mermaid-puppeteer-config`, `pdf-theme`, `pdf-themesdir` — are
rejected here rather than silently overwritten; use `revision`, `mermaid` and
`theme` instead.

> `attributes` previously took a path to a shared `.adoc` file, but was never
> passed to asciidoctor. That form is now a load error; to include a shared file,
> use `include::docs/attributes.adoc[]` in the book master.

## Toolchain boundary

`snowball setup` installs **language-level** deps only (asciidoctor gems via
bundler, `mermaid-cli`, Puppeteer's Chrome). **OS-level** deps — Node.js and
Chrome's shared libraries (`libnss3`, `libgtk-3-0`, `fonts-liberation`, …) — are
the environment's job; `snowball doctor` reports them but never installs them.

## Build

```sh
just build   # -> ./snowball
just test
```
