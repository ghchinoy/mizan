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
"""Summarize Experiment 07 runs (compare-engines schema v2 reports).

Reads docs/experiments/judge-eval/runs/*.json and writes summary.json and
summary.md next to them. For each suite it reports:
  - accuracy vs gold for every judge configuration, with a bootstrap 95% CI
  - Likert MAE / Spearman where the gold is numeric
  - an offline entropy cascade (J5): keep the DiffusionGemma answer when its
    normalized entropy (hesitation) is below a threshold, otherwise take the
    paired Gemini answer from the same run. Reported at fixed thresholds of
    16% and 35%, which are the Hesitation bands in dgem's Studio, not values
    tuned on this data.
  - p50 latency per engine, error counts, readout modes, and backends used.

Pure standard library.
"""

import glob
import json
import math
import os
import random
import statistics

RUNS = os.path.join(os.path.dirname(__file__), "..", "..", "docs", "experiments", "judge-eval", "runs")
THRESHOLDS = (0.16, 0.35)
SEED = 20260925


def boot_ci(xs, iters=2000):
    if not xs:
        return None
    rng = random.Random(SEED)
    n = len(xs)
    means = sorted(sum(xs[rng.randrange(n)] for _ in range(n)) / n for _ in range(iters))
    return (means[int(0.025 * iters)], means[int(0.975 * iters) - 1])


def spearman(x, y):
    if len(x) < 3:
        return None

    def rank(v):
        order = sorted(range(len(v)), key=lambda i: v[i])
        r = [0.0] * len(v)
        i = 0
        while i < len(order):
            j = i
            while j + 1 < len(order) and v[order[j + 1]] == v[order[i]]:
                j += 1
            for k in range(i, j + 1):
                r[order[k]] = (i + j) / 2 + 1
            i = j + 1
        return r

    rx, ry = rank(x), rank(y)
    mx, my = statistics.mean(rx), statistics.mean(ry)
    num = sum((a - mx) * (b - my) for a, b in zip(rx, ry))
    den = math.sqrt(sum((a - mx) ** 2 for a in rx) * sum((b - my) ** 2 for b in ry))
    return num / den if den else None


def hesitation(run):
    """Normalized entropy of a diffusion answer (entropy / ln k), or None."""
    c = run.get("custom_output") or {}
    probs = c.get("probabilities")
    h = c.get("entropy")
    if isinstance(probs, dict) and len(probs) >= 2:
        if h is None:
            h = -sum(p * math.log(p) for p in probs.values() if p > 0)
        return h / math.log(len(probs))
    per = c.get("per_criterion")
    if isinstance(per, list) and per:  # rubric: mean criterion hesitation
        hs = [x.get("entropy", 0) / math.log(2) for x in per if isinstance(x, dict)]
        return sum(hs) / len(hs) if hs else None
    return None


def engine_label(run_side, meta, side):
    eng = meta.get(f"engine_{side}")
    model = meta.get(f"model_{side}") or ""
    if eng == "diffusion":
        be = meta.get("diffusion_backend") or meta.get("diffusion_endpoint") or ""
        mirror = "+mirror" if meta.get("diffusion_mirror") else ""
        return f"dgem[{be}]{mirror}"
    if eng == "local":
        return "local"
    return f"vertex[{model or 'service-default'}]"


def load_merged(path):
    """Load a run and overlay <run>.retry*.json: a retried case replaces the
    original when the original had an engine error on either side (retries
    re-run BOTH engines on only the failed items, so the pair stays same-session
    comparable)."""
    rep = json.load(open(path))
    retries = sorted(glob.glob(path[:-5] + ".retry*.json"))
    merged_ids = []
    for rp in retries:
        rr = json.load(open(rp))
        by_id = {c["id"]: c for c in rr["cases"]}
        for i, c in enumerate(rep["cases"]):
            cmpc = c["comparison"]
            if (cmpc["engine_a"].get("error") or cmpc["engine_b"].get("error")) and c["id"] in by_id:
                rep["cases"][i] = by_id[c["id"]]
                merged_ids.append(c["id"])
    return rep, retries, merged_ids


