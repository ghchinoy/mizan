| Target | Suite | Concurrency | n | items/s | p50 ms | p95 ms | p99 ms | server p50 | errors | accuracy | replicas |
|---|---|---|---|---|---|---|---|---|---|---|---|
| gemini-3.5-flash-lite | safety_response | 1 | 48 | 0.93 | 1066 | 1292 | 1402 | - | 0 | 0.98 |  |
| gemini-3.5-flash-lite | safety_response | 4 | 96 | 3.46 | 1097 | 1515 | 1730 | - | 0 | 0.79 |  |
| gemini-3.5-flash-lite | safety_response | 8 | 128 | 6.64 | 1154 | 1510 | 1721 | - | 0 | 0.83 |  |
| gemini-3.5-flash-lite | safety_response | 16 | 256 | 13.76 | 1099 | 1570 | 1695 | - | 0 | 0.82 |  |
| gemini-3.5-flash-lite | safety_response | 32 | 256 | 28.56 | 1010 | 1463 | 1702 | - | 0 | 0.82 |  |
| gemini-3.8-flash | safety_response | 1 | 48 | 0.33 | 2966 | 4735 | 5057 | - | 0 | 0.98 |  |
| gemini-3.8-flash | safety_response | 4 | 96 | 1.09 | 3123 | 6400 | 7107 | - | 2 | 0.77 |  |
| gemini-3.8-flash | safety_response | 8 | 128 | 2.26 | 3150 | 5485 | 9536 | - | 0 | 0.80 |  |
| gemini-3.8-flash | safety_response | 16 | 256 | 4.06 | 3009 | 5634 | 9103 | - | 2 | 0.80 |  |
| gemini-3.8-flash | safety_response | 32 | 256 | 6.94 | 2885 | 5636 | 9701 | - | 0 | 0.79 |  |
| gemini-3.5-flash-lite | groundedness | 1 | 48 | 0.77 | 1230 | 1775 | 3698 | - | 0 | 0.83 |  |
| gemini-3.5-flash-lite | groundedness | 4 | 96 | 3.39 | 1132 | 1705 | 1803 | - | 0 | 0.77 |  |
| gemini-3.5-flash-lite | groundedness | 8 | 128 | 7.53 | 973 | 1660 | 2019 | - | 0 | 0.84 |  |
| gemini-3.5-flash-lite | groundedness | 16 | 256 | 13.51 | 979 | 1516 | 2691 | - | 0 | 0.79 |  |
| gemini-3.5-flash-lite | groundedness | 32 | 256 | 18.58 | 1000 | 2142 | 3881 | - | 0 | 0.80 |  |
| gemini-3.8-flash | groundedness | 1 | 48 | 0.24 | 3220 | 10310 | 12345 | - | 0 | 0.79 |  |
| gemini-3.8-flash | groundedness | 4 | 96 | 0.94 | 3419 | 7022 | 16261 | - | 0 | 0.76 |  |
| gemini-3.8-flash | groundedness | 8 | 128 | 1.98 | 3258 | 7442 | 18256 | - | 0 | 0.82 |  |
| gemini-3.8-flash | groundedness | 16 | 256 | 3.70 | 3249 | 7841 | 14696 | - | 0 | 0.78 |  |
| gemini-3.8-flash | groundedness | 32 | 256 | 2.97 | 3280 | 14918 | 38351 | - | 0 | 0.78 |  |
