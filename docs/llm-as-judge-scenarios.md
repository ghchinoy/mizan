# LLM-as-a-Judge Scenarios — what can I judge with Mizan?

This guide is organized the way you actually think about evaluation: **start from
what you want to judge**, then jump to the Mizan capability that does it. It is
the inverse of a feature reference — instead of "here is what `eval run` can do,"
each section begins with a goal ("I want to score one response," "I want to pick
the better of two answers") and maps it to the template kind, command, and output
that achieve it.

Every scenario here is grounded in code that ships today. Where a scenario is
**not yet built**, it is called out explicitly under
[Roadmap scenarios](#roadmap-scenarios-not-built-yet). For copy-pasteable,
live-verified recipes see [`docs/testing-guide.md`](testing-guide.md); for the
narrative CLI walkthrough (install, config, CRUD) see
[`docs/user-guide.md`](user-guide.md); for the authoritative architecture see
[`docs/architecture-final.md`](architecture-final.md).

> **How Mizan judges.** A Mizan *metric template* is a stored, named
> autorater definition (`internal/registry/model.go`, `MetricTemplate`). Running
> it materializes the template into either a native Vertex AI
> `EvaluateInstances` call or, for structured output, a direct `genai`
> `GenerateContent` call — the engine picks the path from the template's
> `Kind` (`internal/eval/engine.go`, `Engine.dispatch`). You author templates
> with `mizan registry create` and run them with `mizan eval run` /
> `mizan eval pairwise`.

---

## Decision guide: pick a scenario

| I want to… | Scenario | Kind | Command | Section |
|---|---|---|---|---|
| Score one response on a quality dimension I define | Pointwise quality scoring | `pointwise` | `mizan eval run` | [↓](#scenario-1-score-a-single-response-pointwise) |
| Decide which of two responses is better | Pairwise preference | `pairwise` | `mizan eval pairwise` | [↓](#scenario-2-compare-two-responses-pairwise) |
| Grade against several named criteria and get one score | Rubric — overall score | `rubric` | `mizan eval run` | [↓](#scenario-3-grade-against-an-authored-rubric) |
| See a score **and rationale for each criterion** | Rubric — per-criterion transparency | `rubric` + `--rubric-detail` | `mizan eval run --rubric-detail` | [↓](#scenario-4-explainable-per-criterion-rubric-scoring-rubric-detail) |
| Get a typed JSON verdict (compliance flags, booleans, arrays) | Structured / compliance check | `custom_schema` | `mizan eval run` | [↓](#scenario-5-structured--compliance-verdicts-custom_schema) |
| Judge an image, audio, video, or music asset | Multimodal evaluation | any kind + `--modality` | `--file` / `--gcs` | [↓](#scenario-6-judge-media-not-just-text-multimodal) |
| Choose *which* model does the judging | Judge-model selection | any | `--model` / `default-model` | [↓](#scenario-7-choose-the-judge-model) |
| See timing, token cost, and the resolved target of a run | Per-run observability | any | `--stats` + pre-flight echo | [↓](#scenario-8-see-what-a-run-cost-and-where-it-went) |

All scenarios assume you have configured a project and authenticated — see
[Prerequisites](#prerequisites).

---

## Prerequisites

```sh
go install github.com/ghchinoy/mizan/cmd/mizan@main
mizan config set project-id <your-project-id>
mizan config set location us-central1     # native default region; the "us" multi-region 404s
gcloud auth application-default login      # ADC — there is no API-key auth path
```

Full detail (env-var overrides, `.env` location, staging bucket) is in
[`docs/user-guide.md`](user-guide.md). To keep experiments isolated, point the
registry at a scratch DB: `export MIZAN_REGISTRY_DB=/tmp/mizan-scratch.db`.

---

## Scenario 1: Score a single response (pointwise)

**What it's for.** You have one response (or one asset) and one quality
dimension — conciseness, helpfulness, factual accuracy, tone — and you want a
numeric score plus a rationale. This is the simplest and most common judgment.

**When to use it.** You are grading responses one at a time on a scale you define
in the prompt, and a single number is enough.

**How it works.** `KindPointwise` materializes a `PointwiseMetricSpec` and calls
the native `EvaluateInstances` RPC; the response maps to `{Score, Explanation}`
(`internal/eval/native.go`, `runPointwise` → `runNativePointwise`). The scale is
**whatever your prompt asks for** — Mizan imposes none.

**Author and run:**

```sh
mizan registry create --id demo/conciseness --name "Conciseness" \
  --kind pointwise \
  --prompt "Rate how concise this response is from 0 (verbose) to 1 (concise). Response: {{response}}"

mizan eval run --metric demo/conciseness --field response="The cat sat on the mat."
```

```
Score:        0.95
Explanation:  The sentence is extremely concise, using the minimum number of
              words necessary to convey a complete and clear thought.
```

**What the output tells you.** `Score` is a float on the scale your prompt
defined (here `0.95` = "very concise"). `Explanation` is free-text rationale —
treat it as a qualitative aid, not a machine-parseable field.

**Limits / caveats.**
- Output is a **single** score + explanation. For per-criterion detail, use
  Scenario 4; for a typed object, use Scenario 5.
- The autorater call is non-deterministic: the *shape* (`Score` + `Explanation`)
  is guaranteed, the exact number/text varies run to run.

---

## Scenario 2: Compare two responses (pairwise)

**What it's for.** You have two candidate answers — model A vs model B, old
prompt vs new prompt, human vs machine — and you want the judge to pick the
better one rather than score them independently.

**When to use it.** A/B comparisons, regression checks ("is the new answer at
least as good?"), and preference data collection, where relative quality matters
more than an absolute number.

**How it works.** `KindPairwise` materializes a `PairwiseMetricSpec` with your
baseline/candidate field names and calls native `EvaluateInstances`; the result
is a `PairwiseChoice` mapped to `BASELINE` / `CANDIDATE` / `TIE`
(`internal/eval/pairwise.go`, `runPairwise` → `pairwiseChoiceString`). To counter
position bias, pairwise runs with **flip enabled** (the template's
`--flip-enabled` flag, **default `true`**) and multiple samples (default
`SamplingCount` = 4, `pairwiseDefaultSamplingCount`; lower it to trade
self-consistency for latency).

**Author and run:**

```sh
mizan registry create --id demo/pairwise-quality --name "Pairwise Quality" --kind pairwise \
  --prompt "Which response better answers 'What is the capital of France?' Baseline: {{baseline_response}} Candidate: {{candidate_response}}" \
  --baseline-field baseline_response --candidate-field candidate_response

mizan eval pairwise --metric demo/pairwise-quality \
  --baseline baseline_response="Paris." \
  --candidate candidate_response="The capital of France is Paris, home of the Eiffel Tower."
```

```
Choice:       BASELINE
Explanation:  The baseline response is more direct and concise, providing only
              the information asked for.

(stderr) pairwise flip is enabled: the Choice is the de-biased, authoritative
verdict; the explanation ... may not match your input. ...
```

**What the output tells you.** `Choice` names the winner (`BASELINE`,
`CANDIDATE`, or `TIE`) — pairwise yields no numeric `Score`, so no `Score:` line
is printed. **The `Choice` is authoritative.** `Explanation` justifies the pick,
but see the flip caveat below.

**Limits / caveats.**
- **Placeholder contract (enforced):** the prompt template *must* reference both
  the baseline and candidate field names as `{{name}}` placeholders — the API
  rejects instance keys absent from the template, so Mizan fails fast client-side
  with a clear message (`validatePairwisePlaceholders`).
- **The `Choice` is authoritative; the `Explanation` may not be, under flip.**
  With flip on (the default), the judge evaluates both position orderings and
  returns a de-biased, aggregated `Choice`. The `Explanation`, however, is one
  sampled artifact whose "baseline"/"candidate" wording may reflect a flipped
  ordering — so it can read as though it praises the *other* response even though
  the `Choice` is correct. `mizan eval pairwise` warns about this on stderr (and
  serializes it under `warnings` in `--output json`).
- **`--flip-enabled` is honored.** Create the template with `--flip-enabled=false`
  to keep the explanation's wording aligned with the presented order, at the cost
  of position-bias mitigation. Because the P1 registry stores `FlipEnabled` as a
  plain `bool` (no tri-state), set it explicitly at `create` time.

---

## Scenario 3: Grade against an authored rubric

**What it's for.** You want to judge against **several named criteria you
author** (clarity, correctness, tone…) grouped into categories, and get one
overall score that reflects how many criteria were satisfied.

**When to use it.** Quality bars with more than one dimension, where you want the
judge to consider your specific checklist but a single roll-up score is
sufficient. (If you need a score *per* criterion, jump to Scenario 4.)

**How it works.** `KindRubric` renders your authored `RubricGroups` into the
judge prompt and runs the **native pointwise** path
(`internal/eval/native.go`, `runRubric` → `renderRubricGroups`). The synchronous
`EvaluateInstances` API has no inline structured-rubric input, so the criteria
are rendered as text into the same `{Score, Explanation}` mechanism as
pointwise — the criteria still drive the autorater; the result rolls up to one
score.

**Author and run:**

```sh
mizan registry create --id demo/rubric-quality --name "Response Quality Rubric" --kind rubric \
  --prompt "Evaluate this response: {{response}}" \
  --rubric-group "clarity=Is the response clear;Is it free of jargon" \
  --rubric-group "correctness=Is the factual content accurate"

mizan eval run --metric demo/rubric-quality \
  --field response="The Eiffel Tower is in Paris, France, completed in 1889."
```

```
Score:        5
Explanation:  The response is clear, free of jargon, and factually accurate on
              both the location and the completion date.
```

Author criteria inline with repeatable `--rubric-group "name=crit one;crit two"`
(same name accumulates), or from a JSON file with
`--rubric-groups-file <path>` (`{"group": ["crit1","crit2"], ...}`). Creating a
rubric template with no criteria fails at create time, not at run time.

**What the output tells you.** One `Score` + `Explanation`; the explanation
typically names which criteria were met or missed.

**Limits / caveats.**
- You get a **single** roll-up score — not one score per criterion. For that, use
  `--rubric-detail` (Scenario 4).
- Runs on the native regional path, so it keeps `AutoraterConfig` sampling. A
  global-only judge model still works here — Mizan auto-routes the call to the
  global host (see Scenario 7) — but that global run is not region-pinned.

---

## Scenario 4: Explainable, per-criterion rubric scoring (`--rubric-detail`)

> **Headline scenario.** This is the granular, explainable-rubric path: each
> criterion you authored gets its **own Likert score and rationale**, plus an
> overall score.

**What it's for.** You want full transparency: not just "the response scored 4/5"
but *why*, criterion by criterion — "clarity 5 (well structured), correctness 3
(one date is wrong)." Ideal for reviewer-facing scorecards, calibration, and
debugging where a response fell short.

**When to use it.** Whenever the *breakdown* is the value: rubric authoring,
model comparison at the criterion level, or surfacing a defensible scorecard to a
human reviewer.

**How it works.** Add `--rubric-detail` to a `rubric` template's `eval run`. The
engine routes that run through the **genai structured-output path** with a
deterministic response schema built from your `RubricGroups`, so the judge
returns one typed `{group, criterion, score, rationale}` entry per authored
criterion, plus `overall_score` and `explanation`
(`internal/eval/rubric_structured.go`, `runRubricStructured` →
`generateRubricSchema` / `renderRubricInstruction`). The overall score is
surfaced as `Result.Score`; per-criterion scores are validated and clamped into
the configured scale. This landed in PR #16.

**The scale is configurable.** `--rubric-scale "<min>-<max>"` (default `1-5`)
sets the Likert range every criterion and the overall score are held to; values
are parsed and clamped locally (`ParseRubricScale`, `clampRubricOutput`).

**Author and run:**

```sh
# Reuse the rubric template from Scenario 3, or author a fresh one.
mizan eval run --metric demo/rubric-quality --rubric-detail --rubric-scale 1-5 \
  --field response="The Eiffel Tower is in Paris, France, completed in 1889."
```

Output shape (per-criterion table from `renderRubricDetailResult`; scores are
live autorater output and vary run to run):

```
Score:        4.5
Explanation:  Clear and accurate overall, with minor room for added context.
Per-criterion:
GROUP        CRITERION                      SCORE  RATIONALE
clarity      Is the response clear          5      Direct, well-structured single sentence.
clarity      Is it free of jargon           5      No jargon; plain language.
correctness  Is the factual content accurate 4     Location and date are correct and verifiable.
```

For a machine-readable form, add `--output json` (global flag) — the full
`{per_criterion, overall_score, explanation}` structure is returned under
`CustomOutput`.

**What the output tells you.** `Score` is the judge's overall roll-up on your
scale; the **Per-criterion** table is the transparency payload — one row per
authored criterion with its score and rationale.

**Limits / caveats.**
- This path runs on the **genai client at `location=global`** and **drops
  `AutoraterConfig.SamplingCount`** (no multi-sample self-consistency) — an
  accepted trade-off of routing rubric through the structured-output path (see
  `design/rubric-decisions-locked.md`).
- `--rubric-detail` applies **only** to `rubric` templates; using it on any other
  kind fails with a crisp local error.
- Scale bounds must be **non-negative** integers with `min < max` (`"1-5"`,
  `"0-10"`); negative bounds are intentionally unsupported.
- The judge's returned criteria are **strictly reconciled** against your authored
  set, matched by the exact **(group, criterion)** pair (R-R2):
  - a **missing** authored criterion (authored but not returned) is a **hard
    error** naming the missing pair(s) — a partial scorecard is never surfaced;
  - a **duplicated** authored criterion (the same pair returned more than once) is
    a **hard error** naming the duplicated pair(s);
  - an **extra** criterion (returned but not authored) is **kept in the output**
    and reported as a **warning on stderr** (extras are informative, not
    corrupting) — it does not fail the run.

---

## Scenario 5: Structured / compliance verdicts (`custom_schema`)

**What it's for.** You want a **typed JSON verdict** rather than a free-text
score — e.g. a compliance check that returns `{compliant: bool, overall_score:
int, flagged_issues: [...], explanation: str}`. Anything you can express as a
JSON schema, the judge fills in.

**When to use it.** Policy/compliance gating, safety checks, extraction of
multiple named fields, or any downstream automation that needs to *parse* the
verdict instead of reading prose.

**How it works.** `KindCustomSchema` calls `genai.GenerateContent` directly with
your `ResponseSchema` enforced (`ResponseMIMEType: application/json`,
`Temperature: 0`) and exponential backoff on rate limits; the JSON response is
parsed into `Result.CustomOutput` (`internal/eval/custom.go`, `runCustomSchema`
→ `runGenaiStructured`). Unlike the native path, this path runs at
`location=global`.

**Author and run:** the repo ships an example schema at
[`docs/examples/compliance-schema.json`](examples/compliance-schema.json)
(`overall_score`, `compliant`, `flagged_issues`, `explanation`):

```sh
mizan registry create --id demo/custom-compliance --name "Compliance Check" --kind custom_schema \
  --prompt "Check if this response follows the policy: no medical advice. Response: {{response}}" \
  --response-schema-file docs/examples/compliance-schema.json

mizan eval run --metric demo/custom-compliance --field response="Drink plenty of water and rest."
```

```
Score:                         (none)
Explanation:
CustomOutput[compliant]:       true
CustomOutput[explanation]:     General wellness suggestions, not specific medical advice; complies with the policy.
CustomOutput[flagged_issues]:  []
CustomOutput[overall_score]:   9
```

Supply the schema inline with `--response-schema '<json>'` or from a file with
`--response-schema-file <path>`. Both standard lowercase JSON-Schema types
(`"object"`, `"integer"`, …) and genai's uppercase convention are accepted
(`toGenaiSchema` / `normalizeSchemaTypes`). Creating a `custom_schema` template
with no schema fails at create time.

**What the output tells you.** There is **no** `Score`/`Explanation` — the
verdict lives in `CustomOutput`, one line per property in your schema. Consume it
programmatically with `--output json`.

**Limits / caveats.**
- Runs at `location=global` on the genai path; it does **not** use
  `AutoraterConfig` sampling (single structured call).
- The output shape is only as good as your schema — mark the fields you require
  as `required` in the JSON schema.

---

## Scenario 6: Judge media, not just text (multimodal)

**What it's for.** Your "response" is an **image, audio, video, or music** asset
(optionally alongside text), and you want the judge to evaluate the asset —
"what's the dominant color in this image?", "does this audio match the
transcript?".

**When to use it.** Any evaluation whose subject is a media file. Multimodal
works with pointwise, rubric, and pairwise templates; declare the accepted
`--modality` values (`text`, `image`, `audio`, `video`, `music`) at create time.

**How it works.** Pass assets with `--file key=/path` (local) or
`--gcs key=gs://…` (pre-staged). The two evaluation paths handle media
differently, and this difference is the key caveat:

- **Native path** (`pointwise` / `rubric` / `pairwise`): `EvaluateInstances`
  accepts **`gs://` FileData only — inline bytes are silently dropped**
  (`internal/eval/content.go`, `toNativeFileDataPart`). So a local `--file` is
  **auto-staged to GCS** first (content-addressed by SHA-256) and requires a
  configured `staging-bucket`; a `--gcs` URI is used directly. A resolvable MIME
  type is mandatory (the API drops assets with a missing/wrong type).
- **Genai path** (`custom_schema`, and rubric `--rubric-detail`): **does** accept
  inline bytes, so a local `--file` is read and sent inline — no staging bucket
  needed (`toGenaiInlinePart`, capped at 20 MiB).

**Author and run (native image pointwise):**

```sh
mizan config set staging-bucket gs://<your-bucket>   # required for native local --file

mizan registry create --id demo/image-color --name "Image Color Check" --kind pointwise \
  --prompt "What is the dominant color in this image? {{photo}}" \
  --modality image --modality text

mizan eval run --metric demo/image-color --file photo=/tmp/test-image.png
```

```
Score:        5
Explanation:  The image is a solid color, and that color is unmistakably red.
```

**What the output tells you.** Same result shape as the underlying kind
(pointwise → `Score`/`Explanation`, etc.); the asset is just another instance
field.

**Limits / caveats.**
- **Native local `--file` requires a `staging-bucket`.** Without one, a
  native multimodal run fails with a clear "requires GCS staging" error; use
  `--gcs` with a pre-staged URI, or the genai path (`custom_schema`), which
  accepts inline bytes.
- Native assets need a resolvable MIME type; the genai inline path caps files at
  20 MiB.

---

## Scenario 7: Choose the judge model

**What it's for.** You want to control *which* model performs the judging — pin a
specific autorater for reproducibility, or opt into a newer model.

**How it works.** The autorater model is resolved once per run through a
precedence chain (`internal/eval/model.go`, `resolveModel`):

```
--model flag  >  template AutoraterModel  >  config default-model  >  built-in
```

- **`--model <id>`** on `eval run` / `eval pairwise` — highest precedence,
  per-run override.
- **Template `--model`** at create time — bakes a model into the template.
- **`default-model` config** (`mizan config set default-model <id>`, env
  `MIZAN_DEFAULT_MODEL`) — your account-wide default.
- **Built-in fallback** `gemini-2.5-flash` (`BuiltinDefaultModel`) — the single
  source of truth, used only when nothing above is set.

A malformed model id is rejected **locally** with a crisp error before any API
call (`ValidateModel`).

```sh
mizan config set default-model gemini-2.5-flash          # account-wide default
mizan eval run --metric demo/conciseness --model gemini-2.5-flash --field response="…"   # per-run override
```

**Global-only judges are auto-routed to the global host (R-GLOBAL).** Newer models
such as `gemini-3.5-flash` / `-flash-lite` are **global-only**: they resolve on the
global eval endpoint but **404 on the regional native path** (verified live —
`design/spike-eval-region-autorater.md`). The deciding factor is the eval endpoint
*host*, not the model's location path — so overriding only the autorater's location
while keeping a regional host still 404s. Mizan therefore moves the **whole**
`EvaluateInstances` call to the global host (`aiplatform.googleapis.com` /
`locations/global`) when the resolved autorater is global-only. You no longer need
to set `--location global` by hand.

- **The genai path** (`custom_schema`, and rubric `--rubric-detail`) already runs
  at `location=global` (`GenaiLocation`), so it uses a global-only judge directly.
- **The native path** (`pointwise` / `rubric` / `pairwise`) normally runs at your
  configured region (default `us-central1`). When the resolved judge is
  global-only, the engine runs that native call against the global host instead
  (`internal/eval/route.go`).

**How the routing is detected.** Two layers, so it is both fast and future-proof:

1. **Known-model fast-path.** A resolved autorater whose id begins with a
   documented global-only prefix (today the `gemini-3.5` family — the list lives in
   one place, `globalOnlyModelPrefixes` in `internal/eval/route.go`) is routed
   straight to the global host, skipping a guaranteed-to-404 regional attempt. The
   pre-flight echo (Scenario 8) then shows `location=global` up front.
2. **Self-correcting retry (safety net).** For any global-only judge *not* in that
   list, the first (regional) attempt fails with the specific
   `NOT_FOUND … Autorater model not found` error; Mizan matches that narrowly
   (gRPC `NotFound` **and** an autorater-model message) and transparently retries
   the same call on the global host. A non-autorater `NOT_FOUND` (e.g. a missing
   template) is **not** retried. If the global retry also fails, the original
   error is surfaced with context.

**`--location` is kept for labeling, not honored as residency for a global-only
judge.** A global-only judge cannot run in your region, so the routing is forced to
global regardless of `--location` / `MIZAN_LOCATION`. Your configured location is
still used for output/labeling, and Mizan prints a one-line notice to **stderr** so
this is never silent:

```
mizan: autorater gemini-3.5-flash is global-only (…); routing this eval to the GLOBAL host (location=global). Your configured --location is kept for labeling only.
```

The built-in default stays `gemini-2.5-flash` — served on *both* regional and
global endpoints — so a default run never triggers routing at all.

---

## Scenario 8: See what a run cost and where it went

**What it's for.** You want operational visibility: how long a judgment took, how
many tokens it used, and exactly which project / region / model the call will
hit — *before* it happens.

**How it works.** Two independent features:

- **Pre-flight echo (on by default).** Before every eval call, Mizan prints the
  resolved target to stderr — the project, location, model, and path
  (`native` or `genai`) the run will actually use
  (`cmd/mizan/eval.go`, `printPreflight` ← `Engine.Resolve`). It reflects the
  *actual* per-path location (native = your region; genai/`--rubric-detail` =
  global), so a wrong-project or unexpected-global surprise is visible up front
  instead of only when the API rejects the call. It goes to stderr, so it never
  pollutes `--output json` on stdout.

  ```
  mizan: autorater → project=my-proj location=us-central1 model=gemini-2.5-flash (path=native)
  ```

  For a **known global-only judge** the echo already shows `location=global` (the
  routing is detected up front — Scenario 7). For a global-only judge discovered
  only via the retry, Mizan additionally prints a "routing this eval to the GLOBAL
  host" notice to stderr at run time so the forced-global routing is never silent.

- **`--stats` (opt-in).** Adds a footer with wall-clock **duration** (always
  measured) and, on the genai path only, **token usage**
  (`prompt`/`candidates`/`total`) (`renderResult` / `renderStatsFooter`).

  ```sh
  mizan eval run --metric demo/conciseness --field response="…" --stats
  ```

**What the output tells you.**
- Pre-flight line: the exact resolved target — catch a misconfigured
  project/region before spending a call.
- `--stats`: `Duration` for every run; `Tokens` on the genai path
  (`custom_schema`, `--rubric-detail`). On the **native** path token usage is not
  available — `EvaluateInstances` returns no usage metadata — and `--stats` says
  so explicitly rather than showing zeros.

---

## Capability matrix

| Scenario | Kind | Path | Location | Output | Multimodal? | Sampling? | Token stats? |
|---|---|---|---|---|---|---|---|
| Pointwise scoring | `pointwise` | native | regional | `Score` + `Explanation` | ✅ (gs:// FileData) | ✅ | ❌ |
| Pairwise preference | `pairwise` | native | regional | `Choice` + `Explanation` | ✅ (gs:// FileData) | ✅ (flip, default 4) | ❌ |
| Rubric — overall | `rubric` | native | regional | `Score` + `Explanation` | ✅ (gs:// FileData) | ✅ | ❌ |
| Rubric — per-criterion | `rubric` + `--rubric-detail` | genai | global | overall `Score` + per-criterion table | ✅ (inline) | ❌ (dropped) | ✅ |
| Structured / compliance | `custom_schema` | genai | global | `CustomOutput` (typed JSON) | ✅ (inline) | ❌ | ✅ |

*"regional" = your configured `location` (default `us-central1`); "global" =
`aiplatform.googleapis.com` / `locations/global`. Native local `--file` assets
are auto-staged to GCS; genai paths accept inline bytes (≤ 20 MiB). A native run
whose resolved autorater is **global-only** is auto-routed to the global host
regardless of your `location` (Scenario 7).*

---

## Roadmap scenarios (not built yet)

These are **not implemented** — do not expect them to work today. See
[`docs/implementation-plan.md`](implementation-plan.md) for phasing and the root
[`README.md`](../README.md) for the current boundary.

- **Batch evaluation over a dataset** — judging many instances at once via
  `EvaluateDataset` over GCS-hosted data (which is also the official home for
  API-native per-criterion rubric output with sampling retained). No `eval batch`
  command exists. *Roadmap phase P3.*
- **Template packs / registry import & export** — sharing and versioning
  templates via `mizan pack` and `mizan registry import`/`export`. Not wired.
  *Roadmap phase P2.*
- **Pairwise tri-state flip default** — the engine now honors the template's
  `--flip-enabled` (including `false`), but the P1 registry stores it as a plain
  `bool`, so "unset ⇒ default true" can't be distinguished from an explicit
  `false`. A nullable/tri-state field is a *P2 registry change.*
- **Desktop app** — the Wails GUI is design-stage scaffolding only. *Roadmap
  phase P4.*

---

## Where to go next

- **Copy-pasteable, live-verified recipes** for every kind and multimodal —
  [`docs/testing-guide.md`](testing-guide.md).
- **Install, config, and full CRUD walkthrough** —
  [`docs/user-guide.md`](user-guide.md).
- **Authoritative architecture** (domain model, engine, CLI surface) —
  [`docs/architecture-final.md`](architecture-final.md).
- **Roadmap and acceptance criteria** —
  [`docs/implementation-plan.md`](implementation-plan.md).
</content>
</invoke>
