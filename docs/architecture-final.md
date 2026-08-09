# Mizan — Final Architecture

Status: design for review (pre-implementation)
Date: 2026-08-09
Author: mizan-architect
Supersedes: docs/architecture.md (draft, 2026-08-07) — reconciled here.
Companion: design/collaboration-design.md (contribution layer),
           design/implementation-plan.md (phasing + acceptance criteria)
Ground truth (do not re-derive): docs/research.md.

This document confirms what docs/architecture.md got right, folds in the
collaboration layer, and records the decisions the draft left open. Where a
decision depends on a spike still running, it is marked **pending spike verdict**
and the dependent detail is deferred, not guessed.

---

## 1. Problem & Goals

Mizan is a Go tool over the **Vertex AI Gen AI Evaluation Service**
(`cloud.google.com/go/aiplatform/apiv1beta1`) to **create, manage, share, and
run** Gemini "LLM-as-a-Judge" metric templates across all modalities (text,
image, audio, video, music). Two frontends over one shared core:

- **`cmd/mizan`** — Cobra CLI. **MVP and primary deliverable.**
- **`cmd/mizan-desktop`** — Wails v2 desktop app. **Later phase, same `internal/`
  core.** (User decision 2026-08-09: both in scope; CLI first.)

The Metric Registry is **Mizan's own construct** — there is no GCP-native
resource for named judge templates (research.md §3). Collaboration starts as
**hybrid model C** (local SQLite working copy + git-backed template packs), with
a Store/codec/sync seam that admits a future Firestore/GCS central registry
(model B) as a drop-in (see collaboration-design.md §3.1).

### Non-Goals (architecture level)

- Firestore/GCS central registry implementation (model B) — seam only.
- Batch/async `EvaluateDataset` in the CLI MVP — Phase 3.
- Wails desktop in the MVP — Phase 4.
- GCS staging-bucket lifecycle management as MVP (provide `gs://` yourself for
  large assets; inline bytes for small ones — research.md §6, open in §7).

---

## 2. Confirmed from the draft (unchanged)

The following draft decisions are **validated and retained** — they are correct
and consistent with research.md:

- Single shared Go core in `internal/`, zero UI-framework deps in the core; two
  thin frontends. (Mirrors `eldamo-app`.)
- `apiv1beta1` + `apiv1beta1/aiplatformpb` **exclusively** for eval; GA `apiv1`
  is never imported (lacks multimodal, autorater config, batch — research.md §2).
- `google.golang.org/genai` only for the **custom_schema** fallback path.
- Engine dispatch by `MetricKind` (architecture.md §4), with `content.go`
  supplying the two small converters between `aiplatformpb.Part` and
  `genai.Part` (they are distinct types — retained note).
- `config.LoadConfig()` returns an **error** (not `log.Fatal`) so the GUI can
  show a setup dialog; CLI treats it as fatal. (architecture.md §5.)
