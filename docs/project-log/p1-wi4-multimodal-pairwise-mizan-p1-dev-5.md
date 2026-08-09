# WI-P1-4 — Native multimodal pointwise + pairwise + `eval pairwise` CLI + `WithStager`

**Agent:** mizan-p1-dev-5
**Branch:** `feat/p1-multimodal-pairwise` (from `main` @ `60444cd`)
**PR:** to `main`, **not merged** — eng-manager gates merge
**Date:** 2026-08-09

> Note: the brief referenced `design/project-log/…`; the repo has no `design/` dir,
> so this log follows the established `docs/project-log/` convention (matching WI-7).

## Scope

Final P1 item. Builds on WI-5 (native pointwise/rubric/custom_schema engine) and
WI-7 (`internal/asset` MIME + GCS staging), both already merged to `main`.

1. **Native multimodal pointwise** — image/audio/video/music via `gs://` `FileData`
   ONLY (native `EvaluateInstances` silently drops inline bytes — spike-core). Local
   files are staged to GCS first; pre-staged `gs://` URIs pass through.
2. **Native pairwise** (`KindPairwise`) — `PairwiseMetricSpec` +
   `PairwiseChoice` mapping **BASELINE=1 / CANDIDATE=2 / TIE=3** (confirmed live).
   Defaults: `FlipEnabled=true`, `SamplingCount>=4` (template values honored when set).
3. **`eval pairwise` CLI** — `--metric --baseline key=… --candidate key=…
   [--field/--file/--gcs …]`.
4. **`WithStager(s asset.Stager) Option`** — the engine slot WI-5 left open; no
   signature churn. `wire.NewEngine` builds a `*GCSStager` from `cfg.StagingBucket`
   and passes `WithStager` **only** when a bucket is configured.
5. Carried-forward WI-5 security fixes (see below).

## Files

**Implementation** (commit `216050e`)
- `internal/eval/engine.go` — `stager asset.Stager` field; `WithStager` option;
  `KindPairwise` dispatch.
- `internal/eval/content.go` — `toNativeFileDataPart` (gs://-only native FileData
  converter, distinct from the genai inline converter); `buildContentMap`,
  `isTextRef`/`isMediaRef`/`keysHaveMedia`, `buildJSONInstanceKeys`; hardened
  `readInlineAsset` (the genai inline read path) with `maxInlineBytes` cap.
- `internal/eval/native.go` — shared `runNativePointwise` (pointwise + rubric),
  ContentMap-vs-JsonInstance selection via `keysHaveMedia`.
- `internal/eval/pairwise.go` — `runPairwise`, `buildPairwiseInstance`,
  `pairwiseKeys`, `pairwiseChoiceString`; pairwise defaults.
- `cmd/mizan/eval.go` — `eval pairwise` subcommand; `--field/--file/--gcs` on
  `eval run`; `buildInstance` helper (**no `internal/asset` import** — dep direction).
- `internal/wire/wire.go` — GCSStager construction (tolerates `ErrNoBucket`);
  genai `*_BASE_URL` allow-list validation; combined close.
- `internal/config/config.go` — `ValidateGenaiBaseURL`; shared `endpointHostAllowed`.

**Tests** (commit `60e94a8`)
- `internal/eval/multimodal_test.go` — fake `asset.Stager`; native ContentMap/
  FileData materialization (local staged, gs:// passthrough, mixed text+media,
  missing-MIME error); var/instance-key parity; `toGenaiInlinePart` hardening.
- `internal/eval/pairwise_test.go` — PairwiseChoice mapping for all three (+unspecified);
  FlipEnabled/SamplingCount defaults; honored template sampling; missing field-name
  and missing-variable guards; multimodal pairwise ContentMap.
