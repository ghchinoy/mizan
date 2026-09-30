# Experiment 07: Judge Capabilities With and Without an Autoregressive Autorater

* **Primary session:** 2026-09-29, all engines rerun on the stable **dgem v0.1.0** serving image (`version v0.1.0`, `revision f241b77`, vLLM `a9eafde5`, recorded in [`runs/20260929-v010/backends.json`](judge-eval/runs/20260929-v010/backends.json)). Folder: [`runs/20260929-v010/`](judge-eval/runs/20260929-v010/).
* **Replication:** 2026-09-26, same suites, templates and engines, on the pre-v0.1.0 serving images. Folder: [`runs/20260926/`](judge-eval/runs/20260926/). The item-by-item comparison is in [`compare_vs_20260926.md`](judge-eval/runs/20260929-v010/compare_vs_20260926.md).
* **Harness:** `mizan eval compare-engines`, report schema v2. Each engine is scored against the gold or human label first; agreement between engines is secondary.
* **Suites:** 13 judge suites (1,290 items) from public human-labelled datasets, plus 2 computation suites (650 items).
  - Built by `scripts/judge-eval/build_suites.py`, which pins row indices, dataset revisions, licences and SHA-256 in [`judge-eval/manifest.json`](judge-eval/manifest.json).
  - Before the 09-29 run, `build_suites.py --check` rebuilt all 15 suites byte-identical to the manifest.
  - The suite files are gitignored because several upstream licences are non-commercial.
* **Templates:** the `judge-eval` pack in mizan-templates (22 templates).
* **Scripts:** `scripts/judge-eval/run_all.sh`, `retry_errors.sh`, `analyze.py` and `compare_runs.py`; for speed and cost, `sweep.py`, `run_speed.sh` and `speed_report.py`.
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
| J5 | Entropy cascade: use J1's answer when its hesitation (normalized entropy) is below 16% or 35%, otherwise use the paired Gemini answer. Computed offline from the same runs. Both thresholds were fixed before this data was seen and not tuned on it: 16% is dgem's normalized-entropy gate (EXP-05b) and the edge of the Studio's "Clear" band; 35% reuses the value of dgem's earlier raw-entropy cascade default (0.35 nats) on the normalized scale. It is not a dgem band (those are 16% and 50%). A 35% gate means escalating when dgem is less than about 93% sure on a yes/no, or 90% on A/B/tie. Sweeping the gate on these runs, pooled accuracy stays flat (77.0–77.6%) from 5% to 35% while escalation falls from 68% to 36%, then drops above 35%; see §3.5. | `analyze.py` |
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

**Gate sweep** (dgem G4 → gemini-3.8-flash, 8 suites, 799 paired items, offline). The share escalated is in parentheses.

| Gate | 0 (always Gemini) | 0.05 | 0.16 | 0.25 | **0.35** | 0.50 | 0.70 | never (dgem only) |
|---|---|---|---|---|---|---|---|---|
| Pooled accuracy | 77.2% (100%) | 77.6% (68%) | 77.0% (53%) | 77.1% (44%) | **77.0% (36%)** | 76.1% (25%) | 74.5% (14%) | 72.6% (0%) |

How well hesitation predicts dgem's own errors (AUROC, where 0.5 means no signal) varies by suite:

| Suite | AUROC |
|---|---|
| Faithfulness | 0.88 |
| Pairwise (four suites) | 0.68–0.76 |
| Toxicity | 0.71 |
| Likert | 0.59 |
| Safety | 0.53 |

This is why the cascade beats both judges on faithfulness but does little on safety.

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


## 3b. Speed and cost (Experiment 07b, 2026-09-29, same v0.1.0 deployments)

Folders: [`runs/20260929-throughput/`](judge-eval/runs/20260929-throughput/) (`scripts/judge-eval/sweep.py`) and [`runs/20260929-speed/`](judge-eval/runs/20260929-speed/) (`scripts/judge-eval/run_speed.sh`). The tables are generated by `scripts/judge-eval/speed_report.py`.

