| Run | Suite | n | Engine A | Acc A [95% CI] | p50 A | Engine B | Acc B [95% CI] | p50 B | Only A / only B right | McNemar p | Cascade h16 (esc%) | Cascade h35 (esc%) |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| j0_computation_text | computation_text | 300 | local | 100.0 [100-100] | 0 | vertex[service-default] | 59.3 [54-65] | 69 | 122 / 0 | 3.76e-37 | - | - |
| j0_computation_tools | computation_tools | 350 | local | - | 0 | vertex[service-default] | - | 74 | 0 / 0 | - | - | - |
| j1_vs_j2_pairwise_instruction_following | pairwise_instruction_following | 100 | dgem[vertex-dedicated] | 79.0 [71-87] | 147 | dgem[vertex-dedicated]+mirror | 79.0 [71-87] | 275 | 5 / 5 | 1 | - | - |
| j1_vs_j2_pairwise_mtbench | pairwise_mtbench | 100 | dgem[vertex-dedicated] | 69.0 [60-78] | 176 | dgem[vertex-dedicated]+mirror | 71.0 [62-79] | 352 | 2 / 4 | 0.688 | - | - |
| j1_vs_j2_pairwise_multiturn | pairwise_multiturn | 100 | dgem[vertex-dedicated] | 66.0 [57-75] | 209 | dgem[vertex-dedicated]+mirror | 68.0 [59-77] | 398 | 3 / 5 | 0.727 | - | - |
| j1_vs_j2_pairwise_rewardbench | pairwise_rewardbench | 100 | dgem[vertex-dedicated] | 87.0 [80-93] | 163 | dgem[vertex-dedicated]+mirror | 91.0 [85-96] | 307 | 2 / 6 | 0.289 | - | - |
| j3_vs_j1c_groundedness | groundedness | 100 | vertex[gemini-3.5-flash-lite] | 79.0 [71-87] | 921 | dgem[cloudrun] | 85.0 [78-92] | 180 | 7 / 13 | 0.263 | 80.0 [72-88] (41%) | 81.0 [73-88] (31%) |
| j3_vs_j1c_likert_coherence | likert_coherence | 100 | vertex[gemini-3.5-flash-lite] | 43.0 [33-52] | 945 | dgem[cloudrun] | 38.0 [29-48] | 169 | 16 / 11 | 0.442 | 37.0 [28-46] (56%) | 34.0 [25-43] (27%) |
| j3_vs_j1c_likert_helpfulness | likert_helpfulness | 100 | vertex[gemini-3.5-flash-lite] | 47.0 [37-57] | 976 | dgem[cloudrun] | 37.0 [28-47] | 161 | 27 / 17 | 0.174 | 40.0 [30-50] (66%) | 39.0 [30-48] (44%) |
| j3_vs_j1c_pairwise_instruction_following | pairwise_instruction_following | 100 | vertex[gemini-3.5-flash-lite] | 85.0 [77-92] | 805 | dgem[cloudrun] | 77.0 [68-85] | 168 | 13 / 5 | 0.0963 | 84.0 [76-91] (75%) | 85.0 [77-92] (50%) |
| j3_vs_j1c_pairwise_mtbench (+2 retried) | pairwise_mtbench | 100 | vertex[gemini-3.5-flash-lite] | 69.0 [60-78] | 873 | dgem[cloudrun] | 68.0 [59-77] | 171 | 5 / 4 | 1 | 69.0 [60-78] (67%) | 70.0 [61-79] (46%) |
| j3_vs_j1c_pairwise_multiturn (+1 retried) | pairwise_multiturn | 100 | vertex[gemini-3.5-flash-lite] | 65.7 [56-75] (err 1) | 875 | dgem[cloudrun] | 64.0 [54-73] | 186 | 10 / 9 | 1 | 65.7 [56-75] (90%) | 66.7 [57-76] (62%) |
| j3_vs_j1c_pairwise_rewardbench (+2 retried) | pairwise_rewardbench | 100 | vertex[gemini-3.5-flash-lite] | 90.8 [85-96] (err 2) | 831 | dgem[cloudrun] | 86.0 [79-92] | 163 | 6 / 3 | 0.508 | 89.8 [84-96] (57%) | 89.8 [84-96] (35%) |
| j3_vs_j1c_safety_response | safety_response | 100 | vertex[gemini-3.5-flash-lite] | 80.0 [72-88] | 823 | dgem[cloudrun] | 77.0 [69-85] | 167 | 5 / 2 | 0.453 | 79.0 [71-87] (14%) | 79.0 [71-87] (12%) |
| j3_vs_j1c_toxicity_prompt (+4 retried) | toxicity_prompt | 100 | vertex[gemini-3.5-flash-lite] | 83.7 [77-91] (err 2) | 835 | dgem[cloudrun] | 77.0 [69-85] | 163 | 9 / 2 | 0.0654 | 81.6 [74-89] (17%) | 77.6 [69-86] (10%) |
| j4_vs_j1_groundedness (+20 retried) | groundedness | 100 | vertex[gemini-3.8-flash] | 76.0 [68-84] | 4196 | dgem[vertex-dedicated] | 82.0 [74-89] | 100 | 12 / 18 | 0.362 | 83.0 [75-90] (38%) | 84.0 [76-91] (31%) |
| j4_vs_j1_likert_coherence | likert_coherence | 100 | vertex[gemini-3.8-flash] | 45.0 [35-55] | 4665 | dgem[vertex-dedicated] | 35.0 [26-44] | 98 | 20 / 10 | 0.0987 | 38.0 [29-48] (56%) | 39.0 [30-49] (28%) |
| j4_vs_j1_likert_helpfulness | likert_helpfulness | 100 | vertex[gemini-3.8-flash] | 44.0 [35-54] | 5226 | dgem[vertex-dedicated] | 37.0 [28-47] | 101 | 24 / 17 | 0.349 | 39.0 [30-49] (69%) | 37.0 [28-47] (42%) |
| j4_vs_j1_pairwise_instruction_following (+16 retried) | pairwise_instruction_following | 100 | vertex[gemini-3.8-flash] | 93.0 [88-98] | 3411 | dgem[vertex-dedicated] | 79.0 [71-87] | 99 | 17 / 3 | 0.00258 | 91.0 [85-96] (75%) | 92.0 [86-97] (51%) |
| j4_vs_j1_pairwise_mtbench (+19 retried) | pairwise_mtbench | 100 | vertex[gemini-3.8-flash] | 76.0 [68-84] | 4022 | dgem[vertex-dedicated] | 67.0 [58-76] | 101 | 12 / 3 | 0.0352 | 74.0 [66-83] (69%) | 73.0 [65-82] (48%) |
| j4_vs_j1_pairwise_multiturn (+26 retried) | pairwise_multiturn | 100 | vertex[gemini-3.8-flash] | 71.0 [62-80] | 4854 | dgem[vertex-dedicated] | 61.0 [51-70] | 114 | 13 / 3 | 0.0213 | 71.0 [62-80] (91%) | 71.0 [62-80] (63%) |
| j4_vs_j1_pairwise_rewardbench (+27 retried) | pairwise_rewardbench | 100 | vertex[gemini-3.8-flash] | 90.0 [83-95] | 4059 | dgem[vertex-dedicated] | 86.0 [79-93] | 103 | 9 / 5 | 0.424 | 89.0 [82-94] (60%) | 90.0 [84-95] (36%) |
| j4_vs_j1_safety_response (+16 retried) | safety_response | 100 | vertex[gemini-3.8-flash] | 75.0 [67-83] | 3504 | dgem[vertex-dedicated] | 76.0 [68-84] | 102 | 5 / 6 | 1 | 78.0 [70-86] (14%) | 80.0 [72-87] (10%) |
| j4_vs_j1_toxicity_prompt (+26 retried) | toxicity_prompt | 100 | vertex[gemini-3.8-flash] | 89.8 [84-95] (err 2) | 3923 | dgem[vertex-dedicated] | 79.0 [71-86] | 97 | 12 / 1 | 0.00342 | 86.7 [80-93] (17%) | 83.7 [77-91] (11%) |
| j6_vs_j1_prebuilt_coherence (+23 retried) | prebuilt_coherence | 100 | vertex[service-default] | 43.0 [33-52] | 8685 | dgem[vertex-dedicated] | 35.0 [26-44] | 100 | 28 / 20 | 0.312 | 41.0 [31-51] (79%) | 36.0 [27-46] (57%) |
| j6_vs_j1_prebuilt_fluency (+34 retried) | prebuilt_fluency | 90 | vertex[service-default] | 34.4 [24-44] | 12056 | dgem[vertex-dedicated] | 41.1 [31-51] | 100 | 12 / 18 | 0.362 | 35.6 [26-46] (84%) | 37.8 [28-48] (68%) |
| j6_vs_j1_prebuilt_groundedness (+27 retried) | prebuilt_groundedness | 100 | vertex[service-default] | 82.0 [74-89] | 5439 | dgem[vertex-dedicated] | 77.0 [68-85] | 105 | 14 / 9 | 0.405 | 84.0 [77-91] (32%) | 84.0 [77-91] (20%) |
| j6_vs_j1_prebuilt_safety | prebuilt_safety | 100 | vertex[service-default] | 79.0 [71-87] | 3726 | dgem[vertex-dedicated] | 72.0 [63-81] | 97 | 8 / 1 | 0.0391 | 76.0 [68-84] (16%) | 76.0 [67-84] (9%) |
| lat_j3_vs_j1c | latency_probe.js | 30 | vertex[gemini-3.5-flash-lite] | 96.7 [90-100] | 998 | dgem[cloudrun] | 96.7 [90-100] | 178 | 0 / 0 | 1 | 96.7 [90-100] (0%) | 96.7 [90-100] (0%) |
| lat_j4_vs_j1 | latency_probe.js | 30 | vertex[gemini-3.8-flash] | 96.7 [90-100] | 3050 | dgem[vertex-dedicated] | 96.7 [90-100] | 97 | 0 / 0 | 1 | 96.7 [90-100] (0%) | 96.7 [90-100] (0%) |

