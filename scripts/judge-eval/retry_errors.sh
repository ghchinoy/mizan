#!/usr/bin/env bash
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
#
# Re-run BOTH engines on only the items where either engine errored in a run,
# writing <run>.retryN.json; analyze.py overlays those cases on the original.
# Engines, models, endpoint, tolerance and mirror settings are read back from
# the original report's metadata, so the retry uses the same configuration.
#
#   scripts/judge-eval/retry_errors.sh docs/experiments/judge-eval/runs/j4_vs_j1_groundedness.json [...]
set -euo pipefail
cd "$(dirname "$0")/../.."
MIZAN=${MIZAN:-./bin/mizan}
for run in "$@"; do
  base=${run%.json}
  n=1; while [[ -e "$base.retry$n.json" ]]; do n=$((n + 1)); done
  sub="$base.retry$n.jsonl.in"
  args=$(python3 - "$run" "$sub" <<'EOF'
import json, shlex, sys
run, sub = sys.argv[1], sys.argv[2]
rep = json.load(open(run))
m = rep["meta"]
bad = {c["id"] for c in rep["cases"] if c["comparison"]["engine_a"].get("error") or c["comparison"]["engine_b"].get("error")}
for rp in sorted(__import__("glob").glob(run[:-5] + ".retry*.json")):  # skip items fixed by earlier retries
    for c in json.load(open(rp))["cases"]:
        if not (c["comparison"]["engine_a"].get("error") or c["comparison"]["engine_b"].get("error")):
            bad.discard(c["id"])
lines = [l for l in open(m["dataset"]) if l.strip() and json.loads(l).get("id") in bad]
open(sub, "w").writelines(lines)
a = ["--engine-a", m["engine_a"], "--engine-b", m["engine_b"], "--score-tolerance", str(m["score_tolerance"]), "--notes", "retry of errored items: " + m.get("notes", "")]
if m.get("model_a"): a += ["--model-a", m["model_a"]]
if m.get("model_b"): a += ["--model-b", m["model_b"]]
if m.get("diffusion_mirror"): a += ["--diffusion-mirror", "--diffusion-mirror-side", m.get("diffusion_mirror_side") or "both"]
if m.get("diffusion_backend"): a += ["--diffusion-backend", m["diffusion_backend"]]
if m.get("serial_engines"): a += ["--serial"]
be = m.get("diffusion_endpoint", "")
if "diffusion" in (m["engine_a"], m["engine_b"]):
    import os
    url = {"vertex-dedicated": os.environ.get("DGEM_VERTEX_ENDPOINT"), "gateway": os.environ.get("DGEM_GATEWAY_URL")}.get(be, be)
    if not url:
        sys.exit(f"cannot resolve diffusion endpoint {be!r}: set DGEM_VERTEX_ENDPOINT / DGEM_GATEWAY_URL")
    a += ["--diffusion-endpoint", url]
print(len(lines), " ".join(shlex.quote(x) for x in a))
EOF
)
  count=${args%% *}; flags=${args#* }
  if [[ $count == 0 ]]; then echo "$run: nothing to retry"; rm -f "$sub"; continue; fi
  echo "$run: retrying $count item(s) -> $base.retry$n.json"
  eval "$MIZAN" eval compare-engines --dataset "$sub" --workers "${WORKERS:-2}" --no-store --output-file "$base.retry$n.json" $flags \
    > "$base.retry$n.txt" 2> "$base.retry$n.log" || echo "!! retry failed for $run"
  sed -n '6,7p' "$base.retry$n.txt" || true
  rm -f "$sub"
done