- **Throughput sweep:** each run exercised one engine, with `--engine-b none`. Each concurrency level was a fresh batch of 48–256 items drawn from the suite, after 2 warm-up calls, and items/s is measured against the batch's wall clock.
- **Production load:** the G4 endpoint is the shared production deployment, running as-is with 1–2 replicas and `MAX_INFLIGHT=8`.
- **Live cascade:** served through mizan's `cascade` engine. DiffusionGemma answers first, and Gemini is called only when DiffusionGemma's hesitation is ≥ 0.35.
- **Thinking budget:** each comparison pairs the same Gemini model on the same items, once with the default thinking budget and once with budget 0 (`--thinking-budget 0 --thinking-side b`). Pairwise items go through genai structured output on both sides, because native `EvaluateInstances` has no thinking control.

### 3.7 Throughput sweep (one engine per run; items/s from batch wall clock)

| Target | Suite | c=1 | c=4 | c=8 | c=16 | c=32 | p50 / p95 at c=1 (ms) | p50 / p95 at c=32 (ms) | errors |
|---|---|---|---|---|---|---|---|---|---|
| dgem-g4 | safety_response | 10.5 | 31.4 | 58.6 | 82.8 | 83.2 | 94 / 102 | 373 / 419 | 0/784 |
| dgem-g4 | groundedness | 9.6 | 26.8 | 61.9 | 68.3 | 67.3 | 100 / 126 | 469 / 513 | 0/784 |
| dgem-cloudrun | safety_response | 5.5 | 20.1 | 37.6 | 66.9 | 77.6 | 179 / 247 | 397 / 505 | 0/784 |
| dgem-cloudrun | groundedness | 5.6 | 17.4 | 28.9 | 39.4 | 38.3 | 176 / 208 | 763 / 1075 | 0/784 |
| gemini-3.5-flash-lite | safety_response | 0.9 | 3.5 | 6.6 | 13.8 | 28.6 | 1066 / 1292 | 1010 / 1463 | 0/784 |
| gemini-3.5-flash-lite | groundedness | 0.8 | 3.4 | 7.5 | 13.5 | 18.6 | 1230 / 1775 | 1000 / 2142 | 0/784 |
| gemini-3.8-flash | safety_response | 0.3 | 1.1 | 2.3 | 4.1 | 6.9 | 2966 / 4735 | 2885 / 5636 | 4/784 |
| gemini-3.8-flash | groundedness | 0.2 | 0.9 | 2.0 | 3.7 | 3.0 | 3220 / 10310 | 3280 / 14918 | 0/784 |

Sustained G4 load at 32 concurrent requests: 18,000 requests over 212 s, **84.9 items/s**, p50 376 ms, p95 424 ms, p99 435 ms, 0 errors. The endpoint stayed at 1 replica for the whole run (polled every 20 s).

### 3.8 Live cascade (dgem G4 -> gemini-3.8-flash at hesitation >= 0.35)

| Suite | Mode | Accuracy [95% CI] | Escalated | p50 | p95 | p99 | Simulated (09-29) accuracy / escalated | Gemini alone (09-29) |
|---|---|---|---|---|---|---|---|---|
| safety_response | serial | 81.0 [73-89] | 7% | 94 | 3491 | 4605 | 81 / 8% | 76.0 |
| safety_response | 8 workers | 80.0 [72-87] | 8% | 128 | 3894 | 5074 | 81 / 8% | 76.0 |
| groundedness | serial | 85.0 [78-92] | 32% | 108 | 7628 | 11378 | 86 / 32% | 78.0 |
| groundedness | 8 workers | 86.0 [79-93] | 32% | 133 | 7341 | 15270 | 86 / 32% | 78.0 |
| toxicity_prompt | serial | 86.0 [79-92] | 14% | 95 | 5319 | 8755 | 84 / 12% | 87.9 |
| toxicity_prompt | 8 workers | 86.0 [79-92] | 13% | 127 | 5800 | 9118 | 84 / 12% | 87.9 |
| pairwise_rewardbench | serial | 90.0 [84-95] | 32% | 103 | 6162 | 9066 | 91 / 31% | 90.0 |
| pairwise_rewardbench | 8 workers | 89.0 [82-95] | 26% | 123 | 6036 | 9586 | 91 / 31% | 90.0 |

