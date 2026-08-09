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

> **Rev 3 (2026-08-09):** two updates folded in.
> 1. **Separate templates repo** — canonical packs live in
>    `github.com/ghchinoy/mizan-templates`, not in `ghchinoy/mizan`. P2's pack
>    workflow targets that repo; the validate-packs CI and repo scaffold are
>    delivered *there* (by the architect, out of band) — see collaboration-design
>    §3.3/§3.7. No `packs/` tree or pack CI is added to the code repo.
> 2. **spike-core verdicts (Spikes 0–3) resolved** — single Go module; default
>    region `us-central1`; double-brace placeholders; and the load-bearing finding
>    that **inline bytes are unsupported by native `EvaluateInstances`**, making
>    **GCS staging a P1 prerequisite** for all multimodal native eval (see the new
>    "P1 prerequisites" block below).

---

## Phase ordering & the fan-out rule

```
  Spike 0 ✓ single module ──┐
  Spikes 1-5 (API/registry) ┴─► P1 vertical slice ──► [validated?] ──► P2 collab
   + GCS staging prereq                                   │              P3 batch
                                                          └── fan-out ──► P4 desktop
```

- **Spike verdicts are inputs, not blockers for all of P1.** Spikes 0–3
  (spike-core) and Spike 5 (registry) are **resolved**; their verdicts are folded
  into architecture-final §5/§6/§7/§8. P1's WI-0/WI-1 (scaffold, config, store) do
  not need any further spike. If a remaining verdict is missing when a WI is
  picked up, the WI is blocked on *that* verdict only — raise it, don't guess.

- **P1 PREREQUISITE — GCS staging (NEW, spike-core).** Native
  `EvaluateInstances` **rejects inline bytes**; every non-text native eval needs a
  `gs://` `FileData` reference. Therefore a **GCS staging bucket must exist before
  P1 multimodal eval (WI-P1-4/WI-P1-7) can pass.** Hard blocker: the eval SA
  `sa-scion-warmup` **lacks `storage.buckets.create/list`**, so someone with
  rights must **provision a staging bucket and grant the SA object read/write on
  it** (or grant the SA `storage.admin`) — this is an infra/owner action, not a
  code task. WI-P1-3 (text pointwise) does **not** need the bucket and is the
  slice that validates first; the bucket blocks only the multimodal fan-out.
  *Raise to user/owner now so it is provisioned before WI-P1-4.*
- **The fan-out (P2/P3/P4) does not begin until the P1 slice is validated
  end-to-end** (WI-P1-ACCEPT passes). An interface mismatch in `registry.Service`
  or `eval.Engine` caught here is one fix; caught after three phases build on it,
  it is one fix per phase it reached.

Cross-phase dependency summary:

| Depends on | Consumed by |
|---|---|
| Spike 0 ✓ (single module) | P1 WI-0 (scaffold) — layout settled, no split in P1 |
| Spike 1 ✓ (double-brace placeholder, region us-central1) | P1 WI-3 (native eval) |
| Spike 2 ✓ (ContentMap = `gs://` FileData ONLY; inline unsupported) | P1 WI-3, WI-4 |
| **GCS staging bucket provisioned + SA grant** | **P1 WI-4, WI-7 (multimodal)** |
| Spike 4 (custom_schema; genai inline OK) | P1 WI-5 (genai fallback) |
| Spike 5 ✓ (SQLite CRUD) | P1 WI-1 (store) |
| **P1 `registry.Service` + `MetricTemplate`** | **P2, P4 (hard dep)** |
| **P1 `eval.Engine`** | P3 (batch reuses spec materialization) |
| Templates repo scaffold + validate-packs CI (architect, out of band) | P2 pack workflow |
| Tagged `mizan` release + `MIZAN_RO_TOKEN` secret | templates-repo CI actually gating |
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
  directory skeleton (architecture-final §3), empty `cmd/mizan` builds. **Layout:
  single Go module (Spike 0 resolved)** — do *not* introduce go.work in P1. A
  buildable scaffold already exists on branch `spike/scaffold`; reuse/adapt it.
  **No `packs/` tree and no validate-packs workflow in this repo (rev 3)** — those
  live in `mizan-templates`. *Depends: Spike 0 ✓.*
