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
attributes: docs/attributes.adoc
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
