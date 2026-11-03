# Multimodal Judge Pack

Multidimensional evaluation templates and a core `EvalSet` for auditing vision-language assistants on dense text extraction, Set-of-Mark UI element grounding, grounded pairwise preference, and indirect OCR prompt-injection defense.

- **Namespace:** `multimodal-judge` (every template ID begins `multimodal-judge/`)
- **Author / Maintainer:** Jigyasu Juneja (`@jigyasujuneja`)
- **License:** Apache-2.0

## Templates

| ID | Kind | Modalities | Autorater Pin | Inputs | Purpose |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `multimodal-judge/fine-print-verify` | `rubric` | `image`, `text` | `gemini-3.5-flash` | `image`, `prompt`, `response` | Checks numeric, unit, and footnote accuracy on small-print labels, price tags, and tables. |
| `multimodal-judge/som-ui-grounding` | `choice` | `image`, `text` | `gemini-3.5-flash-lite` | `image`, `prompt`, `response` | Classifies whether an agent's cited Set-of-Mark ID points to the target interactive UI control. |
| `multimodal-judge/visual-pairwise-preference` | `pairwise` | `image`, `text` | `gemini-3.5-flash` | `image`, `prompt`, `baseline_response`, `candidate_response` | Compares two multimodal answers on factual visual grounding and spatial precision with position-flip de-biasing. |
| `multimodal-judge/untrusted-ocr-quarantine` | `boul` | `image`, `text` | `gemini-3.5-flash-lite` | `image`, `prompt`, `response`, `system_policy` | Audits whether an assistant treats imperative instructions inside scene or document pixels strictly as untrusted data. |

## Core EvalSet & Cross-Kind Scorecard Behavior

`evalsets/multimodal-judge-core.yaml` combines all four templates using `weighted-mean` aggregation (`threshold: 0.85`, `gate: true`), assigning `1.5` weight to `multimodal-judge/untrusted-ocr-quarantine` and `1.0` weight to each remaining dimension.

- **Aligned Shared Input Placeholders:** All four templates share `image` (`modality: image`) and `prompt` (`modality: text`), and the three single-response templates (`fine-print-verify`, `som-ui-grounding`, `untrusted-ocr-quarantine`) share `response` (`modality: text`). When running the full suite via Phase-1 `mizan evalset run`, pass the superset of inputs (`image`, `prompt`, `response`, `system_policy`, `baseline_response`, `candidate_response`) so each member receives its required placeholders.
- **Cross-Kind Aggregation Semantics:** Phase-1 `weighted-mean` averages raw numeric member scores without cross-kind normalization (`rubric` scores on `1..5` and `boul` maps pass/fail to `1.0`/`0.0` against `threshold: 0.85`), while categorical and comparative members (`choice` and `pairwise`, where `Score == nil`) appear as non-scalar verdict rows on the scorecard alongside the aggregate gate.

## Examples & Illustrative `gs://` URIs

`examples/golden_evalset.jsonl` ships eight labeled multimodal evaluation cases (two per template across `core` and `adversarial` tiers) formatted for `mizan eval compare-engines --dataset`.

> **Note on Placeholder `gs://` URIs:** The `gs://mizan-examples-public/multimodal-judge/...` URIs referenced in `examples/golden_evalset.jsonl` are **illustrative placeholders**, not a live public Cloud Storage bucket. For live evaluation runs against Vertex AI, replace them with `gs://` URIs in your own bucket or pass local media files via `--file image=/path/to/local.png`.

## Usage

Validate the pack offline without cloud credentials:

```bash
mizan pack validate packs/multimodal-judge
```

Import the templates into your local Mizan registry:

```bash
mizan registry import packs/multimodal-judge
```
