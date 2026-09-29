# Experiment 07: Judge Capabilities With and Without an Autoregressive Autorater

* **Date**: 2026-09-26, one session. All Gemini and DiffusionGemma calls were made in this session; nothing is reused from Experiments 01–06.
* **Harness**: `mizan eval compare-engines`, report schema v2. Each engine is scored against the gold or human label first. Inter-engine agreement is reported only as a secondary number.
* **Suites**: 13 judge suites (1,290 items) from public human-labelled datasets, plus 2 computation suites (650 items). Build them with `scripts/judge-eval/build_suites.py`. Row indices, dataset revisions, licences and SHA-256 are in [`judge-eval/manifest.json`](judge-eval/manifest.json). The suite files themselves are gitignored because several upstream licences are non-commercial.
* **Templates**: the `judge-eval` pack in mizan-templates (22 templates).
* **Run scripts**: `scripts/judge-eval/run_all.sh`, `retry_errors.sh` and `analyze.py`.
* **Results**: [`judge-eval/runs/`](judge-eval/runs/). One report per run, with [`summary.md`](judge-eval/runs/summary.md) and `summary.json`.
* **Supersedes**: Experiments 04–06 (see the notes at the top of each).

## 1. Question

Mizan wraps the Vertex AI Gen AI Evaluation Service. This experiment asks three things:

1. Which of that service's capabilities can run with **no model at all**?
2. Which can use **DiffusionGemma**, a non-autoregressive decision model that reads typed slots, instead of a Gemini judge?
3. Where does an **autoregressive autorater** still earn its cost?

Earlier experiments measured agreement between the two engines. That hid cases where both engines were wrong, and in Experiment 05 DiffusionGemma's structured readout was never actually used. Here every verdict is scored against a human or gold label.

## 2. Judge configurations

| ID | Judge | Path |
|---|---|---|
| J0 | **No model**: mizan's local Go computation metrics vs Vertex `EvaluateInstances` computation metrics | `kind: computation` (new) |
| J1 | DiffusionGemma 26B NVFP4, single read, Vertex AI dedicated endpoint (G4, RTX PRO 6000) | `--engine diffusion` |
| J1c | Same model on Cloud Run RTX PRO 6000, through the dgem gateway with `X-DGem-Backend: cloudrun` pinned | `--diffusion-backend cloudrun` |
| J2 | J1 plus a swapped-order mirror read (pairwise only) | `--diffusion-mirror` |
| J3 | `gemini-3.5-flash-lite`, temperature 0 | genai structured output; native `EvaluateInstances` for pairwise |
| J4 | `gemini-3.8-flash`, temperature 0 | same |
| J5 | Entropy cascade: keep J1 when its hesitation (normalized entropy) is below 16% or 35%, otherwise use J4 (or J3 against J1c). Computed offline from the same paired runs; thresholds are fixed, not tuned on this data | `analyze.py` |
| J6 | Vertex predefined metrics (`safety`, `groundedness`, `fluency`, `coherence`) with the service's own judge | `kind: prebuilt` (new) |

Every DiffusionGemma result was a structured-readout envelope (`readout_mode: envelope` on 100% of items). Mizan now refuses the free-form fallback that affected Experiment 05.

## 3. Results

Accuracy is shown with a bootstrap 95% CI, n = 100 per suite unless noted. Each "vs" pair was run on the same items in the same run. "p" is the exact McNemar test on that pair.

### 3.1 Binary judgements

| Capability (dataset, gold) | J4 3.8-flash | J1 dgem G4 | p | J3 flash-lite | J1c dgem Cloud Run | p | J6 Vertex prebuilt | dgem prebuilt mapping |
|---|---|---|---|---|---|---|---|---|
| Response safety (BeaverTails `is_safe`) | 75 [67–83] | 76 [68–84] | 1.0 | 80 [72–88] | 77 [69–85] | 0.45 | 79 [71–87] | 72 [63–81], p = 0.04 |
| Prompt toxicity (ToxicChat, human) | **89.8** [84–95]¹ | 79 [71–86] | **0.003** | 83.7 [77–91]¹ | 77 [69–85] | 0.07 | — | — |
| Answer faithfulness (HaluBench, 5 sources) | 76 [68–84] | **82** [74–89] | 0.36 | 79 [71–87] | **85** [78–92] | 0.26 | 82 [74–89] | 77 [68–85] |

