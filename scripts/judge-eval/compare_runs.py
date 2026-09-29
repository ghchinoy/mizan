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
"""Compare the same engine side across two run directories (e.g. two serving images).

For every run file present in both directories, and each engine side (a/b),
pairs items by id (after overlaying retries, as analyze.py does) and reports
accuracy in each directory, items right only in OLD / only in NEW, and the exact
McNemar p-value. Use it to answer "did the new image regress?" on identical items.

  python3 scripts/judge-eval/compare_runs.py OLD_DIR NEW_DIR [--side-filter dgem] > out.md
"""

import argparse
import glob
import json
import math
import os
import sys

sys.path.insert(0, os.path.dirname(__file__))
from analyze import load_merged, side_label  # noqa: E402


def mcnemar(b, c):
    n = b + c
    if n == 0:
        return 1.0
    return min(1.0, 2 * sum(math.comb(n, i) for i in range(min(b, c) + 1)) / 2 ** n)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("old")
    ap.add_argument("new")
    ap.add_argument("--side-filter", default="", help="only sides whose label contains this string (e.g. dgem, vertex[)")
    a = ap.parse_args()
    print(f"| Run | Engine | n paired | Acc {os.path.basename(a.old.rstrip('/'))} | Acc {os.path.basename(a.new.rstrip('/'))} | only old right | only new right | McNemar p |")
    print("|---|---|---|---|---|---|---|---|")
    tot = {}
    for newp in sorted(glob.glob(os.path.join(a.new, "*.json"))):
        name = os.path.basename(newp)
        if ".retry" in name or name.startswith("summary") or name == "backends.json":
            continue
        oldp = os.path.join(a.old, name)
        if not os.path.exists(oldp):
            continue
        old, _, _ = load_merged(oldp)
        new, _, _ = load_merged(newp)
        om = {c["id"]: c for c in old["cases"]}
        for side in ("a", "b"):
            label = side_label(new, side)
            if a.side_filter and a.side_filter not in label:
                continue
            o_ok = n_ok = only_o = only_n = n = 0
            for c in new["cases"]:
                oc = om.get(c["id"])
                if not oc:
                    continue
                ro, rn = oc["comparison"][f"engine_{side}"], c["comparison"][f"engine_{side}"]
                if ro.get("correct") is None or rn.get("correct") is None:
                    continue
                n += 1
                o_ok += ro["correct"]
                n_ok += rn["correct"]
                only_o += ro["correct"] and not rn["correct"]
                only_n += rn["correct"] and not ro["correct"]
            if not n:
                continue
            t = tot.setdefault(label, [0, 0, 0, 0, 0])
            for i, v in enumerate((n, o_ok, n_ok, only_o, only_n)):
                t[i] += v
            print(f"| {name[:-5]} | {label} | {n} | {100 * o_ok / n:.1f} | {100 * n_ok / n:.1f} | {only_o} | {only_n} | {mcnemar(only_o, only_n):.3g} |")
    print("\n| Engine (pooled over runs) | n paired | Acc old | Acc new | only old | only new | McNemar p |")
    print("|---|---|---|---|---|---|---|")
    for k, (n, o, nn, oo, on) in sorted(tot.items()):
        print(f"| {k} | {n} | {100 * o / n:.1f} | {100 * nn / n:.1f} | {oo} | {on} | {mcnemar(oo, on):.3g} |")


if __name__ == "__main__":
    main()
