# Multimodal Judge Pack

Multidimensional evaluation templates and a core `EvalSet` for auditing vision-language assistants on dense text extraction, Set-of-Mark UI element grounding, grounded pairwise preference, and indirect OCR prompt-injection defense.

## Templates

| ID | Kind | Modalities | Purpose |
| :--- | :--- | :--- | :--- |
| `multimodal-judge/fine-print-verify` | `rubric` | `image`, `text` | Checks numeric, unit, and footnote accuracy on small-print labels, price tags, and tables. |
| `multimodal-judge/som-ui-grounding` | `choice` | `image`, `text` | Classifies whether an agent's cited Set-of-Mark ID points to the target interactive UI control. |
| `multimodal-judge/visual-pairwise-preference` | `pairwise` | `image`, `text` | Compares two multimodal answers on factual visual grounding and spatial precision with position-flip de-biasing. |
| `multimodal-judge/untrusted-ocr-quarantine` | `boul` | `image`, `text` | Audits whether an assistant treats imperative instructions inside scene or document pixels strictly as untrusted data. |

## Core EvalSet and Fixtures

`evalsets/multimodal-judge-core.yaml` combines all four templates using `weighted-mean` aggregation (`threshold: 0.85`, `gate: true`), assigning `1.5` weight to `multimodal-judge/untrusted-ocr-quarantine` and `1.0` weight to each remaining dimension.

`fixtures/golden_evalset.jsonl` ships eight labeled multimodal evaluation cases (two per template across `core` and `adversarial` tiers) compatible with `mizan eval compare-engines --dataset`.

## Usage

Validate the pack offline without cloud credentials:

```bash
mizan pack validate packs/multimodal-judge
```

Import the templates into your local Mizan registry:

```bash
mizan registry import packs/multimodal-judge
```
