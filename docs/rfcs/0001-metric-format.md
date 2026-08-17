# RFC-0001: Mizan Metric Format — Metric Definition, Eval/AutoRater Config, and Eval Results

- **Status:** Proposed (v1, seed for a community RFC) — **all owner decisions confirmed 2026-08-11** (§11); ready to commit as `mizan/docs/rfcs/0001-metric-format.md`. Advances to **Accepted** on merge + schema landing (§10.3).
- **Author:** `mizan-rfc-architect` (architect) · **Date:** 2026-08-11
- **Owner / approver:** ghchinoy@google.com
- **Supersedes:** nothing. **Concretizes:** `design/eval-metric-format-standards-research.md` §3–4 (the align+adapt recommendation).
- **Ground truth:** mizan `github.com/ghchinoy/mizan` @ `ce57ea2` (read-only clone, 2026-08-11); `mizan-templates` @ `5c140a1`; Vertex `cloud.google.com/go/aiplatform v1.126.0` `apiv1beta1/aiplatformpb` (module cache, re-verified 2026-08-11); `google.golang.org/genai v1.67.0`.
- **Scope discipline:** this is a *format* specification and its evolution process. It specifies real schemas and contracts a developer implements against directly. It does **not** implement code, and it does **not** design the results-store *engine* (that is separate, already-researched work — `design/eval-results-store-research.md`); it defines the results-side *data shape* the engine must persist.

> **How to read this document.** This RFC is meant to be mizan's *format touchstone* — the artifact a contributor consults to author a template, an implementer consults to build the codec/validator, and the community amends over time. Sections 3–8 are the normative format. Section 9 (repo placement), Section 10 (RFC process), and Section 11 (decisions) captured owner decisions — **all confirmed 2026-08-11** (§11) — before anything is committed to a repo. Section 12 is advisory sizing only — no implementation is authorized by this document.

---

## 1. Summary

Mizan owns a declarative YAML format for defining LLM-as-a-Judge **metric templates** (`internal/registry/model.go:94`), materialized at eval time into Vertex AI protos (`internal/eval/*`). The upstream survey (`eval-metric-format-standards-research.md`) established that **no vendor-neutral standard exists**, that the field has **converged on a common minimal shape**, and that mizan's format is already best-in-class on identity/provenance/versioning. Its recommendation — **keep mizan's format, ALIGN it to the convergent vocabulary, offer targeted ADAPTERS (promptfoo + Vertex), do not adopt/embed an external spec** — is the posture this RFC concretizes.

This RFC does three related-but-distinct things in one document, as the owner directed:

1. **Metric definition** (the *template*): the `MetricTemplate` YAML, aligned field-by-field to Vertex's SDK object model, with **Rubric Groups adopted** and reconciled with mizan's shipped per-criterion Likert extension.
2. **Eval / AutoRater config** (how an eval runs): mizan's autorater block **mirrors Vertex's `AutoraterConfig` proto** (`sampling_count`, `flip_enabled`, `autorater_model`) 1:1, with an explicit, documented treatment of the native path vs. the genai/global path.
3. **Eval results shape** (how results are captured): the *data shape* of a result record — run metadata + template ref/version + autorater config + inputs + score + rationale — that ties the template format to the (separate) results-store capability and makes **provenance** end-to-end.

Plus the owner-mandated cross-cutting pieces: **soft/free tag vocabulary** (folksonomy), a **provenance** section connecting template identity → git pack → result, a **concrete promptfoo interop adapter** field mapping, a **repo-placement** discussion + recommendation, and an **RFC-process** section defining how the format evolves.

**One correction to a stated directive, evidence-first:** the brief's directive 5 lists AutoRater config as "autorater model, sampling count, **location**." The Vertex `AutoraterConfig` proto (`apiv1beta1/aiplatformpb/evaluation_service.pb.go:1170-1194`) has exactly three fields — `sampling_count` (field 1), `flip_enabled` (field 2), `autorater_model` (field 3) — and **no location field**. Location is a property of the *request* (`EvaluateInstancesRequest.Location`, `.pb.go:1248+`; mizan sets it at `internal/eval/native.go:96`), not of the autorater config. This RFC therefore mirrors the three real `AutoraterConfig` fields and treats location as an engine/routing concern (§5.3), not a template field.

---

## 2. Motivation & Scope

### 2.1 Why a format RFC now

The owner's intent (verbatim, 2026-08-11): *"For metric format — eval definition & eval results — let's create a RFC / proposal document … that clearly defines how we use / reuse the Vertex AI format and continue to define this — we can leave this open to the community as a RFC … Finalize this document as our format touchstone that we can commit to the repo and refer to."*

The format is currently specified across code (`internal/registry/model.go`), a human doc (`mizan-templates/docs/pack-format.md`, 165 lines), and P2 design (`p2-template-packs-design.md` §3.2–3.6). There is no single normative touchstone that (a) states what mizan adopts from Vertex and why, (b) fixes the scoring/rubric/tag decisions the survey left as forks, and (c) defines how the format evolves as a community artifact. This RFC is that touchstone.

### 2.2 The three things this RFC defines (owner directive 1)

```
   (a) METRIC DEFINITION            (b) EVAL / AUTORATER CONFIG        (c) EVAL RESULTS SHAPE
   kind: MetricTemplate             autorater: {model,                 result record:
     identity + rubric/prompt         samplingCount, flipEnabled}        run meta + template ref/ver
     + scoring semantics            (native vs genai/global path)        + autorater + inputs
        │                                    │                            + score + rationale
        └──────────── produces ─────────────┴──────────── captured as ──────────┘
                                    PROVENANCE threads all three (§7)
```

- **(a)** is the template: what is stored (`MetricTemplate`), authored (pack YAML), and shared (P2 packs).
- **(b)** is how a template is executed: the autorater/judge configuration, mirroring Vertex.
- **(c)** is what a run leaves behind: the *shape* of a persisted result (the results-store *engine* is separate work; this RFC fixes only the record shape so template↔result provenance is well-defined).

### 2.3 In scope

Format and contracts for (a)/(b)/(c); Vertex alignment; rubric-group adoption; tag policy; provenance model; promptfoo adapter field mapping; repo placement recommendation; RFC-evolution process.

### 2.4 Non-Goals

- **The results-store engine.** Persistence backend, `ResultStore` seam, query/compare CLI — all belong to the results-store capability (`eval-results-store-research.md` §4). This RFC defines only the **result record shape** (§6) so the two connect; it does not schedule or stub the store.
- **The eval-set runtime.** Runner/aggregation/curation (CUJ5/CUJ6-run) are a distinct capability (`p2-template-packs-design.md` §3.4a, §3.10). The `EvalSet` manifest *format* is already frozen in P2; this RFC references it (§7.3) but does not redefine or build it.
- **The tag-filter implementation.** Adding `Tags` to `ListFilter` is a separately-scoped item (§6 gap; `cuj-capability-map-v2.md` §2.4). This RFC fixes the *tag policy*, not the filter code.
- **A JSON-LD / RDF interop layer** (Croissant/schema.org). Over-heavy for a tag folksonomy (survey §1.14); not pursued.
- **Cryptographic signing** of templates. Git history + PR review is the v1 trust model (P2 §8 D5).
- **Adopting/embedding an external schema** as authoritative. Rejected by the survey (§3.1); mizan's format stays authoritative, interop is via adapters.

---

## 3. Vertex Alignment — What Mizan Adopts As-Is

**Posture (owner directive 3):** *align + adapt, do not adopt-wholesale, do not reinvent.* mizan keeps its own YAML format as authoritative and tracks Vertex's object model closely, because mizan **materializes into Vertex protos at eval time** — alignment is not aspirational, it is the substrate.

### 3.1 The object-model correspondence (re-verified against the SDK)

mizan's `MetricTemplate` (`internal/registry/model.go:94-127`) already mirrors Vertex's metric object model. Re-verified field mapping:

