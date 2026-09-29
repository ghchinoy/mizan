# Experiment 07: Judge Capabilities With and Without an Autoregressive Autorater

* **Primary session:** 2026-09-29, all engines rerun on the stable **dgem v0.1.0** serving image (`version v0.1.0`, `revision f241b77`, vLLM `a9eafde5`, recorded in [`runs/20260929-v010/backends.json`](judge-eval/runs/20260929-v010/backends.json)). Folder: [`runs/20260929-v010/`](judge-eval/runs/20260929-v010/).
* **Replication:** 2026-09-26, same suites, templates and engines, on the pre-v0.1.0 serving images. Folder: [`runs/20260926/`](judge-eval/runs/20260926/). The item-by-item comparison is in [`compare_vs_20260926.md`](judge-eval/runs/20260929-v010/compare_vs_20260926.md).
* **Harness:** `mizan eval compare-engines`, report schema v2. Each engine is scored against the gold or human label first; agreement between engines is secondary.
* **Suites:** 13 judge suites (1,290 items) from public human-labelled datasets, plus 2 computation suites (650 items).
  - Built by `scripts/judge-eval/build_suites.py`, which pins row indices, dataset revisions, licences and SHA-256 in [`judge-eval/manifest.json`](judge-eval/manifest.json).
  - Before the 09-29 run, `build_suites.py --check` rebuilt all 15 suites byte-identical to the manifest.
  - The suite files are gitignored because several upstream licences are non-commercial.
* **Templates:** the `judge-eval` pack in mizan-templates (22 templates).
* **Scripts:** `scripts/judge-eval/run_all.sh`, `retry_errors.sh`, `analyze.py` and `compare_runs.py`.
* **Supersedes:** Experiments 04–06 (see the note at the top of each).

## 1. Question

Mizan wraps the Vertex AI Gen AI Evaluation Service. This experiment asks, for each of that service's capabilities:

1. Can it run with **no model at all**?
2. Can **DiffusionGemma** do it? DiffusionGemma is non-autoregressive: it reads typed answer slots in one pass instead of generating text.
3. Where does an **autoregressive autorater** still earn its cost?

Earlier experiments reported how often two engines agreed. That hid cases where both were wrong, and Experiment 05 never actually used DiffusionGemma's structured readout. Here every verdict is scored against a human or gold label, and each comparison runs both engines on the same items in the same session.

## 2. Judge configurations

| ID | Judge | Path |
|---|---|---|
| J0 | **No model**: mizan's local Go computation metrics vs Vertex `EvaluateInstances` computation metrics | `kind: computation` |
| J1 | DiffusionGemma 26B NVFP4, single read, Vertex AI dedicated endpoint (G4, 1× RTX PRO 6000) | `--engine diffusion` |
| J1c | Same model on Cloud Run RTX PRO 6000, through the dgem gateway with `X-DGem-Backend: cloudrun` | `--diffusion-backend cloudrun` |
| J1g | Same model through the gateway's default `vertex_first` routing (latency only) | `--diffusion-backend vertex_first` |
| J2 | J1 plus a second read with the two responses swapped (pairwise only) | `--diffusion-mirror` |
| J3 | `gemini-3.5-flash-lite`, temperature 0 | genai structured output; native `EvaluateInstances` for pairwise |
| J4 | `gemini-3.8-flash`, temperature 0 | same |
| J5 | Entropy cascade: use J1's answer when its hesitation (normalized entropy) is below 16% or 35%, otherwise use the paired Gemini answer. Computed offline from the same runs. The thresholds are dgem's standard hesitation bands, not tuned on this data | `analyze.py` |
| J6 | Vertex predefined metrics (`safety`, `groundedness`, `fluency`, `coherence`), judged by the service's own model | `kind: prebuilt` |

Every DiffusionGemma result on both dates came back in the structured-readout format (`readout_mode: envelope`). Mizan now refuses free-form replies, which is the failure that affected Experiment 05. Gemini calls ran in the dgem hosting project. The DiffusionGemma model name in the reports (`diffgemma-26b-a4b-it-q4`) is only the client-side label, which the server echoes back. The serving identity is the `/health` record in `backends.json`.

## 3. Results (2026-09-29, v0.1.0)

Accuracy is shown with a bootstrap 95% CI, n = 100 per suite. Each "vs" pair ran in one run on the same items, and p is the exact McNemar test on that pair. The 09-26 value follows in parentheses.

### 3.1 Binary judgements