| Run | Engine | Likert n | MAE | Spearman |
|---|---|---|---|---|
| j0_computation_text | local | 300 | 0.00 | 1.000 |
| j0_computation_text | vertex[service-default] | 300 | 0.02 | 0.989 |
| j3_vs_j1c_likert_coherence | vertex[gemini-3.5-flash-lite] | 100 | 0.76 | 0.787 |
| j3_vs_j1c_likert_coherence | dgem[cloudrun] | 100 | 0.92 | 0.711 |
| j3_vs_j1c_likert_helpfulness | vertex[gemini-3.5-flash-lite] | 100 | 0.82 | 0.663 |
| j3_vs_j1c_likert_helpfulness | dgem[cloudrun] | 100 | 0.88 | 0.651 |
| j4_vs_j1_likert_coherence | vertex[gemini-3.8-flash] | 100 | 0.71 | 0.777 |
| j4_vs_j1_likert_coherence | dgem[vertex-dedicated] | 100 | 0.95 | 0.719 |
| j4_vs_j1_likert_helpfulness | vertex[gemini-3.8-flash] | 100 | 0.77 | 0.681 |
| j4_vs_j1_likert_helpfulness | dgem[vertex-dedicated] | 100 | 0.88 | 0.652 |
| j6_vs_j1_prebuilt_coherence | vertex[service-default] | 100 | 0.74 | 0.758 |
| j6_vs_j1_prebuilt_coherence | dgem[vertex-dedicated] | 100 | 1.06 | 0.558 |
| j6_vs_j1_prebuilt_fluency | vertex[service-default] | 90 | 1.01 | 0.651 |
| j6_vs_j1_prebuilt_fluency | dgem[vertex-dedicated] | 90 | 0.76 | 0.709 |

| Run | Engine | Pairwise position consistency |
|---|---|---|
| j1_vs_j2_pairwise_instruction_following | dgem[vertex-dedicated]+mirror | 73.0% |
| j1_vs_j2_pairwise_mtbench | dgem[vertex-dedicated]+mirror | 78.0% |
| j1_vs_j2_pairwise_multiturn | dgem[vertex-dedicated]+mirror | 80.0% |
| j1_vs_j2_pairwise_rewardbench | dgem[vertex-dedicated]+mirror | 88.0% |
