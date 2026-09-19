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

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/eval"
)

// runEvalSkillPath is the run-eval SKILL.md relative to this test file. This
// package lives at <repo>/internal/skilldocs, so "../.." is the repo root.
var runEvalSkillRelPath = filepath.Join(
	"..", "..",
	"plugins", "mizan-eval", "skills", "run-eval", "SKILL.md",
)

// driftMarker precedes the fenced json block that documents the eval.Result
// contract in SKILL.md. Anchoring on an explicit marker (rather than "the first
// json block") keeps the gate stable if illustrative json blocks are added.
const driftMarker = "<!-- drift:eval.Result -->"

// freeformPaths are anchored key paths whose CHILDREN are free-form DATA, not a
// declared schema, so the gate treats them as opaque leaves and does not descend
// into them. CustomOutput is a map[string]any (custom_schema / rubric-detail
// payload): its keys are values, not part of eval.Result's contract.
var freeformPaths = map[string]bool{
	"CustomOutput": true,
}

// fenceRe matches a fenced ```json block. It does not anchor the fence at column
// 0 so an indented fence (inside a list item) is still seen.
var fenceRe = regexp.MustCompile("(?ms)^[ \t]*```json[ \t]*\r?\n(.*?)^[ \t]*```")

// skillPath resolves the SKILL.md path from this test file's own location so the
// gate is independent of the working directory `go test` is invoked from.
func skillPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate SKILL.md")
	}
	return filepath.Join(filepath.Dir(thisFile), runEvalSkillRelPath)
}

// documentedResultShape extracts the eval.Result json block that follows the
// drift marker in SKILL.md and returns the parsed object.
func documentedResultShape(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(skillPath(t))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	text := string(raw)

	mi := strings.Index(text, driftMarker)
	if mi < 0 {
		t.Fatalf("SKILL.md is missing the %q marker before the eval.Result json block", driftMarker)
	}
	m := fenceRe.FindStringSubmatch(text[mi:])
	if m == nil {
		t.Fatalf("no fenced ```json block found after the %q marker in SKILL.md", driftMarker)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
		t.Fatalf("documented eval.Result json block is not valid JSON: %v\nblock:\n%s", err, m[1])
	}
	return doc
}

// representativeResult builds an eval.Result with EVERY field populated so that
// the rendered json exposes all keys the struct can emit (including the
// omitempty rubric_detail / warnings / token_usage keys). Populating all fields
// is what makes the render-based key-set the maximal contract the SKILL.md must
// mirror exactly.
func representativeResult() eval.Result {
	score := float32(4)
	passed := true
	confidence := float32(0.9)
	return eval.Result{
		Score:           &score,
		Passed:          &passed,
		Confidence:      &confidence,
		ChoiceSelection: "CANDIDATE",
		PairwiseChoice:  "CANDIDATE",
		Explanation:     "The response satisfies the metric because ...",
		RawOutput:       []string{"..."},
		CustomOutput:    map[string]any{"example": true},
		RubricDetail:    true,
		Warnings:        []string{"..."},
		Stats: eval.Stats{
			Duration:   9 * time.Millisecond,
			TokenUsage: &eval.TokenUsage{PromptTokens: 10, CandidatesTokens: 20, TotalTokens: 30},
		},
	}
}

// renderResultJSON serializes res exactly as the CLI's `-o json` path does.
// cmd/mizan.printJSON is a thin json.Encoder(SetIndent) wrapper, so encoding/json
// here produces the identical KEY SET the CLI emits — which is all this gate
// compares. (cmd/mizan is package main and cannot be imported; the encoder is
// replicated, not re-invoked.)
func renderResultJSON(t *testing.T, res eval.Result) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		t.Fatalf("encode eval.Result: %v", err)
	}
	var live map[string]any
	if err := json.Unmarshal(buf.Bytes(), &live); err != nil {
		t.Fatalf("re-parse rendered eval.Result json: %v", err)
	}
	return live
}

