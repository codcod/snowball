version := `git describe --tags --always --dirty 2>/dev/null || echo "dev"`
bin     := "snowball"

# List recipes
@_:
    just --list

# Build the binary
[group('build')]
build:
    go build -ldflags "-X main.Version={{version}}" -o {{bin}} ./cmd/snowball

# Run tests
[group('qa')]
test:
    go test ./...

# Static checks: formatting drift + go vet (parity with CI)
[group('qa')]
lint: fmt-check
    go vet ./...

# Check formatting
[group('qa')]
fmt-check:
    @unformatted="$(gofmt -l .)"; \
      if [ -n "$unformatted" ]; then echo "gofmt drift:"; echo "$unformatted"; exit 1; fi

# Format in place
[group('qa')]
fmt:
    gofmt -w .

# Print the version the binary reports
[group('release')]
show-version: build
    ./{{bin}} version

# Validate the goreleaser config (also run in CI).
# GitLab tokens are unset so goreleaser detects the GitHub forge deterministically
# (it prefers GitLab whenever GITLAB_TOKEN is present in the environment).
# Exit code 2 ("valid but uses deprecated properties") is tolerated: the `brews`
# pipe is used deliberately (see .goreleaser.yaml). Real errors exit 1 and fail.
[group('release')]
dist-check:
    env -u GITLAB_TOKEN -u GITLAB_PERSONAL_ACCESS_TOKEN goreleaser check || [ $? -eq 2 ]

# Local, unpublished cross-compiled build into ./dist (no tokens, no upload).
[group('release')]
dist-snapshot:
    env -u GITLAB_TOKEN -u GITLAB_PERSONAL_ACCESS_TOKEN goreleaser release --snapshot --clean

# Validate the AsciiDoc manual via snowball (broken includes/xrefs fail the check)
[group('docs')]
docs-check:
    snowball check

# Render the user manual to PDF + EPUB into dist/docs/
[group('docs')]
docs-build:
    snowball build -o dist/docs

# Remove build output
[group('lifecycle')]
clean:
    rm -f {{bin}}

# vim: set ft=make:et:ai
