# Autorater Qualification Summary: `gemini-4-hydrogen` vs. `gemini-3.8-flash`

Generated: `2026-11-12T23:15:12Z`

## 1. Multi-Region (`global`, `us`, `eu`) Availability & Unloaded Serial Latency

| Model | Location | Reachable | Model Version(s) | Serial p50 (ms) | Serial p95 (ms) | Mean Prompt Tok | Mean Thoughts Tok | Native `EvaluateInstances` |
| :--- | :---: | :---: | :--- | ---: | ---: | ---: | ---: | :---: |
| `gemini-3.5-flash-lite` | `global` | YES (6/6) | `gemini-3.5-flash-lite` | 1080 | 1289 | 1594 | 0 | 3/3 |
| `gemini-3.5-flash-lite` | `us` | YES (6/6) | `gemini-3.5-flash-lite` | 1081 | 1207 | 1594 | 0 | 3/3 |
| `gemini-3.5-flash-lite` | `eu` | YES (6/6) | `gemini-3.5-flash-lite` | 1688 | 1738 | 1594 | 0 | 3/3 |
| `gemini-3.8-flash` | `global` | YES (6/6) | `gemini-3.8-flash` | 3038 | 3935 | 1594 | 136 | 3/3 |
| `gemini-3.8-flash` | `us` | YES (6/6) | `gemini-3.8-flash` | 3058 | 7385 | 1594 | 138 | 3/3 |
| `gemini-3.8-flash` | `eu` | YES (6/6) | `gemini-3.8-flash` | 3617 | 3794 | 1594 | 162 | 3/3 |
| `gemini-4-hydrogen` | `global` | YES (6/6) | `gemini-4-hydrogen` | 2282 | 2470 | 1728 | 147 | 3/3 |
| `gemini-4-hydrogen` | `us` | YES (6/6) | `gemini-4-hydrogen` | 2528 | 2956 | 1728 | 169 | 3/3 |
| `gemini-4-hydrogen` | `eu` | YES (6/6) | `gemini-4-hydrogen` | 2987 | 3749 | 1728 | 164 | 3/3 |
| `gemini-4-argon` | `global` | YES (5/5) | `gemini-4-argon` | 4933 | 7043 | 1757 | 0 | 3/3 |
| `gemini-4-argon` | `us` | YES (5/5) | `gemini-4-argon` | 4879 | 11139 | 1757 | 0 | 3/3 |
| `gemini-4-argon` | `eu` | YES (5/5) | `gemini-4-argon` | 8952 | 12733 | 1757 | 21 | 3/3 |
| `gemini-4-carbon` | `global` | YES (5/5) | `gemini-4-carbon` | 5942 | 8135 | 1757 | 108 | 3/3 |
| `gemini-4-carbon` | `us` | YES (5/5) | `gemini-4-carbon` | 6976 | 7703 | 1757 | 103 | 3/3 |
| `gemini-4-carbon` | `eu` | YES (5/5) | `gemini-4-carbon` | 5645 | 6853 | 1757 | 101 | 3/3 |

## 2. Per-Suite Accuracy vs. Human Gold Labels (946 Items Across 10 Suites)

