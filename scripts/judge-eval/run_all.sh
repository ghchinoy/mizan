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
# Experiment 07 runner: every judge configuration on every suite, one session.
#
#   J0  computation, no model (local Go vs Vertex EvaluateInstances)
#   J1  DiffusionGemma, single read, Vertex AI dedicated endpoint (G4 RTX PRO 6000)
#   J1c DiffusionGemma, single read, Cloud Run RTX PRO 6000 (through a dgem gateway, pinned)
#   J2  DiffusionGemma with swapped-order mirror (pairwise suites; paired against J1 in one run)
#   J3  gemini-3.5-flash-lite (structured output / native EvaluateInstances)
#   J4  gemini-3.8-flash
#   J5  entropy cascade J1 -> J4 (computed offline by analyze.py from the J1/J4 run)
#   J6  Vertex prebuilt metrics (EvaluateInstances; judge = service default)
#
# Required environment:
#   MIZAN_PROJECT_ID      GCP project for Vertex calls
#   DGEM_VERTEX_ENDPOINT  Vertex dedicated endpoint URL (…/endpoints/<id>)
#   DGEM_GATEWAY_URL      dgem gateway base URL (…/v1) for the Cloud Run backend
# Optional: MIZAN (binary, default ./bin/mizan), WORKERS, SUITES (space list), TRACK (all|fast|slow).
set -euo pipefail
cd "$(dirname "$0")/../.."
MIZAN=${MIZAN:-./bin/mizan}
WORKERS=${WORKERS:-4}  # keep modest: a shared project with saturated Gemini quota queues requests for minutes
S=docs/experiments/judge-eval/suites
R=${RUN_DIR:-docs/experiments/judge-eval/runs/$(date -u +%Y%m%d)}
mkdir -p "$R"
: "${DGEM_VERTEX_ENDPOINT:?set DGEM_VERTEX_ENDPOINT}" "${DGEM_GATEWAY_URL:?set DGEM_GATEWAY_URL}" "${MIZAN_PROJECT_ID:?set MIZAN_PROJECT_ID}"

JUDGE_SUITES=${SUITES:-"safety_response toxicity_prompt groundedness pairwise_mtbench pairwise_multiturn pairwise_instruction_following pairwise_rewardbench likert_helpfulness likert_coherence"}
PREBUILT_SUITES="prebuilt_safety prebuilt_groundedness prebuilt_fluency prebuilt_coherence"

# TRACK=fast runs everything except gemini-3.8-flash; TRACK=slow runs only the
# gemini-3.8-flash comparisons (that model queued requests for 90-150 s under
# concurrent load on 2026-09-26, while flash-lite / 3.7-flash stayed < 2 s).
# Run both tracks at once to overlap them; unset runs everything in order.
TRACK=${TRACK:-all}

# Record exactly what was tested: serving health (version, revision, vLLM commit)
# for each diffusion backend, once per run directory. Needs gcloud-free ADC via
# python3 (same refresh-token exchange mizan uses).
if [[ ! -s "$R/backends.json" ]]; then
  python3 - "$R/backends.json" <<'PY' || echo "warn: backend health capture failed"
import json, os, sys, time, urllib.parse, urllib.request
c = json.load(open(os.environ.get("GOOGLE_APPLICATION_CREDENTIALS") or os.path.expanduser("~/.config/gcloud/application_default_credentials.json")))
tok = json.load(urllib.request.urlopen("https://oauth2.googleapis.com/token", urllib.parse.urlencode(
    {k: c[k] for k in ("client_id", "client_secret", "refresh_token")} | {"grant_type": "refresh_token"}).encode()))
def get(url, bearer):
    try:
        req = urllib.request.Request(url, headers={"Authorization": "Bearer " + bearer})
        return json.load(urllib.request.urlopen(req, timeout=30))
    except Exception as e:  # noqa: BLE001
        return {"error": str(e)}
keep = ("status", "server", "version", "revision", "vllm_commit", "vllm_ready", "warmed")
v = get(os.environ["DGEM_VERTEX_ENDPOINT"].rstrip("/") + "/invoke/health", tok["access_token"])
gw = os.environ["DGEM_GATEWAY_URL"].rstrip("/").removesuffix("/v1")
cr = get(gw + "/api/status?backend=cloudrun", tok["id_token"])
out = {"captured_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
       "vertex_dedicated": {k: v.get(k) for k in keep if k in v} or v,
       "cloudrun_via_gateway": {k: cr.get(k) for k in ("status", "gpu_hardware", "model") if k in cr} or cr}
