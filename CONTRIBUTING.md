# Contributing to Mizan

Thanks for your interest in improving Mizan. This guide describes how to set up a
local environment, the checks your change must pass, and the conventions this
repository follows. Everything below mirrors what the build and CI actually do —
if you find a discrepancy, the code and CI configuration are the source of truth.

## Prerequisites

- **Go 1.26+.** `go.mod` declares `go 1.26` with `toolchain go1.26.6`, so the
  right compiler is fetched automatically when you build.
- **No C compiler required.** Mizan is CGO-free — SQLite is the pure-Go
  `modernc.org/sqlite` driver, and the build pins `CGO_ENABLED=0`.
- **For integration tests only:** a Google Cloud project with the Vertex AI API
  enabled and Application Default Credentials (see below).

## Setup

```sh
git clone https://github.com/ghchinoy/mizan.git
cd mizan
make build          # builds ./bin/mizan (CGO_ENABLED=0)
make test           # unit tests: go test ./...
```

Run `make help` for the full, self-documenting target list. See the README
["Local development"](README.md#local-development) section for more detail.

## Checks to run before opening a PR

Run these locally and keep them green — they reproduce the CI gates. Each command
is a real Makefile target (`make help` lists them all):

```sh
make fmt-check      # fail if any file is not gofmt-clean (run `make fmt` to fix)
make vet            # go vet ./...
make test           # unit tests
make lint           # golangci-lint (v2)
make vuln           # govulncheck
```

### CI gates and how to reproduce them locally

CI runs three jobs on every pull request (and on pushes to `main`). The table
maps each gate to the local command that reproduces it.

| CI gate (from `.github/workflows/ci.yml`) | Reproduce locally | Blocks merge? |
| --- | --- | --- |
| Build (`CGO_ENABLED=0 go build ./...`) | `make build` builds the CLI; for the full package build CI runs, use `CGO_ENABLED=0 go build ./...` | Yes |
| Vet (`go vet ./...`) | `make vet` | Yes |
| gofmt check (`make fmt-check`) | `make fmt-check` (fix with `make fmt`) | Yes |
| govulncheck (`govulncheck ./...`) | `make vuln` | Yes |
| Test + coverage (`go test -coverprofile ./...`) | `make test`, or `make cover` for the coverage total | Yes (a test failure fails CI) |
| Coverage floor | `make cover` | No — soft, non-blocking warning only |
| golangci-lint (v2) | `make lint` | Yes |
| doc-drift guard | See below | Yes, on PRs (with an override) |

Notes:

- The **coverage floor is a soft nudge, not a gate.** CI prints total coverage
  and warns if it dips below the floor, but never fails the build on coverage
  alone.
- The **doc-drift guard** runs on pull requests only. If your PR changes
  `cmd/mizan/` or `internal/eval/` but updates none of the tracked docs
  (`docs/llm-as-judge-scenarios.md`, or a `docs/*guide.md` such as
  `docs/user-guide.md` / `docs/testing-guide.md`), the job fails. If docs
  genuinely do not apply, override it by adding a `docs: N/A` label to the PR or
  a standalone `docs: N/A` line in the PR body. There is no local Make target for
  this check — just include the relevant doc update in your PR.
- CI tracks the Go toolchain from `go.mod`, so local and CI builds use the same
  compiler.

### Integration tests

Integration-tagged tests call the real Vertex AI API and are **not** part of the
required CI gates. They skip themselves unless a project is configured:

```sh
export MIZAN_PROJECT_ID=<your-project-id>   # or PROJECT_ID
gcloud auth application-default login        # Application Default Credentials
make integration-test                        # go test -tags integration ./...
```

## Branch naming

Use a `type/short-description` branch name, lower-case and hyphenated. Common
types seen in this repo:

- `feat/...` — a new feature, e.g. `feat/pairwise-eval`
- `docs/...` — documentation, e.g. `docs/license-contributing`
- `chore/...` — maintenance, e.g. `chore/bump-deps`

## Pull requests and review

- **Rebase onto the latest `main`** before opening or updating your PR.
- **Changes are reviewed** — the author is not the reviewer. Keep PRs focused and
  reasonably small; for anything beyond a small fix, consider opening an issue
  first to discuss the approach.
- **Docs follow code.** Keep documentation accurate against the shipped binary —
  verify documented commands, flags, and output against the actual build, and
  update the relevant doc in the same PR when you change behavior (the doc-drift
  guard enforces this for `cmd/mizan/` and `internal/eval/`).
- Make sure all required CI gates listed above pass.

## Sharing metric templates

Metric templates are not code changes to this repo. Author a pack with
`mizan pack init|add|validate` and open a PR against
[`github.com/ghchinoy/mizan-templates`](https://github.com/ghchinoy/mizan-templates).
Mizan never pushes on your behalf.

## License

By contributing, you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE), the same license that covers this project.