### 3.9 Gemini thinking budget: model default (A) vs budget 0 (B), same session, same items

| Model | Suite | Acc default | Acc budget 0 | only default right / only budget-0 right | McNemar p | p50 default / budget 0 (ms) | mean thinking tokens default / budget 0 |
|---|---|---|---|---|---|---|---|
| gemini-3.5-flash-lite | groundedness | 78.0 | 79.0 | 3 / 4 | 1 | 922 / 961 | 0 / 0 |
| gemini-3.5-flash-lite | pairwise_instruction_following | 77.0 | 78.0 | 3 / 4 | 1 | 929 / 971 | 0 / 0 |
| gemini-3.5-flash-lite | pairwise_mtbench | 67.0 | 71.0 | 1 / 5 | 0.219 | 1084 / 1073 | 0 / 0 |
| gemini-3.5-flash-lite | toxicity_prompt | 83.7 | 83.8 | 1 / 1 | 1 | 957 / 957 | 0 / 0 |
| gemini-3.8-flash | groundedness | 77.0 | 75.0 | 3 / 1 | 0.625 | 3724 / 2308 | 308 / 27 |
| gemini-3.8-flash | pairwise_instruction_following | 93.0 | 93.0 | 1 / 1 | 1 | 3069 / 2677 | 172 / 98 |
| gemini-3.8-flash | pairwise_mtbench | 78.0 | 77.0 | 3 / 2 | 1 | 3576 / 2595 | 346 / 116 |
| gemini-3.8-flash | toxicity_prompt | 87.8 | 85.7 | 3 / 1 | 0.625 | 2886 / 1997 | 202 / 47 |

### 3.10 Cost per 1,000 judgements (USD, list prices 2026-09-29)

- Vertex G4 dedicated endpoint (g4-standard-48 + RTX PRO 6000, incl. management fees): **$5.85/h per replica**, billed while deployed.
- Cloud Run `dgemma` (RTX PRO 6000 without zonal redundancy, 20 vCPU, 80 GiB, instance-based): **$3.19/h while an instance runs**; scales to zero.
- Gemini: standard-tier token prices (3.8-flash $0.75 in / $3.75 out per 1M through 2026-12-31, $1.50 / $7.50 from 2027; 3.5-flash-lite $0.30 / $2.50); thinking tokens bill as output.
- Vertex predefined metrics: $0.005 per 1k input characters + $0.015 per 1k output characters.

| Judge | Suite | Basis | USD per 1k |
|---|---|---|---|
| dgem-g4 | safety_response | at peak throughput 83/s (fully utilized) | **0.0195** |
| dgem-cloudrun | safety_response | at peak throughput 78/s (fully utilized) | **0.0114** |
| gemini-3.5-flash-lite | safety_response | measured 411 in / 58 out / 0 thinking tokens | **0.268** |
| gemini-3.8-flash | safety_response | measured 411 in / 55 out / 145 thinking tokens | **1.058** |
| gemini-3.8-flash (2027 price) | safety_response | same tokens | 2.117 |
| cascade dgem G4 -> 3.8-flash | safety_response | dgem on every item + Gemini on 8% | 0.104 |
| dgem-g4 | groundedness | at peak throughput 68/s (fully utilized) | **0.0238** |
| dgem-cloudrun | groundedness | at peak throughput 39/s (fully utilized) | **0.0224** |
| gemini-3.5-flash-lite | groundedness | measured 1064 in / 78 out / 0 thinking tokens | **0.514** |
| gemini-3.8-flash | groundedness | measured 1064 in / 74 out / 314 thinking tokens | **2.254** |
| gemini-3.8-flash (2027 price) | groundedness | same tokens | 4.508 |
| gemini-3.8-flash, thinking budget 0 | groundedness | measured 975 in / 93 out / 27 thinking | 1.181 |
| cascade dgem G4 -> 3.8-flash | groundedness | dgem on every item + Gemini on 32% | 0.745 |
| Vertex predefined `prebuilt_safety` | prebuilt_safety | ≥308 input chars (fields only; service template not counted) + 566 output chars | ≥10.03 |
| Vertex predefined `prebuilt_groundedness` | prebuilt_groundedness | ≥4858 input chars (fields only; service template not counted) + 419 output chars | ≥30.57 |
| Vertex predefined `prebuilt_fluency` | prebuilt_fluency | ≥275 input chars (fields only; service template not counted) + 1517 output chars | ≥24.13 |

