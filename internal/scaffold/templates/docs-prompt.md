# Supplement scaffolded docs with real content

You are working in a project whose AsciiDoc user manual was laid down from a starter
skeleton: structure, not content. Your job is to replace every placeholder chapter with
real, project-specific content — grounded in this project's actual README, source code,
configuration, and CLI `--help`/output — and nothing invented.

## Files to check

Start with these three, all under `docs/`:

- `docs/attributes.adoc` — shared attribute definitions included by the book master.
- `docs/user-manual.adoc` — the book master.
- `docs/user-manual/introduction.adoc` — the introduction chapter.

If the project has grown additional chapters since it was scaffolded, check every
`.adoc` file under `docs/` for the same two markers below — not only the three listed
above.

## Placeholder markers to find and remove

A file still needs work if it contains either of these, verbatim:

1. A leading comment: `// TODO: replace this placeholder chapter`
2. A sentence of the form:
   `{product} does ... (replace this with a one-sentence summary of what it does).`

Removing the marker is not enough — replace the surrounding placeholder text itself with
real content about this project.

## What "done" looks like

- No file under `docs/` still contains either marker above.
- Every section you touch reads as specific to this project — not a rephrased template,
  and not a guess. Base it on what the README, the source, the configuration, or running
  the project's own CLI actually show. If you are not sure something is true of this
  project, do not state it.
- You have not invented features, commands, or behaviour that do not exist.

## Structural constraints — do not break these

- Chapters are included by the book master with `leveloffset=+1`, so a chapter renders one
  level down from how it opens. A chapter therefore opens at level 0 — for example
  `= Introduction` — even though it renders as a level-1 section. Keep that relationship:
  opening a chapter one level too high fails validation on the including document, reported
  as a section title out of sequence.
- Do not rename files or change `include::` paths unless you are deliberately restructuring
  the book — a rename without updating every reference breaks the build.
- Once you are done, run `snowball check` to validate the manual still builds cleanly. It
  renders every book master and discards the output — the fastest way to catch a broken
  include or an out-of-sequence section before treating this as finished. If the project
  wraps that in a task runner (a `docs-check` recipe, say), either is fine.