- **WI-P1-1 — Registry model + Store + SQLite.** `MetricTemplate` (collab §6),
  `Store` interface, `SQLiteStore` CRUD, schema migration v1 (incl. provenance
  columns from collab §3.9 so P2 needs no migration churn). Default DB path via
  `os.UserConfigDir()`. **SQLite driver: adopt pure-Go `modernc.org/sqlite`
  (recommended, arch §8)** so `go install cmd/mizan` is cgo-free for the rev-3
  templates CI and end users; Spike 5 validated the layer on `mattn/go-sqlite3`
  but the interface is driver-agnostic. If cgo is retained, document that the
  templates CI must enable cgo. *Depends: Spike 5 ✓.*
- **WI-P1-2 — registry.Service + config.** `Service` façade over `Store`
  (CRUD only in P1; import/export are P2). `config.LoadConfig()` returning error.
  Config includes `Location` (default `us-central1`), `StagingBucket` (required
  for multimodal), and `DefaultTemplatesRepo` (default
  `github.com/ghchinoy/mizan-templates`) — arch §7.
- **WI-P1-3 — Native eval (text pointwise) — THE VALIDATING SLICE.** `eval.Engine`,
  `native.go`: materialize `PointwiseMetricSpec` + `JsonInstance` +
  `AutoraterConfig`, call `EvaluateInstances`, return `Result`. Use double-brace
  `{{x}}` placeholder substitution (spike-core). **`AutoraterModel` must expand to
  the full model resource name** (spike-core). Region default `us-central1`.
  Narrow mockable `EvaluationClient` interface. **Text-only — needs no GCS bucket**,
  so this slice validates before the multimodal fan-out. *Depends: Spike 1 ✓.*