Idle floor: one always-on G4 replica costs $140/day whether or not it serves traffic. At that cost, dgem on G4 is cheaper than gemini-3.8-flash per judgement once it serves more than roughly 132,666 safety judgements per day.


**Reading these tables**

- **DiffusionGemma on one G4 replica saturates at about 83 items/s on short inputs** (safety) **and 68 items/s on 1–3k-token passages** (groundedness).
  - It gets there by 16 concurrent requests, which matches the server's `MAX_INFLIGHT=8` limit plus batching.
  - Beyond that, extra concurrency only adds queueing: p50 at 32 concurrent is about 4× p50 at 1.
  - A 3.5-minute sustained run at 32 concurrent held 85 items/s with 0 errors. The endpoint's GPU-duty-cycle autoscaler did not add the second replica in that window, so plan capacity on one replica per ~80 items/s unless you pre-scale.
- **Cloud Run matches G4 on short inputs** (78 items/s) **but tops out at 39 items/s on long passages.** Its server time grows from 102 to 171 ms under load. Cloud Run is configured with a much smaller KV cache (`KV_CACHE_GB=2` vs 12 on Vertex) and a 4,096-token context, which limits batching of long prompts.
- **Gemini keeps a flat per-request latency but low per-client throughput at these concurrency levels.**
  - gemini-3.5-flash-lite reached 29 items/s at 32 concurrent, and 3.8-flash reached 7.
  - For 3.8-flash on long passages, p99 grew to 38 s at 32 concurrent, while median latency stayed flat at about 3.3 s.
  - Gemini's ceiling is set by project quota, not by a replica, so more concurrency would keep scaling until quota limits.
- **The live cascade reproduces the simulated one.**
  - Accuracy and escalation rate match the J5 simulation within 1–2 items on every suite.
  - Median latency stays at DiffusionGemma's (94–133 ms), because 7–32% of items escalate.
  - p95/p99 is Gemini's (3.5–15 s): whatever share escalates pays Gemini's full latency on top.
- **Turning Gemini thinking off saves 0.4–1.4 s on 3.8-flash at no measurable accuracy cost.** Every one of the 8 comparisons is within noise (p ≥ 0.22), with 3.8-flash moving by 0 to −2 points.
  - Budget 0 is not strictly zero: 3.8-flash still emitted 27–116 thinking tokens per item on average.
  - flash-lite already does not think at its default, so the setting changes nothing there.
  - Even with thinking off, 3.8-flash stays about 20× slower than DiffusionGemma on G4 (2.0–2.7 s vs about 105 ms).
