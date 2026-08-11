# Phase 1 Test-Coverage Report — test-phase1-1

**PR:** #35 — `feat/preflight-src-and-project`
**Reviewed at:** af4acc4b005d033581464f10987797ba4d0d921f (dev tip), branched from main @ 1b15a52a
**Test additions pushed at:** c48493034653695423c4776be233e2b6e1d205b7
**Author of tests:** independent test-engineer (did NOT write the code under test)

---

## Verdict

**Existing coverage was nearly complete** — 4 of the 5 required cases plus the
FIX-SRC global-route/model distinction were already covered by real,
regression-catching tests. **One genuine gap was found and closed:** nothing
verified that the FEAT-PROJECT `--project` flag is actually wired onto the `eval`
command tree as a *persistent* flag inherited by both `eval run` and
`eval pairwise`. Added `cmd/mizan/eval_project_flag_wiring_test.go`.

All green gates pass. No goldens needed regeneration (the addition is test-only
and changes no program output).

---

## Behavior-under-test → coverage map

| # | Required case | Covered by | Status |
|---|---|---|---|
| 1 | flag beats exported env | `eval_project_flag_test.go` → `TestApplyProjectOverride/flag_beats_exported_env` | ✅ pre-existing |
| 2 | flag beats .env | `eval_project_flag_test.go` → `TestApplyProjectOverride/flag_beats_.env` | ✅ pre-existing |
| 3 | `src=flag` shown in preflight | `eval_project_flag_test.go` → `TestPreflightSourceFlag`; golden `testdata/printPreflight_project_flag.golden` via `golden_test.go` | ✅ pre-existing |
| 4 | absent flag = unchanged env/.env precedence | `eval_project_flag_test.go` → `TestApplyProjectOverride/absent_flag_leaves_env/.env_precedence_intact` | ✅ pre-existing |
| 5 | global-route vs model-resource src distinction (must NOT collapse) | `eval_preflight_test.go` → `TestPreflightSources` cases `global-only_model_forces_global_on_native_path:_src=global-route` **and** `fully-qualified_model_overrides_location_on_native_path:_src=model`; goldens `printPreflight_global_route.golden` (native+global → `src=global-route`) vs `printPreflight_native.golden`/`_genai` | ✅ pre-existing |
| — | **[GAP] `--project` is a persistent flag inherited by run + pairwise** | **NEW `eval_project_flag_wiring_test.go`** | ✅ added |

Supporting pre-existing coverage: `keys_test.go` asserts `SourceFlag.String() == "flag"`;
`TestApplyProjectOverride` also covers "flag supplies project when none configured"
and "nil Sources map is tolerated" (empty/boundary paths).

---

## Gap found and closed

**Gap:** The `--project` behavior was exercised only through the two helper
functions (`applyProjectOverride`, `preflightSources`) and the `printPreflight`
goldens. None of these touch the cobra command tree. If the registration line

```go
cmd.PersistentFlags().StringVar(&projectOverride, "project", "", …)
```

regressed to a **local** flag, or to a flag on only one subcommand, every existing
test would still pass while `mizan eval pairwise --project X` broke at runtime with
`unknown flag: --project`. The brief calls for "persistent `--project` flag on eval
(run + pairwise)"; that wiring was untested.

**Tests added** — `cmd/mizan/eval_project_flag_wiring_test.go`:

- `TestEvalProjectFlagIsPersistent` (line 15) — asserts `--project` is on the eval
  command's **persistent** flag set (not local) and is inherited by both `run` and
  `pairwise` subcommands via `InheritedFlags()`.
- `TestEvalRunAcceptsProjectFlag` (line 41) — `eval run --project p` parses the flag
  and reaches the `--metric is required` check (no `unknown flag`), proving CLI
  reachability without a backend.
- `TestEvalPairwiseAcceptsProjectFlag` (line 49) — same for `eval pairwise`, proving
  the sibling subcommand inherits the persistent flag.

Both command-level tests are **cgo-free and network-free**: they fail fast on the
required-`--metric` validation before any config load, DB open, or live API call
(same pattern as the existing `TestEvalRunRequiresMetric`).

---

## Mutation checks (a test that never fails is useless)

| Mutation | Expected | Result |
|---|---|---|
| `PersistentFlags().StringVar` → `Flags().StringVar` (local) in `eval.go` | new wiring tests FAIL | ✅ all 3 failed: `no persistent --project flag`, `unknown flag: --project` on run & pairwise |
| Drop the `case t.Location == eval.GenaiLocation` (global-route) branch in `preflightSources` | pre-existing FIX-SRC test FAILS | ✅ `TestPreflightSources/global-only_model_forces_global_on_native_path:_src=global-route` failed with `locSrc = "model", want "global-route"` |
| (both reverted) | suite green | ✅ restored, green |

The second mutation confirms the pre-existing global-route/model distinction is a
*real* guard, not merely present — the two cases genuinely do not collapse.

---

## Green-gate confirmation

Commands run from repo root, `CGO_ENABLED=0` (cgo-free), Go toolchain per `go.mod`:

| Gate | Command | Result |
|---|---|---|
| build | `go build ./...` | exit 0 |
| vet | `go vet ./...` | exit 0 |
| gofmt | `gofmt -l cmd/ internal/` | empty (clean) |
| test | `go test ./...` | all `ok` (cmd/mizan 3.3s; asset, config, eval, registry, sqlite, version, wire all pass) |
| lint | `golangci-lint run ./...` | `0 issues.` |
| vuln | `govulncheck ./...` | `No vulnerabilities found.` (0 affecting code; 1 in a required module not called) |

No goldens changed — the addition is test-only and alters no program output, so the
golden-update path was not needed.

---

## Recommendations (not acted on — test-engineer scope)

- None blocking. The FEAT-PROJECT precedence *between* env / .env / default is
  owned by `LoadConfig` (tested in `internal/config`); the flag layer sits above it
  and is now covered end-to-end from helper → preflight echo → CLI wiring.

## Termination note
Tests committed (c484930) and pushed to `feat/preflight-src-and-project`. **Not merged.**
