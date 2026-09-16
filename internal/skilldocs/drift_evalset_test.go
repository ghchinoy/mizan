// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package skilldocs

// This is the evalset.EvalSetResult drift gate for the run-eval-set skill
// (plugins/mizan-eval/skills/run-eval-set), the sibling of drift_test.go's
// eval.Result gate and drift_results_test.go's results.Result gate. It is
// HERMETIC by construction: rather than shell out to `mizan eval run --set`
// (which would need a manifest, network, and ADC), it constructs a representative
// evalset.EvalSetResult, renders it through the SAME encoding the CLI's `-o json`
// path uses (encoding/json with SetIndent, mirroring cmd/mizan.printJSON, which
// renderScorecard calls for --output json), and asserts KEY-SET EQUALITY
// (path-anchored) against the key set documented in the run-eval-set SKILL.md
// fenced json block.
//
// EvalSetResult and its Members[] / Aggregate carry NO json tags, so they
// serialize in PascalCase; the walker therefore descends into the Members[]
// slice-structs AND into the nested eval.Result (the Result field), whose own
// snake_case tags (rubric_detail/warnings/duration_ns/token_usage) are the only
// tagged keys. A reflection pass over evalset.EvalSetResult additionally proves
// every declared (non-json:"-") field — including omitempty and slice-element
// fields, recursing through both internal/evalset and internal/eval structs — is
// documented. A synthetic-drift test proves the comparison actually catches
// add/remove/rename.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/evalset"
)

// evalSetSkillRelPath is the run-eval-set SKILL.md relative to this test file.
// This package lives at <repo>/internal/skilldocs, so "../.." is the repo root.
var evalSetSkillRelPath = filepath.Join(
	"..", "..",
	"plugins", "mizan-eval", "skills", "run-eval-set", "SKILL.md",
)

// evalSetDriftMarker precedes the fenced json block documenting the
// evalset.EvalSetResult contract in run-eval-set's SKILL.md. Anchoring on an
// explicit marker keeps the gate stable if illustrative json blocks are added
// elsewhere in the doc.
const evalSetDriftMarker = "<!-- drift:evalset.EvalSetResult -->"

// evalSetFreeformPaths are anchored key paths whose CHILDREN are free-form DATA,
// not a declared schema, so the gate treats them as opaque leaves and does not
// descend into them. The embedded eval.Result's CustomOutput is a map[string]any
// (custom_schema / rubric-detail payload): its keys are values, not part of the
// contract.
var evalSetFreeformPaths = map[string]bool{
	"Members.Result.CustomOutput": true,
}

// evalSetSkillPath resolves the SKILL.md path from this test file's own location
// so the gate is independent of the working directory `go test` runs from.
func evalSetSkillPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate SKILL.md")
	}
	return filepath.Join(filepath.Dir(thisFile), evalSetSkillRelPath)
}

// documentedEvalSetShape extracts the evalset.EvalSetResult json block that
// follows the drift marker in the run-eval-set SKILL.md and returns the parsed
// object.
func documentedEvalSetShape(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(evalSetSkillPath(t))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	text := string(raw)

	mi := strings.Index(text, evalSetDriftMarker)
	if mi < 0 {
		t.Fatalf("SKILL.md is missing the %q marker before the evalset.EvalSetResult json block", evalSetDriftMarker)
	}
	m := fenceRe.FindStringSubmatch(text[mi:])
	if m == nil {
		t.Fatalf("no fenced ```json block found after the %q marker in SKILL.md", evalSetDriftMarker)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
		t.Fatalf("documented evalset.EvalSetResult json block is not valid JSON: %v\nblock:\n%s", err, m[1])
	}
	return doc
}

