# Mizan — Research: Vertex AI Gen AI Evaluation Service (Go)

Status: research complete, pre-implementation
Date: 2026-08-07

## 1. Goal recap

Mizan is a system to create, manage, and run Gemini-based autoraters ("evaluator
templates") stored in a **Metric Registry**, and use them to score arbitrary
assets (text, audio, image, video, music) against a prompt or reference
statement (e.g. a brand guideline). Needs both a Go CLI and a Wails desktop app
sharing the same core.

## 2. Key finding: use `apiv1beta1`, not GA `apiv1`

`cloud.google.com/go/aiplatform` ships two API surfaces:

| Capability | `apiv1` (GA) | `apiv1beta1` (preview) |
|---|---|---|
| `EvaluateInstances` (sync single-call eval) | yes | yes |
| `AutoraterConfig` (sampling_count, flip_enabled, autorater_model) | no | yes |
| Multimodal instances (`ContentMap`) | no (JSON string only) | yes |
| `EvaluateDataset` (async/batch, GCS/BigQuery in, GCS out) | no | yes |
| Rubric-based metrics (`LLMBasedMetricSpec`, `RubricBasedInstructionFollowing*`) | no | yes |

**Decision: Mizan's Go core imports `cloud.google.com/go/aiplatform/apiv1beta1`
and `apiv1beta1/aiplatformpb` exclusively.** There is no reason to touch GA
`apiv1` for eval -- it lacks everything we need (multimodal, autorater config,
batch).

## 3. Core proto types (v1beta1) relevant to Mizan

```go
// Pointwise
type PointwiseMetricSpec struct {
    MetricPromptTemplate     *string // required; supports {{placeholder}} substitution
    SystemInstruction        *string
    CustomOutputFormatConfig *CustomOutputFormatConfig // oneof: ReturnRawOutput bool
}
type PointwiseMetricInstance struct {
    // oneof:
    //   JsonInstance       string      // text-only: JSON blob of placeholder->string
    //   ContentMapInstance *ContentMap // multimodal (v1beta1 only)
}
type PointwiseMetricInput struct {
    MetricSpec *PointwiseMetricSpec
    Instance   *PointwiseMetricInstance
}
type PointwiseMetricResult struct {
    Score        *float32
    Explanation  string
    CustomOutput *CustomOutput // raw string(s) only if ReturnRawOutput=true
}

// Pairwise -- mirrors Pointwise, plus:
type PairwiseMetricSpec struct {
    MetricPromptTemplate       *string
    CandidateResponseFieldName string
    BaselineResponseFieldName  string // paired with AutoraterConfig.FlipEnabled
    SystemInstruction          *string
    CustomOutputFormatConfig   *CustomOutputFormatConfig
}
type PairwiseMetricResult struct {
    PairwiseChoice PairwiseChoice // BASELINE | CANDIDATE | TIE
    Explanation    string
    CustomOutput   *CustomOutput
}

// Autorater / judge model config
type AutoraterConfig struct {
    SamplingCount  *int32  // default 4, range 1-32
    FlipEnabled    *bool   // default true, reduces position bias in pairwise
    AutoraterModel string  // publisher model or tuned endpoint resource name
}
```

### Multimodal support -- `ContentMap`

```go
type ContentMap struct {
    Values map[string]*ContentMap_Contents // placeholder name -> Contents
}
type ContentMap_Contents struct { Contents []*Content }

// Content/Part/Blob/FileData are the SAME types used by Gemini generateContent:
type Content struct { Role string; Parts []*Part }
type Part struct {
    // oneof: Text string | InlineData *Blob | FileData *FileData | ...
}
type Blob struct { MimeType string; Data []byte }       // inline bytes
type FileData struct { MimeType string; FileUri string } // gs:// URI or public URL
```

**This means image/audio/video/music evaluation is natively supported** by
`EvaluateInstances` in v1beta1 -- no need for a separate hand-rolled Gemini
`generateContent` fallback for the common pointwise/pairwise score+explanation
case. Reference the asset either as inline bytes (`Blob`) or a GCS URI
(`FileData.FileUri`), tagged with the correct MIME type, keyed to the
placeholder name used in `MetricPromptTemplate` (e.g. `{{response}}`).

### Where a custom Gemini call IS still needed

`CustomOutputFormatConfig` only supports `ReturnRawOutput bool` -> raw
string(s), not a JSON-Schema-typed structured response. If a registry entry
needs a **strictly typed custom schema** (e.g. multiple named sub-scores, an
array of flagged issues, etc. -- beyond `{score, explanation}` /
`{pairwise_choice, explanation}`), native `EvaluateInstances` cannot express
that. For those cases, fall back to a direct `google.golang.org/genai` call
with `GenerateContentConfig.ResponseSchema` -- same pattern as the
`custom_metric_fn` in the Python reference script we found (see section 6). This
should be an explicit "metric kind" in the registry (`native` vs
`custom_schema`), not a silent fallback.

### Rubric-based metrics

`LLMBasedMetricSpec` supports a rubric source (`RubricGroupKey` referencing an
inline `rubric_groups` map, or `PredefinedRubricGenerationSpec` to
auto-generate rubrics). **Important: there is no server-side "rubric registry"
resource** -- rubrics must be supplied inline per request/dataset row. Mizan's
own Metric Registry is the natural place to persist named rubric sets and
convert them into the inline map at eval time.

### No native "Metric Registry" or "Prompt Template" GCP resource

Confirmed: there is no GCP-native resource for storing named metric templates
(`SavedQuery` is an annotation/labeling concept, unrelated). **The Metric
Registry is 100% Mizan's own construct** -- store it in a local DB (SQLite,
matching `eldamo-app` precedent) or optionally sync to Firestore/GCS. At
eval time, a registry entry is materialized into the appropriate `*Spec` +
`*Instance` proto structs.