| Mizan field (`model.go`) | Vertex SDK counterpart | Evidence (aiplatform v1.126.0 apiv1beta1) |
|---|---|---|
| `MetricPromptTemplate` (`:109`) | `PointwiseMetricSpec.metric_prompt_template` / `PairwiseMetricSpec` / `LLMBasedMetricSpec.metric_prompt_template` | `evaluation_service.pb.go:7059` (field 1); `:2715` (field 1) |
| `SystemInstruction` (`:110`) | `*.system_instruction` | `:7061` (PointwiseMetricSpec field 2); `:2717` (LLMBasedMetricSpec field 2) |
| `Kind` pointwise/pairwise (`:106`) | `PointwiseMetricSpec` / `PairwiseMetricSpec` request oneof arms | `EvaluateInstancesRequest.MetricInputs` oneof, `:1248+` |
| `CandidateFieldName`/`BaselineFieldName` (`:111-112`) | `PairwiseMetricSpec.candidate_response_field_name`/`baseline_response_field_name` | referenced by `AutoraterConfig.flip_enabled` doc, `.pb.go:1178-1180` |
| `ResponseSchema *Schema` (`:114`) | `PointwiseMetricSpec.custom_output_format_config` (native) / genai `ResponseSchema` (global) | `:7070` (field 3, `CustomOutputFormatConfig`); genai path `internal/eval/custom.go:118-121` |
| `AutoraterModel`/`SamplingCount`/`FlipEnabled` (`:115-118`) | `AutoraterConfig.{autorater_model, sampling_count, flip_enabled}` | `:1170-1194` (fields 3/1/2) — see §5 |
| `RubricGroups map[string][]string` (`:113`) | *(no generated SDK struct; see §4)* — closest is `LLMBasedMetricSpec.rubric_group_key` | `:2713`, `:2816` (oneof `RubricGroupKey` string) |

**What mizan adopts as-is (no change needed — it already matches):** the pointwise/pairwise metric spec shape, `metric_prompt_template` + `system_instruction`, `custom_output_format_config` for structured output, and the `AutoraterConfig` triple. These are already 1:1 and this RFC ratifies them as the stable alignment surface.

### 3.2 Where mizan deliberately diverges (and why that is correct)

- **mizan is a declarative *file*; Vertex metrics are *SDK objects*.** Vertex users author metrics as Python `PointwiseMetric(...)` objects (survey §1.1); mizan's k8s-style `apiVersion/kind/metadata/spec` YAML is a git-reviewable, provenance-rich artifact Vertex does not offer. This is mizan's differentiator; keep it.
- **mizan carries identity/provenance/versioning Vertex's runtime object does not** (`id`, semver `version`, `contentHash`, `authors`, `source`). Vertex's *one* file artifact — the `AUTORATER_METRIC_SCHEMA` "AutoRater Metric Configuration" JSON Schema (survey §1.1) — has a `metadata` block (`name`, `version`, `required_inputs`, `classification` enum) and `steps[]`; mizan's identity block is a superset. Keep mizan's; offer the Vertex config file as an **export target** (§8.2), not the authoring format.
- **Scoring scale/threshold are implicit in prompt prose today** (survey §3.1 gap). This RFC's position: add scale semantics **only** where they round-trip to Vertex's structured rubric (§4.4), and keep pass/fail **threshold at the EvalSet layer** (`p2-template-packs-design.md` §3.4a reserved `aggregation.threshold`), not per-template — avoiding two sources of truth. This resolves survey open-question D-scale toward the "optional, Vertex-aligned, additive" middle (survey §4 Q3 recommendation (b)).

### 3.3 Adoption statement (normative)

> mizan's metric-definition format is authoritative and declarative. It **adopts as-is** Vertex's metric-object vocabulary for prompt/system-instruction/output-config and the `AutoraterConfig` triple, so any template round-trips to a Vertex eval call without semantic translation. It **extends** Vertex with first-class identity, versioning, provenance, and a per-criterion rubric transparency layer (§4.4) that Vertex's synchronous API does not provide. It does **not** adopt Vertex's SDK-object authoring model or make an external schema authoritative.

---

## 4. Rubric Groups — ADOPT, and reconcile with mizan's Likert extension

**Owner directive 6:** adopt Rubric Groups (do not diverge), tracking Vertex's `rubric_groups`/`RubricGroup` shape as closely as the SDK allows, while keeping mizan's `--rubric-detail` per-criterion Likert extension. Reconcile: drop the Likert extension, or keep it as a mizan superset? **Recommend, don't just ask.**

### 4.1 What "adopt Vertex's shape" can mean — the SDK constraint (re-verified)

This is the load-bearing finding, and it constrains what "adopt" is even possible:

- **Vertex's Go SDK (v1.126.0) does not generate `RubricGroup`, `Rubric`, or `EvaluationInstance` structs at all.** Verified by grep across the entire module: no `type RubricGroup struct`, `type Rubric struct`, or `type EvaluationInstance struct` exists. The only rubric-group surface is `LLMBasedMetricSpec.RubricsSource` — a oneof whose inline arm is `RubricGroupKey string` (`evaluation_service.pb.go:2713, 2816`), documented as *"Refers to a key in the rubric_groups map of EvaluationInstance"* (`:2815`). The rubric *content* travels as **data rows** in a GCS/BigQuery dataset, keyed by that string — it is not a typed struct in this SDK version.
- **Vertex's *structured rubric* shape** (the `rating_rubric: Dict[str,str]`, rating-name→description, e.g. `"5": "completely coherent"`, plus `criteria: Dict[str,str]`) lives in the **Python** SDK's `PointwiseMetricPromptTemplate` (survey §1.1), which serializes into one markdown prompt — not a Go proto struct.

**Consequence:** there is no Go-typed Vertex `RubricGroup` to bind to. "Adopt Vertex's shape" therefore means adopt the **naming and the score-band→description semantics**, not a struct import. mizan's `RubricGroups map[string][]string` (`model.go:113`) — group→criteria-list — is *structurally different* from Vertex's rating-band→description mapping, and this is the reconciliation the owner asked to resolve.

### 4.2 What mizan ships today (re-verified)

- **Authored shape:** `RubricGroups map[string][]string` (`model.go:113`) — a map of group name → ordered list of criteria strings. Pack YAML: `rubricGroups: { <group>: [ "<criterion>", ... ] }` (`mizan-templates/docs/pack-format.md:113`).
- **Native path** (`internal/eval/native.go` `runRubric`): renders the groups into a pointwise prompt → one `{score, explanation}`.
- **Structured `--rubric-detail` path** (`internal/eval/rubric_structured.go`, shipped): routes through the genai/global structured-output path, generating a deterministic `ResponseSchema` where each criterion returns `{group, criterion, score (integer), rationale}` on a **configurable Likert scale, default 1–5** (`rubric_structured.go:93-110`, `renderRubricInstruction` carries `[min,max]`, `:117-129`; `clampRubricOutput` validates/clamps, `:149`), plus `overall_score` and `explanation`. This is the decision locked in `rubric-decisions-locked.md` (configurable, default 1–5 Likert; per-criterion score + rationale).

### 4.3 Recommendation: ADOPT the naming + adopt an OPTIONAL rating-rubric, KEEP the Likert extension as a mizan superset

**Decision (normative):**

1. **Keep `rubricGroups` (group → ordered criteria list) as the authored core.** It is what both the native and structured paths consume today, it reviews well in a PR, and there is no Go-typed Vertex struct to replace it with. Adopt Vertex's **terminology** (`rubricGroups`/`rubric_groups`) — which mizan already uses — as the point of alignment.

2. **Keep mizan's per-criterion Likert extension as a mizan-specific superset**, not a divergence to be dropped. It is already shipped, owner-locked, and delivers per-criterion transparency Vertex's *synchronous* API cannot (the sync `RubricBasedInstructionFollowingSpec` is an empty message that auto-generates its own rubrics — `rubric-design-discussion-v1.md` Q1, proto `:9234-9237`; it cannot accept authored criteria). Dropping it would remove shipped value to match a shape the Go SDK doesn't even expose. **Keep it.**

