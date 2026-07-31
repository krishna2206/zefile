BINARY  := zefile
# The version comes from version.txt — the single source of truth release-please
# maintains — so a local build reports the same version a Docker build does,
# regardless of which git tags happen to be fetched. The `v` prefix matches the
# release tags. Override with `make VERSION=… build` when needed.
VERSION ?= v$(shell cat version.txt 2>/dev/null || echo 0.0.0)
LDFLAGS := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := help

## build: compile the binary into bin/ (Go only; run `make dist` to include the interface)
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/zefile

## run: build and run
run: build
	./bin/$(BINARY)

## web: build the interface into internal/web/dist
web:
	rm -rf internal/web/dist/assets internal/web/dist/index.html
	cd web && pnpm install --frozen-lockfile && pnpm run build

## dist: build the interface, then a binary embedding it
dist: web build

## generate: regenerate sqlc code from the schema and queries
generate:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

## test: run the test suite with race detection
test:
	go test -race ./...

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go source
fmt:
	go fmt ./...

## vuln: check dependencies for known vulnerabilities
vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

## check: everything CI runs
check: fmt vet test vuln

## clean: remove build artefacts
clean:
	rm -rf bin/ internal/web/dist/assets internal/web/dist/index.html

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'

.PHONY: build web dist run generate test vet fmt vuln check clean help