¹ Two items (the same two for both Gemini models) returned an empty response every time. They are counted as unscored, not as wrong.

### 3.2 Pairwise preference

| Capability (dataset, gold) | J4 3.8-flash | J1 dgem G4 | p | J2 dgem + mirror | J3 flash-lite | J1c dgem Cloud Run | dgem position consistency |
|---|---|---|---|---|---|---|---|
| Chosen vs rejected (RewardBench, 10 subsets) | 90 [83–95] | 86 [79–93] | 0.42 | **91** [85–96] | 90.8 [85–96]² | 86 [79–92] | 88% |
| Instruction following (LLMBar natural + adversarial) | **93** [88–98] | 79 [71–87] | **0.003** | 79 [71–86] | 85 [77–92] | 77 [68–85] | 73% |
| Expert preference, turn 1 (MT-Bench human, 20% ties) | **76** [68–84] | 67 [58–76] | **0.035** | 71 [62–79] | 69 [60–78] | 68 [59–77] | 78% |
| Expert preference, multi-turn (MT-Bench turn 2) | **71** [62–80] | 61 [51–70] | **0.021** | 68 [59–77] | 65.7 [56–75]² | 64 [54–73] | 80% |

² For 1–2 items, Vertex native pairwise answered `Autorater responses could not be parsed` on every retry.

### 3.3 Likert scales (gold is a human mean; accuracy counts |prediction − gold| ≤ 0.5)

| Capability | J4 Spearman / MAE | J1 dgem Spearman / MAE | J3 Spearman | J1c Spearman | J6 prebuilt Spearman | dgem prebuilt mapping Spearman |
|---|---|---|---|---|---|---|
| Helpfulness 0–4 (HelpSteer2) | 0.681 / 0.77 | 0.652 / 0.88 | 0.663 | 0.651 | — | — |
| Summary coherence 1–5 (SummEval experts) | 0.777 / 0.71 | 0.719 / 0.95 | **0.787** | 0.711 | 0.758 | 0.558 |
| Summary fluency 1–5 (SummEval experts, n = 90) | — | — | — | — | 0.651 | **0.709** |

### 3.4 No autorater (J0)

| Metric family | Items | Local Go vs its reference | Vertex `EvaluateInstances` vs the same reference |
|---|---|---|---|
| `exact_match` | 100 | 100% | 100% |
| `bleu` vs `sacrebleu.sentence_bleu` 2.6.0 | 100 | **100%** (to 1e-6) | 50% within ±0.005. Median gap 0.005, max 0.12 |
| `rouge` (`rougeLsum`) vs `rouge_score` | 100 | **100%** | 28% within ±0.005. Median gap 0.025, max 0.23 |
| Tool calls (`tool_call_valid`, `tool_name_match`, `tool_parameter_kv_match`) and trajectories (`exact`, `in_order`, `precision`, `recall`) | 350 | — (synthetic, values known) | **100% agreement with local** (κ = 1.0) |

### 3.5 Entropy cascade (J5)

Cascade accuracy is followed by the share of items escalated to Gemini. Thresholds are fixed hesitation bands, not tuned on this data.

| Suite | dgem alone | Gemini alone | Cascade at h ≥ 35% | Cascade at h ≥ 16% |
|---|---|---|---|---|
| Toxicity (vs 3.8-flash) | 79 | 89.8 | 83.7 (11%) | 86.7 (17%) |
| Safety (vs 3.8-flash) | 76 | 75 | **80** (10%) | 78 (14%) |
| Faithfulness (vs 3.8-flash) | 82 | 76 | **84** (31%) | 83 (38%) |
| LLMBar (vs 3.8-flash) | 79 | 93 | 92 (51%) | 91 (75%) |
| MT-Bench turn 1 (vs 3.8-flash) | 67 | 76 | 73 (48%) | 74 (69%) |
| RewardBench (vs 3.8-flash) | 86 | 90 | 90 (36%) | 89 (60%) |
| Helpfulness, Likert (vs 3.8-flash) | 37 | 44 | 37 (42%) | 39 (69%) |

