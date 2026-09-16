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
"""Render Mizan eval results into a single self-contained HTML report.

This is the bundled renderer for the `report-to-html` agent skill. It consumes
the JSON array that `mizan results list -o json` prints (an array of
results.Result objects; a single object from `mizan results show -o json` is
also accepted), aggregates client-side (mean/min/max score, pass-rate at a
threshold, per-Template.ID grouping, score distribution, per-metric trend over
RunAt), and fills assets/report.template.html to emit ONE offline report.html
with inline CSS/JS and embedded JSON.

It re-implements NO CLI logic: querying and filtering are the CLI's job (the
`mizan results list` flags); this script only performs the terminal HTML
transform the CLI does not do. It makes no network request and needs no
credentials. Paths come from argv (argparse), never string-interpolated.
"""

import argparse
import datetime
import json
import math
import os
import sys

PLACEHOLDER = "__MIZAN_REPORT_DATA__"


def _template_path() -> str:
    """Locate report.template.html relative to this script (../assets)."""
    here = os.path.dirname(os.path.abspath(__file__))
    return os.path.join(here, "..", "assets", "report.template.html")


def _load_results(input_path):
    """Read the results JSON from a file path or stdin. Accepts an array of
    results.Result, or a single object (wrapped into a one-element list)."""
    if input_path in (None, "", "-"):
        raw = sys.stdin.read()
    else:
        with open(input_path, "r", encoding="utf-8") as f:
            raw = f.read()
    raw = raw.strip()
    if not raw:
        return []
    data = json.loads(raw)
    if isinstance(data, dict):
        return [data]
    if isinstance(data, list):
        return data
    raise ValueError("expected a JSON array of results or a single result object")


def _score(rec):
    """Return the numeric Outcome.Score as a float, or None."""
    out = rec.get("Outcome") or {}
    s = out.get("Score")
    if s is None:
        return None
    try:
        return float(s)
    except (TypeError, ValueError):
        return None


def _round(x, nd=6):
    if x is None:
        return None
    return round(float(x), nd)


def _stats(scores):
    """count/mean/min/max/pass over a list of numeric scores."""
    if not scores:
        return {"scored_count": 0, "mean": None, "min": None, "max": None}
    return {
        "scored_count": len(scores),
        "mean": _round(sum(scores) / len(scores)),
        "min": _round(min(scores)),
        "max": _round(max(scores)),
    }


def _distribution(scores):
    """A histogram of scores. Integer-valued scores over a small range get one
    bin per integer; otherwise 10 equal-width bins."""
    if not scores:
        return {"bins": []}
    lo, hi = min(scores), max(scores)
    all_int = all(abs(s - round(s)) < 1e-9 for s in scores)
    if all_int and (hi - lo) <= 12:
        lo_i, hi_i = int(round(lo)), int(round(hi))
        bins = []
        for v in range(lo_i, hi_i + 1):
            c = sum(1 for s in scores if int(round(s)) == v)
            bins.append({"label": str(v), "lo": v, "hi": v, "count": c})
        return {"bins": bins}
    if hi == lo:
        return {"bins": [{"label": _fmt(lo), "lo": lo, "hi": hi, "count": len(scores)}]}
    nbins = 10
    width = (hi - lo) / nbins
    bins = []
    for i in range(nbins):
        b_lo = lo + i * width
        b_hi = lo + (i + 1) * width
        if i < nbins - 1:
            c = sum(1 for s in scores if b_lo <= s < b_hi)
        else:  # last bin is inclusive of the max
            c = sum(1 for s in scores if b_lo <= s <= b_hi)
        bins.append({"label": _fmt(b_lo) + "–" + _fmt(b_hi), "lo": _round(b_lo),
                     "hi": _round(b_hi), "count": c})
    return {"bins": bins}


def _fmt(x):
    s = ("%.2f" % float(x)).rstrip("0").rstrip(".")
    return s if s not in ("", "-0") else "0"


def _pass_rate(scores, threshold):
    if not scores:
        return None, 0
    passed = sum(1 for s in scores if s >= threshold)
    return _round(passed / len(scores)), passed