// representativeEvalSetResult builds an evalset.EvalSetResult with EVERY field
// populated — a member whose embedded eval.Result exercises all omitempty keys
// (rubric_detail/warnings/token_usage), and a full Aggregate — so the rendered
// json exposes every key the struct can emit: the maximal contract the SKILL.md
// must mirror exactly.
func representativeEvalSetResult() evalset.EvalSetResult {
	score := float32(4)
	agg := float32(3.67)
	thr := 3.0
	passed := true
	return evalset.EvalSetResult{
		SetID:      "quickstart/answer-quality",
		SetName:    "Answer Quality Suite",
		Version:    "1.0.0",
		AssetClass: "text-answer",
		Members: []evalset.MemberResult{{
			MetricID: "quickstart/response-helpfulness",
			Status:   evalset.OK,
			Weight:   2,
			Required: true,
			Score:    &score,
			Result: eval.Result{
				Score:          &score,
				PairwiseChoice: "",
				Explanation:    "The response satisfies the metric because ...",
				RawOutput:      []string{"..."},
				CustomOutput:   map[string]any{"per_criterion": []any{}},
				RubricDetail:   true,
				Warnings:       []string{"..."},
				Stats: eval.Stats{
					Duration:   9 * time.Millisecond,
					TokenUsage: &eval.TokenUsage{PromptTokens: 10, CandidatesTokens: 20, TotalTokens: 30},
				},
			},
			Error: "",
		}},
		Aggregate: evalset.Aggregate{
			Method:    evalset.AggWeightedMean,
			Score:     &agg,
			Threshold: &thr,
			Passed:    &passed,
			Scored:    2,
			Failed:    0,
		},
		Verdict:   evalset.Passed,
		Gate:      true,
		StartedAt: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC),
		Duration:  1500 * time.Millisecond,
	}
}

// renderEvalSetJSON serializes res exactly as the CLI's `-o json` path does.
// cmd/mizan.printJSON (which renderScorecard calls for --output json) is a thin
// json.Encoder(SetIndent) wrapper, so encoding/json here produces the identical
// KEY SET the CLI emits — which is all this gate compares. (cmd/mizan is package
// main and cannot be imported; the encoder is replicated, not re-invoked.)
func renderEvalSetJSON(t *testing.T, res evalset.EvalSetResult) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		t.Fatalf("encode evalset.EvalSetResult: %v", err)
	}
	var live map[string]any
	if err := json.Unmarshal(buf.Bytes(), &live); err != nil {
		t.Fatalf("re-parse rendered evalset.EvalSetResult json: %v", err)
	}
	return live
}

// collectEvalSetKeys walks a decoded JSON object and returns the set of
// path-anchored keys (e.g. "Members.Result.Stats.duration_ns"). It descends into
// ARRAY elements under the same prefix (so the Members[] slice-struct contributes
// "Members.MetricID", …), which EvalSetResult needs, and never below a freeform
// path; scalars are leaves.
func collectEvalSetKeys(prefix string, v any, out map[string]bool) {
	switch node := v.(type) {
	case map[string]any:
		for k, val := range node {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			out[path] = true
			if evalSetFreeformPaths[path] {
				continue
			}
			collectEvalSetKeys(path, val, out)
		}
	case []any:
		for _, el := range node {
			collectEvalSetKeys(prefix, el, out)
		}
	}
}

