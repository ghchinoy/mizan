# Mizan Testing Guide

This guide is for **exercising each built capability with real, copy-pasteable
commands** — it's a hands-on companion, not a replacement for the narrative
docs. For conceptual/narrative detail (what a flag means, how config
resolution works, full CRUD walkthroughs), see
[`docs/user_guide.md`](user_guide.md). For phase-by-phase roadmap detail and
work-item IDs (`WI-P1-*`), see
[`docs/implementation-plan.md`](implementation-plan.md).

Every command and every line of output below was run, in this pass, against a
binary built from current `main` (PRs #1–#8 merged; PR #9 — multimodal +
pairwise — still open, see the [Pairwise and multimodal](#pairwise-and-multimodal-pr-9-in-review) section). Nothing here is copied from another doc or
invented.

## Minimal setup

```sh
go install github.com/ghchinoy/mizan/cmd/mizan@main
mizan config set project-id <your-project-id>
mizan config set location us-central1   # default; the "us" multi-region 404s
gcloud auth application-default login   # ADC — no API-key auth path exists
```

Use a scratch registry DB per test session so you don't collide with a real
registry:

```sh
export MIZAN_REGISTRY_DB=/tmp/mizan-testing.db
```

See [`docs/user_guide.md`](user_guide.md#prerequisites) for the full
prerequisites/install/config explanation (env-var overrides, `.env` file
location, the custom-endpoint safeguard, etc.) — it isn't repeated here.

## Text pointwise (fully testable today)

This is the one path that is completely implemented and wired end-to-end:
CLI → `registry.Service` → `eval.Engine` → live Vertex AI
`EvaluateInstances`.

Create the template:

```sh
$ mizan registry create --id demo/conciseness --name "Conciseness" \
    --description "Scores how concise a response is" \
    --kind pointwise \
    --prompt "Rate how concise this response is from 0 (verbose) to 1 (concise). Response: {{response}}" \
    --model gemini-2.5-flash
ID:             demo/conciseness
Name:           Conciseness
Kind:           pointwise
Modalities:     text
Model:          gemini-2.5-flash
SamplingCount:  4
Description:    Scores how concise a response is
Prompt:         Rate how concise this response is from 0 (verbose) to 1 (concise). Response: {{response}}
```

Run it (live call to Vertex AI, captured on 2026-08-09 against
`gemini-2.5-flash` in `us-central1`):

```sh
$ mizan eval run --metric demo/conciseness --field response="The cat sat on the mat."
Score:        0.95
Explanation:  The sentence is extremely concise, using the minimum number of words necessary to convey a complete and clear thought without any redundancy or filler.
```

Re-running this against a live model is a real, non-deterministic autorater
call — don't expect the score/explanation text to be byte-identical on a
re-run; the shape (a `Score` float and an `Explanation` string) is what's
guaranteed.

### What `Score`/`Explanation` mean

- **`Score`** is a float whose scale is whatever your prompt asked for — Mizan
  doesn't impose one. Here the prompt asked for 0 (verbose) to 1 (concise), so
  `0.95` means "very concise."
- **`Explanation`** is free-text rationale from the autorater model. Treat it
  as a qualitative aid, not a machine-parseable field.

## Rubric (engine built, not yet CLI-testable)

The native rubric path is implemented in `internal/eval/native.go`
(`runRubric`, dispatching to `LLMBasedMetricSpec` with inline
`rubric_groups`) and is covered by integration tests:
`internal/eval/rubric_test.go` and
`internal/eval/rubric_custom_integration_test.go`.

What's missing is CLI authoring: `cmd/mizan/registry.go`'s `templateFlags`
struct has no flag to set `RubricGroups` on a `registry.MetricTemplate` —
confirm yourself with `grep -n "RubricGroups" cmd/mizan/*.go` (no matches).
The field is only ever populated directly in Go structs in
`internal/eval/*_test.go`. So a rubric template created through the CLI
always has an empty `RubricGroups`, and running it fails. Reproduced live:

```sh
$ mizan registry create --id demo/rubric-test --name "Rubric test" --kind rubric \
    --prompt "Evaluate the response: {{response}}"
ID:             demo/rubric-test
Name:           Rubric test
Kind:           rubric
Modalities:     text
Model:          gemini-2.5-flash
SamplingCount:  4
Prompt:         Evaluate the response: {{response}}

$ mizan eval run --metric demo/rubric-test --field response="test"
Error: eval: rubric template "demo/rubric-test" has no rubric groups
```

This is a known, tracked gap in the CLI surface (part of the still-incomplete
`WI-P1-6` CLI work), not a bug report and not an engine limitation — a fix
(adding a way to author rubric groups from the CLI) is planned as follow-up
engineering work. There is no workaround via the CLI today: `cmd/mizan/*.go`
has no stdin/JSON-file input path either (confirm with
`grep -rn "os.Stdin\|json.NewDecoder" cmd/mizan/*.go` — no matches), so do not
expect a `--rubric-group` flag or similar; it doesn't exist.

## Custom schema (engine built, not yet CLI-testable)

Same situation as rubric, for the `custom_schema` kind. The engine path is
implemented in `internal/eval/custom.go` (`runCustomSchema`, calling
`genai.GenerateContent` with a `ResponseSchema` and exponential backoff) and
is covered by `internal/eval/custom_test.go` and
`internal/eval/rubric_custom_integration_test.go`.

`templateFlags` has no flag to set `ResponseSchema` either (same grep as
above returns nothing). Reproduced live:

```sh
$ mizan registry create --id demo/custom-test --name "Custom schema test" --kind custom_schema \
    --prompt "Evaluate the response: {{response}}"
ID:             demo/custom-test
Name:           Custom schema test
Kind:           custom_schema
Modalities:     text
Model:          gemini-2.5-flash
SamplingCount:  4
Prompt:         Evaluate the response: {{response}}

$ mizan eval run --metric demo/custom-test --field response="test"
Error: eval: custom_schema template "demo/custom-test" has no response schema
```

Same status as rubric: tracked CLI-surface gap, no workaround, fix planned as
follow-up engineering work.

## Pairwise and multimodal (PR #9, in review)

Checked at the time of writing:

```sh
$ gh pr view 9 --json state,mergedAt
{"state":"OPEN","mergedAt":null}
```

PR #9 is **still open** (explicitly marked "Do not merge — eng-manager gates
merge" in its description), so neither capability is testable via any built
binary yet — this section is a preview, not a recipe. Once it merges, this
section should be replaced with full verified recipes (build from merged
`main`, run real commands, paste real output) rather than left as a preview.

Per the PR description and [`docs/implementation-plan.md`](implementation-plan.md)
(`WI-P1-4`), it will add:

- **Native multimodal pointwise** — image/audio/video/music evaluation via
  `gs://`-staged `FileData` (native `EvaluateInstances` does not accept
  inline bytes; local files get staged to GCS first, pre-staged `gs://` URIs
  pass through as-is).
- **Native pairwise** (`KindPairwise`) plus a new `eval pairwise` CLI command
  (`--metric --baseline key=… --candidate key=… [--field/--file/--gcs …]`
  per the PR description), returning a `PairwiseChoice` result.

Confirm today's pairwise state yourself in the meantime — running a
`--kind pairwise` template against the current `main` build fails at the
engine, not the CLI-authoring layer (unlike rubric/custom_schema above):

```sh
$ mizan registry create --id demo/pairwise-test --name "Pairwise test" --kind pairwise \
    --prompt "Compare: {{baseline}} vs {{candidate}}" \
    --candidate-field candidate --baseline-field baseline
ID:             demo/pairwise-test
Name:           Pairwise test
Kind:           pairwise
Modalities:     text
Model:          gemini-2.5-flash
SamplingCount:  4
Prompt:         Compare: {{baseline}} vs {{candidate}}

$ mizan eval run --metric demo/pairwise-test --field baseline="a" --field candidate="b"
Error: eval: not implemented in P1 slice: pairwise (WI-P1-4)
```

(Confirm in code: `grep -n "KindPairwise" internal/eval/engine.go` shows the
`not implemented` branch.)

## What each result means

- **`Score` + `Explanation`** (pointwise, and rubric once CLI-authorable) —
  demonstrated above for pointwise. A float score plus free-text rationale.
- **`CustomOutput`** (`custom_schema`) — a typed JSON object matching the
  template's `ResponseSchema`, surfaced as `map[string]any` on
  `eval.Result.CustomOutput` (source: `internal/eval/engine.go`'s `Result`
  struct, and `internal/eval/custom_test.go`'s fixtures, e.g. an
  `overall_score`/`compliant`/`flagged_issues`/`explanation` shape). Not
  demonstrable live yet since custom_schema isn't CLI-runnable — this is read
  from code/tests, not observed via a CLI run.
- **`PairwiseChoice`** (pairwise) — an enum-like string result:
  `BASELINE=1`, `CANDIDATE=2`, `TIE=3` (source:
  [`docs/architecture-final.md`](architecture-final.md) §6, and
  [`docs/implementation-plan.md`](implementation-plan.md)'s `WI-P1-4`
  description). Not demonstrable live yet — pairwise has no engine
  implementation on `main` as of this pass; this mapping is documented from
  the spec/code, not from a run.

## Not yet testable / roadmap

Kept consistent with [`docs/user_guide.md`](user_guide.md#coming-soon--roadmap)
— see that section for the full picture. Verified absent from the built
binary in this pass:

- **Template packs and registry import/export** — `mizan pack` and
  `mizan registry import`/`export` do not exist:

  ```sh
  $ mizan pack
  Error: unknown command "pack" for "mizan"

  $ mizan registry --help
  Available Commands:
    create      Create a metric template
    delete      Delete a metric template
    get         Show a metric template
    list        List metric templates
    update      Update a metric template
  ```

  (No `import`/`export` subcommand is listed.) Roadmap phase P2.
- **Batch evaluation** (`EvaluateDataset` over GCS-hosted datasets) — no such
  command exists yet. Roadmap phase P3.
- **The Wails desktop app** (`cmd/mizan-desktop`) — design-stage scaffolding
  only; no built or runnable desktop app. Roadmap phase P4.

See [`docs/implementation-plan.md`](implementation-plan.md) for the
work-item breakdown and acceptance criteria behind each of these.