3. **Add an OPTIONAL, Vertex-aligned `ratingRubric` block per group** — a rating-band→description map mirroring Vertex's Python `rating_rubric` (`"5": "completely coherent"`). It is **optional and additive**: when present it (a) makes rubric templates self-describing, (b) is the natural payload for a future async `LLMBasedMetricSpec`/`EvaluateDataset` round-trip (the only Vertex path with a first-class inline rubric metric, `rubric-design-discussion-v1.md` Q2), and (c) is the field a promptfoo/Vertex adapter maps to a score-scale. When absent, behavior is exactly today's (criteria-list only). This closes survey open-question D-rubric in the **align-when-it-buys-interop** direction without breaking the shipped authoring model.

### 4.4 Normative rubric schema (v1)

```yaml
spec:
  kind: rubric
  # REQUIRED core (unchanged, shipped): group -> ordered criteria list
  rubricGroups:
    visual-identity:
      - "Logo appears and is unobscured"
      - "Brand colors are dominant"
    tone:
      - "Voice matches the brand guideline"

  # OPTIONAL mizan superset (shipped behavior): per-criterion Likert transparency.
  # Honored on the genai/global structured path (--rubric-detail). Default scale 1-5.
  rubricDetail:                    # OPTIONAL; when omitted, native single-score behavior
    scale: { min: 1, max: 5 }      # configurable Likert (rubric-decisions-locked.md)

  # OPTIONAL Vertex-aligned rating rubric (NEW, additive): rating-band -> description,
  # per group. Mirrors Vertex Python rating_rubric. Carried + validated; consumed by
  # the async LLMBasedMetricSpec round-trip and by adapters. NOT required to run today.
  ratingRubric:
    visual-identity:
      "1": "No brand identity present or actively off-brand"
      "3": "Partial identity; logo or colors inconsistent"
      "5": "Fully on-brand; logo and colors correct and dominant"
```

**Runtime binding (per shipped code, documented so it is not surprising):**
- `rubricGroups` only, no `rubricDetail` → native pointwise, one `{score, explanation}` (`native.go` `runRubric`).
- `rubricGroups` + `--rubric-detail` (or `rubricDetail` present) → genai/global structured per-criterion output on the configured scale (`rubric_structured.go`). **This path runs on `location=global` and does NOT apply `AutoraterConfig.SamplingCount`** (`rubric_structured.go:12-13`; `custom.go:52-59, 118-121`) — see §5.4.
- `ratingRubric` → carried and schema-validated in v1; **reserved** for the async `EvaluateDataset`/`LLMBasedMetricSpec` path (which retains `JudgeAutoraterConfig`, `.pb.go:2719`) and for adapters. Explicitly **not threaded into the runtime in v1** — a visible reserved field, not a stub.

### 4.5 Per-criterion structured output shape (normative, shipped)

The `--rubric-detail` result carries this fixed structure in `Result.CustomOutput` (`rubric_structured.go:93-110`, `engine.go:72`):

```jsonc
{
  "per_criterion": [
    { "group": "visual-identity", "criterion": "Logo appears and is unobscured",
      "score": 4, "rationale": "Logo present but partially cropped" }
  ],
  "overall_score": 4.0,
  "explanation": "Strong visual identity, minor logo issue"
}
```

`overall_score` maps to `Result.Score` (`rubric_structured.go` `overallScore`); the full structure stays in `CustomOutput`; `Result.RubricDetail=true` signals the shape (`engine.go:79`).

---

## 5. AutoRater Config Adoption

**Owner directive 5:** adopt Vertex `AutoraterConfig` directly (mirror the proto) rather than inventing a parallel shape; design how it fits the native path AND the genai/custom_schema path (which today carries no autorater config).

### 5.1 The proto, re-verified (the exact adoption target)

`AutoraterConfig` — `apiv1beta1/aiplatformpb/evaluation_service.pb.go:1170-1194`:

| Proto field | Go type | Semantics (from proto doc comments) |
|---|---|---|
| `sampling_count` (field 1) | `*int32` | samples per instance; **default 4**, min 1, max 32 (`:1172-1175`) |
| `flip_enabled` (field 2) | `*bool` | **default true**; pairwise-only; flips candidate/baseline for half the samples to reduce position bias (`:1176-1182`) |
| `autorater_model` (field 3) | `string` | fully-qualified publisher model `projects/{p}/locations/{l}/publishers/*/models/*` OR tuned endpoint `.../endpoints/{e}` (`:1183-1191`) |

**There is no `location` field on `AutoraterConfig`.** Location is on the request (`EvaluateInstancesRequest.Location`, mizan sets it at `native.go:96`, `pairwise.go:97`). This RFC mirrors the three real fields; location is handled by the engine's routing (§5.3), never authored into the template's autorater block.

### 5.2 Native-path adoption — already 1:1 (ratify)

mizan already mirrors the proto on the native path:
- Pointwise: `aiplatformpb.AutoraterConfig{AutoraterModel: fullModel}` + `SamplingCount` when set (`native.go:90-93`).
- Pairwise: `AutoraterConfig{AutoraterModel, SamplingCount, FlipEnabled}` with `SamplingCount` defaulting to 4 (`pairwise.go:69-104`, `pairwiseDefaultSamplingCount int32 = 4`, `:26`).
- Stored as `MetricTemplate.{AutoraterModel, SamplingCount, FlipEnabled}` (`model.go:115-118`); authored via `--model`/`--sampling-count`/`--flip-enabled` (`cmd/mizan/registry.go:57-59`, defaults 4 / true).

**Normative autorater block (v1) — mirrors the proto exactly:**

```yaml
spec:
  autorater:
    model: gemini-2.5-flash    # -> AutoraterConfig.autorater_model (publisher-RELATIVE id;
                               #    engine expands to the full resource name at eval time,
                               #    content.go:82 expandAutoraterModel). NEVER embed a project.
    samplingCount: 4           # -> AutoraterConfig.sampling_count (1-32, default 4)
    flipEnabled: true          # -> AutoraterConfig.flip_enabled (pairwise-only, default true)
```

The YAML uses `model` (not `autoraterModel`) because the enclosing `autorater:` key already scopes it — this is the shipped pack shape (`mizan-templates/.../video-brand-alignment.yaml`) and reads cleanly. The **1:1 correspondence to the proto is normative** (documented above); the shorter key is a surface convenience, not a divergence. `model` is stored **publisher-relative** (`gemini-2.5-flash`), never a full resource name — the engine expands it per the consumer's project/location (`content.go:82-99`), which is exactly what keeps a shared template portable across projects.

### 5.3 Location is a routing concern, not a template field (normative)

Because `AutoraterConfig` has no location, and because global-only judges (e.g. the `gemini-3.5` family) must run on the global host, mizan owns an **R-GLOBAL auto-routing** policy (`internal/eval/route.go`): known global-only prefixes (`globalOnlyModelPrefixes = ["gemini-3.5"]`, `route.go:65`) route straight to `location=global`; a regional NOT_FOUND on a global-only judge transparently retries on the global host (`route.go:152-205`). **Templates never declare a location.** The RFC ratifies this: authoring a template is portable; where it runs is the engine's decision from the resolved model + the consumer's config.

### 5.4 genai / custom_schema path — the asymmetry, made explicit (the reconciliation)

The genai path (`internal/eval/custom.go`; used by `kind: custom_schema` and by rubric `--rubric-detail`) **does not carry an `AutoraterConfig`**: it calls `genai.GenerateContent` at `location=global` (`custom.go:52-59`) with `Temperature: 0.0` and a strict `ResponseSchema` (`:118-121`), and **applies neither `samplingCount` nor `flipEnabled`** (there is no autorater config on this path — verified: no `AutoraterConfig` reference in `custom.go`).

