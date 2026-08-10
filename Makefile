# Mizan Makefile — local build, test, and quality gates.
#
# Mizan is CGO-free: SQLite is the pure-Go modernc.org/sqlite driver, so the
# build target pins CGO_ENABLED=0 to keep binaries statically linkable and
# reproducible across environments.
#
# Quality gates are `go vet`, `gofmt`, `govulncheck`, and golangci-lint. The
# earlier exclusion of golangci-lint was reversed by owner decision (R-LINT);
# see .golangci.yml for the curated linter set and its rationale.
#
# Per-target help text is the inline `## ` comment on each target line; the
# default `help` target auto-generates its listing from those comments, so the
# doc text has a single source of truth and cannot drift.

# Toolchain overrides — set on the command line to substitute a different binary.
GO             ?= go
GOFMT          ?= gofmt
BIN_DIR        := bin

# Version metadata injected into the binary via -ldflags -X. The git calls are
# guarded (2>/dev/null || echo <fallback>) so `make build` still works outside a
# git checkout or with no tags. Override any of these on the command line
# (e.g. `make build VERSION=v1.2.3`).
VERSION_PKG    := github.com/ghchinoy/mizan/internal/version
VERSION        ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT         ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE           ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)
LDFLAGS        := -X $(VERSION_PKG).version=$(VERSION) \
                  -X $(VERSION_PKG).commit=$(COMMIT) \
                  -X $(VERSION_PKG).date=$(DATE)

.DEFAULT_GOAL := help

.PHONY: help build install desktop test integration-test vet fmt fmt-check lint vuln clean

help: ## list available targets (default)
	@echo "Mizan — available make targets:"
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*## "} {printf "  %-18s %s\n", $$1, $$2}'

build: ## Build the mizan CLI to bin/mizan (CGO_ENABLED=0)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/mizan ./cmd/mizan

install: ## go install the mizan CLI into GOBIN/GOPATH (CGO_ENABLED=0)
	CGO_ENABLED=0 $(GO) install -ldflags "$(LDFLAGS)" ./cmd/mizan

# desktop: Wails wiring lands in a later phase; this target adds NO Wails
# toolchain dependency (mizan-desktop is a Wails-free stub for now).
desktop: ## Build the mizan-desktop stub to bin/mizan-desktop
	$(GO) build -o $(BIN_DIR)/mizan-desktop ./cmd/mizan-desktop

test: ## Run unit tests (go test ./...)
	$(GO) test ./...

# integration-test: requires a Google Cloud project — set PROJECT_ID or
# MIZAN_PROJECT_ID, otherwise the integration-tagged tests skip themselves.
integration-test: ## Run integration-tagged tests (needs PROJECT_ID/MIZAN_PROJECT_ID)
	$(GO) test -tags integration ./...

vet: ## Run go vet ./...
	$(GO) vet ./...

fmt: ## Format all Go source in place (gofmt -w .)
	$(GOFMT) -w .

# fmt-check: fail (non-zero exit) when any file is not gofmt-clean, so this is a
# usable CI gate — `gofmt -l` alone always exits 0.
fmt-check: ## List files needing formatting; fail if any (gofmt -l .)
	@out=$$($(GOFMT) -l .); [ -z "$$out" ] || { echo "$$out"; exit 1; }

# lint: requires golangci-lint (v2.x) —
#   go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
# Config and curated linter set live in .golangci.yml.
lint: ## Run golangci-lint over the module
	golangci-lint run ./...

# vuln: requires govulncheck — go install golang.org/x/vuln/cmd/govulncheck@latest
vuln: ## Run govulncheck ./...
	govulncheck ./...

clean: ## Remove the bin/ directory
	rm -rf $(BIN_DIR)