| Suite | `gemini-3.5-flash-lite` | `gemini-3.8-flash` | `gemini-4-hydrogen` | `gemini-4-argon` | `gemini-4-carbon` | `gemini-4-hydrogen - gemini-3.8-flash` (pts) | McNemar $p$ (`gemini-4-hydrogen` vs `gemini-3.8-flash`) |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| `safety_response` | 79.0% (79/100) | 77.0% (77/100) | 78.0% (78/100) | - | - | +1.0 | 1.000 |
| `toxicity_prompt` | 81.8% (81/99) | 90.8% (89/98) | 88.8% (87/98) | 90.6% (87/96) | 90.7% (88/97) | -2.0 | 0.500 |
| `groundedness` | 82.0% (82/100) | 78.0% (78/100) | 78.0% (78/100) | - | - | +0.0 | 1.000 |
| `pairwise_mtbench` | 64.0% (64/100) | 78.0% (78/100) | 75.0% (75/100) | 76.0% (76/100) | 70.0% (70/100) | -3.0 | 0.250 |
| `pairwise_multiturn` | 66.0% (66/100) | 73.0% (73/100) | 72.0% (72/100) | - | - | -1.0 | 1.000 |
| `pairwise_instruction_following` | 78.0% (78/100) | 92.0% (92/100) | 94.0% (94/100) | 92.0% (92/100) | 94.0% (94/100) | +2.0 | 0.625 |
| `pairwise_rewardbench` | 88.0% (88/100) | 89.0% (89/100) | 90.0% (90/100) | - | - | +1.0 | 1.000 |
| `likert_helpfulness` | 39.0% (39/100) | 46.0% (46/100) | 40.0% (40/100) | - | - | -6.0 | 0.180 |
| `likert_coherence` | 43.0% (43/100) | 43.0% (43/100) | 47.0% (47/100) | - | - | +4.0 | 0.541 |
| `calibration_suite` | 91.3% (42/46) | 91.3% (42/46) | 93.5% (43/46) | 91.3% (42/46) | 93.5% (43/46) | +2.2 | 1.000 |
| **Pooled Discrete + Calibration (746 items)** | **77.9%** [74.6%, 80.8%] | **83.1%** [80.2%, 85.6%] | **82.9%** [80.2%, 85.5%] | - | - | **-0.13** | **1.0000** |
| **Pooled All (946 items)** | **70.1%** [67.1%, 73.0%] | **74.9%** [72.1%, 77.6%] | **74.6%** [71.8%, 77.3%] | - | - | **-0.32** | **0.8013** |
| **Pooled Hard Slice (346 items)** | **76.8%** [72.8%, 81.4%] | **87.5%** [84.0%, 91.0%] | **86.9%** [83.1%, 90.4%] | **86.8%** [83.3%, 90.4%] | **86.0%** [82.2%, 89.5%] | **-0.58** | **0.7539** |

### 2b. Likert Scale Correlation (Spearman $\rho$ / MAE vs. Human Mean)

| Suite | `gemini-3.5-flash-lite` ($\rho$ / MAE) | `gemini-3.8-flash` ($\rho$ / MAE) | `gemini-4-hydrogen` ($\rho$ / MAE) | $\Delta \rho$ (`gemini-4-hydrogen - gemini-3.8-flash`) |
| :--- | :---: | :---: | :---: | :---: |
| `likert_helpfulness` | 0.592 / 0.95 | 0.684 / 0.77 | 0.653 / 0.79 | -0.031 |
| `likert_coherence` | 0.775 / 0.83 | 0.779 / 0.71 | 0.801 / 0.70 | +0.022 |

## 3. Thinking-Level A/B (`DEFAULT` vs. `--thinking-level low`) on `gemini-4-hydrogen`

| Suite | `DEFAULT` Acc | `LOW` Acc | $\Delta$ Acc (`LOW - DEFAULT`) | McNemar $p$ | `DEFAULT` p50 (ms) | `LOW` p50 (ms) | `DEFAULT` Mean Thoughts Tok | `LOW` Mean Thoughts Tok |
| :--- | :---: | :---: | :---: | :---: | ---: | ---: | ---: | ---: |
| `groundedness` | 79.0% | 80.0% | +1.0 pts | 1.000 | 3519 | 2841 | 411 | 275 |
| `toxicity_prompt` | 90.8% | 88.8% | -2.0 pts | 0.688 | 2378 | 1865 | 337 | 165 |
| `pairwise_instruction_following` | 94.0% | 92.0% | -2.0 pts | 0.625 | 2233 | 1817 | 310 | 155 |
| `pairwise_mtbench` | 74.0% | 80.0% | +6.0 pts | 0.070 | 3215 | 2474 | 556 | 255 |
| `calibration_suite` | 93.5% | 93.5% | +0.0 pts | 1.000 | 2569 | 1921 | 325 | 151 |
| **Pooled (444 items)** | **85.4%** | **86.0%** | **+0.68 pts** | **0.6636** | **2738** | **2150** | **396** | **206** |

## 4. Serial Paired Latency (`--serial --workers 1`, `groundedness`, $n=15$)

| Comparison | Side A | Side A p50 / p95 (ms) | Side B | Side B p50 / p95 (ms) | Side A Mean Thoughts | Side B Mean Thoughts |
| :--- | :--- | :---: | :--- | :---: | ---: | ---: |
| `gemini-3.8-flash` vs `gemini-4-hydrogen` | `gemini-3.8-flash` | 3165 / 4590 | `gemini-4-hydrogen` | 2369 / 2810 | 160 | 164 |
| `gemini-4-hydrogen` `DEFAULT` vs `LOW` | `gemini-4-hydrogen (default)` | 2252 / 3306 | `gemini-4-hydrogen (low)` | 2012 / 2891 | 171 | 146 |