| Capability (dataset, gold) | J4 3.8-flash | J1 dgem G4 | p | J3 flash-lite | J1c dgem Cloud Run | p | J6 Vertex prebuilt | dgem prebuilt mapping | p |
|---|---|---|---|---|---|---|---|---|---|
| Response safety (BeaverTails `is_safe`) | 76 (75) | **80** (76) | 0.34 | 79 (80) | 79 (77) | 1.0 | 81 (79) | 68 (72) | **0.0002** |
| Prompt toxicity (ToxicChat, human) | **87.9**¹ (89.8) | 81 (79) | 0.065 (0.003) | 85.7¹ (83.7) | 81 (77) | 0.18 | — | — | |
| Answer faithfulness (HaluBench, 5 sources) | 78 (76) | 81 (82) | 0.72 | 81 (79) | 84 (85) | 0.65 | 79 (82) | 79 (77) | 1.0 |

¹ Gemini returned an empty response on the same 1–2 items (`tc-640`, `tc-3970`) on every attempt, on both dates. Those items count as unscored.

### 3.2 Pairwise preference

| Capability (dataset, gold) | J4 3.8-flash | J1 dgem G4 | p | J2 dgem + mirror | J3 flash-lite | J1c dgem Cloud Run | dgem position consistency |
|---|---|---|---|---|---|---|---|
| Chosen vs rejected (RewardBench, 10 subsets) | 90 (90) | 87 (86) | 0.51 | **90** (91) | 91 (90.8) | 87 (86) | 88% (88%) |
| Instruction following (LLMBar natural + adversarial) | **94** (93) | 79 (79) | **0.0015** (0.003) | 82 (79) | 82 (85) | 79 (77) | 73% (73%) |
| Expert preference, turn 1 (MT-Bench human, 20% ties) | 78 (76) | 73 (67) | 0.36 (0.035) | 73 (71) | 68 (69) | 72 (68) | 80% (78%) |
| Expert preference, multi-turn (MT-Bench turn 2) | 71 (71) | 65 (61) | 0.21 (0.021) | 64 (68) | 66² (65.7) | 65 (64) | 76% (80%) |

² Vertex native pairwise answered `Autorater responses could not be parsed` on the same 3 items on every retry.

"Position consistency" is the share of items where dgem picks the same winner after the two responses are swapped.

### 3.3 Likert scales

Spearman correlation with the human mean, then MAE. The 09-26 Spearman is in parentheses.

| Capability | J4 3.8-flash | J1 dgem G4 | J3 flash-lite | J1c dgem Cloud Run | J6 prebuilt | dgem prebuilt mapping |
|---|---|---|---|---|---|---|
| Helpfulness 0–4 (HelpSteer2) | 0.695 / 0.77 (0.681) | 0.652 / 0.90 (0.652) | 0.665 (0.663) | 0.652 (0.651) | — | — |
| Summary coherence 1–5 (SummEval experts) | 0.778 / 0.70 (0.777) | 0.742 / 0.89 (0.719) | 0.773 (0.787) | 0.739 (0.711) | 0.712 (0.758) | 0.640 (0.558) |
| Summary fluency 1–5 (SummEval experts, n = 90) | — | — | — | — | 0.658 (0.651) | **0.688** (0.709) |

Exact-match accuracy within ±0.5 of the human mean is 34–46% for every judge, so read Spearman.

### 3.4 No autorater (J0; identical on both dates)

| Metric family | Items | Local Go vs reference | Vertex `EvaluateInstances` vs the same reference |
|---|---|---|---|
| `exact_match` | 100 | 100% | 100% |
| `bleu` vs `sacrebleu.sentence_bleu` 2.6.0 | 100 | **100%** (to 1e-6) | 50% within ±0.005; median gap 0.005, max 0.12 |
| `rouge` (`rougeLsum`) vs `rouge_score` | 100 | **100%** | 28% within ±0.005; median gap 0.025, max 0.23 |
| Tool calls (`tool_call_valid`, `tool_name_match`, `tool_parameter_kv_match`) and trajectories (`exact`, `in_order`, `precision`, `recall`) | 350 | — (synthetic, values known) | **100% agreement with local** (κ = 1.0) |

### 3.5 Entropy cascade (J5, dgem G4 → gemini-3.8-flash)

Cascade accuracy is followed by the share of items escalated to Gemini.