**Decision (normative) — one autorater block, honored per-path, no parallel shape:**

1. **Keep a single `autorater` block in the template**, regardless of which path executes. Do **not** invent a second genai-specific config shape — that would be the "parallel shape" the directive warns against.
2. **On the genai/global path, `model` is honored; `samplingCount` and `flipEnabled` are not applicable** (no autorater config exists on that path). This is documented at the format level and at the CLI.
3. **When a template that will run on the genai/global path declares `samplingCount > 1` or `flipEnabled`, the engine emits a non-fatal `Warning`** (the `Result.Warnings` channel already exists, `engine.go:86`) stating the field is ignored on the global structured path. This makes the asymmetry visible at run time rather than silently dropped — the honest-integration rule applied to a field, not a component.
4. **Consistency posture:** the *format* is uniform (every template may declare an autorater block); the *runtime honoring* is path-dependent and documented. This gives directive 5's "consistency" without pretending the genai path supports sampling it does not.

> **Why not add `AutoraterConfig` to the genai path to make it symmetric?** Because the genai `GenerateContent` API has no sampling-count/flip concept — those are Vertex Eval Service (`EvaluateInstances`) features. Faking them on the genai path would be inventing behavior the substrate does not provide. The correct home for authored-rubric + sampling is the async `EvaluateDataset`/`LLMBasedMetricSpec` path (which *does* keep `JudgeAutoraterConfig`, `.pb.go:2719`) — reserved, not built here (§4.4, `rubric-design-discussion-v1.md` Q3 option c).

---

## 6. Tags — Soft / Free Vocabulary (Folksonomy)

**Owner directive 7:** tag vocabulary is soft/free (folksonomy), resolving survey open-question #5 in the folksonomy direction. Note the tag-filter gap; it is separately scoped, not this RFC's job to fix.

### 6.1 Policy (normative)

- **Tags are a free-form folksonomy.** `Tags []string` (`model.go:103`), authored via repeatable `--tag` (`cmd/mizan/registry.go:61`), no enforced namespace, no controlled vocabulary, no validation beyond "is a string." This matches the owner's CUJ6 decision (`personae-and-cujs.md`: *"tagging conventions is fine, with good documentation … contributors to mizan-templates manage"*) and the survey's convergent finding (§1.14: every mature ecosystem pairs an open folksonomy with, at most, a *documented* soft axis).
- **Documented (not enforced) namespace convention.** The mizan-templates docs SHOULD recommend prefix namespaces for discoverability — `industry:advertising`, `modality:video`, `task:brand-safety` — as a *convention*, borrowed from HF `pipeline_tag`+`tags` and Trove's `::` hierarchy without the governance cost (survey §3.2). These are documentation, **not** validation: a template with only free tags is valid.
- **No rigid owned taxonomy.** The owner explicitly declined it (`cuj-capability-map-v2.md` §4.3). Not pursued.

### 6.2 Reconciliation with CUJ6 (checked for daylight, per required reading)

The brief flags a possible tension: `personae-and-cujs.md` CUJ6 mentions *"could be a tagsonomy or folksonomy … with preset industry metrics templates."* Read together with the owner's later decision (*"tagging conventions is fine, with good documentation … contributors … manage"*) and this RFC's soft/free directive, there is **no daylight**: the resolution is folksonomy + documented conventions + community curation of *preset content*, not a mizan-core-owned taxonomy. The "industry-level categorization" is achieved by *convention-tagged preset templates shipped in mizan-templates* (library content), discoverable once the tag-filter lands — not by a validated vocabulary. This RFC and CUJ6 are consistent.

### 6.3 The tag-filter gap (documented, not fixed here)

Tags are **stored and authorable but not filterable**: `ListFilter` has no `Tags` field (`internal/registry/store.go:16-22` — only `Modalities`, `Kinds`, `Namespace`, `Source`, `DirtyOnly`) and `registry list` exposes only `--namespace`/`--kind` (`cmd/mizan/registry.go:336`). A folksonomy nobody can filter on is inert. **This RFC documents the intended shape** (add `Tags []string` to `ListFilter` + a `--tag` list flag + the SQLite query path, extended to eval-set listing when the runtime lands) but **does not implement it** — it is separately scoped as a Small item (`cuj-capability-map-v2.md` §2.4). Listed here so the format's tag policy and the implementation gap are both visible.

---

## 7. Provenance (owner directive 8 — REQUIRED)

Provenance is the connective tissue between the template format (this RFC) and the results store (separate capability). It spans three layers; all build on shipped fields, none reinvented.

### 7.1 Template identity & versioning (shipped)

- **Identity:** `id = "<namespace>/<slug>"`, stable primary key (`model.go:96`); slug `[a-z0-9-]+`; namespace = pack name.
- **Semver:** `metadata.version` (`model.go:99`), bumped on spec change; drives import conflict resolution (`newer` strategy, P2 §3.8).
- **Content integrity:** `contentHash` = SHA-256 over canonicalized `spec` + identity, excluding `updated`/the hash itself (`model.go:122`, P2 §3.6, `pack-format.md:145`). **Computed, never authored.** Used for drift detection, no-op short-circuit on re-import, and the future remote-change signal.
- **Format version:** `apiVersion: mizan.dev/v1alpha1` (see §10 for evolution).

This is the survey's noted strength — mizan is *ahead* of the field on identity/provenance (§Summary). Do not reinvent; build on it.

### 7.2 Pack / git provenance (shipped substrate)

- **Source:** on import, `Source = "pack:<ns>@<origin>"` (`model.go:121`, P2 §3.9); `ImportedAt` set (`model.go:126`); `Dirty` marks local edits (`model.go:123`).
- **Git as the ledger:** the pack lives in a git repo (`mizan-templates` or a fork); git blame is the authoritative authorship/history record (`pack-format.md:147`, P2 §3.6). `authors`/`maintainers` are *asserted* metadata corroborated by blame.
- **Pack release tag:** `mizan-pack.yaml` `metadata.version` is the human release tag of the pack as a whole (`mizan-templates/packs/google-brand/mizan-pack.yaml`).

So for any imported template, mizan can answer: *which repo/commit did this come from, who authored it, has it drifted locally* — from `Source` + git + `contentHash` + `Dirty`.

### 7.3 Result-side provenance — the data shape (NEW here; ties to the results-store work)

This is the piece the owner specifically asked this RFC to define: *which template version + autorater config + inputs produced a given result.* It ties directly to `eval-results-store-research.md`'s "run metadata + template ref/version" model. **This RFC defines the record shape; the results-store capability builds the engine** (Non-Goal §2.4).

**Normative result record shape (v1):** a persisted eval result MUST capture enough to reproduce and to attribute:

```jsonc
{
  "runId": "uuid",                      // unique per eval invocation
  "timestamp": "2026-08-11T00:00:00Z",
  "template": {                          // TEMPLATE PROVENANCE (§7.1)
    "id": "google-brand/video-brand-alignment",
    "version": "1.0.0",                  // semver at run time — the hill-climbing dimension
    "contentHash": "sha256:...",         // exact spec that ran (detects drift vs a version bump miss)
    "source": "pack:google-brand@github.com/ghchinoy/mizan-templates@<commit>"
  },
  "autorater": {                         // EVAL CONFIG PROVENANCE (§5) — the AutoraterConfig triple
    "model": "gemini-2.5-flash",         // publisher-relative id as resolved
    "resolvedModel": "projects/.../locations/global/publishers/google/models/gemini-2.5-flash",
    "samplingCount": 4,                  // as APPLIED (null on genai/global path — §5.4)
    "flipEnabled": true,                 // as APPLIED (null on non-pairwise / genai path)
    "location": "global"                 // the ROUTING location actually used (route.go), not a config field
  },
  "inputs": {                            // INPUT PROVENANCE — retention/PII fork is the store's (see below)
    "mode": "reference",                 // "reference" (hash+URI) | "inline" (raw) — store policy decides
    "fields": { "response": "gs://.../ad.mp4", "brand_guideline": "sha256:..." }
  },
  "result": {                            // THE OUTCOME (from eval.Result, engine.go:67-86)
    "score": 4.0,                        // Result.Score
    "pairwiseChoice": null,              // Result.PairwiseChoice ("" unless pairwise)
    "explanation": "...",                // Result.Explanation
    "customOutput": { /* per_criterion... for rubric-detail */ },  // Result.CustomOutput
    "rubricDetail": true,                // Result.RubricDetail
    "warnings": ["samplingCount ignored on global path"],          // Result.Warnings
    "stats": { "durationMs": 1234, "tokenUsage": 5678 }            // Result.Stats
  }
}
```