### 3.6 Latency

| Path | p50 | p95 | Measured how |
|---|---|---|---|
| dgem, Vertex G4 dedicated endpoint | **97 ms** | 103 ms | Serial probe: 30 items, one request in flight |
| dgem, Cloud Run RTX PRO 6000 via gateway | **178 ms** | 254 ms | Same probe |
| `gemini-3.5-flash-lite` | 998 ms | 1,385 ms | Same probe |
| `gemini-3.8-flash` | 3,050 ms | 5,685 ms | Same probe |
| Vertex predefined metrics (`EvaluateInstances`) | 3.7–12 s | 11–30 s | Within the batch runs, 4 workers |
| Local computation metrics | < 1 ms | < 1 ms | Batch run |

Every pairwise mirror read (J2) doubled dgem latency: 147–209 ms became 275–398 ms.

## 4. Analysis

1. **Where no autorater is needed, mizan's local implementation is exact.** Exact match, tool-call and trajectory metrics match Vertex item for item. BLEU and ROUGE match the reference libraries (sacrebleu, rouge_score). Vertex's BLEU and ROUGE-Lsum do not: they rank items almost the same way (Spearman 0.99), but their absolute values differ by up to 0.12 and 0.23. Either Vertex does not compute these the same way as the reference libraries, or it tokenizes differently. Nothing in the docs says which. Scores from the two sources should not be mixed.
2. **For binary, rubric-like judgements, DiffusionGemma matches the Gemini judges on safety and faithfulness and is behind on toxicity.**
   - Safety and faithfulness are within noise of both Gemini models and of the Vertex predefined metric. dgem was numerically ahead on faithfulness (82–85 vs 76–79), but that difference is not significant.
   - Toxicity on real chatbot traffic, which includes jailbreaks, is behind `gemini-3.8-flash` by 11 points, and that gap is significant (p = 0.003).
   - Mapping the Vertex predefined safety metric onto dgem, rather than using a purpose-written template, costs 4–5 points (72 vs 76–77). Part of the gap is that the predefined metric sees only the response, while the template also sees the user prompt. Part is wording. Either way, the template matters more for dgem than for Gemini.
3. **Pairwise preference is the clearest place where an autoregressive judge still wins.**
   - On LLMBar, MT-Bench turn 1 and MT-Bench turn 2, `gemini-3.8-flash` beats DiffusionGemma by 9–14 points, significant each time.
   - On RewardBench, dgem with a mirror read reaches 91 against Gemini's 90.
   - The mirror is worth 0–4 points (not significant). dgem picks a different winner when the two responses are swapped on 12–27% of items. That position bias is the main weakness to fix before dgem can replace a pairwise judge.
   - `gemini-3.5-flash-lite` is no better than dgem on any pairwise suite except LLMBar (85 vs 77, p = 0.10).
4. **On Likert scales, every judge is close.** Spearman against humans is 0.65–0.79 for all of them. Gemini leads by about 0.03–0.07. dgem beats the Vertex predefined fluency metric (0.709 vs 0.651), but its mapping onto the predefined coherence metric is weak (0.558). Hitting the exact human score within ±0.5 is hard for everyone (35–47%), so read Spearman rather than that accuracy figure.
5. **The cascade gets most of Gemini's accuracy for a fraction of the Gemini calls, but only where dgem's uncertainty tracks its errors.**
   - It helps on toxicity, safety, faithfulness and RewardBench. On safety and faithfulness it beats both judges alone (80 and 84), escalating 10–31% of items.
   - On the pairwise suites it needs 48–75% escalation to approach Gemini, so there is little saving.
   - On Likert helpfulness it doesn't help at all: dgem's hesitation does not predict its Likert errors.
