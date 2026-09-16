# Mizan → Stax Evaluator-Export Spec (L1)

Author: `dev-l1` · Date: 2026-09-16 · **INTERCHANGE SPEC — deliverable of the Stax-inspired build phase (L1).**
Design of record: `design/stax-build-phase-design.md` §4.D, §6 Phase 5, §9 "L1 — export spec".
Upstream analysis: `design/stax-analysis.md` §1.2 (Stax eval model), §4b-L1 (leverage play), OQ2 (fidelity risk).
Ground truth (Mizan): `github.com/ghchinoy/mizan@main` — struct shapes cited below inspected at clone time.
Ground truth (Stax): carried from `design/stax-analysis.md` (`google-labs-code/stax@b3d47c8`).

> **Scope.** This document specifies a **one-directional Mizan→Stax evaluator export**: the mapping,
> the placeholder rename map, the two fidelity options, and a worked example. It does **not** build the
> exporter CLI — that is gated on OQ-C and dispatched separately after the fidelity finding
> (§7). A Stax-side importer is Stax's codebase and is out of scope.

---

## 1. Why this exists

Both Mizan and Stax are Apache-2.0, so an interchange schema between them is legally unencumbered
(`stax-analysis.md` §5). The play is strategic, not a Mizan feature: it positions Mizan as the
**git-native, PR-reviewed, credential-free-validated authoring + CI-gate front-end** feeding a Stax-style
evaluator library — exactly the "library first + integration spec for a future service" the owner chose
for CUJ3. The only real risk is **semantic fidelity** (analysis OQ2): Mizan's per-criterion Likert
rubric does not round-trip losslessly into Stax's single-category model. This spec resolves that with two
options and a de-risking spike (§6).

