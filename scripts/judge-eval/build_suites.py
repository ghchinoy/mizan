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
"""Build the Experiment 07 judge-eval suites from public, human-labelled datasets.

Every suite is a `mizan eval compare-engines --dataset` JSONL file whose
`expected` field is a gold/human label, bound to a template in the
`judge-eval` pack of mizan-templates.

Rows come from the Hugging Face datasets-server API, pinned to the dataset
revision recorded in the manifest. Sampling is deterministic: a fixed seed over
upstream rows sorted by row index. The suites are written to
docs/experiments/judge-eval/suites/, which is gitignored because several
upstream licences are non-commercial (CC-BY-NC). Only manifest.json (row
indices, class balance, SHA-256 per suite) is committed, so anyone can rebuild
the exact same files.

Usage:
  python3 scripts/judge-eval/build_suites.py            # build all suites
  python3 scripts/judge-eval/build_suites.py --only safety_response,pairwise_mtbench
  python3 scripts/judge-eval/build_suites.py --check    # rebuild and compare SHA-256 with manifest.json

Needs Python 3.9+ and network access. pyarrow is optional but strongly recommended
(parquet download instead of thousands of rate-limited /rows calls).
"""

import argparse
import ast
import hashlib
import json
import os
import random
import sys
import time
import urllib.parse
import urllib.request

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
OUT_DIR = os.path.join(ROOT, "docs", "experiments", "judge-eval", "suites")
MANIFEST = os.path.join(ROOT, "docs", "experiments", "judge-eval", "manifest.json")
API = "https://datasets-server.huggingface.co"
HUB = "https://huggingface.co/api/datasets/"
SEED = 20260925
N = 100  # target items per suite

# ---------------------------------------------------------------------------
# HTTP helpers
# ---------------------------------------------------------------------------

_cache = {}


def _get(url, tries=6):
    if url in _cache:
        return _cache[url]
    headers = {"User-Agent": "mizan-judge-eval-builder"}
    tok = os.environ.get("HF_TOKEN")
    if tok:
        headers["Authorization"] = "Bearer " + tok
    for attempt in range(tries):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=60) as r:
                data = json.load(r)
                _cache[url] = data
                return data
        except Exception as e:  # noqa: BLE001 - retry any transient failure
            if attempt == tries - 1:
                raise RuntimeError(f"GET {url}: {e}") from e
            time.sleep(1.5 * (2 ** attempt))


def hub_info(dataset):
    d = _get(HUB + dataset)
    return {"revision": d.get("sha"), "license": (d.get("cardData") or {}).get("license")}


def fetch_all(dataset, config, split, max_rows=None):
    """Return [(row_idx, row)] for a whole split.

    Prefers the datasets-server parquet export (one download per shard, needs
    pyarrow); falls back to paging /rows (100 rows per call, rate limited).
    Row indices are positions in the split either way, so manifests agree.
    """
    try:
        import pyarrow.parquet as pq  # noqa: PLC0415 - optional dependency
        import io  # noqa: PLC0415
    except ImportError:
        pq = None
    if pq is not None:
        q = urllib.parse.urlencode({"dataset": dataset, "config": config})
        files = [f for f in _get(f"{API}/parquet?{q}")["parquet_files"] if f["split"] == split]
        if files:
            files.sort(key=lambda f: f["url"])
            rows = []
            headers = {"User-Agent": "mizan-judge-eval-builder"}
            if os.environ.get("HF_TOKEN"):
                headers["Authorization"] = "Bearer " + os.environ["HF_TOKEN"]
            for f in files:
                with urllib.request.urlopen(urllib.request.Request(f["url"], headers=headers), timeout=300) as r:
                    table = pq.read_table(io.BytesIO(r.read()))
                for rec in table.to_pylist():
                    rows.append((len(rows), rec))
            sys.stderr.write(f"  {dataset}:{split} {len(rows)} rows (parquet)\n")
            return rows[:max_rows] if max_rows else rows
    q = urllib.parse.urlencode({"dataset": dataset, "config": config, "split": split, "offset": 0, "length": 100})
    first = _get(f"{API}/rows?{q}")
    total = first["num_rows_total"]
    if max_rows:
        total = min(total, max_rows)
    rows = [(r["row_idx"], r["row"]) for r in first["rows"]]
    for off in range(100, total, 100):
        q = urllib.parse.urlencode({"dataset": dataset, "config": config, "split": split, "offset": off, "length": 100})
        rows += [(r["row_idx"], r["row"]) for r in _get(f"{API}/rows?{q}")["rows"]]
        time.sleep(0.25)
    sys.stderr.write(f"  {dataset}:{split} {len(rows)} rows (paged)\n")
    return rows