### Batch/async evaluation

```go
type EvaluateDatasetRequest struct {
    Location        string
    Dataset         *EvaluationDataset  // oneof: GcsSource | BigquerySource
    Metrics         []*Metric           // oneof spec incl. Pointwise/Pairwise/LlmBased/...
    OutputConfig    *OutputConfig       // GcsDestination
    AutoraterConfig *AutoraterConfig
}
```
Returns a standard LRO (`*EvaluateDatasetOperation`) using the same
`longrunning` package conventions as elsewhere in the SDK
(`op.Wait(ctx)`/`op.Poll(ctx)`). `EvaluationDataset` rows are JSON/BigQuery
records, not first-class `ContentMap` -- multimodal batch rows must reference
GCS URIs as string fields interpreted via the metric's template. This is a
"Phase 2" capability, not needed for MVP CLI/GUI single-run eval.

### `EvaluationClient` methods (v1beta1)

- `EvaluateInstances(ctx, req) (*EvaluateInstancesResponse, error)` -- sync,
  one metric per call. **This is the workhorse for Mizan's interactive/CLI
  single-item scoring.**
- `EvaluateDataset(ctx, req) (*EvaluateDatasetOperation, error)` -- async LRO,
  for batch scale-out (Phase 2).
- `EvaluateDatasetOperation(name)` -- reattach to a running op by name.

## 4. Go SDK for Gemini calls (custom-schema fallback path)

`google.golang.org/genai` (modern unified SDK, `v1.65.0` at time of writing)
is the correct client for any custom Gemini call:

```go
client, err := genai.NewClient(ctx, &genai.ClientConfig{
    Backend:  genai.BackendVertexAI,
    Project:  projectID,
    Location: location,
})
resp, err := client.Models.GenerateContent(ctx, autoraterModel, contents,
    &genai.GenerateContentConfig{
        ResponseMIMEType: "application/json",
        ResponseSchema:   mySchema, // *genai.Schema for strict typing
        ThinkingConfig:   &genai.ThinkingConfig{ThinkingBudget: -1},
    })
```
This mirrors patterns already used in this user's other repos
(`go-code/internal/agent/vertex.go`, `mcp-genmedia-go/mcp-gemini-go`,
`moonshine-experiments/scenarios/qa/eval.go`).

