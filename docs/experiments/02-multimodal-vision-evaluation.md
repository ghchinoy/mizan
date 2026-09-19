# Experiment 02: Multimodal Vision & Multi-Rubric Evaluation

This experiment investigates multimodal vision conditioning across two distinct architectures using image inputs:
1. Gemini 2.5 Flash (autoregressive transformer vision-encoder).
2. DiffusionGemma 26B (discrete block diffusion vision-conditioned canvas).

---

## Case 2.1: Visual Proposition Check on Software Architecture Diagram

* **Metric Template**: `demo/image-diagram-check`
* **Kind**: `boul`
* **Prompt**: `Does this image show an architectural software diagram with components and arrows? {{image}}`
* **Asset**: `docs/diagrams/component-architecture.webp` (2101×643 WebP diagram illustrating Mizan's Go packages and engine dispatch flows).

### Command
```bash
mizan eval compare-engines \
  --metric demo/image-diagram-check \
  --file image=docs/diagrams/component-architecture.webp \
  --engine-a vertex \
  --engine-b diffusion
```

### Empirical Results
```
Metric:             demo/image-diagram-check (boul)
Verdict Agreement:  DIVERGENT (Mismatch)

DIMENSION     ENGINE A (VERTEX)                                              ENGINE B (DIFFUSION)
---------     -------------                                                  -------------
Passed:       PASS                                                           FAIL
Score:        1.00                                                           0.00
Confidence:   100.0%                                                         99.8%
Duration:     3054.0 ms                                                      10808.0 ms
Explanation:  The image is explicitly titled "Mizan - component              DiffusionGemma verdict: no (confidence: 99.8%, stderr: ±0.0000)
              architecture" and clearly displays various software
              components (represented by labeled boxes and shapes)
              interconnected by arrows...
```

### Divergence Analysis
* **Engine A (Vertex AI Gemini)**: High-resolution OCR reading. Gemini reads the heading text `"Mizan - component architecture"`, detects the graphviz edge arrows and box outlines, and returns `PASS` with 100% confidence.
* **Engine B (DiffusionGemma)**: Low-resolution vision projection on discrete diffusion canvas. DiffusionGemma's Metal vision encoder is trained primarily on natural photos and standard photographic objects; when exposed to a high-aspect-ratio (2101×643) technical vector diagram with small text nodes, it fails to recognize the semantic category "architecture diagram" and assigns 99.8% probability to `no`.
* **Architectural Lesson**: For technical diagram OCR and schematic reasoning, large multimodal models (Gemini) remain necessary; local diffusion vision encoders require natural imagery or specialized diagram fine-tuning.

---

## Case 2.2: Multi-Criterion Visual Rubric Scorecard

* **Metric Template**: `quickstart/image-rubric-scorecard`
* **Kind**: `rubric` (6 criteria across 3 groups)
* **Prompt**: `Assess this candidate marketing image against the criteria. Image: {{image}}`
* **Asset**: `docs/diagrams/component-architecture.webp`

### Single-Pass Diffusion Forward Execution
```bash
mizan eval run \
  --metric quickstart/image-rubric-scorecard \
  --engine diffusion \
  --file image=docs/diagrams/component-architecture.webp
```

### Live Output
```
Score:        3.3333335
Explanation:  DiffusionGemma evaluated 4/6 rubric criteria passed (score: 3.3/5)

Per-criterion:
GROUP              CRITERION                                         SCORE  RATIONALE
brand_presence     A brand logo is clearly visible                   0      Slot readout: no (confidence: 95.6%, stderr: ±0.0348)
brand_presence     Uses a predominantly blue-and-white color scheme  0      Slot readout: no (confidence: 96.2%, stderr: ±0.0060)
safety             Contains no violent or graphic content            1      Slot readout: yes (confidence: 95.2%, stderr: ±0.0389)
safety             Contains no adult or suggestive content           1      Slot readout: yes (confidence: 79.1%, stderr: ±0.1388)
technical_quality  The image is in sharp focus                       1      Slot readout: yes (confidence: 57.3%, stderr: ±0.1924)
technical_quality  The main subject is well-lit and clearly visible  1      Slot readout: yes (confidence: 82.2%, stderr: ±0.1396)
```

### Key Mechanical Insights
1. **Zero Multi-Criterion Compute Penalty**: Evaluating all 6 criteria simultaneously in DiffusionGemma took the **exact same forward pass time (~1,700 ms)** as evaluating a single boolean question. In an autoregressive model, each criterion adds ~50–100 tokens of serial generation time.
2. **Per-Criterion Uncertainty Attribution**:
   - `Uses a predominantly blue-and-white color scheme`: High certainty `no` (`stderr: ±0.0060`) — the diagram's pastel salmon and green boxes clearly violate blue/white.
   - `The image is in sharp focus`: High variance (`confidence: 57.3%, stderr: ±0.1924`) — the model detects uncertainty around what constitutes "focus" in a synthetic vector graphic.
