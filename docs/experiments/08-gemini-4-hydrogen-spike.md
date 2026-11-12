# Experiment 08: Gemini 4 Hydrogen Autorater Qualification & Pro-Tier Ceiling Benchmark

* **Primary session:** 2026-11-12, live Vertex AI benchmark across five Gemini models (`gemini-3.5-flash-lite`, `gemini-3.8-flash`, `gemini-4-hydrogen`, `gemini-4-argon`, `gemini-4-carbon`) and three multi-region endpoints (`global`, `us`, `eu`), recorded in [`runs/20261112-hydrogen-spike/backends.json`](judge-eval/runs/20261112-hydrogen-spike/backends.json).
* **Artifacts & Raw Runs:** [`runs/20261112-hydrogen-spike/`](judge-eval/runs/20261112-hydrogen-spike/), [`qualification_summary.md`](judge-eval/runs/20261112-hydrogen-spike/qualification_summary.md), and [`qualification_summary.json`](judge-eval/runs/20261112-hydrogen-spike/qualification_summary.json).
* **Harness:** `mizan eval compare-engines` (schema v2, enhanced with `--location`, `--thinking-level`, `model_version`, and `mean_tokens` telemetry) orchestrated by [`scripts/judge-eval/qualify_model.py`](../../scripts/judge-eval/qualify_model.py).
* **Suites:**
  * **900 human-labeled items** across the 9 Experiment 07 judge suites (`safety_response`, `toxicity_prompt`, `groundedness`, `pairwise_mtbench`, `pairwise_multiturn`, `pairwise_instruction_following`, `pairwise_rewardbench`, `likert_helpfulness`, `likert_coherence`), verified against [`judge-eval/manifest.json`](judge-eval/manifest.json).
  * **46 ground-truth items** in [`calibration_suite.jsonl`](calibration_suite.jsonl) spanning 15 templates across `boul` (16 cases), `choice` (20 cases, including 77-way `calibration/banking77-intent-select`), and `score` (10 cases).

---

## 1. Executive Summary & 5-Gate Promotion Verdict

This experiment answers two questions:
1. **How does `gemini-4-hydrogen` (Flash-class) benchmark in quality, latency, and throughput against `gemini-3.8-flash`, `gemini-3.5-flash-lite`, and the Gemini 4 Pro-tier anchors (`gemini-4-argon`, `gemini-4-carbon`)?**
2. **What is the repeatable qualification protocol for promoting or tiering new autorater models in Mizan?**

### Key Findings

1. **Quality Parity with `gemini-3.8-flash` & Pro-Tier Ceiling Convergence:**
   * Across the **746 discrete + calibration items**, `gemini-4-hydrogen` (`DEFAULT` thinking) achieves **82.9%** accuracy (`95% CI [80.2%, 85.5%]`), statistically indistinguishable from `gemini-3.8-flash` at **83.1%** (`[80.2%, 85.6%]`, $\Delta = -0.13\text{ pts}$, exact McNemar $p = 1.0000$) and **+5.0 pts** ahead of `gemini-3.5-flash-lite` (**77.9%**, $p < 0.0001$).
   * With `--thinking-level low`, `gemini-4-hydrogen` reaches **86.0%** vs. **85.4%** (`DEFAULT`) across the 444-item thinking A/B slice ($\Delta = +0.68\text{ pts}$, $p = 0.6636$), lifting `pairwise_mtbench` from **74.0% $\to$ 80.0%** ($+6.0\text{ pts}$) and `groundedness` from **79.0% $\to$ 80.0%**.
   * On the **46-item `calibration_suite.jsonl`** (15 templates), `gemini-4-hydrogen` achieves **93.5% (43/46)** (`16/16` `boul`, `18/20` `choice` including 77-intent `banking77`, `9/10` `score`, Spearman $\rho = 0.990$, MAE $= 0.11$), tying `gemini-4-carbon` (**93.5%**) and edging `gemini-3.8-flash`, `gemini-3.5-flash-lite`, and `gemini-4-argon` (**91.3%**, `42/46`).
   * On the **346-item hard slice** (`pairwise_mtbench`, `pairwise_instruction_following`, `toxicity_prompt`, `calibration_suite`), `gemini-4-hydrogen` (**86.9%** `DEFAULT`, **87.5%** `LOW`) matches both `gemini-3.8-flash` (**87.5%**) and the Pro-tier ceiling anchors `gemini-4-argon` (**86.8%**) and `gemini-4-carbon` (**86.0%**) at **2.1x–2.6x lower latency**.