def balanced(rng, pools, per_pool):
    """Sample per_pool items from each pool (dict key -> list), deterministically."""
    out = []
    for key in sorted(pools):
        items = sorted(pools[key], key=lambda x: x[0])
        k = min(per_pool, len(items))
        out += rng.sample(items, k)
    return out


def short(s, limit):
    return isinstance(s, str) and 0 < len(s) <= limit


# ---------------------------------------------------------------------------
# Suite builders. Each returns (items, source) where items are JSONL dicts.
# ---------------------------------------------------------------------------


def safety_response(rng):
    """BeaverTails 30k_test: human is_safe label on (prompt, response)."""
    ds = "PKU-Alignment/BeaverTails"
    rows = fetch_all(ds, "default", "30k_test")
    pools = {"safe": [], "unsafe": []}
    for idx, r in rows:
        if not (short(r["prompt"], 1500) and short(r["response"], 2500)):
            continue
        pools["safe" if str(r["is_safe"]).lower() == "true" else "unsafe"].append((idx, r))
    picked = balanced(rng, pools, N // 2)
    harm, vtx = [], []
    for idx, r in picked:
        safe = str(r["is_safe"]).lower() == "true"
        base = {"tier": "safe" if safe else "unsafe", "category": "safety", "source": f"{ds}#30k_test:{idx}"}
        harm.append(dict(base, id=f"bt-{idx}", metric="judge-eval/response-harm-check",
                         fields={"prompt": r["prompt"], "response": r["response"]}, expected="FAIL" if safe else "PASS"))
        # Vertex prebuilt safety reads only the response (SafetyInstance.prediction).
        vtx.append(dict(base, id=f"bt-{idx}", metric="judge-eval/prebuilt-safety",
                        fields={"response": r["response"]}, expected="PASS" if safe else "FAIL"))
    return {"safety_response": harm, "prebuilt_safety": vtx}, ds


def toxicity_prompt(rng):
    """ToxicChat (toxicchat0124 test): human toxicity label on real user prompts."""
    ds = "lmsys/toxic-chat"
    rows = fetch_all(ds, "toxicchat0124", "test")
    pools = {"toxic": [], "benign": []}
    for idx, r in rows:
        if not short(r["user_input"], 2000):
            continue
        pools["toxic" if int(r["toxicity"]) == 1 else "benign"].append((idx, r))
    items = []
    for idx, r in balanced(rng, pools, N // 2):
        tox = int(r["toxicity"]) == 1
        tier = ("jailbreak" if int(r["jailbreaking"]) == 1 else "toxic") if tox else "benign"
        items.append({"id": f"tc-{idx}", "metric": "judge-eval/prompt-toxicity-check", "tier": tier, "category": "toxicity",
                      "fields": {"message": r["user_input"]}, "expected": "PASS" if tox else "FAIL",
                      "source": f"{ds}#toxicchat0124/test:{idx}"})
    return {"toxicity_prompt": items}, ds


def groundedness(rng):
    """HaluBench: PASS = answer faithful to passage (human/source-derived labels)."""
    ds = "PatronusAI/HaluBench"
    rows = fetch_all(ds, "default", "test")
    # DROP answers are serialized python lists of span candidates; skip that source.
    sources = ["covidQA", "pubmedQA", "FinanceBench", "RAGTruth", "halueval"]
    per = {s: {"PASS": [], "FAIL": []} for s in sources}
    for idx, r in rows:
        s = r["source_ds"]
        if s not in per or not (short(r["passage"], 7000) and short(r["question"], 800) and short(r["answer"], 1500)):
            continue
        per[s][r["label"]].append((idx, r))
    picked = []
    for s in sources:  # 5 sources x 2 labels x 10 = 100
        picked += balanced(rng, per[s], N // (2 * len(sources)))
    faith, vtx = [], []
    for idx, r in picked:
        base = {"tier": r["source_ds"], "category": "groundedness", "source": f"{ds}#test:{idx}"}
        fields = {"passage": r["passage"], "question": r["question"], "answer": r["answer"]}
        faith.append(dict(base, id=f"hb-{idx}", metric="judge-eval/answer-faithfulness-check", fields=fields, expected=r["label"]))
        # Vertex groundedness sees only (prediction, context); short extractive
        # answers ("Rams") are unjudgeable without the question, so the question
        # is prepended to the context rather than dropped.
        vfields = {"context": f"Question: {r['question']}\n\nPassage:\n{r['passage']}", "answer": r["answer"]}
        vtx.append(dict(base, id=f"hb-{idx}", metric="judge-eval/prebuilt-groundedness", fields=vfields, expected=r["label"]))
    return {"groundedness": faith, "prebuilt_groundedness": vtx}, ds


def _mt_turns(conv):
    try:
        return ast.literal_eval(conv) if isinstance(conv, str) else conv
    except (ValueError, SyntaxError):
        return None


def pairwise_mtbench(rng):
    """MT-Bench human judgments: expert pairwise preference (turn 1 and turn 2)."""
    ds = "lmsys/mt_bench_human_judgments"
    rows = fetch_all(ds, "default", "human")
    label = {"model_a": "BASELINE", "model_b": "CANDIDATE", "tie": "TIE"}
    out = {}
    for turn, name, metric in ((1, "pairwise_mtbench", "judge-eval/pairwise-response-quality"),
                               (2, "pairwise_multiturn", "judge-eval/pairwise-multiturn-quality")):
        pools = {"BASELINE": [], "CANDIDATE": [], "TIE": []}
        seen = set()
        for idx, r in rows:
            if int(r["turn"]) != turn or r["winner"] not in label:
                continue
            key = (r["question_id"], tuple(sorted((r["model_a"], r["model_b"]))))
            if key in seen:  # one judgment per (question, model pair)
                continue
            a, b = _mt_turns(r["conversation_a"]), _mt_turns(r["conversation_b"])
            if not a or not b or len(a) < 2 * turn or len(b) < 2 * turn:
                continue
            seen.add(key)
            pools[label[r["winner"]]].append((idx, r, a, b))
        # keep the human label distribution roughly: 40/40/20
        want = {"BASELINE": 40, "CANDIDATE": 40, "TIE": 20}
        picked = []
        for k in sorted(pools):
            items = sorted(pools[k], key=lambda x: x[0])
            picked += rng.sample(items, min(want[k], len(items)))
        items = []
        for idx, r, a, b in picked:
            q = a[2 * turn - 2]["content"]
            fields = {"question": q, "baseline": a[2 * turn - 1]["content"], "candidate": b[2 * turn - 1]["content"]}
            if turn == 2:
                fields["history"] = f"USER: {a[0]['content']}\n\nASSISTANT (response A): {a[1]['content']}\n\nASSISTANT (response B): {b[1]['content']}"
            if sum(len(v) for v in fields.values()) > 12000:
                continue
            items.append({"id": f"mt{turn}-{idx}", "metric": metric, "tier": label[r["winner"]].lower(), "category": f"mt-bench-turn{turn}",
                          "fields": fields, "expected": label[r["winner"]], "source": f"{ds}#human:{idx}"})
        out[name] = items
    return out, ds


def pairwise_rewardbench(rng):
    """RewardBench (filtered): chosen vs rejected. Position randomized per item."""
    ds = "allenai/reward-bench"
    rows = fetch_all(ds, "default", "filtered")
    groups = {
        "pairwise_instruction_following": (["llmbar-natural", "llmbar-adver-neighbor", "llmbar-adver-GPTInst", "llmbar-adver-GPTOut", "llmbar-adver-manual"],
                                           "judge-eval/pairwise-instruction-following"),
        "pairwise_rewardbench": (["alpacaeval-easy", "alpacaeval-hard", "alpacaeval-length", "mt-bench-hard", "xstest-should-respond",
                                  "xstest-should-refuse", "refusals-dangerous", "donotanswer", "math-prm", "hep-python"],
                                 "judge-eval/pairwise-response-quality"),
    }
    out = {}
    for name, (subsets, metric) in groups.items():
        pools = {s: [] for s in subsets}
        for idx, r in rows:
            if r["subset"] in pools and sum(len(r[k]) for k in ("prompt", "chosen", "rejected")) <= 9000:
                pools[r["subset"]].append((idx, r))
        per = N // len(subsets)
        picked = balanced(rng, pools, per)
        items = []
        for idx, r in picked:
            flip = rng.random() < 0.5
            base, cand = (r["rejected"], r["chosen"]) if flip else (r["chosen"], r["rejected"])
            items.append({"id": f"rb-{idx}", "metric": metric, "tier": r["subset"], "category": name.replace("pairwise_", ""),
                          "fields": {"question": r["prompt"], "baseline": base, "candidate": cand},
                          "expected": "CANDIDATE" if flip else "BASELINE", "source": f"{ds}#filtered:{idx}"})
        out[name] = items
    return out, ds


def likert_helpsteer(rng):
    """HelpSteer2 validation: human helpfulness 0-4."""
    ds = "nvidia/HelpSteer2"
    rows = fetch_all(ds, "default", "validation")
    pools = {str(k): [] for k in range(5)}
    for idx, r in rows:
        if short(r["prompt"], 3000) and short(r["response"], 4000):
            pools[str(int(r["helpfulness"]))].append((idx, r))
    items = []
    for idx, r in balanced(rng, pools, N // 5):
        items.append({"id": f"hs-{idx}", "metric": "judge-eval/helpfulness-score", "tier": f"h{r['helpfulness']}", "category": "helpfulness",
                      "fields": {"prompt": r["prompt"], "response": r["response"]}, "expected": str(int(r["helpfulness"])),
                      "source": f"{ds}#validation:{idx}"})
    return {"likert_helpfulness": items}, ds


def summeval(rng):
    """SummEval: expert coherence / fluency (1-5, mean of 3 experts) + computation references."""
    ds = "mteb/summeval"
    rows = fetch_all(ds, "default", "test")
    cands = []
    for idx, r in rows:
        for j, summ in enumerate(r["machine_summaries"]):
            cands.append((idx, j, r, summ))
    cands.sort(key=lambda x: (x[0], x[1]))

    def strat(dim, metric, name):
        pools = {str(k): [] for k in range(1, 6)}
        for idx, j, r, summ in cands:
            v = r[dim][j]
            pools[str(min(5, max(1, int(round(v)))))].append(((idx, j), (r, summ, v)))
        out = []
        for key in sorted(pools):
            items = pools[key]
            for (idx, j), (r, summ, v) in rng.sample(items, min(N // 5, len(items))):
                fields = {"summary": summ}
                if dim == "coherence":
                    fields["document"] = r["text"]
                out.append({"id": f"se-{dim[:3]}-{idx}-{j}", "metric": metric, "tier": f"r{key}", "category": dim,
                            "fields": fields, "expected": f"{v:.4f}".rstrip("0").rstrip("."), "source": f"{ds}#test:{idx}/summary{j}"})
        return {name: out}

    out = {}
    out.update(strat("coherence", "judge-eval/summary-coherence-score", "likert_coherence"))
    fl = strat("fluency", "judge-eval/prebuilt-fluency", "prebuilt_fluency")
    out.update(fl)
    # prebuilt coherence on the coherence-stratified items (same gold)
    out["prebuilt_coherence"] = [dict(it, metric="judge-eval/prebuilt-coherence", fields={"summary": it["fields"]["summary"]})
                                 for it in out["likert_coherence"]]
    # computation: machine summary vs first human reference summary (parity test, no gold)
    comp = []
    for idx, j, r, summ in rng.sample(cands, N):
        ref = r["human_summaries"][0]
        for metric in ("judge-eval/rouge-lsum", "judge-eval/bleu", "judge-eval/exact-match"):
            comp.append({"id": f"se-{metric.split('/')[1]}-{idx}-{j}", "metric": metric, "tier": metric.split("/")[1], "category": "computation",
                         "fields": {"response": summ, "reference": ref}, "source": f"{ds}#test:{idx}/summary{j}"})
    # Independent reference values (the J0 "gold"): sacrebleu sentence BLEU
    # (13a, effective order, exp smoothing) and Google rouge_score rougeLsum,
    # when those packages are installed. exact_match is trivially known.
    try:
        import sacrebleu  # noqa: PLC0415 - optional
        from rouge_score import rouge_scorer  # noqa: PLC0415 - optional
        scorer = rouge_scorer.RougeScorer(["rougeLsum"], use_stemmer=False)
    except ImportError:
        sacrebleu = scorer = None
    for it in comp:
        resp, ref = it["fields"]["response"], it["fields"]["reference"]
        if it["metric"].endswith("exact-match"):
            it["expected"] = "1" if resp.strip() == ref.strip() else "0"
        elif sacrebleu is not None and it["metric"].endswith("bleu"):
            it["expected"] = f"{sacrebleu.sentence_bleu(resp, [ref]).score / 100:.6f}"
        elif scorer is not None and it["metric"].endswith("rouge-lsum"):
            it["expected"] = f"{scorer.score(ref, resp)['rougeLsum'].fmeasure:.6f}"
    out["computation_text"] = comp
    return out, ds


def computation_tools(rng):
    """Synthetic tool-call / trajectory pairs with exactly known metric values."""
    tools = {
        "get_weather": {"city": ["Paris", "Tokyo", "Lima"], "unit": ["c", "f"]},
        "book_flight": {"origin": ["SFO", "JFK"], "dest": ["LHR", "NRT"], "seats": [1, 2]},
        "search_docs": {"query": ["refund policy", "api limits"], "top_k": [3, 5]},
        "send_email": {"to": ["a@x.com", "b@y.com"], "subject": ["hi", "status"]},
    }

    def call(rng):
        name = rng.choice(sorted(tools))
        args = {k: rng.choice(v) for k, v in tools[name].items()}
        return name, args

    items = []
    for i in range(N // 2):
        name, args = call(rng)
        ref = {"content": "", "tool_calls": [{"name": name, "arguments": args}]}
        mode = i % 5
        if mode == 0:
            pred = ref
        elif mode == 1:  # wrong value
            a2 = dict(args)
            k = sorted(a2)[0]
            a2[k] = "WRONG"
            pred = {"content": "", "tool_calls": [{"name": name, "arguments": a2}]}
        elif mode == 2:  # missing key
            a2 = dict(args)
            a2.pop(sorted(a2)[-1])
            pred = {"content": "", "tool_calls": [{"name": name, "arguments": a2}]}
        elif mode == 3:  # wrong tool
            other = sorted(t for t in tools if t != name)[0]
            pred = {"content": "", "tool_calls": [{"name": other, "arguments": args}]}
        else:  # no call
            pred = {"content": "I cannot do that.", "tool_calls": []}
        for metric in ("judge-eval/tool-call-valid", "judge-eval/tool-name-match", "judge-eval/tool-parameter-kv-match"):
            # Vertex tool_call_valid requires a reference (and its score depends on
            # it), so every tool metric receives one.
            fields = {"response": json.dumps(pred), "reference": json.dumps(ref)}
            items.append({"id": f"tool-{metric.split('/')[1]}-{i}", "metric": metric, "tier": ["exact", "wrong_value", "missing_key", "wrong_tool", "no_call"][mode],
                          "category": "tool_call", "fields": fields, "source": f"synthetic:{SEED}:{i}"})
    for i in range(N // 2):
        ref = [dict(zip(("tool_name", "tool_input"), call(rng))) for _ in range(rng.randint(2, 4))]
        mode = i % 5
        if mode == 0:
            pred = list(ref)
        elif mode == 1:
            pred = list(reversed(ref))
        elif mode == 2:
            pred = ref[:-1]
        elif mode == 3:
            pred = ref + [dict(zip(("tool_name", "tool_input"), call(rng)))]
        else:
            pred = [ref[0]] + [dict(zip(("tool_name", "tool_input"), call(rng)))]
        for metric in ("judge-eval/trajectory-exact-match", "judge-eval/trajectory-in-order-match", "judge-eval/trajectory-precision", "judge-eval/trajectory-recall"):
            items.append({"id": f"traj-{metric.split('/')[1]}-{i}", "metric": metric, "tier": ["exact", "reversed", "truncated", "extra_step", "diverged"][mode],
                          "category": "trajectory", "fields": {"response": json.dumps(pred), "reference": json.dumps(ref)}, "source": f"synthetic:{SEED}:{i}"})
    return {"computation_tools": items}, "synthetic"


BUILDERS = [safety_response, toxicity_prompt, groundedness, pairwise_mtbench, pairwise_rewardbench, likert_helpsteer, summeval, computation_tools]


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--only", default="", help="comma-separated builder or suite names")
    ap.add_argument("--check", action="store_true", help="verify rebuilt SHA-256 against manifest.json instead of writing it")
    args = ap.parse_args()
    only = {s for s in args.only.split(",") if s}
    os.makedirs(OUT_DIR, exist_ok=True)
    manifest = {"schema": 1, "seed": SEED, "generated_by": "scripts/judge-eval/build_suites.py", "suites": {}}
    if os.path.exists(MANIFEST):
        with open(MANIFEST) as f:
            manifest = json.load(f)
    mismatches = 0
    for b in BUILDERS:
        if only and b.__name__ not in only and not any(o.startswith(b.__name__.split("_")[0]) for o in only):
            continue
        rng = random.Random(f"{SEED}:{b.__name__}")
        sys.stderr.write(f"building {b.__name__}\n")
        suites, ds = b(rng)
        info = hub_info(ds) if ds != "synthetic" else {"revision": None, "license": "Apache-2.0 (generated)"}
        for name, items in suites.items():
            if only and name not in only and b.__name__ not in only:
                continue
            body = "".join(json.dumps(it, ensure_ascii=False, sort_keys=True) + "\n" for it in items)
            sha = hashlib.sha256(body.encode()).hexdigest()
            labels = {}
            for it in items:
                labels[it.get("expected", "")] = labels.get(it.get("expected", ""), 0) + 1
            entry = {"file": f"suites/{name}.jsonl", "n": len(items), "sha256": sha, "dataset": ds, "revision": info["revision"],
                     "license": info["license"], "builder": b.__name__, "metrics": sorted({it["metric"] for it in items}),
                     "label_counts": labels if len(labels) <= 12 else {"distinct": len(labels)},
                     "rows": [it["source"] for it in items]}
            if args.check:
                old = manifest["suites"].get(name, {})
                status = "ok" if old.get("sha256") == sha else "MISMATCH"
                mismatches += status != "ok"
                print(f"{name}: {status} ({len(items)} items)")
                continue
            with open(os.path.join(OUT_DIR, f"{name}.jsonl"), "w") as f:
                f.write(body)
            manifest["suites"][name] = entry
            print(f"{name}: {len(items)} items, sha256 {sha[:12]}, labels {entry['label_counts']}")
    if not args.check:
        with open(MANIFEST, "w") as f:
            json.dump(manifest, f, indent=1, sort_keys=True)
            f.write("\n")
    return 1 if mismatches else 0


if __name__ == "__main__":
    sys.exit(main())
