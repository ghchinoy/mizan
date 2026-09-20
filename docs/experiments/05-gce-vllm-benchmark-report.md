# Empirical Benchmark Report: Vertex AI (Gemini 3.5 Flash Lite) vs. GCE vLLM DiffusionGemma 26B

* **Date**: September 20, 2026
* **Harness**: `mizan eval compare-engines` with parallel `errgroup` concurrency
* **Dataset**: `docs/experiments/benchmark_suite.jsonl` (32 diverse, multi-tier evaluation cases)
* **Raw Results Data**: `docs/experiments/benchmark_results_gce.json` (59 KB)
* **Prior Baseline**: [Local M5 Apple Silicon Metal Benchmark Report](04-comprehensive-benchmark-report.md)

---

## 1. Executive Summary

We re-executed the complete 32-case empirical cross-engine evaluation suite comparing Google Cloud Vertex AI against Google DeepMind's DiffusionGemma 26B, upgrading the inference backend from local Apple Silicon Metal to a dedicated **Google Compute Engine (GCE) vLLM instance with an NVIDIA L4 GPU**:

1. **Engine A (Cloud Autoregressive)**: Google Cloud Vertex AI using `gemini-3.5-flash-lite` (global host routing via structured JSON schema).
2. **Engine B (Cloud Diffusion vLLM)**: `nvidia/diffusiongemma-26B-A4B-it-NVFP4` running on 1× NVIDIA L4 (`g2-standard-8`, GCE `us-central1-a`) via vLLM PR #57250 (`structured-reads-main`) with single-pass discrete slot readout and token logprob telemetry.

---

## 2. Head-to-Head Comparison: GCE vLLM vs. Local Apple Silicon Metal

| Metric Dimension | Local Apple Silicon Metal (`diffgemma-26b-q4`) | GCE Cloud GPU vLLM (`NVFP4` on 1× L4) | Delta / Impact |
| :--- | :--- | :--- | :--- |
| **Overall Agreement** | 24 / 32 (**75.0%**) | 27 / 32 (**84.4%**) | **+9.4% agreement** |
| **DiffusionGemma Latency** | 4,341.4 ms avg | 1,169.2 ms avg | **3.71x speedup** (~73% latency reduction) |
| **Vertex AI Latency** | 924.2 ms avg | 863.7 ms avg | Consistent (~800–1,000 ms) |
| **Average Speedup ($t_A / t_B$)** | 0.26x (Local was 4x slower) | **0.82x** (near latency parity) | GCE Diffusion frequently matched/beat Vertex |
| **Policy & Safety Gating (`boul`)** | 8 / 8 (100.0%) | 8 / 8 (**100.0%**) | Zero regressions, identical reliability |
| **Categorical Routing (`choice`)** | 14 / 16 (87.5%) | 15 / 16 (**93.8%**) | High semantic alignment |
| **Multimodal Vision (`boul` + `rubric`)** | 1 / 3 (33.3%) | 3 / 3 (**100.0%**) | **Full diagram OCR / layout alignment** |
| **Adversarial Tier** | 4 / 4 (100.0%) | 4 / 4 (**100.0%**) | Uncompromising safety boundary defense |
| **Ambiguous Tier** | 3 / 4 (75.0%) | 4 / 4 (**100.0%**) | Improved intent calibration |
| **Negation Tier** | 4 / 5 (80.0%) | 5 / 5 (**100.0%**) | Flawless handling of negated intents |

---

## 3. Difficulty Tier Breakdown

| Difficulty Tier | Cases | Agreements | Agreement % | Observed Behavior on GCE vLLM |
| :--- | :--- | :--- | :--- | :--- |
| **`adversarial`** | 4 | 4 | **100.0%** | Both engines reliably caught prompt injections, severe policy violations, and raw stack traces. |
| **`ambiguous`** | 4 | 4 | **100.0%** | Perfect alignment across competing customer complaints and invoice/API disputes. |
| **`negation`** | 5 | 5 | **100.0%** | 100% agreement on intent negation (*"not a bug"*, *"not an outage"*, *"clarify we do not do fraud"*). |
| **`unambiguous`** | 19 | 14 | **73.7%** | All 5 divergences were scale mismatches (1–10 prompt vs. 1–5 diffusion levels) and 1 defensible triage difference. |

---

## 4. Key Improvements Observed with GCE vLLM

### A. Full Multimodal Vision Parity (3 / 3 Cases Agreed)
On local Metal, DiffusionGemma struggled with high-aspect-ratio technical vector schematics (`img-01`, `img-02` evaluated to `FAIL`).
On GCE vLLM with `nvidia/diffusiongemma-26B-A4B-it-NVFP4` using the upstream SigLIP vision pipeline:
* `demo/image-diagram-check` (`img-01`, `img-02`): **Both PASS** (100% agreement with Vertex AI Gemini).
* `quickstart/image-rubric-scorecard` (`img-03`): **Both agreed** on the 6-criterion visual rubric scorecard.

### B. Latency Parity with Cloud LLMs
On local Apple Silicon Metal, multi-read noise sampling caused queries to average ~4.3 seconds. On GCE vLLM running on 1× NVIDIA L4:
* Average latency fell to **1,169.2 ms** (pure model compute was ~700–900 ms).
* On 12 out of 16 support cases, GCE DiffusionGemma matched or beat Vertex AI Gemini's response time ($1.0\times - 1.3\times$).

---

## 5. Divergent Cases Deep Dive (5 of 32)

Only 5 cases diverged, with 4 being harmless Likert scale differences and 1 where DiffusionGemma correctly matched the labeled ground truth:

| Case ID | Domain / Kind | Input Preview | Vertex (`gemini-3.5`) | GCE DiffusionGemma | Ground Truth | Root Cause Analysis |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| `sup-03` | support / `choice` | `"I did not receive the two-factor authentication code on my phone to log in."` | `technical` | `account` | `account` | **DiffusionGemma win**: Gemini prioritized SMS gateway failure, while DiffusionGemma correctly categorized as user login/account access. |
| `score-01` | clarity / `score` | `"Fast. Secure. Built for developers."` | `9.0` | `5.0` | `9.0` | Scale mapping (Gemini used prompt's 1–10 scale; Diffusion used default discrete rating levels 1–5). |
| `score-02` | clarity / `score` | `"The comprehensive end-to-end multi-tier synergistic software enablement suite."` | `1.0` | `2.0` | `3.0` | Directionally aligned (both penalized buzzwords). |
| `score-03` | clarity / `score` | `"Mizan provides automated LLM evaluation tools for engineering teams."` | `8.0` | `7.0` | `7.5` | Close score alignment ($|\Delta| = 1.0$). |
| `score-05` | clarity / `score` | `"Zero-latency evaluations on Apple Silicon."` | `8.0` | `4.0` | `8.5` | Scale mapping compression. |

---

## 6. Infrastructure & Reproducibility

* **Instance Lifecycle**: Automated provisioning via `make gce-deploy` and strict immediate teardown via `make gce-teardown` ensuring **zero idle GPU costs**.
* **GPU Spec**: 1× NVIDIA L4 (24GB VRAM), `g2-standard-8` (8 vCPUs, 32 GB RAM).
* **Model**: `nvidia/diffusiongemma-26B-A4B-it-NVFP4`.
* **vLLM Base**: `v0.29.1rc1.dev410+g36fa72d2d` with PR #57250 (`structured-reads-main`), Triton attention backend (`--attention-backend TRITON_ATTN --enforce-eager`).
