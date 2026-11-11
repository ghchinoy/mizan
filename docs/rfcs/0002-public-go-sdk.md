# RFC-0002: Public Go Evaluation SDK (`pkg/mizan`) and Multimodal N-Variation Engine

* **Author:** Hussain Chinoy
* **Status:** Proposed (EGM Review Resolved; Checked In at `docs/rfcs/0002-public-go-sdk.md`)
* **Date:** 2026-11-10
* **Owner / Approver:** ghchinoy@google.com

--------------------------------------------------------------------------------

## Overview

**Mizan** (`module github.com/ghchinoy/mizan`) is a Go command-line interface (`cmd/mizan`), Model Context Protocol (MCP) server (`internal/mcpserver`), and evaluation engine (`internal/eval`) for authoring, validating, and running LLM-as-a-Judge and deterministic evaluations across text, image, audio, video, and music. Shared declarative evaluation specifications live in the companion Git repository **`mizan-templates`** as YAML manifests (`apiVersion: mizan.dev/v1alpha1`) of two kinds:

* **`MetricTemplate`**: Defines a single evaluation metric (`internal/registry`), its declared input placeholders (`spec.inputs`), its prompt or rubric criteria, its `autorater` judge configuration (`model`, `samplingCount`, `flipEnabled`), and a deterministic SHA-256 content digest computed over its semantic fields to detect local edits and sync conflicts.
* **`EvalSet`**: Defines an ordered suite of weighted or required `MetricTemplate` members (`internal/evalset`), suite-level and per-member input alias bindings (`spec.inputs` and `spec.members[].bind`, mapping `setInputName -> memberPlaceholderName`), a score aggregation rule (`mean`, `weighted-mean`, or `min` at runtime; `max`, `median`, and `sum` are schema-reserved), and an optional pass/fail threshold gate.

Each `MetricTemplate` dispatches by **`MetricKind`** across four execution substrates inside `internal/eval`:

1. **Structured `genai` Path (`GenerateContent`)**: Calls `google.golang.org/genai` (`Models.GenerateContent`) with `ResponseMIMEType: "application/json"` and a `ResponseSchema`. Serves `boul` (boolean proposition with confidence), `choice` (discrete category selection), `score` (numeric or rubric rating), `custom_schema` (caller-authored JSON schema), per-criterion structured `rubric`, and opt-in `genai` `pairwise`. Accepts inline asset bytes up to `20 MiB` and URI references (`FileData`).
2. **Vertex Native Path (`EvaluateInstances`)**: Calls the Vertex AI Gen AI Evaluation Service gRPC API (`cloud.google.com/go/aiplatform/apiv1beta1`, `EvaluationClient.EvaluateInstances`), automatically routing newer preview judge families (`gemini-3.x`, `gemini-4.x`) to Vertex's `global` endpoint when regional endpoints do not host them. Serves `pointwise` (single-item score), `pairwise` (baseline vs. candidate comparison with position-flip de-biasing `FlipEnabled` and `SamplingCount`), `rubric` (native rubric groups), and `prebuilt` (Google-authored service-side prompts such as `safety`, `groundedness`, and `fluency` whose prompt text lives inside the Vertex service). Accepts `gs://` Cloud Storage `FileData` URIs only for multimodal assets.
3. **Diffusion Path (`DiffusionGemma`)**: Calls a discrete block-diffusion judge endpoint (`internal/eval/diffusion`) supporting up to 26 discrete choice slots for low-latency evaluation and entropy-cascade routing.
4. **Model-Free Local Path**: Executes `heuristic` checks (substring, RE2 regex, exact match, JSON validity, JSON Schema validity) and `computation` metrics (BLEU, ROUGE, exact match, tool-call and trajectory checks) in pure Go with zero network calls or credentials.

Every completed model-backed evaluation records an applied-judge provenance record capturing the resolved model ID, model source (`flag`, `template`, `config-default`, `builtin`), and target host/location (unlike Vertex `EvaluateInstances`, caller-supplied `*genai.Client` instances target the endpoint already configured on that client). Automated reflection tests (**`skilldocs` drift gates** in `internal/skilldocs/`) verify that CLI `-o json` output keys and `config` keys match Mizan's agent skill documentation (`SKILL.md`).

This RFC defines **`github.com/ghchinoy/mizan/pkg/mizan`**, a SQLite-free public Go SDK over Mizan's evaluation and YAML catalog layers, paired with targeted upgrades to `internal/eval`, `internal/evalset`, and Mizan's SQLite stores so downstream Go services—specifically **`omni-editingbay`**, a Go multimodal media-editing application that generates `N` candidate video/audio cuts per shot at runtime and evaluates them via Gemini Enterprise (`genai.BackendEnterprise`, e.g., `gemini-4-argon`)—can import and run Mizan evaluations in-process.

## The Problem

Downstream Go applications such as `omni-editingbay` currently face five architectural blockers when evaluating runtime media variations with Mizan: external Go code cannot import Mizan in-process, and shelling out to `mizan eval run` fails whenever a workflow requires `genai.BackendEnterprise`, concurrent local database access, or dynamic `N`-way choice sets:

1. **Complete `internal/` Package Encapsulation**: Every Go package in Mizan (`internal/eval`, `internal/registry`, `internal/evalset`, `internal/asset`, `internal/wire`, `internal/results`) resides under `internal/`, enforced by Go compiler visibility rules. External Go modules cannot import Mizan's engine, YAML codecs, or `EvalSet` runner.
2. **Hardcoded `genai.BackendVertexAI` and Uninjectable `*genai.Client`**: Mizan's internal client constructor hardcodes `Backend: genai.BackendVertexAI` with a 3-field `genai.ClientConfig{Backend, Project, Location}`, discarding caller-configured `genai.BackendEnterprise` settings, custom `HTTPOptions` (`BaseURL`, headers, `RetryOptions`), and `Credentials`. Meanwhile, the CLI composition root (`internal/wire`) unconditionally requires a Vertex `ProjectID` and constructs regional and global `aiplatform.EvaluationClient` instances, and `internal/eval` fails when `pointwise`, default `rubric`, or default `pairwise` templates run without a Vertex `EvaluationClient`. Model ID validation also rejects `"models/<id>"` prefixes, and template-pinned `spec.autorater.model: gemini-3.5-flash-lite` outranks the engine fallback model, preventing an Enterprise engine from overriding imported pack models to `gemini-4-argon` without per-call repetition.
3. **Text-Before-Video Multimodal Part Ordering and `20 MiB` File-Only Input Limits**: On the structured `genai` path, Mizan constructs `contents[0].Parts` by placing the entire rendered prompt and rubric text *ahead* of media parts, violating Gemini multimodal video prompting guidance (where media tokens should precede prompt and rubric instruction text) and detaching multiple videos from their labels. Prompt rendering also emits a malformed label when an asset's modality is unset, duplicates media parts if a placeholder appears twice, and accepts only local file paths (`<= 20 MiB`) or `gs://` URIs—blocking in-memory `[]byte`, `https://` signed/Enterprise URIs (`> 20 MiB`), and `*genai.Part` values carrying `VideoMetadata` (FPS and clip start/end offsets) or per-part `MediaResolution`.
4. **Static `spec.choices` and 1:1 Placeholder Validation Blocking `N` Runtime Variations**: `choice` evaluation requires static `spec.choices` (`minItems: 2`) in YAML, while instance validation enforces a 1:1 bijection between `{{placeholder}}` tokens in the prompt template and `Instance` field keys. When `omni-editingbay` generates `N` candidate takes (`take_1 .. take_N`, where `N` varies per shot), it cannot pass `N` media variations or specialize the allowed JSON enum choices without mutating YAML files on disk.
5. **Cross-Process SQLite Write-Lock Contention and `EvalSet` Subset Failures**: Shelling out to `mizan eval run` opens `~/.config/mizan/registry.db` and `results.db` with zero busy timeout, `DELETE` journal mode, and an unconditional deferred transaction during startup migration that rewrites the schema version on **every** `Open()`—causing immediate `SQLITE_BUSY` failures across concurrent CLI runs even on read-only template lookups. Separately, `internal/evalset` copies all suite-level inputs into every member's `Instance` without resolving `Set.Inputs` or `Member.Bind`, causing member validation to fail any `EvalSet` whose members consume distinct subsets of suite inputs.