// collectKeys walks a decoded JSON object and returns the set of path-anchored
// keys (e.g. "Stats.token_usage.prompt_tokens"). It descends only into objects,
// and never below a freeform path. Arrays and scalars are leaves.
func collectKeys(prefix string, v any, out map[string]bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	for k, val := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		out[path] = true
		if freeformPaths[path] {
			continue
		}
		collectKeys(path, val, out)
	}
}

// structKeys reflects over a struct type and returns the set of path-anchored
// json key names it declares, skipping json:"-" fields and descending into
// nested structs from the same package (Stats, TokenUsage) but not into maps or
// slices (which are free-form or scalar leaves). This proves completeness: every
// declared field must be documented, including omitempty fields the rendered
// sample might not exercise.
func structKeys(prefix string, t reflect.Type, out map[string]bool) {
	if t.Kind() == reflect.Pointer {
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
		if freeformPaths[path] {
			continue
		}
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && strings.Contains(ft.PkgPath(), "internal/eval") {
			structKeys(path, ft, out)
		}
	}
}

func keySet(m map[string]any) map[string]bool {
	out := map[string]bool{}
	collectKeys("", m, out)
	return out
}

func sortedKeys(s map[string]bool) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// diff returns keys in a but not in b.
func diff(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// TestRunEvalDriftKeySetEquality is the primary gate: the eval.Result key set
// documented in SKILL.md must EQUAL the key set the CLI's `-o json` path renders
// from a representative eval.Result. A field added, renamed, or retagged on the
// struct changes the rendered set and fails here until the skill is updated —
// the drift-catch binder's plugindocs test provides for its CLI.
func TestRunEvalDriftKeySetEquality(t *testing.T) {
	documented := keySet(documentedResultShape(t))
	live := keySet(renderResultJSON(t, representativeResult()))

	if missing := diff(live, documented); len(missing) > 0 {
		t.Errorf("SKILL.md eval.Result block is MISSING keys the CLI emits (drift): %v", missing)
	}
	if extra := diff(documented, live); len(extra) > 0 {
		t.Errorf("SKILL.md eval.Result block documents keys the CLI does NOT emit (drift): %v", extra)
	}
	if !reflect.DeepEqual(sortedKeys(documented), sortedKeys(live)) {
		t.Errorf("documented vs live key-set mismatch:\n documented: %v\n live:       %v",
			sortedKeys(documented), sortedKeys(live))
	}
}

// TestRunEvalDriftStructCompleteness proves the documented block covers EVERY
// declared eval.Result field (recursing into Stats/TokenUsage), including
// omitempty fields. This closes the gap where a newly added omitempty field
// would be absent from the rendered sample yet must still be documented.
func TestRunEvalDriftStructCompleteness(t *testing.T) {
	documented := keySet(documentedResultShape(t))
	declared := map[string]bool{}
	structKeys("", reflect.TypeOf(eval.Result{}), declared)

	if missing := diff(declared, documented); len(missing) > 0 {
		t.Errorf("eval.Result declares json fields NOT documented in SKILL.md (drift): %v\n"+
			"declared: %v\ndocumented: %v", missing, sortedKeys(declared), sortedKeys(documented))
	}
}

// TestRunEvalDriftAppliedNeverSerialized guards the specific invariant that the
// resolved autorater (Applied, json:"-") is never part of the -o json contract,
// so the skill must not tell an agent to read it.
func TestRunEvalDriftAppliedNeverSerialized(t *testing.T) {
	live := keySet(renderResultJSON(t, representativeResult()))
	if live["Applied"] {
		t.Error("eval.Result serialized an 'Applied' key; it must stay json:\"-\" (never in -o json)")
	}
	documented := keySet(documentedResultShape(t))
	if documented["Applied"] {
		t.Error("SKILL.md documents an 'Applied' key; Applied is json:\"-\" and never emitted")
	}
}
