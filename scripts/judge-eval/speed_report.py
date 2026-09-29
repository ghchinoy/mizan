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
"""Experiment 07b report: throughput, live cascade, thinking budget, cost.

  python3 scripts/judge-eval/speed_report.py \\
      --throughput docs/experiments/judge-eval/runs/20260929-throughput \\
      --speed docs/experiments/judge-eval/runs/20260929-speed \\
      --baseline docs/experiments/judge-eval/runs/20260929-v010 > speed.md

Prices are list prices (USD, us-central1 / global), read from the Cloud
Billing Catalog and the Vertex pricing pages on 2026-09-29; see PRICES.
"""

import argparse
import glob
import json
import math
import os
import statistics
import sys

sys.path.insert(0, os.path.dirname(__file__))
from analyze import boot_ci, load_merged, mcnemar  # noqa: E402

# --- prices (2026-09-29) -------------------------------------------------------
PRICES = {
    # Gemini, standard tier, global, per 1M tokens (output includes reasoning tokens).
    "gemini-3.8-flash": {"in": 0.75, "out": 3.75, "note": "introductory price through 2026-12-31; $1.50 / $7.50 from 2027-01-01"},
    "gemini-3.8-flash@2027": {"in": 1.50, "out": 7.50, "note": "list price from 2027-01-01"},
    "gemini-3.5-flash-lite": {"in": 0.30, "out": 2.50, "note": ""},
    # Vertex AI online prediction, g4-standard-48 (48 vCPU, 180 GiB) + 1x RTX PRO 6000, Iowa, incl. management fees.
    "g4_hour": 48 * (0.05624995 + 0.00733695) + 180 * (0.0067505 + 0.0008805) + (1.26000003 + 0.16434783),
    # Cloud Run instance-based: RTX PRO 6000 (no zonal redundancy) + 20 vCPU + 80 GiB, us-central1.
    "cloudrun_hour": 3600 * (0.00036522 + 20 * 0.000018 + 80 * 0.000002),
    # Gen AI evaluation service, legacy model-based (predefined) metrics, per 1k characters.
    "prebuilt_in_1k_chars": 0.005,
    "prebuilt_out_1k_chars": 0.015,
}


def pct(xs, p):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(round(p * (len(xs) - 1))))] if xs else None


