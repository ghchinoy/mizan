---
name: run-eval-set
description: Run a curated multi-concern Mizan eval-set (an EvalSet manifest) against a shared asset/response, interpret the weighted scorecard — per-member verdicts, the aggregate, the overall PASS/FAIL, and the gate — and honor the gate exit code for CI, driving the `mizan` CLI over `-o json`. Use when a user wants to evaluate an asset against a whole suite of metrics at once, run a set of evals, get a single weighted verdict across several concerns, or wire an eval-set into a CI gate.
license: Apache-2.0
compatibility: Requires the `mizan` CLI on PATH (go install github.com/ghchinoy/mizan/cmd/mizan@latest). Live evals call Vertex AI and need Google Application Default Credentials (ADC) plus a configured project/location; this skill never takes or stores credentials — it relies on the user's existing ADC exactly as the CLI does.
metadata:
  author: ghchinoy
  version: "0.1.0"
---

# Run a Mizan eval-set (`run-eval-set`)

Run a **curated set of evals** — several metric templates, each weighted — against
one shared asset/response, then interpret the **weighted scorecard**: per-member
verdicts, the computed aggregate, the overall PASS/FAIL verdict, and (for CI) the
opt-in **gate exit code**. This skill drives the local `mizan` CLI and reasons over
its machine-readable `-o json` output. It wraps commands that exist in the Mizan
CLI today; it starts no server and re-implements no CLI logic.

Use this instead of `run-eval` when the question is "how does this asset do across
*all* our concerns at once, and does it pass the bar?" rather than a single metric.

## When to use this skill

- "Run our answer-quality suite / eval-set against this response."
- "Evaluate this asset against all our concerns and give me one PASS/FAIL."
- "Score this against several metrics with weights and aggregate the result."
- "Wire this eval-set into CI so a failing verdict fails the build."

## Prerequisites (check first, in order)

1. **`mizan` is installed.** Run the precheck and stop with an install hint if it fails:
   ```bash
   command -v mizan >/dev/null 2>&1 || {
     echo "mizan not found on PATH. Install with: go install github.com/ghchinoy/mizan/cmd/mizan@latest" >&2
     exit 1
   }
   ```
2. **Credentials for a live eval.** Running a set calls Vertex AI (once per scored
   member) and needs ADC and a project. Do **not** collect or store keys — rely on
   the user's existing ADC (`gcloud auth application-default login`) and the project
   already resolved by `mizan` (`flag > env MIZAN_PROJECT_ID/PROJECT_ID > .env >
   default`). If the run fails with a project/credentials error, surface the CLI's
   message verbatim and ask the user to configure ADC/project; never attempt to
   supply credentials yourself.

## Locating or scaffolding the EvalSet manifest

In this phase `--set` is a **filesystem path** to an `EvalSet` manifest (a YAML
file, `kind: EvalSet`), not a registry id — point `--set` at the file on disk.

- **Locate an existing one.** Look for a `kind: EvalSet` YAML the user already has
  (commonly under a pack's `evalsets/` directory, e.g. the shipped
  `docs/examples/evalset-quickstart/evalsets/answer-quality.yaml`). Confirm the
  member metric ids it references are importable/resolvable in the registry
  (`mizan registry get <ns>/<slug> -o json`); a member whose template cannot be
  resolved shows up as a `Missing` row rather than being silently dropped.
- **Scaffold one from described concerns.** If the user describes a set of metrics
  and weights rather than handing you a file, assemble a manifest yourself and
  write it to disk, then pass its path to `--set`. A minimal manifest:

  ```yaml
  apiVersion: mizan.dev/v1alpha1
  kind: EvalSet
  metadata:
    id: quickstart/answer-quality      # namespaced <pack>/<slug>
    name: Answer Quality Suite
    version: 1.0.0
    assetClass: text-answer
  spec:
    inputs:                            # shared inputs, passed by identity to every member
      prompt: prompt
      response: response
    members:
      - metric: quickstart/response-helpfulness
        weight: 2
      - metric: quickstart/response-conciseness
        weight: 1
    aggregation:
      method: weighted-mean            # mean | weighted-mean | min
      threshold: 3.0                   # scores are on the judge's 1-5 scale
      gate: false                      # opt-in CI gate (see "Gate & exit code")
    display:
      order: as-listed
  ```

  The set's shared `inputs` are passed **by identity** to every member (the set
  input name is the member's placeholder name); you provide the values once on the
  command line below.