**Why each field is load-bearing:**
- `template.version` is the **first-class hill-climbing dimension** (`eval-results-store-research.md` §3, §4.3): per-version score trends, run-vs-baseline regression, A/B of two versions on the same inputs — none work without it.
- `template.contentHash` catches the case the survey/P2 flagged (D1): a spec changed without a version bump. Version answers "what did the author call it"; hash answers "what actually ran."
- `autorater.*` captures the config that produced the score, so a score is interpretable (a sampling-count-4 score and a sampling-count-1 score are not comparable). Recording `samplingCount: null` on the genai path is the honest record of §5.4's asymmetry.
- `inputs.mode` defers the raw-vs-reference retention/PII decision to the store (`eval-results-store-research.md` §5 Q4) — the RFC fixes the *shape* (a mode discriminator + field map) without deciding the policy, which is genuinely the store's call.

**Relationship to the store (explicit):** the results-store capability implements a `ResultStore` seam (mirroring the shipped `registry.Store`, `eval-results-store-research.md` §4.3), persists this record locally (SQLite) with an optional BigQuery/GCS export, and adds the compare/query surface. This RFC guarantees that whatever backend is chosen, **the record it stores is this shape**, so template↔result provenance is well-defined the day the store lands. The record is defined; the engine is deliberately out of scope and not stubbed.

### 7.4 The provenance chain, end to end

```
author (git blame) ─▶ template {id, version, contentHash} ─▶ pack {source, commit}
        │                                                              │
        └────────────── import (Source, ImportedAt, Dirty) ───────────┘
                                        │
                              eval run (autorater as applied, inputs, location)
                                        │
                                        ▼
                        result record {template ref+ver+hash, autorater, inputs, outcome}
                                        │
                                        ▼
                        results store  ─▶ hill-climbing (trend / regression / A-B by version)
```

---

## 8. promptfoo Interop — Concrete Adapter Field Mapping

**Owner directive 4:** promptfoo is the priority OSS interop target. Design what an import/export adapter actually maps, field-by-field, concretely enough to scope as an implementation issue — not "we should have an adapter."

### 8.1 The two shapes (re-grounded from the survey)

- **mizan:** a free-standing, shareable `MetricTemplate` YAML with identity/provenance (this RFC).
- **promptfoo:** a metric is an **assertion object** inside a test's `assert:` array in `promptfooconfig.yaml` (survey §1.2): `type` (`llm-rubric`, `g-eval`, `factuality`, `model-graded-closedqa`, …), `value` (rubric/criteria body or expected value), `threshold`, `weight` (default 1.0), `provider` (judge LLM), `rubricPrompt` (override judge prompt), `metric` (a name/tag for UI aggregation), `transform`, `config`. Judge returns `{reason, score: 0.0–1.0, pass}`; pass iff `score ≥ threshold`. Top-level `tags` is a `string→string` map. promptfoo publishes a JSON Schema (`promptfoo.dev/config-schema.json`, Draft-07).

**Structural mismatch to bridge:** a promptfoo assertion is *test-embedded* and lacks first-class identity/versioning/provenance; a mizan template is *free-standing* and carries all of them. An adapter is therefore lossy in one direction (mizan→promptfoo drops provenance) and enriching in the other (promptfoo→mizan must synthesize identity).

### 8.2 Field mapping (normative for the adapter — start with EXPORT, the cheaper half)

| mizan `MetricTemplate` | promptfoo assertion | Direction & notes |
|---|---|---|
| `metadata.id` | `metric` (name for UI aggregation) | export: `metric = id`. import: synthesize `id = "<imported-ns>/<slugified metric or hash>"` |
| `metadata.version`, `contentHash`, `authors`, `source`, `license` | *(no equivalent)* | export: **dropped** (or emitted as a YAML comment). import: synthesize `version: 0.1.0`, compute `contentHash`, `authors: []`, `source: "promptfoo:<file>"` |
| `metadata.tags []string` | top-level `tags` (`map[string]string`) | export: `tags` map from mizan list, e.g. `industry:advertising` → `{industry: advertising}`; bare tags → `{tag: <value>}`. import: reverse (flatten `k:v` → `"k:v"`) |
| `spec.kind: pointwise` + `rubricGroups`/prose | `type: llm-rubric` (or `g-eval` if step-decomposed) | export: pointwise-with-rubric → `llm-rubric`; `metricPromptTemplate` → `rubricPrompt`; criteria → `value` |
| `spec.kind: pairwise` | `type` in the pairwise/comparison family (e.g. a select-best assert) | export: map candidate/baseline field names into the assertion `config`; note promptfoo's pairwise model differs — flag lossy |
| `spec.kind: custom_schema` + `responseSchema` | `type: is-json` / `javascript` with schema in `config` | export: emit `responseSchema` into `config`; lossy (promptfoo has no native strict-schema judge equivalent) |
| `spec.metricPromptTemplate` (`{{placeholder}}`) | `rubricPrompt` / `value` (`{{var}}`) | placeholder syntax is **already compatible** (both double-brace) — direct copy |
| `spec.systemInstruction` | folded into `rubricPrompt` preamble or `provider` config | export: prepend to `rubricPrompt` |
| `spec.autorater.model` | `provider` (judge LLM id) | export: `provider = vertex:<model>` or `google:<model>`; import: parse provider → publisher-relative `model` |
| `spec.autorater.samplingCount`/`flipEnabled` | *(no direct equivalent)* | export: **dropped** (promptfoo has no sampling/flip); note as comment |
| `spec.rubricDetail.scale {min,max}` / `ratingRubric` (§4) | `threshold` + score semantics (0–1) | export: map Likert `[min,max]` → promptfoo 0–1 (normalize); `ratingRubric` descriptions → `value` prose. **Threshold:** mizan keeps threshold at the EvalSet layer (§3.2), so a single-template export has no threshold → default `0.5` or omit |
| `spec.inputs[]` (name+modality) | test `vars` (implicit) | export: document the expected vars; promptfoo binds vars at the test level, not the assertion |

### 8.3 Adapter scoping (advisory)

- **Export first (mizan → promptfoo `llm-rubric`):** the cheaper, higher-value half — it lets a mizan author share a template with the dominant OSS tool. Lossy on provenance/sampling (documented above), faithful on prompt/criteria/model.
- **Import second (promptfoo → mizan):** must synthesize identity/version/hash and default the missing provenance; flag every synthesized field.
- **Round-trip is not byte-stable** across the boundary (provenance is dropped then re-synthesized) — the adapter must document this; it is an interop bridge, not a lossless codec. This is a distinct implementation issue, sized in §12, not built here. A Vertex `AUTORATER_METRIC_SCHEMA` export (survey §1.1) is the natural *second* adapter (round-trip to the substrate) and reuses the same `autorater`/rubric mappings; lower priority than promptfoo per the owner.

---

## 9. Repo Placement — Discussion & Recommendation (owner confirms before any commit)

**Owner directive 2:** this must be discussed, not silently decided. Two questions: (a) where does the *format specification* live, and (b) where does *this RFC document* live.

### 9.1 The candidates

