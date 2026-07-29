BINARY  := zefile
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := help

## build: compile the binary into bin/
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/zefile

## run: build and run
run: build
	./bin/$(BINARY)

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
	rm -rf bin/

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'

.PHONY: build run generate test vet fmt vuln check clean help
