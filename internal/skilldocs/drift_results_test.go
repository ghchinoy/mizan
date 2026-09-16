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

// This is the results.Result drift gate for the report-to-html skill
// (plugins/mizan-results), the sibling of drift_test.go's eval.Result gate for
// run-eval. It is HERMETIC by construction: `mizan results list -o json` reads a
// local store and needs no network or ADC, but rather than shell out this test
// constructs a representative results.Result, renders it through the SAME
// encoding the CLI's `-o json` path uses (encoding/json with SetIndent, mirroring
// cmd/mizan.printJSON), and asserts KEY-SET EQUALITY (path-anchored) against the
// key set documented in the report-to-html SKILL.md fenced json block. A
// reflection pass over results.Result additionally proves every declared
// (non-json:"-") field — including omitempty and slice-element struct fields — is
// documented, so even an omitempty field added to the struct fails the gate until
// the skill is updated. A synthetic-drift test proves the comparison actually
// catches add/remove/rename.

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

	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/results"
)

// reportSkillRelPath is the report-to-html SKILL.md relative to this test file.
// This package lives at <repo>/internal/skilldocs, so "../.." is the repo root.
var reportSkillRelPath = filepath.Join(
	"..", "..",
	"plugins", "mizan-results", "skills", "report-to-html", "SKILL.md",
)

// resultDriftMarker precedes the fenced json block documenting the results.Result
// contract in report-to-html's SKILL.md. Anchoring on an explicit marker keeps
// the gate stable if illustrative json blocks are added elsewhere in the doc.
const resultDriftMarker = "<!-- drift:results.Result -->"

// resultFreeformPaths are anchored key paths whose CHILDREN are free-form DATA,
// not a declared schema, so the gate treats them as opaque leaves and does not
// descend into them. Outcome.CustomOutput is a map[string]any (custom_schema /
// rubric-detail payload): its keys are values, not part of results.Result's
// contract.
var resultFreeformPaths = map[string]bool{
	"Outcome.CustomOutput": true,
}

// reportSkillPath resolves the SKILL.md path from this test file's own location
// so the gate is independent of the working directory `go test` runs from.
func reportSkillPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate SKILL.md")
	}
	return filepath.Join(filepath.Dir(thisFile), reportSkillRelPath)
}

// documentedResultResultShape extracts the results.Result json block that follows
// the drift marker in the report-to-html SKILL.md and returns the parsed object.
func documentedResultResultShape(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(reportSkillPath(t))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	text := string(raw)

	mi := strings.Index(text, resultDriftMarker)
	if mi < 0 {
		t.Fatalf("SKILL.md is missing the %q marker before the results.Result json block", resultDriftMarker)
	}
	m := fenceRe.FindStringSubmatch(text[mi:])
	if m == nil {
		t.Fatalf("no fenced ```json block found after the %q marker in SKILL.md", resultDriftMarker)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
		t.Fatalf("documented results.Result json block is not valid JSON: %v\nblock:\n%s", err, m[1])
	}
	return doc
}

// representativeResult builds a results.Result with EVERY field populated (a
// non-nil Rubric, a non-empty Inputs slice, all omitempty Outcome fields set) so
// the rendered json exposes every key the struct can emit — the maximal contract
// the SKILL.md must mirror exactly.
func representativeStoredResult() results.Result {
	score := float32(4)
	smin, smax := 1, 5
	return results.Result{
		RunID:   "01JABCDEF0123456789ABCDEFG",
		RunAt:   time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC),
		RunKind: results.RunKindSingle,
		Mizan:   results.MizanBuild{Version: "v0.1.0", Commit: "abcdef0", Date: "2026-09-16"},
		Invocation: results.Invocation{
			Command: "eval run", ProjectID: "my-project", Location: "us-central1",
			HostLabel: "workstation", Actor: "alice",
		},
		Template: results.TemplateRef{
			ID: "brand/tone", Version: "1.0.0", ContentHash: "sha256:...",
			Kind: registry.MetricKind("rubric"), Source: "pack:google-brand@origin",
		},
		Autorater: results.AppliedAutorater{
			Model: "gemini-2.5-pro", SamplingCount: 1, FlipEnabled: true,
			EffectiveHost: "global", Location: "global", ModelSource: "flag",
		},
		Rubric: &results.RubricRef{
			Method: "authored", GeneratorModel: "gemini-2.5-pro", Recipe: "general_quality_v1",
			Origins: []string{"authored"}, ScaleMin: &smin, ScaleMax: &smax, DetailMode: true,
		},
		Inputs: []results.StoredInput{{
			Field: "response", Modality: registry.Modality("text"), ContentHash: "sha256:...",
			Mode: results.ModeInline, Inline: "the response text",
			URI: "gs://bucket/object", MimeType: "text/plain",
		}},
		Outcome: results.Outcome{
			Score: &score, PairwiseChoice: "CANDIDATE",
			Explanation:  "The response satisfies the metric because ...",
			CustomOutput: map[string]any{"per_criterion": []any{}},
			RubricDetail: true, Warnings: []string{"..."}, DurationNS: 9000000,
			TokenUsage: &results.TokenUsage{PromptTokens: 10, CandidatesTokens: 20, TotalTokens: 30},
		},
	}
}

// renderResultResultJSON serializes res exactly as the CLI's `-o json` path does.
// cmd/mizan.printJSON is a thin json.Encoder(SetIndent) wrapper, so encoding/json
// here produces the identical KEY SET the CLI emits — which is all this gate
// compares. (cmd/mizan is package main and cannot be imported; the encoder is
// replicated, not re-invoked.)
func renderResultResultJSON(t *testing.T, res results.Result) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		t.Fatalf("encode results.Result: %v", err)
	}
	var live map[string]any
	if err := json.Unmarshal(buf.Bytes(), &live); err != nil {
		t.Fatalf("re-parse rendered results.Result json: %v", err)
	}
	return live
}