- Cobra for CLI; Wails v2 + Lit/Vite for desktop (user's established stacks).
- Testing: pure unit tests for `registry`; `eval` behind a narrow mockable
  `EvaluationClient` interface; live calls behind `//go:build integration`.

---

## 3. Reconciled module layout

Adds the collaboration-layer packages (codec / pack / sync / schema / service)
and the desktop split. `internal/registry` grows the contribution layer;
everything else is as drafted.

```
mizan/
  go.mod                          module github.com/ghchinoy/mizan
  go.work                         (IFF spike-core verdict = go.work; see §5)
  cmd/
    mizan/                        Cobra CLI (MVP)
      main.go  root.go
      registry.go                 create|list|get|update|delete|import|export
      pack.go                     pack init|validate|add
      eval.go                     eval run|pairwise
      config.go                   config show|set
    mizan-desktop/                Wails v2 (Phase 4)
      main.go  wails.json
      frontend/                   Lit + Vite (eldamo-app stack)
  internal/
    config/       config.go       LoadConfig() -> Config, error
    registry/                     Metric Registry + contribution layer
      model.go                    MetricTemplate, InputSpec, enums (see collab §6)
      service.go                  Service — the ONLY type CLI/GUI/eval depend on
      store.go                    Store interface (CRUD + ListChangedSince)
      codec.go                    Codec interface + YAMLCodec
      sync.go                     SyncBackend interface + GitPackBackend
      pack.go                     pack manifest read/write, dir glob, scaffold
      validate.go                 validation pipeline (structural..lint)
      schema/metrictemplate.json  JSON Schema (structural source of truth)
      sqlite/sqlite.go            SQLiteStore (default Store impl)
      # firestore/  (model B, NOT built now — see Non-Goals)
    eval/
      engine.go native.go custom.go content.go
      batch.go                    (Phase 3) EvaluateDataset wrapper
    asset/        mime.go gcs.go
    app/          app.go          (Phase 4) Wails bindings over registry.Service
  packs/                          canonical shared template packs (in-repo; user decision)
    <pack-name>/
      mizan-pack.yaml
      templates/*.yaml            one MetricTemplate per file (data only; not imported by Go)
  docs/           research.md architecture.md spikes.md
                  collaboration-design.md architecture-final.md implementation-plan.md
  .github/workflows/validate-packs.yaml   repo-level pack CI gate, path-filtered to packs/**
```

The `packs/` tree is pure data (no Go imports it), so it does not affect
`go build ./...`, module deps, or the CLI binary — code and packs coexist in one
repo per the 2026-08-09 user decision. See collaboration-design.md §3.3/§3.7.

**Dependency direction (enforced):** `cmd/*` → `registry.Service`,
`eval.Engine`, `config`. `eval` → `registry` (reads `MetricTemplate`) but never
`sqlite`/`sync`/`codec`. Nothing in `cmd/*` imports `sqlite`, `sync`, `codec`, or
`aiplatformpb` directly. This is what keeps model B a drop-in and is an explicit
acceptance check (collaboration-design §8).

---

## 4. Go version

- Container ships **go1.26.1**; the draft referenced 1.25.x (from the
  `eldamo-app` precedent).
- **Decision: target the toolchain in the build container — `go 1.26` in
  `go.mod`** (`toolchain go1.26.1`). No language feature in this design requires
  1.26 specifically, so this is low-risk and reversible; pin to what actually
  builds here rather than to a precedent repo's older line.
- Wails v2.13.0 is compatible with the go1.26 line for the desktop phase; if
  Spike 6 surfaces a Wails/toolchain incompatibility, the desktop module can pin
  its own `toolchain` directive (another reason the go.work split, if chosen, is
  attractive — §5). **Pending Spike 6 verdict** on any Wails-specific pin.

---

## 5. Single module vs `go.work` — **pending spike-core (Spike 0) verdict**

This is genuinely load-bearing and the brief defers it to the spike-core
verdict, which is **not yet available** (research/ spike-verdicts are empty as of
2026-08-09T14:xx). The decision therefore stays open; both layouts are prepared:

- **Single module** — simplest; matches `eldamo-app`. Cost: CLI-only users pull
  Wails' transitive deps (webview bindings) into `go install
  .../cmd/mizan@latest`, bloating the CLI and its CI.
- **`go.work` two modules** — `mizan-core` (CLI + `internal/`) and
  `mizan-desktop` (Wails, depends on core via workspace/replace). Keeps the CLI
  lean and independently `go install`-able; lets desktop pin its own toolchain.
  Cost: two `go.mod`s, slightly more release/CI ceremony.

**Architect lean (non-binding, defers to spike):** given the CLI is meant to be a
lightweight, scriptable, `go install`-able tool (and CI runs `go install
.../cmd/mizan` in the pack-validation workflow — collaboration-design §3.7), the
Wails transitive-dep bloat argues for the **go.work split**. **Spike 0 decides;
P1 does not depend on the outcome** because P1 ships no Wails code — P1 builds
identically under either layout, and the split (if chosen) is introduced at the
P4 boundary at latest, or at P1 scaffolding if Spike 0 lands first.

---

## 6. Domain model & engine

The authoritative `MetricTemplate` struct is in **collaboration-design.md §6**
(it extends architecture.md §3 with `Inputs`, attribution, and provenance/sync
fields). The engine and eval-time proto materialization are unchanged from
architecture.md §3–§4:

- `pointwise`/`pairwise`, text-only → `*MetricSpec` + `JsonInstance`.
- `pointwise`/`pairwise`, any non-text asset → `*MetricSpec` +
  `ContentMapInstance` (Blob for inline / FileData for `gs://`).
- `rubric` → `LLMBasedMetricSpec` + inline `rubric_groups`.
- `custom_schema` → `genai.GenerateContent` with `ResponseSchema` + backoff retry.

`AutoraterConfig` (SamplingCount, FlipEnabled, AutoraterModel) is populated from
the template's `autorater.*` fields.

> **Pending Spike 1/2 verdicts:** exact `{{placeholder}}` vs `{x}` substitution
> syntax; supported eval-service region(s); observed inline payload ceiling per
> modality. These affect `content.go`/`native.go` implementation and the config
> default `Location`, **not** the module layout or the domain model. Gemini usage
> is global or `us` per user decision; eval-service region defers to the spike.

---

## 7. Config

Per architecture.md §5 (`mcp-common` shape), with the collaboration additions:

```go
type Config struct {
    ProjectID       string // required (env PROJECT_ID / MIZAN_PROJECT_ID); default ghchinoy-genai-sa in examples
    Location        string // eval-service region — PENDING Spike 1 verdict; Gemini global|us
    StagingBucket   string // optional, gs:// stripped
    APIEndpoint     string // optional override
    RegistryDBPath  string // default ~/.config/mizan/registry.db (os.UserConfigDir)
    PackCacheDir    string // default ~/.cache/mizan/packs  (for `import <git-url>`)
}
```

---

## 8. Dependencies (reconciled go.mod)

```
require (
    cloud.google.com/go/aiplatform v1.121.0   // apiv1beta1 only
    google.golang.org/genai        v1.65.0    // custom_schema fallback
    github.com/spf13/cobra         v1.10.2
    github.com/mattn/go-sqlite3    <pin>       // matches eldamo-app
    github.com/joho/godotenv       <pin>
    gopkg.in/yaml.v3               <pin>       // YAMLCodec (collaboration layer)
    github.com/Masterminds/semver/v3 <pin>     // template version comparison
    github.com/santhosh-tekuri/jsonschema/v6 <pin> // structural pack validation
    // desktop module only (see §5):
    github.com/wailsapp/wails/v2   v2.13.0
)
```

`mattn/go-sqlite3` requires cgo; the pack-validation CI job needs cgo enabled to
`go install` the CLI, or the CLI must be built with a pure-Go SQLite driver.
**Open sub-question:** consider `modernc.org/sqlite` (pure Go, no cgo) to keep
`go install .../cmd/mizan@latest` and CI frictionless. *Confirm during P1 /
Spike 5.*

---

## 9. Alternatives Considered (architecture level)

- **Two independent apps (no shared core)** — rejected; duplicates domain/eval
  logic, guarantees drift between CLI and GUI. Shared `internal/` is the whole
  point of the layout.
- **GA `apiv1`** — rejected (research.md §2: no multimodal/autorater/batch).
- **Silent genai fallback for all custom output** — rejected;
  `custom_schema` is an explicit, declared metric kind so behavior is
  predictable and testable (research.md §3).
- **Firestore as the primary/only Store from day one** — rejected (user
  deferral; adds infra + auth + offline handling before any value ships). The
  seam preserves the option.

---

## 10. Open Questions (architecture level)

1. **Module layout** — single vs go.work. *Pending Spike 0 verdict.*
2. **SQLite driver** — cgo `mattn/go-sqlite3` vs pure-Go `modernc.org/sqlite`
   (affects `go install`/CI friction). *Confirm P1 / Spike 5.*
3. **Eval-service region / placeholder syntax / inline size ceiling.** *Pending
   Spikes 1–2.*
4. **`apiv1beta1` preview stability posture** for a production tool
   (research.md §7 open item) — confirm breaking-change cadence before GA claims.
5. Pack repo location — **RESOLVED (user, 2026-08-09): in-repo `packs/` tree in
   `ghchinoy/mizan`.** See collaboration-design.md §3.3/§3.7.

---

## 11. Acceptance Criteria (architecture level)

- `go build ./...` succeeds on go1.26.1 under the chosen layout; `go vet` clean.
- Dependency-direction check passes: `cmd/*` imports only `registry.Service`,
  `eval.Engine`, `config` — never `sqlite`/`sync`/`codec`/`aiplatformpb`.
- `eval` unit tests pass against a mocked `EvaluationClient`; integration tests
  compile behind `//go:build integration`.
- All **pending spike verdicts** (§5, §6, §8, §10) are resolved and recorded in
  docs/research.md §7 before the packages they gate are implemented.
