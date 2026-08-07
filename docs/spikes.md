# Mizan — Spike Plan

Status: draft for review, pre-implementation
Date: 2026-08-07

Purpose: de-risk the unknowns in docs/research.md and docs/architecture.md
before committing to full implementation. Each spike is small, time-boxed,
and produces a throwaway or promotable prototype plus a written verdict.

## Spike 0 — Project scaffolding (0.5 day)

**Goal:** stand up `go.mod`, directory skeleton, and decide the
single-module vs `go.work` two-module question from architecture.md section 9.

Tasks:
- [ ] `go mod init github.com/ghchinoy/mizan` (confirm module path with user)
- [ ] Create directory skeleton per architecture.md section 2
- [ ] Prototype both layouts (single module vs go.work) far enough to run
      `go build ./...` successfully in each, compare `go mod graph` size
- [ ] **Verdict needed:** single module or go.work split?

Acceptance: empty `cmd/mizan` and `cmd/mizan-desktop` binaries both build.

## Spike 1 — Native EvaluateInstances: text pointwise (1 day)

**Goal:** prove the v1beta1 client works end-to-end for the simplest case.

Tasks:
- [ ] `go get cloud.google.com/go/aiplatform/apiv1beta1`
- [ ] Auth via ADC (`gcloud auth application-default login`)
- [ ] Write a throwaway `main.go` that:
  - builds a `PointwiseMetricSpec{MetricPromptTemplate: "Rate the following
    response for helpfulness 1-5: {{response}}", ...}`
  - builds `PointwiseMetricInstance{JsonInstance: `{"response": "..."}`}`
  - calls `EvaluateInstances` with an `AutoraterConfig{SamplingCount: 1}`
  - prints `Score` + `Explanation`
- [ ] Confirm placeholder substitution behavior exactly (is it Go
      text/template style `{{response}}` or Python-style `{response}`? --
      the Python SDK reference used `.format(**instance)` client-side,
      Format used by native API needs verification against live behavior)
- [ ] Confirm what location(s) support the Eval API (not all Vertex AI
      regions support GenAI Evaluation Service -- verify against docs/actual
      API errors, note supported region(s) in research.md)

Acceptance: a live scored response with explanation, printed to stdout.
**Verdict needed:** exact template placeholder syntax; supported region(s).

## Spike 2 — Native multimodal via ContentMap (1-1.5 days)

**Goal:** prove image/audio/video evaluation works through the same
EvaluateInstances call using ContentMapInstance, both inline bytes and GCS URI.

Tasks:
- [ ] Extend Spike 1 prototype: build a `PointwiseMetricInstance_ContentMapInstance`
      with one placeholder holding an inline image (`Blob{MimeType, Data}`)
      read from local disk
- [ ] Repeat with a `FileData{FileUri: "gs://..."}` reference (requires a
      test GCS bucket + uploaded sample asset)
- [ ] Repeat with an audio file, then a short video file
- [ ] Note actual behavior/errors if payload exceeds inline size limits --
      capture the real error message and threshold observed
- [ ] Confirm whether `{{placeholder}}` in `MetricPromptTemplate` combined
      with a `ContentMap` for the SAME key works as expected (text template
      references an image placeholder by name)

Acceptance: image, audio, and video assets each successfully scored via
native EvaluateInstances, both via inline bytes and GCS URI.
**Verdict needed:** actual inline size ceiling observed; whether music
(audio subtype) needs any special MIME handling vs generic audio.

## Spike 3 — Pairwise + AutoraterConfig.FlipEnabled (0.5 day)

**Goal:** validate pairwise comparison and bias-mitigation flip behavior.

Tasks:
- [ ] Build a `PairwiseMetricSpec` with `CandidateResponseFieldName` /
      `BaselineResponseFieldName`, run with `FlipEnabled: true` and `false`,
      compare results/explanations on an intentionally biased test case
- [ ] Confirm `PairwiseChoice` enum values returned (BASELINE/CANDIDATE/TIE
      naming) match research.md assumptions