// collectResultKeys walks a decoded JSON object and returns the set of
// path-anchored keys (e.g. "Outcome.TokenUsage.PromptTokens"). Unlike the
// eval.Result gate it descends into ARRAY elements under the same prefix (so a
// slice-of-struct field like Inputs[] contributes "Inputs.Field", …), which
// results.Result needs. It never descends below a freeform path; scalars are
// leaves.
func collectResultKeys(prefix string, v any, out map[string]bool) {
	switch node := v.(type) {
	case map[string]any:
		for k, val := range node {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			out[path] = true
			if resultFreeformPaths[path] {
				continue
			}
			collectResultKeys(path, val, out)
		}
	case []any:
		for _, el := range node {
			collectResultKeys(prefix, el, out)
		}
	}
}

// collectResultStructKeys reflects over results.Result and returns the set of
// path-anchored json key names it declares, skipping json:"-" fields and
// descending through pointers and slices into nested structs from the
// internal/results package (Mizan/Invocation/Template/Autorater/Rubric/Inputs
// element/Outcome/TokenUsage). registry string-kinds (Modality, MetricKind) and
// time.Time are scalars and are not descended. This proves completeness: every
// declared field — including omitempty and slice-element fields — must be
// documented.
func collectResultStructKeys(prefix string, t reflect.Type, out map[string]bool) {
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
		if resultFreeformPaths[path] {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Slice {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && strings.Contains(ft.PkgPath(), "internal/results") {
			collectResultStructKeys(path, ft, out)
		}
	}
}

func resultKeySet(m map[string]any) map[string]bool {
	out := map[string]bool{}
	collectResultKeys("", m, out)
	return out
}

// TestReportResultDriftKeySetEquality is the primary gate: the results.Result key
// set documented in report-to-html's SKILL.md must EQUAL the key set the CLI's
// `-o json` path renders from a representative results.Result. A field added,
// renamed, or retagged on the struct (or its nested types) changes the rendered
// set and fails here until the skill is updated.
func TestReportResultDriftKeySetEquality(t *testing.T) {
	documented := resultKeySet(documentedResultResultShape(t))
	live := resultKeySet(renderResultResultJSON(t, representativeStoredResult()))

	if missing := diff(live, documented); len(missing) > 0 {
		t.Errorf("SKILL.md results.Result block is MISSING keys the CLI emits (drift): %v", missing)
	}
	if extra := diff(documented, live); len(extra) > 0 {
		t.Errorf("SKILL.md results.Result block documents keys the CLI does NOT emit (drift): %v", extra)
	}
	if !reflect.DeepEqual(sortedKeys(documented), sortedKeys(live)) {
		t.Errorf("documented vs live key-set mismatch:\n documented: %v\n live:       %v",
			sortedKeys(documented), sortedKeys(live))
	}
}

// TestReportResultDriftStructCompleteness proves the documented block covers
// EVERY declared results.Result field (recursing through pointers and slices into
// the nested internal/results structs), including omitempty and slice-element
// fields. This closes the gap where a newly added field would be absent from the
// rendered sample yet must still be documented.
func TestReportResultDriftStructCompleteness(t *testing.T) {
	documented := resultKeySet(documentedResultResultShape(t))
	declared := map[string]bool{}
	collectResultStructKeys("", reflect.TypeOf(results.Result{}), declared)

	if missing := diff(declared, documented); len(missing) > 0 {
		t.Errorf("results.Result declares json fields NOT documented in SKILL.md (drift): %v\n"+
			"declared: %v\ndocumented: %v", missing, sortedKeys(declared), sortedKeys(documented))
	}
}

// TestReportResultDriftCatchesSyntheticDrift proves the comparison logic actually
// catches drift: starting from the live key set, an added key, a removed key, and
// a renamed key must each be flagged by diff() against the documented set. This
// guards the gate itself so a future refactor cannot silently neuter it.
func TestReportResultDriftCatchesSyntheticDrift(t *testing.T) {
	documented := resultKeySet(documentedResultResultShape(t))
	live := resultKeySet(renderResultResultJSON(t, representativeStoredResult()))

	// Baseline: the real sets must agree (no drift today).
	if len(diff(live, documented)) != 0 || len(diff(documented, live)) != 0 {
		t.Fatalf("baseline expected equal key-sets before synthetic mutation")
	}

	// Added field on the CLI side must be reported as MISSING from the doc.
	added := clone(live)
	added["Outcome.NewField"] = true
	if got := diff(added, documented); len(got) != 1 || got[0] != "Outcome.NewField" {
		t.Errorf("added-field drift not caught: %v", got)
	}

	// Removed field from the CLI side must be reported as EXTRA in the doc.
	removed := clone(live)
	delete(removed, "Outcome.Score")
	if got := diff(documented, removed); len(got) != 1 || got[0] != "Outcome.Score" {
		t.Errorf("removed-field drift not caught: %v", got)
	}

	// Renamed field must be caught on BOTH sides (old extra in doc, new missing).
	renamed := clone(live)
	delete(renamed, "RunID")
	renamed["RunIdentifier"] = true
	if got := diff(documented, renamed); len(got) != 1 || got[0] != "RunID" {
		t.Errorf("renamed-field (old name) drift not caught: %v", got)
	}
	if got := diff(renamed, documented); len(got) != 1 || got[0] != "RunIdentifier" {
		t.Errorf("renamed-field (new name) drift not caught: %v", got)
	}
}

func clone(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
