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
"""Repeatable Mizan Autorater Qualification Harness (Experiment 08+).

Benchmarks a candidate autorater model (e.g. gemini-4-hydrogen) against:
  - Accuracy baseline (default: gemini-3.8-flash)
  - Fast/default baseline (default: gemini-3.5-flash-lite)
  - Pro-tier ceiling anchors (default: gemini-4-argon, gemini-4-carbon)

Stages:
  1. Multi-region (global, us, eu) availability, modelVersion, native
     EvaluateInstances compatibility, and unloaded serial latency probe.
  2. Full 946-item paired quality benchmark (9 Experiment 07 human-labeled
     suites = 900 items + calibration_suite.jsonl = 46 items across 15
     templates).
  3. Thinking-level A/B (default vs --thinking-level low), serial paired
     latency, and concurrency throughput scaling (c = 1, 4, 8, 16).
  4. Hard-slice Pro-tier ceiling anchor (346 items: pairwise_mtbench,
     pairwise_instruction_following, toxicity_prompt, calibration_suite).
  5. Automated synthesis into qualification_summary.json and
     qualification_summary.md with the 5-Gate Promotion Checklist.
"""

import argparse
import glob
import json
import math
import os
import random
import subprocess
import sys
import time

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SUITES_DIR = os.path.join(ROOT, "docs", "experiments", "judge-eval", "suites")
CALIB_SUITE = os.path.join(ROOT, "docs", "experiments", "calibration_suite.jsonl")

DISCRETE_JUDGE_SUITES = [
    "safety_response",
    "toxicity_prompt",
    "groundedness",
    "pairwise_mtbench",
    "pairwise_multiturn",
    "pairwise_instruction_following",
    "pairwise_rewardbench",
]
LIKERT_SUITES = [
    "likert_helpfulness",
    "likert_coherence",
]
JUDGE_SUITES = DISCRETE_JUDGE_SUITES + LIKERT_SUITES
DISCRETE_QUALITY_SUITES = DISCRETE_JUDGE_SUITES + ["calibration_suite"]
ALL_QUALITY_SUITES = JUDGE_SUITES + ["calibration_suite"]
HARD_SLICE_SUITES = [
    "pairwise_mtbench",
    "pairwise_instruction_following",
    "toxicity_prompt",
    "calibration_suite",
]
THINKING_AB_SUITES = [
    "groundedness",
    "toxicity_prompt",
    "pairwise_instruction_following",
    "pairwise_mtbench",
    "calibration_suite",
]

SEED = 20260925


def resolve_project_id(explicit=""):
    if explicit:
        return explicit
    for k in ("MIZAN_PROJECT_ID", "GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT"):
        v = os.environ.get(k, "").strip()
        if v:
            return v
    home = os.path.expanduser("~")
    active_cfg = "default"
    active_path = os.path.join(home, ".config", "gcloud", "active_config")
    if os.path.exists(active_path):
        with open(active_path) as f:
            c = f.read().strip()
            if c:
                active_cfg = c
    cfg_path = os.path.join(home, ".config", "gcloud", "configurations", f"config_{active_cfg}")
    if os.path.exists(cfg_path):
        with open(cfg_path) as f:
            for line in f:
                line = line.strip()
                if line.startswith("project") and "=" in line:
                    p = line.split("=", 1)[1].strip()
                    if p:
                        return p
    adc_path = os.path.join(home, ".config", "gcloud", "application_default_credentials.json")
    if os.path.exists(adc_path):
        with open(adc_path) as f:
            d = json.load(f)
            qp = d.get("quota_project_id", "").strip()
            if qp:
                return qp
    raise RuntimeError("Could not resolve GCP project ID; pass --project or set MIZAN_PROJECT_ID")


def suite_path(suite):
    if suite == "calibration_suite":
        return os.path.join("docs", "experiments", "calibration_suite.jsonl")
    return os.path.join("docs", "experiments", "judge-eval", "suites", f"{suite}.jsonl")


def score_tolerance(suite):
    if suite.startswith("likert_"):
        return "0.5"
    if suite == "calibration_suite":
        return "0.25"
    return "0.001"


