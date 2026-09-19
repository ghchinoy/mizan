# Experiment 01: Categorical Support Triage

* **Metric Template**: `quickstart/support-intent-choice`
* **Kind**: `choice`
* **Choices**: `billing`, `technical`, `account`, `general`
* **Prompt**: `Classify the primary intent of this support message: {{message}}`

---

## Case 1.1: Unambiguous Technical Query

### Input
```
"Our PostgreSQL database connection keeps throwing 500 socket timeout errors."
```

### Command
```bash
mizan eval compare-engines \
  --metric quickstart/support-intent-choice \
  --field message="Our PostgreSQL database connection keeps throwing 500 socket timeout errors." \
  --engine-a vertex \
  --engine-b diffusion
```

### Empirical Results
```
Metric:             quickstart/support-intent-choice (choice)
Verdict Agreement:  AGREEMENT (Match)

DIMENSION     ENGINE A (VERTEX)                                              ENGINE B (DIFFUSION)
---------     -------------                                                  -------------
Selection:    technical                                                      technical
Confidence:   -                                                              99.8% (stderr: ±0.0018)
Duration:     1385.0 ms                                                      7171.0 ms
Explanation:  The message describes a specific technical problem related to  DiffusionGemma classification: technical (confidence: 99.8%)
              a PostgreSQL database connection and socket timeout errors...
```

### DiffusionGemma Probability Distribution
```json
{
  "technical": 0.9976759,
  "billing":   0.0011140,
  "account":   0.0010255,
  "general":   0.0001846
}
```
* **Takeaway**: 100% agreement. DiffusionGemma assigned 99.77% mass to `technical` with negligible variance ($\pm 0.0018$), matching Gemini's assessment.

---

## Case 1.2: Unambiguous Billing Query

### Input
```
"Can you send me a copy of my July invoice?"
```

### Command
```bash
mizan eval compare-engines \
  --metric quickstart/support-intent-choice \
  --field message="Can you send me a copy of my July invoice?" \
  --engine-a vertex \
  --engine-b diffusion
```

### Empirical Results
```
Metric:             quickstart/support-intent-choice (choice)
Verdict Agreement:  AGREEMENT (Match)

DIMENSION     ENGINE A (VERTEX)                                                    ENGINE B (DIFFUSION)
---------     -------------                                                        -------------
Selection:    billing                                                              billing
Confidence:   -                                                                    97.7% (stderr: ±0.0129)
Duration:     1570.0 ms                                                            7282.0 ms
Explanation:  The user is requesting a copy of an invoice, which is directly       DiffusionGemma classification: billing (confidence: 97.7%)
              related to billing and financial records.
```

### DiffusionGemma Probability Distribution
```json
{
  "billing":   0.9768074,
  "technical": 0.0225622,
  "account":   0.0005511,
  "general":   0.0000793
}
```
* **Takeaway**: 100% agreement. Notice the slight noise leakage into `technical` (2.25%), producing an empirical $\pm\text{stderr}$ of $\pm 0.0129$.

---

## Case 1.3: Conflicting / Multi-Intent Boundary Test

### Input
```
"I was double-charged $500 for my annual subscription because your server was throwing 500 API errors. Fix the database bug or give me a refund."
```

### Command
```bash
mizan eval compare-engines \
  --metric quickstart/support-intent-choice \
  --field message="I was double-charged $500 for my annual subscription because your server was throwing 500 API errors. Fix the database bug or give me a refund." \
  --engine-a vertex \
  --engine-b diffusion
```

### Empirical Results
```
Metric:             quickstart/support-intent-choice (choice)
Verdict Agreement:  AGREEMENT (Match)

DIMENSION     ENGINE A (VERTEX)                                                    ENGINE B (DIFFUSION)
---------     -------------                                                        -------------
Selection:    billing                                                              billing
Confidence:   -                                                                    98.5% (stderr: ±0.0091)
Duration:     2842.0 ms                                                            8848.0 ms
Explanation:  The user's primary concern is a double charge for a subscription     DiffusionGemma classification: billing (confidence: 98.5%)
              and explicitly requests a refund, which falls under billing issues.
              While a technical error is cited as the cause, the desired
              resolution is financial.
```

### Analysis & Insight
Despite the message containing prominent technical keywords (`server`, `500 API errors`, `database bug`), both engines independently determined that the primary operational routing intent is **financial / billing**. Gemini's reasoning clearly articulates the hierarchy of intent (*"While a technical error is cited as the cause, the desired resolution is financial"*), while DiffusionGemma's discrete attention heads converged on `billing` with 98.5% probability.