| Suite | dgem alone | Gemini alone | Cascade at h ≥ 35% | Cascade at h ≥ 16% |
|---|---|---|---|---|
| Safety | 80 | 76 | **81** (8%) | 80 (15%) |
| Faithfulness | 81 | 78 | **86** (32%) | 83 (44%) |
| Toxicity | 81 | 87.9 | 83.8 (12%) | 84.8 (17%) |
| RewardBench | 87 | 90 | **91** (31%) | 91 (56%) |
| LLMBar | 79 | 94 | 94 (58%) | 94 (80%) |
| MT-Bench turn 1 | 73 | 78 | 75 (41%) | 77 (63%) |
| Helpfulness, Likert | 35 | 43 | 34 (34%) | 35 (54%) |

### 3.6 Latency (serial probe: 30 items, one request in flight)

| Path | p50 | p95 | 09-26 p50 |
|---|---|---|---|
| dgem, Vertex G4 dedicated endpoint (direct `/invoke`) | **105 ms** | 119 ms | 97 ms |
| dgem via the gateway, `vertex_first` (all 30 served by Vertex) | 169 ms | 197 ms | — |
| dgem, Cloud Run RTX PRO 6000 via the gateway | 182 ms | 201 ms | 178 ms |
| `gemini-3.5-flash-lite` | 1,429 ms | 1,780 ms | 998 ms |
| `gemini-3.8-flash` | 3,125 ms | 6,446 ms | 3,050 ms |
| Vertex predefined metrics | 3.9–12.6 s (batch, 4 workers) | | 3.7–12 s |
| Local computation metrics | < 1 ms | | < 1 ms |

The mirror read (J2) doubles dgem latency: 147–203 ms becomes 289–407 ms. The gateway adds about 65 ms to a Vertex call.

## 4. Analysis

1. **v0.1.0 did not regress.**
   - Paired on identical items across all suites, dgem on Vertex scored 67.0% on 09-26 and 67.9% on v0.1.0 (1,720 paired verdicts, p = 0.23). Cloud Run went from 68.6% to 69.8% (930 verdicts, p = 0.21).
   - No single dgem suite changed significantly.
   - The Gemini judges were just as stable: 3.8-flash went from 74.0% to 74.5%, flash-lite from 72.2% to 71.5%.
   - Across the roughly 60 per-suite comparisons, exactly one is below 0.05: flash-lite on helpfulness, 47 → 40, p = 0.016. That is about what chance alone would produce at this many tests.
2. **With no autorater, mizan's local implementation is exact.**
   - Exact match, tool-call and trajectory metrics match Vertex item for item.
   - BLEU and ROUGE match the reference libraries. Vertex's versions rank items almost the same way (Spearman 0.99) but differ in value by up to 0.12 and 0.23, and the Vertex docs don't explain the difference. Don't mix scores from the two sources.
3. **On binary rubric-style judgements, dgem is at parity.**
   - Safety and faithfulness are within noise of both Gemini models and of the Vertex predefined metric on both dates.
   - The toxicity gap (7–11 points) shrank to borderline significance on v0.1.0: p = 0.065, against 0.003 on 09-26.
   - dgem's mapping of the Vertex predefined safety metric is still clearly worse than a purpose-written template (68 vs 80). That mapping sees only the response, not the user prompt, and the gap is significant against the Vertex metric itself (p = 0.0002). **Use the template, not the predefined-metric mapping.**
4. **Pairwise preference is where an autoregressive judge keeps an edge, but only instruction following is clear-cut.**
   - **LLMBar:** gemini-3.8-flash leads by 14–15 points, significant on both dates.
   - **MT-Bench turn 1 and turn 2:** Gemini leads by 5–10 points on both dates. That was significant on 09-26 but not on 09-29, because dgem gained 4–6 points (within its own noise). The consistent direction still favours Gemini.
   - **RewardBench:** dgem plus the mirror read ties Gemini at 90.
   - The mirror changes accuracy by −4 to +5 points, which is not significant.
   - dgem picks a different winner after swapping the responses on 12–27% of items, the same as on 09-26. This position bias remains the main thing to fix.
5. **Likert:** every judge correlates with humans at 0.64–0.78 (Spearman). Gemini 3.8 is ahead by 0.04, and dgem beats the Vertex predefined fluency metric on both dates.
6. **The cascade works where dgem's hesitation tracks its errors.**
   - At 8–32% escalation it beats both judges alone on safety (81) and faithfulness (86), and matches Gemini on RewardBench (91 at 31%).
   - On LLMBar and MT-Bench it needs 41–80% escalation to approach Gemini, so it saves little.
   - On Likert helpfulness it doesn't help at all.
