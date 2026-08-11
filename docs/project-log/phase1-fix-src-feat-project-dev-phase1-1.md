# Phase 1 CODE — FIX-SRC + FEAT-PROJECT (dev-phase1-1)

**Agent:** dev-phase1-1 (developer) · **Date:** 2026-08-11
**Branch:** `feat/preflight-src-and-project` (from `origin/main` @ `1b15a52a`, the #34 merge)
**Repo:** github.com/ghchinoy/mizan

Two tightly-coupled changes to the eval pre-flight source-attribution mechanism
(the `preflightSources` / `config.SourceOf` machinery FIX-CONFIG / PR #33 added).
Landed as ONE combined PR because both touch `preflightSources` and the
`printPreflight` goldens.

## ITEM A — FIX-SRC: accurate src label for forced global-only routing

**Problem.** On the NATIVE path a known global-only judge is auto-routed to the
global host (`isGlobalOnlyModel` in `internal/eval/route.go`; `Engine.Resolve`
sets `location=global`, `path=native`). The pre-flight previously attributed that
`location=global` to `src=model`, implying a fully-qualified model *resource*
carried the location — which was false; the override came from global-only
ROUTING.

**Fix.** In `cmd/mizan/eval.go` `preflightSources`, added a branch: when
`t.Path != "genai"` (native) and `t.Location == eval.GenaiLocation` (the single
global-location const, which aliases the R-GLOBAL host location in `route.go`),
label it `src=global-route`. A location genuinely derived from a fully-qualified
model resource (e.g. an embedded `europe-west4`) still falls through to
`src=model`. The two cases are **not** collapsed.

## ITEM B — FEAT-PROJECT: per-invocation `--project` flag

**Scope chosen:** a **persistent flag on the `eval` command** (`newEvalCmd`), so
it applies to both `eval run` and `eval pairwise` without polluting the unrelated
`registry`/`config` command trees the way a root-level persistent flag would.
(The brief allowed either root-persistent or per-eval; per-eval-persistent is the
cleanest fit for the current wiring and is documented as such.)

**Precedence (documented in code + docs + tests):**

```
--project flag  >  exported MIZAN_PROJECT_ID / PROJECT_ID  >  .env  >  default
```

**Mechanism.** `applyProjectOverride(cfg, projectOverride)` runs right after
`mustConfig()` in both eval RunE paths. When the flag is non-empty it replaces
`cfg.ProjectID` and re-attributes the `project-id` source to a new
`config.SourceFlag` (String → `"flag"`), so the pre-flight echo shows `src=flag`.
This reuses the FIX-CONFIG `config.Source` / `cfg.Sources` mechanism — no parallel
path. An empty flag leaves `cfg` untouched, preserving env/.env precedence.

`SourceFlag` was appended to the `Source` enum (highest precedence; existing enum
values unchanged) with `String()`/`MarshalText()` returning `"flag"`.

## Files changed

- `cmd/mizan/eval.go` — `preflightSources` global-route branch (A); `projectOverride`
  var + `--project` persistent flag on `eval` + `applyProjectOverride` + calls in
  both RunE paths (B); expanded doc comments.
- `internal/config/keys.go` — `SourceFlag` enum value + `String()` case (B).
- `cmd/mizan/eval_preflight_test.go` — new case: global-only model forces global on
  native path ⇒ `src=global-route` (A).
- `cmd/mizan/eval_project_flag_test.go` — new: flag beats exported env; flag beats
  .env; flag supplies project when none configured; absent flag unchanged; nil
  Sources tolerated; `src=flag` shown in preflight (B).
- `internal/config/keys_test.go` — `TestSourceString` pins `flag`/`env`/`env-file`/
  `default` tokens for `String()` + `MarshalText()` (B).
- `cmd/mizan/golden_test.go` + `testdata/printPreflight_global_route.golden` (A) +
  `testdata/printPreflight_project_flag.golden` (B) — new golden cases.
- `docs/user-guide.md` — documents `--project` + precedence, removes the
  "--project deferred" follow-up note (B); documents `src=global-route` (A).
- `docs/llm-as-judge-scenarios.md` — pre-flight `src=` token enumeration now lists
  `flag`/`model`/`global-path`/`global-route`; global-only paragraph notes
  `src=global-route` (A + B).
- **Not touched:** `docs/testing-guide.md` (its preflight src note is a separate
  later phase — avoided same-file race).

## New goldens

```
printPreflight_global_route: mizan: autorater → project=my-project (src=env-file) location=global (src=global-route) model=gemini-3.5-flash (path=native)
printPreflight_project_flag: mizan: autorater → project=flag-project (src=flag) location=us-central1 (src=default) model=gemini-2.5-flash (path=native)
```
Existing `printPreflight_native` / `printPreflight_genai` goldens are unchanged
(verified: no diff after `-update`).

## Verification (all green)

| Gate | Command | Result |
|---|---|---|
| build | `go build ./...` | OK |
| vet | `go vet ./...` | OK |
| gofmt | `gofmt -l .` | empty (clean) |
| test | `go test ./...` | all packages ok |
| goldens | `go test ./cmd/mizan -run TestGolden -update` then re-run | pass |
| lint | `golangci-lint run ./...` | 0 issues |
| vuln | `govulncheck ./...` | No vulnerabilities found (0 called) |

CLI smoke: `--project` appears on `eval run` and `eval pairwise` help only, not on
`root`/`registry`/`config`.
