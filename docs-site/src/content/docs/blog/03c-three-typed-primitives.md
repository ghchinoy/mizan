---
title: "Beyond fuzzy text: turning LLM-as-a-judge into strongly-typed code with boul, choice, and score"
date: 2026-10-07
authors:
  - ghchinoy
excerpt: >
  Traditional LLM evals yield unstructured prose or require sprawling JSON
  schemas. Mizan introduces three strongly-typed decision primitives — boul,
  choice, and score — turning opaque eval SDKs into predictable developer code.
tags: ["evals", "llm-as-a-judge", "architecture"]
draft: false
series: "Build Evals with Mizan"
seriesOrder: 3.3
canonicalUrl: ""
---

The [previous playbooks](/mizan/blog/03a-per-persona-playbooks-part-1/) walked through
how four personas use evaluations in practice. Across all of them, a familiar
tension surfaces the moment you try to connect an LLM judge to automated code:
natural language models are great at subjective nuance, but software pipelines
demand deterministic, strongly-typed decisions.

If you ask an LLM judge to verify compliance or route a customer ticket using
standard prompt engineering, you quickly end up wrestling with format drift, regex
parsing hacks, or 50-line OpenAPI JSON schemas.

Mizan resolves this by introducing three top-level, strongly-typed decision
primitives: **`boul`**, **`choice`**, and **`score`**.

## The problem with opaque eval SDKs

The native Google Cloud Gen AI Evaluation Service SDK is built around academic
evaluation benchmarks (`pointwise`, `pairwise`, `custom_schema`, and `rubric`).
In production systems, however, developers rarely ask academic questions.
Instead, they almost always need to answer one of three concrete engineering questions:

1. **Proposition Verification**: *Did this artifact pass the condition?* (e.g. brand safety, PII detection, factual consistency).
2. **Categorical Routing**: *Which predefined bucket does this input belong to?* (e.g. support ticket triage, sentiment routing).
3. **Calibrated Grading**: *Where does this sit along an ordered, continuous scale?* (e.g. headline clarity, audio fidelity).

In the raw SDK, answering question #1 or #2 requires writing custom schema logic,
defining nested JSON objects, and manually validating that the judge's response
conforms to your desired types. If the model hallucinates an extra key or changes
its casing, your automated pipeline breaks.

## The inspiration: from mathematical primitives to developer code

At its heart, TypeSafe AI's Jev project demonstrated the power of dropping open-ended
text generation in favor of discrete, parallelized decision primitives. We loved that
conceptual clarity.

Mizan takes that inspiration and adapts it directly into developer-friendly CLI
and API abstractions above Google's generative infrastructure.

### 1. `boul` (Binary proposition check)

Why **`boul`**? It is a phonetic homophone for `bool`, with a respectful nod to
Jev's `noul`. To any software engineer, it is immediately recognizable and
explainable as a boolean evaluator.

A `boul` metric evaluates whether a proposition holds true or false. Under the hood,
Mizan compiles an optimized boolean schema, forcing Gemini to return a clean boolean
verdict along with a calibrated confidence float (0.0 to 1.0) and rationale:

```sh
mizan registry create --id safety/pii-check --name "PII Check" \
  --kind boul \
  --prompt "Does this customer message contain unmasked PII or credit card numbers? {{message}}"
```

Run it against a customer message:

```sh
mizan eval run --metric safety/pii-check \
  --field message="My phone is 555-0199 and card is 4111-2222-3333-4444"
```

The output gives you a definitive, programmatic verdict:

```
Passed:       FAIL (confidence=0.98)
Explanation:  The text contains an explicit phone number and credit card number.
```

If your pipeline reads JSON (`-o json`), `passed` is a native JSON boolean (`true` or `false`),
and `Score` is set to `1.0` or `0.0`. There are no regex matches or substring checks required.

### 2. `choice` (Categorical routing)

Google's native `pairwise` metric is notoriously rigid: it strictly compares two
candidates (A vs B). But real applications frequently need to route across three,
four, or twenty discrete categories.

With Mizan, you declare `kind: choice` and provide `--choices`:

```sh
mizan registry create --id support/intent --name "Ticket Intent" \
  --kind choice \
  --choices "billing, technical, account, sales, general" \
  --prompt "Classify the primary intent of this support message: {{message}}"
```

Under the hood, Mizan synthesizes an OpenAPI-compliant enum array of your choices
into a structured JSON schema. The model is constrained by Gemini's grammar-guided
decoding to select *only* from your predefined buckets:

```sh
mizan eval run --metric support/intent \
  --field message="I need a copy of last month's invoice."
```

```
Selection:    billing
Explanation:  The user is requesting billing and invoice documentation.
```

In `mizan results list` or `-o json`, the output cleanly surfaces the selected category
string.

### 3. `score` (Calibrated continuous grading)

When you need a score along a numeric range, `kind: score` maps data along an ordered
continuous scale:

```sh
mizan registry create --id copy/clarity --name "Ad Clarity" \
  --kind score \
  --prompt "Score the clarity and punchiness of this headline from 1 to 10: {{headline}}"
```

```sh
mizan eval run --metric copy/clarity \
  --field headline="Fast. Secure. Built for developers."
```

```
Score:        9
Explanation:  The headline is punchy, memorable, and immediately communicates value.
```

## Zero type errors via the direct genai structured path

The structural value Mizan brings is the translation layer between these three
primitives and Google Cloud's execution engines:

```
   [ Mizan CLI / Go API ]  <-- Developer thinks in 3 simple primitives
             │
   ┌─────────┴─────────┐
   ▼                   ▼
[ Decision Primitives] [ Execution Engine ]
  - boul                ├──> Direct genai structured JSON (location=global)
  - choice              ├──> Direct genai enum schema (location=global)
  - score               └──> Direct genai numeric / calibrated rubric
```

Rather than attempting to shoehorn these primitives into legacy, text-only API
formats, Mizan routes `boul`, `choice`, and `score` directly through
`google.golang.org/genai` using strict `ResponseSchema` and `ResponseMIMEType: "application/json"`
at `location=global`.

This architecture brings three immediate benefits:
1. **Zero Schema Boilerplate**: You never write raw OpenAPI schemas for boolean checks or category enums by hand.
2. **0% Format Hallucinations**: Because Gemini enforces the schema during token generation, the output is guaranteed to parse.
3. **Multimodal Native**: The exact same `boul` or `choice` primitive works on text, images, audio, and video files.

In the next piece, [Contribute a template pack](/mizan/blog/04-contribute-a-template-pack/),
we'll look at how to bundle these primitives into versioned, shareable packs that
anyone on your team can import.