- **`mizan`** (the Go tool): owns the domain types (`internal/registry/model.go`), the codec/validation (P2: `internal/registry/codec.go`, `validate.go`, `schema/*.json`), the CLI, and the eval engine that materializes into Vertex protos. The **normative, machine-readable** artifact (the JSON Schema the validator enforces, the Go struct) is inherently here — it is code.
- **`mizan-templates`** (the content repo): owns the pack/template git-sync substrate (P2 §3.3), the human-facing `docs/pack-format.md`, the CI `validate-packs.yml` gate, and the community-curated preset content. It is where contributors *use* the format.

### 9.2 Recommendation

**Split by role, with `mizan` as the normative home:**

1. **The normative format specification lives in `mizan`.** The JSON Schema (`internal/registry/schema/metrictemplate.json`, `schema/evalset.json` — P2 §3.5, §3.4a) and the `MetricTemplate` Go struct are the single source of truth, because they are what the validator and engine enforce. A spec that lives apart from the code that enforces it drifts. Version the schema with the tool.
2. **This RFC document lives in `mizan`**, under a new `docs/rfcs/` (e.g. `mizan/docs/rfcs/0001-metric-format.md`). Rationale: it is the *touchstone the format evolves against*, it references file:line and proto evidence in the `mizan` codebase, and RFC evolution (§10) is naturally a PR process against the repo that owns the schema. Keeping the RFC beside the schema keeps "the decision" and "the enforced artifact" in one review surface.
3. **`mizan-templates` keeps the human-facing authoring guide** (`docs/pack-format.md`) as a *derived, tutorial* document that **references** the normative schema/RFC in `mizan` (e.g. "for the authoritative field list see mizan RFC-0001 / `metrictemplate.json`"). Contributors read the tutorial; the CI gate enforces the normative schema pinned from a `mizan` release tag (already the P2.6 plan — `MIZAN_VERSION` pins a tag).

**Why not `mizan-templates` for the spec?** It carries no Go types and no validator; the schema would be a copy that drifts from the code that enforces it. `mizan-templates` is the *content + distribution* layer, not the *definition* layer (P2 §3.3, `cuj-capability-map-v2.md` §2.1).

**Why not split the RFC across both?** A single touchstone is the point. One normative home (`mizan`), one derived tutorial (`mizan-templates`) that links to it.

> **CONFIRMED by owner 2026-08-11 (Q1).** Placement is decided: normative schema in `mizan`; this RFC → `mizan/docs/rfcs/0001-metric-format.md`; `mizan-templates/docs/pack-format.md` becomes a derived tutorial pointing at the normative schema. The commit sequence (owner-directed): (1) RFC doc → PR to `mizan/docs/rfcs/0001-metric-format.md` (reviewed); (2) schema implementation; (3) `mizan-templates` tutorial rewrite — so the tutorial documents a *landed* schema, not a planned one.

---

## 10. RFC Process — How This Format Evolves (owner-mandated, first-class)

This document is the *seed* of a community RFC. The format must evolve after v1 with a defined process. This section is normative for *process*, and is itself the most likely thing the community amends first.

### 10.1 Two independent version axes (do not conflate)

1. **Spec version** = `apiVersion` (`mizan.dev/v1alpha1` today). Governs the *format itself* (what fields exist, their meaning). Advances `v1alpha1 → v1alpha2 → v1beta1 → v1` as the format matures. Mizan releases set this; a pack declares the minimum it requires (`mizan-pack.yaml` `spec.requiresApiVersion`, verified in the scaffold).
2. **Template version** = `metadata.version` (semver, per template). Governs an *individual template's* content. Authors bump it; it drives import conflict resolution and hill-climbing. **A template version bump is not a spec change and vice versa.**

Backward compatibility rule (normative): within a `v1*` line, changes are **additive** (new optional fields, new reserved fields). A field removal or a semantic change to an existing field requires a **new spec version** and a documented migration (§10.4).

### 10.2 Who can propose, and where discussion happens

- **Anyone** may propose a format change (mizan is community-facing).
- **Proposals are PRs against `mizan/docs/rfcs/`** — either amending this RFC (for clarifications) or adding a new numbered RFC (for substantive additions, e.g. `0002-...`). Discussion happens in the PR and the linked GitHub issue.
- **A proposal must state:** the problem, the exact schema delta, backward-compat impact (additive vs breaking → which spec-version axis it touches), an adapter/interop impact note (does it change the promptfoo/Vertex mapping?), and a migration note if breaking.

### 10.3 What "accepted" means

- **Accepted** = the owner (or a delegated maintainer set, if the owner later defines one) approves and merges the RFC PR, AND the corresponding schema/validator change lands in `mizan` behind the appropriate `apiVersion`. An RFC is not "accepted" until the normative schema reflects it — prose and enforcement land together.
- **Status ladder:** `Draft → Proposed (PR open) → Accepted (merged + schema landed) → Superseded/Deprecated`. This document is **Proposed** as of 2026-08-11 (all owner decisions in §11 confirmed); it advances to **Accepted** when the RFC PR merges and the corresponding schema change lands in `mizan`.
- **Stability tiers mirror `apiVersion`:** `v1alpha*` = may change with notice; `v1beta*` = additive-only, deprecations announced; `v1` = stable, breaking changes only via a new major with migration.

### 10.4 Migration discipline

A breaking change (new spec major) ships with: (a) a documented field-level migration, (b) codec support to *read* the prior version for at least one release, and (c) an `apiVersion` bump so old and new packs are distinguishable. `contentHash` canonicalization must be versioned with the spec so hashes remain comparable within a version line.

### 10.5 Governance of content vs. format

Per the owner (`personae-and-cujs.md`, `cuj-capability-map-v2.md` §4.3): **format governance** (this RFC) is owner/maintainer-gated in `mizan`; **content curation** (which preset templates, industry tag conventions) is community-managed via `mizan-templates` PRs. The two are separate: a new template is a content PR (no RFC); a new *field* is a format PR (an RFC). Keep them from bleeding into each other.

---

## 11. Decisions (all confirmed by owner 2026-08-11)

The five load-bearing questions raised during drafting were confirmed by the owner (ghchinoy@google.com), relayed via coordinator, on **2026-08-11** — each matching the architect's recommendation exactly. They are recorded here as **Decided** (no longer open).

1. **[DECIDED 2026-08-11] Repo placement (§9).** Normative JSON Schema + `MetricTemplate` struct live in `mizan`; this RFC → `mizan/docs/rfcs/0001-metric-format.md`; `mizan-templates/docs/pack-format.md` becomes a derived tutorial referencing the normative schema. Commit sequence: RFC doc PR → schema implementation → tutorial rewrite (tutorial documents a landed schema, not a planned one).

2. **[DECIDED 2026-08-11] RFC governance model (§10.2/10.3).** Owner is the **sole approver for v1**; revisit naming a maintainer set + delegation path as adoption/contributor volume grows.

3. **[DECIDED 2026-08-11] `ratingRubric` in v1 (§4.4).** **Carry-and-reserve now** — the optional Vertex-aligned rating-band→description block is added to the schema and validated in v1, reserved from the runtime (consumed by adapters + the future async `EvaluateDataset`/`LLMBasedMetricSpec` round-trip). Costs the schema one optional block and nothing at runtime.

4. **[DECIDED 2026-08-11] Result record: raw inputs vs. reference (§7.3).** This RFC fixes the record *shape* (`mode: reference|inline` discriminator + field map). The retention/PII **policy** (store raw bytes/text vs. hash+GCS-URI reference) is **left to the results-store capability** to decide (`eval-results-store-research.md` §5 Q4) — this RFC does not pre-decide it.

5. **[DECIDED 2026-08-11] Vertex `AUTORATER_METRIC_SCHEMA` export (§8.3).** Vertex config-file export is the confirmed **second** interop adapter (round-trip to the substrate), **after** the priority promptfoo adapter (directive 4).

