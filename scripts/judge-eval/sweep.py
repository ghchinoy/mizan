#!/usr/bin/env python3
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
"""Throughput sweep for judge engines (Experiment 07b).

For each target engine and each concurrency level, runs `mizan eval
compare-engines --engine-b none` on a suite cycled to N items (one engine per
run, so engines do not compete), and records items/s (batch wall clock),
latency p50/p95/p99, error rate and accuracy. A target stops climbing once its
error rate exceeds --max-error-rate. Vertex dedicated-endpoint replica counts
are captured before and after each dgem-G4 level.

  python3 scripts/judge-eval/sweep.py --out docs/experiments/judge-eval/runs/<date>-throughput \\
      --suites safety_response groundedness --levels 1 4 8 16 32

Needs MIZAN_PROJECT_ID, DGEM_VERTEX_ENDPOINT, DGEM_GATEWAY_URL; ADC.
"""

import argparse
import json
import os
import re
import subprocess
import sys
import time
import urllib.parse
import urllib.request

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SUITES = os.path.join(ROOT, "docs", "experiments", "judge-eval", "suites")


def targets():
    v, g = os.environ["DGEM_VERTEX_ENDPOINT"], os.environ["DGEM_GATEWAY_URL"]
    return {
        "dgem-g4": ["--engine-a", "diffusion", "--diffusion-endpoint", v],
        "dgem-cloudrun": ["--engine-a", "diffusion", "--diffusion-endpoint", g, "--diffusion-backend", "cloudrun"],
        "gemini-3.5-flash-lite": ["--engine-a", "vertex", "--model-a", "gemini-3.5-flash-lite"],
        "gemini-3.8-flash": ["--engine-a", "vertex", "--model-a", "gemini-3.8-flash"],
    }


def items_for(level):
    return {1: 48, 4: 96, 8: 128}.get(level, 256)


def cycled(suite, n, path):
    rows = [json.loads(l) for l in open(os.path.join(SUITES, suite + ".jsonl")) if l.strip()]
    with open(path, "w") as f:
        for i in range(n):
            r = dict(rows[i % len(rows)])
            r["id"] = f"{r['id']}#{i // len(rows)}"
            f.write(json.dumps(r) + "\n")


def access_token():
    c = json.load(open(os.environ.get("GOOGLE_APPLICATION_CREDENTIALS") or os.path.expanduser("~/.config/gcloud/application_default_credentials.json")))
    body = urllib.parse.urlencode({k: c[k] for k in ("client_id", "client_secret", "refresh_token")} | {"grant_type": "refresh_token"}).encode()
    return json.load(urllib.request.urlopen("https://oauth2.googleapis.com/token", body))["access_token"]


def replicas():
    m = re.search(r"projects/([^/]+)/locations/([^/]+)/endpoints/(\d+)", os.environ["DGEM_VERTEX_ENDPOINT"])
    if not m:
        return None
    proj, loc, ep = m.groups()
    try:
        req = urllib.request.Request(f"https://{loc}-aiplatform.googleapis.com/v1/projects/{proj}/locations/{loc}/endpoints/{ep}",
                                     headers={"Authorization": "Bearer " + access_token()})
        d = json.load(urllib.request.urlopen(req, timeout=20))
        return sum(dm.get("status", {}).get("availableReplicaCount", 0) for dm in d.get("deployedModels", []))
    except Exception:  # noqa: BLE001
        return None


