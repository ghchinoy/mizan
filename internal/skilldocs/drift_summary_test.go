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

// This is the results.TemplateSummary + results.TrendPoint drift gate for the
// OPTIONAL server-computed enrichment path of the report-to-html skill
// (plugins/mizan-results). It is the sibling of drift_results_test.go's
// results.Result gate for the same skill's default client-side path: where that
// gate anchors `mizan results list -o json`, this one anchors
// `mizan results summary -o json` and `mizan results trend -o json` — the two
// server-side aggregations the enrichment consumes.
//
// It is HERMETIC by construction: `mizan results summary`/`trend` read a local
// store and need no network or ADC, but rather than shell out this test
// constructs a FULLY-POPULATED results.TemplateSummary / results.TrendPoint
// (every omitempty field set, plus the slice-element structs ThresholdStats,
// ScoreBucket and CriterionMean), renders each as a one-element ARRAY through the
// SAME encoding the CLI's `-o json` path uses (encoding/json with SetIndent,
// mirroring cmd/mizan.printJSON over the []TemplateSummary / []TrendPoint the
// render funcs pass it — cmd/mizan/results.go:497,537), and asserts KEY-SET
// EQUALITY (path-anchored) against the key set documented in the report-to-html
// SKILL.md fenced json blocks. A reflection pass over each struct additionally
// proves every declared (non-json:"-") field — recursing through pointers AND
// slice-element structs — is documented, so even an omitempty field added to any
// of these types fails the gate until the skill is updated. A synthetic-drift
// self-test proves the comparison actually catches add/remove/rename.
//
// It REUSES the shared helpers in drift_results_test.go (fenceRe, collectResultKeys
// which descends array elements, collectResultStructKeys which recurses pointers
// and slices into internal/results structs, resultKeySet, diff, sortedKeys, clone)
// so the two results gates stay mechanically identical.

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/results"
)

// Markers preceding the fenced json blocks that document the maximal
// TemplateSummary / TrendPoint shapes in the report-to-html SKILL.md. Anchoring
// on explicit markers keeps the gate stable as illustrative blocks are added.
const (
	summaryDriftMarker = "<!-- drift:results.TemplateSummary -->"
	trendDriftMarker   = "<!-- drift:results.TrendPoint -->"
)

// documentedEnrichmentShape extracts the json block that follows the given marker
// in the report-to-html SKILL.md and returns it parsed as an arbitrary value
// (these blocks are ARRAYS, matching the CLI's `-o json` output for summary/trend).
func documentedEnrichmentShape(t *testing.T, marker string) any {
	t.Helper()
	raw, err := os.ReadFile(reportSkillPath(t))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	text := string(raw)

	mi := strings.Index(text, marker)
	if mi < 0 {
		t.Fatalf("SKILL.md is missing the %q marker before its json block", marker)
	}
	m := fenceRe.FindStringSubmatch(text[mi:])
	if m == nil {
		t.Fatalf("no fenced ```json block found after the %q marker in SKILL.md", marker)
	}
	var doc any
	if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
		t.Fatalf("documented json block after %q is not valid JSON: %v\nblock:\n%s", marker, err, m[1])
	}
	return doc
}

// renderEnrichmentJSON serializes v exactly as the CLI's `-o json` path does.
// cmd/mizan.printJSON is a thin json.Encoder(SetIndent) wrapper, so encoding/json
// here produces the identical KEY SET the CLI emits — which is all this gate
// compares. (cmd/mizan is package main and cannot be imported; the encoder is
// replicated, not re-invoked.)
func renderEnrichmentJSON(t *testing.T, v any) any {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		t.Fatalf("encode %T: %v", v, err)
	}
	var live any
	if err := json.Unmarshal(buf.Bytes(), &live); err != nil {
		t.Fatalf("re-parse rendered %T json: %v", v, err)
	}
	return live
}

// enrichmentKeySet path-anchors every key in a decoded shape (object or array),
// reusing collectResultKeys, which descends both objects and array elements.
func enrichmentKeySet(v any) map[string]bool {
	out := map[string]bool{}
	collectResultKeys("", v, out)
	return out
}

func f64(v float64) *float64 { return &v }

