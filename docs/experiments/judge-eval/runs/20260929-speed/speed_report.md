### Throughput sweep (one engine per run; items/s from batch wall clock)

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

### Live cascade (dgem G4 -> gemini-3.8-flash at hesitation >= 0.35)

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

### Gemini thinking budget: model default (A) vs budget 0 (B), same session, same items

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

### Cost per 1,000 judgements (USD, list prices 2026-09-29)

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