def pct(xs, p):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(round(p * (len(xs) - 1))))] if xs else None


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", required=True)
    ap.add_argument("--suites", nargs="+", default=["safety_response", "groundedness"])
    ap.add_argument("--levels", nargs="+", type=int, default=[1, 4, 8, 16, 32])
    ap.add_argument("--targets", nargs="+", default=list(targets()))
    ap.add_argument("--max-error-rate", type=float, default=0.05)
    ap.add_argument("--cooldown", type=int, default=20, help="seconds between levels")
    ap.add_argument("--mizan", default=os.path.join(ROOT, "bin", "mizan"))
    a = ap.parse_args()
    os.makedirs(a.out, exist_ok=True)
    tg = targets()
    rows = []
    summ_path = os.path.join(a.out, "sweep.json")
    if os.path.exists(summ_path):
        rows = json.load(open(summ_path))
    done = {(r["target"], r["suite"], r["level"]) for r in rows}
    for suite in a.suites:
        for t in a.targets:
            for lvl in a.levels:
                if (t, suite, lvl) in done:
                    continue
                n = items_for(lvl)
                ds = os.path.join(a.out, f".{suite}.{n}.jsonl")
                cycled(suite, n, ds)
                rep = os.path.join(a.out, f"{t}__{suite}__c{lvl}.json")
                rb = replicas() if t == "dgem-g4" else None
                cmd = [a.mizan, "eval", "compare-engines", "--dataset", ds, "--engine-b", "none", "--workers", str(lvl),
                       "--warmup", "2", "--no-store", "--score-tolerance", "0.001", "--output-file", rep,
                       "--notes", f"throughput sweep {t} {suite} concurrency={lvl}"] + tg[t]
                print(f"=== {t} {suite} c={lvl} n={n} ({time.strftime('%H:%M:%S')})", flush=True)
                subprocess.run(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
                ra = replicas() if t == "dgem-g4" else None
                if not os.path.exists(rep):
                    print("   no report", flush=True)
                    break
                d = json.load(open(rep))
                runs = [c["comparison"]["engine_a"] for c in d["cases"]]
                ok = [r for r in runs if not r.get("error")]
                lat = [r["duration_ms"] for r in ok]
                srv = [r["custom_output"]["server_ms"] for r in ok if (r.get("custom_output") or {}).get("server_ms")]
                corr = [r["correct"] for r in ok if r.get("correct") is not None]
                toks = [r["tokens"] for r in ok if r.get("tokens")]
                row = {"target": t, "suite": suite, "level": lvl, "n": len(runs), "errors": len(runs) - len(ok),
                       "error_rate": (len(runs) - len(ok)) / len(runs), "wall_s": d["meta"]["batch_wall_ms"] / 1000,
                       "items_per_s": len(ok) / (d["meta"]["batch_wall_ms"] / 1000),
                       "p50_ms": pct(lat, .5), "p95_ms": pct(lat, .95), "p99_ms": pct(lat, .99),
                       "server_p50_ms": pct(srv, .5), "accuracy": sum(corr) / len(corr) if corr else None,
                       "replicas_before": rb, "replicas_after": ra,
                       "mean_prompt_tokens": sum(x["prompt_tokens"] for x in toks) / len(toks) if toks else None,
                       "mean_output_tokens": sum(x["candidates_tokens"] for x in toks) / len(toks) if toks else None,
                       "mean_thoughts_tokens": sum(x.get("thoughts_tokens", 0) for x in toks) / len(toks) if toks else None,
                       "error_samples": sorted({(r.get("error") or "")[:120] for r in runs if r.get("error")})[:3]}
                rows.append(row)
                json.dump(rows, open(summ_path, "w"), indent=1)
                os.remove(ds)
                print(f"   {row['items_per_s']:.2f} items/s  p50 {row['p50_ms']:.0f}  p95 {row['p95_ms']:.0f}  err {row['errors']}  acc {row['accuracy']}  replicas {rb}->{ra}", flush=True)
                if row["error_rate"] > a.max_error_rate:
                    print(f"   stop {t}: error rate {row['error_rate']:.1%}", flush=True)
                    break
                time.sleep(a.cooldown)
    # markdown table
    lines = ["| Target | Suite | Concurrency | n | items/s | p50 ms | p95 ms | p99 ms | server p50 | errors | accuracy | replicas |",
             "|---|---|---|---|---|---|---|---|---|---|---|---|"]
    for r in rows:
        f = lambda v, fmt="{:.0f}": "-" if v is None else fmt.format(v)
        rep = "" if r["replicas_before"] is None else f"{r['replicas_before']}→{r['replicas_after']}"
        lines.append(f"| {r['target']} | {r['suite']} | {r['level']} | {r['n']} | {r['items_per_s']:.2f} | {f(r['p50_ms'])} | {f(r['p95_ms'])} | {f(r['p99_ms'])} | "
                     f"{f(r['server_p50_ms'])} | {r['errors']} | {f(r['accuracy'], '{:.2f}')} | {rep} |")
    open(os.path.join(a.out, "sweep.md"), "w").write("\n".join(lines) + "\n")
    print("\n".join(lines))


if __name__ == "__main__":
    sys.exit(main())
