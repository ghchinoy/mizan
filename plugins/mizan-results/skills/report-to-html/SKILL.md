---
name: report-to-html
description: Turn stored Mizan eval results into a single self-contained, standalone HTML report (summary table, score distribution, per-metric trend) by querying the `mizan` CLI over `-o json`, aggregating client-side, and rendering an offline report.html. Use when a user asks to review, summarize, visualize, chart, or report on past evaluation results, generate an HTML results report/dashboard, or see score trends over time.
license: Apache-2.0
compatibility: Requires the `mizan` CLI on PATH (go install github.com/ghchinoy/mizan/cmd/mizan@latest) and python3 (3.8+) for the bundled renderer. Reads the local results store only; needs no network and no credentials — this skill never takes or stores keys and relies on the user's existing config/ADC exactly as the CLI does.
metadata:
  author: ghchinoy
  version: "0.1.0"
---

# Report Mizan results to HTML (`report-to-html`)

Turn eval results already persisted in the local Mizan results store into **one
self-contained, standalone HTML file** — a summary table, a score distribution,
and a per-metric trend over time. This skill drives the local `mizan` CLI over
its machine-readable `-o json` output and reasons/aggregates client-side; it
**starts no server**, makes **no network request**, and **re-implements no CLI
logic** (querying and filtering are done by the CLI's own flags).

The output `report.html` embeds its own CSS, JavaScript, and data (the results
JSON) inline — so it opens directly from the filesystem with **no external fetch,
no CDN, and no listening process**.

## When to use this skill

- "Make an HTML report of my recent eval results."
- "Summarize / visualize / chart the results for metric X."
- "Show me the score trend over time for our brand-safety evals."
- "Generate a shareable results dashboard I can open in a browser."

## Prerequisites (check first, in order)

1. **`mizan` is installed.** Run the precheck and stop with an install hint if it fails:
   ```bash
   command -v mizan >/dev/null 2>&1 || {
     echo "mizan not found on PATH. Install with: go install github.com/ghchinoy/mizan/cmd/mizan@latest" >&2
     exit 1
   }
   ```
2. **`python3` is available** for the bundled renderer:
   ```bash
   command -v python3 >/dev/null 2>&1 || {
     echo "python3 not found on PATH; it is required to render the HTML report." >&2
     exit 1
   }
   ```
3. **No credentials needed.** `results list` / `results show` read the **local**
   results store the CLI already writes; they call no LLM and need no ADC. Do not
   collect or store keys — rely on the user's existing `mizan` configuration
   exactly as the CLI does.

## Step 1 — query the results with the CLI (never fabricate a command)

The data source is **`mizan results list -o json`**, which prints a JSON **array**
(newest first) of the `results.Result` records persisted by `eval run` /
`eval pairwise`. Filter with the **real** flags only:

```bash
mizan results list -o json                                   # all stored results (newest first)
mizan results list --metric <namespace>/<slug> -o json       # one template id
mizan results list --namespace <ns> -o json                  # all templates in a namespace
mizan results list --since 2026-08-01 -o json                # RFC3339 timestamp or YYYY-MM-DD date
mizan results list --limit 200 -o json                       # cap rows (0 = backend default)
```

Combine filters as needed, then save the array to a file to feed the renderer:

```bash
mizan results list --namespace <ns> --since 2026-08-01 -o json > /tmp/mizan-results.json
```

> **Use only these flags for this skill.** `report-to-html` filters with
> `--metric`, `--namespace`, `--since`, and `--limit`. It does **not** use
> `results list --tag`: that flag exists (registry→results tag join) but
> tag-filtered discovery is **out of scope** for this skill, which reports over
> whatever `results list` returns. There is still **no `mizan results summary`
> and no `mizan results trend` command** — do not use or reference them. Filtering
> is the CLI's job (the flags above); all summary and trend numbers are computed
> **client-side** by the bundled renderer from the `results list` array. There is
> no `--format`; the switch is `-o json`.

For a single run's full provenance (drill-down), use:

```bash
mizan results show <run-id> -o json                          # one results.Result object
```

The `RunID` for drill-down is the `RunID` field of any array element (e.g.
`.[0].RunID` for the newest).

## Step 2 — render the standalone HTML report

Pass the saved JSON to the bundled renderer. **Always pass paths as argv
arguments** (never interpolate a path into a shell string — the renderer reads
`--input`/`--output` from `sys.argv`):

```bash
python3 scripts/render_report.py --input /tmp/mizan-results.json --output report.html --threshold 3
```

Or stream directly from the CLI over stdin (the renderer reads stdin when
`--input` is omitted or `-`):

```bash
mizan results list --namespace <ns> -o json \
  | python3 scripts/render_report.py --output report.html --threshold 3
```

Flags:

- `--input <path>` / `-i <path>` — the `results list -o json` array file. Omit (or
  pass `-`) to read the JSON from **stdin**.
- `--output <path>` / `-o <path>` — where to write the single `report.html`
  (default `report.html`).
- `--threshold <number>` / `-t <number>` — the pass/fail cutoff for the pass-rate
  (a result **passes** when its numeric `Score >= threshold`). Default `3`.
- `--title <string>` — optional report heading override.

The renderer emits **one** `report.html` with everything inline. Report the
output path back to the user; the file opens directly in a browser with no server.

## Client-side aggregation (what the renderer computes)

The renderer computes **only** from the `results list` array — the CLI does not
provide these:

- **Overall summary:** result count, mean / min / max `Outcome.Score`, and the
  **pass-rate** at `--threshold` (fraction with `Score >= threshold`). Pairwise
  results (no `Score`, a `PairwiseChoice` instead) are counted and tallied by
  choice, and excluded from score statistics.
- **Per-metric grouping:** the same statistics grouped by `Template.ID`.
- **Score distribution:** a histogram of `Outcome.Score` across all pointwise
  results.
- **Per-metric trend:** results bucketed by `RunAt` (chronological), so score
  movement over time is visible per `Template.ID`.
- **Per-criterion detail (when present):** when a result carries
  `Outcome.RubricDetail == true`, its `Outcome.CustomOutput` holds the free-form
  per-criterion breakdown; the renderer surfaces it in the drill-down without
  assuming a fixed schema (it is free-form data, not part of the contract).

## Graceful degradation on an empty store

`mizan results list -o json` prints `[]` when the store is empty. The renderer
**must not error** on an empty array: it produces a valid `report.html` that
states there are no results yet and suggests running an eval first
(`mizan eval run …`). Always surface that empty-store report rather than failing.

## The `results.Result` shape (`-o json`)

`mizan results list -o json` prints a JSON **array** of the objects below;
`mizan results show <run-id> -o json` prints **one** such object. Most top-level
fields use Go's default (capitalized) key names — they carry no `json` tag — so
read `RunID`, `Template.ID`, `Outcome.Score`, etc. exactly as shown. This is the
shape the drift test in `internal/skilldocs` keys off; keep it in lockstep with
the CLI. Fields marked *omitempty* below (`Rubric`, and several `Outcome`/`Rubric`
sub-fields) may be **absent** on a given record; the maximal shape is documented
here so the contract is complete:

<!-- drift:results.Result -->
```json
{
  "RunID": "01JABCDEF0123456789ABCDEFG",
  "RunAt": "2026-09-16T12:00:00Z",
  "RunKind": "single",
  "Mizan": {
    "Version": "v0.1.0",
    "Commit": "abcdef0",
    "Date": "2026-09-16"
  },
  "Invocation": {
    "Command": "eval run",
    "ProjectID": "my-project",
    "Location": "us-central1",
    "HostLabel": "workstation",
    "Actor": "alice"
  },
  "Template": {
    "ID": "brand/tone",
    "Version": "1.0.0",
    "ContentHash": "sha256:...",
    "Kind": "rubric",
    "Source": "pack:google-brand@origin"
  },
  "Autorater": {
    "Model": "gemini-2.5-pro",
    "SamplingCount": 1,
    "FlipEnabled": true,
    "EffectiveHost": "global",
    "Location": "global",
    "ModelSource": "flag"
  },
  "Rubric": {
    "Method": "authored",
    "GeneratorModel": "gemini-2.5-pro",
    "Recipe": "general_quality_v1",
    "Origins": ["authored"],
    "ScaleMin": 1,
    "ScaleMax": 5,
    "DetailMode": true
  },
  "Inputs": [
    {
      "Field": "response",
      "Modality": "text",
      "ContentHash": "sha256:...",
      "Mode": "inline",
      "Inline": "the response text",
      "URI": "gs://bucket/object",
      "MimeType": "text/plain"
    }
  ],
  "Outcome": {
    "Score": 4,
    "PairwiseChoice": "CANDIDATE",
    "Explanation": "The response satisfies the metric because ...",
    "CustomOutput": {
      "per_criterion": []
    },
    "RubricDetail": true,
    "Warnings": ["..."],
    "DurationNS": 9000000,
    "TokenUsage": {
      "PromptTokens": 10,
      "CandidatesTokens": 20,
      "TotalTokens": 30
    }
  }
}
```

Field semantics (from `internal/results/result.go`):

- **`RunID`** — the run's ULID (time-sortable); use it with `results show` to drill
  down. **`RunAt`** — UTC wall-clock start (the time axis for trend bucketing).
  **`RunKind`** — `single` for a standalone eval.
- **`Template.ID`** — `<namespace>/<slug>`; the grouping key for per-metric
  summary and trend. `Template.Version`/`ContentHash` pin the exact spec.
- **`Autorater.Model`** — the resolved autorater used (shown in drill-down).
- **`Rubric`** — present (*omitempty*) only for rubric templates; `GeneratorModel`,
  `Recipe`, `Origins`, `ScaleMin`, `ScaleMax` are themselves *omitempty*.
- **`Inputs[]`** — the stored eval input fields (`Inline` set for `Mode: inline`,
  `URI` for `Mode: reference`; `Inline`/`URI`/`MimeType` are *omitempty*).
- **`Outcome.Score`** — pointwise numeric score (the value aggregated for
  mean/min/max, pass-rate, distribution, and trend); *omitempty*, absent on a
  pure pairwise result.
- **`Outcome.PairwiseChoice`** — `BASELINE`/`CANDIDATE`/`TIE` for pairwise runs;
  *omitempty*.
- **`Outcome.Explanation`** — the judge's rationale (shown in drill-down);
  *omitempty*.
- **`Outcome.CustomOutput`** — free-form map (custom_schema / rubric-detail
  payload); its keys are data, not part of the contract. *omitempty*.
- **`Outcome.RubricDetail`** — `true` when a per-criterion breakdown is present in
  `CustomOutput`; *omitempty*.
- **`Outcome.Warnings`** — non-fatal notes; *omitempty*.
- **`Outcome.DurationNS`** — wall-clock duration in nanoseconds.
- **`Outcome.TokenUsage`** — prompt/candidates/total tokens; present (*omitempty*)
  only on the genai / custom_schema path.

## Reporting back to the user

State the output path of the single `report.html`, the number of results included,
the filters applied, the pass-rate threshold used, and the headline numbers
(count, mean score, pass-rate). Note that the file is self-contained and opens
directly in a browser with no server. On an empty store, say so and suggest
running an eval first. On CLI failure, surface the CLI's stderr message verbatim
and suggest the concrete fix.