## Running the set

```bash
mizan eval run --set <path/to/evalset.yaml> --field <key>=<value> -o json
```

Fill the **shared** instance inputs the same way `run-eval` fills a single metric's
inputs — keys must match the manifest's `spec.inputs` names:

- `--field key=value` — a **text** value (repeatable).
- `--file  key=/path/to/asset` — a **local** media asset; the engine stages it to GCS.
- `--gcs   key=gs://bucket/obj` — a **pre-staged** media asset.

Other real flags: `--fail-fast` (abort at the first errored/missing member instead
of the default continue-on-error), `--model <id>` (override the autorater for every
member). `-o json` prints the full `evalset.EvalSetResult`; omit it for the human
scorecard table.

**Never route media through a text slot.** As with `run-eval`, `mizan` hard-errors
if a `gs://` URI or a local media path is passed to a `--field` (text) slot — the
CLI's `guardTextSlot` protection. Use `--file`/`--gcs` for media inputs.

> `--set` and `--metric` are mutually exclusive — exactly one is required. Use
> `--set` here; `--metric` is the single-metric `run-eval` path.

## Reading the scorecard (`evalset.EvalSetResult`)

`-o json` prints exactly one JSON object (the `evalset.EvalSetResult` struct) to
**stdout**; pre-flight echo, warnings, and store notices go to **stderr**. Parse
stdout and reason over the struct. This is the shape the drift test in
`internal/skilldocs/drift_evalset_test.go` keys off — keep it in lockstep with the
CLI. These structs carry **no json tags**, so top-level and nested `evalset` fields
serialize in **PascalCase**; only the embedded `eval.Result` (the `Result` field)
carries the `run-eval` snake_case tags (`rubric_detail`, `warnings`, `duration_ns`,
`token_usage`):

<!-- drift:evalset.EvalSetResult -->
```json
{
  "SetID": "quickstart/answer-quality",
  "SetName": "Answer Quality Suite",
  "Version": "1.0.0",
  "AssetClass": "text-answer",
  "Members": [
    {
      "MetricID": "quickstart/response-helpfulness",
      "Status": "OK",
      "Weight": 2,
      "Required": true,
      "Score": 4,
      "Result": {
        "Score": 4,
        "PairwiseChoice": "",
        "Explanation": "The response satisfies the metric because ...",
        "RawOutput": ["..."],
        "CustomOutput": {},
        "rubric_detail": true,
        "warnings": ["..."],
        "Stats": {
          "duration_ns": 9000000,
          "token_usage": {
            "prompt_tokens": 10,
            "candidates_tokens": 20,
            "total_tokens": 30
          }
        }
      },
      "Error": ""
    }
  ],
  "Aggregate": {
    "Method": "weighted-mean",
    "Score": 3.67,
    "Threshold": 3,
    "Passed": true,
    "Scored": 2,
    "Failed": 0
  },
  "Verdict": "PASSED",
  "Gate": true,
  "StartedAt": "2026-09-16T12:00:00Z",
  "Duration": 1500000000
}
```

Field semantics (from `internal/evalset/result.go`):

- **`SetID` / `SetName` / `Version` / `AssetClass`** — the manifest's `metadata`
  (id, name, version, assetClass); identify the set in your summary.
