# Mizan — Architecture Plan

Status: draft for review, pre-implementation
Date: 2026-08-07

## 1. Guiding principle

One shared Go core (`internal/`), zero UI-framework dependencies in the core,
two thin frontends: a Cobra CLI (`cmd/mizan`) and a Wails v2 desktop app
(`cmd/mizan-desktop` + `internal/app`). This mirrors the `eldamo-app`
precedent in this workspace (internal/db shared by internal/app and cmd/*).

## 2. Module layout (proposed)

```
mizan/
  go.mod                          module github.com/ghchinoy/mizan
  cmd/
    mizan/                        Cobra CLI entrypoint
      main.go
      root.go
      registry.go                 `mizan registry create|list|get|update|delete`
      eval.go                     `mizan eval run --metric <name> --input ...`
      config.go                   `mizan config set|get`
    mizan-desktop/                Wails v2 entrypoint
      main.go
      wails.json
      frontend/                   Lit + Vite (matches eldamo-app stack)
  internal/
    config/
      config.go                   LoadConfig() -- PROJECT_ID/LOCATION/etc, .env support
    registry/                     Metric Registry (pure domain logic)
      model.go                    MetricTemplate struct, Modality enum, MetricKind enum
      store.go                    Store interface (CRUD)
      sqlite/
        sqlite.go                 SQLite-backed implementation (default)
    eval/                         Evaluation engine
      engine.go                   Engine.Run(ctx, MetricTemplate, Instance) (Result, error)
      native.go                   apiv1beta1 EvaluateInstances path (pointwise/pairwise)
      custom.go                   genai fallback path for strict custom-schema metrics
      content.go                  asset -> ContentMap/Part/Blob/FileData construction
      batch.go                    (Phase 2) EvaluateDataset wrapper
    asset/
      mime.go                     MIME detection, size checks
      gcs.go                      optional GCS staging upload helper
    app/                          Wails-bound layer (GUI-only, thin)
      app.go                      App struct binds internal/registry + internal/eval
  docs/
    research.md
    architecture.md
    spikes.md
```

## 3. Core domain types (draft)

```go
// internal/registry/model.go
type Modality string
const (
    ModalityText  Modality = "text"
    ModalityImage Modality = "image"
    ModalityAudio Modality = "audio"
    ModalityVideo Modality = "video"
    ModalityMusic Modality = "music" // audio subtype, may just alias ModalityAudio
)

type MetricKind string
const (
    KindPointwise    MetricKind = "pointwise"
    KindPairwise     MetricKind = "pairwise"
    KindRubricBased  MetricKind = "rubric"
    KindCustomSchema MetricKind = "custom_schema" // forces genai fallback path
)

type MetricTemplate struct {
    ID                    string
    Name                  string
    Description           string
    Kind                  MetricKind
    Modalities            []Modality // which asset types this template accepts
    MetricPromptTemplate  string     // {{placeholder}} syntax, matches proto field
    SystemInstruction     string
    CandidateFieldName    string     // pairwise only
    BaselineFieldName     string     // pairwise only
    ResponseSchema        *Schema    // only for KindCustomSchema; JSON schema
    AutoraterModel        string     // default publisher model, e.g. gemini-2.5-pro
    SamplingCount         int32      // AutoraterConfig.SamplingCount, default 4
    FlipEnabled           bool       // AutoraterConfig.FlipEnabled, default true (pairwise)
    RubricGroup           map[string]string // for KindRubricBased
    CreatedAt, UpdatedAt  time.Time
}
```

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
    FilePath string // local file, will be read inline or staged to GCS
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

## 4. Engine dispatch logic

```
Engine.Run(tmpl, inst):
  switch tmpl.Kind:
    case Pointwise, Pairwise:
      if tmpl has only ModalityText inputs and no per-metric custom schema:
        -> native.go: build PointwiseMetricSpec/PairwiseMetricSpec + JsonInstance
           -> EvaluateInstances
      else (any non-text asset present):
        -> native.go: build *Spec + ContentMapInstance (Blob or FileData per asset)
           -> EvaluateInstances   [[still native -- v1beta1 supports this]]
    case RubricBased:
      -> native.go: LLMBasedMetricSpec w/ RubricGroupKey -> inline rubric_groups map
         -> EvaluateInstances
    case CustomSchema:
      -> custom.go: direct genai.GenerateContentConfig{ResponseSchema: tmpl.ResponseSchema}
         call, manual retry w/ backoff, parse JSON response into CustomOutput
```

Both native.go and custom.go share `content.go` helpers for turning an
`AssetRef` into a `*genai.Part` / `*aiplatformpb.Part` (note: these are
*different* Go types from *different* packages even though structurally
similar -- `apiv1beta1/aiplatformpb.Part` vs `google.golang.org/genai.Part` --
content.go needs two small converters, not a shared type).

## 5. Config (`internal/config`)

Mirrors `mcp-common/config.go` pattern from this workspace:

```go
type Config struct {
    ProjectID      string // required, fatal if unset (env: PROJECT_ID or MIZAN_PROJECT_ID)
    Location       string // default: us-central1
    StagingBucket  string // optional, gs:// prefix stripped
    APIEndpoint    string // optional override
    RegistryDBPath string // default: ~/.config/mizan/registry.db (or XDG equivalent)
}
func LoadConfig() (*Config, error) // returns error instead of log.Fatal --
                                    // both CLI and GUI need graceful handling,
                                    // GUI can't just os.Exit on missing config.
```

Note: deviate slightly from the `mcp-common` precedent (which does
`log.Fatal`) since the Wails GUI must show a config dialog on missing
PROJECT_ID rather than crash. CLI's `main.go` can choose to treat the
returned error as fatal; GUI's `Startup()` shows a setup screen instead.

## 6. CLI surface (Cobra, draft)

```
mizan registry create --name <n> --kind pointwise --modality text \
    --prompt-template ./template.txt [--modality image ...]
mizan registry list
mizan registry get <name>
mizan registry delete <name>

mizan eval run --metric <name> \
    --field response=./output.mp4 \
    --field prompt="a dog running in a park" \
    [--sampling-count 4] [--flip] [--output json|table]

mizan eval pairwise --metric <name> \
    --candidate ./a.mp3 --baseline ./b.mp3 --field context="..."

mizan config show
mizan config set project-id ...
```

Follows the `drivectl` precedent: `cobra.Command.GroupID` to group
`registry`, `eval`, `config` subcommand families; `internal/registry` and
`internal/eval` are the only imports needed by `cmd/mizan`.

## 7. Wails desktop app surface (draft)

`internal/app/app.go` binds a thin layer over the same core:

```go
func (a *App) ListMetricTemplates() ([]registry.MetricTemplate, error)
func (a *App) SaveMetricTemplate(t registry.MetricTemplate) error
func (a *App) DeleteMetricTemplate(id string) error
func (a *App) RunEvaluation(metricID string, fields map[string]string) (eval.Result, error)
func (a *App) PickFile() (string, error)          // wraps wails runtime.OpenFileDialog
func (a *App) GetConfig() (config.Config, error)
func (a *App) SaveConfig(c config.Config) error
```

Frontend stack: Lit + Vite, matching `eldamo-app` (proven in this workspace,
avoids introducing React/Vue tooling churn). `wails.json` template copied
from `eldamo-app` with name/paths adjusted. Go 1.25.x, Wails v2.13.0 pinned.

## 8. Persistence

- Default: SQLite file at `~/.config/mizan/registry.db` (or platform
  equivalent via `os.UserConfigDir()`), using `mattn/go-sqlite3` (matches
  `eldamo-app`). No vector search needed here (unlike eldamo), so this can be
  a much simpler schema: single `metric_templates` table, JSON-serialized
  complex fields (Modalities, RubricGroup, ResponseSchema) in TEXT columns.
- `internal/registry.Store` is an interface so a Firestore-backed
  implementation could be added later without touching CLI/GUI code.

## 9. Dependencies (draft go.mod)

```
require (
    cloud.google.com/go/aiplatform v1.121.0   // apiv1beta1 only
    google.golang.org/genai v1.65.0           // custom-schema fallback path
    github.com/spf13/cobra v1.10.2
    github.com/mattn/go-sqlite3 <version>
    github.com/joho/godotenv <version>
    github.com/wailsapp/wails/v2 v2.13.0      // cmd/mizan-desktop only (separate go.mod? see open Q below)
)
```

**Open question for spikes:** should `cmd/mizan-desktop` be a separate Go
module (its own go.mod) to avoid forcing the CLI-only user to pull in Wails'
transitive deps (webview bindings, etc.)? `eldamo-app` uses a single module
for its whole repo. Given Mizan's CLI is meant to be a lightweight,
scriptable tool (possibly used in CI), **recommend evaluating a Go workspace
(`go.work`) with two modules**: `mizan-core` (CLI + internal/) and
`mizan-desktop` (Wails, depends on mizan-core via replace/workspace). Confirm
during Spike 1.

## 10. Testing strategy

- `internal/registry`: pure unit tests, in-memory or temp-file SQLite.
- `internal/eval`: unit tests with a mocked `EvaluationClient` interface
  (define a narrow interface wrapping just `EvaluateInstances` so it can be
  faked in tests without hitting real Vertex AI).
- Integration tests behind a build tag (`//go:build integration`) that
  actually call Vertex AI, gated by `PROJECT_ID` env var presence, run
  manually / in a separate CI job with credentials.