def side_stats(cases, side):
    corr, lat, errs, conf, correct_conf, pred, gold = [], [], 0, [], [], [], []
    for c in cases:
        r = c["comparison"][f"engine_{side}"]
        if r.get("error"):
            errs += 1
            continue
        lat.append(r["duration_ms"])
        if r.get("correct") is not None:
            corr.append(1.0 if r["correct"] else 0.0)
        if r.get("score") is not None and c["comparison"]["kind"] not in ("boul",):
            try:
                g = float(c.get("expected", ""))
                pred.append(float(r["score"]))
                gold.append(g)
            except ValueError:
                pass
    st = {"scored": len(corr), "correct": int(sum(corr)), "errors": errs,
          "accuracy": (sum(corr) / len(corr)) if corr else None, "ci95": boot_ci(corr),
          "p50_ms": statistics.median(lat) if lat else 0, "p95_ms": sorted(lat)[int(0.95 * (len(lat) - 1))] if lat else 0}
    if pred:
        st["likert"] = {"n": len(pred), "mae": sum(abs(a - b) for a, b in zip(pred, gold)) / len(pred), "spearman": spearman(pred, gold)}
    return st


def summarize(path):
    rep, retries, merged = load_merged(path)
    meta = rep["meta"]
    name = os.path.basename(path)[:-5]
    suite = os.path.basename(meta["dataset"])[:-6]
    out = {"run": name, "suite": suite, "n": len(rep["cases"]), "notes": meta.get("notes", ""), "started_at": meta.get("started_at"),
           "retry_files": [os.path.basename(r) for r in retries], "retried_items": len(merged), "engines": {}}
    for side in ("a", "b"):
        s = rep[f"engine_{side}"]
        st = side_stats(rep["cases"], side)
        mirror_on = meta.get("diffusion_mirror") and meta.get("diffusion_mirror_side", "both") in ("both", "", side)
        m2 = dict(meta, diffusion_mirror=mirror_on)
        st.update({"label": engine_label(s, m2, side), "readout_modes": s.get("readout_modes"), "backends_used": s.get("backends_used"),
                   "position_consistency": s.get("pairwise_position_consistency"), "server_p50_ms": s.get("server_p50_ms")})
        out["engines"][side] = st
    # paired, recomputed on cases where both sides were scored
    oa = ob = bc = bw = agree = n = 0
    da, db = [], []
    for c in rep["cases"]:
        a, b = c["comparison"]["engine_a"], c["comparison"]["engine_b"]
        if a.get("error") or b.get("error"):
            continue
        n += 1
        agree += bool(c["comparison"].get("agreement"))
        if a.get("correct") is None or b.get("correct") is None:
            continue
        da.append(1.0 if a["correct"] else 0.0)
        db.append(1.0 if b["correct"] else 0.0)
        if a["correct"] and b["correct"]:
            bc += 1
        elif a["correct"]:
            oa += 1
        elif b["correct"]:
            ob += 1
        else:
            bw += 1
    out["paired"] = {"n": n, "agreement_pct": 100 * agree / n if n else None, "only_a_correct": oa, "only_b_correct": ob,
                     "both_correct": bc, "both_wrong": bw, "mcnemar_exact_p": mcnemar(oa, ob) if da else None,
                     "accuracy_diff_a_minus_b": (sum(da) - sum(db)) / len(da) if da else None}

    # J5 cascade when exactly one side is diffusion and the other is vertex.
    sides = {meta.get("engine_a"): "a", meta.get("engine_b"): "b"}
    if "diffusion" in sides and "vertex" in sides and len(sides) == 2:
        d, v = sides["diffusion"], sides["vertex"]
        casc = {}
        for thr in THRESHOLDS:
            xs, esc, lat = [], 0, []
            for c in rep["cases"]:
                dr, vr = c["comparison"][f"engine_{d}"], c["comparison"][f"engine_{v}"]
                if dr.get("correct") is None or vr.get("correct") is None:
                    continue
                h = hesitation(dr)
                use_v = h is None or h >= thr
                esc += use_v
                xs.append(1.0 if (vr if use_v else dr)["correct"] else 0.0)
                lat.append(dr["duration_ms"] + (vr["duration_ms"] if use_v else 0))
            if xs:
                casc[f"h{int(thr * 100)}"] = {"n": len(xs), "accuracy": sum(xs) / len(xs), "ci95": boot_ci(xs), "escalated_pct": esc / len(xs),
                                              "p50_ms": statistics.median(lat)}
        out["cascade"] = casc
    return out


