---
name: run-eval
description: Run a single (pointwise) or pairwise Mizan evaluation of a response/asset against a metric template and explain the verdict, driving the `mizan` CLI over `-o json` and reasoning over the parsed result. Use when a user asks to evaluate, score, grade, or A/B-compare a text response or media asset against a metric, rubric, or guidance, or to understand why an asset passed/failed an eval.
license: Apache-2.0
compatibility: Requires the `mizan` CLI on PATH (go install github.com/ghchinoy/mizan/cmd/mizan@latest). Live evals call Vertex AI and need Google Application Default Credentials (ADC) plus a configured project/location; this skill never takes or stores credentials — it relies on the user's existing ADC exactly as the CLI does.
metadata:
  author: ghchinoy
  version: "0.1.0"
---

# Run a Mizan evaluation (`run-eval`)

Score one response against a metric (**pointwise**), or compare two responses and
pick the better (**pairwise**), by driving the local `mizan` CLI and reasoning
over its machine-readable `-o json` output. This skill wraps commands that exist
in the Mizan CLI today; it starts no server and re-implements no CLI logic.

## When to use this skill

- "Evaluate / score / grade this response against metric X."
- "Does this asset align with our brand / quality / safety guidance?"
- "Compare response A vs response B — which is better and why?"
- "Explain why this run got the score it did."

## Prerequisites (check first, in order)

1. **`mizan` is installed.** Run the precheck and stop with an install hint if it fails:
   ```bash
   command -v mizan >/dev/null 2>&1 || {
     echo "mizan not found on PATH. Install with: go install github.com/ghchinoy/mizan/cmd/mizan@latest" >&2
     exit 1
   }
   ```
2. **Credentials for a live eval.** A real eval calls Vertex AI and needs ADC and a
   project. Do **not** collect or store keys — rely on the user's existing ADC
   (`gcloud auth application-default login`) and the project already resolved by
   `mizan` (`flag > env MIZAN_PROJECT_ID/PROJECT_ID > .env > default`). If the run
   fails with a project/credentials error, surface the CLI's message verbatim and
   ask the user to configure ADC/project; never attempt to supply credentials yourself.

## Selecting the metric

If you do not already have a metric id (`<namespace>/<slug>`), discover one:

```bash
mizan registry list -o json          # all templates: [{ID, Name, Kind, ...}, ...]
mizan registry list --namespace <ns> -o json
mizan registry list --kind rubric -o json     # single|pointwise, compare|pairwise, rubric, custom_schema
mizan registry get <namespace>/<slug> -o json  # inspect one template (Kind, Inputs, field names)
```

Use `registry get` to learn the template's **input field names** (and, for pairwise,
its baseline/candidate field names) so you fill the right slots below. Pick a
pairwise (`compare`) template for A/B comparisons and a pointwise/rubric template
for single scoring.

> Only real flags: `registry list` supports `--namespace` and `--kind` (no `--tag`).

## Running a single (pointwise) evaluation

```bash
mizan eval run --metric <namespace>/<slug> --field <key>=<value> -o json
```

Fill instance inputs by source (keys must match the template's input names):

- `--field key=value` — a **text** value (repeatable).
- `--file  key=/path/to/asset` — a **local** media asset; the engine stages it to GCS.
- `--gcs   key=gs://bucket/obj` — a **pre-staged** media asset.

Useful flags: `--stats` (duration; token usage on the genai/custom_schema path),
`--rubric-detail` (per-criterion scores for a rubric template),
`--model <id>` (override the autorater), `--no-store` (do not persist the run).

**Never route media through a text slot.** `mizan` hard-errors if a `gs://` URI or a
local media path is passed to a text slot (`--field`/`--baseline`/`--candidate`) —
this is the CLI's `guardTextSlot` protection, because a media reference sent as text
would be read verbatim by the judge, which never sees the asset. Always use `--file`
or `--gcs` for media. Honor this: choose the flag by the field's modality, and if the
CLI returns the guard error, re-run with `--file`/`--gcs` instead of forcing text.

## Running a pairwise (compare) evaluation

```bash
mizan eval pairwise --metric <namespace>/<slug> \
  --baseline <baseKey>=<text> --candidate <candKey>=<text> -o json
```

`--baseline`/`--candidate` are **text** slots (same `guardTextSlot` rule applies). For
a **media** pairwise, supply the baseline/candidate slots with `--gcs`/`--file`
instead, keyed by the template's baseline/candidate field names, e.g.:

```bash
mizan eval pairwise --metric <ns>/<slug> \
  --gcs <baseKey>=gs://bucket/a.mp4 --gcs <candKey>=gs://bucket/b.mp4 -o json
```

Pairwise yields a `PairwiseChoice` (`BASELINE` / `CANDIDATE` / `TIE`), not a `Score`.

## Reading the result (`eval.Result`)

`-o json` prints exactly one JSON object (the `eval.Result` struct) to **stdout**;
pre-flight echo, warnings, and store notices go to **stderr**. Parse stdout and
reason over the struct. This is the shape the drift test in
`internal/skilldocs/drift_test.go` keys off — keep it in lockstep with the CLI:

<!-- drift:eval.Result -->
```json
{
  "Score": 4,
  "PairwiseChoice": "CANDIDATE",
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
}
```

Field semantics (from `internal/eval/engine.go`):

- **`Score`** — pointwise numeric score (a number, or `null` when a pairwise run
  produced a choice instead).
- **`PairwiseChoice`** — `BASELINE` / `CANDIDATE` / `TIE`; empty (`""`) for pointwise.
- **`Explanation`** — the judge's rationale; the primary thing to surface to the user.
- **`RawOutput`** — raw judge output, when the template requests it.
- **`CustomOutput`** — free-form map for `custom_schema` templates and, with
  `--rubric-detail`, the `{per_criterion, overall_score, explanation}` breakdown.
- **`rubric_detail`** — present/`true` only on a `--rubric-detail` run (omitted otherwise).
- **`warnings`** — non-fatal notes (e.g. the pairwise flip caveat); present only when
  the run produced any (also echoed to stderr). Always surface these to the user.
- **`Stats.duration_ns`** — wall-clock duration in nanoseconds (always present).
- **`Stats.token_usage`** — prompt/candidates/total tokens; present only on the
  genai / `custom_schema` path (the native path returns no token usage).

> The resolved autorater (`Applied`) is deliberately **not** serialized (`json:"-"`);
> do not expect it in `-o json`.

## Surfacing the persisted RunID

By default a successful `eval run` / `eval pairwise` is **persisted** to the local
results store (opt out with `--no-store`), but the run's stdout JSON does **not**
include the RunID. Retrieve the persisted RunID (a time-sortable ULID) right after
the run so the user can drill down later:

`mizan results list -o json` returns a JSON **array** (newest first), so read the
RunID from the first element — `.[0].RunID`, not `.RunID`:

```bash
mizan results list --metric <namespace>/<slug> --limit 1 -o json   # newest first; RunID is .[0].RunID
mizan results show <run-id> -o json                                # full provenance for that run
```

## Reporting back to the user

Summarize concisely: the metric, the **Score** (or **PairwiseChoice**), the
**Explanation**, any **warnings**, the duration (and token usage if present), and the
persisted **RunID** for follow-up. On failure, surface the CLI's stderr message
verbatim (especially credentials/project or `guardTextSlot` errors) and suggest the
concrete fix.