def build_model(results, threshold, title=None, filters_note=None):
    """Aggregate results into the report model the template renders."""
    all_scores = []
    pairwise_count = 0
    by_metric = {}
    metric_order = []

    for rec in results:
        out = rec.get("Outcome") or {}
        tmpl = rec.get("Template") or {}
        mid = tmpl.get("ID") or "(unknown)"
        if mid not in by_metric:
            by_metric[mid] = {"scores": [], "points": [], "count": 0}
            metric_order.append(mid)
        g = by_metric[mid]
        g["count"] += 1
        sc = _score(rec)
        if sc is not None:
            all_scores.append(sc)
            g["scores"].append(sc)
            ra = rec.get("RunAt")
            if ra:
                g["points"].append({"t": ra, "run_id": rec.get("RunID"), "score": sc})
        elif out.get("PairwiseChoice"):
            pairwise_count += 1

    st = _stats(all_scores)
    prate, pcount = _pass_rate(all_scores, threshold)
    summary = {
        "count": len(results),
        "scored_count": st["scored_count"],
        "pairwise_count": pairwise_count,
        "mean": st["mean"],
        "min": st["min"],
        "max": st["max"],
        "pass_rate": prate,
        "pass_count": pcount,
        "threshold": _round(threshold),
    }

    per_metric = []
    for mid in metric_order:
        g = by_metric[mid]
        mst = _stats(g["scores"])
        mprate, _ = _pass_rate(g["scores"], threshold)
        per_metric.append({
            "id": mid, "count": g["count"], "scored_count": mst["scored_count"],
            "mean": mst["mean"], "min": mst["min"], "max": mst["max"],
            "pass_rate": mprate,
        })

    trend = []
    for mid in metric_order:
        pts = sorted(by_metric[mid]["points"], key=lambda p: p["t"])
        if pts:
            trend.append({"id": mid, "points": pts})

    rows = []
    for rec in results:
        out = rec.get("Outcome") or {}
        tmpl = rec.get("Template") or {}
        auto = rec.get("Autorater") or {}
        sc = _score(rec)
        passed = None if sc is None else (sc >= threshold)
        dur_ns = out.get("DurationNS")
        dur_ms = None if dur_ns in (None, 0) else _round(dur_ns / 1e6, 1)
        tu = out.get("TokenUsage")
        tokens = None
        if tu:
            tokens = "prompt=%s candidates=%s total=%s" % (
                tu.get("PromptTokens"), tu.get("CandidatesTokens"), tu.get("TotalTokens"))
        rows.append({
            "run_id": rec.get("RunID") or "",
            "run_at": rec.get("RunAt") or "",
            "metric": (tmpl.get("ID") or "") + ("@" + tmpl.get("Version") if tmpl.get("Version") else ""),
            "score": _round(sc) if sc is not None else None,
            "passed": passed,
            "choice": out.get("PairwiseChoice") or "",
            "model": auto.get("Model") or "",
            "explanation": out.get("Explanation") or "",
            "warnings": out.get("Warnings") or [],
            "duration_ms": dur_ms,
            "tokens": tokens,
            "custom_output": out.get("CustomOutput") if out.get("RubricDetail") else None,
        })

    return {
        "title": title or "Mizan Results Report",
        "generated_at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d %H:%M UTC"),
        "filters_note": filters_note,
        "summary": summary,
        "per_metric": per_metric,
        "distribution": _distribution(all_scores),
        "trend": trend,
        "results": rows,
    }


def _embed(model):
    """Serialize the model for safe embedding inside a <script> block."""
    js = json.dumps(model, ensure_ascii=False)
    return (js.replace("<", "\\u003c").replace(">", "\\u003e")
              .replace("&", "\\u0026").replace("\u2028", "\\u2028")
              .replace("\u2029", "\\u2029"))


def render(model, template_path):
    with open(template_path, "r", encoding="utf-8") as f:
        template = f.read()
    if PLACEHOLDER not in template:
        raise ValueError("template is missing the %s placeholder" % PLACEHOLDER)
    return template.replace(PLACEHOLDER, _embed(model))


def main(argv=None):
    p = argparse.ArgumentParser(
        description="Render Mizan `results list -o json` output into a single self-contained HTML report.")
    p.add_argument("-i", "--input", default=None,
                   help="path to the results JSON array (from `mizan results list -o json`); reads stdin if omitted or '-'")
    p.add_argument("-o", "--output", default="report.html",
                   help="output HTML file path (default: report.html)")
    p.add_argument("-t", "--threshold", type=float, default=3.0,
                   help="pass/fail score threshold; a result passes when Score >= threshold (default: 3)")
    p.add_argument("--title", default=None, help="optional report heading")
    p.add_argument("--filters-note", default=None,
                   help="optional note describing the filters applied to `results list` (e.g. \"namespace=brand since=2026-08-01\"); recorded in the report meta line")
    p.add_argument("--template", default=None,
                   help="override the HTML template path (default: bundled assets/report.template.html)")
    args = p.parse_args(argv)

    try:
        results = _load_results(args.input)
    except (OSError, ValueError, json.JSONDecodeError) as e:
        print("error reading results JSON: %s" % e, file=sys.stderr)
        return 1

    model = build_model(results, args.threshold, title=args.title, filters_note=args.filters_note)
    template_path = args.template or _template_path()
    try:
        html = render(model, template_path)
    except (OSError, ValueError) as e:
        print("error rendering report: %s" % e, file=sys.stderr)
        return 1

    with open(args.output, "w", encoding="utf-8") as f:
        f.write(html)

    n = model["summary"]["count"]
    print("wrote %s (%d result%s)" % (args.output, n, "" if n == 1 else "s"), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
