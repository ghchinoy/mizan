package eval

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// This file closes edge-case gaps in the rubric per-criterion transparency path
// that the primary rubric_structured_test.go does not exercise: exact-boundary
// scores (off-by-one in the clamp), fractional per-criterion coercion/rounding,
// fractional overall_score (kept as a float, not rounded), a missing
// overall_score, unknown/extra judge fields, duplicate criteria, and
// empty/whitespace rationale. All run on the fake GenaiClient seam (no network).

// runRubricDetail is a tiny helper: drive the rubric-detail path with a canned
// judge JSON body on the given [min,max] scale and return the Result. So these
// clamp/coercion gap tests stay orthogonal to R-R2 reconciliation, the authored
// RubricGroups are derived to EXACTLY match the (group, criterion) pairs in the
// canned response (the happy path), leaving reconciliation a no-op here.
func runRubricDetail(t *testing.T, respJSON string, min, max int) Result {
	t.Helper()
	fg := &fakeGenai{respText: respJSON}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
	tmpl := rubricTemplate()
	tmpl.RubricGroups = groupsFromResponse(t, respJSON)
	res, err := eng.Run(context.Background(), tmpl, rubricInstance(), WithRubricDetail(min, max))
	if err != nil {
		t.Fatalf("Run(scale %d-%d): %v", min, max, err)
	}
	return res
}

// groupsFromResponse extracts the authored RubricGroups implied by a canned judge
// response: every (group, criterion) pair in per_criterion, deduped, preserving
// first-seen order within each group. This keeps the clamp/coercion gap tests on
// the reconciliation happy path regardless of the placeholder criterion strings
// they use.
func groupsFromResponse(t *testing.T, respJSON string) map[string][]string {
	t.Helper()
	var body struct {
		PerCriterion []struct {
			Group     string `json:"group"`
			Criterion string `json:"criterion"`
		} `json:"per_criterion"`
	}
	if err := json.Unmarshal([]byte(respJSON), &body); err != nil {
		t.Fatalf("groupsFromResponse: %v", err)
	}
	groups := make(map[string][]string)
	seen := make(map[[2]string]bool)
	for _, e := range body.PerCriterion {
		key := [2]string{e.Group, e.Criterion}
		if seen[key] {
			continue
		}
		seen[key] = true
		groups[e.Group] = append(groups[e.Group], e.Criterion)
	}
	return groups
}

// pcScores extracts the per_criterion "score" values as a slice for comparison.
func pcScores(t *testing.T, res Result) []any {
	t.Helper()
	pc, ok := res.CustomOutput["per_criterion"].([]any)
	if !ok {
		t.Fatalf("per_criterion missing or wrong type: %T", res.CustomOutput["per_criterion"])
	}
	out := make([]any, 0, len(pc))
	for _, item := range pc {
		out = append(out, item.(map[string]any)["score"])
	}
	return out
}

// TestRunRubricStructuredBoundaryNotClamped proves scores EXACTLY at min and max
// are left untouched (the clamp is inclusive on both ends: `< min` / `> max`, not
// `<=` / `>=`). This is the off-by-one guard the mid-range clamp test does not
// cover.
func TestRunRubricStructuredBoundaryNotClamped(t *testing.T) {
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"c1","score":1,"rationale":"exact min"},
			{"group":"clarity","criterion":"c2","score":5,"rationale":"exact max"},
			{"group":"tone","criterion":"c3","score":3,"rationale":"mid"}
		],
		"overall_score": 1,
		"explanation": "boundaries"
	}`
	res := runRubricDetail(t, resp, 1, 5)

	got := pcScores(t, res)
	want := []any{1, 5, 3} // all coerced to int, none clamped
	if !reflect.DeepEqual(got, want) {
		t.Errorf("boundary per-criterion scores = %v, want %v", got, want)
	}
	// overall_score exactly at min is preserved (not bumped).
	if res.CustomOutput["overall_score"] != float64(1) {
		t.Errorf("overall_score = %v, want 1 (exact min, unchanged)", res.CustomOutput["overall_score"])
	}
	if res.Score == nil || *res.Score != 1 {
		t.Errorf("Score = %v, want 1", res.Score)
	}
}

// TestRunRubricStructuredScoreCoercionRounds proves a non-integer per-criterion
// score (JSON numbers decode as float64) is rounded to the nearest int (half away
// from zero) before being surfaced, on a scale wide enough that rounding is not
// masked by clamping.
func TestRunRubricStructuredScoreCoercionRounds(t *testing.T) {
	// 0-10 scale so none of these are clamped; only the rounding is under test.
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"c1","score":3.4,"rationale":"down"},
			{"group":"clarity","criterion":"c2","score":3.5,"rationale":"half up"},
			{"group":"tone","criterion":"c3","score":3.6,"rationale":"up"},
			{"group":"tone","criterion":"c4","score":7.5,"rationale":"half up"}
		],
		"overall_score": 5,
		"explanation": "rounding"
	}`
	res := runRubricDetail(t, resp, 0, 10)

	got := pcScores(t, res)
	want := []any{3, 4, 4, 8} // 3.4->3, 3.5->4, 3.6->4, 7.5->8
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rounded per-criterion scores = %v, want %v", got, want)
	}
	// Every coerced score must be a Go int, never a lingering float64.
	for i, v := range got {
		if _, ok := v.(int); !ok {
			t.Errorf("score[%d] = %T %v, want int", i, v, v)
		}
	}
}

