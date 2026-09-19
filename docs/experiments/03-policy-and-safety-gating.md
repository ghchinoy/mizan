# Experiment 03: Policy & Brand Safety Gating

* **Metric Template**: `quickstart/brand-safety-boul`
* **Kind**: `boul`
* **Prompt**: `Evaluate if the following text is safe, polite, and free of profanity or offensive claims: {{text}}`

---

## Case 3.1: Courteous Marketing Copy (Safe Pass)

### Input
```
"Welcome to our summer product catalog!"
```

### Command
```bash
mizan eval run \
  --metric quickstart/brand-safety-boul \
  --engine diffusion \
  --field text="Welcome to our summer product catalog!"
```

### Output
```
Passed:                       PASS (confidence=1.00)
Score:                        1
Explanation:                  DiffusionGemma verdict: yes (confidence: 100.0%, stderr: ±0.0000)
CustomOutput[denoise_ms]:     1777.76
CustomOutput[passed]:         true
CustomOutput[probabilities]:  map[no:3.92e-07 yes:0.9999996]
CustomOutput[steps]:          1
```

---

## Case 3.2: Severe Policy Violation (Fraud / Illicit Gambling)

### Input
```
"Visit our illegal casino website to win cryptocurrency instantly with unverified cards!"
```

### Command
```bash
mizan eval compare-engines \
  --metric quickstart/brand-safety-boul \
  --field text="Visit our illegal casino website to win cryptocurrency instantly with unverified cards!" \
  --engine-a vertex \
  --engine-b diffusion
```

### Empirical Results
```
Metric:             quickstart/brand-safety-boul (boul)
Verdict Agreement:  AGREEMENT (Match)

DIMENSION     ENGINE A (VERTEX)                                              ENGINE B (DIFFUSION)
---------     -------------                                                  -------------
Passed:       FAIL                                                           FAIL
Score:        0.00                                                           0.00
Confidence:   100.0%                                                         100.0%
Duration:     2045.0 ms                                                      6184.0 ms
Explanation:  The text promotes an 'illegal casino' and suggests using       DiffusionGemma verdict: no (confidence: 100.0%, stderr: ±0.0000)
              'unverified cards,' which encourages illegal activity and
              financial risk. This is neither safe nor polite, and it
              contains claims that promote illicit behavior.
```

### Analysis & Takeaway
Both models reached absolute mathematical consensus on the safety boundary:
- **Vertex AI Gemini**: Generated a detailed, comprehensive legal/policy explanation identifying financial fraud and illicit activities.
- **DiffusionGemma**: Assigned 100.0% probability mass to `no` with zero empirical standard error ($\pm 0.0000$), returning a clean `FAIL` gate in a single forward pass.
- **Production Pipeline Value**: Teams can run local DiffusionGemma as an instant pre-commit or real-time ingestion gate; any input that yields `FAIL` with $\text{stderr} < \pm 0.01$ is immediately rejected without spending API tokens, while borderline cases ($\text{stderr} > \pm 0.05$) can be escalated to Vertex AI Gemini for human-readable audit explanations.
