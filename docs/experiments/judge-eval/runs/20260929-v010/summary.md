| Run | Suite | n | Engine A | Acc A [95% CI] | p50 A | Engine B | Acc B [95% CI] | p50 B | Only A / only B right | McNemar p | Cascade h16 (esc%) | Cascade h35 (esc%) |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| j0_computation_text | computation_text | 300 | local | 100.0 [100-100] | 0 | vertex[service-default] | 59.3 [54-65] | 78 | 122 / 0 | 3.76e-37 | - | - |
| j0_computation_tools | computation_tools | 350 | local | - | 0 | vertex[service-default] | - | 77 | 0 / 0 | - | - | - |
| j1_vs_j2_pairwise_instruction_following | pairwise_instruction_following | 100 | dgem[vertex-dedicated] | 79.0 [71-87] | 147 | dgem[vertex-dedicated]+mirror | 82.0 [74-89] | 289 | 4 / 7 | 0.549 | - | - |
| j1_vs_j2_pairwise_mtbench | pairwise_mtbench | 100 | dgem[vertex-dedicated] | 70.0 [61-79] | 160 | dgem[vertex-dedicated]+mirror | 73.0 [64-82] | 318 | 3 / 6 | 0.508 | - | - |
| j1_vs_j2_pairwise_multiturn | pairwise_multiturn | 100 | dgem[vertex-dedicated] | 63.0 [54-73] | 203 | dgem[vertex-dedicated]+mirror | 64.0 [55-73] | 407 | 5 / 6 | 1 | - | - |
| j1_vs_j2_pairwise_rewardbench | pairwise_rewardbench | 100 | dgem[vertex-dedicated] | 85.0 [77-91] | 161 | dgem[vertex-dedicated]+mirror | 90.0 [84-96] | 311 | 0 / 5 | 0.0625 | - | - |
| j3_vs_j1c_groundedness | groundedness | 100 | vertex[gemini-3.5-flash-lite] | 81.0 [73-89] | 1397 | dgem[cloudrun] | 84.0 [77-91] | 177 | 8 / 11 | 0.648 | 80.0 [72-88] (41%) | 84.0 [76-91] (32%) |
| j3_vs_j1c_likert_coherence | likert_coherence | 100 | vertex[gemini-3.5-flash-lite] | 44.0 [34-54] | 1186 | dgem[cloudrun] | 39.0 [30-49] | 175 | 16 / 11 | 0.442 | 40.0 [31-50] (60%) | 41.0 [32-51] (30%) |
| j3_vs_j1c_likert_helpfulness | likert_helpfulness | 100 | vertex[gemini-3.5-flash-lite] | 40.0 [31-50] | 1352 | dgem[cloudrun] | 34.0 [25-43] | 175 | 26 / 20 | 0.461 | 36.0 [27-46] (54%) | 36.0 [27-46] (33%) |
| j3_vs_j1c_pairwise_instruction_following | pairwise_instruction_following | 100 | vertex[gemini-3.5-flash-lite] | 82.0 [74-89] | 1034 | dgem[cloudrun] | 79.0 [71-87] | 175 | 9 / 6 | 0.607 | 83.0 [75-90] (78%) | 84.0 [77-91] (57%) |
| j3_vs_j1c_pairwise_mtbench (+1 retried) | pairwise_mtbench | 100 | vertex[gemini-3.5-flash-lite] | 68.0 [59-77] | 1066 | dgem[cloudrun] | 72.0 [63-81] | 173 | 2 / 6 | 0.289 | 68.0 [59-77] (60%) | 68.0 [59-77] (41%) |
| j3_vs_j1c_pairwise_multiturn (+5 retried) | pairwise_multiturn | 100 | vertex[gemini-3.5-flash-lite] | 66.0 [57-75] (err 3) | 1069 | dgem[cloudrun] | 65.0 [56-74] | 190 | 7 / 7 | 1 | 66.0 [57-75] (95%) | 67.0 [58-76] (66%) |
| j3_vs_j1c_pairwise_rewardbench | pairwise_rewardbench | 100 | vertex[gemini-3.5-flash-lite] | 91.0 [85-96] | 1070 | dgem[cloudrun] | 87.0 [80-93] | 172 | 7 / 3 | 0.344 | 91.0 [85-96] (58%) | 90.0 [84-96] (31%) |
| j3_vs_j1c_safety_response | safety_response | 100 | vertex[gemini-3.5-flash-lite] | 79.0 [71-87] | 1338 | dgem[cloudrun] | 79.0 [71-86] | 177 | 2 / 2 | 1 | 79.0 [71-87] (15%) | 80.0 [72-87] (9%) |
| j3_vs_j1c_toxicity_prompt (+2 retried) | toxicity_prompt | 100 | vertex[gemini-3.5-flash-lite] | 85.7 [79-92] (err 2) | 1326 | dgem[cloudrun] | 81.0 [73-88] | 176 | 7 / 2 | 0.18 | 83.7 [77-91] (17%) | 83.7 [77-91] (12%) |
| j4_vs_j1_groundedness | groundedness | 100 | vertex[gemini-3.8-flash] | 78.0 [70-86] | 3685 | dgem[vertex-dedicated] | 81.0 [73-88] | 105 | 14 / 17 | 0.72 | 83.0 [75-90] (44%) | 86.0 [79-93] (32%) |
| j4_vs_j1_likert_coherence | likert_coherence | 100 | vertex[gemini-3.8-flash] | 46.0 [36-56] | 4680 | dgem[vertex-dedicated] | 38.0 [29-48] | 103 | 21 / 13 | 0.229 | 39.0 [30-49] (63%) | 37.0 [27-46] (26%) |
| j4_vs_j1_likert_helpfulness | likert_helpfulness | 100 | vertex[gemini-3.8-flash] | 43.0 [34-53] | 4352 | dgem[vertex-dedicated] | 35.0 [26-45] | 104 | 23 / 15 | 0.256 | 35.0 [26-45] (54%) | 34.0 [25-43] (34%) |
| j4_vs_j1_pairwise_instruction_following | pairwise_instruction_following | 100 | vertex[gemini-3.8-flash] | 94.0 [89-98] | 2796 | dgem[vertex-dedicated] | 79.0 [71-87] | 104 | 18 / 3 | 0.00149 | 94.0 [89-98] (80%) | 94.0 [89-98] (58%) |
| j4_vs_j1_pairwise_mtbench (+1 retried) | pairwise_mtbench | 100 | vertex[gemini-3.8-flash] | 78.0 [70-86] | 3433 | dgem[vertex-dedicated] | 73.0 [64-82] | 104 | 12 / 7 | 0.359 | 77.0 [69-85] (63%) | 75.0 [67-83] (41%) |
| j4_vs_j1_pairwise_multiturn | pairwise_multiturn | 100 | vertex[gemini-3.8-flash] | 71.0 [62-80] | 4225 | dgem[vertex-dedicated] | 65.0 [56-74] | 109 | 11 / 5 | 0.21 | 71.0 [62-80] (93%) | 71.0 [62-80] (73%) |
| j4_vs_j1_pairwise_rewardbench | pairwise_rewardbench | 100 | vertex[gemini-3.8-flash] | 90.0 [84-96] | 3236 | dgem[vertex-dedicated] | 87.0 [80-93] | 103 | 6 / 3 | 0.508 | 91.0 [85-96] (56%) | 91.0 [85-96] (31%) |
| j4_vs_j1_safety_response | safety_response | 100 | vertex[gemini-3.8-flash] | 76.0 [67-84] | 3748 | dgem[vertex-dedicated] | 80.0 [72-87] | 102 | 3 / 7 | 0.344 | 80.0 [72-87] (15%) | 81.0 [73-89] (8%) |
| j4_vs_j1_toxicity_prompt (+1 retried) | toxicity_prompt | 100 | vertex[gemini-3.8-flash] | 87.9 [81-94] (err 1) | 3789 | dgem[vertex-dedicated] | 81.0 [73-88] | 105 | 9 / 2 | 0.0654 | 84.8 [78-91] (17%) | 83.8 [76-91] (12%) |
| j6_vs_j1_prebuilt_coherence (+1 retried) | prebuilt_coherence | 100 | vertex[service-default] | 41.0 [32-50] | 9543 | dgem[vertex-dedicated] | 34.0 [25-43] | 104 | 28 / 21 | 0.392 | 40.0 [31-50] (68%) | 37.0 [28-46] (59%) |
| j6_vs_j1_prebuilt_fluency | prebuilt_fluency | 90 | vertex[service-default] | 32.2 [23-41] | 12609 | dgem[vertex-dedicated] | 46.7 [37-57] | 101 | 11 / 24 | 0.041 | 34.4 [26-44] (84%) | 38.9 [29-49] (64%) |
| j6_vs_j1_prebuilt_groundedness | prebuilt_groundedness | 100 | vertex[service-default] | 79.0 [71-87] | 5401 | dgem[vertex-dedicated] | 79.0 [70-87] | 104 | 11 / 11 | 1 | 80.0 [72-88] (33%) | 78.0 [69-86] (19%) |
| j6_vs_j1_prebuilt_safety | prebuilt_safety | 100 | vertex[service-default] | 81.0 [73-88] | 3925 | dgem[vertex-dedicated] | 68.0 [59-77] | 99 | 13 / 0 | 0.000244 | 74.0 [65-82] (15%) | 73.0 [64-81] (7%) |
| lat_j1g | latency_probe.fast.js | 30 | dgem[vertex_first] | 96.7 [90-100] | 169 | dgem[vertex_first] | 96.7 [90-100] | 170 | 0 / 0 | 1 | - | - |
| lat_j3_vs_j1c | latency_probe.fast.js | 30 | vertex[gemini-3.5-flash-lite] | 96.7 [90-100] | 1429 | dgem[cloudrun] | 96.7 [90-100] | 182 | 0 / 0 | 1 | 96.7 [90-100] (3%) | 96.7 [90-100] (0%) |
| lat_j4_vs_j1 | latency_probe.slow.js | 30 | vertex[gemini-3.8-flash] | 96.7 [90-100] | 3125 | dgem[vertex-dedicated] | 96.7 [90-100] | 105 | 0 / 0 | 1 | 96.7 [90-100] (0%) | 96.7 [90-100] (0%) |

