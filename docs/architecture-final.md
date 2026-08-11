# Mizan — Final Architecture

Status: design for review (pre-implementation)
Date: 2026-08-09
Author: mizan-architect
Supersedes and REPLACES docs/architecture.md (draft, 2026-08-07): its unique,
           still-accurate content is folded in here and the standalone draft file
           has been removed (2026-08-09).
Companion: design/collaboration-design.md (contribution layer),
           design/implementation-plan.md (phasing + acceptance criteria)
Ground truth (do not re-derive): docs/research.md.

This document confirms what the retired 2026-08-07 draft got right, folds in the
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
- GCS staging-bucket **lifecycle** management as MVP (creation/retention/GC). Note
  (rev 3 / spike-core): a staging bucket *itself* is now a **P1 prerequisite** for
  multimodal native eval — inline bytes are unsupported on the native path, so all
  non-text assets must be staged to `gs://` (§6). MVP expects a bucket to already
  exist (config `StagingBucket`); Mizan uploads to it but does not manage its
  lifecycle. (The `genai` custom-schema path still accepts inline bytes — §6.)

---

## 2. Confirmed from the draft (unchanged)

The following draft decisions are **validated and retained** — they are correct
and consistent with research.md:

- Single shared Go core in `internal/`, zero UI-framework deps in the core; two
  thin frontends. (Mirrors `eldamo-app`.)
- `apiv1beta1` + `apiv1beta1/aiplatformpb` **exclusively** for eval; GA `apiv1`
  is never imported (lacks multimodal, autorater config, batch — research.md §2).
- `google.golang.org/genai` only for the **custom_schema** fallback path.
- Engine dispatch by `MetricKind` (see §6), with `content.go`
  supplying the two small converters between `aiplatformpb.Part` and
  `genai.Part` (they are distinct types — retained note).
- `config.LoadConfig()` returns an **error** (not `log.Fatal`) so the GUI can
  show a setup dialog; CLI treats it as fatal. (see §7)
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
      sqlite/sqlite.go            SQLiteStore (default Store impl; pure-Go driver — see §8)
      # firestore/  (model B, NOT built now — see Non-Goals)
    eval/
      engine.go native.go custom.go content.go
      batch.go                    (Phase 3) EvaluateDataset wrapper
    asset/        mime.go gcs.go   gcs.go staging is now a P1 prerequisite — see §6
    app/          app.go          (Phase 4) Wails bindings over registry.Service
  docs/           research.md spikes.md
                  collaboration-design.md architecture-final.md implementation-plan.md
  # NOTE (rev 3): NO packs/ tree and NO validate-packs workflow live in THIS repo.
  # Canonical template packs live in the dedicated github.com/ghchinoy/mizan-templates
  # repo, with their own CI gate. See collaboration-design.md §3.3/§3.7.
