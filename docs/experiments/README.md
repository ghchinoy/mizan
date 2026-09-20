# Mizan Empirical Evaluation Experiments: Vertex AI Gemini vs. DiffusionGemma

This directory documents live, empirical comparison experiments evaluating Mizan metric templates across two distinct generative architectures:
1. **Cloud Autoregressive Engine**: Google Cloud Vertex AI / Gemini 2.5 Flash via direct structured JSON output.
2. **Local Discrete Block Diffusion Engine**: Google DeepMind DiffusionGemma 26B (`diffgemma-26b-a4b-it-q4`) running locally on Apple Silicon Metal via discrete slot readouts (`http://127.0.0.1:8080/v1`).

---

## Hardware & Environment Setup

* **Client Machine**: Apple Silicon Mac (macOS Darwin arm64) running Mizan CLI `v0.2.0`.
* **Engine A (Cloud)**:
  * Backend: Google Cloud Vertex AI (location: `global`).
  * Model: `publishers/google/models/gemini-2.5-flash`.
  * Protocol: HTTPS REST / `google.golang.org/genai` with strict `ResponseSchema`.
* **Engine B (Local Metal)**:
  * Backend: Local DiffusionGemma Rust Metal inference server listening on `http://127.0.0.1:8080/v1`.
  * Model: `diffgemma-26b-a4b-it-q4` (4-bit quantized, ~18.8 GiB unified memory footprint).
  * Protocol: Discrete block diffusion slot readout over a 256-token canvas with multi-read noise sampling.

---

## Comparison Dimensions

Every experiment is executed using `mizan eval compare-engines` with parallel `errgroup` concurrency, evaluating:

1. **Verdict Agreement**:
   - `boul`: Matching boolean verdicts (`PASS` vs. `FAIL`).
   - `choice`: Matching category selection string.
   - `score`: Absolute delta between continuous ratings.
2. **Confidence & Uncertainty Calibration**:
   - Gemini: Generative confidence (uncalibrated or inferred from logprobs).
   - DiffusionGemma: Softmax probabilities, standard error ($\pm\sigma$), and Shannon entropy across $N$ denoise perturbation samples.
3. **Latency & Throughput**:
   - Wall-clock execution time and speedup factor ($\text{Duration}_A / \text{Duration}_B$).
   - Denoise forward pass time vs. KV prefill time.
4. **Qualitative Reasoning**:
   - Paragraph-length generative rationales vs. instant discrete slot logit readouts.

---

## Experiment Index

| # | Experiment | Template / Kind | Modality | Focus |
|---|---|---|---|---|
| **01** | [Categorical Support Triage](01-text-categorical-triage.md) | `quickstart/support-intent-choice` (`choice`) | Text | Discrete routing, multi-intent ambiguity, and probability splits |
| **02** | [Multimodal Vision & Multi-Rubrics](02-multimodal-vision-evaluation.md) | `demo/image-diagram-check` (`boul`), `quickstart/image-rubric-scorecard` (`rubric`) | Image (`.webp`) | Visual OCR, technical diagram divergence, and single-pass multi-criterion fanout |
| **03** | [Policy & Brand Safety Gating](03-policy-and-safety-gating.md) | `quickstart/brand-safety-boul` (`boul`) | Text | Proposition verification, adversarial inputs, and confidence calibration |
| **04** | [Comprehensive Benchmark Report (Local Metal)](04-comprehensive-benchmark-report.md) | Full Suite (32 cases) | Text + Vision | 32-case empirical benchmark on Apple Silicon Metal |
| **05** | [GCE vLLM Cloud GPU Benchmark Report](05-gce-vllm-benchmark-report.md) | Full Suite (32 cases) | Text + Vision | 32-case re-run on GCE 1× NVIDIA L4 GPU showing 3.7x latency speedup and 84.4% agreement |