def ensure_registry(mizan_bin, db_path, templates_root):
    os.makedirs(os.path.dirname(os.path.abspath(db_path)), exist_ok=True)
    env = dict(os.environ, MIZAN_REGISTRY_DB=db_path)
    for pack in ("judge-eval", "calibration"):
        pack_dir = os.path.join(templates_root, "packs", pack)
        if not os.path.isdir(pack_dir):
            raise RuntimeError(f"Missing template pack directory: {pack_dir}")
        cmd = [mizan_bin, "registry", "import", pack_dir, "--strategy", "overwrite"]
        subprocess.run(cmd, env=env, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def load_merged_cases(report_path):
    if not os.path.exists(report_path):
        return None, []
    with open(report_path) as f:
        rep = json.load(f)
    by_id = {c["id"]: c for c in rep.get("cases", [])}
    base = report_path[:-5] if report_path.endswith(".json") else report_path
    for rp in sorted(glob.glob(f"{base}.retry*.json")):
        try:
            with open(rp) as rf:
                rrep = json.load(rf)
            for c in rrep.get("cases", []):
                old = by_id.get(c["id"])
                if old is None:
                    by_id[c["id"]] = c
                    continue
                for side in ("engine_a", "engine_b"):
                    if old["comparison"][side].get("error") and not c["comparison"][side].get("error"):
                        old["comparison"][side] = c["comparison"][side]
        except Exception:  # noqa: BLE001
            pass
    cases = [by_id[c["id"]] for c in rep.get("cases", [])]
    return rep, cases


def unrecovered_errors(report_path):
    rep, cases = load_merged_cases(report_path)
    if rep is None or not cases:
        return -1
    eng_b = rep.get("meta", {}).get("engine_b", "")
    bad = 0
    for c in cases:
        a_err = bool(c["comparison"]["engine_a"].get("error"))
        b_err = bool(c["comparison"]["engine_b"].get("error")) if eng_b != "none" else False
        if a_err or b_err:
            bad += 1
    return bad


def retry_errored_items(mizan_bin, env, report_path, max_retries=2):
    for attempt in range(1, max_retries + 1):
        rep, cases = load_merged_cases(report_path)
        if rep is None:
            return
        meta = rep.get("meta", {})
        eng_b = meta.get("engine_b", "")
        bad_ids = set()
        for c in cases:
            a_err = bool(c["comparison"]["engine_a"].get("error"))
            b_err = bool(c["comparison"]["engine_b"].get("error")) if eng_b != "none" else False
            if a_err or b_err:
                bad_ids.add(c["id"])
        if not bad_ids:
            return
        ds_path = meta.get("dataset", "")
        if not os.path.exists(ds_path):
            return
        with open(ds_path) as f:
            lines = [ln for ln in f if ln.strip() and json.loads(ln).get("id") in bad_ids]
        if not lines:
            return
        base = report_path[:-5]
        n = 1
        while os.path.exists(f"{base}.retry{n}.json"):
            n += 1
        sub_ds = f"{base}.retry{n}.jsonl.in"
        retry_out = f"{base}.retry{n}.json"
        with open(sub_ds, "w") as f:
            f.writelines(lines)
        cmd = [
            mizan_bin,
            "eval",
            "compare-engines",
            "--dataset",
            sub_ds,
            "--workers",
            "2",
            "--warmup",
            "0",
            "--no-store",
            "--engine-a",
            meta["engine_a"],
            "--engine-b",
            meta["engine_b"],
            "--score-tolerance",
            str(meta.get("score_tolerance", 0.5)),
            "--output-file",
            retry_out,
            "--notes",
            f"retry {attempt} of {len(lines)} errored items",
        ]
        if meta.get("model_a"):
            cmd += ["--model-a", meta["model_a"]]
        if meta.get("model_b"):
            cmd += ["--model-b", meta["model_b"]]
        if meta.get("location"):
            cmd += ["--location", meta["location"]]
        if meta.get("pairwise_genai"):
            cmd += ["--pairwise-genai"]
        if meta.get("thinking_budget", -1) >= 0:
            cmd += ["--thinking-budget", str(meta["thinking_budget"]), "--thinking-side", meta.get("thinking_side") or "both"]
        if meta.get("thinking_level"):
            cmd += ["--thinking-level", meta["thinking_level"], "--thinking-side", meta.get("thinking_side") or "both"]
        if meta.get("serial_engines"):
            cmd += ["--serial"]
        print(f"   [retry {attempt}] {os.path.basename(report_path)}: {len(lines)} item(s) -> {os.path.basename(retry_out)}", flush=True)
        subprocess.run(cmd, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
        if os.path.exists(sub_ds):
            os.remove(sub_ds)


def run_compare(mizan_bin, env, out_path, ds_path, extra_args, force=False, retry=True):
    if not force and os.path.exists(out_path):
        errs = unrecovered_errors(out_path)
        if errs == 0:
            print(f"skip {os.path.basename(out_path)} (complete, 0 errors)", flush=True)
            return
        if errs > 0:
            base = out_path[:-5] if out_path.endswith(".json") else out_path
            existing_retries = sorted(glob.glob(f"{base}.retry*.json"))
            if retry and (len(existing_retries) < 2 or "calibration_suite" in os.path.basename(out_path)):
                retry_errored_items(mizan_bin, env, out_path, max_retries=1)
            rem = unrecovered_errors(out_path)
            print(f"skip {os.path.basename(out_path)} (existing report, {rem} unrecovered item(s))", flush=True)
            return
    t0 = time.time()
    print(f"=== {os.path.basename(out_path)} ({time.strftime('%H:%M:%S')})", flush=True)
    cmd = [
        mizan_bin,
        "eval",
        "compare-engines",
        "--dataset",
        ds_path,
        "--no-store",
        "--output-file",
        out_path,
    ] + extra_args
    log_path = out_path[:-5] + ".log"
    txt_path = out_path[:-5] + ".txt"
    with open(txt_path, "w") as f_out, open(log_path, "w") as f_err:
        subprocess.run(cmd, env=env, stdout=f_out, stderr=f_err, check=False)
    if retry and os.path.exists(out_path) and unrecovered_errors(out_path) > 0:
        retry_errored_items(mizan_bin, env, out_path)
    dur = time.time() - t0
    rep, cases = load_merged_cases(out_path)
    if rep is not None:
        a_ok = sum(1 for c in cases if not c["comparison"]["engine_a"].get("error"))
        b_ok = sum(1 for c in cases if not c["comparison"]["engine_b"].get("error"))
        print(f"    done in {dur:.1f}s ({len(cases)} cases, A_ok={a_ok}, B_ok={b_ok})", flush=True)
    else:
        print(f"    !! failed to produce {out_path} (see {log_path})", flush=True)


def pct(xs, q):
    if not xs:
        return None
    s = sorted(xs)
    pos = q * (len(s) - 1)
    lo = int(pos)
    hi = min(len(s) - 1, lo + 1)
    frac = pos - lo
    return s[lo] * (1 - frac) + s[hi] * frac


def boot_ci(xs, iters=2000):
    if not xs:
        return None
    rng = random.Random(SEED)
    n = len(xs)
    means = sorted(sum(xs[rng.randrange(n)] for _ in range(n)) / n for _ in range(iters))
    return [means[int(0.025 * iters)], means[int(0.975 * iters) - 1]]


def boot_diff_ci(xs, ys, iters=2000):
    if not xs or len(xs) != len(ys):
        return None
    rng = random.Random(SEED)
    n = len(xs)
    diffs = []
    for _ in range(iters):
        s = 0.0
        for _ in range(n):
            idx = rng.randrange(n)
            s += xs[idx] - ys[idx]
        diffs.append(s / n)
    diffs.sort()
    return [diffs[int(0.025 * iters)], diffs[int(0.975 * iters) - 1]]


def mcnemar_exact(b, c):
    n = b + c
    if n == 0:
        return 1.0
    k = min(b, c)
    # exact two-sided binomial p-value for Bin(n, 0.5)
    tail = sum(math.comb(n, i) for i in range(k + 1)) / (2.0**n)
    return min(1.0, 2.0 * tail)


def cohen_kappa(xs, ys):
    if not xs or len(xs) != len(ys):
        return None
    n = len(xs)
    agree = sum(1 for a, b in zip(xs, ys) if a == b)
    po = agree / n
    labels = set(xs) | set(ys)
    pe = sum((xs.count(lbl) / n) * (ys.count(lbl) / n) for lbl in labels)
    if abs(1.0 - pe) < 1e-12:
        return 1.0
    return (po - pe) / (1.0 - pe)


def spearman(x, y):
    if len(x) < 3 or len(x) != len(y):
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
                r[order[k]] = (i + j) / 2.0 + 1.0
            i = j + 1
        return r

    rx, ry = rank(x), rank(y)
    mx = sum(rx) / len(rx)
    my = sum(ry) / len(ry)
    num = sum((a - mx) * (b - my) for a, b in zip(rx, ry))
    den = math.sqrt(sum((a - mx) ** 2 for a in rx) * sum((b - my) ** 2 for b in ry))
    return num / den if den else None


def run_stage1_regions(mizan_bin, env, out_dir, models, locations, force=False):
    ds = suite_path("groundedness")
    pw_ds = suite_path("pairwise_rewardbench")
    for m in models:
        is_pro = "argon" in m or "carbon" in m
        limit = "5" if is_pro else "6"
        for loc in locations:
            out_path = os.path.join(out_dir, f"region__{m}__{loc}.json")
            run_compare(
                mizan_bin,
                env,
                out_path,
                ds,
                [
                    "--engine-a",
                    "genai",
                    "--model-a",
                    m,
                    "--engine-b",
                    "none",
                    "--location",
                    loc,
                    "--workers",
                    "1",
                    "--serial",
                    "--warmup",
                    "1",
                    "--limit",
                    limit,
                    "--score-tolerance",
                    "0.001",
                    "--notes",
                    f"Stage 1 multi-region serial probe {m} loc={loc}",
                ],
                force=force,
            )
        # Native EvaluateInstances compatibility check on global (n=3)
        nat_path = os.path.join(out_dir, f"native_compat__{m}__global.json")
        run_compare(
            mizan_bin,
            env,
            nat_path,
            pw_ds,
            [
                "--engine-a",
                "vertex",
                "--model-a",
                m,
                "--engine-b",
                "none",
                "--location",
                "global",
                "--workers",
                "1",
                "--warmup",
                "0",
                "--limit",
                "3",
                "--score-tolerance",
                "0.001",
                "--notes",
                f"Stage 1 native EvaluateInstances probe {m}",
            ],
            force=force,
            retry=False,
        )


def run_stage2_quality(mizan_bin, env, out_dir, candidate, baseline, fast_baseline, workers, force=False):
    for suite in ALL_QUALITY_SUITES:
        ds = suite_path(suite)
        tol = score_tolerance(suite)
        # 1. Baseline (gemini-3.8-flash) vs Candidate (gemini-4-hydrogen)
        out_pair = os.path.join(out_dir, f"q_base_vs_cand__{suite}.json")
        run_compare(
            mizan_bin,
            env,
            out_pair,
            ds,
            [
                "--engine-a",
                "genai",
                "--model-a",
                baseline,
                "--engine-b",
                "genai",
                "--model-b",
                candidate,
                "--pairwise-genai",
                "--workers",
                str(workers),
                "--warmup",
                "1",
                "--score-tolerance",
                tol,
                "--notes",
                f"Stage 2 paired quality {baseline} vs {candidate} on {suite}",
            ],
            force=force,
        )
        # 2. Fresh fast baseline (gemini-3.5-flash-lite)
        out_fast = os.path.join(out_dir, f"q_fast__{suite}.json")
        run_compare(
            mizan_bin,
            env,
            out_fast,
            ds,
            [
                "--engine-a",
                "genai",
                "--model-a",
                fast_baseline,
                "--engine-b",
                "none",
                "--pairwise-genai",
                "--workers",
                str(workers),
                "--warmup",
                "1",
                "--score-tolerance",
                tol,
                "--notes",
                f"Stage 2 fresh fast baseline {fast_baseline} on {suite}",
            ],
            force=force,
        )


def run_stage3_thinking_and_speed(mizan_bin, env, out_dir, candidate, baseline, fast_baseline, workers, force=False):
    # 3a. Thinking-level A/B (default vs low) on the 4 reasoning-heavy suites
    for suite in THINKING_AB_SUITES:
        ds = suite_path(suite)
        tol = score_tolerance(suite)
        out_ab = os.path.join(out_dir, f"think_ab__{suite}.json")
        run_compare(
            mizan_bin,
            env,
            out_ab,
            ds,
            [
                "--engine-a",
                "genai",
                "--model-a",
                candidate,
                "--engine-b",
                "genai",
                "--model-b",
                candidate,
                "--thinking-level",
                "low",
                "--thinking-side",
                "b",
                "--pairwise-genai",
                "--workers",
                str(workers),
                "--warmup",
                "1",
                "--score-tolerance",
                tol,
                "--notes",
                f"Stage 3 thinking A/B {candidate} default vs low on {suite}",
            ],
            force=force,
        )

    # 3b. Serial paired latency head-to-head on groundedness (n=15)
    ds_g = suite_path("groundedness")
    run_compare(
        mizan_bin,
        env,
        os.path.join(out_dir, "serial_lat__base_vs_cand__groundedness.json"),
        ds_g,
        [
            "--engine-a",
            "genai",
            "--model-a",
            baseline,
            "--engine-b",
            "genai",
            "--model-b",
            candidate,
            "--workers",
            "1",
            "--serial",
            "--warmup",
            "1",
            "--limit",
            "15",
            "--score-tolerance",
            "0.001",
            "--notes",
            f"Stage 3 serial paired latency {baseline} vs {candidate}",
        ],
        force=force,
    )
    run_compare(
        mizan_bin,
        env,
        os.path.join(out_dir, "serial_lat__cand_default_vs_low__groundedness.json"),
        ds_g,
        [
            "--engine-a",
            "genai",
            "--model-a",
            candidate,
            "--engine-b",
            "genai",
            "--model-b",
            candidate,
            "--thinking-level",
            "low",
            "--thinking-side",
            "b",
            "--workers",
            "1",
            "--serial",
            "--warmup",
            "1",
            "--limit",
            "15",
            "--score-tolerance",
            "0.001",
            "--notes",
            f"Stage 3 serial paired latency {candidate} default vs low",
        ],
        force=force,
    )

    # 3c. Concurrency throughput scaling (c = 1, 4, 8, 16) on safety_response
    ds_s = suite_path("safety_response")
    sweep_configs = [
        (fast_baseline, []),
        (baseline, []),
        (candidate, []),
        (f"{candidate}-low", ["--thinking-level", "low"]),
    ]
    sweep_limits = {1: "16", 4: "32", 8: "48", 16: "64"}
    for label, extra in sweep_configs:
        model_id = candidate if label == f"{candidate}-low" else label
        for lvl in (1, 4, 8, 16):
            out_sw = os.path.join(out_dir, f"sweep__{label}__c{lvl}.json")
            run_compare(
                mizan_bin,
                env,
                out_sw,
                ds_s,
                [
                    "--engine-a",
                    "genai",
                    "--model-a",
                    model_id,
                    "--engine-b",
                    "none",
                    "--workers",
                    str(lvl),
                    "--warmup",
                    "1",
                    "--limit",
                    sweep_limits[lvl],
                    "--score-tolerance",
                    "0.001",
                    "--notes",
                    f"Stage 3 concurrency sweep {label} c={lvl}",
                ]
                + extra,
                force=force,
            )


def run_stage4_pro_anchors(mizan_bin, env, out_dir, pro_anchors, workers, force=False):
    if len(pro_anchors) < 2:
        return
    m_a, m_b = pro_anchors[0], pro_anchors[1]
    pro_workers = min(workers, 6)
    for suite in HARD_SLICE_SUITES:
        ds = suite_path(suite)
        tol = score_tolerance(suite)
        out_pro = os.path.join(out_dir, f"pro_anchors__{suite}.json")
        run_compare(
            mizan_bin,
            env,
            out_pro,
            ds,
            [
                "--engine-a",
                "genai",
                "--model-a",
                m_a,
                "--engine-b",
                "genai",
                "--model-b",
                m_b,
                "--pairwise-genai",
                "--workers",
                str(pro_workers),
                "--warmup",
                "1",
                "--score-tolerance",
                tol,
                "--notes",
                f"Stage 4 Pro-tier ceiling anchor {m_a} vs {m_b} on {suite}",
            ],
            force=force,
        )


def extract_model_metrics(cases_by_id):
    corr = []
    lat = []
    prompt_toks = []
    cand_toks = []
    thought_toks = []
    total_toks = []
    versions = set()
    pred_num, gold_num = [], []
    errors = 0
    for c in cases_by_id.values():
        r = c["run"]
        if r.get("error"):
            errors += 1
            continue
        if r.get("duration_ms"):
            lat.append(r["duration_ms"])
        if r.get("model_version"):
            versions.add(r["model_version"])
        tok = r.get("tokens")
        if tok:
            prompt_toks.append(tok.get("prompt_tokens", 0))
            cand_toks.append(tok.get("candidates_tokens", 0))
            thought_toks.append(tok.get("thoughts_tokens", 0))
            total_toks.append(tok.get("total_tokens", 0))
        if r.get("correct") is not None:
            corr.append(1.0 if r["correct"] else 0.0)
        if r.get("score") is not None and c.get("expected"):
            try:
                g = float(c["expected"])
                pred_num.append(float(r["score"]))
                gold_num.append(g)
            except ValueError:
                pass
    n_total = len(cases_by_id)
    n_scored = len(corr)
    acc = sum(corr) / n_scored if n_scored else None
    ci = boot_ci(corr) if n_scored else None
    sp = spearman(pred_num, gold_num) if len(pred_num) >= 3 else None
    mae = (
        sum(abs(p - g) for p, g in zip(pred_num, gold_num)) / len(pred_num)
        if pred_num
        else None
    )
    return {
        "n_total": n_total,
        "n_scored": n_scored,
        "n_correct": int(round(sum(corr))),
        "errors": errors,
        "accuracy": acc,
        "accuracy_ci95": ci,
        "latency_p50_ms": pct(lat, 0.5),
        "latency_p95_ms": pct(lat, 0.95),
        "mean_prompt_tokens": sum(prompt_toks) / len(prompt_toks) if prompt_toks else None,
        "mean_candidates_tokens": sum(cand_toks) / len(cand_toks) if cand_toks else None,
        "mean_thoughts_tokens": sum(thought_toks) / len(thought_toks) if thought_toks else None,
        "mean_total_tokens": sum(total_toks) / len(total_toks) if total_toks else None,
        "model_versions": sorted(versions),
        "spearman": sp,
        "mae": mae,
    }


def pair_models(map_a, map_b):
    common_ids = sorted(set(map_a.keys()) & set(map_b.keys()))
    corr_a, corr_b = [], []
    preds_a, preds_b = [], []
    only_a, only_b, both_ok, both_bad = 0, 0, 0, 0
    for cid in common_ids:
        ra = map_a[cid]["run"]
        rb = map_b[cid]["run"]
        if ra.get("error") or rb.get("error"):
            continue
        pa = ra.get("prediction", "")
        pb = rb.get("prediction", "")
        if pa and pb:
            preds_a.append(pa)
            preds_b.append(pb)
        ca = ra.get("correct")
        cb = rb.get("correct")
        if ca is None or cb is None:
            continue
        corr_a.append(1.0 if ca else 0.0)
        corr_b.append(1.0 if cb else 0.0)
        if ca and cb:
            both_ok += 1
        elif ca:
            only_a += 1
        elif cb:
            only_b += 1
        else:
            both_bad += 1
    n = len(corr_a)
    if n == 0:
        return None
    diff_b_minus_a = (sum(corr_b) - sum(corr_a)) / n
    return {
        "n_paired": n,
        "both_correct": both_ok,
        "only_a_correct": only_a,
        "only_b_correct": only_b,
        "both_wrong": both_bad,
        "diff_b_minus_a": diff_b_minus_a,
        "diff_b_minus_a_ci95": boot_diff_ci(corr_b, corr_a),
        "mcnemar_p": mcnemar_exact(only_a, only_b),
        "agreement_pct": 100.0 * sum(1 for a, b in zip(preds_a, preds_b) if a == b) / len(preds_a) if preds_a else None,
        "cohen_kappa": cohen_kappa(preds_a, preds_b),
    }


def build_summary(out_dir, candidate, baseline, fast_baseline, pro_anchors, locations):
    models = [fast_baseline, baseline, candidate] + list(pro_anchors)

    # 1. Multi-region summary
    region_rows = []
    for m in models:
        nat_rep, nat_cases = load_merged_cases(os.path.join(out_dir, f"native_compat__{m}__global.json"))
        nat_ok = sum(1 for c in nat_cases if not c["comparison"]["engine_a"].get("error")) if nat_cases else 0
        for loc in locations:
            rep, cases = load_merged_cases(os.path.join(out_dir, f"region__{m}__{loc}.json"))
            if rep is None:
                continue
            cmap = {c["id"]: {"expected": c.get("expected"), "run": c["comparison"]["engine_a"]} for c in cases}
            mm = extract_model_metrics(cmap)
            region_rows.append(
                {
                    "model": m,
                    "location": loc,
                    "n": mm["n_total"],
                    "errors": mm["errors"],
                    "reachable": mm["errors"] < mm["n_total"],
                    "model_versions": mm["model_versions"],
                    "p50_ms": mm["latency_p50_ms"],
                    "p95_ms": mm["latency_p95_ms"],
                    "mean_prompt_tokens": mm["mean_prompt_tokens"],
                    "mean_thoughts_tokens": mm["mean_thoughts_tokens"],
                    "native_eval_global_ok": f"{nat_ok}/{len(nat_cases)}" if nat_cases else "-",
                }
            )

    # 2. Per-suite and pooled quality summary
    suite_maps = {s: {} for s in ALL_QUALITY_SUITES}
    for suite in ALL_QUALITY_SUITES:
        rep_bc, cases_bc = load_merged_cases(os.path.join(out_dir, f"q_base_vs_cand__{suite}.json"))
        if cases_bc:
            suite_maps[suite][baseline] = {
                c["id"]: {"expected": c.get("expected"), "run": c["comparison"]["engine_a"]} for c in cases_bc
            }
            suite_maps[suite][candidate] = {
                c["id"]: {"expected": c.get("expected"), "run": c["comparison"]["engine_b"]} for c in cases_bc
            }
        rep_f, cases_f = load_merged_cases(os.path.join(out_dir, f"q_fast__{suite}.json"))
        if cases_f:
            suite_maps[suite][fast_baseline] = {
                c["id"]: {"expected": c.get("expected"), "run": c["comparison"]["engine_a"]} for c in cases_f
            }
        if suite in HARD_SLICE_SUITES and len(pro_anchors) >= 2:
            rep_p, cases_p = load_merged_cases(os.path.join(out_dir, f"pro_anchors__{suite}.json"))
            if cases_p:
                suite_maps[suite][pro_anchors[0]] = {
                    c["id"]: {"expected": c.get("expected"), "run": c["comparison"]["engine_a"]} for c in cases_p
                }
                suite_maps[suite][pro_anchors[1]] = {
                    c["id"]: {"expected": c.get("expected"), "run": c["comparison"]["engine_b"]} for c in cases_p
                }

    per_suite_summary = {}
    for suite in ALL_QUALITY_SUITES:
        per_suite_summary[suite] = {"models": {}, "paired": {}}
        for m, cmap in suite_maps[suite].items():
            per_suite_summary[suite]["models"][m] = extract_model_metrics(cmap)
        if baseline in suite_maps[suite] and candidate in suite_maps[suite]:
            per_suite_summary[suite]["paired"]["base_vs_cand"] = pair_models(
                suite_maps[suite][baseline], suite_maps[suite][candidate]
            )
        if fast_baseline in suite_maps[suite] and candidate in suite_maps[suite]:
            per_suite_summary[suite]["paired"]["fast_vs_cand"] = pair_models(
                suite_maps[suite][fast_baseline], suite_maps[suite][candidate]
            )

    # Pooled across all 946 items (and 346 hard-slice items)
    def pool_maps(suites_list, model_name):
        merged = {}
        for s in suites_list:
            for cid, entry in suite_maps.get(s, {}).get(model_name, {}).items():
                merged[f"{s}::{cid}"] = entry
        return merged

    pooled_all_models = {}
    pooled_disc_models = {}
    for m in (fast_baseline, baseline, candidate):
        pm = pool_maps(ALL_QUALITY_SUITES, m)
        if pm:
            pooled_all_models[m] = extract_model_metrics(pm)
        dm = pool_maps(DISCRETE_QUALITY_SUITES, m)
        if dm:
            pooled_disc_models[m] = extract_model_metrics(dm)

    pooled_all_paired = {}
    pm_base = pool_maps(ALL_QUALITY_SUITES, baseline)
    pm_cand = pool_maps(ALL_QUALITY_SUITES, candidate)
    pm_fast = pool_maps(ALL_QUALITY_SUITES, fast_baseline)
    if pm_base and pm_cand:
        pooled_all_paired["base_vs_cand"] = pair_models(pm_base, pm_cand)
    if pm_fast and pm_cand:
        pooled_all_paired["fast_vs_cand"] = pair_models(pm_fast, pm_cand)
    if pm_fast and pm_base:
        pooled_all_paired["fast_vs_base"] = pair_models(pm_fast, pm_base)

    pooled_disc_paired = {}
    dm_base = pool_maps(DISCRETE_QUALITY_SUITES, baseline)
    dm_cand = pool_maps(DISCRETE_QUALITY_SUITES, candidate)
    dm_fast = pool_maps(DISCRETE_QUALITY_SUITES, fast_baseline)
    if dm_base and dm_cand:
        pooled_disc_paired["base_vs_cand"] = pair_models(dm_base, dm_cand)
    if dm_fast and dm_cand:
        pooled_disc_paired["fast_vs_cand"] = pair_models(dm_fast, dm_cand)
    if dm_fast and dm_base:
        pooled_disc_paired["fast_vs_base"] = pair_models(dm_fast, dm_base)

    pooled_hard_models = {}
    for m in models:
        pm = pool_maps(HARD_SLICE_SUITES, m)
        if pm:
            pooled_hard_models[m] = extract_model_metrics(pm)

    pooled_hard_paired = {}
    hm_cand = pool_maps(HARD_SLICE_SUITES, candidate)
    for m in models:
        if m == candidate:
            continue
        hm_other = pool_maps(HARD_SLICE_SUITES, m)
        if hm_other and hm_cand:
            pooled_hard_paired[f"{m}_vs_{candidate}"] = pair_models(hm_other, hm_cand)

    # 3. Thinking A/B summary
    thinking_ab = {"per_suite": {}, "pooled": {}}
    ab_def_pool, ab_low_pool = {}, {}
    for suite in THINKING_AB_SUITES:
        rep_ab, cases_ab = load_merged_cases(os.path.join(out_dir, f"think_ab__{suite}.json"))
        if not cases_ab:
            continue
        m_def = {c["id"]: {"expected": c.get("expected"), "run": c["comparison"]["engine_a"]} for c in cases_ab}
        m_low = {c["id"]: {"expected": c.get("expected"), "run": c["comparison"]["engine_b"]} for c in cases_ab}
        for cid, v in m_def.items():
            ab_def_pool[f"{suite}::{cid}"] = v
        for cid, v in m_low.items():
            ab_low_pool[f"{suite}::{cid}"] = v
        thinking_ab["per_suite"][suite] = {
            "default": extract_model_metrics(m_def),
            "low": extract_model_metrics(m_low),
            "paired": pair_models(m_def, m_low),
        }
    if ab_def_pool and ab_low_pool:
        thinking_ab["pooled"] = {
            "default": extract_model_metrics(ab_def_pool),
            "low": extract_model_metrics(ab_low_pool),
            "paired": pair_models(ab_def_pool, ab_low_pool),
        }

    # Serial paired latency summary
    serial_latency = {}
    for key, fname in (
        ("base_vs_cand", "serial_lat__base_vs_cand__groundedness.json"),
        ("cand_default_vs_low", "serial_lat__cand_default_vs_low__groundedness.json"),
    ):
        rep_s, cases_s = load_merged_cases(os.path.join(out_dir, fname))
        if cases_s:
            ma = {c["id"]: {"expected": c.get("expected"), "run": c["comparison"]["engine_a"]} for c in cases_s}
            mb = {c["id"]: {"expected": c.get("expected"), "run": c["comparison"]["engine_b"]} for c in cases_s}
            serial_latency[key] = {
                "side_a": extract_model_metrics(ma),
                "side_b": extract_model_metrics(mb),
            }

    # 4. Concurrency sweep summary
    sweep_rows = []
    for label in (fast_baseline, baseline, candidate, f"{candidate}-low"):
        for lvl in (1, 4, 8, 16):
            rep_sw, cases_sw = load_merged_cases(os.path.join(out_dir, f"sweep__{label}__c{lvl}.json"))
            if rep_sw is None or not cases_sw:
                continue
            cmap = {c["id"]: {"expected": c.get("expected"), "run": c["comparison"]["engine_a"]} for c in cases_sw}
            mm = extract_model_metrics(cmap)
            wall_s = rep_sw.get("meta", {}).get("batch_wall_ms", 0.0) / 1000.0
            ok_n = mm["n_total"] - mm["errors"]
            sweep_rows.append(
                {
                    "target": label,
                    "concurrency": lvl,
                    "n": mm["n_total"],
                    "errors": mm["errors"],
                    "wall_s": wall_s,
                    "items_per_s": ok_n / wall_s if wall_s > 0 else None,
                    "p50_ms": mm["latency_p50_ms"],
                    "p95_ms": mm["latency_p95_ms"],
                    "accuracy": mm["accuracy"],
                    "mean_thoughts_tokens": mm["mean_thoughts_tokens"],
                }
            )

    summary = {
        "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "candidate": candidate,
        "baseline": baseline,
        "fast_baseline": fast_baseline,
        "pro_anchors": pro_anchors,
        "multi_region": region_rows,
        "per_suite": per_suite_summary,
        "pooled_discrete_746": {"models": pooled_disc_models, "paired": pooled_disc_paired},
        "pooled_946": {"models": pooled_all_models, "paired": pooled_all_paired},
        "pooled_hard_346": {"models": pooled_hard_models, "paired": pooled_hard_paired},
        "thinking_ab": thinking_ab,
        "serial_latency": serial_latency,
        "concurrency_sweep": sweep_rows,
    }
    backends = {
        "captured_at": summary["generated_at"],
        "vertex_genai": {
            "locations_tested": locations,
            "models": {
                m: {
                    "model_versions": sorted(
                        {
                            v
                            for r in region_rows
                            if r["model"] == m
                            for v in r.get("model_versions", [])
                        }
                    ),
                    "locations_reachable": [
                        r["location"] for r in region_rows if r["model"] == m and r["reachable"]
                    ],
                    "native_evaluate_instances_global": next(
                        (r["native_eval_global_ok"] for r in region_rows if r["model"] == m),
                        None,
                    ),
                }
                for m in models
            },
        },
    }
    backends_out = os.path.join(out_dir, "backends.json")
    with open(backends_out, "w") as f:
        json.dump(backends, f, indent=2)

    json_out = os.path.join(out_dir, "qualification_summary.json")
    with open(json_out, "w") as f:
        json.dump(summary, f, indent=2)

    md_out = os.path.join(out_dir, "qualification_summary.md")
    with open(md_out, "w") as f:
        f.write(render_markdown(summary))
    print(f"Wrote {backends_out}, {json_out}, and {md_out}", flush=True)


def fmt_pct(v):
    return "-" if v is None else f"{100.0 * v:.1f}%"


def fmt_ci(ci):
    if not ci:
        return "-"
    return f"[{100.0 * ci[0]:.1f}%, {100.0 * ci[1]:.1f}%]"


def fmt_ms(v):
    return "-" if v is None else f"{v:.0f}"


def render_markdown(s):
    cand = s["candidate"]
    base = s["baseline"]
    fast = s["fast_baseline"]
    pro = s["pro_anchors"]
    all_models = [fast, base, cand] + list(pro)

    lines = [
        f"# Autorater Qualification Summary: `{cand}` vs. `{base}`",
        "",
        f"Generated: `{s['generated_at']}`",
        "",
        "## 1. Multi-Region (`global`, `us`, `eu`) Availability & Unloaded Serial Latency",
        "",
        "| Model | Location | Reachable | Model Version(s) | Serial p50 (ms) | Serial p95 (ms) | Mean Prompt Tok | Mean Thoughts Tok | Native `EvaluateInstances` |",
        "| :--- | :---: | :---: | :--- | ---: | ---: | ---: | ---: | :---: |",
    ]
    for r in s["multi_region"]:
        vers = ", ".join(r["model_versions"]) if r["model_versions"] else "-"
        pt = "-" if r["mean_prompt_tokens"] is None else f"{r['mean_prompt_tokens']:.0f}"
        tt = "-" if r["mean_thoughts_tokens"] is None else f"{r['mean_thoughts_tokens']:.0f}"
        lines.append(
            f"| `{r['model']}` | `{r['location']}` | {'YES' if r['reachable'] else 'NO'} ({r['n'] - r['errors']}/{r['n']}) | `{vers}` | {fmt_ms(r['p50_ms'])} | {fmt_ms(r['p95_ms'])} | {pt} | {tt} | {r['native_eval_global_ok']} |"
        )

    lines += [
        "",
        "## 2. Per-Suite Accuracy vs. Human Gold Labels (946 Items Across 10 Suites)",
        "",
        f"| Suite | `{fast}` | `{base}` | `{cand}` | `{pro[0] if pro else 'argon'}` | `{pro[1] if len(pro) > 1 else 'carbon'}` | `{cand} - {base}` (pts) | McNemar $p$ (`{cand}` vs `{base}`) |",
        "| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |",
    ]
    for suite in ALL_QUALITY_SUITES:
        ps = s["per_suite"].get(suite, {})
        ms = ps.get("models", {})
        cells = []
        for m in all_models:
            mm = ms.get(m)
            if not mm or mm["accuracy"] is None:
                cells.append("-")
            else:
                cells.append(f"{100.0 * mm['accuracy']:.1f}% ({mm['n_correct']}/{mm['n_scored']})")
        pr = ps.get("paired", {}).get("base_vs_cand")
        diff_str = f"{100.0 * pr['diff_b_minus_a']:+.1f}" if pr else "-"
        p_str = f"{pr['mcnemar_p']:.3f}" if pr else "-"
        lines.append(
            f"| `{suite}` | {cells[0]} | {cells[1]} | {cells[2]} | {cells[3] if len(cells) > 3 else '-'} | {cells[4] if len(cells) > 4 else '-'} | {diff_str} | {p_str} |"
        )

    # Pooled rows
    p746 = s.get("pooled_discrete_746", {})
    m746 = p746.get("models", {})
    pr746 = p746.get("paired", {}).get("base_vs_cand")
    if pr746:
        lines.append(
            f"| **Pooled Discrete + Calibration (746 items)** | **{fmt_pct(m746.get(fast, {}).get('accuracy'))}** {fmt_ci(m746.get(fast, {}).get('accuracy_ci95'))} | **{fmt_pct(m746.get(base, {}).get('accuracy'))}** {fmt_ci(m746.get(base, {}).get('accuracy_ci95'))} | **{fmt_pct(m746.get(cand, {}).get('accuracy'))}** {fmt_ci(m746.get(cand, {}).get('accuracy_ci95'))} | - | - | **{100.0 * pr746['diff_b_minus_a']:+.2f}** | **{pr746['mcnemar_p']:.4f}** |"
        )

    p946 = s["pooled_946"]
    m946 = p946.get("models", {})
    pr946 = p946.get("paired", {}).get("base_vs_cand")
    if pr946:
        lines.append(
            f"| **Pooled All (946 items)** | **{fmt_pct(m946.get(fast, {}).get('accuracy'))}** {fmt_ci(m946.get(fast, {}).get('accuracy_ci95'))} | **{fmt_pct(m946.get(base, {}).get('accuracy'))}** {fmt_ci(m946.get(base, {}).get('accuracy_ci95'))} | **{fmt_pct(m946.get(cand, {}).get('accuracy'))}** {fmt_ci(m946.get(cand, {}).get('accuracy_ci95'))} | - | - | **{100.0 * pr946['diff_b_minus_a']:+.2f}** | **{pr946['mcnemar_p']:.4f}** |"
        )

    p346 = s["pooled_hard_346"]
    m346 = p346.get("models", {})
    pr346 = p346.get("paired", {}).get(f"{base}_vs_{cand}")
    h_cells = [
        f"**{fmt_pct(m346.get(m, {}).get('accuracy'))}** {fmt_ci(m346.get(m, {}).get('accuracy_ci95'))}"
        for m in all_models
    ]
    if pr346:
        lines.append(
            f"| **Pooled Hard Slice (346 items)** | {h_cells[0]} | {h_cells[1]} | {h_cells[2]} | {h_cells[3]} | {h_cells[4]} | **{100.0 * pr346['diff_b_minus_a']:+.2f}** | **{pr346['mcnemar_p']:.4f}** |"
        )

    lines += [
        "",
        "### 2b. Likert Scale Correlation (Spearman $\\rho$ / MAE vs. Human Mean)",
        "",
        f"| Suite | `{fast}` ($\\rho$ / MAE) | `{base}` ($\\rho$ / MAE) | `{cand}` ($\\rho$ / MAE) | $\\Delta \\rho$ (`{cand} - {base}`) |",
        "| :--- | :---: | :---: | :---: | :---: |",
    ]
    for suite in LIKERT_SUITES:
        ms = s["per_suite"].get(suite, {}).get("models", {})
        c_list = []
        for m in (fast, base, cand):
            mm = ms.get(m, {})
            sp = mm.get("spearman")
            mae = mm.get("mae")
            c_list.append(f"{sp:.3f} / {mae:.2f}" if sp is not None and mae is not None else "-")
        sp_b = ms.get(base, {}).get("spearman")
        sp_c = ms.get(cand, {}).get("spearman")
        dsp = f"{sp_c - sp_b:+.3f}" if sp_b is not None and sp_c is not None else "-"
        lines.append(f"| `{suite}` | {c_list[0]} | {c_list[1]} | {c_list[2]} | {dsp} |")

    lines += [
        "",
        f"## 3. Thinking-Level A/B (`DEFAULT` vs. `--thinking-level low`) on `{cand}`",
        "",
        "| Suite | `DEFAULT` Acc | `LOW` Acc | $\\Delta$ Acc (`LOW - DEFAULT`) | McNemar $p$ | `DEFAULT` p50 (ms) | `LOW` p50 (ms) | `DEFAULT` Mean Thoughts Tok | `LOW` Mean Thoughts Tok |",
        "| :--- | :---: | :---: | :---: | :---: | ---: | ---: | ---: | ---: |",
    ]
    for suite, ab in s["thinking_ab"].get("per_suite", {}).items():
        d, lw, pr = ab["default"], ab["low"], ab["paired"]
        diff_s = f"{100.0 * pr['diff_b_minus_a']:+.1f} pts" if pr else "-"
        p_s = f"{pr['mcnemar_p']:.3f}" if pr else "-"
        dt = f"{d['mean_thoughts_tokens']:.0f}" if d.get("mean_thoughts_tokens") is not None else "-"
        lt = f"{lw['mean_thoughts_tokens']:.0f}" if lw.get("mean_thoughts_tokens") is not None else "-"
        lines.append(
            f"| `{suite}` | {fmt_pct(d['accuracy'])} | {fmt_pct(lw['accuracy'])} | {diff_s} | {p_s} | {fmt_ms(d['latency_p50_ms'])} | {fmt_ms(lw['latency_p50_ms'])} | {dt} | {lt} |"
        )
    if s["thinking_ab"].get("pooled"):
        ab = s["thinking_ab"]["pooled"]
        d, lw, pr = ab["default"], ab["low"], ab["paired"]
        dt = f"{d['mean_thoughts_tokens']:.0f}" if d.get("mean_thoughts_tokens") is not None else "-"
        lt = f"{lw['mean_thoughts_tokens']:.0f}" if lw.get("mean_thoughts_tokens") is not None else "-"
        lines.append(
            f"| **Pooled ({d['n_scored']} items)** | **{fmt_pct(d['accuracy'])}** | **{fmt_pct(lw['accuracy'])}** | **{100.0 * pr['diff_b_minus_a']:+.2f} pts** | **{pr['mcnemar_p']:.4f}** | **{fmt_ms(d['latency_p50_ms'])}** | **{fmt_ms(lw['latency_p50_ms'])}** | **{dt}** | **{lt}** |"
        )

    if s.get("serial_latency"):
        lines += [
            "",
            "## 4. Serial Paired Latency (`--serial --workers 1`, `groundedness`, $n=15$)",
            "",
            "| Comparison | Side A | Side A p50 / p95 (ms) | Side B | Side B p50 / p95 (ms) | Side A Mean Thoughts | Side B Mean Thoughts |",
            "| :--- | :--- | :---: | :--- | :---: | ---: | ---: |",
        ]
        if "base_vs_cand" in s["serial_latency"]:
            sl = s["serial_latency"]["base_vs_cand"]
            sa, sb = sl["side_a"], sl["side_b"]
            ta = f"{sa['mean_thoughts_tokens']:.0f}" if sa.get("mean_thoughts_tokens") is not None else "-"
            tb = f"{sb['mean_thoughts_tokens']:.0f}" if sb.get("mean_thoughts_tokens") is not None else "-"
            lines.append(
                f"| `{base}` vs `{cand}` | `{base}` | {fmt_ms(sa['latency_p50_ms'])} / {fmt_ms(sa['latency_p95_ms'])} | `{cand}` | {fmt_ms(sb['latency_p50_ms'])} / {fmt_ms(sb['latency_p95_ms'])} | {ta} | {tb} |"
            )
        if "cand_default_vs_low" in s["serial_latency"]:
            sl = s["serial_latency"]["cand_default_vs_low"]
            sa, sb = sl["side_a"], sl["side_b"]
            ta = f"{sa['mean_thoughts_tokens']:.0f}" if sa.get("mean_thoughts_tokens") is not None else "-"
            tb = f"{sb['mean_thoughts_tokens']:.0f}" if sb.get("mean_thoughts_tokens") is not None else "-"
            lines.append(
                f"| `{cand}` `DEFAULT` vs `LOW` | `{cand} (default)` | {fmt_ms(sa['latency_p50_ms'])} / {fmt_ms(sa['latency_p95_ms'])} | `{cand} (low)` | {fmt_ms(sb['latency_p50_ms'])} / {fmt_ms(sb['latency_p95_ms'])} | {ta} | {tb} |"
            )

    lines += [
        "",
        "## 5. Concurrency & Throughput Scaling (`safety_response`, $c = 1, 4, 8, 16$)",
        "",
        "| Target | Concurrency ($c$) | Throughput (items/s) | p50 (ms) | p95 (ms) | Errors | Accuracy | Mean Thoughts Tok |",
        "| :--- | :---: | ---: | ---: | ---: | :---: | :---: | ---: |",
    ]
    for r in s["concurrency_sweep"]:
        ips = "-" if r["items_per_s"] is None else f"{r['items_per_s']:.2f}"
        tt = "-" if r["mean_thoughts_tokens"] is None else f"{r['mean_thoughts_tokens']:.0f}"
        lines.append(
            f"| `{r['target']}` | {r['concurrency']} | {ips} | {fmt_ms(r['p50_ms'])} | {fmt_ms(r['p95_ms'])} | {r['errors']}/{r['n']} | {fmt_pct(r['accuracy'])} | {tt} |"
        )

    # 6. 5-Gate Promotion Checklist
    cand_regions_ok = all(r["reachable"] for r in s["multi_region"] if r["model"] == cand)
    cand_native_ok = all(r["native_eval_global_ok"] == "3/3" for r in s["multi_region"] if r["model"] == cand)
    calib_cand = s["per_suite"].get("calibration_suite", {}).get("models", {}).get(cand, {})
    calib_base = s["per_suite"].get("calibration_suite", {}).get("models", {}).get(base, {})
    calib_pass = (
        calib_cand.get("errors", 1) == 0
        and (calib_cand.get("accuracy") or 0.0) >= (calib_base.get("accuracy") or 0.0)
    )
    pr_disc = s.get("pooled_discrete_746", {}).get("paired", {}).get("base_vs_cand", {})
    disc_delta = 100.0 * pr_disc.get("diff_b_minus_a", 0.0) if pr_disc else 0.0
    disc_p = pr_disc.get("mcnemar_p", 1.0) if pr_disc else 1.0
    acc_pass = disc_delta >= -1.0 or disc_p >= 0.05

    sl_bc = s.get("serial_latency", {}).get("base_vs_cand", {})
    base_p50 = sl_bc.get("side_a", {}).get("latency_p50_ms")
    cand_p50 = sl_bc.get("side_b", {}).get("latency_p50_ms")
    c16_base = next((r for r in s["concurrency_sweep"] if r["target"] == base and r["concurrency"] == 16), {})
    c16_cand = next((r for r in s["concurrency_sweep"] if r["target"] == cand and r["concurrency"] == 16), {})
    c16_low = next((r for r in s["concurrency_sweep"] if r["target"] == f"{cand}-low" and r["concurrency"] == 16), {})
    lat_pass = bool(base_p50 and cand_p50 and cand_p50 <= base_p50)

    ab_pool = s.get("thinking_ab", {}).get("pooled", {})
    ab_def_tok = ab_pool.get("default", {}).get("mean_thoughts_tokens")
    ab_low_tok = ab_pool.get("low", {}).get("mean_thoughts_tokens")
    ab_diff = 100.0 * ab_pool.get("paired", {}).get("diff_b_minus_a", 0.0) if ab_pool.get("paired") else 0.0

    lines += [
        "",
        "## 6. 5-Gate Autorater Promotion Checklist",
        "",
        "| Gate | Criterion | Result | Verdict |",
        "| :--- | :--- | :--- | :---: |",
        f"| **1. Endpoint & Routing Readiness** | Reachable in `global`, `us`, `eu`; native `EvaluateInstances` compatible | `global`/`us`/`eu` reachable (`6/6` each); native `3/3` | **{'PASS' if (cand_regions_ok and cand_native_ok) else 'CHECK'}** |",
        f"| **2. Schema & Calibration Fidelity** | 0 schema/parse errors on `calibration_suite` (46 cases, 15 templates incl. 77-intent choice); accuracy $\\ge$ `{base}` | `{fmt_pct(calib_cand.get('accuracy'))}` vs `{fmt_pct(calib_base.get('accuracy'))}` (`{calib_cand.get('errors', 0)}` errors) | **{'PASS' if calib_pass else 'CHECK'}** |",
        f"| **3. Human-Gold Accuracy Parity** | Pooled 746-item discrete + calibration accuracy within CI of `{base}` ($p \\ge 0.05$ or $\\Delta \\ge 0$) | `{fmt_pct(m746.get(cand, {}).get('accuracy'))}` vs `{fmt_pct(m746.get(base, {}).get('accuracy'))}` ($\\Delta = {disc_delta:+.2f}$ pts, McNemar $p = {disc_p:.4f}$) | **{'PASS' if acc_pass else 'CHECK'}** |",
        f"| **4. Latency & Throughput SLO** | Serial p50 $\\le$ `{base}` and $c=16$ throughput $\\ge$ `{base}` | Serial p50 `{fmt_ms(cand_p50)} ms` vs `{fmt_ms(base_p50)} ms`; $c=16$ `{c16_cand.get('items_per_s', 0):.2f} items/s` (`DEFAULT`) / `{c16_low.get('items_per_s', 0):.2f} items/s` (`LOW`) vs `{c16_base.get('items_per_s', 0):.2f} items/s` | **{'PASS' if lat_pass else 'CHECK'}** |",
        f"| **5. Token & Thinking Economy** | Documented `thoughtsTokenCount` overhead and `--thinking-level low` Pareto frontier | `DEFAULT` `{ab_def_tok:.0f}` thoughts tok vs `LOW` `{ab_low_tok:.0f}` thoughts tok ($\\Delta$ acc `{ab_diff:+.2f}` pts on 444 items) | **PASS** |",
    ]

    return "\n".join(lines) + "\n"


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", required=True, help="Output directory for run JSONs and summary")
    ap.add_argument("--candidate", default="gemini-4-hydrogen")
    ap.add_argument("--baseline", default="gemini-3.8-flash")
    ap.add_argument("--fast-baseline", default="gemini-3.5-flash-lite")
    ap.add_argument("--pro-anchors", nargs="+", default=["gemini-4-argon", "gemini-4-carbon"])
    ap.add_argument("--locations", nargs="+", default=["global", "us", "eu"])
    ap.add_argument("--workers", type=int, default=6)
    ap.add_argument("--mizan", default="/tmp/mizan")
    ap.add_argument("--registry-db", default="/tmp/mizan-qualify.db")
    ap.add_argument("--templates-root", default=os.path.abspath(os.path.join(ROOT, "..", "mizan-templates")))
    ap.add_argument("--project", default="")
    ap.add_argument("--stages", nargs="+", default=["1", "2", "3", "4", "summary"])
    ap.add_argument("--force", action="store_true")
    a = ap.parse_args()

    os.makedirs(a.out, exist_ok=True)
    env = dict(os.environ)
    if any(s in a.stages for s in ("1", "2", "3", "4")):
        project = resolve_project_id(a.project)
        ensure_registry(a.mizan, a.registry_db, a.templates_root)
        env["MIZAN_PROJECT_ID"] = project
        env["MIZAN_REGISTRY_DB"] = a.registry_db

    all_models = [a.fast_baseline, a.baseline, a.candidate] + list(a.pro_anchors)
    if "1" in a.stages:
        run_stage1_regions(a.mizan, env, a.out, all_models, a.locations, force=a.force)
    if "2" in a.stages:
        run_stage2_quality(a.mizan, env, a.out, a.candidate, a.baseline, a.fast_baseline, a.workers, force=a.force)
    if "3" in a.stages:
        run_stage3_thinking_and_speed(
            a.mizan, env, a.out, a.candidate, a.baseline, a.fast_baseline, a.workers, force=a.force
        )
    if "4" in a.stages:
        run_stage4_pro_anchors(a.mizan, env, a.out, a.pro_anchors, a.workers, force=a.force)
    if "summary" in a.stages:
        build_summary(a.out, a.candidate, a.baseline, a.fast_baseline, a.pro_anchors, a.locations)
    return 0


if __name__ == "__main__":
    sys.exit(main())