- **Cost, at list prices:**
  - **Fully utilized:** DiffusionGemma costs about **$0.01–0.02 per 1,000 judgements**, 13–26× cheaper than gemini-3.5-flash-lite ($0.27–0.51) and 50–110× cheaper than gemini-3.8-flash ($1.06–2.25 at today's introductory price, double that from 2027).
  - **Idle cost:** the dedicated G4 replica costs $140/day whether or not it serves traffic. It beats 3.8-flash on cost only above roughly 130k safety judgements per day, or about 60k on long-passage groundedness. Cloud Run scales to zero and has no idle cost, but pays a ~2.5-minute cold start.
  - **The cascade costs $0.10–0.75 per 1,000**, depending on how often it escalates.
  - **The Vertex predefined metrics are by far the most expensive option**, at **≥ $10–31 per 1,000** judgements. They are billed per character at the legacy model-based rate, and their long explanations dominate the output charge. They are also the slowest (3.9–12.6 s p50).

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
8. **Speed and cost (§3b).** One G4 replica sustains about 80 items/s at about 0.4 s p50. Fully utilized, DiffusionGemma judges for about $0.02 per 1,000, against $1–2 for gemini-3.8-flash and ≥ $10 for the Vertex predefined metrics. The live cascade keeps DiffusionGemma's median latency and adds Gemini's tail on escalated items. Turning off Gemini thinking cuts 3.8-flash latency by 13–38% with no measurable accuracy loss, but it stays about 20× slower than DiffusionGemma.
9. **Reliability improved.**
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
| Anything above at high volume or low latency | — | **dgem**: about 80 items/s per G4 replica, about 0.1 s p50 when unloaded, about $0.02 per 1,000 at full use. Predefined metrics cost ≥ $10 per 1,000 and take 4–13 s | §3b |
| Adaptive rubric *generation* | Yes | **Autoregressive only.** dgem can *score* generated rubrics as yes/no slots | design |
| Audio / video judging, written rationales | Yes | **Autoregressive only** (dgem takes at most one image and returns probabilities, not prose) | design |

## 6. Limitations

- **Statistical power:** n = 100 per suite gives 95% CIs about ±8 points wide. The two dates are replications on the same items, not independent samples, so they can't be pooled into a larger test. Verdicts rest on a consistent direction across both dates plus significance on at least one.
- **Templates:** each suite uses one template, and dgem is more sensitive to wording and to which fields it sees (the prebuilt-safety mapping gap above).
- **Predefined-metric judge:** the Vertex predefined metrics use the service's judge, documented as `gemini-2.5-flash`, and the API rejects any judge override. Gemini 2.5 retires on 2026-10-20, so J6 numbers may change when the service switches judges. mizan's built-in default autorater moved to `gemini-3.5-flash` in PR #109, but that affects templates that pin no model, not J6.
- **Context limit:** dgem's Cloud Run deployment has a 4,096-token context. Mizan now sends each field once, which is what keeps the long HaluBench passages under that limit.
- **Ties and contamination:** 20% of MT-Bench labels are ties, and neither engine predicts ties well. LLMBar and MT-Bench are public and may appear in Gemini's training data.
- **Cascade:** J5 in §3.5 is simulated from paired runs. §3.8 serves the same cascade live, and it agrees within 1–2 items.
- **Cost:** list prices on 2026-09-29, with no committed-use or negotiated discounts.
  - DiffusionGemma cost per 1,000 assumes full utilization at the measured peak.
  - The predefined-metric cost is a lower bound: it counts only the field characters, not the service's own prompt template.
  - Gemini pairwise through native `EvaluateInstances` returns no token counts, so its cost is not measured.
- **Throughput:** the sweep ran against the shared production endpoint, and a short sweep does not trigger autoscaling. Gemini numbers are per project and depend on quota.

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
# speed and cost (07b)
python3 scripts/judge-eval/sweep.py --out <throughput dir> --targets dgem-g4 dgem-cloudrun
python3 scripts/judge-eval/sweep.py --out <throughput dir>/gemini --targets gemini-3.5-flash-lite gemini-3.8-flash
RUN_DIR=<speed dir> ./scripts/judge-eval/run_speed.sh
python3 scripts/judge-eval/speed_report.py --throughput <throughput dir> --speed <speed dir> --baseline $RUN_DIR
```