- **WI-P1-4 — Multimodal + pairwise.** `content.go` (AssetRef → **`FileData`
  (`gs://`) only** → ContentMapInstance — **inline `Blob` is NOT accepted by
  native `EvaluateInstances`**, spike-core); pointwise for image/audio/video/music;
  pairwise (`PairwiseMetricSpec`, FlipEnabled; `PairwiseChoice` BASELINE=1
  CANDIDATE=2 TIE=3). **Requires the GCS staging bucket prerequisite** (see top of
  plan). *Depends: Spike 2 ✓ (gs://-only), Spike 3 ✓ (pairwise), GCS bucket.*
  **This is the first fan-out within P1 — only after the WI-3 slice validates.**
- **WI-P1-5 — Rubric + custom_schema.** `rubric` via `LLMBasedMetricSpec` inline
  rubric_groups; `custom_schema` via `genai` `GenerateContent` +
  `ResponseSchema` + exponential backoff. **The genai path DOES accept inline
  bytes** (unlike native eval) — keep the two content converters distinct
  (arch §6). *Depends: Spike 4.*
- **WI-P1-6 — CLI surface.** `registry create|list|get|update|delete`,
  `eval run|pairwise`, `config show|set` (architecture.md §6). Output `json|table`.
- **WI-P1-7 — Asset ingestion + GCS staging.** `asset/mime.go` (DetectContentType
  + extension fallback — **mismatched MIME silently drops the asset**, spike-core,
  so detection must be correct); `asset/gcs.go` **uploads local non-text assets to
  the `StagingBucket` and returns the `gs://` URI** — this is now **required**, not
  a later option, because native eval has no inline path. Error clearly if a
  multimodal eval is attempted with no `StagingBucket` configured. Staging-bucket
  *lifecycle* management remains a Non-Goal. *Depends: GCS bucket prerequisite.*

### Acceptance criteria (P1)

- **WI-P1-ACCEPT (the gate before fan-out):** on a machine with ADC creds and
  `PROJECT_ID=ghchinoy-genai-sa`, `mizan registry create` a text pointwise
  template, then `mizan eval run --metric <name> --field response="..."` prints a
  numeric score and explanation from a **live** `EvaluateInstances` call.
- All modalities (image/audio/video/music) score via native `ContentMapInstance`
  using **`gs://` `FileData` (inline bytes are unsupported on the native path)**;
  pairwise returns a `PairwiseChoice` (BASELINE=1/CANDIDATE=2/TIE=3); rubric and
  custom_schema each return their result shape (custom_schema via genai, which
  *does* accept inline bytes) — each with an integration test behind
  `//go:build integration`. Multimodal tests require the provisioned staging bucket.
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
  Namespaced-id enforcement. **(Rev 3) The `validate-packs` CI workflow does NOT
  live in this repo** — it is committed to `mizan-templates` (delivered by the
  architect out of band, collab §3.7). `pack init` does not emit a CI workflow.
- **WI-P2-3 — Validation pipeline.** `validate.go` steps 1–5 (structural →
  lint), creds-free; discovers `packs/*` when given a repo tree; enforces
  double-brace `{{x}}` placeholders (spike-core); `--dry-run` step 6 (opt-in
  live). `mizan pack validate` exits non-zero on error. **This is the binary the
  `mizan-templates` CI `go install`s** — keep its dependency graph cgo-free (see
  WI-P1-1) so it installs without a C toolchain.
- **WI-P2-4 — SyncBackend + GitPackBackend.** `sync.go` `SyncBackend` interface;
  `GitPackBackend` (Load/Save over a `packs/` tree or a single pack dir).
  `import <git-url>` / bare `import` **defaults to
  `github.com/ghchinoy/mizan-templates`** (via `Config.DefaultTemplatesRepo`),
  shelling out to user's git into `PackCacheDir` and reading its `packs/` tree.
  **No code change to `GitPackBackend` for the rev-3 pivot** — only the default
  URL differs (seam validation, collab §8).
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
- The `validate-packs` workflow **in `mizan-templates`** blocks a PR adding an
  invalid template under `packs/` (creds-free) while leaving non-pack PRs
  unaffected. (Delivered/scaffolded by the architect; requires `MIZAN_RO_TOKEN` +
  a pinnable `mizan` version — collab §3.7/§7.)
- `import github.com/ghchinoy/mizan-templates` (and bare `import`, which defaults
  there) discovers/imports all packs under `packs/`.
- Seam-proof test passes; switching the default source (templates repo ↔ fork ↔
  local tree) needs no `GitPackBackend`/`cmd/*` change.

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

- **WI-P4-1 — Module split (OPTIONAL; single module chosen in Spike 0).** Spike 0
  resolved to a single module, so **no split is planned**. Only revisit if Wails
  deps bloat the CLI's `go install` (which the templates CI runs) or Spike 6 forces
  a desktop-specific toolchain pin — arch §5. *Default: skip.*
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

- All three design docs merged to `docs/` of `ghchinoy/mizan` via PR (this
  architect's WI-4 — carried by the amended PR #2, rev 3).
- The `mizan-templates` repo is scaffolded (README, `packs/` layout, one example
  pack, `docs/pack-format.md`, `validate-packs.yml`) — delivered by the architect.
- Every "pending spike verdict" resolved and recorded in research.md §7 before
  the gated WI is implemented (spike-core 0–3 and Spike 5 already resolved).
- **GCS staging bucket provisioned + SA granted object rw** before P1 multimodal
  (WI-P1-4/7) — infra prerequisite.
- P1 slice validated (WI-P1-ACCEPT, text pointwise, needs no bucket) before any
  P2/P3/P4 work begins (the fan-out rule).
- Each phase's acceptance criteria met and signed off by the reviewer/QA before
  the next phase that depends on it starts.

---

## Suggested sequencing for the eng-manager

1. **Infra/owner first:** provision the GCS staging bucket + SA grant (blocks P1
   multimodal), and decide validator-pinning for the templates CI (tag a `mizan`
   release or accept a `main` pseudo-version; set `MIZAN_RO_TOKEN`) — collab §7.
2. Confirm spike verdicts recorded: 0–3 (core) and 5 (registry) are in; 4 gates
   WI-P1-5, 6 gates P4.
3. Build P1 as the vertical slice (WI-0→WI-1→WI-2→WI-3→WI-6), **validate**
   (WI-P1-ACCEPT — text pointwise, no bucket needed), *then* fan out
   WI-4/WI-5/WI-7 (WI-4/7 need the staging bucket).
4. After P1 validation, P2 and P3 may proceed in parallel (independent); P4 after
   P2 if desktop should ship with collaboration, else any time after P1. P2's pack
   workflow targets `mizan-templates` (already scaffolded).