// representativeTemplateSummary builds a results.TemplateSummary with EVERY field
// populated — including the omitempty pointer statistics, the Threshold object
// (present only with --threshold) and a non-empty Buckets slice — so the rendered
// json exposes every key the struct and its slice-element structs can emit.
func representativeTemplateSummary() results.TemplateSummary {
	return results.TemplateSummary{
		TemplateID:      "brand/tone",
		TemplateVersion: "1.0.0",
		N:               12,
		NUnscored:       1,
		Mean:            f64(4.1),
		Min:             f64(2),
		Max:             f64(5),
		Stddev:          f64(0.8),
		Threshold:       &results.ThresholdStats{Value: 3, Pass: 9, Fail: 3, PassRate: 0.75},
		Buckets:         []results.ScoreBucket{{Lo: 2, Hi: 2.6, Count: 3}},
	}
}

// representativeTrendPoint builds a results.TrendPoint with EVERY field populated
// — including the omitempty Mean pointer and a non-empty PerCriterion slice
// (present only for rubric-detail results with --per-criterion) — so the rendered
// json exposes every key the struct and CriterionMean can emit.
func representativeTrendPoint() results.TrendPoint {
	return results.TrendPoint{
		Bucket:       "2026-09-16",
		N:            5,
		NUnscored:    1,
		Mean:         f64(4.2),
		PerCriterion: []results.CriterionMean{{Group: "brand", Criterion: "tone match", N: 5, Mean: 4.2}},
	}
}

// TestReportSummaryDriftKeySetEquality is the primary TemplateSummary gate: the
// key set documented in report-to-html's SKILL.md must EQUAL the key set the
// CLI's `results summary -o json` path renders from a fully-populated
// []TemplateSummary. A field added, renamed, or retagged on TemplateSummary,
// ThresholdStats, or ScoreBucket changes the rendered set and fails here.
func TestReportSummaryDriftKeySetEquality(t *testing.T) {
	documented := enrichmentKeySet(documentedEnrichmentShape(t, summaryDriftMarker))
	live := enrichmentKeySet(renderEnrichmentJSON(t, []results.TemplateSummary{representativeTemplateSummary()}))

	if missing := diff(live, documented); len(missing) > 0 {
		t.Errorf("SKILL.md TemplateSummary block is MISSING keys the CLI emits (drift): %v", missing)
	}
	if extra := diff(documented, live); len(extra) > 0 {
		t.Errorf("SKILL.md TemplateSummary block documents keys the CLI does NOT emit (drift): %v", extra)
	}
	if !reflect.DeepEqual(sortedKeys(documented), sortedKeys(live)) {
		t.Errorf("documented vs live key-set mismatch:\n documented: %v\n live:       %v",
			sortedKeys(documented), sortedKeys(live))
	}
}

// TestReportTrendDriftKeySetEquality is the primary TrendPoint gate, the sibling
// of the TemplateSummary gate for `results trend -o json`.
func TestReportTrendDriftKeySetEquality(t *testing.T) {
	documented := enrichmentKeySet(documentedEnrichmentShape(t, trendDriftMarker))
	live := enrichmentKeySet(renderEnrichmentJSON(t, []results.TrendPoint{representativeTrendPoint()}))

	if missing := diff(live, documented); len(missing) > 0 {
		t.Errorf("SKILL.md TrendPoint block is MISSING keys the CLI emits (drift): %v", missing)
	}
	if extra := diff(documented, live); len(extra) > 0 {
		t.Errorf("SKILL.md TrendPoint block documents keys the CLI does NOT emit (drift): %v", extra)
	}
	if !reflect.DeepEqual(sortedKeys(documented), sortedKeys(live)) {
		t.Errorf("documented vs live key-set mismatch:\n documented: %v\n live:       %v",
			sortedKeys(documented), sortedKeys(live))
	}
}

