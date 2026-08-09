# Mizan — Implementation Plan

Status: design for review (pre-implementation)
Date: 2026-08-09
Author: mizan-architect
Companions: design/architecture-final.md, design/collaboration-design.md
Ground truth: docs/research.md. Spikes: docs/spikes.md.

Audience: the eng-manager (`mizan-em`) who hands discrete work items to
developers. Each phase lists work items (WI), cross-phase dependencies, and
**explicit acceptance criteria**. Phase 1 is a **single vertical slice that runs
end to end** before any fan-out; later phases are explicitly conditional on it.

---

## Phase ordering & the fan-out rule

```
  Spike 0 (module layout) ──┐
  Spikes 1-5 (API/registry) ┴─► P1 vertical slice ──► [validated?] ──► P2 collab
                                                          │              P3 batch
                                                          └── fan-out ──► P4 desktop
```

- **Spike verdicts are inputs, not blockers for all of P1.** P1's WI-0/WI-1
  (scaffold, config, store) do not need any spike. P1's eval WIs consume Spike
  1/2 verdicts. If a verdict is still missing when a WI is picked up, the WI is
  blocked on *that* verdict only — raise it, don't guess.
- **The fan-out (P2/P3/P4) does not begin until the P1 slice is validated
  end-to-end** (WI-P1-ACCEPT passes). An interface mismatch in `registry.Service`
  or `eval.Engine` caught here is one fix; caught after three phases build on it,
  it is one fix per phase it reached.

Cross-phase dependency summary:

| Depends on | Consumed by |
|---|---|
| Spike 0 verdict (layout) | P1 WI-0 (scaffold) — or defer split to P4 |
| Spike 1 verdict (placeholder syntax, region) | P1 WI-3 (native eval) |
| Spike 2 verdict (ContentMap, inline ceiling) | P1 WI-3, P1 WI-4 (multimodal) |
| Spike 4 (custom_schema) | P1 WI-5 (genai fallback) |
| Spike 5 (SQLite CRUD) | P1 WI-1 (store) |
| **P1 `registry.Service` + `MetricTemplate`** | **P2, P4 (hard dep)** |
| **P1 `eval.Engine`** | P3 (batch reuses spec materialization) |
| Spike 6 (Wails binding) | P4 |

---

## Phase 1 — Core + CLI MVP (the vertical slice)

**Goal:** one modality's one metric kind runs end to end through the real stack —
config → SQLite store → `registry.Service` → `eval.Engine` → live
`EvaluateInstances` → printed result — proving every interface seam before
anything fans out.

**The slice (WI-1..WI-3 + WI-6): text pointwise.**
`mizan registry create` a text pointwise template → persisted in SQLite →
`mizan eval run` → live score+explanation printed. This is the smallest path
that exercises `config`, `registry.Store`, `registry.Service`, `eval.Engine`,
`native.go`, and the CLI wiring together.

### Work items

- **WI-P1-0 — Scaffold.** `go mod init github.com/ghchinoy/mizan` (go 1.26),
  directory skeleton (architecture-final §3), empty `cmd/mizan` builds. Layout
  per **Spike 0 verdict** (single vs go.work); if verdict absent, use single
  module and isolate the choice so P4 can split without touching P1.
  *Depends: Spike 0 (soft).*
- **WI-P1-1 — Registry model + Store + SQLite.** `MetricTemplate` (collab §6),
  `Store` interface, `SQLiteStore` CRUD, schema migration v1 (incl. provenance
  columns from collab §3.9 so P2 needs no migration churn). Default DB path via
  `os.UserConfigDir()`. Decide SQLite driver (cgo vs `modernc.org/sqlite` —
  arch §8). *Depends: Spike 5.*
- **WI-P1-2 — registry.Service + config.** `Service` façade over `Store`
  (CRUD only in P1; import/export are P2). `config.LoadConfig()` returning error.
- **WI-P1-3 — Native eval (text pointwise).** `eval.Engine`, `native.go`:
  materialize `PointwiseMetricSpec` + `JsonInstance` + `AutoraterConfig`, call
  `EvaluateInstances`, return `Result`. Narrow mockable `EvaluationClient`
  interface. *Depends: Spike 1 verdict (placeholder syntax, region).*
