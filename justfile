version := `git describe --tags --always --dirty 2>/dev/null || echo "dev"`
bin     := "snowball"

# List recipes
@_:
    just --list

# Build the binary
build:
    go build -ldflags "-X main.Version={{version}}" -o {{bin}} ./cmd/snowball

# Run tests
test:
    go test ./...

# Static checks: formatting drift + go vet (parity with CI)
lint: fmt-check
    go vet ./...

# Check formatting
fmt-check:
    @unformatted="$(gofmt -l .)"; \
      if [ -n "$unformatted" ]; then echo "gofmt drift:"; echo "$unformatted"; exit 1; fi

# Format in place
fmt:
    gofmt -w .

# Print the version the binary reports
show-version: build
    ./{{bin}} version

# Remove build output
clean:
    rm -f {{bin}}

# vim: set ft=make:et:ai

# Validate the AsciiDoc manual via snowball (broken includes/xrefs fail the check)
docs-check:
    snowball check

# Render the user manual to PDF + EPUB into dist/docs/
docs-build:
    snowball build -o dist/docs

# Validate the goreleaser config (also run in CI).
# GitLab tokens are unset so goreleaser detects the GitHub forge deterministically
# (it prefers GitLab whenever GITLAB_TOKEN is present in the environment).
# Exit code 2 ("valid but uses deprecated properties") is tolerated: the `brews`
# pipe is used deliberately (see .goreleaser.yaml). Real errors exit 1 and fail.
dist-check:
    env -u GITLAB_TOKEN -u GITLAB_PERSONAL_ACCESS_TOKEN goreleaser check || [ $? -eq 2 ]

# Local, unpublished cross-compiled build into ./dist (no tokens, no upload).
dist-snapshot:
    env -u GITLAB_TOKEN -u GITLAB_PERSONAL_ACCESS_TOKEN goreleaser release --snapshot --clean