**Attribution nuance (Apache-2.0 / NOTICE).** Concept interop is unrestricted. If the exporter ever
lifts Stax *text or code* (e.g. copies a seed evaluator's prompt verbatim), retain the Apache-2.0
license header and add the source to `NOTICE`. An interchange *format* we author from Mizan's own model
carries no such obligation. Stax carries Google copyright under `google-labs-code`; Mizan is not an
official Google project — keep provenance clear but neither blocks reuse.

---

## 2. The two eval models, side by side

**Stax `LLMEvaluator` (target).** A Stax evaluator is a **prompt template** — one or more `Prompt`s
(`llmproviders/dto/Prompt` = `{role, text}`) carrying `{{variable}}` substitutions — plus a set of named
**`output_categories`** mapped to numeric values. At run time the judge emits **one** category name plus reasoning; a regex extracts the category,
which maps to its numeric score (no match → `NaN`) (`stax-analysis.md` §1.2,
`EvaluationServiceImpl.runPointwiseLLMEval:354-397`). This is **categorical / choice-based scoring — a
single verdict per evaluator**, not per-criterion decomposition. Multiple evaluators can live under one
`EvaluationContainer` (a Stax `Project` or `DataSet`), which is what makes fan-out (Option B) viable.

Stax reserved template variables: `prompt`, `output`, `expected_output`, `history`,
`system.instruction`, and `.a`/`.b` suffixes for pairwise SxS (`stax-analysis.md` §1.2).

**Mizan `MetricTemplate` (source).** Relevant fields (`internal/registry/model.go`):

- `Kind` ∈ {`pointwise`, `pairwise`, `rubric`, `custom_schema`}
- `MetricPromptTemplate string` — `{{placeholder}}` body
- `SystemInstruction string`
- `Inputs []InputSpec` — declared placeholders + modality
- `RubricGroups map[string][]string` — group → ordered criteria (rubric only)
- `RatingRubric map[string]map[string]string` — group → {band → description}, e.g. `{"1":"poor","5":"great"}`
- `RubricDetail.Scale {Min,Max}` — the Likert range the per-criterion path scores on (default 1–5)
- `CandidateFieldName` / `BaselineFieldName` — pairwise only
- `ResponseSchema *Schema` — custom_schema only
- `AutoraterModel string` — Vertex/Gemini publisher-relative id

Mizan's rubric path emits, per run, a structured object
`{per_criterion:[{group, criterion, score(int), rationale}], overall_score, explanation}`
(`internal/eval/rubric_structured.go:80-130`) — i.e. **one score + rationale per (group, criterion)
pair**, plus an overall rollup.

---

## 3. Mapping table (Mizan → Stax `LLMEvaluatorRequestDTO`)

The exporter emits the **real Stax create-evaluator request DTO** — the exact JSON body a Stax consumer
`POST`s to `LLMEvaluatorController.createLLMEvaluator` (`google-labs-code/stax`,
`domain/evaluator/llm/LLMEvaluatorController.java`). The target types are
`LLMEvaluatorRequestDTO extends BaseLLMEvaluatorRequestDTO` plus `llmproviders/dto/Prompt`,
`evaluator/dto/OutputCategoryDTO`, and `evaluator/dto/EvaluatorVariableDTO`. Confirmed field shapes are
in §3.1 below.

| Mizan | Stax DTO field | Fidelity |
|---|---|---|
| template `ID` (+ `::group::criterion` on fan-out) | `name` (String) | **direct** |
| — (fixed) | `output_format_type` = `"Choices"` (`ScoreType` enum, by name) | **synthesized** — categorical scoring is the only v1 target |
| declared `{{var}}`s used in prompts | `variables` (`List<EvaluatorVariableDTO>` = `{name, required}`) | **derived** — distinct post-rename vars, each `required:true` |
| `AutoraterModel` (Vertex/Gemini) | `model_id` (String, `@NotNull`) | **NOT auto-mapped** — user supplies via `--model-id`; Mizan never migrates model/key bindings (design N3). See §6.3 |
| `MetricPromptTemplate` | a `Prompt` with `role: "USER"`, `text` (String) | **direct** — subject to the placeholder rename map (§4) |
| `SystemInstruction` | a `Prompt` with `role: "SYSTEM"`, `text` (String) | **direct** — omitted when empty |
| input placeholders (`{{response}}`, …) | `{{output}}` / `{{prompt}}` / `{{expected_output}}` inside `Prompt.text` | **rename map required** (§4) — Mizan field names ≠ Stax reserved vars |
| `pointwise` + `RatingRubric` (band→description) | `output_categories` (`List<OutputCategoryDTO>` = `{name, value:String}`) | **direct-ish** — Likert bands become categories, one evaluator |
| `rubric` (`RubricGroups`, per-criterion Likert) | `output_categories` (single categorical per evaluator) | **LOSSY — see §5 options A/B** |
| `pairwise` (`CandidateFieldName`/`BaselineFieldName`, flip/sampling) | (pairwise SxS) | **out of L1 v1** — different entity shape; Mizan's flip+sampling bias controls have no Stax equivalent |
| `custom_schema` (`ResponseSchema`) | — | **out of L1 v1** — no Stax categorical analogue for arbitrary JSON schema |
| `Modalities` (text/image/audio/video/music) | text/chat (Stax is text-centric) | **note** — non-text modalities have no faithful Stax target; export text-modality templates in v1 |

**In v1 the exporter handles `pointwise` and `rubric` (text modality).** `pairwise`, `custom_schema`, and
non-text modalities are explicitly deferred and must fail the export with a clear "unsupported kind /
modality" message rather than emit a lossy guess.

### 3.1 Confirmed real Stax DTO schema (ground truth)

Established from `google-labs-code/stax` (package `com.planck.planck`) before writing emit code. Jackson
serializes enums **by name** and fields in **declaration order**; `@JsonInclude(NON_NULL)` drops null
optionals.

- **`BaseLLMEvaluatorRequestDTO`** (`domain/evaluator/dto/`): `name` (String), `output_format_type`
  (`ScoreType`), `description` (String, optional), `variables` (`List<EvaluatorVariableDTO>`, optional),
  `model_id` (String, `@NotNull`), `prompts` (`List<Prompt>`, `@NotEmpty`).
- **`LLMEvaluatorRequestDTO`** (`domain/evaluator/llm/dto/`): adds `output_categories`
  (`List<OutputCategoryDTO>`).
- **`Prompt`** (`llmproviders/dto/`): `role` (`InputRole`), `text` (String) — the body field is **`text`**,
  not `content`.
- **`OutputCategoryDTO`** (`domain/evaluator/dto/`): `name` (String), `color` (optional), `color_name`
  (optional), `value` (**String**, `@NumberRange`), `start_range`/`end_range` (optional). The exporter
  emits only `name` + `value` (others are `NON_NULL`-omitted).
- **`EvaluatorVariableDTO`** (`domain/evaluator/dto/`): `name` (String), `required` (boolean).
- **`InputRole`** enum: `USER, ASSISTANT, SYSTEM, DEVELOPER, TOOL, FUNCTION` — **UPPERCASE**, no
  `@JsonValue` (serialized by name).
- **`ScoreType`** enum: `Json, String, Double, Integer, Choices, Boolean, HUMAN_BINARY` — serialized by
  name; the exporter uses `Choices`.

**No batch-create endpoint exists**: `POST /` creates one evaluator from one `LLMEvaluatorRequestDTO`. The
exporter's array output (rubric fan-out) is a Mizan bundling convention in which **each array element is a
valid standalone create body** — a consumer POSTs each element.

---

## 4. Placeholder rename map

Mizan authors name their own input fields (e.g. `response`, `question`, `reference`); Stax uses a fixed
set of reserved template variables. The exporter rewrites `{{mizan_field}}` → `{{stax_var}}` in the
prompt body and drops the field from any category/prompt that Stax supplies implicitly.

| Mizan input field (convention) | Stax reserved var | Notes |
|---|---|---|
| `response`, `output`, `answer`, `candidate` | `{{output}}` | the thing being judged |
| `prompt`, `question`, `input`, `instruction` | `{{prompt}}` | the user/task prompt |
| `reference`, `expected`, `ground_truth`, `gold` | `{{expected_output}}` | reference answer |
| `history`, `conversation`, `context` | `{{history}}` | prior turns |
| (system instruction, from `SystemInstruction`) | `{{system.instruction}}` | mapped from the field, not a placeholder |
| `candidate` / `baseline` (pairwise) | `{{output.a}}` / `{{output.b}}` | pairwise only — out of v1 |

**Rules.**
1. Matching is **case-insensitive on the field name**, longest/most-specific alias first
   (`ground_truth` before `truth`).
2. A Mizan field that matches **no** reserved var is a **hard error** in v1 (the export cannot promise
   the judge will receive it) — the exporter lists the unmapped field and the allowed targets. A future
   version may pass unknown fields through as literal `{{var}}` if Stax accepts arbitrary `Prompt`
   variables, but v1 does not guess.
3. **Alias collision is a hard error** (symmetric with rule 2). If two or more declared Mizan inputs
   used in the template would map to the **same** Stax reserved var — e.g. both `output` and `answer`
   present → `{{output}}`, or `expected` and `ground_truth` → `{{expected_output}}` — the export fails in
   v1, naming the colliding source fields and their shared target. Silently merging them would send two
   distinct Mizan inputs to one Stax variable and corrupt the judge prompt. The author resolves it by
   removing/renaming one field or supplying an override (rule 4) that redirects one to a different var.
4. The map is **configurable** (an override table) so a template using a non-conventional field name can
   be exported without renaming the source template.
5. Exact placeholder set is validated against the template's declared `Inputs` (`model.go` `InputSpec`)
   — every `{{var}}` in the body must be a declared input, mirroring Mizan's own `validatePlaceholders`.
   (Implementation note: the exporter reads templates that the registry already validated on write, so it
   relies on that upstream `validatePlaceholders` guarantee rather than re-deriving it — the exporter's own
   hard errors are the *rename* concerns of rules 2–3, which the registry does not check.)

---

## 5. The fidelity decision (analysis OQ2)

Mizan's `rubric` kind scores **each (group, criterion) pair** on a Likert scale and returns a rationale
per pair plus an overall rollup. Stax's `LLMEvaluator` emits **one** category → **one** score. The two
do not round-trip losslessly. Two options, both specified; the exporter defaults to B and offers A as an
explicit opt-in.

**Category-name derivation rule (both options).** For a Likert scale `[min,max]`, every integer band in
range becomes one `output_category` mapped to its own numeric value. The category **name** is derived
deterministically from the band's `RatingRubric` description:

- **Anchored band** (the band has a non-empty description in the relevant `RatingRubric` group):
  name = `"{band}-{desc}"`, e.g. `1-poor`, `5-great`.
- **Un-anchored / interior band** (no description — typically the interior 2..(max-1)): name =
  `"score-{band}"`, e.g. `score-2`, `score-3`, `score-4`.

This is a **rule, not just the example's shape**: it applies to every band of every evaluator the
exporter emits, so category names are stable and reproducible across templates. (In Option A, where no
single group's descriptions apply, all bands use the `score-{band}` form.)

### Option A — Flatten (one evaluator)

Emit **one** Stax evaluator whose prompt embeds **all** criteria and whose `output_categories` are the
aggregate Likert bands (`score-1`…`score-N`, or the overall RatingRubric bands if the template supplies
group-agnostic ones).

- **Cardinality:** 1 Mizan template → 1 Stax evaluator (matches a hand-authored Stax evaluator).
- **Loss:** per-criterion scores collapse to a single overall; per-criterion rationales are lost;
  per-group band descriptions are dropped (they conflict across groups when flattened).
- **When to use:** the consumer only wants a single headline score and accepts losing the differentiator.
- **Requirement:** the exporter **must emit a warning** listing the dropped per-criterion granularity.

### Option B — Fan-out (one evaluator per criterion) — **RECOMMENDED DEFAULT**

Emit **one** Stax evaluator **per (group, criterion) pair**, each with that criterion's Likert bands as
`output_categories`. Because Stax's `EvaluationContainer` holds many evaluators, the M evaluators live
side by side and Stax's workbook renders one column per evaluator — the natural analogue of Mizan's
per-criterion scorecard.

- **Cardinality:** 1 Mizan template with M criterion-pairs → **M** Stax evaluators. Grouping is carried
  in a **name convention** `"{template-id}::{group}::{criterion}"` so the M evaluators are traceable back
  to one Mizan template (and re-importable).
- **Fidelity:** per-criterion **scores preserved** (1 evaluator == 1 criterion); per-criterion
  **rationales preserved** (Stax judge reasoning is per-evaluator).
- **Residual loss (minor):** (1) Mizan's single `overall_score` rollup is not reconstructed on the Stax
  side (Stax aggregation is a UI/analytics concern, not an evaluator field); (2) `RatingRubric` band
  descriptions are keyed **per group**, not per criterion, so criterion-pairs in the same group share
  band names, and only the **anchored** bands (typically 1 and 5) carry descriptions — the interior
  bands (2–4) fall back to generic `score-N`. Both are cosmetic and reconstructable from the source.
- **This preserves Mizan's load-bearing differentiator** (per-criterion + adaptive rubrics), honoring
  non-goal N5 ("do not regress the per-criterion rubric model to Stax's category-map").

**Recommendation.** Default to **Option B for `rubric`** and **direct map for `pointwise`**; make
Option A (flatten) an explicit `--flatten` opt-in. This is validated by the spike (§6): Option B is a
strict information *superset* of the hand-built Stax evaluator and is round-trippable for per-criterion
scores + rationales.

---

## 6. Worked examples

### 6.1 Rubric (`kind: rubric`)

Source template (real Mizan golden fixture,
`internal/registry/testdata/golden/templates/rubric-brand.yaml`):

```yaml
apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: acme/rubric-brand
  name: Rubric Brand
  version: 1.2.3
spec:
  kind: rubric
  modalities: [text]
  rubricGroups:
    clarity: [clear, concise]
    tone:    [on-brand]
  ratingRubric:
    clarity: { "1": poor, "5": great }
  rubricDetail:
    scale: { min: 1, max: 5 }
```

This template yields **3 per-criterion scoring units**: `(clarity, clear)`, `(clarity, concise)`,
`(tone, on-brand)`, each scored on `[1,5]`.

Exported with `--model-id model-123`. All examples below are the **real Stax
`LLMEvaluatorRequestDTO`** wire bytes (§3.1), byte-locked by `cmd/mizan/export_golden_test.go`.

### Option A output (flatten) — 1 evaluator

```json
{
  "name": "acme/rubric-brand (flattened)",
  "output_format_type": "Choices",
  "variables": [
    { "name": "output", "required": true }
  ],
  "model_id": "model-123",
  "prompts": [
    {
      "role": "USER",
      "text": "Evaluate {{output}} against the following rubric and return ONE overall category.\n- [clarity] clear\n- [clarity] concise\n- [tone] on-brand\n"
    }
  ],
  "output_categories": [
    { "name": "score-1", "value": "1" },
    { "name": "score-2", "value": "2" },
    { "name": "score-3", "value": "3" },
    { "name": "score-4", "value": "4" },
    { "name": "score-5", "value": "5" }
  ]
}
```

Per-criterion detail and the `1:poor / 5:great` band descriptions are **lost** (a warning is printed).

### Option B output (fan-out) — 3 evaluators — **DEFAULT**

Each array element is a valid standalone `LLMEvaluatorRequestDTO` (no batch endpoint exists — POST each):

```json
[
  {
    "name": "acme/rubric-brand::clarity::clear",
    "output_format_type": "Choices",
    "variables": [
      { "name": "output", "required": true }
    ],
    "model_id": "model-123",
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
    "model_id": "model-123",
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
    "model_id": "model-123",
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

Every Mizan per-criterion score maps to exactly one Stax evaluator; the `clarity` group's `1:poor /
5:great` anchors carry into both `clarity::*` evaluators; `tone` has no RatingRubric so its bands are
generic. The `::`-delimited name convention lets a re-import reconstruct the single `acme/rubric-brand`
template with its two groups.

### 6.2 Pointwise (`kind: pointwise`)

`pointwise` is the **direct map** (§3): one Mizan template → one Stax evaluator, no flatten/fan-out
choice. The `MetricPromptTemplate` becomes a `USER` `Prompt` (placeholders renamed per §4), and the
`RatingRubric` band→description map becomes `output_categories` via the §5 category-name derivation rule.
When a template declares no `RatingRubric`, the bands fall back to `score-{band}` across the
`rubricDetail.scale` range (default `[1,5]`).

Source template (real Mizan golden fixture,
`internal/registry/testdata/golden/templates/pointwise-quality.yaml`, RatingRubric added inline to show
band derivation):

```yaml
apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: acme/pointwise-quality
  name: Pointwise Quality
  version: 1.0.0
spec:
  kind: pointwise
  modalities: [text]
  inputs:
    - { name: response, modality: text, required: true }
  metricPromptTemplate: 'Rate the response: {{response}}'
  systemInstruction: Be strict.
  ratingRubric:
    default: { "1": poor, "5": great }
  rubricDetail:
    scale: { min: 1, max: 5 }
```

Export (1 evaluator): `{{response}}` renames to `{{output}}` (§4); `systemInstruction` becomes a `SYSTEM`
`Prompt` (emitted first, before the `USER` prompt); the `default` band anchors 1/5 name the anchored
categories, interior bands use `score-{band}`:

```json
{
  "name": "acme/pointwise-quality",
  "output_format_type": "Choices",
  "variables": [
    { "name": "output", "required": true }
  ],
  "model_id": "model-123",
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

**System-instruction wire shape (resolved against Stax ground truth).** The `systemInstruction` is emitted
as a `Prompt` in the `prompts` list with `role: "SYSTEM"` and body field **`text`** — the same shape as
the `USER` prompt — **not** a bespoke `{ "instruction": … }` object and **not** a `content` field. This
matches Stax's `llmproviders/dto/Prompt` (`role` is the `InputRole` enum, UPPERCASE by name; body is
`text`), gathered into `BaseLLMEvaluatorRequestDTO.prompts` (`List<Prompt>`, `@NotEmpty`). The SYSTEM
prompt is omitted entirely when the template has no `systemInstruction`. Fields are emitted in DTO
declaration order — `name`, `output_format_type`, `variables`, `model_id`, `prompts`,
`output_categories` — and `output_categories` (a `List<OutputCategoryDTO>`) is ordered by ascending
numeric value. These bytes are locked by golden tests in `cmd/mizan/export_golden_test.go`.

If the template omitted `ratingRubric`, the categories would be `[{"name":"score-1","value":"1"}, …,
{"name":"score-5","value":"5"}]`. No per-criterion decomposition is involved, so pointwise export is
lossless within the mapping.

### 6.3 model_id and the N3 no-migration invariant

Stax's `model_id` is `@NotNull`, but design invariant N3 forbids Mizan from migrating model or credential
bindings. The exporter resolves this by treating `model_id` as a **user-supplied deployment binding**, not
a fidelity field: it never derives `model_id` from a template's `AutoraterModel`. Supply it with
`--model-id <stax-model-id>`. If omitted, `model_id` is emitted as `""` and the exporter prints a warning
so the gap is explicit and the output is not silently un-ingestible.

---

## 7. Fidelity finding (OQ2) and go/no-go (OQ-C)

A throwaway spike (design §4.D deliverable 2, **not merged** — run outside the mizan module) exported
`rubric-brand.yaml` both ways and diffed each against a hand-built Stax evaluator. Full finding:
`state/l1-fidelity-spike-finding.md`. Summary:

- **Option A** — lossy, **not** round-trippable (per-criterion detail unrecoverable).
- **Option B** — round-trippable for per-criterion **scores + rationales**; only the overall-score
  rollup and interior band **descriptions** are residual (cosmetic, reconstructable). Option B is a
  strict information superset of a hand-authored Stax evaluator.
- **OQ2 verdict:** fidelity is **achievable**, not impossible. **OQ-C recommendation: GO** — commit a
  Mizan→Stax exporter, default **Option B for rubric**, `--flatten` (Option A) as an explicit opt-in,
  direct map for pointwise, and fail-closed on pairwise/custom_schema/non-text in v1.

---

## 8. What this spec does NOT cover

- **The exporter CLI** — gated on OQ-C; dispatched as a follow-up after the finding above.
- **A Stax-side importer** — that is Stax's codebase.
- **`pairwise` / `custom_schema` export** and **non-text modalities** — deferred; v1 fails closed.
- **Key/credential migration** — never; Mizan is ADC-only, no key custody (N3).