- `internal/eval/multimodal_integration_test.go` — `//go:build integration` live
  multimodal pointwise (local-staged PNG + pre-staged gs://) and live pairwise text.
- `internal/config/config_test.go` — `ValidateGenaiBaseURL` allow-list table test.

## Design decisions

- **Native and genai converters stay distinct.** `toNativeFileDataPart`
  (aiplatformpb, gs:// FileData only) vs `toGenaiInlinePart` (genai, inline bytes
  OK). No merging.
- **ContentMap vs JsonInstance** is chosen by whether any referenced key resolves
  to a non-text asset (`keysHaveMedia`). Text-only instances keep the JsonInstance
  path unchanged, so text-only and custom_schema-inline still work with **no bucket**.
- **Empty `StagingBucket` does not fail construction.** `wire.NewEngine` passes
  `WithStager` only when a bucket is set; a multimodal eval needing staging fails at
  Run time with `asset.ErrNoBucket` (proven by `TestRunPointwiseLocalFileNoStager`).
- **Rubric reuses the native pointwise path** (`runNativePointwise`), so multimodal
  rubric fields materialize a ContentMap too (dedup per rev-4/test-3 R1).
- **`FlipEnabled` tri-state limitation:** the P1 registry stores `FlipEnabled bool`,
  which cannot express an explicit `false`. An unset (zero-value) template therefore
  gets the default-on behavior. Documented as a follow-up (a `*bool` or an explicit
  "flip disabled" field would be needed to let a template opt out).

## Live finding — pairwise template must reference baseline/candidate placeholders

The first live pairwise run returned:

```
rpc error: code = InvalidArgument desc = The instance contains extra keys that are
not present in the metric prompt template. ... The extra keys are: ['candidate', 'baseline'].
```

The Eval Service validates that every instance key appears as a `{{placeholder}}` in
the metric prompt template. A pairwise template must therefore reference the baseline
and candidate field-name placeholders (in addition to setting
`BaselineResponseFieldName`/`CandidateResponseFieldName` on the spec). This is a
template-authoring contract, not an engine bug — the engine correctly forwards the
declared keys. Both the unit and live test templates now model valid usage, and this
should be documented for metric-pack authors.

## Security items (carried-forward WI-5 audit)

| Item | Severity | Disposition |
|---|---|---|
| (a) Unbounded `os.ReadFile` in `toGenaiInlinePart` | MED must-fix | **Fixed.** `readInlineAsset` does `filepath.EvalSymlinks`, `os.Lstat` regular-file guard (rejects dirs/devices/FIFOs/sockets), a 20 MiB size cap, and a bounded `io.LimitReader` read. Tested (`TestToGenaiInlinePartHardening`: oversize + non-regular-file rejection). |
| (b) genai `*_BASE_URL` endpoint override | LOW | **Verified + fixed.** The genai SDK **does** honor `GOOGLE_VERTEX_BASE_URL` / `GOOGLE_GEMINI_BASE_URL` (v1.67.0 `base_url.go getBaseURL`, priority 3). Added allow-list parity: `config.ValidateGenaiBaseURL` applies the same `*.googleapis.com` policy (with the `MIZAN_ALLOW_CUSTOM_ENDPOINT=1` escape hatch) in `wire.NewEngine`, closing the ADC-token-redirection gap. ADC/TLS never weakened. |
| (c) Path canonicalization / symlink | LOW | **Fixed** as part of (a) — `EvalSymlinks` + regular-file guard. Not a sandbox: a CLI user may reference any file they can read; this is defense-in-depth (OOM/hang/special-file DoS), not path confinement. |
| (d) Cheap rev-4 non-blockers + test-3 R1 (rubric/pointwise dedup) | — | **Applied** where low-risk: rubric and pointwise now share `runNativePointwise`. |

## Dependency direction (LOCKED constraint held)

`cmd/*` imports only `registry`, `eval`, `config`, `wire` — **not** `internal/asset`.
MIME detection, modality inference, and staging happen inside `eval`/`wire`. genai and
native clients are built only in `wire.go`. Verified: `cmd/mizan` has no direct
`internal/asset` import.

## Acceptance / verification

| Gate | Result |
|---|---|
| `gofmt -l .` | clean |
| `go build ./...` | ok |
| `CGO_ENABLED=0 go build ./cmd/mizan` | ok (cgo-free preserved) |
| `go vet ./...` | ok |
| `go vet -tags integration ./...` | ok |
| `CGO_ENABLED=0 go test ./...` | PASS (all packages) |
| `govulncheck ./...` | **reachable = 0** (0 vulns called; 1 in a required module not called) |

### Live integration output (PROJECT_ID=ghchinoy-genai-sa, bucket gs://ghchinoy-genai-sa-mizan-staging)

```
=== RUN   TestLiveMultimodalPointwiseImage
    LIVE multimodal image pointwise score=5 explanation=The image is a perfectly
    uniform, vibrant red across its entire area, meeting all criteria for a solid,
    saturated single color.
--- PASS: TestLiveMultimodalPointwiseImage (2.49s)
=== RUN   TestLiveMultimodalPointwiseImageGCS
    staged to gs://ghchinoy-genai-sa-mizan-staging/mizan-staging/de95a16e….png (image/png)
    LIVE gs:// image pointwise score=5 explanation=The image is a solid, vibrant red,
    which is a highly saturated color; there is no desaturation towards white, black,
    or grey.
--- PASS: TestLiveMultimodalPointwiseImageGCS (9.13s)
=== RUN   TestLivePairwiseText
    LIVE pairwise choice=CANDIDATE explanation=The baseline response provides
    specific, relevant steps for resetting a password, while the candidate response
    offers a generic and unhelpful troubleshooting tip…
--- PASS: TestLivePairwiseText (1.69s)
```

(The pairwise explanation names the responses swapped — an artifact of `FlipEnabled`
sampling — but the de-flipped final `PairwiseChoice=CANDIDATE` correctly picks the
substantive answer, confirming BASELINE=1/CANDIDATE=2/TIE=3.)

## Follow-ups

- `FlipEnabled` tri-state (registry `bool` can't express explicit-false).
- Document the pairwise "template must reference baseline/candidate placeholders"
  contract for metric-pack authors.