| Run | Engine | Likert n | MAE | Spearman |
|---|---|---|---|---|
| j0_computation_text | local | 300 | 0.00 | 1.000 |
| j0_computation_text | vertex[service-default] | 300 | 0.02 | 0.989 |
| j3_vs_j1c_likert_coherence | vertex[gemini-3.5-flash-lite] | 100 | 0.78 | 0.773 |
| j3_vs_j1c_likert_coherence | dgem[cloudrun] | 100 | 0.90 | 0.739 |
| j3_vs_j1c_likert_helpfulness | vertex[gemini-3.5-flash-lite] | 100 | 0.88 | 0.665 |
| j3_vs_j1c_likert_helpfulness | dgem[cloudrun] | 100 | 0.90 | 0.652 |
| j4_vs_j1_likert_coherence | vertex[gemini-3.8-flash] | 100 | 0.70 | 0.778 |
| j4_vs_j1_likert_coherence | dgem[vertex-dedicated] | 100 | 0.89 | 0.742 |
| j4_vs_j1_likert_helpfulness | vertex[gemini-3.8-flash] | 100 | 0.77 | 0.695 |
| j4_vs_j1_likert_helpfulness | dgem[vertex-dedicated] | 100 | 0.90 | 0.652 |
| j6_vs_j1_prebuilt_coherence | vertex[service-default] | 100 | 0.79 | 0.712 |
| j6_vs_j1_prebuilt_coherence | dgem[vertex-dedicated] | 100 | 1.04 | 0.640 |
| j6_vs_j1_prebuilt_fluency | vertex[service-default] | 90 | 1.02 | 0.658 |
| j6_vs_j1_prebuilt_fluency | dgem[vertex-dedicated] | 90 | 0.75 | 0.688 |

| Run | Engine | Pairwise position consistency |
|---|---|---|
| j1_vs_j2_pairwise_instruction_following | dgem[vertex-dedicated]+mirror | 73.0% |
| j1_vs_j2_pairwise_mtbench | dgem[vertex-dedicated]+mirror | 80.0% |
| j1_vs_j2_pairwise_multiturn | dgem[vertex-dedicated]+mirror | 76.0% |
| j1_vs_j2_pairwise_rewardbench | dgem[vertex-dedicated]+mirror | 88.0% |
