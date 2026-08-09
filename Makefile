# Mizan Makefile — local build, test, and quality gates.
#
# Mizan is CGO-free: SQLite is the pure-Go modernc.org/sqlite driver, so the
# build target pins CGO_ENABLED=0 to keep binaries statically linkable and
# reproducible across environments.
#
# Quality gates are intentionally limited to `go vet`, `gofmt`, and
# `govulncheck` — golangci-lint is deliberately NOT used.
#
# Per-target help text is the inline `## ` comment on each target line; the
# default `help` target auto-generates its listing from those comments, so the
# doc text has a single source of truth and cannot drift.

# Toolchain overrides — set on the command line to substitute a different binary.
GO             ?= go
GOFMT          ?= gofmt
BIN_DIR        := bin

.DEFAULT_GOAL := help

.PHONY: help build install desktop test integration-test vet fmt fmt-check vuln clean

help: ## list available targets (default)
	@echo "Mizan — available make targets:"
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*## "} {printf "  %-18s %s\n", $$1, $$2}'

build: ## Build the mizan CLI to bin/mizan (CGO_ENABLED=0)
	CGO_ENABLED=0 $(GO) build -o $(BIN_DIR)/mizan ./cmd/mizan

install: ## go install the mizan CLI into GOBIN/GOPATH
	$(GO) install ./cmd/mizan

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

# vuln: requires govulncheck — go install golang.org/x/vuln/cmd/govulncheck@latest
vuln: ## Run govulncheck ./...
	govulncheck ./...

clean: ## Remove the bin/ directory
	rm -rf $(BIN_DIR)