- **WI-P1-4 — Multimodal + pairwise.** `content.go` (AssetRef → Blob/FileData →
  ContentMapInstance); pointwise for image/audio/video/music; pairwise
  (`PairwiseMetricSpec`, FlipEnabled). *Depends: Spike 2 (ContentMap, inline
  ceiling), Spike 3 (pairwise).* **This is the first fan-out within P1 — only
  after WI-3 slice validates.**
- **WI-P1-5 — Rubric + custom_schema.** `rubric` via `LLMBasedMetricSpec` inline
  rubric_groups; `custom_schema` via `genai` `GenerateContent` +
  `ResponseSchema` + exponential backoff. *Depends: Spike 4.*
- **WI-P1-6 — CLI surface.** `registry create|list|get|update|delete`,
  `eval run|pairwise`, `config show|set` (architecture.md §6). Output `json|table`.
- **WI-P1-7 — Asset ingestion.** `asset/mime.go` (DetectContentType + extension
  fallback), inline-vs-`gs://` decision by size against the **Spike 2** ceiling.
  GCS staging upload is a documented later option, not built here (Non-Goal).

### Acceptance criteria (P1)

- **WI-P1-ACCEPT (the gate before fan-out):** on a machine with ADC creds and
  `PROJECT_ID=ghchinoy-genai-sa`, `mizan registry create` a text pointwise
  template, then `mizan eval run --metric <name> --field response="..."` prints a
  numeric score and explanation from a **live** `EvaluateInstances` call.
- All modalities (image/audio/video/music) score via native `ContentMapInstance`
  (inline bytes and `gs://`), pairwise returns a `PairwiseChoice`, rubric and
  custom_schema each return their result shape — each with an integration test
  behind `//go:build integration`.
- `internal/registry` and `internal/eval` unit tests pass (eval against mocked
  `EvaluationClient`); `go build ./...` + `go vet` clean on go1.26.1.
- Dependency-direction check: `cmd/mizan` imports only `registry.Service`,
  `eval.Engine`, `config`.
- All P1-consumed spike verdicts recorded in research.md §7 (no "TBD").

---

## Phase 2 — Collaboration / contribution layer

**Conditional on P1 validated.** Adds the contribution channel over the P1
`registry.Service` and `Store` (design in collaboration-design.md). Purely
additive: no P1 command implementation changes beyond registering new subcommands.

### Work items

- **WI-P2-1 — Codec + JSON Schema.** `YAMLCodec` (MetricTemplate ⇄ pack YAML,
  collab §3.2); `schema/metrictemplate.json`; `contentHash` computation (collab
  §3.4). *Depends: P1 MetricTemplate.*
- **WI-P2-2 — Pack read/write.** `pack.go`: manifest, `packs/*/` + `templates/*.yaml`
  discovery/glob, `pack init packs/<name>` scaffold, `pack add`.
  Namespaced-id enforcement. Also add the repo-level
  `.github/workflows/validate-packs.yaml` (path-filtered to `packs/**`, builds
  CLI from checkout — collab §3.7); `pack init` does not re-emit it in-repo.
- **WI-P2-3 — Validation pipeline.** `validate.go` steps 1–5 (structural →
  lint), creds-free; discovers `packs/*` when given a repo tree; `--dry-run`
  step 6 (opt-in live). `mizan pack validate` exits non-zero on error.
- **WI-P2-4 — SyncBackend + GitPackBackend.** `sync.go` `SyncBackend` interface;
  `GitPackBackend` (Load/Save over a `packs/` tree or a single pack dir).
  `import <git-url>` / bare `import` defaults to `github.com/ghchinoy/mizan`,
  shelling out to user's git into `PackCacheDir` and reading its `packs/` tree.
- **WI-P2-5 — Service import/export + reconciliation.** `Service.Import/Export`,
  the §3.8 conflict matrix, `--strategy`, `ImportReport`, `dirty` tracking.
- **WI-P2-6 — CLI surface.** `registry import|export`, `pack init|validate|add`.
- **WI-P2-7 — Seam-proof test.** Automated check that `cmd/*` contains no
  SQLite/YAML/git symbols (collab §8) — proves model B is a drop-in.