Acceptance: documented, reproducible pairwise result showing flip mitigates
position bias (or documented finding that it doesn't matter for the test
case chosen -- either is a valid spike outcome).

## Spike 4 — Custom-schema fallback via genai (0.5-1 day)

**Goal:** validate the direct-Gemini fallback path for metrics needing a
strict typed response schema beyond {score, explanation}.

Tasks:
- [ ] Define a `genai.Schema` with 3+ named fields (e.g.
      `{overall_score: number, brand_tone_score: number, flagged_issues:
      array<string>}`)
- [ ] Call `client.Models.GenerateContent` with `ResponseSchema` set,
      `ResponseMIMEType: "application/json"`, against a multimodal input
      (e.g. an image + brand guideline text)
- [ ] Add retry w/ exponential backoff on `RESOURCE_EXHAUSTED` (mirror the
      Python reference's `_generate_content_with_retry`)
- [ ] Parse and validate the JSON response against the schema

Acceptance: a strict-schema multimodal custom metric run successfully,
with at least one simulated retry-triggering failure handled gracefully.

## Spike 5 — Metric Registry storage (0.5 day)

**Goal:** validate SQLite-backed registry CRUD with JSON-serialized complex
fields, confirm it's ergonomic for both CLI and (eventually) Wails binding.

Tasks:
- [ ] Implement `internal/registry/sqlite` with a minimal schema (single
      table, `modalities`/`rubric_group`/`response_schema` as JSON TEXT
      columns)
- [ ] Write + run unit tests: create, list, get, update, delete a
      `MetricTemplate`
- [ ] Confirm `os.UserConfigDir()`-based default DB path works correctly on
      macOS (primary dev platform) and note Windows/Linux path expectations
      for later verification

Acceptance: passing unit test suite for registry CRUD against a temp SQLite
file.

## Spike 6 — Wails binding smoke test (0.5 day)

**Goal:** confirm the shared `internal/` packages bind cleanly into a Wails
`App` struct without pulling CLI-only concerns into the GUI layer, and that
Wails' Go-to-TS struct generation handles Mizan's domain types
(`MetricTemplate`, `Result`, etc.) acceptably.

Tasks:
- [ ] Scaffold `cmd/mizan-desktop` from `eldamo-app`'s `wails.json` +
      `main.go` shape
- [ ] Bind `ListMetricTemplates`/`SaveMetricTemplate`/`RunEvaluation` per
      architecture.md section 7
- [ ] Run `wails dev`, confirm generated TypeScript bindings in
      `frontend/wailsjs/` look reasonable for the domain types (especially
      pointer fields like `*float32` in `Result.Score` and `map[string]any`
      in `CustomOutput` -- these are known trouble spots for Wails' TS
      codegen)
- [ ] Note any struct shape adjustments needed for clean TS generation

Acceptance: `wails dev` launches, a stub frontend page can call
`ListMetricTemplates()` and see the (empty) result in the browser console.
**Verdict needed:** any Go struct shape changes required for clean Wails TS
codegen (e.g. avoid `*float32`, prefer `float32` + separate `HasScore bool`
if pointer types prove awkward).

## Spike sequencing & total estimate

Spikes 1-4 can run in parallel (independent prototypes hitting the live
Vertex AI API). Spike 5 is independent and can run in parallel with 1-4.
Spike 0 must complete first (defines the module layout). Spike 6 depends on
Spike 0 and benefits from Spike 5 being done (real registry to bind against).

Rough total: 4-6 engineer-days across all spikes if run mostly in parallel
by 1-2 people; ~2 days of that is genuinely load-bearing for architecture
decisions (Spike 0's module-layout verdict, Spike 1/2's placeholder syntax
and multimodal size-limit findings feed directly into `internal/eval`
implementation).

## Definition of done for the spike phase

All "Verdict needed" items above are answered and appended to
docs/research.md section 7 ("Open questions") as resolved, with the actual
finding recorded (not just "TBD"). Only then should full `internal/eval` and
`internal/registry` implementation begin.