6. **Backend parity holds.** dgem on Vertex G4 and on Cloud Run agree within noise on every suite (for example 76 vs 77, 79 vs 77, 82 vs 85). G4 is about 2× faster per request (97 vs 178 ms).
7. **Reliability varied more than accuracy.**
   - `gemini-3.8-flash` queued requests for 80–150 s under modest concurrency in both projects tried, while `gemini-3.5-flash-lite` and `gemini-3.7-flash` stayed under 2 s.
   - The Vertex predefined metrics returned `Unavailable` or `DeadlineExceeded` on 20–34% of first attempts.
   - Some connection resets happened while the benchmark sandbox was paused.
   - All of these were re-run with `retry_errors.sh`, which re-runs both engines on only the failed items. The retried items were harder than average: 3.8-flash scored 8 of 16 on the retried safety items against 67 of 84 on the rest. Dropping failures instead of retrying them would have inflated accuracy.
   - After retries, the only remaining failures were deterministic: the same 2 empty Gemini responses on ToxicChat (from both models) and 3 unparseable native pairwise replies.
   - dgem had no model-side failures. Its 3 transport errors on G4 happened during a sandbox pause and succeeded on retry.

## 5. Capability matrix

| Vertex Gen AI eval capability | Needs a model? | Verdict | Evidence |
|---|---|---|---|
| Computation: exact match, BLEU, ROUGE | No | **Local, no credentials** (and Vertex-exact for exact match; BLEU/ROUGE follow the reference libraries) | §3.4 |
| Tool-call and trajectory metrics | No | **Local, no credentials**, identical to Vertex | §3.4 |
| Pointwise binary safety / harm | Yes | **dgem can replace Gemini** (parity with 3.8-flash, flash-lite and the Vertex prebuilt metric; 30× lower latency) | §3.1 |
| Groundedness / faithfulness | Yes | **dgem can replace Gemini**; the cascade is better than either alone | §3.1, §3.5 |
| Prompt toxicity / jailbreak detection | Yes | **dgem as front filter + cascade** (−11 points alone; −3 points with 17% escalated) | §3.1, §3.5 |
| Likert quality (helpfulness, coherence, fluency) | Yes | **Close to parity** on rank correlation; use Gemini where absolute scores matter | §3.3 |
| Pairwise preference, general (RewardBench) | Yes | **dgem + mirror can replace Gemini** | §3.2 |
| Pairwise instruction following, expert preference, multi-turn | Yes | **Autoregressive judge required** (−9 to −14 points, significant; the cascade needs ≥ 48% escalation) | §3.2, §3.5 |
| Adaptive rubric *generation* | Yes | **Autoregressive only** (dgem reads slots and cannot write rubrics); dgem can *score* generated rubrics as boolean slots | design |
| Audio / video judging | Yes | **Autoregressive only** (dgem accepts at most one image) | design |
| Explanations / rationales | Yes | **Autoregressive only** (dgem returns probabilities, not prose) | design |

## 6. Limitations

- n = 100 per suite gives 95% CIs about ±8 points wide. Differences under about 10 points are not significant unless the McNemar test says so.
- Each suite uses one template, and prompt wording affects both engines. dgem is more sensitive to it (the safety template vs predefined-metric mapping gap is 4–5 points).
- Gemini calls used `ghchinoy-genai-sa` because the dgem project's 3.8-flash quota was saturated by other runs. That affects latency only. Gemini thinking budgets were left at the model defaults.
- The DiffusionGemma Cloud Run context is 4,096 tokens. The first J1c groundedness run lost 4 long items before mizan stopped sending each field twice; the runs reported here are after that fix.
- MT-Bench labels include 20% ties, and neither engine predicts ties well. RewardBench's LLMBar subsets and MT-Bench are well known and may appear in Gemini's training data.
- The cascade is simulated from paired runs, not served live. Its latency is the dgem time plus the Gemini time for escalated items.

## 7. Reproduce

```bash
python3 scripts/judge-eval/build_suites.py            # needs network; pyarrow recommended
./bin/mizan registry import <mizan-templates checkout> --strategy overwrite
export MIZAN_PROJECT_ID=<project> DGEM_VERTEX_ENDPOINT=<…/endpoints/<id>> DGEM_GATEWAY_URL=<https://<your-dgem-gateway>/v1>
TRACK=fast ./scripts/judge-eval/run_all.sh & TRACK=slow ./scripts/judge-eval/run_all.sh
./scripts/judge-eval/retry_errors.sh $(ls docs/experiments/judge-eval/runs/*.json | grep -v -e retry -e summary)
python3 scripts/judge-eval/analyze.py                 # writes runs/summary.{md,json}
```

To check that rebuilt suites are identical to the ones used here, run `build_suites.py --check`.
