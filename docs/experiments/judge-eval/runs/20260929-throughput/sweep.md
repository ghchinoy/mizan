| Target | Suite | Concurrency | n | items/s | p50 ms | p95 ms | p99 ms | server p50 | errors | accuracy | replicas |
|---|---|---|---|---|---|---|---|---|---|---|---|
| dgem-g4 | safety_response | 1 | 48 | 10.53 | 94 | 102 | 110 | 55 | 0 | 0.96 | 1→1 |
| dgem-g4 | safety_response | 4 | 96 | 31.39 | 127 | 136 | 137 | 85 | 0 | 0.79 | 1→1 |
| dgem-g4 | safety_response | 8 | 128 | 58.58 | 138 | 149 | 151 | 87 | 0 | 0.82 | 1→1 |
| dgem-g4 | safety_response | 16 | 256 | 82.83 | 187 | 237 | 242 | 93 | 0 | 0.80 | 1→1 |
| dgem-g4 | safety_response | 32 | 256 | 83.22 | 373 | 419 | 427 | 92 | 0 | 0.80 | 1→1 |
| dgem-cloudrun | safety_response | 1 | 48 | 5.46 | 179 | 247 | 268 | 61 | 0 | 0.96 |  |
| dgem-cloudrun | safety_response | 4 | 96 | 20.09 | 197 | 221 | 229 | 92 | 0 | 0.78 |  |
| dgem-cloudrun | safety_response | 8 | 128 | 37.62 | 206 | 246 | 298 | 100 | 0 | 0.83 |  |
| dgem-cloudrun | safety_response | 16 | 256 | 66.92 | 240 | 273 | 305 | 120 | 0 | 0.81 |  |
| dgem-cloudrun | safety_response | 32 | 256 | 77.60 | 397 | 505 | 600 | 99 | 0 | 0.81 |  |
| dgem-g4 | groundedness | 1 | 48 | 9.63 | 100 | 126 | 138 | 59 | 0 | 0.79 | 1→1 |
| dgem-g4 | groundedness | 4 | 96 | 26.82 | 139 | 208 | 248 | 91 | 0 | 0.82 | 1→1 |
| dgem-g4 | groundedness | 8 | 128 | 61.91 | 125 | 170 | 174 | 83 | 0 | 0.84 | 1→1 |
| dgem-g4 | groundedness | 16 | 256 | 68.34 | 229 | 243 | 314 | 114 | 0 | 0.83 | 1→1 |
| dgem-g4 | groundedness | 32 | 256 | 67.32 | 469 | 513 | 521 | 115 | 0 | 0.83 | 1→1 |
| dgem-cloudrun | groundedness | 1 | 48 | 5.55 | 176 | 208 | 217 | 63 | 0 | 0.79 |  |
| dgem-cloudrun | groundedness | 4 | 96 | 17.36 | 208 | 304 | 357 | 102 | 0 | 0.82 |  |
| dgem-cloudrun | groundedness | 8 | 128 | 28.86 | 247 | 433 | 484 | 137 | 0 | 0.85 |  |
| dgem-cloudrun | groundedness | 16 | 256 | 39.45 | 350 | 650 | 671 | 159 | 0 | 0.80 |  |
| dgem-cloudrun | groundedness | 32 | 256 | 38.30 | 763 | 1075 | 1413 | 171 | 0 | 0.84 |  |