// TestRunRubricStructuredFractionalOverallScore proves overall_score is treated
// as a real (NUMBER schema) value: an in-range fraction is preserved verbatim
// (NOT rounded like per-criterion), and an out-of-range fraction is clamped to
// the boundary. In both cases Result.Score mirrors the (post-clamp) value.
func TestRunRubricStructuredFractionalOverallScore(t *testing.T) {
	cases := []struct {
		name       string
		overall    string
		wantStored float64
		wantScore  float32
	}{
		{"in-range fraction preserved", "3.5", 3.5, 3.5},
		{"above max clamped to max", "5.9", 5.0, 5.0},
		{"below min clamped to min", "0.25", 1.0, 1.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := `{"per_criterion":[{"group":"clarity","criterion":"c1","score":3,"rationale":"ok"}],` +
				`"overall_score":` + tc.overall + `,"explanation":"x"}`
			res := runRubricDetail(t, resp, 1, 5)

			if res.CustomOutput["overall_score"] != tc.wantStored {
				t.Errorf("overall_score = %v, want %v", res.CustomOutput["overall_score"], tc.wantStored)
			}
			if res.Score == nil || *res.Score != tc.wantScore {
				t.Errorf("Score = %v, want %v", res.Score, tc.wantScore)
			}
		})
	}
}

// TestRunRubricStructuredMissingOverallScore proves a judge body that omits
// overall_score degrades gracefully: no panic, per_criterion still round-trips,
// and Result.Score is left nil (rather than defaulting to 0, which would read as
// a real minimum score).
func TestRunRubricStructuredMissingOverallScore(t *testing.T) {
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"c1","score":4,"rationale":"ok"}
		],
		"explanation": "no overall"
	}`
	res := runRubricDetail(t, resp, 1, 5)

	if res.Score != nil {
		t.Errorf("Score = %v, want nil when overall_score is absent", res.Score)
	}
	if _, present := res.CustomOutput["overall_score"]; present {
		t.Errorf("overall_score should be absent from CustomOutput, got %v", res.CustomOutput["overall_score"])
	}
	if got := pcScores(t, res); !reflect.DeepEqual(got, []any{4}) {
		t.Errorf("per_criterion scores = %v, want [4]", got)
	}
}

// TestRunRubricStructuredPreservesUnknownFields proves extra/unknown fields the
// judge may add (top-level and inside a per_criterion entry) survive the
// round-trip untouched, while the known numeric fields are still clamped/coerced.
func TestRunRubricStructuredPreservesUnknownFields(t *testing.T) {
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"c1","score":9,"rationale":"hi","confidence":0.9}
		],
		"overall_score": 4,
		"explanation": "extra",
		"notes": "judge chatter"
	}`
	res := runRubricDetail(t, resp, 1, 5)

	if res.CustomOutput["notes"] != "judge chatter" {
		t.Errorf("top-level unknown field dropped: %v", res.CustomOutput["notes"])
	}
	first := res.CustomOutput["per_criterion"].([]any)[0].(map[string]any)
	if first["confidence"] != 0.9 {
		t.Errorf("per-entry unknown field dropped: %v", first["confidence"])
	}
	// Known field still clamped (9 -> 5) and coerced to int alongside the unknown.
	if first["score"] != 5 {
		t.Errorf("score = %v, want 5 (clamped) despite unknown sibling field", first["score"])
	}
}

// TestRunRubricStructuredDuplicateCriteria locks the R-R2 behavior: the judge
// returning the SAME authored (group, criterion) pair more than once is a HARD
// ERROR (duplicates corrupt the scorecard), superseding the earlier
// trust-the-list-and-keep-both behavior. The template authors "dup" once; the
// response returns it twice.
func TestRunRubricStructuredDuplicateCriteria(t *testing.T) {
	tmpl := rubricTemplate()
	tmpl.RubricGroups = map[string][]string{"clarity": {"dup"}}
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"dup","score":2,"rationale":"first"},
			{"group":"clarity","criterion":"dup","score":9,"rationale":"second"}
		],
		"overall_score": 3,
		"explanation": "dupes"
	}`
	fg := &fakeGenai{respText: resp}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
	_, err := eng.Run(context.Background(), tmpl, rubricInstance(), WithRubricDetail(1, 5))
	if err == nil {
		t.Fatal("expected hard error for a duplicated authored criterion, got nil")
	}
	for _, want := range []string{"eval:", "duplicat", "dup"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// TestRunRubricStructuredEmptyRationale proves empty and whitespace-only
// rationales are tolerated (not rejected, not normalized) and round-trip
// verbatim — the schema constrains shape, not content richness.
func TestRunRubricStructuredEmptyRationale(t *testing.T) {
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"c1","score":3,"rationale":""},
			{"group":"tone","criterion":"c2","score":4,"rationale":"   "}
		],
		"overall_score": 3,
		"explanation": ""
	}`
	res := runRubricDetail(t, resp, 1, 5)

	pc := res.CustomOutput["per_criterion"].([]any)
	if r := pc[0].(map[string]any)["rationale"]; r != "" {
		t.Errorf("empty rationale altered: %q", r)
	}
	if r := pc[1].(map[string]any)["rationale"]; r != "   " {
		t.Errorf("whitespace rationale altered: %q", r)
	}
	if res.CustomOutput["explanation"] != "" {
		t.Errorf("empty explanation altered: %q", res.CustomOutput["explanation"])
	}
}
