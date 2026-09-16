---
name: triage-a-result
description: Explain and triage a single past Mizan evaluation run — pull its full record with `mizan results show <run-id> -o json` (or find it with `mizan results list -o json`), lay out the provenance (template, autorater, inputs, build), explain the verdict, and suggest concrete next steps. Use when a user asks why an eval scored the way it did, wants to debug or understand a past run, or needs to know what to change and re-run.
license: Apache-2.0
compatibility: Requires the `mizan` CLI on PATH (go install github.com/ghchinoy/mizan/cmd/mizan@latest). Reads the local results store only — no network and no credentials; this skill never takes or stores keys and re-runs nothing on its own.
metadata:
  author: ghchinoy
  version: "0.1.0"
---

# Triage a Mizan result (`triage-a-result`)

Take one past evaluation run and make it legible: retrieve its stored record,
walk the **provenance** (which template/version, which autorater, which inputs,
which build), **explain the verdict** (the score or pairwise choice and the
judge's rationale), and propose **concrete next steps**. This skill drives the
local `mizan` CLI over its machine-readable `-o json` output and reasons
client-side; it **starts no server**, makes **no LLM call**, needs **no
credentials**, and **re-implements no CLI logic**.

## When to use this skill

- "Why did this eval score a 2?" / "Explain run `<run-id>`."
- "Debug / triage my last evaluation."
- "What should I change and re-run to improve this result?"
- "Show me the full provenance of this run."

## Prerequisites (check first)

1. **`mizan` is installed.** Stop with an install hint if it fails:
   ```bash
   command -v mizan >/dev/null 2>&1 || {
     echo "mizan not found on PATH. Install with: go install github.com/ghchinoy/mizan/cmd/mizan@latest" >&2
     exit 1
   }
   ```
2. **No credentials.** `results show` / `results list` read the **local** results
   store the CLI already wrote; they call no LLM and need no ADC. Do not collect
   or store keys.

## Step 1 — find the run (`mizan results list -o json`)

If the user already has a `RunID`, skip to Step 2. Otherwise locate it with
`results list`, which prints a JSON **array** of `results.Result` (newest first).
Filter with the **real** flags only:

```bash
mizan results list -o json                              # all stored results (newest first)
mizan results list --metric <namespace>/<slug> -o json  # one template id
mizan results list --namespace <ns> -o json             # all templates in a namespace
mizan results list --since 2026-08-01 -o json           # RFC3339 timestamp or YYYY-MM-DD
mizan results list --limit 50 -o json                   # cap rows (0 = backend default)
```

The `RunID` of any element (e.g. `.[0].RunID` for the newest) is what you pass to
`results show`.

> **No tag filter here.** This skill does **not** use `results list --tag`: that
> flag exists but tag-filtered discovery is **deferred / out of scope**. Narrow
> with `--metric`/`--namespace`/`--since`/`--limit`, then match any tag-like
> intent **client-side** over each result's `Template` data. Do not invoke
> `results summary`/`trend` here either: those commands **exist**, but their
> summary/trend enrichment is **out of scope for triage (deferred — B3)**, and
> `kind: heuristic` authoring is likewise out of scope.

## Step 2 — pull the full record (`mizan results show <run-id> -o json`)

```bash
mizan results show <run-id> -o json          # one results.Result object
mizan results show <run-id>                  # human-readable rendering
```

`-o json` prints a single `results.Result` — the same shape `results list` returns
as array elements. **The full `results.Result` `-o json` contract is documented
and hermetically drift-gated once** under the `report-to-html` skill (marker
`<!-- drift:results.Result -->` in
`plugins/mizan-results/skills/report-to-html/SKILL.md`) and in
`docs/agent-skills.md`; that single gate in `internal/skilldocs` is the source of
truth for the shape, so triage reads from it rather than re-declaring a second
contract block. The fields triage keys off:

## Step 3 — read the provenance (what produced this verdict)

- **`RunID`** — the run's ULID (time-sortable). **`RunAt`** — UTC start time.
  **`RunKind`** — `single` or the pairwise kind.
- **`Template.ID` / `Template.Version` / `Template.ContentHash` / `Template.Kind`
  / `Template.Source`** — exactly which metric spec (and version) was evaluated;
  the `ContentHash` pins the spec so a later edit is detectable.
- **`Autorater.Model` / `SamplingCount` / `FlipEnabled` / `EffectiveHost` /
  `Location` / `ModelSource`** — the resolved judge model and how it was picked
  (`ModelSource` = flag/template/config/built-in).
- **`Rubric`** (*omitempty*, rubric templates only) — `Method`, `GeneratorModel`,
  `Recipe`, `Origins`, `ScaleMin`/`ScaleMax`, `DetailMode`: how the rubric was
  produced and its score scale.
- **`Inputs[]`** — the evaluated fields (`Field`, `Modality`, `Mode`, and
  `Inline`/`URI`/`MimeType`); confirm the right asset went to the right slot.
- **`Mizan`** (`Version`/`Commit`/`Date`) and **`Invocation`**
  (`Command`/`ProjectID`/`Location`/`HostLabel`/`Actor`) — the build and the
  who/where/how of the run, for reproducibility.

## Step 4 — explain the verdict (`Outcome`)

- **`Outcome.Score`** — the pointwise numeric score (against the template's scale,
  e.g. 1–5). Absent (*omitempty*) on a pure pairwise run.
- **`Outcome.PairwiseChoice`** — `BASELINE`/`CANDIDATE`/`TIE` for pairwise runs.
- **`Outcome.Explanation`** — the judge's rationale; the core of the triage —
  quote/summarize the specific reasons cited.
- **`Outcome.RubricDetail`** = `true` means a per-criterion breakdown is present in
  **`Outcome.CustomOutput`** (free-form data — surface it without assuming a fixed
  schema, its keys are not part of the contract).
- **`Outcome.Warnings`** — non-fatal notes worth relaying. **`Outcome.DurationNS`**
  / **`Outcome.TokenUsage`** — cost/latency signals for the run.

## Step 5 — suggest concrete next steps

From the provenance + verdict, propose actionable fixes, e.g.:

- **Low score with clear rationale** → point at the specific `Explanation` gaps and
  suggest revising the asset, then re-run `run-eval` with the same `Template.ID`.
- **Wrong/unexpected input** → an `Inputs[]` slot got the wrong asset or modality;
  re-run with corrected fields.
- **Unexpected judge** → `Autorater.ModelSource`/`Model` shows a fallback model;
  pin one via `--model` or the `default-model` config (see `configure-mizan`).
- **Rubric mismatch** → the `Rubric`/`Template.Version` is not what was intended;
  pick or author the right template (`discover-and-import-templates`,
  `author-and-validate-a-template-pack`).
- **Compare peers** → use `results list --metric <id>` to see how sibling runs
  scored, or `report-to-html` for a summary/trend view across many runs.

Never silently re-run an eval; suggest the exact command and let the user decide
(re-running a live eval uses their ADC and may incur cost).

## Reporting back to the user

Lead with the headline: the `Template.ID`@`Version`, the `Score` (or
`PairwiseChoice`), and a one-line reading of the `Explanation`. Then give the
provenance (autorater, inputs, build), any `Warnings`, and a short ranked list of
concrete next steps with the exact commands. Surface any CLI stderr verbatim with
the concrete fix; if the run id is not found, suggest `results list -o json` to
locate the right `RunID`.
