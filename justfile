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