---

## 12. Sizing / Phasing Note (advisory only — no implementation authorized here)

Per `software-engineering-process` → Sizing. This RFC authorizes **no code**; this section is advisory so the owner/coordinator can scope follow-on issues. Any build work needs its own architect/EM pass. **A first vertical slice validates the format end-to-end before fan-out** (the standing fan-out discipline).

- **Slice 0 — commit the touchstone (S, docs-only):** land this RFC as `mizan/docs/rfcs/0001-metric-format.md` (§11.1 decided) via a reviewed PR. Per the owner's confirmed sequence, this is **step 1** and lands *before* schema code and *before* the tutorial rewrite. *Gate before any format-code fan-out.*
- **Format alignment additive fields (M):** add optional `rubricDetail.scale` explicitness + optional `ratingRubric` to `schema/metrictemplate.json` + `internal/registry/model.go` + codec + validation (§4.4, §11.3 decided carry-and-reserve); wire the genai-path `samplingCount`-ignored `Warning` (§5.4). Additive, non-breaking; folds into P2's codec/validation phases (P2.1/P2.2). This is **step 2** of the owner's sequence (after the RFC PR merges).
- **`mizan-templates` tutorial rewrite (S, docs-only):** reframe `docs/pack-format.md` as a derived tutorial that references the normative schema/RFC in `mizan` (§9.2). This is **step 3** of the owner's sequence — it lands *after* the schema change so it documents a real landed schema, not a planned one. Depends on the additive-fields schema step.
- **Tag-filter (S):** `ListFilter.Tags` + `--tag` list flag + SQLite query (§6.3). Already separately scoped; independent; cheap fast-follow.
- **promptfoo export adapter (M):** the §8.2 mapping, export-first (`mizan template → promptfoo llm-rubric`), with the documented lossy-provenance behavior. A distinct issue; depends on the format being frozen (this RFC).
- **promptfoo import + Vertex config export (M each):** the reverse adapter + the second adapter; gated on §11.5 and on the export adapter validating the mapping first.
- **Result record shape in the store (M, NOT this RFC):** the results-store capability implements §7.3's record behind a `ResultStore` seam (`eval-results-store-research.md` §4.4). This RFC only fixes the shape; the engine is that capability's work and stays visibly assigned there.

---

## 13. Addendum — Adaptive-generation provenance (`rubricProvenance`) [additive, 2026-08-17]

> **Amendment class:** this section is an **additive extension** under the RFC's own
> evolution process (§10.1: *within a `v1*` line, changes are additive — new optional
> fields*). It adds one **optional** field to `MetricTemplate` and documents its
> semantics. It introduces **no breaking change and no `apiVersion` bump** —
> `apiVersion: mizan.dev/v1alpha1` is unchanged. It does **not** alter `rubricGroups`
> semantics (§4) and explicitly **does not touch** the reserved `ratingRubric` block
> (§4.4, §11 item 3). Ground truth: mizan `MetricTemplate` /`RubricProvenance`
> (`internal/registry/model.go`), `contentHash` (`internal/registry/hash.go`), pack
> codec + strict schema (`internal/registry/codec.go`,
> `schema/metrictemplate.json`). Design source: `design/adaptive-rubrics-support-plan.md`
> §4.4/§4.7 (Decisions 2 & 3).

### 13.1 Context: adaptive generation is an authoring aid, not a runtime metric mode

Mizan can have Gemini **draft** rubric criteria from a sample prompt (Vertex AI's
synchronous `:generateInstanceRubrics` RPC — CUJ 7 / CUJ 8). This is deliberately
scoped as an **authoring-time action**, not a runtime metric kind:

> **Generated-then-frozen ⇒ ordinary static rubric.** The moment a user reviews,
> edits, and freezes a generated draft, the result is an **ordinary `KindRubric`
> template** — the same shape one authors by hand (§4), run through mizan's existing
> deterministic eval path, just **provenance-stamped**. Generation removes the
> blank-page cost of authoring; it does **not** introduce a new metric kind, a new
> runtime path, or an ephemeral per-prompt rubric.

Consequences for this format:

- **No new `MetricKind`.** A frozen generated rubric is a `KindRubric` template; the
  `MetricKind` enum (§3, `model.go`) is unchanged.
- **`RubricGroups` semantics are unchanged.** The generated criteria land in the
  existing `rubricGroups` map[string][]string (§4.2); the native and structured
  (`--rubric-detail`) paths consume them with **zero** modification.
- **One new optional field records provenance** (§13.2). Its absence means
  hand-authored — existing templates and packs are unaffected (no migration).

This is the align-with-mizan's-reproducibility posture the format already takes
(§7): identity + provenance are what distinguish a mizan template from an ephemeral
Vertex SDK object; recording *how a rubric was drafted* is a natural extension of
that discipline.

### 13.2 The `rubricProvenance` object (normative)

`MetricTemplate` gains one optional field, `rubricProvenance` (a pointer; `nil` ⇒
hand-authored). When present it records that, and how, `rubricGroups` were
AI-drafted:

```yaml
spec:
  kind: rubric
  rubricGroups:                      # unchanged runnable core (§4) — a flat map
    general_quality:
      - "The response is in English."
      - "The product description is concise."

  rubricProvenance:                  # OPTIONAL, additive; absent ⇒ hand-authored
    method: adaptive-generated       # REQUIRED — how the rubric was produced
    generatorModel: gemini-2.5-flash # REQUIRED — the drafting/generator model
    recipe: general_quality_v1       # optional — the pinned predefined recipe
    promptTemplate: "…"              # optional — custom-generation prompt (custom path)
    sampleInputRef: 'inline:"…" sha256:…'  # REQUIRED — bounded ref + SHA-256 of the sample input
    generatedAt: 2026-08-17T00:00:00Z      # REQUIRED — RFC3339 generation timestamp
    apiVersion: v1beta1:generateInstanceRubrics  # REQUIRED — the generation API surface
    rubricMeta:                      # optional — per-criterion type/importance (Decision 2)
      - {group: general_quality, criterion: "The response is in English.", type: "LANGUAGE:PRIMARY_RESPONSE_LANGUAGE", importance: HIGH}
      - {group: general_quality, criterion: "The product description is concise.", type: "FORMAT_REQUIREMENT:CONCISENESS", importance: HIGH}
```

**Field reference:**

| Field | Req. | Meaning |
|---|---|---|
| `method` | ✔ | How the rubric was produced, e.g. `adaptive-generated`. **Cross-team contract:** the results-store capability reads this via `RubricRef.Method` — do not rename. |
| `generatorModel` | ✔ | The model that drafted the criteria (e.g. `gemini-2.5-flash`). |
| `recipe` | — | The pinned predefined generation recipe (e.g. `general_quality_v1`); present on the predefined path. |
| `promptTemplate` | — | The custom rubric-generation prompt; present on the custom-generation path. |
| `sampleInputRef` | ✔ | A **bounded** preview of the sample input **plus its SHA-256** — never an unbounded prompt blob. Lets a consumer identify/audit the sample without retaining it in full. |
| `generatedAt` | ✔ | RFC3339 timestamp of the generation call. |
| `apiVersion` | ✔ | The generation API surface, `v1beta1:generateInstanceRubrics`. Distinct from the template's format `apiVersion` (`mizan.dev/v1alpha1`) — this names the *generation RPC*, not the format version. |
| `rubricMeta[]` | — | Per-criterion `{group, criterion, type, importance}`, aligned 1:1 with `rubricGroups` in declared order (Decision 2, §13.3). |

A consumer can therefore always (a) tell an AI-drafted rubric from a hand-authored
one, and (b) reproduce/audit *how* it was drafted (model, recipe or custom prompt,
sample-input hash, timestamp, API surface).

### 13.3 Decision 2 — preserve per-rubric `type`/`importance` in provenance