## 5. Concurrency & Throughput Scaling (`safety_response`, $c = 1, 4, 8, 16$)

| Target | Concurrency ($c$) | Throughput (items/s) | p50 (ms) | p95 (ms) | Errors | Accuracy | Mean Thoughts Tok |
| :--- | :---: | ---: | ---: | ---: | :---: | :---: | ---: |
| `gemini-3.5-flash-lite` | 1 | 1.00 | 983 | 1172 | 0/16 | 100.0% | 0 |
| `gemini-3.5-flash-lite` | 4 | 4.16 | 871 | 1126 | 0/32 | 96.9% | 0 |
| `gemini-3.5-flash-lite` | 8 | 8.86 | 849 | 1052 | 0/48 | 97.9% | 0 |
| `gemini-3.5-flash-lite` | 16 | 15.85 | 797 | 1051 | 0/64 | 89.1% | 0 |
| `gemini-3.8-flash` | 1 | 0.36 | 2678 | 3990 | 0/16 | 100.0% | 94 |
| `gemini-3.8-flash` | 4 | 1.35 | 2686 | 3768 | 0/32 | 96.9% | 117 |
| `gemini-3.8-flash` | 8 | 2.22 | 2841 | 4123 | 0/48 | 97.9% | 118 |
| `gemini-3.8-flash` | 16 | 3.37 | 3035 | 5336 | 0/64 | 89.1% | 128 |
| `gemini-4-hydrogen` | 1 | 0.29 | 2319 | 11095 | 0/16 | 100.0% | 291 |
| `gemini-4-hydrogen` | 4 | 1.30 | 2238 | 4856 | 0/32 | 90.6% | 270 |
| `gemini-4-hydrogen` | 8 | 2.71 | 2243 | 3897 | 0/48 | 95.8% | 246 |
| `gemini-4-hydrogen` | 16 | 5.05 | 2259 | 4916 | 0/64 | 89.1% | 261 |
| `gemini-4-hydrogen-low` | 1 | 0.48 | 1744 | 3919 | 0/16 | 100.0% | 124 |
| `gemini-4-hydrogen-low` | 4 | 2.10 | 1706 | 2627 | 0/32 | 96.9% | 124 |
| `gemini-4-hydrogen-low` | 8 | 3.98 | 1811 | 2706 | 0/48 | 95.8% | 137 |
| `gemini-4-hydrogen-low` | 16 | 7.43 | 1745 | 2695 | 0/64 | 89.1% | 130 |

## 6. 5-Gate Autorater Promotion Checklist

| Gate | Criterion | Result | Verdict |
| :--- | :--- | :--- | :---: |
| **1. Endpoint & Routing Readiness** | Reachable in `global`, `us`, `eu`; native `EvaluateInstances` compatible | `global`/`us`/`eu` reachable (`6/6` each); native `3/3` | **PASS** |
| **2. Schema & Calibration Fidelity** | 0 schema/parse errors on `calibration_suite` (46 cases, 15 templates incl. 77-intent choice); accuracy $\ge$ `gemini-3.8-flash` | `93.5%` vs `91.3%` (`0` errors) | **PASS** |
| **3. Human-Gold Accuracy Parity** | Pooled 746-item discrete + calibration accuracy within CI of `gemini-3.8-flash` ($p \ge 0.05$ or $\Delta \ge 0$) | `82.9%` vs `83.1%` ($\Delta = -0.13$ pts, McNemar $p = 1.0000$) | **PASS** |
| **4. Latency & Throughput SLO** | Serial p50 $\le$ `gemini-3.8-flash` and $c=16$ throughput $\ge$ `gemini-3.8-flash` | Serial p50 `2369 ms` vs `3165 ms`; $c=16$ `5.05 items/s` (`DEFAULT`) / `7.43 items/s` (`LOW`) vs `3.37 items/s` | **PASS** |
| **5. Token & Thinking Economy** | Documented `thoughtsTokenCount` overhead and `--thinking-level low` Pareto frontier | `DEFAULT` `396` thoughts tok vs `LOW` `206` thoughts tok ($\Delta$ acc `+0.68` pts on 444 items) | **PASS** |
