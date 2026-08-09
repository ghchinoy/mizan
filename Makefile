# Mizan Makefile — local build, test, and quality gates.
#
# Mizan is CGO-free: SQLite is the pure-Go modernc.org/sqlite driver, so the
# build target pins CGO_ENABLED=0 to keep binaries statically linkable and
# reproducible across environments.
#
# Quality gates are intentionally limited to `go vet`, `gofmt`, and
# `govulncheck` — golangci-lint is deliberately NOT used.

# Run `gofmt`/`go vet`/etc. against the whole module.
GO             ?= go
BIN_DIR        := bin

.DEFAULT_GOAL := help

.PHONY: help build install desktop test integration-test vet fmt fmt-check vuln clean

## help: list available targets (default)
help:
	@echo "Mizan — available make targets:"
	@echo "  build             Build the mizan CLI to bin/mizan (CGO_ENABLED=0)"
	@echo "  install           go install the mizan CLI into GOBIN/GOPATH"
	@echo "  desktop           Build the mizan-desktop stub to bin/mizan-desktop"
	@echo "  test              Run unit tests (go test ./...)"
	@echo "  integration-test  Run integration-tagged tests (needs PROJECT_ID)"
	@echo "  vet               Run go vet ./..."
	@echo "  fmt               Format all Go source in place (gofmt -w .)"
	@echo "  fmt-check         List files needing formatting (gofmt -l .)"
	@echo "  vuln              Run govulncheck ./..."
	@echo "  clean             Remove the bin/ directory"

## build: compile the mizan CLI (CGO-free) to bin/mizan
build:
	CGO_ENABLED=0 $(GO) build -o $(BIN_DIR)/mizan ./cmd/mizan

## install: install the mizan CLI onto the local system
install:
	$(GO) install ./cmd/mizan

## desktop: build the mizan-desktop stub (Wails wiring lands in a later phase;
## this target adds NO Wails toolchain dependency).
desktop:
	$(GO) build -o $(BIN_DIR)/mizan-desktop ./cmd/mizan-desktop

## test: run the unit test suite
test:
	$(GO) test ./...

## integration-test: run integration-tagged tests. These require a Google Cloud
## project: set PROJECT_ID or MIZAN_PROJECT_ID, otherwise the integration tests
## skip themselves.
integration-test:
	$(GO) test -tags integration ./...

## vet: run go vet across the module
vet:
	$(GO) vet ./...

## fmt: format all Go source in place
fmt:
	gofmt -w .

## fmt-check: list files that are not gofmt-clean (empty output = clean)
fmt-check:
	gofmt -l .

## vuln: scan for known vulnerabilities. Requires govulncheck:
## go install golang.org/x/vuln/cmd/govulncheck@latest
vuln:
	govulncheck ./...

## clean: remove build output
clean:
	rm -rf $(BIN_DIR)
