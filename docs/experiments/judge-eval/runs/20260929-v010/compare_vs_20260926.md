| Run | Engine | n paired | Acc 20260926 | Acc 20260929-v010 | only old right | only new right | McNemar p |
|---|---|---|---|---|---|---|---|
| j0_computation_text | local | 300 | 100.0 | 100.0 | 0 | 0 | 1 |
| j0_computation_text | vertex[service-default] | 300 | 59.3 | 59.3 | 0 | 0 | 1 |
| j1_vs_j2_pairwise_instruction_following | dgem[vertex-dedicated] | 100 | 79.0 | 79.0 | 3 | 3 | 1 |
| j1_vs_j2_pairwise_instruction_following | dgem[vertex-dedicated]+mirror | 100 | 79.0 | 82.0 | 3 | 6 | 0.508 |
| j1_vs_j2_pairwise_mtbench | dgem[vertex-dedicated] | 100 | 69.0 | 70.0 | 3 | 4 | 1 |
| j1_vs_j2_pairwise_mtbench | dgem[vertex-dedicated]+mirror | 100 | 71.0 | 73.0 | 3 | 5 | 0.727 |
| j1_vs_j2_pairwise_multiturn | dgem[vertex-dedicated] | 100 | 66.0 | 63.0 | 8 | 5 | 0.581 |
| j1_vs_j2_pairwise_multiturn | dgem[vertex-dedicated]+mirror | 100 | 68.0 | 64.0 | 5 | 1 | 0.219 |
| j1_vs_j2_pairwise_rewardbench | dgem[vertex-dedicated] | 100 | 87.0 | 85.0 | 2 | 0 | 0.5 |
| j1_vs_j2_pairwise_rewardbench | dgem[vertex-dedicated]+mirror | 100 | 91.0 | 90.0 | 2 | 1 | 1 |
| j3_vs_j1c_groundedness | vertex[gemini-3.5-flash-lite] | 100 | 79.0 | 81.0 | 1 | 3 | 0.625 |
| j3_vs_j1c_groundedness | dgem[cloudrun] | 100 | 85.0 | 84.0 | 5 | 4 | 1 |
| j3_vs_j1c_likert_coherence | vertex[gemini-3.5-flash-lite] | 100 | 43.0 | 44.0 | 5 | 6 | 1 |
| j3_vs_j1c_likert_coherence | dgem[cloudrun] | 100 | 38.0 | 39.0 | 6 | 7 | 1 |
| j3_vs_j1c_likert_helpfulness | vertex[gemini-3.5-flash-lite] | 100 | 47.0 | 40.0 | 7 | 0 | 0.0156 |
| j3_vs_j1c_likert_helpfulness | dgem[cloudrun] | 100 | 37.0 | 34.0 | 6 | 3 | 0.508 |
| j3_vs_j1c_pairwise_instruction_following | vertex[gemini-3.5-flash-lite] | 100 | 85.0 | 82.0 | 3 | 0 | 0.25 |
| j3_vs_j1c_pairwise_instruction_following | dgem[cloudrun] | 100 | 77.0 | 79.0 | 2 | 4 | 0.688 |
| j3_vs_j1c_pairwise_mtbench | vertex[gemini-3.5-flash-lite] | 100 | 69.0 | 68.0 | 2 | 1 | 1 |
| j3_vs_j1c_pairwise_mtbench | dgem[cloudrun] | 100 | 68.0 | 72.0 | 2 | 6 | 0.289 |
| j3_vs_j1c_pairwise_multiturn | vertex[gemini-3.5-flash-lite] | 97 | 64.9 | 66.0 | 3 | 4 | 1 |
| j3_vs_j1c_pairwise_multiturn | dgem[cloudrun] | 100 | 64.0 | 65.0 | 2 | 3 | 1 |
| j3_vs_j1c_pairwise_rewardbench | vertex[gemini-3.5-flash-lite] | 98 | 90.8 | 90.8 | 0 | 0 | 1 |
| j3_vs_j1c_pairwise_rewardbench | dgem[cloudrun] | 100 | 86.0 | 87.0 | 2 | 3 | 1 |
| j3_vs_j1c_safety_response | vertex[gemini-3.5-flash-lite] | 100 | 80.0 | 79.0 | 1 | 0 | 1 |
| j3_vs_j1c_safety_response | dgem[cloudrun] | 100 | 77.0 | 79.0 | 1 | 3 | 0.625 |
| j3_vs_j1c_toxicity_prompt | vertex[gemini-3.5-flash-lite] | 98 | 83.7 | 85.7 | 1 | 3 | 0.625 |
| j3_vs_j1c_toxicity_prompt | dgem[cloudrun] | 100 | 77.0 | 81.0 | 0 | 4 | 0.125 |
| j4_vs_j1_groundedness | vertex[gemini-3.8-flash] | 100 | 76.0 | 78.0 | 0 | 2 | 0.5 |
| j4_vs_j1_groundedness | dgem[vertex-dedicated] | 100 | 82.0 | 81.0 | 3 | 2 | 1 |
| j4_vs_j1_likert_coherence | vertex[gemini-3.8-flash] | 100 | 45.0 | 46.0 | 6 | 7 | 1 |
| j4_vs_j1_likert_coherence | dgem[vertex-dedicated] | 100 | 35.0 | 38.0 | 3 | 6 | 0.508 |
| j4_vs_j1_likert_helpfulness | vertex[gemini-3.8-flash] | 100 | 44.0 | 43.0 | 4 | 3 | 1 |
| j4_vs_j1_likert_helpfulness | dgem[vertex-dedicated] | 100 | 37.0 | 35.0 | 5 | 3 | 0.727 |
| j4_vs_j1_pairwise_instruction_following | vertex[gemini-3.8-flash] | 100 | 93.0 | 94.0 | 0 | 1 | 1 |
| j4_vs_j1_pairwise_instruction_following | dgem[vertex-dedicated] | 100 | 79.0 | 79.0 | 4 | 4 | 1 |
| j4_vs_j1_pairwise_mtbench | vertex[gemini-3.8-flash] | 100 | 76.0 | 78.0 | 1 | 3 | 0.625 |
| j4_vs_j1_pairwise_mtbench | dgem[vertex-dedicated] | 100 | 67.0 | 73.0 | 2 | 8 | 0.109 |
| j4_vs_j1_pairwise_multiturn | vertex[gemini-3.8-flash] | 100 | 71.0 | 71.0 | 0 | 0 | 1 |
| j4_vs_j1_pairwise_multiturn | dgem[vertex-dedicated] | 100 | 61.0 | 65.0 | 3 | 7 | 0.344 |
| j4_vs_j1_pairwise_rewardbench | vertex[gemini-3.8-flash] | 100 | 90.0 | 90.0 | 1 | 1 | 1 |
| j4_vs_j1_pairwise_rewardbench | dgem[vertex-dedicated] | 100 | 86.0 | 87.0 | 2 | 3 | 1 |
| j4_vs_j1_safety_response | vertex[gemini-3.8-flash] | 100 | 75.0 | 76.0 | 0 | 1 | 1 |
| j4_vs_j1_safety_response | dgem[vertex-dedicated] | 100 | 76.0 | 80.0 | 1 | 5 | 0.219 |
| j4_vs_j1_toxicity_prompt | vertex[gemini-3.8-flash] | 98 | 89.8 | 87.8 | 3 | 1 | 0.625 |
| j4_vs_j1_toxicity_prompt | dgem[vertex-dedicated] | 100 | 79.0 | 81.0 | 1 | 3 | 0.625 |
| j6_vs_j1_prebuilt_coherence | vertex[service-default] | 100 | 43.0 | 41.0 | 9 | 7 | 0.804 |
| j6_vs_j1_prebuilt_coherence | dgem[vertex-dedicated] | 100 | 35.0 | 34.0 | 7 | 6 | 1 |
| j6_vs_j1_prebuilt_fluency | vertex[service-default] | 90 | 34.4 | 32.2 | 6 | 4 | 0.754 |
| j6_vs_j1_prebuilt_fluency | dgem[vertex-dedicated] | 90 | 41.1 | 46.7 | 5 | 10 | 0.302 |
| j6_vs_j1_prebuilt_groundedness | vertex[service-default] | 100 | 82.0 | 79.0 | 3 | 0 | 0.25 |
| j6_vs_j1_prebuilt_groundedness | dgem[vertex-dedicated] | 100 | 77.0 | 79.0 | 3 | 5 | 0.727 |
| j6_vs_j1_prebuilt_safety | vertex[service-default] | 100 | 79.0 | 81.0 | 0 | 2 | 0.5 |
| j6_vs_j1_prebuilt_safety | dgem[vertex-dedicated] | 100 | 72.0 | 68.0 | 4 | 0 | 0.125 |
| lat_j3_vs_j1c | vertex[gemini-3.5-flash-lite] | 30 | 96.7 | 96.7 | 0 | 0 | 1 |
| lat_j3_vs_j1c | dgem[cloudrun] | 30 | 96.7 | 96.7 | 0 | 0 | 1 |
| lat_j4_vs_j1 | vertex[gemini-3.8-flash] | 30 | 96.7 | 96.7 | 0 | 0 | 1 |
| lat_j4_vs_j1 | dgem[vertex-dedicated] | 30 | 96.7 | 96.7 | 0 | 0 | 1 |

| Engine (pooled over runs) | n paired | Acc old | Acc new | only old | only new | McNemar p |
|---|---|---|---|---|---|---|
| dgem[cloudrun] | 930 | 68.6 | 69.8 | 26 | 37 | 0.207 |
| dgem[vertex-dedicated] | 1720 | 67.0 | 67.9 | 59 | 74 | 0.225 |
| dgem[vertex-dedicated]+mirror | 400 | 77.2 | 77.2 | 13 | 13 | 1 |
| local | 300 | 100.0 | 100.0 | 0 | 0 | 1 |
| vertex[gemini-3.5-flash-lite] | 923 | 72.2 | 71.5 | 23 | 17 | 0.43 |
| vertex[gemini-3.8-flash] | 928 | 74.0 | 74.5 | 15 | 19 | 0.608 |
| vertex[service-default] | 690 | 59.9 | 59.1 | 18 | 13 | 0.473 |