def mcnemar(b, c):
    n = b + c
    if n == 0:
        return 1.0
    k = min(b, c)
    p = sum(math.comb(n, i) for i in range(k + 1)) / 2 ** n * 2
    return min(1.0, p)


def main():
    runs = sorted(glob.glob(os.path.join(RUNS, "*.json")))
    runs = [r for r in runs if not r.endswith("summary.json") and ".retry" not in r]
    rows = [summarize(r) for r in runs]
    with open(os.path.join(RUNS, "summary.json"), "w") as f:
        json.dump(rows, f, indent=1)

    def pct(x):
        return "-" if x is None else f"{100 * x:.1f}"

    def ci(c):
        return "" if not c else f" [{100 * c[0]:.0f}-{100 * c[1]:.0f}]"

    lines = ["| Run | Suite | n | Engine A | Acc A [95% CI] | p50 A | Engine B | Acc B [95% CI] | p50 B | Only A / only B right | McNemar p | Cascade h16 (esc%) | Cascade h35 (esc%) |",
             "|---|---|---|---|---|---|---|---|---|---|---|---|---|"]
    for r in rows:
        a, b, p = r["engines"]["a"], r["engines"]["b"], r["paired"]
        c16 = r.get("cascade", {}).get("h16")
        c35 = r.get("cascade", {}).get("h35")
        fmt_c = lambda c: "-" if not c else f"{pct(c['accuracy'])}{ci(c['ci95'])} ({100 * c['escalated_pct']:.0f}%)"
        mp = p.get("mcnemar_exact_p")
        rt = f" (+{r['retried_items']} retried)" if r.get("retried_items") else ""
        lines.append(f"| {r['run']}{rt} | {r['suite']} | {r['n']} | {a['label']} | {pct(a['accuracy'])}{ci(a['ci95'])}{' (err ' + str(a['errors']) + ')' if a['errors'] else ''} | {a['p50_ms']:.0f} | "
                     f"{b['label']} | {pct(b['accuracy'])}{ci(b['ci95'])}{' (err ' + str(b['errors']) + ')' if b['errors'] else ''} | {b['p50_ms']:.0f} | "
                     f"{p.get('only_a_correct')} / {p.get('only_b_correct')} | {'-' if mp is None else f'{mp:.3g}'} | {fmt_c(c16)} | {fmt_c(c35)} |")
    lik = ["", "| Run | Engine | Likert n | MAE | Spearman |", "|---|---|---|---|---|"]
    for r in rows:
        for side in ("a", "b"):
            e = r["engines"][side]
            if e.get("likert"):
                L = e["likert"]
                sp = "-" if L.get("spearman") is None else f"{L['spearman']:.3f}"
                lik.append(f"| {r['run']} | {e['label']} | {L['n']} | {L['mae']:.2f} | {sp} |")
    pos = ["", "| Run | Engine | Pairwise position consistency |", "|---|---|---|"]
    for r in rows:
        for side in ("a", "b"):
            e = r["engines"][side]
            if e.get("position_consistency") is not None:
                pos.append(f"| {r['run']} | {e['label']} | {100 * e['position_consistency']:.1f}% |")
    with open(os.path.join(RUNS, "summary.md"), "w") as f:
        f.write("\n".join(lines + lik + pos) + "\n")
    print("\n".join(lines + lik + pos))


if __name__ == "__main__":
    main()