## Goals

* Expose `github.com/ghchinoy/mizan/pkg/mizan` as a SQLite-free, `os.Stderr`-silent public Go SDK for loading `MetricTemplate` and `EvalSet` specifications (from `[]byte`, `io/fs.FS`, or Go structs) and executing single metrics and `EvalSet` suites in-process.
* Support caller-configured `*genai.Client` instances (including `genai.BackendEnterprise` with explicit backend provenance labeling, `gemini-4-argon`, `"models/<id>"` prefixes, custom `HTTPOptions`, and an engine-level `WithModelOverride`) alongside a single-method `Generator` interface for offline unit testing in downstream applications.
* Place labeled media preamble `genai.Part` items ahead of all prompt and rubric instruction text (**Media-Preamble-First**) on the `genai` structured path without duplicating repeated placeholders, fix empty-modality label formatting, and support `URI` (`gs://` and SSRF-validated `https://`), bounded `Bytes` (`<= 20 MiB` aggregate inline per request), and `*genai.Part` (`VideoMetadata`, `MediaResolution`) inputs.
* Support runtime `N`-way candidate `Variation` lists bound to a single declared placeholder (e.g., `{{variations}}`) with synchronized per-run `WithChoices` or `WithChoicesFromVariations` specialization, without mutating YAML on disk or changing existing template SHA-256 `ContentHash` digests.
* Auto-route `pointwise`, `rubric`, and `pairwise` templates through structured `genai` evaluators when no Vertex `EvaluationClient` is configured (failing fast with an actionable error only on Vertex-exclusive `prebuilt` templates), populate `Result.Explanation` across all model-backed paths (including structured `rubric`), and surface structured finish-reason, block-reason, and retry telemetry.
* Fix `EvalSet` per-member input projection and `Set.Inputs` / `Member.Bind` (`setInputName -> memberPlaceholderName`) alias resolution, and eliminate SQLite startup write locks on read-only opens of migrated databases while serializing concurrent writers under `WAL` mode and a 5-second busy timeout.

## Non-Goals

* Promoting `internal/registry/sqlite`, `internal/results/sqlite`, `internal/mcpserver`, `internal/wire`, or `cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb` types into Mizan's public Go API surface.
* Splitting Mizan into multiple `go.mod` modules in this change (deferred unless a downstream consumer requires removing unused root `go.mod` MVS version floors).
* Executing `KindPrebuilt` metrics on `genai`-only / `BackendEnterprise` engines without a Vertex `EvaluationClient` (prebuilt judge prompts are proprietary to the managed Vertex `EvaluateInstances` service).
* Raising the `20 MiB` aggregate inline byte limit on `GenerateContent` calls (`google.golang.org/genai` base64-inflates inline buffers ~5x in heap and gateways reject oversized JSON bodies; SDK callers with media `> 20 MiB` pass `URI` or `FromGenaiPart` `FileData` references).

## Critical User Journeys (CUJs)

### CUJ 1: In-Process Enterprise N-Way Video Variation Selection

* **Given**: `omni-editingbay` holds an initialized `*genai.Client` configured with `Backend: genai.BackendEnterprise`, an embedded `embed.FS` containing a `kind: choice` `MetricTemplate` with placeholders `{{variations}}` and `{{brief}}` (whose YAML pins `spec.autorater.model: gemini-3.5-flash-lite` and default placeholder choices `["candidate_a", "candidate_b"]`), and 4 candidate video renders (`take_1 .. take_4`) referenced by `gs://` or `https://` URIs.
* **When**: `omni-editingbay` constructs `mizan.NewEvaluator(genaiClient, mizan.WithBackendLabel("enterprise"), mizan.WithModelOverride("gemini-4-argon"))`, loads the catalog via `mizan.LoadCatalogFS(embeddedFS)`, and calls `ev.Run` with `mizan.Variations(...)` and `mizan.WithChoicesFromVariations("none")`.
* **Then**: Mizan makes zero SQLite or `os.Stderr` calls, builds `contents[0].Parts` with the 4 labeled video URI parts ahead of the rendered prompt and JSON directive, constrains the `ResponseSchema` enum to `["take_1", "take_2", "take_3", "take_4", "none"]`, invokes `gemini-4-argon` through the caller's `*genai.Client`, verifies the returned `ChoiceSelection` is in that allowed set, and returns a `mizan.Result` with `ChoiceSelection`, `Explanation`, `Stats`, and JSON-serializable `Applied` provenance (`Backend: "enterprise"`, `Model: "gemini-4-argon"`, `ModelSource: "engine-override"`).

### CUJ 2: Video Clip Window Evaluation via `*genai.Part` (`VideoMetadata`)

