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
snowball watch       # re-render whenever a source changes
snowball check       # validate masters without keeping artifacts (MR pipelines)
snowball clean       # delete the outputs a build would produce
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

### Parallelism

`-j N` renders up to N books at once (default: up to 4, `-j 1` forces serial).

Concurrency is per **book**, never per format. asciidoctor-diagram writes
generated images and its `.asciidoctor` cache next to the source, so the PDF and
EPUB of one book share those files and must not run together. Serialising them
is also faster: the second format reuses the diagram cache the first populated
and costs a fraction of it.

### Watching

`snowball watch` renders once, then rebuilds on every change, keeping the
toolchain check and the mermaid preflight out of the loop.

It watches each book's whole source tree, not just the master, so edits to
`include::`d chapters trigger a rebuild. Only `.adoc` files and the configured
theme trigger one — that is deliberate: builds write PDFs, EPUBs and generated
diagrams into the very directories being watched, and reacting to those would
loop forever. A failed build is reported and the watch continues.

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

### Cleaning

`snowball clean` removes the `.pdf`/`.epub` files a build would produce, honouring
`--book`, `--pdf`/`--epub` and `-o`. It does not need the toolchain installed.

Generated diagram images are left alone — they sit next to the sources and cannot
be told apart from hand-authored ones. `--cache` additionally drops the
`.asciidoctor` cache directories, which are unambiguously generated.

## Toolchain boundary

`snowball setup` installs **language-level** deps only: asciidoctor gems via
bundler, `mermaid-cli`, Puppeteer's Chrome — **including bundler itself**. A
missing bundler is not your job to fix; `setup` bootstraps it with `gem install
--no-document bundler`, and both bundler and the pinned gem set install into
snowball's own cache — `snowball/toolchain/gems` under your user cache
directory (`~/.cache` on Linux, `~/Library/Caches` on macOS,
`%LocalAppData%` on Windows), pointed at with `GEM_HOME`. Nothing is written
outside it and nothing needs `sudo`.

`setup` does need **ruby**, **gem**, **node** and **npm** already on `PATH` —
these stay the environment's job, and `setup` fails fast naming whichever one
is missing (never a raw `exec: "bundle": executable file not found` or
similar). **OS-level** deps — Node.js itself and Chrome's shared libraries
(`libnss3`, `libgtk-3-0`, `fonts-liberation`, …) — are the environment's job
too; `snowball doctor` reports them but never installs them.

## Build

```sh
just build   # -> ./snowball
just test
just lint    # gofmt drift + go vet, same static checks CI runs
```
