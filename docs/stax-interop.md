# Stax interoperability (`export stax`)

Mizan can hand a metric you authored, reviewed in a pull request, and validated
without credentials straight to [Stax](https://github.com/google-labs-code/stax),
Google Labs' LLM-evaluator library. The `mizan export stax` command converts one
local metric template into the **real Stax create-evaluator request** so a Stax
consumer can create the evaluator with a single HTTP call. This page explains
what that conversion produces, how to run it, what a consumer does with the
output, and where the two systems do not line up perfectly.

This is a one-directional export (Mizan to Stax). It is grounded in the approved
export spec, [`mizan-stax-export-spec.md`](../design/mizan-stax-export-spec.md),
and complements the command reference in
[the user guide](user-guide.md#export-to-stax-export-stax). Nothing here changes
how Mizan evaluates: it is a translation of a template's shape into Stax's DTO.

## What Stax is, and why interop matters

Stax is an evaluator library built around one core idea: an evaluator is a
**prompt template plus a set of named output categories**. At run time a judge
model reads the prompt, emits exactly one category name (plus its reasoning), and
a regular expression maps that category to a numeric score. Stax scoring is
therefore **categorical**: a single verdict per evaluator, not a decomposed
scorecard. Many evaluators can live side by side in one Stax container, which is
what makes Mizan's per-criterion export (below) fit naturally.

Mizan and Stax are both Apache-2.0, so an interchange between them is legally
unencumbered. The value of the interop is strategic rather than a new Mizan
runtime feature: Mizan is the git-native, PR-reviewed, credential-free authoring
and CI-gate front end, and Stax is a place those authored evaluators can run. If
your team authors metrics in Mizan but wants to seed a Stax evaluator library
from them, `export stax` is the bridge.

## The mapping (Mizan template to Stax DTO)

The exporter emits the exact JSON body a consumer `POST`s to Stax's
create-evaluator endpoint: a `LLMEvaluatorRequestDTO` (which extends
`BaseLLMEvaluatorRequestDTO`), carrying `Prompt`, `OutputCategoryDTO`, and
`EvaluatorVariableDTO` values. The field shapes below are confirmed against
`google-labs-code/stax` (package `com.planck.planck`).

| Mizan | Stax DTO field | Notes |
|---|---|---|
| template `ID` (plus `::group::criterion` on fan-out) | `name` (String) | direct |
| (fixed) | `output_format_type` = `"Choices"` | Stax categorical scoring; the only v1 target |
| declared `{{vars}}` used in the prompts | `variables`: `[{name, required}]` | one entry per distinct post-rename variable, each `required: true` |
| `AutoraterModel` | **not mapped** to `model_id` | you supply `model_id` with `--model-id`; see [Fidelity and known limitations](#fidelity-and-known-limitations) |
| `MetricPromptTemplate` | a `Prompt` with `role: "USER"`, `text` | direct, after placeholder rename |
| `SystemInstruction` | a `Prompt` with `role: "SYSTEM"`, `text` | emitted first; omitted entirely when empty |
| input placeholders (`{{response}}`, ...) | renamed `{{output}}` / `{{prompt}}` / `{{expected_output}}` / `{{history}}` inside `text` | rename map required |
| `RatingRubric` bands over the Likert scale | `output_categories`: `[{name, value}]` | `value` is a **String**, ordered by ascending numeric value |

A few shapes are worth calling out because they trip people who assume the
older, informal Stax schema:

- **`Prompt` uses `text`, not `content`.** Each prompt is `{role, text}`.
- **`role` is UPPERCASE.** It is the Stax `InputRole` enum serialized by name:
  `USER`, `SYSTEM`, `ASSISTANT`, and so on. The exporter emits `SYSTEM` (when a
  system instruction is present) and `USER`.
- **`output_categories` is a list of objects, and `value` is a String.** It is
  `[{ "name": "1-poor", "value": "1" }, ...]`, ordered by ascending numeric
  value, not a name-to-number map.
- **Fields serialize in DTO declaration order:** `name`, `output_format_type`,
  `variables`, `model_id`, `prompts`, `output_categories`. Null optionals are
  dropped. These bytes are locked by golden tests, so the output is stable.

### Rubric to Option B fan-out (the default)

Mizan's `rubric` kind scores **each `(group, criterion)` pair** on a Likert scale
and returns a rationale per pair, plus an overall rollup. A Stax evaluator emits
one verdict. To avoid collapsing that detail, the exporter defaults to a
**fan-out**: it emits **one Stax evaluator per criterion**. A template with M
criterion-pairs produces M evaluators, emitted as a JSON array.

Each evaluator is named with the `"{template-id}::{group}::{criterion}"`
convention, so the set stays traceable back to the one Mizan template it came
from (and is re-importable). This preserves Mizan's load-bearing differentiator,
per-criterion scores and rationales, while still producing valid single-category
Stax evaluators.

### Rubric with `--flatten` to Option A (opt-in)

If you only want a single headline score, pass `--flatten`. This emits **one**
aggregate evaluator whose prompt lists every criterion. It is **lossy**:
per-criterion scores and rationales, and per-group band descriptions, are
dropped. The command prints a `warning: flatten (Option A) dropped per-criterion
granularity: ...` line to stderr listing exactly what was collapsed, so the loss
is never silent.

### Pointwise to a direct single DTO

`pointwise` is a direct map: one Mizan template to one Stax evaluator, with no
flatten or fan-out choice. The `MetricPromptTemplate` becomes a `USER` prompt,
`SystemInstruction` (if any) becomes a `SYSTEM` prompt emitted first, and the
`RatingRubric` bands over the template's Likert scale become the
`output_categories`.

### Category naming

Category names follow one explicit rule that applies to every band of every
evaluator the exporter emits:

- A band is **anchored** only when its `RatingRubric` description is present and
  slugifies to a non-empty token. It is then named `"{band}-{slug}"`, where the
  slug is the description lowercased with each run of non-alphanumeric characters
  collapsed to a single hyphen and leading and trailing hyphens trimmed (so
  `"Very Good!"` becomes `very-good`, giving `5-very-good`). The `1-poor` and
  `5-great` names in the examples below are this rule applied to `poor` and
  `great`.
- Every **other band** is named `"score-{band}"`, for example `score-2`,
  `score-3`, `score-4`. This covers a band with no description, a blank
  (whitespace-only) description, and the edge case of a description that
  slugifies to an empty token (for example one made only of punctuation).

When a template declares no `RatingRubric`, every band falls back to
`"score-{band}"` across the `rubricDetail.scale` range (default 1 to 5).

### Placeholder rename

Mizan authors name their own input fields; Stax uses a fixed set of reserved
variables. The exporter rewrites `{{mizan_field}}` to `{{stax_var}}` inside the
prompt and system text:

| Mizan input field | Stax reserved variable |
|---|---|
| `response`, `output`, `answer`, `candidate` | `{{output}}` |
| `prompt`, `question`, `input`, `instruction` | `{{prompt}}` |
| `reference`, `expected`, `ground_truth`, `gold` | `{{expected_output}}` |
| `history`, `conversation`, `context` | `{{history}}` |

Two situations **fail the export** (nothing is written), rather than emit a lossy
guess:

- a placeholder that maps to **no** reserved variable. Rename the field, or
  override it with `--placeholder-map mizan_field=stax_var`.
- two distinct fields that map to the **same** reserved variable (an alias
  collision).

## How to run the export

The command takes one metric by id and writes JSON to stdout, or to a file with
`--out`:

```sh
# Fan out a rubric metric to one Stax evaluator per criterion (the default).
$ mizan export stax --metric acme/rubric-brand --model-id gemini-2.5-pro --out evaluators.json

# A pointwise metric, printed to stdout.
$ mizan export stax --metric acme/pointwise-quality --model-id gemini-2.5-pro

# Flatten a rubric to a single aggregate evaluator (lossy, opt-in).
$ mizan export stax --metric acme/rubric-brand --model-id gemini-2.5-pro --flatten
```

Key flags:

| Flag | Meaning |
|---|---|
| `--metric <id>` | the local template to export (required) |
| `--model-id <id>` | the Stax-side model id to bind (see below; supply it) |
| `--out <file>` | write JSON to a file instead of stdout |
| `--flatten` | rubric only: emit one aggregate evaluator (Option A) |
| `--placeholder-map a=b` | override the rename map for a non-conventional field |

`export stax` reads only the local registry. It never calls Vertex or Gemini,
opens no network connection, and never reads, emits, or migrates any API key.
Key migration is out of scope by design.

## Worked examples (real DTO bytes)

The JSON below is the real Stax `LLMEvaluatorRequestDTO` wire format. The bytes
(apart from the `model_id` you pass) are locked by golden tests in
`cmd/mizan/export_golden_test.go`.

### Pointwise (one evaluator)

Given a pointwise template whose input `{{response}}` renames to `{{output}}`, a
`Be strict.` system instruction, and a `RatingRubric` anchoring bands 1 and 5,
`mizan export stax --metric acme/pointwise-quality --model-id gemini-2.5-pro`
emits a single object:

```json
{
  "name": "acme/pointwise-quality",
  "output_format_type": "Choices",
  "variables": [
    { "name": "output", "required": true }
  ],
  "model_id": "gemini-2.5-pro",
  "prompts": [
    { "role": "SYSTEM", "text": "Be strict." },
    { "role": "USER", "text": "Rate the response: {{output}}" }
  ],
  "output_categories": [
    { "name": "1-poor", "value": "1" },
    { "name": "score-2", "value": "2" },
    { "name": "score-3", "value": "3" },
    { "name": "score-4", "value": "4" },
    { "name": "5-great", "value": "5" }
  ]
}
```

### Rubric fan-out (an array, one evaluator per criterion)

A rubric with groups `clarity: [clear, concise]` and `tone: [on-brand]` yields
three per-criterion evaluators. Because Stax has **no batch-create endpoint**,
the array is a Mizan bundling convention: each element is a complete, standalone
create body. The `clarity` group anchors bands 1 and 5, so those names carry into
both `clarity::*` evaluators; `tone` has no `RatingRubric`, so its bands are
generic.

```json
[
  {
    "name": "acme/rubric-brand::clarity::clear",
    "output_format_type": "Choices",
    "variables": [
      { "name": "output", "required": true }
    ],
    "model_id": "gemini-2.5-pro",
    "prompts": [
      { "role": "USER", "text": "Evaluate {{output}} on the criterion \"clear\" (rubric group: clarity). Return ONE category." }
    ],
    "output_categories": [
      { "name": "1-poor", "value": "1" },
      { "name": "score-2", "value": "2" },
      { "name": "score-3", "value": "3" },
      { "name": "score-4", "value": "4" },
      { "name": "5-great", "value": "5" }
    ]
  },
  {
    "name": "acme/rubric-brand::clarity::concise",
    "output_format_type": "Choices",
    "variables": [
      { "name": "output", "required": true }
    ],
    "model_id": "gemini-2.5-pro",
    "prompts": [
      { "role": "USER", "text": "Evaluate {{output}} on the criterion \"concise\" (rubric group: clarity). Return ONE category." }
    ],
    "output_categories": [
      { "name": "1-poor", "value": "1" },
      { "name": "score-2", "value": "2" },
      { "name": "score-3", "value": "3" },
      { "name": "score-4", "value": "4" },
      { "name": "5-great", "value": "5" }
    ]
  },
  {
    "name": "acme/rubric-brand::tone::on-brand",
    "output_format_type": "Choices",
    "variables": [
      { "name": "output", "required": true }
    ],
    "model_id": "gemini-2.5-pro",
    "prompts": [
      { "role": "USER", "text": "Evaluate {{output}} on the criterion \"on-brand\" (rubric group: tone). Return ONE category." }
    ],
    "output_categories": [
      { "name": "score-1", "value": "1" },
      { "name": "score-2", "value": "2" },
      { "name": "score-3", "value": "3" },
      { "name": "score-4", "value": "4" },
      { "name": "score-5", "value": "5" }
    ]
  }
]
```

## What a consumer does with the artifact

The output is create-request JSON, not a batch document. A Stax consumer creates
each evaluator with one HTTP call:

- **A single object** (pointwise, or rubric with `--flatten`): `POST` the object
  once to Stax's create-evaluator endpoint. That creates one evaluator.
- **An array** (rubric fan-out): iterate the array and `POST` **each element
  separately**. Stax's `POST /` creates one evaluator from one
  `LLMEvaluatorRequestDTO`; there is no batch-create endpoint, so an N-element
  array becomes N create calls. The `::group::criterion` names keep the resulting
  evaluators grouped and traceable back to the one Mizan template.

Fill in `--model-id` before you POST (see below). Once created, the evaluators
run under Stax's own categorical scoring, one verdict each.

## Fidelity and known limitations

The mapping is honest about where Mizan and Stax do not line up. None of these
are bugs; they are the seams between a per-criterion rubric model and a
single-category one.

- **`model_id` is a user-supplied deployment binding, never migrated.** Mizan
  does not carry model or credential bindings across systems (design invariant
  N3). A template's `AutoraterModel` is a Vertex/ADC binding, not a Stax model
  id, so the exporter never derives `model_id` from it. You bind the Stax-side
  model yourself with `--model-id`, and you should always supply it before you
  POST.
- **The exporter fails closed on `model_id`: when unset, the field is omitted so
  Stax rejects cleanly.** Stax marks `model_id` `@NotNull` but not `@NotBlank`, so
  an empty string would slip past validation and produce an evaluator that is
  accepted yet unusable. To avoid that, when you do not pass `--model-id` the
  exporter drops the `model_id` field entirely (rather than emitting `""`) and
  prints a warning to stderr. A create request with no `model_id` fails Stax's
  `@NotNull` check with a clean 400, so an unfilled artifact can never be silently
  ingested. Always supply `--model-id` so the field is present; the examples on
  this page all do.
- **`pairwise`, `custom_schema`, and non-text modalities fail closed.** They have
  no faithful Stax target, so the export fails with a clear `unsupported in v1`
  error rather than emitting a lossy guess.
- **The overall-score rollup does not survive.** Mizan's rubric path produces a
  single `overall_score` alongside the per-criterion detail. Stax has no
  evaluator-level rollup field (aggregation across evaluators is a Stax UI and
  analytics concern), so the fan-out preserves the per-criterion scores but not
  the rollup. The rollup is reconstructable on the Stax side from the individual
  evaluator scores.
- **Interior Likert band descriptions are generic.** `RatingRubric` descriptions
  are keyed per group, and typically only the anchor bands (1 and 5) carry a
  description. Criterion-pairs in the same group therefore share band names, and
  the interior bands (2 to 4) fall back to `score-N`. This is cosmetic and
  reconstructable from the source template.

For the full mapping rationale, the fidelity spike, and the go/no-go analysis,
see [`mizan-stax-export-spec.md`](../design/mizan-stax-export-spec.md). For the
command reference and every flag, see
[the user guide's export section](user-guide.md#export-to-stax-export-stax).