## 5. Prior art in this workspace (reference only, do not copy verbatim)

- `vertex-ai-creative-studio/experiments/veo-genetic-prompt-optimizer/
  veo_genetic_prompt_optimizer/evaluate_prompts.py` -- Python reference impl
  using `vertexai.preview.evaluation` (`EvalTask`, `PointwiseMetric`,
  `PairwiseMetric`, `AutoraterConfig`, `CustomMetric`). Good reference for
  behavior (pointwise/pairwise x single/batch x text/multimodal), but it's
  Python and predates the native `ContentMap` multimodal support in the Go
  proto -- its multimodal path (`CustomMetric` calling Gemini directly) is
  what Mizan should replace with native `ContentMap`-based `EvaluateInstances`
  calls, only falling back to direct Gemini calls for strict custom schemas.
- `moonshine-experiments/scenarios/qa/eval.go` -- Go LLM-as-judge pattern
  (`JudgeResponse{Score, Rationale}`, `ResponseMIMEType: "application/json"`,
  manual `json.Unmarshal`). Useful pattern for the custom-schema fallback
  path; note it has **no retry logic** on the judge call -- Mizan should add
  exponential backoff (see `_generate_content_with_retry` in the Python
  reference for the shape of this).
- `upstream-gmcs/experiments/mcp-genmedia/mcp-genmedia-go/mcp-common/
  config.go` -- `LoadConfig()` env-var pattern (`PROJECT_ID` required/fatal,
  `LOCATION` defaults to `us-central1`, `GENMEDIA_BUCKET` optional w/
  `gs://` stripped, `VERTEX_API_ENDPOINT` optional). Reuse this shape for
  Mizan's config.
- `eldamo-app/` -- mature Wails v2 (v2.13.0) + Go 1.25 reference architecture.
  See `docs/architecture.md` for how Mizan should mirror its
  `internal/<domain>` + `internal/app` + `cmd/` split.

## 6. Multimodal input handling -- asset ingestion

For all modalities (image, audio, video, music), the asset needs to become a
`Part` -- either:
- **Inline bytes** (`Blob{MimeType, Data}`) for small files, read directly
  from local disk (CLI) or file picker (Wails).
- **GCS URI** (`FileData{MimeType, FileUri}`) for large files -- Mizan should
  support an optional "upload to GCS staging bucket" step for assets that
  exceed inline request size limits (Gemini/Vertex has request payload size
  ceilings -- typically single-digit MB region for inline; check current
  limits at implementation time, this shifts by model/region).

MIME type detection: use Go's `net/http.DetectContentType` for a first pass,
falling back to file extension mapping for formats it doesn't recognize well
(e.g. some audio/video containers).

## 7. Answered vs open questions

**Answered:**
- Native multimodal Eval API exists (v1beta1 `ContentMap`) -- use it.
- No native Metric/Rubric Registry resource -- must build our own.
- Cobra is the user's established Go CLI framework (20+ prior projects).
- Wails v2.13.0 + Lit/Vite is the user's established desktop stack
  (`eldamo-app`).

**Open (to resolve during spikes, see docs/spikes.md):**
- Storage backend for the Metric Registry: SQLite (matches `eldamo-app`
  precedent, zero external deps) vs. Firestore (if multi-user/cloud sync is
  a goal). Recommendation: start SQLite, local-first, matching `eldamo-app`.
- Whether `apiv1beta1` proto stability is acceptable for a production tool,
  given "preview" status -- need to confirm current deprecation/stability
  posture before committing (check CHANGES.md history for breaking changes
  cadence).
- Exact inline-request payload size limits per modality/model at
  implementation time (verify against current Vertex AI quota docs, these
  change).
- Whether Mizan needs its own GCS staging bucket management (upload assets
  before eval) as MVP, or whether "provide a gs:// URI yourself" is
  sufficient for v1.
