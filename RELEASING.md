# Releasing snowball

`snowball` is distributed as cross-compiled static binaries via
[goreleaser](https://goreleaser.com). Each tag also publishes a GitHub release and updates
the Homebrew formula in a separate tap repo (`github.com/codcod/homebrew-tap` →
`brew install codcod/tap/snowball`).

> Two things differ from the sibling Go projects that share this release shape. First,
> snowball **ships no user manual of its own**: `README.md` and `CHANGELOG.md` are its whole
> documentation surface. Because snowball renders *other* projects' books rather than one of
> its own, there is no docs-attach step and no `docs-release.yml` workflow here. Second,
> `.goreleaser.yaml` does **not** set `mode: replace`, so a re-run behaves differently (see
> [Re-running a release](#re-running-a-release)).

## Cutting a release

Cutting a release is a deliberate human action. Tagging and pushing are never automated.

**1. Stamp the changelog.** In [`CHANGELOG.md`](CHANGELOG.md), retitle the `[Unreleased]`
section to `[X.Y.Z] - YYYY-MM-DD`, add a fresh empty `[Unreleased]` above it, and add a link
reference at the bottom following the existing pattern:

```
[X.Y.Z]: https://github.com/codcod/snowball/compare/vA.B.C...vX.Y.Z
```

Reconcile the entries by hand against what actually shipped since the last tag:

```sh
git log v0.2.2..HEAD --oneline
```

Every user-visible change should already have an entry. If a change altered behaviour and
left the changelog untouched, it was not finished. Commit the stamp, so the tag includes it.

**2. Tag and push.** Everything from here is tag-driven: the
[`release`](.github/workflows/release.yml) workflow runs goreleaser on any `v*` tag.

```sh
git tag v0.2.3
git push origin v0.2.3
```

The version is stamped into the binary via `-ldflags -X main.Version={{ .Version }}`. Never
hand-edit a version constant — the tag is the single source of truth.

## What a release produces

For `linux`/`darwin`/`windows` × `amd64`/`arm64`:

- a **GitHub release** with `.tar.gz` archives — each bundling `README.md`, `CHANGELOG.md`
  and `LICENSE` alongside the binary — plus `checksums.txt`;
- an updated **Homebrew formula** committed to the tap. The formula declares `ruby` and
  `node` as dependencies. Its caveat tells the user to run `snowball setup` once to install
  the pinned gems, mermaid-cli and Chrome;
- a **prerelease**, automatically, when the tag looks like one (`prerelease: auto` — so
  `v1.0.0-rc.1` is marked as such without any extra step).

Before publishing anything, goreleaser runs `go mod tidy` and `go test ./...` as pre-hooks. A
failing test aborts the release before a single artifact is uploaded.

## Re-running a release

The workflow can be re-run for an **existing** tag via *Actions → release → Run workflow*.
Use `workflow_dispatch` and pass the tag name. This is useful after fixing a missing or
expired secret.

Unlike the sibling projects, snowball's `.goreleaser.yaml` does **not** set `mode: replace`
under `release:`. A re-run against a tag whose assets are already published can therefore
fail with `422 already_exists` rather than overwriting them. Either delete the existing
release's assets first, or add `mode: replace` to the `release:` block if re-runs become
routine.

## Validating locally (no publish)

There are no `dist-*` recipes in the [`justfile`](justfile). Invoke goreleaser directly
(`brew install goreleaser`):

```sh
goreleaser check                       # is .goreleaser.yaml valid?
goreleaser release --snapshot --clean  # cross-compile into ./dist, upload nothing
```

Run the ordinary gates before tagging, too — CI runs the same static checks plus a build,
the test suite, and a `version`/`--help` smoke:

```sh
just build && just test && just lint
```

## What the release depends on

- **`HOMEBREW_TAP_GITHUB_TOKEN`** — a repository secret on `codcod/snowball`: a PAT with
  `repo` scope on `codcod/homebrew-tap`. Confirmed working; every release through `v0.2.2`
  published its formula with it. An expired token is the most likely cause of a release that
  builds fine but never reaches the tap.
- **`GITHUB_TOKEN`** — provided automatically by Actions; the workflow grants it
  `contents: write` to create the release and upload assets.

## Versioning

Semantic versioning. While the version is below `1.0.0`, breaking changes may land in a minor
release. They must be labelled `### Breaking` in the changelog and carry a migration hint in
the error the user actually hits, rather than being left as silent behaviour drift.
