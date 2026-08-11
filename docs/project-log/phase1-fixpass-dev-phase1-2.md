# Phase 1 FIX PASS — quality-gate findings on PR #35 (dev-phase1-2)

**Agent:** dev-phase1-2 (developer) · **Date:** 2026-08-11
**Branch:** `feat/preflight-src-and-project` (commits added ON TOP of `d0ec7bc`, the
test-engineer's `--project` wiring-test commit; no rebase, no force-push, no new branch)
**Repo:** github.com/ghchinoy/mizan · **PR:** #35

PR #35 passed all three gates (review APPROVE, security NO BLOCKING, tests). Per
program policy every finding — including non-blocking — is addressed before the PR
is reported ready. Review finding #2 (persistent-flag wiring test) was already
fixed by the test engineer (`c484930`). This pass closes the remaining THREE.

## Finding 1 (Review, Optional — correctness of ITEM A) — provenance for a global-pinned model resource

**Problem.** On the NATIVE path a fully-qualified model RESOURCE explicitly pinned
to `locations/global` (e.g. `projects/p/locations/global/publishers/google/models/gemini-2.5-flash`)
surfaced `location=global` — the SAME string a routing-forced global produces — so
`preflightSources` mislabeled it `src=global-route` instead of `src=model`.
`ResolvedTarget` carried the resolved `global` location STRING but not its
PROVENANCE (routing-global vs resource-derived). The regional-resource case
(e.g. embedded `europe-west4`) was already correct.

**Fix (bounded — a field on the resolve result + population + preflight read).**
- `internal/eval/model.go`: added `ResolvedTarget.LocationFromModelResource bool`.
  `Engine.Resolve` sets it `true` when `parseFullModelResource` supplies the
  location. The global-only ROUTING branch (`isGlobalOnlyModel`) clears it back to
  `false` **only when routing actually changes a non-global location to global** —
  so a resource explicitly pinned to `global` keeps its resource provenance, while
  a regional resource that routing overrides to global flips to routing-forced. The
  cases are NOT collapsed.
- `cmd/mizan/eval.go` `preflightSources`: added a `case t.LocationFromModelResource:
  locSrc = "model"` BEFORE the `t.Location == eval.GenaiLocation` (global-route)
  fallback, so a resource-pinned global reads `src=model` and a routing-forced
  global still reads `src=global-route`. Existing branch ordering (config-match
  first, then genai) preserved to avoid behavior drift for same-location resources.

**Scope guard:** honored. The change is confined to one struct field, its
population in `Engine.Resolve`, and one read in `preflightSources`. No other
`Engine.Resolve` consumers or shared infrastructure required changes, so no
escalation to `mizan-em-followup` was needed.

**Tests / goldens.**
- `internal/eval/model_test.go`: new `TestResolveLocationProvenance` (regional
  resource ⇒ from-model; global-pinned resource ⇒ from-model; global-only bare
  model ⇒ routing; regional resource overridden by global-only routing ⇒ routing).
  Extended `TestResolveNativeFQModelReflectsEmbeddedLocation` to assert the flag.
- `cmd/mizan/eval_preflight_test.go`: new case — global-pinned model resource on
  native path ⇒ `src=model` (not `global-route`) — alongside the existing
  global-route and regional-resource cases.
- New golden `cmd/mizan/testdata/printPreflight_global_model.golden`:
  `mizan: autorater → project=my-project (src=env-file) location=global (src=model) model=gemini-2.5-flash (path=native)`.
  No EXISTING golden changed (`printPreflight` output is unchanged for prior cases
  because it echoes a pre-computed `src` string; only `preflightSources` logic
  changed). Verified: `-update` produced only the one new file.

## Finding 2 (Security, Low) — validate `--project` value locally

**Fix.** `internal/config/config.go`: added `ValidateProjectID(project string) error`
enforcing the canonical GCP project-id format via
`^[a-z][a-z0-9-]{4,28}[a-z0-9]$` (6–30 chars, a lowercase letter first, lowercase
letters / digits / `-`, no trailing `-`). Empty is accepted (means "no override,
keep env/.env/default"). Placed alongside the sibling config-value validators
`ValidateEndpoint` / `ValidateGenaiBaseURL` (project is a config value). Wired into
both `eval run` and `eval pairwise` RunE right after `mustConfig()` and before
`applyProjectOverride`, mirroring the local `ValidateModel` guard on `--model`, so
a malformed `--project` fails LOCALLY with a crisp error instead of only failing
server-side with an opaque `InvalidArgument`.

**Test.** `internal/config/config_test.go`: `TestValidateProjectID` — accept
(typical / min-6 / exactly-30 / digits+hyphens) and reject (empty-is-ok, too-short,
31-chars, leading digit, leading/trailing hyphen, uppercase, underscore, space,
newline, slash).

## Finding 3 (Review, Nit) — `keys.go` MarshalText comment

`internal/config/keys.go` `Source.MarshalText` doc comment enumerated
`"env"/"env-file"/"default"` but omitted `flag`. Updated to
`"flag"/"env"/"env-file"/"default"`. Comment-only — behavior was already correct.

## Verification (full local gate — all green)

| Gate | Command | Result |
|---|---|---|
| build | `go build ./...` | OK |
| vet | `go vet ./...` | OK |
| gofmt | `gofmt -l .` | empty (clean) |
| test | `go test ./...` | all packages ok |
| goldens | `go test ./cmd/mizan -run TestGolden -update` then re-run | pass (1 new golden, no existing golden changed) |
| lint | `golangci-lint run` | 0 issues |
| vuln | `govulncheck ./...` | No vulnerabilities found (0 called; 1 in a required module, uncalled) |

`govulncheck` was not preinstalled in this environment; installed via
`go install golang.org/x/vuln/cmd/govulncheck@latest` and run — clean.

## Files changed

- `internal/eval/model.go` — `ResolvedTarget.LocationFromModelResource` field +
  population in `Engine.Resolve` (F1).
- `internal/eval/model_test.go` — `TestResolveLocationProvenance`; extended FQ test (F1).
- `cmd/mizan/eval.go` — `preflightSources` provenance branch (F1); `ValidateProjectID`
  calls in both RunE paths (F2).
- `cmd/mizan/eval_preflight_test.go` — global-pinned resource case (F1).
- `cmd/mizan/golden_test.go` + `testdata/printPreflight_global_model.golden` — new golden (F1).
- `internal/config/config.go` — `ValidateProjectID` (F2).
- `internal/config/config_test.go` — `TestValidateProjectID` (F2).
- `internal/config/keys.go` — MarshalText comment (F3).

## Commits (on top of `d0ec7bc`)

1. `fix(eval): attribute global-pinned model-resource location to src=model` (F1)
2. `feat(config): validate --project value locally (ValidateProjectID)` (F2)
3. `docs(config): include 'flag' in Source.MarshalText comment enumeration` (F3)