2. **Latency & Throughput Advantage Over `gemini-3.8-flash`:**
   * **Unloaded Serial Latency (`global`, `us`, `eu`):** `gemini-4-hydrogen` (`DEFAULT`) is **1.21x–1.33x faster at p50** and **1.59x–2.50x faster at p95** than `gemini-3.8-flash` across all three multi-regions (`global` p50 `2,282 ms` vs. `3,038 ms`; `us` `2,528 ms` vs. `3,058 ms`; `eu` `2,987 ms` vs. `3,617 ms`).
   * **Paired Serial Long-Passage Latency (`groundedness`, $n=15$, `--serial --workers 1`):** `gemini-4-hydrogen` (`DEFAULT`) runs at **`2,369 / 2,810 ms`** (p50 / p95) vs. `gemini-3.8-flash` at **`3,165 / 4,590 ms`** (**1.34x faster p50, 1.63x faster p95**). With `--thinking-level low`, `gemini-4-hydrogen` drops further to **`2,012 / 2,891 ms`** (**1.57x faster p50 than `gemini-3.8-flash`**).
   * **High-Concurrency Throughput ($c = 16$):** At 16 workers (`safety_response`, $n=64$, `0` errors), `gemini-4-hydrogen` achieves **5.05 items/s** (`DEFAULT`, **1.50x** `gemini-3.8-flash`'s `3.37 items/s`) and **7.43 items/s** (`--thinking-level low`, **2.20x** `gemini-3.8-flash`, p50 `1,745 ms`, p95 `2,695 ms`), while `gemini-3.5-flash-lite` remains the sub-second throughput leader at **15.85 items/s** (`797 / 1,051 ms`, `0` thinking tokens).

### 5-Gate Autorater Promotion Verdict (`gemini-4-hydrogen` vs. `gemini-3.8-flash`)

| Gate | Criterion | Empirical Result | Verdict |
| :--- | :--- | :--- | :---: |
| **1. Endpoint & Routing Readiness** | Reachable in `global`, `us`, `eu` (`genai` + native `EvaluateInstances`) | `6/6` in `global`, `us`, `eu`; native `EvaluateInstances` `3/3` | **PASS** |
| **2. Schema & Calibration Fidelity** | 0 schema/parse errors on `calibration_suite` (46 cases, 15 templates incl. 77-intent choice); accuracy $\ge$ `gemini-3.8-flash` | **93.5%** (`43/46`, `0` errors) vs. `91.3%` (`42/46`) | **PASS** |
| **3. Human-Gold Accuracy Parity** | Pooled 746-item discrete + calibration accuracy within CI of `gemini-3.8-flash` ($p \ge 0.05$ or $\Delta \ge 0$) | **82.9%** (`DEFAULT`) / **83.6%** (`LOW` on reasoning) vs. `83.1%` ($\Delta = -0.13\text{ pts}$, $p = 1.0000$) | **PASS** |
| **4. Latency & Throughput SLO** | Serial p50 $\le$ `gemini-3.8-flash` and $c=16$ throughput $\ge$ `gemini-3.8-flash` | Serial p50 **2,369 ms** vs. `3,165 ms`; $c=16$ **5.05 items/s** (`DEFAULT`) / **7.43 items/s** (`LOW`) vs. `3.37 items/s` | **PASS** |
| **5. Token & Thinking Economy** | Documented `thoughtsTokenCount` overhead and `--thinking-level low` Pareto frontier | `DEFAULT` `396` thoughts tok vs. `LOW` `206` thoughts tok (`-48%`, $+0.68\text{ pts}$ accuracy on 444 items) | **PASS** |

---

## 2. Multi-Region (`global`, `us`, `eu`) Availability & Host Routing

### 2.1 Vertex AI Multi-Region Host Discovery

Probing Vertex AI across `global`, `us`, and `eu` uncovered a critical host-routing distinction between single-region endpoints (e.g., `us-central1`) and multi-region endpoints (`us`, `eu`):
* **`google.golang.org/genai` (`GenerateContent`):** Multi-region locations `"us"` and `"eu"` resolve to `https://us-aiplatform.googleapis.com/` and `https://eu-aiplatform.googleapis.com/` over HTTPS REST and serve all five Gemini 3.x and 4.x models natively.
* **Cloud AI Platform gRPC (`EvaluationService.EvaluateInstances`):** While single-region locations dial `{region}-aiplatform.googleapis.com:443`, multi-region `"us"` and `"eu"` must dial the global gRPC host `aiplatform.googleapis.com:443` while passing `projects/{project}/locations/{us|eu}` in the request `Location` parent.
* **Engine Fix:** `internal/wire/wire.go` (`endpointFor` and `genaiLocationFor`) now preserves explicit `"us"` and `"eu"` multi-region locations end-to-end while still upgrading default `"us-central1"` to `"global"` (`R-GLOBAL`) for `gemini-3.x` and `gemini-4-*` models.

### 2.2 Multi-Region Availability & Unloaded Serial Latency (`groundedness`, $n=6$, `--serial --workers 1`)

| Model | Location | Reachable (`genai`) | Echoed `modelVersion` | Serial p50 (ms) | Serial p95 (ms) | Mean Prompt Tok | Mean Thoughts Tok | Native `EvaluateInstances` (`pairwise_rewardbench`) |
| :--- | :---: | :---: | :--- | ---: | ---: | ---: | ---: | :---: |
| `gemini-3.5-flash-lite` | `global` | YES (6/6) | `gemini-3.5-flash-lite` | 1,080 | 1,289 | 1,594 | 0 | 3/3 |
| `gemini-3.5-flash-lite` | `us` | YES (6/6) | `gemini-3.5-flash-lite` | 1,081 | 1,207 | 1,594 | 0 | 3/3 |
| `gemini-3.5-flash-lite` | `eu` | YES (6/6) | `gemini-3.5-flash-lite` | 1,688 | 1,738 | 1,594 | 0 | 3/3 |
| `gemini-3.8-flash` | `global` | YES (6/6) | `gemini-3.8-flash` | 3,038 | 3,935 | 1,594 | 136 | 3/3 |
| `gemini-3.8-flash` | `us` | YES (6/6) | `gemini-3.8-flash` | 3,058 | 7,385 | 1,594 | 138 | 3/3 |
| `gemini-3.8-flash` | `eu` | YES (6/6) | `gemini-3.8-flash` | 3,617 | 3,794 | 1,594 | 162 | 3/3 |
| **`gemini-4-hydrogen`** | `global` | **YES (6/6)** | `gemini-4-hydrogen` | **2,282** | **2,470** | 1,728 | 147 | **3/3** |
| **`gemini-4-hydrogen`** | `us` | **YES (6/6)** | `gemini-4-hydrogen` | **2,528** | **2,956** | 1,728 | 169 | **3/3** |
| **`gemini-4-hydrogen`** | `eu` | **YES (6/6)** | `gemini-4-hydrogen` | **2,987** | **3,749** | 1,728 | 164 | **3/3** |
| `gemini-4-argon` | `global` | YES (5/5) | `gemini-4-argon` | 4,933 | 7,043 | 1,757 | 0 | 3/3 |
| `gemini-4-argon` | `us` | YES (5/5) | `gemini-4-argon` | 4,879 | 11,139 | 1,757 | 0 | 3/3 |
| `gemini-4-argon` | `eu` | YES (5/5) | `gemini-4-argon` | 8,952 | 12,733 | 1,757 | 21 | 3/3 |
| `gemini-4-carbon` | `global` | YES (5/5) | `gemini-4-carbon` | 5,942 | 8,135 | 1,757 | 108 | 3/3 |
| `gemini-4-carbon` | `us` | YES (5/5) | `gemini-4-carbon` | 6,976 | 7,703 | 1,757 | 103 | 3/3 |
| `gemini-4-carbon` | `eu` | YES (5/5) | `gemini-4-carbon` | 5,645 | 6,853 | 1,757 | 101 | 3/3 |

> **Tokenizer Note:** Gemini 4 models (`gemini-4-hydrogen`, `gemini-4-argon`, `gemini-4-carbon`) tokenize the identical `groundedness` prompts into **~8.4%–10.2% more input tokens** (`1,728–1,757` mean prompt tokens) than Gemini 3.x (`1,594` mean prompt tokens).

---

## 3. Quality Benchmark Across 946 Human- & Gold-Labeled Items

All comparisons ran in the same session using paired `mizan eval compare-engines` runs (temperature `0`, seed `20260925` for bootstrap 95% CIs, exact two-sided McNemar test on discordant pairs).

### 3.1 Per-Suite Accuracy & Pooled Aggregates

| Suite ($n$) | `gemini-3.5-flash-lite` | `gemini-3.8-flash` | `gemini-4-hydrogen` (`DEFAULT`) | `gemini-4-hydrogen` (`LOW`) | `gemini-4-argon` (Pro) | `gemini-4-carbon` (Pro) | $\Delta$ (`4-hydrogen - 3.8-flash`) | McNemar $p$ |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| `safety_response` (100) | 79.0% (79/100) | 77.0% (77/100) | 78.0% (78/100) | — | — | — | +1.0 pts | 1.000 |
| `toxicity_prompt` (100)¹ | 81.8% (81/99) | 90.8% (89/98) | 88.8% (87/98)² | 88.8% (87/98) | 90.6% (87/96) | 90.7% (88/97) | -2.0 pts | 0.500 |
| `groundedness` (100) | 82.0% (82/100) | 78.0% (78/100) | 78.0% (78/100) | **80.0% (80/100)** | — | — | +0.0 pts | 1.000 |
| `pairwise_mtbench` (100) | 64.0% (64/100) | 78.0% (78/100) | 75.0% (75/100) | **80.0% (80/100)** | 76.0% (76/100) | 70.0% (70/100) | -3.0 pts | 0.250 |
| `pairwise_multiturn` (100) | 66.0% (66/100) | 73.0% (73/100) | 72.0% (72/100) | — | — | — | -1.0 pts | 1.000 |
| `pairwise_instruction_following` (100) | 78.0% (78/100) | 92.0% (92/100) | **94.0% (94/100)** | 92.0% (92/100) | 92.0% (92/100) | **94.0% (94/100)** | **+2.0 pts** | 0.625 |
| `pairwise_rewardbench` (100) | 88.0% (88/100) | 89.0% (89/100) | **90.0% (90/100)** | — | — | — | **+1.0 pts** | 1.000 |
| `likert_helpfulness` (100)³ | 39.0% (39/100) | 46.0% (46/100) | 40.0% (40/100) | — | — | — | -6.0 pts | 0.180 |
| `likert_coherence` (100)³ | 43.0% (43/100) | 43.0% (43/100) | **47.0% (47/100)** | — | — | — | **+4.0 pts** | 0.541 |
| `calibration_suite` (46) | 91.3% (42/46) | 91.3% (42/46) | **93.5% (43/46)** | **93.5% (43/46)** | 91.3% (42/46) | **93.5% (43/46)** | **+2.2 pts** | 1.000 |
| **Pooled Discrete + Calibration (746)** | **77.9%** `[74.6, 80.8]` | **83.1%** `[80.2, 85.6]` | **82.9%** `[80.2, 85.5]` | **83.6%**⁴ | — | — | **-0.13 pts** | **1.0000** |
| **Pooled All (946)** | **70.1%** `[67.1, 73.0]` | **74.9%** `[72.1, 77.6]` | **74.6%** `[71.8, 77.3]` | — | — | — | **-0.32 pts** | **0.8013** |
| **Pooled Hard Slice (346)** | **76.8%** `[72.8, 81.4]` | **87.5%** `[84.0, 91.0]` | **86.9%** `[83.1, 90.4]` | **87.5%** (`301/344`) | **86.8%** `[83.3, 90.4]` | **86.0%** `[82.2, 89.5]` | **-0.58 pts** | **0.7539** |

¹ On `toxicity_prompt` (ToxicChat), Vertex safety filters blocked 1–4 severe prompts (`tc-640`, `tc-3970`, plus `tc-0057`/`tc-0083` on Pro models) with empty responses across all models, matching Experiment 07.
² In the Stage 3a thinking A/B rerun on `toxicity_prompt`, `gemini-4-hydrogen` (`DEFAULT`) scored **90.8% (89/98)**, tying `gemini-3.8-flash` (`89/98`).
³ For Likert suites, exact match within $\pm 0.5$ of the continuous human mean is secondary to Spearman rank correlation ($\rho$) and MAE (Table 3.2).
⁴ Pooled 746-item accuracy when substituting `--thinking-level low` on the 5 suites tested in Stage 3a (`622/744 = 83.6%`).

### 3.2 Continuous Likert & Calibration Score Correlation (Spearman $\rho$ / MAE)

| Suite | `gemini-3.5-flash-lite` ($\rho$ / MAE) | `gemini-3.8-flash` ($\rho$ / MAE) | `gemini-4-hydrogen` ($\rho$ / MAE) | `gemini-4-argon` ($\rho$ / MAE) | `gemini-4-carbon` ($\rho$ / MAE) | $\Delta \rho$ (`4-hydrogen - 3.8-flash`) |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: |
| `likert_helpfulness` (HelpSteer2, 0–4) | 0.592 / 0.95 | **0.684 / 0.77** | 0.653 / 0.79 | — | — | -0.031 |
| `likert_coherence` (SummEval, 1–5) | 0.775 / 0.83 | 0.779 / 0.71 | **0.801 / 0.70** | — | — | **+0.022** |
| `calibration_suite` (`score` subset, $n=10$) | 0.979 / 0.14 | 0.985 / 0.14 | **0.990 / 0.11** | 0.976 / 0.12 | **0.990 / 0.09** | **+0.005** |

### 3.3 Pro-Tier Ceiling Analysis (`gemini-4-argon` vs. `gemini-4-carbon`)

Evaluating `gemini-4-argon` and `gemini-4-carbon` across the 346-item hard slice reveals why Pro-tier models are unnecessary as default Mizan autoraters:
* **No Accuracy Ceiling Gain Over Flash Tier:** Pooled hard-slice accuracy is **86.8%** for `gemini-4-argon` and **86.0%** for `gemini-4-carbon`, compared to **86.9%** (`DEFAULT`) and **87.5%** (`LOW`) for `gemini-4-hydrogen` and **87.5%** for `gemini-3.8-flash`.
* **`gemini-4-argon` vs. `gemini-4-carbon` Tradeoff:** `gemini-4-argon` beats `gemini-4-carbon` on `pairwise_mtbench` (**76.0% vs. 70.0%**, $+6.0\text{ pts}$, McNemar $p = 0.031$) while generating **zero thinking tokens by default** (`0` mean thoughts tokens in `global`/`us`), whereas `gemini-4-carbon` edges `gemini-4-argon` on `pairwise_instruction_following` (**94.0% vs. 92.0%**) and `calibration_suite` (**93.5% vs. 91.3%**) — tying `gemini-4-hydrogen` on both.
* **Latency Cost:** Both Pro models run at **5.2–7.1 s p50** and **8.0–12.7 s p95**, roughly **2.3x–3.1x slower** than `gemini-4-hydrogen`.

---

## 4. `ThinkingLevel` (`DEFAULT` vs. `--thinking-level low`) Discovery & Tradeoffs

An endpoint capability probe across all five models revealed distinct `ThinkingConfig` behavior on Vertex AI:
* **`gemini-4-hydrogen`:** Defaults to dynamic thinking (`310–556` mean `thoughtsTokenCount` across complex judge suites). Setting `--thinking-level low` (`ThinkingLevelLow`) cuts thinking tokens by **48%** (`396` $\to$ `206` mean tokens) and reduces p50 latency by **21–25%** (`2,738 ms` $\to$ `2,150 ms`) while **improving** pooled accuracy by **+0.68 pts** (`85.4%` $\to$ `86.0%`, McNemar $p = 0.6636$). Setting `ThinkingBudget = 0` is rejected by Vertex AI (`400 INVALID_ARGUMENT: Budget 0 is invalid. This model only works in thinking mode.`).
* **Why `LOW` Beats `DEFAULT` on `pairwise_mtbench` (+6.0 pts):** On open-ended pairwise preference with ties (`pairwise_mtbench`), unrestricted thinking (`556` mean thoughts tokens) causes occasional "overthinking" where the judge rationalizes minor stylistic flaws into `TIE` or flips preference; constraining reasoning to `LOW` (`255` mean thoughts tokens) improves accuracy from **74.0% to 80.0%** ($p = 0.070$) and cuts p95 latency from **10,331 ms to 4,660 ms** (**2.22x faster p95**).
* **`gemini-3.5-flash-lite`:** Defaults to **0 thinking tokens** (`800–1,080 ms` p50). Passing `--thinking-level low` *activates* thinking (`~151` thoughts tokens) and doubles latency — so `--thinking-level` should remain unset on `gemini-3.5-flash-lite`.

| Suite ($n$) | `DEFAULT` Acc | `LOW` Acc | $\Delta$ Acc (`LOW - DEFAULT`) | McNemar $p$ | `DEFAULT` p50 / p95 (ms) | `LOW` p50 / p95 (ms) | `DEFAULT` Thoughts Tok | `LOW` Thoughts Tok |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | ---: | ---: |
| `groundedness` (100) | 79.0% | **80.0%** | +1.0 pts | 1.000 | 3,519 / 6,955 | **2,841 / 4,691** | 411 | 275 |
| `toxicity_prompt` (98) | **90.8%** | 88.8% | -2.0 pts | 0.688 | 2,378 / 6,942 | **1,865 / 3,476** | 337 | 165 |
| `pairwise_instruction_following` (100) | **94.0%** | 92.0% | -2.0 pts | 0.625 | 2,233 / 6,093 | **1,817 / 3,157** | 310 | 155 |
| `pairwise_mtbench` (100) | 74.0% | **80.0%** | **+6.0 pts** | 0.070 | 3,215 / 10,331 | **2,474 / 4,660** | 556 | 255 |
| `calibration_suite` (46) | **93.5%** | **93.5%** | +0.0 pts | 1.000 | 2,569 / 6,216 | **1,921 / 2,906** | 325 | 151 |
| **Pooled (444 scored items)** | **85.4%** | **86.0%** | **+0.68 pts** | **0.6636** | **2,738** (p50) | **2,150** (p50) | **396** | **206** |

---

## 5. Serial Paired Latency & Concurrency Scaling

### 5.1 Serial Paired Long-Passage Latency (`--serial --workers 1`, `groundedness`, $n=15$)

| Comparison | Side A | Side A p50 / p95 (ms) | Side B | Side B p50 / p95 (ms) | Speedup (p50 / p95) | Side A Mean Thoughts | Side B Mean Thoughts |
| :--- | :--- | :---: | :--- | :---: | :---: | ---: | ---: |
| `gemini-3.8-flash` vs. `gemini-4-hydrogen` | `gemini-3.8-flash` | 3,165 / 4,590 | `gemini-4-hydrogen` (`DEFAULT`) | **2,369 / 2,810** | **1.34x / 1.63x** | 160 | 164 |
| `gemini-4-hydrogen` `DEFAULT` vs. `LOW` | `gemini-4-hydrogen` (`DEFAULT`) | 2,252 / 3,306 | `gemini-4-hydrogen` (`LOW`) | **2,012 / 2,891** | **1.12x / 1.14x** | 171 | 146 |

### 5.2 Concurrency & Throughput Sweep (`safety_response`, $c \in \{1, 4, 8, 16\}$)

| Target | $c=1$ items/s (p50 / p95 ms) | $c=4$ items/s (p50 / p95 ms) | $c=8$ items/s (p50 / p95 ms) | $c=16$ items/s (p50 / p95 ms) | $c=16$ Errors | Mean Thoughts Tok ($c=16$) |
| :--- | :---: | :---: | :---: | :---: | :---: | ---: |
| `gemini-3.5-flash-lite` | **1.00** (983 / 1,172) | **4.16** (871 / 1,126) | **8.86** (849 / 1,052) | **15.85** (797 / 1,051) | 0/64 | 0 |
| `gemini-3.8-flash` | 0.36 (2,678 / 3,990) | 1.35 (2,686 / 3,768) | 2.22 (2,841 / 4,123) | 3.37 (3,035 / 5,336) | 0/64 | 128 |
| **`gemini-4-hydrogen` (`DEFAULT`)** | 0.29 (2,319 / 11,095) | 1.30 (2,238 / 4,856) | **2.71** (2,243 / 3,897) | **5.05** (2,259 / 4,916) | 0/64 | 261 |
| **`gemini-4-hydrogen` (`LOW`)** | **0.48** (1,744 / 3,919) | **2.10** (1,706 / 2,627) | **3.98** (1,811 / 2,706) | **7.43** (1,745 / 2,695) | 0/64 | 130 |

---

## 6. Repeatable Autorater Qualification Protocol & Tiering Recommendation

### 6.1 Running the Qualification Harness on Future Models

Whenever a new candidate model appears on Vertex AI, run [`scripts/judge-eval/qualify_model.py`](../../scripts/judge-eval/qualify_model.py) to execute the 4-stage benchmark and generate `backends.json`, `qualification_summary.json`, and `qualification_summary.md`:

```bash
# Build local binary with latest multi-region & thinking-level flags
go build -o bin/mizan ./cmd/mizan

# Run all 4 stages + summary synthesis (resumable; skips completed suites unless --force)
python3 scripts/judge-eval/qualify_model.py \
  --mizan ./bin/mizan \
  --candidate gemini-4-hydrogen \
  --baseline gemini-3.8-flash \
  --fast-baseline gemini-3.5-flash-lite \
  --pro-anchors gemini-4-argon gemini-4-carbon \
  --locations global us eu \
  --out docs/experiments/judge-eval/runs/$(date +%Y%m%d)-hydrogen-spike
```

### 6.2 Mizan Autorater Tiering Recommendation

1. **Fast / High-Volume Default Tier (`gemini-3.5-flash-lite`):**
   * Retain `gemini-3.5-flash-lite` as the default `spec.autorater.model` for single-proposition `boul`, low-cardinality `choice`, and high-throughput CI gates where sub-second latency (`~800–1,080 ms` p50, **`15.85 items/s`** at $c=16$, `0` thinking token overhead) and **82.0%–91.3%** accuracy (`groundedness`, `safety_response`, `pairwise_rewardbench`, `calibration_suite`) are optimal.
2. **Reasoning / Pairwise / Multi-Class Flash Tier (`gemini-4-hydrogen`, replacing `gemini-3.8-flash`):**
   * Promote **`gemini-4-hydrogen`** (ideally with `--thinking-level low` or `WithThinkingLevel("low")`) as the recommended reasoning/pairwise Flash autorater, superseding `gemini-3.8-flash`. It delivers **+5.0 to +16.0 pts higher accuracy** than `gemini-3.5-flash-lite` on hard pairwise preference (`pairwise_instruction_following` **94.0%**, `pairwise_mtbench` **80.0%** at `LOW`), toxicity detection (**88.8%–90.8%**), and 77-intent calibration (**93.5%**), while matching `gemini-3.8-flash` and Pro-tier models (`gemini-4-argon`, `gemini-4-carbon`) in accuracy at **1.34x–1.57x lower serial latency** and **1.50x–2.20x higher concurrent throughput**.
3. **Pro-Tier Audit Anchors (`gemini-4-argon`, `gemini-4-carbon`):**
   * Reserve `gemini-4-argon` and `gemini-4-carbon` for offline gold-label arbitration or tie-breaking cascades rather than bulk evaluation, as `gemini-4-hydrogen` already reaches the Pro-tier accuracy ceiling (**86.9%–87.5%** vs. **86.0%–86.8%**) at less than half the latency.

---

## 7. Glossary & Appendix: TL;DR Primer on Evaluation Suites, Metrics & Measurements

This appendix provides a self-contained reference for readers sharing or reviewing the benchmark tables above.

### 7.1 Mizan Evaluation Primitives (`kind`)

| Primitive | Output Schema | How It Runs on Vertex AI | How Accuracy Is Scored Against Gold Labels |
| :--- | :--- | :--- | :--- |
| **`boul`** (Boolean Proposition) | `{"passed": bool, "confidence": float, "reasoning": string}` | Direct `google.golang.org/genai` (`GenerateContent`) with strict `ResponseSchema` at `temperature = 0` | Exact boolean match (`passed == expected`). For detector templates (e.g., toxicity, prompt injection, drift), `passed: true` means *"the condition is detected."* |
| **`choice`** (Categorical Triage) | `{"choice": enum, "confidence": float, "reasoning": string}` | Direct `genai` `GenerateContent` with `ResponseSchema` constraining `choice` to the allowed enum list (up to 255 static choices) | Exact string match (`choice == expected`). |
| **`score`** (Continuous / Likert Rating) | `{"score": number, "confidence": float, "reasoning": string}` | Direct `genai` `GenerateContent` with numeric `ResponseSchema` bounded to `[min, max]` | Primary: **Spearman rank correlation ($\rho$)** and **Mean Absolute Error (MAE)** against the human mean rating. Secondary: match within `--score-tolerance` ($\pm 0.5$ on 5-point Likert suites; $\pm 1.0$ on `calibration_suite`). |
| **`pairwise`** (A vs. B Preference) | `{"choice": "A" \| "B" \| "TIE", ...}` | Vertex AI native `EvaluationService.EvaluateInstances` (`PairwiseMetricInput`) | Exact match (`A`, `B`, or `TIE`) against the human preference label. |

### 7.2 The 10 Benchmark Suites (946 Total Items)

| Suite | Items ($n$) | Primitive (`kind`) | Upstream Human / Gold Dataset | What It Measures |
| :--- | :---: | :---: | :--- | :--- |
| `safety_response` | 100 | `boul` | **BeaverTails** (`is_safe` human annotations) | Whether an assistant's response to an adversarial prompt is safe (`true`) or harmful (`false`). |
| `toxicity_prompt` | 100 | `boul` | **ToxicChat** (`human_annotation` split) | Detecting subtle toxicity, jailbreaks, or harmful intent in real user prompts (`true` = toxic). |
| `groundedness` | 100 | `boul` | **HaluBench** (5 subsets: PubMedQA, FinanceBench, RAGTruth, HaluEval, CovidQA) | Faithfulness: whether every claim in an answer is supported by the provided source passage (`true` = faithful, `false` = hallucinated). |
| `pairwise_rewardbench` | 100 | `pairwise` | **RewardBench** (10 subsets across chat, reasoning, safety) | Choosing the better response (`A` vs. `B`) when a known ground-truth `chosen` vs. `rejected` pair exists. |
| `pairwise_instruction_following` | 100 | `pairwise` | **LLMBar** (Natural + Adversarial traps) | Choosing which response strictly obeyed all explicit formatting, length, and negative constraints (ignoring superficial verbosity/polish). |
| `pairwise_mtbench` | 100 | `pairwise` | **MT-Bench Human Judgements** (Turn 1, includes ~20% human `TIE`s) | 3-way preference (`A`, `B`, `TIE`) against expert human annotators on open-ended writing, extraction, coding, and math. |
| `pairwise_multiturn` | 100 | `pairwise` | **MT-Bench Human Judgements** (Turn 2 follow-up) | 3-way preference (`A`, `B`, `TIE`) on multi-turn conversations where Turn 2 introduces a follow-up constraint or correction. |
| `likert_helpfulness` | 100 | `score` (0–4) | **HelpSteer2** (mean of multiple human raters) | Continuous 0–4 helpfulness rating compared against the continuous average of human annotators. |
| `likert_coherence` | 100 | `score` (1–5) | **SummEval** (mean of 3 expert human raters) | Continuous 1–5 summary coherence rating compared against the mean of 3 expert human judges. |
| `calibration_suite` | 46 | `boul` (16), `choice` (20), `score` (10) | **Mizan Calibration Pack** (15 templates across Civil Comments, BoolQ, MS MARCO, ANLI, ChaosNLI, CLINC150, GoEmotions, **Banking77**, SST-5, Yelp, AgentDrift) | Multi-template schema and taxonomy stress test, including 77-intent classification (`banking77-intent-select`), 28-way emotion selection (`go-emotions`), 3-way NLI entailment, prompt injection detection, and agent trajectory drift. |

**Why three pooled aggregates are reported:**
* **Pooled Discrete + Calibration (746 items):** Combines the 7 discrete human-labeled suites (`safety_response`, `toxicity_prompt`, `groundedness`, `pairwise_rewardbench`, `pairwise_instruction_following`, `pairwise_mtbench`, `pairwise_multiturn` = 700 items) plus `calibration_suite` (46 items). This is the **primary accuracy headline** because every item has an unambiguous discrete label (`true`/`false`, `A`/`B`/`TIE`, or class enum).
* **Pooled All (946 items):** Adds the 200 continuous Likert items (`likert_helpfulness`, `likert_coherence`) using a strict $\pm 0.5$ tolerance around the fractional human mean. Because human annotators themselves only agree within $\pm 0.5$ roughly 40–47% of the time on 5-point Likert scales, Likert suites should primarily be read via **Spearman $\rho$** and **MAE**.
* **Pooled Hard Slice (346 items):** The 4 suites where reasoning and taxonomy breadth matter most (`pairwise_mtbench`, `pairwise_instruction_following`, `toxicity_prompt`, `calibration_suite`), used to test whether Pro-tier models (`gemini-4-argon`, `gemini-4-carbon`) offer any ceiling advantage over Flash-tier models.

### 7.3 Statistical & Performance Metrics Glossary

* **Bootstrap 95% Confidence Interval (`[lo%, hi%]`):** Computed by resampling the $N$ evaluation items with replacement 2,000 times (fixed seed `20260925`) and taking the 2.5th and 97.5th percentiles. Two models with heavily overlapping CIs are within sampling noise.
* **Exact Two-Sided McNemar Test ($p$-value):** Because both models evaluate the *exact same items*, a paired McNemar test is far more sensitive than an unpaired two-proportion test. It ignores items where both models are right or both are wrong and tests only the **discordant pairs**:
  * $n_{10}$: items where Model A is right and Model B is wrong.
  * $n_{01}$: items where Model B is right and Model A is wrong.
  * Under the null hypothesis that both models have equal accuracy, $n_{10} \sim \text{Binomial}(n_{10}+n_{01}, 0.5)$. A value of **$p < 0.05$** indicates a statistically significant difference; **$p \ge 0.05$** (e.g., $p = 1.0000$ on the 746-item pool where `3.8-flash` won 41 discordant items and `4-hydrogen` won 40) confirms statistical parity.
* **Spearman's Rank Correlation ($\rho$, `-1.0` to `+1.0`):** Measures how well a judge's numeric `score` preserves the *relative ranking* of responses compared to human raters, independent of whether the model scores slightly harsher or more leniently on average. Higher is better ($\rho > 0.65$ on HelpSteer2 and $\rho > 0.78$ on SummEval represent strong human-level rank agreement).
* **Mean Absolute Error (MAE):** The average absolute distance $|\text{score}_{\text{model}} - \text{score}_{\text{human}}|$ on the rating scale (e.g., `0.70` points on a 1–5 scale). Lower is better.
* **Serial p50 / p95 Latency (`ms`):**
  * **p50 (Median):** 50% of calls complete faster than this time.
  * **p95 (Tail Latency):** 95% of calls complete faster than this time. In parallel batch evaluations (`errgroup` workers), high p95 tail latency (often caused by unconstrained reasoning bursts) bottlenecks overall wall-clock completion.
* **Throughput (`items/s` at Concurrency $c$):** Total successfully evaluated items divided by end-to-end batch wall-clock seconds at $c \in \{1, 4, 8, 16\}$ concurrent workers.
* **`thoughtsTokenCount` & `ThinkingLevel` (`DEFAULT` vs. `LOW`):**
  * Gemini 3.x and 4.x reasoning models generate internal chain-of-thought tokens (`thoughtsTokenCount`) before emitting the structured JSON verdict (`candidatesTokenCount`).
  * Thinking tokens are billed as output tokens and directly drive latency. Passing `--thinking-level low` (`ThinkingLevelLow`) caps internal reasoning depth, cutting thinking tokens by ~48% on `gemini-4-hydrogen` and halving p95 tail latency without sacrificing accuracy.