// TestReportSummaryDriftStructCompleteness proves the documented TemplateSummary
// block covers EVERY declared field, recursing through pointers and slices into
// the nested internal/results structs (ThresholdStats, ScoreBucket), including
// omitempty and slice-element fields. This closes the gap where a newly added
// field would be absent from the rendered sample yet must still be documented.
func TestReportSummaryDriftStructCompleteness(t *testing.T) {
	documented := enrichmentKeySet(documentedEnrichmentShape(t, summaryDriftMarker))
	declared := map[string]bool{}
	collectResultStructKeys("", reflect.TypeOf(results.TemplateSummary{}), declared)

	if missing := diff(declared, documented); len(missing) > 0 {
		t.Errorf("results.TemplateSummary declares json fields NOT documented in SKILL.md (drift): %v\n"+
			"declared: %v\ndocumented: %v", missing, sortedKeys(declared), sortedKeys(documented))
	}
}

// TestReportTrendDriftStructCompleteness proves the documented TrendPoint block
// covers EVERY declared field, recursing through the CriterionMean slice-element
// struct, including omitempty fields.
func TestReportTrendDriftStructCompleteness(t *testing.T) {
	documented := enrichmentKeySet(documentedEnrichmentShape(t, trendDriftMarker))
	declared := map[string]bool{}
	collectResultStructKeys("", reflect.TypeOf(results.TrendPoint{}), declared)

	if missing := diff(declared, documented); len(missing) > 0 {
		t.Errorf("results.TrendPoint declares json fields NOT documented in SKILL.md (drift): %v\n"+
			"declared: %v\ndocumented: %v", missing, sortedKeys(declared), sortedKeys(documented))
	}
}

// TestReportEnrichmentDriftCatchesSyntheticDrift proves the comparison logic
// actually catches drift: starting from the live key sets of BOTH shapes, an
// added key, a removed key, and a renamed key (including into a slice-element
// struct) must each be flagged by diff() against the documented set. This guards
// the gate itself so a future refactor cannot silently neuter it.
func TestReportEnrichmentDriftCatchesSyntheticDrift(t *testing.T) {
	// --- TemplateSummary: prove add / remove are caught. ---
	sumDoc := enrichmentKeySet(documentedEnrichmentShape(t, summaryDriftMarker))
	sumLive := enrichmentKeySet(renderEnrichmentJSON(t, []results.TemplateSummary{representativeTemplateSummary()}))
	if len(diff(sumLive, sumDoc)) != 0 || len(diff(sumDoc, sumLive)) != 0 {
		t.Fatalf("baseline expected equal TemplateSummary key-sets before synthetic mutation")
	}
	// Added field on the CLI side (in the ThresholdStats slice-element struct)
	// must be reported as MISSING from the doc.
	added := clone(sumLive)
	added["threshold.new_field"] = true
	if got := diff(added, sumDoc); len(got) != 1 || got[0] != "threshold.new_field" {
		t.Errorf("added-field drift (TemplateSummary) not caught: %v", got)
	}
	// Removed field from the CLI side must be reported as EXTRA in the doc.
	removed := clone(sumLive)
	delete(removed, "stddev")
	if got := diff(sumDoc, removed); len(got) != 1 || got[0] != "stddev" {
		t.Errorf("removed-field drift (TemplateSummary) not caught: %v", got)
	}

	// --- TrendPoint: prove rename is caught on BOTH sides. ---
	trendDoc := enrichmentKeySet(documentedEnrichmentShape(t, trendDriftMarker))
	trendLive := enrichmentKeySet(renderEnrichmentJSON(t, []results.TrendPoint{representativeTrendPoint()}))
	if len(diff(trendLive, trendDoc)) != 0 || len(diff(trendDoc, trendLive)) != 0 {
		t.Fatalf("baseline expected equal TrendPoint key-sets before synthetic mutation")
	}
	// Rename a slice-element field: old name extra in doc, new name missing.
	renamed := clone(trendLive)
	delete(renamed, "per_criterion.criterion")
	renamed["per_criterion.rubric_criterion"] = true
	if got := diff(trendDoc, renamed); len(got) != 1 || got[0] != "per_criterion.criterion" {
		t.Errorf("renamed-field (old name) drift (TrendPoint) not caught: %v", got)
	}
	if got := diff(renamed, trendDoc); len(got) != 1 || got[0] != "per_criterion.rubric_criterion" {
		t.Errorf("renamed-field (new name) drift (TrendPoint) not caught: %v", got)
	}
}