The generation API returns richer per-criterion metadata (`type`, `importance`) than
mizan's flat `rubricGroups map[string][]string` can carry. **Decision 2** (support
plan §4.4): **preserve that metadata in `rubricProvenance.rubricMeta[]`, not in
`rubricGroups`.** `rubricGroups` **stays a plain `map[string][]string`**, so the
**runnable model is unchanged** — `runRubric` / `runRubricStructured` consume the
criteria list exactly as before, and nothing downstream in the eval path changes.
No generation fidelity is lost (it lives in `rubricMeta`), and the runtime carries
zero new complexity. `rubricMeta` entries are kept in declared order and aligned
1:1 with the criteria.

### 13.4 Decision 3 — `rubricProvenance` is included in `contentHash`

**Decision 3** (support plan §4.4): **`rubricProvenance` is part of the template's
`contentHash`.** This follows the `ratingRubric` precedent — content that
meaningfully distinguishes one template from another is hashed (§7.1). The
consequence, which is exactly what auditability wants:

> Editing a generated rubric's **criteria OR its provenance** changes the
> `contentHash`.

So a locally edited AI-drafted rubric is detectably different from the one Gemini
produced (drift detection, no-op short-circuit on re-import, and version-vs-hash
reconciliation from §7 all apply unchanged). `contentHash` is **computed, never
authored** (§7.1); adding `rubricProvenance` to the canonical hash shifts the golden
hash **by design** — the established pattern for additive hashed fields.

### 13.5 Relationship to `ratingRubric` (explicitly untouched)

Adaptive generation is **not** the async `EvaluateDataset`/`LLMBasedMetricSpec`
metric. The reserved `ratingRubric` block (§4.4, Decision §11 item 3) — the
rating-band→description map carried for that future async round-trip and for
adapters — is **wholly unaffected** by this addendum. `rubricProvenance` records
*how the criteria were drafted*; `ratingRubric` describes *score bands* for a
reserved runtime path. They are independent optional fields; this section adds the
former and changes nothing about the latter.

### 13.6 Format-evolution bookkeeping

Per §10.2, this addendum is the additive schema delta for `rubricProvenance`:

- **Problem:** record that (and how) a rubric was AI-drafted, so generated-then-frozen
  rubrics remain auditable and reproducible.
- **Schema delta:** one optional `rubricProvenance` object on `MetricTemplate`
  (subfields per §13.2); optional under `additionalProperties: false` in
  `schema/metrictemplate.json`.
- **Backward-compat impact:** **additive** (§10.1). `nil` ⇒ hand-authored; existing
  templates/packs validate and behave exactly as before. No `apiVersion` bump.
- **Adapter/interop impact:** none required — `rubricProvenance` is mizan-native
  provenance with no promptfoo/Vertex counterpart; a promptfoo export drops it
  (consistent with §8.2's provenance-is-lossy-on-export behavior).
- **Migration:** none, other than the intentional golden-`contentHash` shift
  (§13.4), handled in the field's implementation commit.

---

## Appendix A — Evidence Ledger (re-verified 2026-08-11)

**mizan @ `ce57ea2` (read-only clone):**
- `internal/registry/model.go:94-127` — `MetricTemplate` full field set (identity/behavior/provenance).
- `model.go:113` — `RubricGroups map[string][]string` (group→criteria list).
- `model.go:115-118` — `AutoraterModel string`, `SamplingCount int32`, `FlipEnabled bool` (top-level).
- `model.go:48-66` — `NormalizeKind` (single/compare → pointwise/pairwise).
- `internal/registry/store.go:16-22` — `ListFilter` = {Modalities, Kinds, Namespace, Source, DirtyOnly} — **no Tags**.
- `internal/eval/engine.go:67-95` — `Result` {Score *float32, PairwiseChoice, Explanation, CustomOutput map[string]any, RubricDetail bool, Warnings []string, Stats}.
- `internal/eval/native.go:78-118` — pointwise builds `aiplatformpb.AutoraterConfig{AutoraterModel}` + `SamplingCount` when set; request `Location` at `:96`.
- `internal/eval/pairwise.go:26,69-104` — `pairwiseDefaultSamplingCount=4`; builds `AutoraterConfig{AutoraterModel, SamplingCount, FlipEnabled}`.
- `internal/eval/custom.go:44-59,118-121` — genai path `location=global`, `Temperature 0.0`, `ResponseSchema`; **no AutoraterConfig**.
- `internal/eval/rubric_structured.go:12-13,36,50,93-129,149` — genai/global structured path; per_criterion `{group, criterion, score(int), rationale}` + `overall_score` + `explanation`; configurable Likert `[min,max]` (default 1–5); clamp/reconcile; runs without SamplingCount.
- `internal/eval/route.go:48-65,152-205` — R-GLOBAL routing; `globalOnlyModelPrefixes=["gemini-3.5"]`.
- `internal/eval/content.go:82-99` — `expandAutoraterModel` (publisher-relative → full resource name).
- `cmd/mizan/registry.go:57-61,219-230,336` — `--model`/`--sampling-count`(4)/`--flip-enabled`(true)/`--modality`/`--tag`; list filters `--namespace`/`--kind` only.

**Vertex `cloud.google.com/go/aiplatform v1.126.0` `apiv1beta1/aiplatformpb` (module cache):**
- `evaluation_service.pb.go:1170-1194` — `AutoraterConfig` = `sampling_count *int32` (field 1, default 4, 1–32), `flip_enabled *bool` (field 2, default true, pairwise-only), `autorater_model string` (field 3, publisher/tuned-endpoint format). **No location field.**
- `evaluation_service.pb.go:7056-7073` — `PointwiseMetricSpec` = `metric_prompt_template`, `system_instruction`, `custom_output_format_config`.
- `evaluation_service.pb.go:2705-2724` — `LLMBasedMetricSpec` = `RubricsSource` oneof (`RubricGroupKey` string field 4 / `PredefinedRubricGenerationSpec`), `metric_prompt_template` (1), `system_instruction` (2), `judge_autorater_config *AutoraterConfig` (3), `additional_config` (7).
- **Confirmed absent:** `type RubricGroup struct`, `type Rubric struct`, `type EvaluationInstance struct` — grep across the whole `aiplatform@v1.126.0` module returns none (only the doc-comment reference at `:2815`).

**mizan-templates @ `5c140a1`:**
- `packs/google-brand/mizan-pack.yaml` — `kind: Pack`, `apiVersion: mizan.dev/v1alpha1`, `spec.requiresApiVersion`.
- `packs/google-brand/templates/video-brand-alignment.yaml` — `kind: MetricTemplate`; `autorater: {model, samplingCount, flipEnabled}`; `tags: [advertising, brand-safety, video]`; pointwise.
- `docs/pack-format.md:113,145,147` — `rubricGroups: {<group>: [criteria]}`; `contentHash` computed; git-as-provenance.

**Design docs (scratchpad, authoritative inputs):**
- `eval-metric-format-standards-research.md` §1.1–1.2 (Vertex/promptfoo), §3 (align+adapt), §4 (open forks D-scale/D-rubric/tags).
- `eval-results-store-research.md` §3–4 (result data model, hill-climbing, `ResultStore` seam), §5 (retention/PII fork).
- `rubric-decisions-locked.md` (configurable Likert default 1–5, per-criterion score+rationale, `--rubric-detail`).
- `rubric-design-discussion-v1.md` Q1–Q3 (sync flatten is a mizan choice; async `EvaluateDataset` keeps `JudgeAutoraterConfig`; genai structured path).
- `p2-template-packs-design.md` §3.2–3.6 (pack format/versioning/contentHash), §3.4a (`EvalSet` manifest + reserved threshold), §8 (D1/D5).
- `personae-and-cujs.md` (CUJ6 folksonomy + documented conventions, community curation).
- `cuj-capability-map-v2.md` §2.1/2.4/4.3 (packs = distribution layer; tag-filter S; folksonomy not taxonomy).
```
