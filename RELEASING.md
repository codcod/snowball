# Releasing snowball

`snowball` is distributed as cross-compiled static binaries via
[goreleaser](https://goreleaser.com). Each tag also publishes a GitHub release and updates
the Homebrew formula in a separate tap repo (`github.com/codcod/homebrew-tap` →
`brew install codcod/tap/snowball`).

> This process is the same shape as [pickle](https://github.com/codcod/pickle)'s and
> morty/summer's `RELEASING.md` — snowball's own carries more operational detail because it
> has actually shipped 8+ releases, not because the process itself differs.

> Like morty and summer, snowball's own AsciiDoc user manual (`docs/user-manual.adoc`) is
> built and attached by [`docs-release.yml`](.github/workflows/docs-release.yml) — snowball
> dogfoods its own scaffold output. The workflow triggers when `release.yml` completes
> (`workflow_run`), not on goreleaser's `release: published` event. Releases created with
> the default `secrets.GITHUB_TOKEN` do not trigger further workflow runs, so a
> `release: published` trigger alone never fires; snowball's past 8 releases confirm that.
> The job also runs on `macos-latest`, not `ubuntu-latest`, because it needs
> preinstalled Homebrew. `.goreleaser.yaml` now sets `mode: replace` and
> `replace_existing_artifacts: true`, matching morty/summer (see
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
git log "$(git describe --tags --abbrev=0)..HEAD" --oneline
```

Every user-visible change should already have an entry. If a change altered behaviour and
left the changelog untouched, it was not finished. Commit the stamp, so the tag includes it.

**2. Tag and push.** Everything from here is tag-driven: the
[`release`](.github/workflows/release.yml) workflow runs goreleaser on any `v*` tag.

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
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
- once that release run **succeeds**, [`docs-release.yml`](.github/workflows/docs-release.yml)
  runs next. It builds the AsciiDoc user manual with snowball itself and attaches the
  PDF/EPUB to the same release. It soft-fails, so a broken manual never unpublishes or
  blocks the release;
- a **prerelease**, automatically, when the tag looks like one (`prerelease: auto` — so
  `v1.0.0-rc.1` is marked as such without any extra step).

Before publishing anything, goreleaser runs `go mod tidy` and `go test ./...` as pre-hooks. A
failing test aborts the release before a single artifact is uploaded.

## Re-running a release

The workflow can be re-run for an **existing** tag via *Actions → release → Run workflow*.
Use `workflow_dispatch` and pass the tag name. This is useful after fixing a missing or
expired secret.

`.goreleaser.yaml`'s `mode: replace` **plus** `replace_existing_artifacts: true` lets a
re-run overwrite already-published artifacts instead of failing with `422 already_exists`.
`mode: replace` alone affects only the release body/changelog strategy; without the second
key, every asset upload on a re-run still 422s. This is proven behavior: on morty, a
`workflow_dispatch` re-run of an existing tag re-uploaded every asset cleanly with both keys
set.

> **Changed recently.** Before this, snowball's `.goreleaser.yaml` set neither key, so a
> re-run against a tag whose assets were already published failed with `422 already_exists`
> unless the existing release's assets were deleted first. Anyone diffing release history
> against an older tag should expect that older behavior instead.

## Validating locally (no publish)

```sh
just dist-check      # goreleaser check — is .goreleaser.yaml valid?
just dist-snapshot   # cross-compile into ./dist, upload nothing
```

> **`goreleaser check` currently exits non-zero, and that is expected.** It reports
> `configuration is valid, but uses deprecated properties` because the `brews:` block has
> been deprecated in favour of `homebrew_casks`. That warning is expected, and the
> configuration still works: releases up to and including `v0.3.0` published their formula
> from it. When `check` fails, focus on *which* line failed: this deprecation notice is
> known; anything else is not. The release workflow tracks the latest v2
> (`version: "~> v2"`), so this will stop being only a warning whenever GoReleaser removes
> the key.

> **Unset `GITLAB_TOKEN` before running goreleaser locally — this is still required.**
> GoReleaser picks its forge from the environment. If `GITLAB_TOKEN` or
> `GITLAB_PERSONAL_ACCESS_TOKEN` is left set for some other project, it treats this
> repository as GitLab-hosted and generates a Homebrew formula whose download URLs point at
> `gitlab.com/codcod/snowball` — a repository that does not exist. `.goreleaser.yaml` now sets
> `release.github: {owner: codcod, name: snowball}` explicitly, but **this does not change
> the above**: it declares intent, it does not override goreleaser's env-based detection,
> and a stray GitLab token still wins (confirmed live — pinning the forge did not stop it).
> The `dist-check`/`dist-snapshot` justfile recipes defensively `env -u GITLAB_TOKEN -u
> GITLAB_PERSONAL_ACCESS_TOKEN`, and that guard remains required, not optional. A misdetected
> run is harmless under `--snapshot`, which uploads nothing; a local non-snapshot run without
> the guard would not produce a usable release at all. CI is unaffected (it sets no GitLab
> token), and the tap is correct today. When in doubt:
>
> ```sh
> env -u GITLAB_TOKEN -u GITLAB_PERSONAL_ACCESS_TOKEN goreleaser release --snapshot --clean
> ```
>
> Then confirm the generated formula points where you expect:
>
> ```sh
> grep -o 'url "https://[^/]*' dist/homebrew/snowball.rb | sort -u   # expect github.com
> ```

Run the ordinary gates before tagging, too. CI runs the same static checks, plus a build,
the test suite, a `version`/`--help` smoke test, a `.goreleaser.yaml` validation
(`goreleaser-check`), and a workflow-YAML lint (`ci-surface`, actionlint). `ci.yml` and
`release.yml` also carry a `concurrency:` guard against overlapping runs:

```sh
just build && just test && just lint
```

## What the release depends on

- **`HOMEBREW_TAP_GITHUB_TOKEN`** — a repository secret on `codcod/snowball`: a PAT with
  `repo` scope on `codcod/homebrew-tap`. Confirmed working; every release through `v0.3.0`
  published its formula with it. An expired token is the most likely cause of a release that
  builds fine but never reaches the tap.
- **`GITHUB_TOKEN`** — provided automatically by Actions; the workflow grants it
  `contents: write` to create the release and upload assets.

## Versioning

Semantic versioning. While the version is below `1.0.0`, breaking changes may land in a minor
release. They must be labelled `### Breaking` in the changelog and include a migration hint
in the error the user actually sees, rather than becoming silent behaviour drift.