json.dump(out, open(sys.argv[1], "w"), indent=1)
print("backends:", json.dumps(out))
PY
fi
cmp() { # name suite-or-path extra-args...
  local name=$1 suite=$2; shift 2
  local slow=0; [[ " $* " == *" gemini-3.8-flash "* ]] && slow=1
  if [[ $TRACK == fast && $slow == 1 ]] || [[ $TRACK == slow && $slow == 0 ]]; then return; fi
  if [[ -s "$R/$name.json" && -z "${FORCE:-}" ]]; then echo "skip $name (exists)"; return; fi
  echo "=== $name ($(date -u +%H:%M:%S))"
  local ds="$S/$suite.jsonl"; [[ -f "$suite" ]] && ds="$suite"
  "$MIZAN" eval compare-engines --dataset "$ds" --workers "$WORKERS" --no-store \
    --output-file "$R/$name.json" "$@" > "$R/$name.txt" 2> "$R/$name.log" || echo "!! $name failed (see $R/$name.log)"
  sed -n '5,8p' "$R/$name.txt" || true
}

tolerance_for() { case $1 in likert_*|prebuilt_fluency|prebuilt_coherence) echo 0.5;; *) echo 0.001;; esac; }

# J0: no autorater.
cmp j0_computation_text  computation_text  --engine-a local --engine-b vertex --score-tolerance 0.0051 --notes "J0 text computation"
cmp j0_computation_tools computation_tools --engine-a local --engine-b vertex --score-tolerance 0.0051 --notes "J0 tool/trajectory computation"

for s in $JUDGE_SUITES; do
  tol=$(tolerance_for "$s")
  # J4 vs J1: gemini-3.8-flash vs DiffusionGemma on the Vertex G4 endpoint.
  cmp "j4_vs_j1_$s" "$s" --engine-a vertex --model-a gemini-3.8-flash --engine-b diffusion \
      --diffusion-endpoint "$DGEM_VERTEX_ENDPOINT" --score-tolerance "$tol" --notes "J4 gemini-3.8-flash vs J1 dgem G4"
  # J3 vs J1c: gemini-3.5-flash-lite vs DiffusionGemma on Cloud Run (gateway, pinned backend).
  cmp "j3_vs_j1c_$s" "$s" --engine-a vertex --model-a gemini-3.5-flash-lite --engine-b diffusion \
      --diffusion-endpoint "$DGEM_GATEWAY_URL" --diffusion-backend cloudrun --score-tolerance "$tol" --notes "J3 gemini-3.5-flash-lite vs J1c dgem Cloud Run"
  if [[ $s == pairwise_* ]]; then
    # J1 (repeat) vs J2: DiffusionGemma G4 without and with the swapped-order mirror.
    cmp "j1_vs_j2_$s" "$s" --engine-a diffusion --engine-b diffusion --diffusion-endpoint "$DGEM_VERTEX_ENDPOINT" \
        --diffusion-mirror --diffusion-mirror-side b --notes "J1 dgem G4 vs J2 dgem G4 mirror"
  fi
done

for s in $PREBUILT_SUITES; do
  tol=$(tolerance_for "$s")
  cmp "j6_vs_j1_$s" "$s" --engine-a vertex --engine-b diffusion --diffusion-endpoint "$DGEM_VERTEX_ENDPOINT" \
      --score-tolerance "$tol" --notes "J6 Vertex prebuilt (service judge) vs J1 dgem G4 prebuilt mapping"
done
# Latency probe: serial (one request in flight, engines one after the other) on
# 30 items so queueing and client contention do not distort p50/p95. Accuracy
# numbers come from the runs above; these runs are only for latency.
head -30 "$S/safety_response.jsonl" > "$R/latency_probe.$TRACK.jsonl.in"
WORKERS=1 cmp lat_j4_vs_j1 "$R/latency_probe.$TRACK.jsonl.in" --serial --engine-a vertex --model-a gemini-3.8-flash \
    --engine-b diffusion --diffusion-endpoint "$DGEM_VERTEX_ENDPOINT" --notes "serial latency probe"
WORKERS=1 cmp lat_j3_vs_j1c "$R/latency_probe.$TRACK.jsonl.in" --serial --engine-a vertex --model-a gemini-3.5-flash-lite \
    --engine-b diffusion --diffusion-endpoint "$DGEM_GATEWAY_URL" --diffusion-backend cloudrun --notes "serial latency probe"
# Gateway overhead: dgem through the gateway with vertex_first routing (compare its
# p50 with the direct-endpoint dgem side of lat_j4_vs_j1).
WORKERS=1 cmp lat_j1g "$R/latency_probe.$TRACK.jsonl.in" --serial --engine-a diffusion --engine-b diffusion \
    --diffusion-endpoint "$DGEM_GATEWAY_URL" --diffusion-backend vertex_first --notes "serial latency probe: dgem via gateway vertex_first (both sides)"
rm -f "$R/latency_probe.$TRACK.jsonl.in"
echo "done $(date -u +%H:%M:%S)"