def gemini_cost_per_1k(model, prompt_toks, out_toks, thought_toks):
    p = PRICES[model]
    return 1000 * (prompt_toks * p["in"] + (out_toks + thought_toks) * p["out"]) / 1e6


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--throughput", required=True)
    ap.add_argument("--speed", required=True)
    ap.add_argument("--baseline", required=True)
    ap.add_argument("--prebuilt-chars", default="", help="json {suite: {in_chars, out_chars}} measured sample")
    a = ap.parse_args()
    out = []
    P = out.append

    # --- throughput --------------------------------------------------------
    sweep = []
    for f in [os.path.join(a.throughput, "sweep.json"), os.path.join(a.throughput, "gemini", "sweep.json")]:
        if os.path.exists(f):
            sweep += json.load(open(f))
    P("### Throughput sweep (one engine per run; items/s from batch wall clock)\n")
    P("| Target | Suite | c=1 | c=4 | c=8 | c=16 | c=32 | p50 / p95 at c=1 (ms) | p50 / p95 at c=32 (ms) | errors |")
    P("|---|---|---|---|---|---|---|---|---|---|")
    peak = {}
    tok = {}
    for t in ["dgem-g4", "dgem-cloudrun", "gemini-3.5-flash-lite", "gemini-3.8-flash"]:
        for s in ["safety_response", "groundedness"]:
            rs = {r["level"]: r for r in sweep if r["target"] == t and r["suite"] == s}
            if not rs:
                continue
            cells = [f"{rs[l]['items_per_s']:.1f}" if l in rs else "-" for l in (1, 4, 8, 16, 32)]
            lo, hi = rs.get(1), rs.get(32)
            errs = sum(r["errors"] for r in rs.values())
            n = sum(r["n"] for r in rs.values())
            P(f"| {t} | {s} | " + " | ".join(cells) +
              f" | {lo['p50_ms']:.0f} / {lo['p95_ms']:.0f} | " + (f"{hi['p50_ms']:.0f} / {hi['p95_ms']:.0f}" if hi else "-") + f" | {errs}/{n} |")
            peak[(t, s)] = max(r["items_per_s"] for r in rs.values())
            tk = [r for r in rs.values() if r.get("mean_prompt_tokens")]
            if tk:
                tok[(t, s)] = tuple(statistics.mean(r[k] for r in tk) for k in ("mean_prompt_tokens", "mean_output_tokens", "mean_thoughts_tokens"))
    sus = os.path.join(a.throughput, "sustained_g4_c32.json")
    if os.path.exists(sus):
        d = json.load(open(sus))
        P(f"\nSustained G4 load at 32 concurrent requests: {d['n']:,} requests over {d['wall_s']:.0f} s, **{d['items_per_s']:.1f} items/s**, "
          f"p50 {d['p50_ms']:.0f} ms, p95 {d['p95_ms']:.0f} ms, p99 {d['p99_ms']:.0f} ms, {d['errors']} errors. "
          "The endpoint stayed at 1 replica for the whole run (polled every 20 s).")

    # --- live cascade ----------------------------------------------------------
    P("\n### Live cascade (dgem G4 -> gemini-3.8-flash at hesitation >= 0.35)\n")
    P("| Suite | Mode | Accuracy [95% CI] | Escalated | p50 | p95 | p99 | Simulated (09-29) accuracy / escalated | Gemini alone (09-29) |")
    P("|---|---|---|---|---|---|---|---|---|")
    base = {r["run"]: r for r in json.load(open(os.path.join(a.baseline, "summary.json")))}
    casc_esc = {}
    for s in ["safety_response", "groundedness", "toxicity_prompt", "pairwise_rewardbench"]:
        for mode in ("serial", "w8"):
            f = os.path.join(a.speed, f"c_{mode}_{s}.json")
            if not os.path.exists(f):
                continue
            rep, _, _ = load_merged(f)
            runs = [c["comparison"]["engine_a"] for c in rep["cases"]]
            ok = [r for r in runs if not r.get("error")]
            corr = [1.0 if r["correct"] else 0.0 for r in ok if r.get("correct") is not None]
            esc = [r["custom_output"].get("cascade_escalated") for r in ok]
            lat = [r["duration_ms"] for r in ok]
            b = base.get(f"j4_vs_j1_{s}", {})
            sim = b.get("cascade", {}).get("h35")
            gem = b.get("engines", {}).get("a", {}).get("accuracy")
            ci = boot_ci(corr)
            casc_esc[s] = sum(esc) / len(esc)
            P(f"| {s} | {'serial' if mode == 'serial' else '8 workers'} | {100 * sum(corr) / len(corr):.1f} [{100 * ci[0]:.0f}-{100 * ci[1]:.0f}] | "
              f"{100 * sum(esc) / len(esc):.0f}% | {pct(lat, .5):.0f} | {pct(lat, .95):.0f} | {pct(lat, .99):.0f} | "
              + (f"{100 * sim['accuracy']:.0f} / {100 * sim['escalated_pct']:.0f}%" if sim else "-") + " | "
              + (f"{100 * gem:.1f}" if gem is not None else "-") + " |")

    # --- thinking budget ---------------------------------------------------------
    P("\n### Gemini thinking budget: model default (A) vs budget 0 (B), same session, same items\n")
    P("| Model | Suite | Acc default | Acc budget 0 | only default right / only budget-0 right | McNemar p | p50 default / budget 0 (ms) | mean thinking tokens default / budget 0 |")
    P("|---|---|---|---|---|---|---|---|")
    think_tok = {}
    for f in sorted(glob.glob(os.path.join(a.speed, "t_*.json"))):
        if ".retry" in f:
            continue
        rep, _, _ = load_merged(f)
        model = rep["meta"]["model_a"]
        suite = os.path.basename(rep["meta"]["dataset"])[:-6]
        A = [c["comparison"]["engine_a"] for c in rep["cases"]]
        B = [c["comparison"]["engine_b"] for c in rep["cases"]]
        pa = [(x, y) for x, y in zip(A, B) if x.get("correct") is not None and y.get("correct") is not None]
        oa = sum(1 for x, y in pa if x["correct"] and not y["correct"])
        ob = sum(1 for x, y in pa if y["correct"] and not x["correct"])
        acc = lambda rs: 100 * sum(1 for r in rs if r.get("correct")) / max(1, sum(1 for r in rs if r.get("correct") is not None))
        lat = lambda rs: pct([r["duration_ms"] for r in rs if not r.get("error")], .5)
        th = lambda rs: statistics.mean([(r.get("tokens") or {}).get("thoughts_tokens", 0) for r in rs if r.get("tokens")] or [0])
        for side, rs in (("default", A), ("zero", B)):
            tk = [r["tokens"] for r in rs if r.get("tokens")]
            if tk:
                think_tok[(model, suite, side)] = (statistics.mean(x["prompt_tokens"] for x in tk), statistics.mean(x["candidates_tokens"] for x in tk),
                                                   statistics.mean(x.get("thoughts_tokens", 0) for x in tk))
        P(f"| {model} | {suite} | {acc(A):.1f} | {acc(B):.1f} | {oa} / {ob} | {mcnemar(oa, ob):.3g} | {lat(A):.0f} / {lat(B):.0f} | {th(A):.0f} / {th(B):.0f} |")

    # --- cost --------------------------------------------------------------------
    P("\n### Cost per 1,000 judgements (USD, list prices 2026-09-29)\n")
    P(f"- Vertex G4 dedicated endpoint (g4-standard-48 + RTX PRO 6000, incl. management fees): **${PRICES['g4_hour']:.2f}/h per replica**, billed while deployed.")
    P(f"- Cloud Run `dgemma` (RTX PRO 6000 without zonal redundancy, 20 vCPU, 80 GiB, instance-based): **${PRICES['cloudrun_hour']:.2f}/h while an instance runs**; scales to zero.")
    P("- Gemini: standard-tier token prices (3.8-flash $0.75 in / $3.75 out per 1M through 2026-12-31, $1.50 / $7.50 from 2027; 3.5-flash-lite $0.30 / $2.50); thinking tokens bill as output.")
    P("- Vertex predefined metrics: $0.005 per 1k input characters + $0.015 per 1k output characters.\n")
    P("| Judge | Suite | Basis | USD per 1k |")
    P("|---|---|---|---|")
    for s in ["safety_response", "groundedness"]:
        for t, hour in (("dgem-g4", PRICES["g4_hour"]), ("dgem-cloudrun", PRICES["cloudrun_hour"])):
            if (t, s) in peak:
                P(f"| {t} | {s} | at peak throughput {peak[(t, s)]:.0f}/s (fully utilized) | **{1000 * hour / (3600 * peak[(t, s)]):.4f}** |")
        for m in ("gemini-3.5-flash-lite", "gemini-3.8-flash"):
            if (m, s) in tok:
                pt, ot, tt = tok[(m, s)]
                P(f"| {m} | {s} | measured {pt:.0f} in / {ot:.0f} out / {tt:.0f} thinking tokens | **{gemini_cost_per_1k(m, pt, ot, tt):.3f}** |")
                if m == "gemini-3.8-flash":
                    p2 = PRICES["gemini-3.8-flash@2027"]
                    P(f"| {m} (2027 price) | {s} | same tokens | {1000 * (pt * p2['in'] + (ot + tt) * p2['out']) / 1e6:.3f} |")
        k = ("gemini-3.8-flash", s, "zero")
        if k in think_tok:
            pt, ot, tt = think_tok[k]
            P(f"| gemini-3.8-flash, thinking budget 0 | {s} | measured {pt:.0f} in / {ot:.0f} out / {tt:.0f} thinking | {gemini_cost_per_1k('gemini-3.8-flash', pt, ot, tt):.3f} |")
        if s in casc_esc and ("dgem-g4", s) in peak and ("gemini-3.8-flash", s) in tok:
            dg = 1000 * PRICES["g4_hour"] / (3600 * peak[("dgem-g4", s)])
            gm = gemini_cost_per_1k("gemini-3.8-flash", *tok[("gemini-3.8-flash", s)])
            P(f"| cascade dgem G4 -> 3.8-flash | {s} | dgem on every item + Gemini on {100 * casc_esc[s]:.0f}% | {dg + casc_esc[s] * gm:.3f} |")
    if a.prebuilt_chars and os.path.exists(a.prebuilt_chars):
        for s, v in json.load(open(a.prebuilt_chars)).items():
            c = v["in_chars"] * PRICES["prebuilt_in_1k_chars"] + v["out_chars"] * PRICES["prebuilt_out_1k_chars"]
            P(f"| Vertex predefined `{s}` | {s} | ≥{v['in_chars']} input chars (fields only; service template not counted) + {v['out_chars']} output chars | ≥{c:.2f} |")
    P(f"\nIdle floor: one always-on G4 replica costs ${24 * PRICES['g4_hour']:.0f}/day whether or not it serves traffic. "
      f"At that cost, dgem on G4 is cheaper than gemini-3.8-flash per judgement once it serves more than roughly "
      f"{24 * PRICES['g4_hour'] / (gemini_cost_per_1k('gemini-3.8-flash', *tok[('gemini-3.8-flash', 'safety_response')]) / 1000):,.0f} safety judgements per day."
      if ("gemini-3.8-flash", "safety_response") in tok else "")
    print("\n".join(out))


if __name__ == "__main__":
    main()