### Acceptance criteria (P2)

- Round-trip: `export → import → export` is byte-identical modulo computed
  `updated`; no field loss.
- `pack validate` fails on each defect in collab §8; passes the §3.2 video
  example; the §3.2 video template imports and runs via the P1 engine.
- Reconciliation matrix (§3.8) fully unit-tested.
- The repo-level `validate-packs` workflow blocks a PR adding an invalid template
  under `packs/` (creds-free) while leaving code-only PRs unaffected.
- `import github.com/ghchinoy/mizan` (and bare `import`) discovers/imports all
  packs under `packs/`.
- Seam-proof test passes.

---

## Phase 3 — Batch eval (`EvaluateDataset`)

**Conditional on P1 validated; independent of P2.** Reuses P1 spec
materialization for the async LRO path (research.md §3 batch).

### Work items

- **WI-P3-1 — `eval/batch.go`.** Build `EvaluateDatasetRequest`
  (GcsSource/BigquerySource, `[]*Metric`, `OutputConfig` GcsDestination,
  AutoraterConfig); reuse WI-P1-3/5 spec builders.
- **WI-P3-2 — LRO lifecycle.** Submit, poll/wait (`op.Wait`/`op.Poll`),
  reattach by operation name (`EvaluateDatasetOperation(name)`).
- **WI-P3-3 — CLI surface.** `mizan eval batch --dataset gs://... --metric <name>
  --out gs://...`; `mizan eval batch status <op-name>`.

### Acceptance criteria (P3)

- A GCS-sourced dataset scored end to end; output written to the GCS
  destination; job reattachable by op name after process restart.
- Multimodal batch rows reference `gs://` URIs interpreted via the metric
  template (research.md §3). Integration test behind `//go:build integration`.

---

## Phase 4 — Wails desktop

**Conditional on P1 validated; benefits from P2.** Thin `internal/app` binding
over the same `registry.Service` + `eval.Engine`; no new domain logic.

### Work items

- **WI-P4-1 — Module split (if go.work per Spike 0).** Introduce `mizan-desktop`
  module / go.work; keep CLI lean (arch §5). *Depends: Spike 0.*
- **WI-P4-2 — Wails scaffold.** `cmd/mizan-desktop` from `eldamo-app` shape;
  Lit+Vite frontend; Wails v2.13.0 (toolchain pin per Spike 6). *Depends: Spike 6.*
- **WI-P4-3 — App bindings.** `internal/app/app.go`: List/Save/Delete templates,
  RunEvaluation, PickFile, Get/SaveConfig (architecture.md §7), plus
  import/export if P2 shipped. Adjust struct shapes for clean TS codegen per
  **Spike 6 verdict** (`*float32`/`map[string]any` trouble spots).
- **WI-P4-4 — Frontend views.** Template list/editor, eval runner, config setup
  dialog (uses `LoadConfig` error path).

### Acceptance criteria (P4)

- `wails dev` launches; the app lists templates, runs an eval, and shows a
  score+explanation, all via `registry.Service`/`eval.Engine` (no logic
  duplicated in `internal/app`).
- Generated TS bindings compile; Spike 6 struct adjustments (if any) applied and
  recorded.
- Config setup dialog appears (not a crash) when `PROJECT_ID` is unset.

---

## Global acceptance / definition of done

- All three design docs merged to `docs/` via PR (this architect's WI-4).
- Every "pending spike verdict" resolved and recorded in research.md §7 before
  the gated WI is implemented.
- P1 slice validated before any P2/P3/P4 work begins (the fan-out rule).
- Each phase's acceptance criteria met and signed off by the reviewer/QA before
  the next phase that depends on it starts.

---

## Suggested sequencing for the eng-manager

1. Ensure Spikes 0,1,2,3,4,5 verdicts are recorded (Spike 6 only gates P4).
2. Build P1 as the vertical slice (WI-0→WI-1→WI-2→WI-3→WI-6), **validate**
   (WI-P1-ACCEPT), *then* fan out WI-4/WI-5/WI-7.
3. After P1 validation, P2 and P3 may proceed in parallel (independent); P4 after
   P2 if desktop should ship with collaboration, else any time after P1.
