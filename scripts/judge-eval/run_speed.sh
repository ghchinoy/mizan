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
# Experiment 07b runner (after sweep.py): Gemini thinking-budget A/B and the
# live entropy cascade.
#
#   T*  same Gemini model on both sides; side B has --thinking-budget 0. Pairwise
#       suites go through genai structured output on BOTH sides (--pairwise-genai)
#       because native EvaluateInstances has no thinking control.
#   C*  engine "cascade": dgem (Vertex G4) first, gemini-3.8-flash only when
#       hesitation >= 0.35; served live, serial and at 8 workers.
set -euo pipefail
cd "$(dirname "$0")/../.."
MIZAN=${MIZAN:-./bin/mizan}
S=docs/experiments/judge-eval/suites
R=${RUN_DIR:?set RUN_DIR}
mkdir -p "$R"
: "${DGEM_VERTEX_ENDPOINT:?}" "${MIZAN_PROJECT_ID:?}"

cmp() { # name suite workers extra...
  local name=$1 suite=$2 w=$3; shift 3
  if [[ -s "$R/$name.json" && -z "${FORCE:-}" ]]; then echo "skip $name"; return; fi
  echo "=== $name ($(date -u +%H:%M:%S))"
  "$MIZAN" eval compare-engines --dataset "$S/$suite.jsonl" --workers "$w" --no-store --output-file "$R/$name.json" "$@" \
    > "$R/$name.txt" 2> "$R/$name.log" || echo "!! $name failed"
  sed -n '5,8p' "$R/$name.txt" || true
}

if [[ ${PART:-all} != cascade ]]; then
  for m in gemini-3.8-flash gemini-3.5-flash-lite; do
    for s in toxicity_prompt groundedness; do
      cmp "t_${m}_${s}" "$s" 3 --engine-a vertex --model-a "$m" --engine-b vertex --model-b "$m" \
          --thinking-budget 0 --thinking-side b --score-tolerance 0.001 --notes "thinking default (A) vs budget 0 (B)"
    done
    for s in pairwise_instruction_following pairwise_mtbench; do
      cmp "t_${m}_${s}" "$s" 3 --engine-a vertex --model-a "$m" --engine-b vertex --model-b "$m" --pairwise-genai \
          --thinking-budget 0 --thinking-side b --notes "genai pairwise: thinking default (A) vs budget 0 (B)"
    done
  done
fi

if [[ ${PART:-all} != thinking ]]; then
  for s in safety_response groundedness toxicity_prompt pairwise_rewardbench; do
    cmp "c_serial_$s" "$s" 1 --serial --engine-a cascade --engine-b none --diffusion-endpoint "$DGEM_VERTEX_ENDPOINT" \
        --cascade-model gemini-3.8-flash --cascade-threshold 0.35 --score-tolerance 0.001 --notes "live cascade, serial"
    cmp "c_w8_$s" "$s" 8 --engine-a cascade --engine-b none --diffusion-endpoint "$DGEM_VERTEX_ENDPOINT" \
        --cascade-model gemini-3.8-flash --cascade-threshold 0.35 --score-tolerance 0.001 --notes "live cascade, 8 workers"
  done
fi
echo "done $(date -u +%H:%M:%S)"