7. **Backends:** Vertex G4, Cloud Run and gateway `vertex_first` give the same accuracy within noise. The dedicated endpoint is fastest (105 ms), the gateway adds about 65 ms, and Cloud Run adds about 75 ms.
8. **Reliability improved.**
   - On 09-26, gemini-3.8-flash queued requests for 80–150 s and the Vertex predefined metrics failed 20–34% of first attempts. Those items were retried rather than dropped.
   - On 09-29 the whole rerun took 24 minutes; 11 items errored on the first attempt. After retries, only deterministic failures remained: 3 empty Gemini responses and 3 unparseable native pairwise replies.
   - dgem had no model-side failures on either date.

## 5. Capability matrix

| Vertex Gen AI eval capability | Needs a model? | Verdict | Evidence |
|---|---|---|---|
| Computation: exact match, BLEU, ROUGE | No | **Local, no credentials.** Exact match is Vertex-exact; BLEU/ROUGE follow the reference libraries | §3.4 |
| Tool-call and trajectory metrics | No | **Local, no credentials**, identical to Vertex | §3.4 |
| Pointwise binary safety / harm | Yes | **dgem can replace Gemini** with a purpose-written template (parity on both dates; about 30× lower latency). Avoid the prebuilt-safety mapping | §3.1 |
| Groundedness / faithfulness | Yes | **dgem can replace Gemini**; the cascade beats either alone | §3.1, §3.5 |
| Prompt toxicity / jailbreak detection | Yes | **dgem as a front filter plus cascade** (−7 to −11 points alone; −4 at 12% escalation) | §3.1, §3.5 |
| Likert quality | Yes | **Close to parity** on rank correlation; use Gemini where absolute scores matter | §3.3 |
| Pairwise preference, general (RewardBench) | Yes | **dgem plus mirror, or the cascade, can replace Gemini** | §3.2, §3.5 |
| Pairwise expert and multi-turn (MT-Bench) | Yes | **Prefer an autoregressive judge.** Gemini leads by 5–10 points on both dates, significant on one | §3.2 |
| Pairwise instruction following (LLMBar) | Yes | **Autoregressive judge required** (−14 to −15 points, significant on both dates) | §3.2 |
| Adaptive rubric *generation* | Yes | **Autoregressive only.** dgem can *score* generated rubrics as yes/no slots | design |
| Audio / video judging, written rationales | Yes | **Autoregressive only** (dgem takes at most one image and returns probabilities, not prose) | design |

## 6. Limitations

- **Statistical power:** n = 100 per suite gives 95% CIs about ±8 points wide. The two dates are replications on the same items, not independent samples, so they can't be pooled into a larger test. Verdicts rest on a consistent direction across both dates plus significance on at least one.
- **Templates:** each suite uses one template, and dgem is more sensitive to wording and to which fields it sees (the prebuilt-safety mapping gap above).
- **Predefined-metric judge:** the Vertex predefined metrics use the service's judge, documented as `gemini-2.5-flash`, and the API rejects any judge override. Gemini 2.5 retires on 2026-10-20, so J6 numbers may change when the service switches judges. mizan's built-in default autorater moved to `gemini-3.5-flash` in PR #109, but that affects templates that pin no model, not J6.
- **Context limit:** dgem's Cloud Run deployment has a 4,096-token context. Mizan now sends each field once, which is what keeps the long HaluBench passages under that limit.
- **Ties and contamination:** 20% of MT-Bench labels are ties, and neither engine predicts ties well. LLMBar and MT-Bench are public and may appear in Gemini's training data.
- **Cascade:** it was simulated from paired runs, not served live. Its latency is dgem plus Gemini on escalated items.

## 7. Reproduce

```bash
python3 scripts/judge-eval/build_suites.py --check       # rebuild suites; must match manifest.json
./bin/mizan registry import <mizan-templates checkout> --strategy overwrite
export MIZAN_PROJECT_ID=<project> DGEM_VERTEX_ENDPOINT=<…/endpoints/<id>> DGEM_GATEWAY_URL=<https://<your-dgem-gateway>/v1>
export RUN_DIR=docs/experiments/judge-eval/runs/<yyyymmdd>
TRACK=fast ./scripts/judge-eval/run_all.sh & TRACK=slow ./scripts/judge-eval/run_all.sh   # separate MIZAN_REGISTRY_DB per track
./scripts/judge-eval/retry_errors.sh $(ls $RUN_DIR/*.json | grep -v -e retry -e summary -e backends)
python3 scripts/judge-eval/analyze.py $RUN_DIR
python3 scripts/judge-eval/compare_runs.py docs/experiments/judge-eval/runs/20260926 $RUN_DIR
```