* **Given**: `omni-editingbay` constructs a `*genai.Part` referencing a 120 MiB source video URI with `VideoMetadata{StartOffset: 10s, EndOffset: 25s, FPS: ptr(2.0)}` (and optional per-part `MediaResolution`) and loads an existing `kind: rubric` or `kind: pointwise` template from `mizan-templates`.
* **When**: `omni-editingbay` wraps the part with `mizan.FromGenaiPart(part)` and calls `ev.Run` on its `*genai.Client`-only `Evaluator`.
* **Then**: Mizan preserves `StartOffset`, `EndOffset`, `FPS`, and per-part `MediaResolution` on the outbound media part, auto-routes `KindRubric` and `KindPointwise` over structured `genai` (`KindPointwise` reusing `score` evaluation without violating `ratingRubric`'s carry-and-reserve contract), populates `Result.Score`, `Result.Explanation`, and (for rubric templates) `Result.Criteria`, and attaches a non-fatal entry in `Result.Warnings` if the template declared Vertex-only `SamplingCount > 1`.

### CUJ 3: Heterogeneous `EvalSet` Gate Over Embedded Templates

* **Given**: An `EvalSet` combines three templates with different placeholder subsets: `visual-continuity` (`{{video}}`), `brief-adherence` (`{{video}}`, `{{brief}}`), and `metadata-json` (`kind: heuristic`, `{{metadata}}` mapped from set input `clip_meta` via `bind: {clip_meta: metadata}`).
* **When**: The caller invokes `ev.RunSet(ctx, evalSet, catalog, mizan.SetRunOptions{Inputs: ...})`.
* **Then**: Mizan projects only the bound/referenced fields into each member's `Instance` (preventing `unknown instance field` failures while still rejecting any set-level input unreferenced by all members), executes both LLM and heuristic members in-memory, and returns `EvalSetResult` with per-member results, aggregate score, pass/fail `Verdict`, and summed `TokenUsage`.

### CUJ 4: Concurrent CLI & MCP Invocations Without `SQLITE_BUSY`

* **Given**: An existing `~/.config/mizan/registry.db` at current schema version and 8 parallel `mizan eval run` subprocesses (or concurrent `mizan mcp http` tool calls).
* **When**: All 8 processes open `registry.db` and `results.db` simultaneously.
* **Then**: Read-only opens of an already-migrated `registry.db` short-circuit schema migration without starting a write transaction, concurrent result writes serialize under `WAL` mode and a 5-second busy timeout with zero `SQLITE_BUSY` failures, and the parent config directory (`0700`), `.db` file (`0600`), and present `-wal`/`-shm` sidecars (`0600`) preserve owner-only permissions.

## Technical Plan (Architecture)

### Component Boundaries & Dependency Direction

Mizan retains a single root `go.mod` (`github.com/ghchinoy/mizan`) while enforcing strict package-level dependency isolation so compiling `github.com/ghchinoy/mizan/pkg/mizan` links **none** of `modernc.org/sqlite`, `cloud.google.com/go/storage`, `github.com/joho/godotenv`, `github.com/spf13/cobra`, `github.com/modelcontextprotocol/go-sdk`, `internal/wire`, `internal/config`, or `internal/mcpserver`:

```text
+-----------------------------------------------------------------------+
| Downstream Go Host (e.g., omni-editingbay)                            |
|   owns *genai.Client (BackendEnterprise / gemini-4-argon), embed.FS   |
+-----------------------------------+-----------------------------------+
                                    | imports
                                    v
+-----------------------------------------------------------------------+
| pkg/mizan  (Public Go SDK — Curated Types, Zero SQLite, Zero Stderr)  |
|   Evaluator, Catalog, Template, EvalSet, Asset, Variation, Result     |
+-----------+-----------------------+-----------------------+-----------+
            |                       |                       |
            v                       v                       v
+-------------------+   +-----------------------+   +-------------------+
| internal/registry |   | internal/eval         |   | internal/evalset  |
| Pure Model, YAML  |   | Engine, Media-First   |   | Runner, Bind &    |
| Codec, JSONSchema |   | Genai, Choice/Score,  |   | Subset Projection |
| Validator, Hash   |   | Heuristic/Computation |   +-------------------+
+-------------------+   +-----------+-----------+
                                    |
                                    v
                        +-----------------------+
                        | internal/asset        |
                        | Pure Stager Interface |
                        | & MIME Sniffing       |
                        +-----------------------+

  [CLI / MCP Composition Root ONLY — Never Imported by pkg/mizan]
  cmd/mizan, internal/mcpserver
    └── internal/wire (Vertex EvaluationClient dialer + Config wiring)
          ├── internal/sqliteutil -> registry/sqlite, results/sqlite (WAL + 0600)
          └── internal/asset/gcs (cloud.google.com/go/storage Stager)
```

### Recorded Architectural Decisions

1. **Public Import Path & Curated Type Ownership (`github.com/ghchinoy/mizan/pkg/mizan`)**:
   * Input manifests (`Template`, `EvalSet`) wrap unexported `internal/registry` and `internal/evalset` domain structs behind read accessors (hiding SQLite sync state while avoiding 16 duplicated authoring structs), whereas evaluation outputs (`Result`, `EvalSetResult`, `MemberResult`, `CriterionScore`, `Stats`, `TokenUsage`, `AppliedJudge`, `FinishError`) are plain JSON-serializable value structs owned by `pkg/mizan`.
   * Owning output value structs in `pkg/mizan` preserves `pkg.go.dev` documentation and allows `pkg/mizan.Result` to JSON-serialize `Applied` provenance without disturbing `internal/eval.Result`'s CLI stdout JSON contract (`skilldocs` drift gates). An automated parity test verifies that every internal output field and `MetricKind`/`Modality` constant is mapped or explicitly allowlisted as internal-only.
2. **Multimodal `genai.Part` Ordering — Media-Preamble-First + Rendered Text**:
   * On all `genai` structured paths, `contents[0].Parts` emits every unique media item at the front of the message as `[TextPart("[<modality> \"<label>\"]:"), MediaPart_1, ..., TextPart("[<modality> \"<label_N>\"]:"), MediaPart_N, TextPart(renderedPrompt + directiveSuffix)]` (including any sanitized `Variation.Description` in the label).
   * Existing `mizan-templates` manifests frequently place rubric context *above* `{{response}}`; splitting inline at placeholder boundaries would leave leading rubric prose ahead of video tokens unless templates were rewritten. Media-Preamble-First guarantees labeled media preamble parts precede all prompt and rubric instruction text on every template while inline `[attached <modality>: "<label>"]` markers preserve in-sentence reference clarity and deduplicate repeated `{{var}}` mentions. When zero media items are present, `contents[0].Parts` remains a single `TextPart` (`len(Parts) == 1`).
3. **Auto-Routing `pointwise`, `rubric`, and `pairwise` on `*genai.Client`-Only Engines**:
   * When an engine has no Vertex `EvaluationClient` (`pkg/mizan` with `*genai.Client`, or `WithEngine("genai")`), `KindRubric`, `KindPairwise`, and `KindPointwise` dispatch over structured `genai` (`KindPointwise` reusing structured `score` evaluation so `ratingRubric` remains carry-and-reserve).
   * Vertex-only `SamplingCount > 1` and `FlipEnabled` fields append informational entries to `Result.Warnings`. `KindPrebuilt` fails fast before any network call with an error naming the template ID, prebuilt metric, active backend, and remedies.

### Runtime N-Variation & Model Precedence Contracts

* **Single-Placeholder Multi-Asset Variations**: A template declares one standard input placeholder (e.g., `name: variations` in `spec.inputs`, requiring zero changes to `internal/registry/schema/metrictemplate.json` or existing `ContentHash` digests). At runtime, `mizan.Variations(...)` binds an ordered slice of `{ID, Asset, Description}` items to that key, expanding into labeled preamble media parts and optionally deriving `KindChoice`'s allowed enum via `WithChoicesFromVariations(extra...)` or explicit `WithChoices(choices)`. Before calling the model, `choice` evaluation verifies that effective choices contain `2..32` (`2..26` on diffusion) unique, non-empty, control-character-free strings, checks alignment when variations are bound without overriding static YAML choices, and verifies the model's returned selection belongs to the effective choice set.
* **5-Tier Model Precedence Chain**: `Per-Run WithModel > Engine WithModelOverride > Template AutoraterModel > Engine WithFallbackModel (config default-model) > BuiltinDefaultModel`. `WithModelOverride("gemini-4-argon")` overrides template-pinned `gemini-3.5-flash-lite` across all runs on an Enterprise `Evaluator` without requiring `WithModel` on every call.

## Alternatives Considered

### Alternative A: Promote `internal/eval`, `internal/registry`, and `internal/evalset` Directly to `pkg/`

* **Pros**: Minimal wrapper code; zero conversion layer between CLI and library callers.
* **Cons**: Freezes `cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb` (`EvaluationClient`) into Mizan's SemVer API, exposes SQLite sync state (`Dirty`, `ImportedAt`), keeps `Result.Applied` hidden behind `json:"-"`, and leaks CLI `os.Stderr` global-routing notices to library consumers.
* **Reason for Rejection**: Rejected by author: leaks unstable `v1beta1` protobufs, storage sync fields, and CLI stderr side effects into the public library contract.

### Alternative B: Out-of-Process Integration Only (MCP Server or CLI Subprocess)

* **Pros**: Zero public Go API surface to maintain; process isolation between `omni-editingbay` and Mizan.
* **Cons**: Cannot share an in-memory `genai.BackendEnterprise` `*genai.Client` or custom HTTP transport, cannot pass `*genai.Part` (`VideoMetadata` clip offsets) or runtime `N`-way `Choices` without writing temporary YAML files on disk, and incurs subprocess/IPC lifecycle management.
* **Reason for Rejection**: Rejected by author: fails `omni-editingbay`'s requirement for in-process `BackendEnterprise` client sharing, in-memory `N`-way variation specialization, and `*genai.Part` fidelity.

### Alternative C: Root-Level Go Package (`github.com/ghchinoy/mizan`, `package mizan`)

* **Pros**: Shorter import path (`import "github.com/ghchinoy/mizan"`).
* **Cons**: Mixes SDK `.go` source files into the repository root alongside CLI, plugin, docs-site, and Python workspace configuration files.
* **Reason for Rejection**: Rejected by author: selected `github.com/ghchinoy/mizan/pkg/mizan` to keep the repository root free of Go source files.

### Alternative D: Placeholder-Boundary Inline Media Interleaving (`[TextBefore, Media, TextAfter]`)

* **Pros**: Keeps surrounding sentence fragments adjacent to mid-prompt media placeholders.
* **Cons**: Existing templates (such as `video-brand-alignment.yaml`) write rubric prose *above* `{{response}}`, leaving rubric prose ahead of video tokens unless templates are rewritten to start with `{{video}}`.
* **Reason for Rejection**: Rejected by author: selected Media-Preamble-First (`[LabelPart_1, MediaPart_1, ..., LabelPart_N, MediaPart_N, TextPart(renderedPrompt + directiveSuffix)]`) so labeled media preamble parts precede all prompt and rubric instruction text regardless of placeholder position in the template.

### Alternative E: Fail Fast on `KindPointwise` When No Vertex `EvaluationClient` Is Configured

* **Pros**: Avoids wrapping free-form `pointwise` prompts in `{score, explanation}` JSON schemas when `SamplingCount` cannot be emulated.
* **Cons**: Breaks existing `kind: pointwise` templates from `mizan-templates` when loaded by `BackendEnterprise` SDK callers.
* **Reason for Rejection**: Rejected by author: selected auto-routing `pointwise` (reusing structured `score` evaluation), `rubric`, and `pairwise` over structured `genai` so existing packs run unmodified on `*genai.Client`-only engines.

### Alternative F: Nested `pkg/mizan/go.mod` Submodule

* **Pros**: Could trim unused root `go.mod` `require` lines from `go list -m all` if `pkg/mizan` owned all core code.
* **Cons**: Go forbids a nested module from importing parent `internal/*` without requiring the root module (re-importing the root MVS graph and creating circular/lockstep tag dependencies if `cmd/mizan` also imports `pkg/mizan`).
* **Reason for Rejection**: Rejected by author: package-level extraction inside the single `go.mod` already eliminates SQLite, GCS, Cobra, MCP, and `godotenv` from compiled downstream binaries (`go list -deps ./pkg/mizan`) without multi-module tag churn.

### Alternative G: Lift the `20 MiB` Inline Byte Limit for Video Buffers

* **Pros**: Allows passing large local MP4 files or `[]byte` slices inline without URI references.
* **Cons**: `google.golang.org/genai` (`InternalDeepMarshal`) base64-encodes inline bytes into JSON and unmarshals/re-marshals them into a `map[string]any`, amplifying heap ~5x (~2 GiB RSS for eight 50 MiB clips) and triggering HTTP 413 gateway rejections.
* **Reason for Rejection**: Rejected by author: keep the `20 MiB` cap across aggregate inline bytes per call and route `> 20 MiB` media via `URI` or `FromGenaiPart` (`FileData`).

## Risks & Mitigations

* **`genai.NewClient` Mutates `BackendEnterprise` In-Place**: In `google.golang.org/genai v1.77.0`, `NewInternalAPIClient` rewrites `ClientConfig.Backend = BackendVertexAI` whenever initialized with `BackendEnterprise`. *Mitigation*: `NewEvaluator` maps `ClientConfig().Backend` to `"vertex"` or `"gemini-api"` by default and exposes `WithBackendLabel(string)` so Enterprise callers explicitly stamp `Applied.Backend = "enterprise"`.
* **Whole-Second `VideoMetadata` Serialization in `google.golang.org/genai`**: `genai.VideoMetadata` formats `StartOffset` and `EndOffset` with whole-second `%.0fs` precision during JSON marshaling, which can collapse sub-second clip windows (`10.2s..10.4s` -> `"10s".."10s"`). *Mitigation*: Validate `0 <= StartOffset < EndOffset` (when `EndOffset > 0`), `FPS` in `(0, 24]`, and non-zero rounded duration when `EndOffset > 0`, returning an explicit pre-flight error on sub-second collapse.
* **DNS-Rebinding Boundary on `https://` Media URIs**: Gemini/Vertex servers fetch `FileData.FileURI` server-side; client-side IP-literal checks cannot prevent DNS rebinding on arbitrary public hostnames. *Mitigation*: Reject all non-global/private IPv4, IPv6, and IPv4-mapped IPv6 literals via `netip.ParseAddr` + `.Unmap()`, and recommend `WithAllowedURIHosts` / `WithAllowedGCSBuckets` for multi-tenant server deployments.
* **Multimodal Token Multiplication Across `N` Video Variations**: Evaluating `N` full-resolution videos in one prompt scales context tokens linearly with `N`. *Mitigation*: Enforce `MaxVariations = 32` and expose `WithMediaResolution("low"|"medium"|"high")`, per-part `MediaResolution` preservation on `FromGenaiPart`, `WithThinkingBudget`, and `WithMaxOutputTokens`.
* **Single-Pass `genai` Auto-Routing vs. Vertex `SamplingCount` / `FlipEnabled`**: Structured `genai` auto-routing executes a single deterministic schema pass rather than multi-sample averaging or server-side position flipping. *Mitigation*: Preserve existing non-fatal `Result.Warnings` entries whenever `SamplingCount > 1` or `FlipEnabled` is declared on a template executed via `genai`.

--------------------------------------------------------------------------------

## Non-Functional Requirements

* **Security & Input Containment**:
  * **SSRF & URI Policy**: `URI` and `FromGenaiPart` (`FileData.FileURI`) allow only `gs://` and `https://` schemes, rejecting `http://`, `file://`, userinfo credentials, `localhost`, and any literal IP parsed by `netip.ParseAddr` (after `.Unmap()`) that is loopback, private, link-local, multicast, unspecified, or CGNAT (`100.64.0.0/10`) unless the host appears in `WithAllowedURIHosts`. `Evaluator` exposes `WithAllowedURIHosts([]string)` and `WithAllowedGCSBuckets([]string)` for confused-deputy confinement.
  * **Local File Access Disabled by Default in SDK**: `pkg/mizan.NewEvaluator` rejects `File(path)` local filesystem reads unless the host explicitly passes `WithLocalFileAccess(true)` (subject to symlink resolution, regular-file verification, and the `20 MiB` aggregate inline cap).
  * **Prompt-Injection & Schema Bounds**: Runtime `Choices`, `Variation.ID`, and `Variation.Description` reject ASCII control characters (including CR/LF and NUL) and cap cardinality (`MaxChoices = 32`, `MaxVariations = 32`, `<= 26` on diffusion, `Description <= 256` bytes). `ParseTemplate`, `ParseEvalSet`, and `LoadCatalogFS` enforce `1 MiB` per-file and `64 MiB` aggregate read caps and strict JSON Schema validation (`additionalProperties: false` parity so snake_case typos like `system_instruction:` fail immediately at load time).
  * **SQLite `0600` Secret Hygiene**: File-backed SQLite stores create their parent directory with mode `0700`, `chmod` the `.db` file to `0600` before enabling `WAL`, check current journal mode before issuing `WAL` pragmas, and `chmod` `<path>-wal` and `<path>-shm` to `0600` whenever present.
* **Memory, Quota, & Retry Economics**:
  * Aggregate inline `InlineData.Data` bytes across all parts in a single `GenerateContent` call are capped at `20 MiB`.
  * When `client.ClientConfig().HTTPOptions.RetryOptions` has active inner SDK retries (`RetryOptions != nil` and `Attempts == nil || *Attempts > 1`), `pkg/mizan.NewEvaluator` defaults Mizan's outer retry attempts to `1` (overridable via `WithMaxAttempts`) to prevent `5 x 5 = 25x` nested retry storms on HTTP 429/503.
  * `RunOption`s expose `WithMediaResolution`, `WithThinkingBudget`, and `WithMaxOutputTokens` to bound multimodal video token consumption across `N` variations.
* **Observability & Zero Process Pollution**:
  * `pkg/mizan` defaults all internal notice writers to `io.Discard`, never loads `.env` files or writes to `os.Stderr`, and surfaces operational notices in `Result.Warnings`.
  * Blocked, empty, or truncated model responses return a typed `*FinishError` carrying `FinishReason`, `BlockReason`, `FinishMessage`, `ResponseID`, `ModelVersion`, `TemplateID`, and `Kind`. `Result.Stats` records `Attempts`, `RetryBackoff`, `ResponseID`, `ModelVersion`, `FinishReason`, and `CachedTokens`.
* **Backward Compatibility**:
  * Existing `MetricTemplate` and `EvalSet` YAML files (`mizan.dev/v1alpha1`) and SHA-256 `ContentHash` digests remain unchanged; existing `registry.db` (schema v5) and `results.db` (schema v1) require zero schema migrations.
  * Text-only `genai` prompts continue to emit a single `TextPart` (`len(Parts) == 1`), and CLI `eval run -o json`, `eval run --set -o json`, and `results list -o json` JSON key sets remain unchanged (`skilldocs` drift gates remain green).
  * Any future desktop GUI milestone (`cmd/mizan-desktop`) must live in an isolated `desktop/go.mod` module so cgo/GUI dependencies never enter root `go.mod`.

--------------------------------------------------------------------------------

## Detailed Implementation Plan

Work is partitioned into five sequential phases; every phase compiles cleanly and passes `go test -race ./...` before the next phase begins.

### Target Files to Create/Modify

#### Phase 1: Internal Transport Isolation & SQLite Write-Lock-Free Read Opens

##### 1. `internal/eval/native.go` ([Modify]), `internal/eval/native_test.go` ([Modify]), `internal/wire/wire.go` ([Modify]), & `internal/wire/wire_test.go` ([Modify])

* **Type / Scope**: Move Vertex `EvaluationClient` dialer into `internal/wire`.
* **Proposed Changes**:
  * Move `NewClient(ctx, location, apiEndpoint)` and `endpointFor(location, apiEndpoint)` (plus `TestEndpointFor` from `internal/eval/native_test.go`) out of `internal/eval/native.go` into `internal/wire/wire.go` and `internal/wire/wire_test.go` (its sole production caller).
  * Remove `"github.com/ghchinoy/mizan/internal/config"`, `"cloud.google.com/go/aiplatform/apiv1beta1"`, and `"google.golang.org/api/option"` from `internal/eval/native.go`, eliminating `github.com/joho/godotenv` and the gRPC dialer from `internal/eval`'s import graph without adding new packages.

##### 2. `internal/asset/gcs/gcs.go` ([Create]), `internal/asset/gcs/gcs_test.go` ([Create]), `internal/asset/gcs/gcs_integration_test.go` ([Create]), `internal/asset/gcs.go` ([Delete]), `internal/asset/gcs_integration_test.go` ([Delete]), `internal/asset/stager.go` ([Modify]), `internal/asset/mime.go` ([Modify]), `internal/asset/stager_test.go` ([Modify]), `internal/eval/multimodal_integration_test.go` ([Modify]), & `internal/wire/wire.go` ([Modify])

* **Type / Scope**: Isolate `cloud.google.com/go/storage` outside `internal/asset`.
* **Proposed Changes**:
  * Export `StageInput.Validate() error`, `ResolveMIME(override, name string, head []byte) string`, and `GCSObjectName(gcsURI string) string` in `internal/asset/stager.go`, plus `SniffLen` and `SafeExt(localPath string) string` in `internal/asset/mime.go`.
  * Move `GCSStager` and `NewGCSStager` (`cloud.google.com/go/storage`) into `internal/asset/gcs/gcs.go`, deleting `internal/asset/gcs.go` and moving `GCSStager` unit and integration tests into `internal/asset/gcs/{gcs_test.go,gcs_integration_test.go}`.
  * Update `internal/wire/wire.go` and `internal/eval/multimodal_integration_test.go` to import `internal/asset/gcs`, leaving `internal/asset` dependent only on Go stdlib and `internal/registry`.

##### 3. `internal/sqliteutil/open.go` ([Create]), `internal/sqliteutil/open_test.go` ([Create]), `internal/registry/sqlite/sqlite.go` ([Modify]), `internal/registry/sqlite/sqlite_test.go` ([Modify]), `internal/results/sqlite/sqlite.go` ([Modify]), & `internal/results/sqlite/sqlite_test.go` ([Modify])

* **Type / Scope**: Shared SQLite opener, WAL + `0600` hygiene, and write-lock-free read `Open()`.
* **Proposed Changes**:
  * Implement `sqliteutil.Open(path string) (*sql.DB, bool, error)` returning `(db, isFileBacked, err)`:
    1. Detect `isFileBacked = path != "" && path != ":memory:" && !strings.HasPrefix(path, "file::memory:") && !strings.Contains(path, "mode=memory")`. If `isFileBacked`, run `os.MkdirAll(filepath.Dir(path), 0o700)`.
    2. Run `sql.Open("sqlite", path)`, `db.SetMaxOpenConns(1)`, and `db.Ping()`.
    3. If `isFileBacked`, run `os.Chmod(path, 0o600)`.
    4. Run `db.Exec("PRAGMA busy_timeout = 5000;")`.
    5. If `isFileBacked`, query `PRAGMA journal_mode;`. Only when the returned mode is not `"wal"`, execute `PRAGMA journal_mode = WAL;` via `QueryRow` (accepting fallback modes such as `"delete"` or `"memory"` on read-only/non-mmap mounts without failing `Open`) and `PRAGMA synchronous = NORMAL;`, then `os.Chmod(path+"-wal", 0o600)` and `os.Chmod(path+"-shm", 0o600)` ignoring `os.ErrNotExist`.
  * In `internal/registry/sqlite/sqlite.go` (`migrate`):
    1. Query `PRAGMA user_version;` on `s.db`; if `uv == 5`, return `nil` immediately before starting any transaction.
    2. When `uv < 5`, acquire a single `*sql.Conn` (`s.db.Conn(ctx)`), execute `BEGIN IMMEDIATE`, **re-read `PRAGMA user_version` inside the `IMMEDIATE` transaction** (committing and returning `nil` if a concurrent process already migrated to `5`), and pass that `*sql.Conn` (never `s.db`, which would self-deadlock under `MaxOpenConns(1)`) to all `migrateV*` helpers before `COMMIT` and best-effort `0o600` sidecar `chmod`.
  * In `internal/results/sqlite/sqlite.go` (`migrate` and `unmarshalIf`):
    1. Query `PRAGMA user_version;`; if `uv == 1`, return `nil` immediately. When `uv == 0`, acquire `*sql.Conn`, run `BEGIN IMMEDIATE`, re-check `PRAGMA user_version`, execute schema v1 DDL + `PRAGMA user_version = 1;` on that `*sql.Conn`, and `COMMIT`.
    2. Add the `maxJSONColumnBytes = 1 << 20` size guard inside 2-argument `unmarshalIf(raw string, dst any) error` (preserving all 7 `scanResult` call sites).

#### Phase 2: `internal/eval` Multimodal Preamble, N-Variation `Choices`, & Enterprise Routing

##### 4. `internal/eval/engine.go` ([Modify]), `internal/eval/applied.go` ([Modify]), `internal/eval/model.go` ([Modify]), `internal/eval/route.go` ([Modify]), `internal/eval/applied_test.go` ([Modify]), `internal/eval/defect_a_test.go` ([Modify]), & `internal/eval/model_test.go` ([Modify])

* **Type / Scope**: Core evaluation types, options, 5-tier model precedence, and `genai`-only dispatch.
* **Proposed Changes**:
  * Extend `AssetRef` in `internal/eval/engine.go` (reusing `*genai.VideoMetadata` and `*genai.PartMediaResolution` directly):

    ```go
    type AssetRef struct {
        Modality       registry.Modality
        Text           string
        FilePath       string
        GCSUri         string                     // retained for CLI/native compatibility; normalized with URI
        URI            string                     // gs:// or SSRF-validated https:// URI
        Bytes          []byte                     // inline bytes (<= 20 MiB aggregate per call)
        MimeType       string
        VideoMeta      *genai.VideoMetadata       // optional clip offsets / FPS for genai video parts
        PartResolution *genai.PartMediaResolution // optional per-part media resolution from genai.Part
        Label          string                     // variation ID when inside Items
        Description    string                     // optional sanitized variation description (<= 256B)
        Items          []AssetRef                 // ordered N-way variation slice on a single placeholder
    }
    ```

  * Keep `eval.GenaiClient` as the existing single-method interface. Add `Engine` options: `WithModelOverride(model string)` (`modelSourceEngineOverride = "engine-override"`), `WithBackend(backend string)` (`"vertex"`, `"gemini-api"`, `"enterprise"`, `"custom"`), `WithGenaiLocation(loc string)`, `WithRetryMaxAttempts(n int)`, `WithAllowedURIHosts(hosts []string)`, `WithAllowedGCSBuckets(buckets []string)`, `WithAllowLocalFiles(allow bool)` (default `true` in `internal/eval.NewEngine` for CLI compatibility; set `false` by default in `pkg/mizan.NewEvaluator`).
  * Add `RunOption`s: `WithChoices(choices []string)`, `WithChoicesFromVariations(extra ...string)`, `WithMediaResolution(res string)` (mapping `"low"` -> `genai.MediaResolutionLow`, `"medium"` -> `genai.MediaResolutionMedium`, `"high"` -> `genai.MediaResolutionHigh`, erroring on unknown values), `WithMaxOutputTokens(n int32)`.
  * Extend `Stats` with `json:"-"` SDK telemetry fields (`Attempts int`, `RetryBackoff time.Duration`, `ResponseID string`, `ModelVersion string`, `FinishReason string`, `CachedTokens int32`) and `AppliedAutorater` (`internal/eval/applied.go`) with `Backend string`, `ModelVersion string`, and `EffectiveChoices []string`.
  * Update `resolveModel` and `modelSource` (`internal/eval/model.go`) to `cmp.Or(rcOverride, e.modelOverride, tmpl.AutoraterModel, e.defaultModel, BuiltinDefaultModel)`. Update `ValidateModel` and `bareModelID` to accept `"models/<bare>"` (updating `model_test.go`). Append `"gemini-4"` to `globalOnlyModelPrefixes` in `internal/eval/route.go` (used by Vertex `EvaluateInstances` routing).
  * Update `checkEngineForKind`, `dispatch`, and `Engine.Resolve`: when `rc.engineOverride == "genai"` or (`rc.engineOverride == ""` && `e.client == nil` && `e.genai != nil`):
    * Reject `KindPrebuilt` with: `eval: template %q (kind "prebuilt", metric %q) requires the Vertex AI Evaluation Service (EvaluateInstances) and cannot run on a genai-only (%s) engine; use a "boul", "choice", or "score" template, or configure a Vertex EvaluationClient`.
    * Route `KindRubric` -> `runRubricStructured`, `KindPairwise` -> `runPairwiseGenai`, and `KindPointwise` -> `e.runScore` (preserving `RatingRubric`'s carry-and-reserve invariant in `internal/registry/model.go`). Stamp `Path = "genai"`, `EffectiveHost = "genai"`, `Location = e.genaiLocation`, and `Backend = e.backend` in `Resolve` and `AppliedAutorater` (updating `applied_test.go` and `defect_a_test.go`).

##### 5. `internal/eval/content.go` ([Modify]), `internal/eval/custom.go` ([Modify]), `internal/eval/boul.go` ([Modify]), `internal/eval/choice.go` ([Modify]), `internal/eval/score.go` ([Modify]), `internal/eval/pairwise.go` ([Modify]), `internal/eval/rubric_structured.go` ([Modify]), `internal/eval/speed_options.go` ([Modify]), `internal/eval/diffusion.go` ([Modify]), `internal/registry/validate.go` ([Modify]), `internal/eval/custom_test.go` ([Modify]), & `internal/eval/multimodal_test.go` ([Modify])

* **Type / Scope**: Media-Preamble-First part assembly, SSRF/URI guards, dynamic `Choices`, `FinishError`, and test updates.
* **Proposed Changes**:
  * Unify `varPattern` (`internal/eval/content.go`) and `placeholderPattern` (`internal/registry/validate.go`) onto `\{\{\s*([a-zA-Z0-9_]+)\s*\}\}` via `registry.ExtractPlaceholders(s string) []string`. Note explicitly that `SystemInstruction` is literal system text and never undergoes `{{var}}` placeholder substitution (matching Vertex `EvaluateInstances`).
  * Update `isTextRef` and `isMediaRef` (`internal/eval/content.go`) so any `AssetRef` with `FilePath != ""`, `GCSUri != ""`, `URI != ""`, `len(Bytes) > 0`, or `len(Items) > 0` returns `isMediaRef == true` and `isTextRef == false`.
  * Implement `validateMediaURI(rawURI string, allowedHosts, allowedBuckets []string) error` on `cmp.Or(ref.URI, ref.GCSUri)`: allow `gs://<bucket>/<object>` (checking `allowedBuckets` when non-empty) and `https://<host>/...` (rejecting userinfo, empty host, `localhost`, and any `netip.ParseAddr` literal—after `.Unmap()`—that is loopback, private, link-local, multicast, unspecified, or in `100.64.0.0/10` unless `host` is in `allowedHosts`; checking `allowedHosts` when non-empty). Validate `VideoMetadata` (`0 <= StartOffset`, `EndOffset == 0 || (StartOffset < EndOffset && math.Round(EndOffset.Seconds()) > math.Round(StartOffset.Seconds()))`, `FPS == nil || (*FPS > 0 && *FPS <= 24)`).
  * Update `toNativeFileDataPart` (`internal/eval/content.go`) and `internal/eval/diffusion.go` to fail fast with an actionable error when an `AssetRef` carries `Bytes`, non-`gs://` `URI`, `VideoMeta`, or `Items`.
  * Export `WrapGenaiClient(c *genai.Client) (GenaiClient, error)` in `internal/eval/custom.go`. Refactor `runGenaiStructured` to assemble **Media-Preamble-First**:
    1. Validate placeholders against `inst.Fields`; enforce `len(ref.Items) <= MaxVariations` (`32`), non-empty unique control-char-free `Label`s, and control-char-free `Description` (`<= 256` bytes).
    2. Walk `MetricPromptTemplate` placeholders in first-seen order: on the first occurrence of a media placeholder, materialize `[TextPart("[<modality> \"<label>\"]" + optionalDesc + ":"), MediaPart]` into `preambleParts` (expanding `Items` in slice order) while tracking `totalInlineBytes`; fail if `totalInlineBytes > maxInlineBytes` (`20 MiB`) with a remediation error naming `URI` / `FromGenaiPart`. Substitute inline `[attached <modality>: "<name>"]` (or `[attached <modality> variations: "<id_1>", ...]`) in `renderedPrompt`, deriving `<modality>` via `cmp.Or(string(ref.Modality), string(asset.ModalityForMIME(mime)))`.
    3. Combine `finalText` (`renderedPrompt` + optional `"\n\n" + directiveSuffix`). Emit `[]*genai.Part{genai.NewPartFromText(finalText)}` when `len(preambleParts) == 0`, else `append(preambleParts, genai.NewPartFromText(finalText))` (updating `custom_test.go` and `multimodal_test.go` part-index assertions).
    4. Apply `rc.mediaResolution` and `rc.maxOutputTokens` onto `genai.GenerateContentConfig`.
  * Add `*FinishError` in `internal/eval/custom.go` triggered before JSON parsing when `resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != ""`, `len(resp.Candidates) == 0`, or `cand.FinishReason != "" && cand.FinishReason != genai.FinishReasonUnspecified && cand.FinishReason != genai.FinishReasonStop` (so existing unit-test mocks with empty `FinishReason` continue passing). Add HTTP `502`/`504` to `isTransientGenaiError`, and parameterize error prefixes with `tmpl.Kind` and `tmpl.ID`.
  * Add empty `BaselineFieldName == "" || CandidateFieldName == ""` guard to `runPairwiseGenai` (`internal/eval/speed_options.go` / `pairwise.go`).
  * Update `runChoice` (`internal/eval/choice.go`) and `buildDiffusionSchema` (`internal/eval/diffusion.go`) to resolve effective choices (`WithChoices` > `WithChoicesFromVariations` > `tmpl.Choices`), enforce `2..32` (`2..26` on diffusion) unique control-char-free options, check variation-label alignment when `Items` are bound without overriding YAML choices, and verify `res.CustomOutput["selection"]` belongs to effective choices.
  * Update `runRubricStructured` (`internal/eval/rubric_structured.go`) to populate `res.Explanation` from `res.CustomOutput["explanation"].(string)`.

##### 6. `internal/results/store.go` ([Modify]) & `internal/results/store_test.go` ([Modify])

* **Type / Scope**: Safe input hashing for `Bytes`, `URI`, and `Items` in CLI result records.
* **Proposed Changes**: Update `buildInputs` so `Bytes` hashes via SHA-256 into `ContentHash` using `ModeReference` with empty `Inline` (never storing raw binary bytes in SQLite JSON), `URI` populates `StoredInput.URI`, and `Items` hashes item digests deterministically. Leave `results.TokenUsage` unchanged so `drift_results_test.go` and `report-to-html/SKILL.md` remain untouched.

#### Phase 3: `internal/evalset` Input Projection & `Bind` Resolution

##### 7. `internal/evalset/binding.go` ([Modify]), `internal/evalset/runner.go` ([Modify]), `internal/evalset/domain_test.go` ([Modify]), & `internal/evalset/runner_test.go` ([Modify])

* **Type / Scope**: Per-member input projection and `Set.Inputs` / `Member.Bind` (`setInput -> memberPlaceholder`) resolution.
* **Proposed Changes**:
  * Implement `ExpectedFields(tmpl registry.MetricTemplate) []string`: prompt placeholders (+ `BaselineFieldName`/`CandidateFieldName` on `pairwise`) for prompt-based kinds, `nativeTextFields` roles for `computation`/`prebuilt`, and `cmp.Or(tmpl.Heuristic.Target, "response")` for `heuristic`.
  * Replace full-map copy in `instanceFor` with `projectMemberInstance(set Set, member Member, tmpl registry.MetricTemplate, shared map[string]eval.AssetRef) (eval.Instance, map[string]bool, error)`:
    1. Build the effective `setKey -> memberPlaceholder` map starting from `set.Inputs` and overriding per `setKey` with `member.Bind` (preserving `domain.go:67-70`'s `setInputName -> memberPlaceholderName` contract). Validate that every explicit target in `member.Bind` exists in `ExpectedFields(tmpl)` and that two set keys do not collide onto the same `memberPlaceholder`.
    2. For any `expectedKey` of `tmpl` not yet bound by `set.Inputs`/`member.Bind`, fall back to identity lookup `shared[expectedKey]`. Mark each matched `setKey` consumed.
    3. Across the pre-flight pass over all resolved members, verify every key in `opt.Inputs` is consumed by **at least one** member (failing fast on unreferenced suite-level input typos).
  * Add `RunOpts []eval.RunOption` to `evalset.RunOptions` (retaining sequential member execution so `FailFast` `Skipped` ordering stays deterministic, and leaving `evalset.EvalSetResult` JSON fields untouched so `drift_evalset_test.go` stays green). Update `runner_test.go`'s `tmpl(id)` helper to declare `{{asset}}` where `Inputs: {"asset": ...}` is passed.

#### Phase 4: Public Go SDK (`pkg/mizan`) & Contract Guard Tests

##### 8. `pkg/mizan/doc.go` ([Create]), `pkg/mizan/types.go` ([Create]), `pkg/mizan/asset.go` ([Create]), `pkg/mizan/catalog.go` ([Create]), & `pkg/mizan/evaluator.go` ([Create])

* **Type / Scope**: Public Go SDK package `github.com/ghchinoy/mizan/pkg/mizan`.
* **Proposed Changes**:
  * `types.go`:
    * Opaque manifest wrappers `Template` (wrapping `registry.MetricTemplate` with read accessors `ID()`, `Name()`, `Version()`, `ContentHash()`, `Kind()`, `Inputs()`, `Choices()`, `AutoraterModel()`) and `EvalSet` (wrapping `evalset.Set` with read accessors `ID()`, `Name()`, `Version()`, `Members()`, `Aggregation()`), plus `TemplateSpec` and `NewTemplate(TemplateSpec) (Template, error)` for programmatic construction.
    * `Kind` and `Modality` string constants, `Instance` (`type Instance map[string]Asset`), `SetRunOptions` (`Inputs map[string]Asset`, `FailFast bool`, `RunOptions []RunOption`), and JSON-serializable output value structs: `Result`, `CriterionScore`, `Stats`, `TokenUsage`, `AppliedJudge`, `FinishError`, `EvalSetResult` (summing `TokenUsage` across `Members`), `MemberResult`, `SetVerdict`, and `DecodeCustom[T any](Result) (T, error)`.
  * `asset.go`: `Asset`, `Variation` (`ID string`, `Asset Asset`, `Description string`), constructors `Text(string) Asset`, `Bytes([]byte, string) Asset`, `URI(string, string) Asset`, `File(string) Asset`, `FromGenaiPart(*genai.Part) (Asset, error)` (preserving `VideoMetadata` and `PartMediaResolution`), `Variations(...Variation) Asset`, and method `(Asset) WithVideoMetadata(*genai.VideoMetadata) Asset`.
  * `catalog.go`: `TemplateSource` interface (`Template(id string) (Template, bool)`), `ParseTemplate([]byte) (Template, error)`, `ParseEvalSet([]byte) (EvalSet, error)`, `NewCatalog() *Catalog`, `(*Catalog).AddTemplate(Template) error`, `(*Catalog).AddEvalSet(EvalSet) error`, `(*Catalog).Template(id string) (Template, bool)`, `(*Catalog).Templates() []Template`, `(*Catalog).EvalSet(id string) (EvalSet, bool)`, `(*Catalog).EvalSets() []EvalSet`, and `LoadCatalogFS(fs.FS) (*Catalog, error)` (`1 MiB` per-file / `64 MiB` total `io.LimitReader`, strict `ValidateTemplateSchema` + semantic validation + `NormalizeKind` + `ContentHash`).
  * `evaluator.go`:
    * `Generator` interface (`GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)`).
    * `NewEvaluator(client *genai.Client, opts ...EvaluatorOption) (*Evaluator, error)` (validates `client != nil && client.Models != nil`, defaults notice writer to `io.Discard` and local file access to `false`, derives default backend `"vertex"`/`"gemini-api"` and location from `client.ClientConfig()`, and sets outer `WithRetryMaxAttempts(1)` when `ro := cc.HTTPOptions.RetryOptions; ro != nil && (ro.Attempts == nil || *ro.Attempts > 1)` unless overridden).
    * `NewEvaluatorFromGenerator(gen Generator, opts ...EvaluatorOption) (*Evaluator, error)` (default backend `"custom"`) and `NewOfflineEvaluator(opts ...EvaluatorOption) *Evaluator`.
    * `EvaluatorOption`s: `WithBackendLabel`, `WithModelOverride`, `WithFallbackModel`, `WithMaxAttempts`, `WithAllowedURIHosts`, `WithAllowedGCSBuckets`, `WithLocalFileAccess`, `WithNoticeWriter`.
    * `(*Evaluator).Run(ctx, tmpl, inst, ...RunOption) (Result, error)` and `(*Evaluator).RunSet(ctx, set, src TemplateSource, opts SetRunOptions) (EvalSetResult, error)`.
    * `RunOption`s: `WithModel`, `WithChoices`, `WithChoicesFromVariations`, `WithRubricScale`, `WithThinkingBudget`, `WithMediaResolution`, `WithMaxOutputTokens`.

##### 9. `pkg/mizan/seam_test.go` ([Create]), `pkg/mizan/parity_test.go` ([Create]), & `pkg/mizan/evaluator_test.go` ([Create])

* **Type / Scope**: Architectural guardrails and SDK unit tests.
* **Proposed Changes**:
  * `seam_test.go` (`TestPkgMizanImportSeam`): walks the recursive transitive `go/build` import closure of `pkg/mizan` and fails if any package matches `modernc.org/sqlite`, `internal/registry/sqlite`, `internal/results/sqlite`, `cloud.google.com/go/storage`, `internal/asset/gcs`, `internal/wire`, `internal/config`, `github.com/joho/godotenv`, `internal/mcpserver`, `github.com/spf13/cobra`, or `github.com/modelcontextprotocol/go-sdk`; asserts `pkg/mizan`'s direct imports exclude `os/exec`; and inspects `go/ast` exported declarations of `pkg/mizan` to forbid any `aiplatformpb` reference.
  * `parity_test.go` (`TestPublicTypeParity`): reflects over `eval.Result`, `eval.Stats`, `eval.TokenUsage`, `eval.AppliedAutorater`, `evalset.EvalSetResult`, and `evalset.MemberResult` (recursing into nested structs) and parses `internal/registry/model.go` via `go/ast` for `MetricKind` and `Modality` `const` declarations to guarantee 1:1 mapping or explicit allowlisting.
  * `evaluator_test.go`: table-driven tests covering `WithBackendLabel("enterprise")` + `WithModelOverride("gemini-4-argon")`, `"models/gemini-4-argon"`, `Variations` (including `Description`) + `WithChoicesFromVariations`, Media-Preamble-First ordering, `FromGenaiPart` (`VideoMetadata` + `PartMediaResolution` + sub-second rounding error), `20 MiB` aggregate inline rejection, `netip.ParseAddr` SSRF rejection (including `::ffff:127.0.0.1`), strict YAML snake_case rejection (`system_instruction:`), `LoadCatalogFS` over `fstest.MapFS`, heterogeneous `EvalSet` execution (`bind: {clip_meta: metadata}`), `pointwise`/`rubric`/`pairwise` auto-routing, `prebuilt` fast-fail, `FinishError` (`SAFETY`), and zero `os.Stderr` output.

#### Phase 5: CLI Seam Guard, Documentation, & CI Drift Gates

##### 10. `cmd/mizan/seam_test.go` ([Modify]), `docs/rfcs/0002-public-go-sdk.md` ([Create]), `docs/user-guide.md` ([Modify]), `docs/llm-as-judge-scenarios.md` ([Modify]), & `.github/workflows/ci.yml` ([Modify])

* **Type / Scope**: `cmd/*` seam hardening, checked-in RFC, user/scenario documentation, and CI doc-drift guard.
* **Proposed Changes**:
  * Add `"aiplatformpb"` and `"internal/asset/gcs"` to `forbiddenCmdImports` in `cmd/mizan/seam_test.go`.
  * Check this finalized RFC into `docs/rfcs/0002-public-go-sdk.md`.
  * Document `pkg/mizan` (`NewEvaluator`, `WithBackendLabel("enterprise")`, `WithModelOverride`, `Variations`, `LoadCatalogFS`) in `docs/user-guide.md`.
  * Document the Media-Preamble-First multimodal part-ordering contract and `genai`-only auto-routing for `pointwise`/`rubric`/`pairwise` in `docs/llm-as-judge-scenarios.md`.
  * Update `.github/workflows/ci.yml` (`doc-drift` POSIX `case` pattern and error message) from `cmd/mizan/*|internal/eval/*` to `cmd/mizan/*|internal/eval/*|pkg/mizan/*`.

### Verification Plan

* **Compilation & Build**: `CGO_ENABLED=0 go build ./... && go build -o bin/mizan ./cmd/mizan`
* **Unit, Race, Seam, & Drift Suite**: `go test -race ./...`
* **Transitive Dependency Gate**: `go list -deps ./pkg/mizan/... | grep -E 'modernc\.org/sqlite|cloud\.google\.com/go/storage|joho/godotenv|spf13/cobra|modelcontextprotocol|internal/(wire|config|mcpserver)'` (must match 0 lines; also enforced inside `TestPkgMizanImportSeam`)
* **Static Analysis, Plugin Parity, & Vulnerability Checks**:
  * `golangci-lint run ./...`
  * `govulncheck ./...`
  * `./scripts/validate-plugins.sh && ./scripts/test-validate-plugins.sh`
* **Required Test Cases (Happy Path & Edge Cases)**:

| Subsystem | Test Case | Expected Behavior |
| :--- | :--- | :--- |
| `pkg/mizan` Seam | `TestPkgMizanImportSeam` & `TestPublicTypeParity` | Zero SQLite/GCS/Cobra/MCP/`godotenv`/`wire`/`config` in transitive closure; zero direct `os/exec` or exported `aiplatformpb`; zero unmapped output fields or enum constants. |
| `pkg/mizan` Enterprise | `NewEvaluator` with `BackendEnterprise` `*genai.Client` + `WithBackendLabel("enterprise")` + `WithModelOverride("gemini-4-argon")` on template pinning `gemini-3.5-flash-lite` | Calls `"gemini-4-argon"` via caller's `*genai.Client`; `Result.Applied.Backend == "enterprise"`, `ModelSource == "engine-override"`; zero `os.Stderr` bytes. |
| Multimodal Ordering | `Run` with 3 video `Variations` (with `Description`) + template placing prose before `{{variations}}` and repeating `{{variations}}` | `contents[0].Parts` emits `[Label_1, Video_1, Label_2, Video_2, Label_3, Video_3, SingleTrailingTextPart]`; videos attached once; text-only run emits `len(Parts) == 1`. |
| Dynamic `Choices` | `Variations` (`take_1..take_4`) + `WithChoicesFromVariations("none")`; out-of-enum model reply; mismatch without `WithChoices` | Schema enum equals `["take_1","take_2","take_3","take_4","none"]`; out-of-enum or mismatched variation IDs return explicit error; template `ContentHash` unchanged. |
| Media & Security | `FromGenaiPart` with `VideoMetadata` + `PartMediaResolution`; `10.2s..10.4s` rounding collapse; `21 MiB` aggregate inline `Bytes`; `http://` or `::ffff:127.0.0.1` `URI`; `File()` without opt-in | Preserves clip metadata and per-part resolution; rejects collapsed offsets, `>20 MiB` inline bytes, IPv4/IPv6/mapped-IPv4 SSRF URIs, and unopted local files before any network call. |
| Native-Kind Routing | `pointwise`, `rubric`, `pairwise`, and `prebuilt` on `*genai.Client`-only `Evaluator` | First three succeed via structured `genai` (`pointwise` reusing `runScore` with `RatingRubric` untouched) with populated `Explanation` and `SamplingCount` warning when `>1`; `prebuilt` fails fast naming backend and remedies. |
| `EvalSet` Projection | `RunSet` over 3 heterogeneous templates (subset placeholders + `bind: {clip_meta: metadata}` + unreferenced suite typo key) | Valid suite passes with projected inputs and summed `TokenUsage` while `drift_evalset_test.go` stays green; suite with unreferenced input key fails pre-flight. |
| SQLite Concurrency | 16 concurrent goroutines/handles opening file-backed `registry.db` & `results.db` (migrated + unmigrated race) + read-only `Open()` | Zero `SQLITE_BUSY` or self-deadlock; zero write transactions or WAL rewrites on migrated read-only `Open()`; parent dir `0700` and `.db` / present sidecars `0600`; `:memory:` still works. |