// collectEvalSetStructKeys reflects over evalset.EvalSetResult and returns the
// set of path-anchored json key names it declares, skipping json:"-" fields and
// descending through pointers and slices into nested structs from the Mizan
// internal/evalset and internal/eval packages (MemberResult element, the embedded
// eval.Result, Stats, TokenUsage, Aggregate). The single substring check
// "internal/eval" matches BOTH internal/eval and internal/evalset. String kinds
// (MemberStatus, SetVerdict, AggregationMethod), time.Time, and time.Duration are
// scalars and are not descended. This proves completeness: every declared field —
// including omitempty and slice-element fields — must be documented.
func collectEvalSetStructKeys(prefix string, t reflect.Type, out map[string]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := f.Name
		if tag, ok := f.Tag.Lookup("json"); ok {
			first := strings.Split(tag, ",")[0]
			if first == "-" {
				continue
			}
			if first != "" {
				name = first
			}
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		out[path] = true
		if evalSetFreeformPaths[path] {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Slice {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && strings.Contains(ft.PkgPath(), "internal/eval") {
			collectEvalSetStructKeys(path, ft, out)
		}
	}
}

func evalSetKeySet(m map[string]any) map[string]bool {
	out := map[string]bool{}
	collectEvalSetKeys("", m, out)
	return out
}

// TestRunEvalSetDriftKeySetEquality is the primary gate: the evalset.EvalSetResult
// key set documented in run-eval-set's SKILL.md must EQUAL the key set the CLI's
// `-o json` path renders from a representative EvalSetResult. A field added,
// renamed, or retagged on the struct (or its nested types) changes the rendered
// set and fails here until the skill is updated.
func TestRunEvalSetDriftKeySetEquality(t *testing.T) {
	documented := evalSetKeySet(documentedEvalSetShape(t))
	live := evalSetKeySet(renderEvalSetJSON(t, representativeEvalSetResult()))

	if missing := diff(live, documented); len(missing) > 0 {
		t.Errorf("SKILL.md evalset.EvalSetResult block is MISSING keys the CLI emits (drift): %v", missing)
	}
	if extra := diff(documented, live); len(extra) > 0 {
		t.Errorf("SKILL.md evalset.EvalSetResult block documents keys the CLI does NOT emit (drift): %v", extra)
	}
	if !reflect.DeepEqual(sortedKeys(documented), sortedKeys(live)) {
		t.Errorf("documented vs live key-set mismatch:\n documented: %v\n live:       %v",
			sortedKeys(documented), sortedKeys(live))
	}
}

// TestRunEvalSetDriftStructCompleteness proves the documented block covers EVERY
// declared evalset.EvalSetResult field (recursing through pointers and slices into
// the nested internal/evalset and internal/eval structs), including omitempty and
// slice-element fields. This closes the gap where a newly added field would be
// absent from the rendered sample yet must still be documented.
func TestRunEvalSetDriftStructCompleteness(t *testing.T) {
	documented := evalSetKeySet(documentedEvalSetShape(t))
	declared := map[string]bool{}
	collectEvalSetStructKeys("", reflect.TypeOf(evalset.EvalSetResult{}), declared)

	if missing := diff(declared, documented); len(missing) > 0 {
		t.Errorf("evalset.EvalSetResult declares json fields NOT documented in SKILL.md (drift): %v\n"+
			"declared: %v\ndocumented: %v", missing, sortedKeys(declared), sortedKeys(documented))
	}
}

// TestRunEvalSetDriftAppliedNeverSerialized guards the specific invariant that the
// embedded eval.Result's resolved autorater (Applied, json:"-") is never part of
// the -o json contract, so the skill must not tell an agent to read it.
func TestRunEvalSetDriftAppliedNeverSerialized(t *testing.T) {
	live := evalSetKeySet(renderEvalSetJSON(t, representativeEvalSetResult()))
	if live["Members.Result.Applied"] {
		t.Error("eval.Result serialized an 'Applied' key under Members.Result; it must stay json:\"-\"")
	}
	documented := evalSetKeySet(documentedEvalSetShape(t))
	if documented["Members.Result.Applied"] {
		t.Error("SKILL.md documents a 'Members.Result.Applied' key; Applied is json:\"-\" and never emitted")
	}
}

// TestRunEvalSetDriftCatchesSyntheticDrift proves the comparison logic actually
// catches drift: starting from the live key set, an added key, a removed key, and
// a renamed key must each be flagged by diff() against the documented set. This
// guards the gate itself so a future refactor cannot silently neuter it.
func TestRunEvalSetDriftCatchesSyntheticDrift(t *testing.T) {
	documented := evalSetKeySet(documentedEvalSetShape(t))
	live := evalSetKeySet(renderEvalSetJSON(t, representativeEvalSetResult()))

	// Baseline: the real sets must agree (no drift today).
	if len(diff(live, documented)) != 0 || len(diff(documented, live)) != 0 {
		t.Fatalf("baseline expected equal key-sets before synthetic mutation")
	}

	// Added field on the CLI side must be reported as MISSING from the doc.
	added := clone(live)
	added["Aggregate.NewField"] = true
	if got := diff(added, documented); len(got) != 1 || got[0] != "Aggregate.NewField" {
		t.Errorf("added-field drift not caught: %v", got)
	}

	// Removed field from the CLI side must be reported as EXTRA in the doc.
	removed := clone(live)
	delete(removed, "Verdict")
	if got := diff(documented, removed); len(got) != 1 || got[0] != "Verdict" {
		t.Errorf("removed-field drift not caught: %v", got)
	}

	// Renamed field must be caught on BOTH sides (old extra in doc, new missing).
	renamed := clone(live)
	delete(renamed, "SetID")
	renamed["SetIdentifier"] = true
	if got := diff(documented, renamed); len(got) != 1 || got[0] != "SetID" {
		t.Errorf("renamed-field (old name) drift not caught: %v", got)
	}
	if got := diff(renamed, documented); len(got) != 1 || got[0] != "SetIdentifier" {
		t.Errorf("renamed-field (new name) drift not caught: %v", got)
	}
}
