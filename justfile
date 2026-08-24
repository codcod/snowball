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

# Remove build output
clean:
    rm -f {{bin}}

# vim: set ft=make:et:ai

# Validate the AsciiDoc manual via snowball (broken includes/xrefs fail the check)
docs-check:
    snowball check

# Render the user manual to PDF + EPUB into dist/docs/ (never committed)
docs-build:
    snowball build -o dist/docs