- **`Members[]`** — one row per member (the scorecard). For each member:
  - **`MetricID`** — the member's template id.
  - **`Status`** — `OK` (ran, result returned), `Errored` (engine returned an
    error), `Missing` (template id could not be resolved), or `Skipped` (fail-fast
    aborted before it ran). Only `OK` members contribute a score.
  - **`Weight`** — the member's weight in the aggregate.
  - **`Required`** — whether the member is required; a required member that does not
    succeed forces a `FAILED` verdict.
  - **`Score`** — the member's numeric score, mirrored from `Result` for
    convenience; `null` for non-scalar members (e.g. pairwise/custom_schema) and
    for non-`OK` members.
  - **`Result`** — the full embedded `eval.Result` for the member (same shape the
    `run-eval` skill documents: `Score`, `PairwiseChoice`, `Explanation`,
    `RawOutput`, `CustomOutput`, `rubric_detail`, `warnings`, `Stats` with
    `duration_ns` and optional `token_usage`). Read `Result.Explanation` to explain
    a member's verdict.
  - **`Error`** — the engine/resolution error message for an `Errored`/`Missing`
    member; empty on success. Always surface these.
- **`Aggregate`** — the computed scalar aggregation over the numeric-scored members:
  - **`Method`** — `mean`, `weighted-mean`, or `min` (empty when the manifest
    declared no `aggregation` block).
  - **`Score`** — the aggregate value, or `null` when no member produced a numeric
    score.
  - **`Threshold`** — the pass threshold from the manifest, or `null` when none.
  - **`Passed`** — whether `Score` met `Threshold`; `null` when there is no threshold.
  - **`Scored`** — count of members included in the aggregate (OK + finite score).
  - **`Failed`** — count of members that `Errored` or are `Missing` (Skipped
    members are **not** counted as failures).
- **`Verdict`** — the overall outcome, **always computed** regardless of the gate:
  `PASSED` or `FAILED`. This is the headline PASS/FAIL to report.
- **`Gate`** — the manifest's opt-in gate flag (`aggregation.gate`). The library
  records it but does **not** act on it; the CLI turns it into an exit code (below).
- **`StartedAt`** — RFC3339 start time. **`Duration`** — total wall-clock duration
  in **nanoseconds**.

## Gate & exit code (for CI)

The set's **`Verdict`** (PASSED/FAILED) is always shown on stdout. Whether a FAILED
verdict also **fails the process** is the opt-in gate, keyed off the manifest's
`aggregation.gate` flag. The rule the CLI applies (from `evalSetGateError` in
`cmd/mizan/evalset.go`) is exactly:

<!-- gate-exit-code -->
```json
{
  "gate=false, verdict=PASSED": 0,
  "gate=false, verdict=FAILED": 0,
  "gate=true, verdict=PASSED": 0,
  "gate=true, verdict=FAILED": 1
}
```

So a non-zero exit happens **only** when the set is a gate (`gate: true`) **and**
the verdict is `FAILED`; every other combination exits `0` (the scorecard still
shows the verdict). To wire an eval-set into CI, use a gated manifest and let the
exit code fail the build:

```bash
# Gated manifest: a FAILED verdict exits non-zero and fails the CI step.
mizan eval run --set ./evalsets/answer-quality-strict-gate.yaml \
  --field prompt="$PROMPT" --field response="$RESPONSE" -o json
echo "exit: $?"   # 0 = passed (or non-gate); non-zero = gate failed
```

When reporting to a human, always state the **Verdict** and whether the set is a
**gate**; when a gate fails, surface the CLI's stderr gate message verbatim
(`eval-set gate failed: FAILED verdict for <set id>`).

## Out of scope (do not reference)

- **Per-eval-set persistence / trend.** Eval-set *results* are **not** persisted to
  the results store, and there is **no** per-eval-set history/trend command
  (Mizan's `results` ships only `list`/`show`, over single-eval runs). Do not tell
  the user to `mizan results` for the set's history or reference a `results
  summary`/`trend` command — those do not exist. Persistence/trend for eval-sets is
  a documented follow-up, not a shipping capability.

## Reporting back to the user

Summarize concisely: the set (`SetID`/`SetName`), the **overall Verdict**
(PASS/FAIL) and, if gated, whether the gate passed; the **Aggregate** (`Method`,
`Score` vs `Threshold`); and a per-member line for each `Members[]` row — its
`MetricID`, `Status`, `Score` (or `-`), and, for any failing/errored member, its
`Result.Explanation` or `Error`. On failure, surface the CLI's stderr message
verbatim (especially credentials/project or `guardTextSlot` errors) and suggest the
concrete fix.