```

**Rev 3 (2026-08-09):** the canonical template `packs/` tree does **not** live in
this code repo — the user moved it to a dedicated
**`github.com/ghchinoy/mizan-templates`** repo (superseding the rev-2 in-repo
decision). The `mizan` repo carries only code + design docs; it neither builds nor
imports packs. The contribution CI gate lives in `mizan-templates`. The
Store/Codec/SyncBackend seam is unchanged, so this was a default-URL change plus
relocating one CI file — see collaboration-design.md §3.3/§3.7.

**Dependency direction (enforced):** `cmd/*` → `registry.Service`,
`eval.Engine`, `config`. `eval` → `registry` (reads `MetricTemplate`) but never
`sqlite`/`sync`/`codec`. Nothing in `cmd/*` imports `sqlite`, `sync`, `codec`, or
`aiplatformpb` directly. This is what keeps model B a drop-in and is an explicit
acceptance check (collaboration-design §8).

**Persistence.** Default `Store` is a SQLite file at `RegistryDBPath` (§7,
`~/.config/mizan/registry.db` via `os.UserConfigDir()`) using the pure-Go
`modernc.org/sqlite` driver recommended in §8 (to be confirmed in P1). Schema is a
single `metric_templates` table;
complex fields (`Modalities`, `RubricGroup`, `ResponseSchema`, `Inputs`) are
JSON-serialized into TEXT columns. The `Store` interface (§3 `store.go`) keeps a
future Firestore-backed impl a drop-in (§9).

**Component diagram (current build vs roadmap).** Phase 1 is now complete: all
four metric kinds (pointwise, rubric, custom_schema, pairwise) and GCS
multimodal staging are implemented and CLI-runnable end-to-end, so those
nodes/edges are solid. Only two elements remain genuinely dashed/roadmap: the
Firestore/GCS `SyncBackend` (still a planned drop-in, not built) and the
external `mizan-templates` repo integration (`registry import|export`/`pack`
still don't exist in this binary — P2):

![Mizan component architecture diagram showing cmd/mizan composed via internal/wire over registry.Service and eval.Engine, with the sqlite.Store implementation and all four metric-kind dispatch paths (pointwise, rubric, custom_schema, pairwise) plus GCS staging shown solid, and only the Firestore SyncBackend and the mizan-templates repo shown dashed as not-yet-built](diagrams/component-architecture.webp)

---

## 4. Go version

- Container ships **go1.26.1**; the draft referenced 1.25.x (from the
  `eldamo-app` precedent).
- **Decision: target the toolchain in the build container — `go 1.26` in
  `go.mod`** (`toolchain go1.26.1`). No language feature in this design requires
  1.26 specifically, so this is low-risk and reversible; pin to what actually
  builds here rather than to a precedent repo's older line.
- Wails v2.13.0 is compatible with the go1.26 line for the desktop phase; if
  Spike 6 surfaces a Wails/toolchain incompatibility, that is one trigger to
  revisit the (currently rejected) go.work split so desktop can pin its own
  `toolchain` — §5. **Pending Spike 6 verdict** on any Wails-specific pin.

---

## 5. Module layout — **RESOLVED: single Go module** (spike-core Spike 0)

**Verdict (spike-core, 2026-08-09): a single Go module**, not `go.work`. A
buildable scaffold was pushed to branch `spike/scaffold` on `ghchinoy/mizan`
confirming it. Rationale from the spike: the single module is simplest, matches
the `eldamo-app` precedent, and the anticipated Wails transitive-dep bloat did not
justify the two-module ceremony at this stage.

- **Chosen — single module.** One `go.mod`, `cmd/mizan` and `cmd/mizan-desktop`
  under one module. P1 builds against it directly.
- **Rejected — `go.work` two modules** (`mizan-core` + `mizan-desktop`). Would
  keep CLI-only `go install` leaner and let desktop pin its own toolchain, but
  adds a second `go.mod` and release/CI ceremony the spike judged premature. Kept
  as a **reversible** future option: if the Wails deps later bloat `go install
  .../cmd/mizan` (which the rev-3 templates CI now runs — collaboration-design
  §3.7), the split can be introduced at the P4 boundary without touching P1.

**Rev-3 interaction:** the templates CI `go install`s `cmd/mizan` (collaboration-
design §3.7). Under a single module that pulls the full dependency set; this makes
the **pure-Go SQLite driver** recommendation (§8) more pressing (cgo-free
install), and is a data point to watch for a possible later split if Wails deps
land in the CLI's module graph.

---

## 6. Domain model & engine

The authoritative `MetricTemplate` struct is in **collaboration-design.md §6**
(it extends the retired draft's §3 domain struct with `Inputs`, attribution, and
provenance/sync fields). The engine and eval-time proto materialization are folded
from the retired draft; the eval-layer types are given below and the authoritative
dispatch is the bullets that follow. (CLI surface and desktop bindings folded from
the retired draft are in §12 and §13.)

- `pointwise`/`pairwise`, text-only → `*MetricSpec` + `JsonInstance`.
- `pointwise`/`pairwise`, any non-text asset → `*MetricSpec` +
  `ContentMapInstance` with **`FileData` (`gs://`) only** — see the inline-bytes
  finding below.
- `rubric` → shares the native `PointwiseMetricSpec`/`EvaluateInstances` path
  with `pointwise` (`runRubric` in `internal/eval/native.go`), because the
  synchronous `EvaluateInstances` API has no `LLMBasedMetricSpec` input for
  inline rubric groups (that message exists only on the batch
  `EvaluateDataset` path, and there it references rubric groups by key, not
  inline) — instead the rubric criteria are rendered as additional judge-prompt
  text via `renderRubricGroups` and appended to the metric prompt.
- `custom_schema` → `genai.GenerateContent` with `ResponseSchema` + backoff retry.

`AutoraterConfig` (SamplingCount, FlipEnabled, AutoraterModel) is populated from
the template's `autorater.*` fields.

The eval-layer types (folded from the retired draft §3):

```go
// internal/eval/engine.go
type Instance struct {
    // Named placeholders -> content. For text, a plain string is enough;
    // for other modalities, a reference to an asset (path or gs:// URI).
    Fields map[string]AssetRef
}
type AssetRef struct {
    Modality Modality
    Text     string // for ModalityText
    FilePath string // local file; staged to gs:// for native eval (inline only on the genai custom-schema path)
    GCSUri   string // pre-staged asset
    MimeType string // detected or explicit
}
type Result struct {
    Score          *float32
    PairwiseChoice string // "" unless pairwise
    Explanation    string
    RawOutput      []string          // if ReturnRawOutput
    CustomOutput   map[string]any    // if KindCustomSchema
}

type Engine interface {
    Run(ctx context.Context, tmpl registry.MetricTemplate, inst Instance) (Result, error)
}
```

> **Spike 1–3 verdicts — RESOLVED (spike-core, 2026-08-09):**
>
> - **Placeholder syntax:** the API accepts **both** `{{x}}` and `{x}`; Mizan
>   **standardizes on double-brace `{{x}}`** (validator enforces —
>   collaboration-design §3.2/§3.5).
> - **Eval-service region:** the **native** `EvaluateInstances` path targets a
>   **specific regional endpoint** — `us-central1` (verified default), plus
>   `us-east4`, `us-west1`, `europe-west1`, `europe-west4`, and `global` all return
>   live scores; `asia-northeast1` is rejected (`FailedPrecondition`) and the **`us`
>   multi-region endpoint 404s — do NOT use it**. **Default `Location = us-central1`**
>   (§7). Surface the service's `Unsupported region` error verbatim. Prefer a
>   concrete region for the native path; reserve `global` for the genai path below.
> - **The `genai` custom-schema fallback path uses `location=global`** and is
>   distinct from the native regional path — keep the two clients/locations
>   separate (this mirrors the inline-bytes asymmetry).
> - **MAJOR — inline bytes are NOT supported by native `EvaluateInstances` /
>   `ContentMapInstance`.** All non-text native eval **requires `gs://` `FileData`
>   staging** — GCS staging is mandatory for *every* multimodal native eval, not
>   just large payloads. There is no inline-`Blob` fast path on the native route;
>   `content.go` therefore emits `FileData` only for non-text assets and must
>   stage local files to GCS first (or error clearly if no bucket is configured).
> - **Exception — the `genai` custom-schema fallback path DID accept inline
>   bytes.** The two paths differ: native `EvaluateInstances` = `gs://` only;
>   `genai.GenerateContent` (custom_schema) = inline bytes OK. `content.go` must
>   keep the two converters distinct and honor this asymmetry.
> - **Autorater model must be a FULL RESOURCE NAME** —
>   `projects/{ProjectID}/locations/{Location}/publishers/google/models/{model}`; a
>   bare id (`gemini-2.5-pro`) is rejected `InvalidArgument: Invalid autorater model
>   resource name`. Packs store only a **publisher-relative id** (portability —
>   they must not embed a project); `native.go` **expands** it to the full resource
>   name at materialization using config `ProjectID`/`Location`. See
>   collaboration-design §3.2/§6.
> - **Mismatched MIME silently drops the asset** — `asset/mime.go` must detect and
>   set the correct MIME or the eval silently loses the input.
> - **`PairwiseChoice`** confirmed live: `BASELINE=1`, `CANDIDATE=2`, `TIE=3`.
>
> These affect `content.go` / `native.go` / `asset/` and the config default
> `Location`, and make **GCS staging a P1 prerequisite** (implementation-plan §
> P1). They do **not** change the module layout or the domain model.

**Sequence diagram — text-pointwise `eval run` (fully implemented).** This is
one example of a fully implemented path; rubric, custom_schema, pairwise, and
multimodal are also implemented end-to-end — see the component diagram above:

![Sequence diagram of mizan eval run for a text-pointwise metric: CLI loads config, opens registry.Service and eval.Engine via wire, fetches the MetricTemplate, expands the autorater model to a full resource name, builds the PointwiseMetricSpec/JsonInstance/AutoraterConfig, calls Vertex AI EvaluateInstances, and renders the mapped Result to stdout](diagrams/eval-sequence.webp)

---

## 7. Config

Per the mcp-common shape (folded from the retired draft), with the collaboration
additions:

```go
type Config struct {
    ProjectID            string // required (env PROJECT_ID / MIZAN_PROJECT_ID); default ghchinoy-genai-sa in examples
    Location             string // native eval-service region; default "us-central1" (spike-core). Valid: us-central1|us-east4|us-west1|europe-west1|europe-west4|global. NOTE: the "us" MULTI-REGION 404s (do not use); asia-northeast1 rejected. genai custom-schema path uses location=global separately.
    StagingBucket        string // gs:// staging bucket — REQUIRED for multimodal native eval (inline bytes unsupported, §6); gs:// stripped
    APIEndpoint          string // optional override
    RegistryDBPath       string // default ~/.config/mizan/registry.db (os.UserConfigDir)
    PackCacheDir         string // default ~/.cache/mizan/packs  (for `import <git-url>`)
    DefaultTemplatesRepo string // default "github.com/ghchinoy/mizan-templates" (rev 3) — bare `registry import` source
}
```

> **`StagingBucket` is no longer optional for multimodal.** Per spike-core, native
> `EvaluateInstances` rejects inline bytes, so any non-text native eval needs a
> `gs://` staging bucket. See §6 and the P1-prerequisite note in
> implementation-plan.md. Text-only eval and the `genai` custom-schema path do not
> require it.

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

**SQLite driver — recommendation firmed to pure-Go `modernc.org/sqlite` (rev 3).**
The rev-3 templates CI `go install`s `cmd/mizan` to obtain the validator
(collaboration-design §3.7); with the cgo `mattn/go-sqlite3` driver that CI would
need a C toolchain. Adopting the pure-Go `modernc.org/sqlite` driver (drop-in
`database/sql`) keeps `go install .../cmd/mizan@<ver>` cgo-free in a stock
`setup-go` runner — for the templates CI *and* for end users installing the CLI.
Spike 5 validated the storage layer against `mattn/go-sqlite3`, but the interface
and JSON-column approach are driver-agnostic, so swapping the driver is low-risk.
**Confirmed in P1 (WI-P1-1):** `go.mod` pins `modernc.org/sqlite v1.56.0`; the
`mattn/go-sqlite3` line in the dependency block above is superseded and kept
only as the historical rationale for why the pure-Go driver was chosen.

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

1. **Module layout** — **RESOLVED (spike-core Spike 0): single Go module.** §5.
2. **SQLite driver** — **RESOLVED (confirmed in P1, WI-P1-1): pure-Go
   `modernc.org/sqlite`** (cgo-free `go install` for the rev-3 templates CI and
   end users; see `go.mod`). §8.
3. **Eval-service region / placeholder syntax / inline bytes** — **RESOLVED
   (spike-core):** default `us-central1`; double-brace `{{x}}`; **inline bytes
   unsupported on native eval → GCS staging mandatory for multimodal.** §6/§7.
4. **`apiv1beta1` preview stability posture** for a production tool
   (research.md §7 open item) — confirm breaking-change cadence before GA claims.
5. **Pack repo location** — **RESOLVED (user, 2026-08-09, rev 3): dedicated
   `github.com/ghchinoy/mizan-templates` repo** (supersedes rev-2 in-repo). See
   collaboration-design.md §3.3/§3.7.
6. **GCS staging provisioning (NEW, spike-core):** the eval SA `sa-scion-warmup`
   lacks `storage.buckets.create/list`; a staging bucket must be **provisioned**
   (or the SA granted `storage.admin`) before P1 multimodal eval works. Hard
   dependency — see implementation-plan.md P1 prerequisites. *Raise to user/owner.*
7. **Validator provisioning for the templates CI (rev-3):** no tagged `mizan`
   release exists to pin `MIZAN_VERSION`; needs a release/tag workflow or a `main`
   pseudo-version, plus a `MIZAN_RO_TOKEN` secret. collaboration-design §3.7/§7.

---

## 11. Acceptance Criteria (architecture level)

- `go build ./...` succeeds on go1.26.1 as a **single module** (§5); `go vet`
  clean. **No `packs/` tree or pack CI exists in this repo** (rev 3) — they live
  in `mizan-templates`.
- Dependency-direction check passes: `cmd/*` imports only `registry.Service`,
  `eval.Engine`, `config` — never `sqlite`/`sync`/`codec`/`aiplatformpb`.
- `eval` unit tests pass against a mocked `EvaluationClient`; integration tests
  compile behind `//go:build integration`.
- `go install github.com/ghchinoy/mizan/cmd/mizan@<ver>` succeeds **cgo-free** in a
  stock `setup-go` runner (pure-Go SQLite driver, §8) — the rev-3 templates-CI
  requirement.
- Remaining **pending spike verdicts** (§10 Q4) are resolved and recorded in
  docs/research.md §7 before the packages they gate are implemented; the
  spike-core verdicts (§5/§6/§7/§8) are already folded in here.

---

## 12. CLI surface (Cobra)

Folded from the retired draft §6 and reconciled to the rev-3 command set (§3).
This block is kept as **historical/design-level** and is not updated
flag-by-flag as the CLI evolved (for example, the shipped flags are `--prompt`
and `--flip-enabled`, not `--prompt-template`/`--flip` as sketched below). For
the real, current, live-verified CLI surface, see
[`docs/testing-guide.md`](testing-guide.md) and
[`docs/user-guide.md`](user-guide.md).

```
# Registry — local working-copy CRUD
mizan registry create --name <n> --kind pointwise --modality text \
    --prompt-template ./template.txt [--modality image ...]
mizan registry list
mizan registry get <name>
mizan registry update <name> [--prompt-template ./template.txt] [...]
mizan registry delete <name>

# Registry — share (contribution layer; see collaboration-design.md §3.6)
mizan registry import <src> [--strategy newer|skip|overwrite|fork] \
    [--namespace <ns>] [--dry-run]   # src = git URL | pack dir; bare src = default templates repo
mizan registry export <name> --out <dir>          # write template(s) as an on-disk pack

# Packs — author/scaffold and validate locally
mizan pack init <dir>                             # scaffold a new pack (manifest + skeleton)
mizan pack validate <dir>                         # structural + lint validation
mizan pack add <dir> <name>                       # add a template to a pack

# Eval
mizan eval run --metric <name> \
    --field response=./output.mp4 \
    --field prompt="a dog running in a park" \
    [--sampling-count 4] [--flip] [--output json|table]
mizan eval pairwise --metric <name> \
    --candidate ./a.mp3 --baseline ./b.mp3 --field context="..."

# Config
mizan config show
mizan config set project-id ...
```

Follows the `drivectl` precedent: `cobra.Command.GroupID` groups the `registry`,
`pack`, `eval`, and `config` subcommand families. Per the dependency direction in
§3, `cmd/mizan` imports only `registry.Service`, `eval.Engine`, and `config` —
never `sqlite`/`sync`/`codec`/`aiplatformpb`.

---

## 13. Wails desktop bindings (Phase 4)

Folded from the retired draft §7. **Phase 4 — not the MVP.** `internal/app/app.go`
binds a thin layer that wraps `registry.Service` and `eval.Engine` (the rev-3 seam,
§3); it holds no domain logic of its own:

```go
func (a *App) ListMetricTemplates() ([]registry.MetricTemplate, error)
func (a *App) SaveMetricTemplate(t registry.MetricTemplate) error
func (a *App) DeleteMetricTemplate(id string) error
func (a *App) RunEvaluation(metricID string, fields map[string]string) (eval.Result, error)
func (a *App) PickFile() (string, error)          // wraps wails runtime.OpenFileDialog
func (a *App) GetConfig() (config.Config, error)
func (a *App) SaveConfig(c config.Config) error
```

Frontend stack: Lit + Vite, matching the `eldamo-app` precedent (avoids
introducing React/Vue tooling churn); `wails.json` adapted from `eldamo-app` with
name/paths adjusted. The Go and Wails toolchain versions are governed by §4 and §5
and are deliberately not restated here to avoid drift.
