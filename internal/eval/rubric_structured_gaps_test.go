package eval

import (
	"context"
	"reflect"
	"testing"
)

// This file closes edge-case gaps in the rubric per-criterion transparency path
// that the primary rubric_structured_test.go does not exercise: exact-boundary
// scores (off-by-one in the clamp), fractional per-criterion coercion/rounding,
// fractional overall_score (kept as a float, not rounded), a missing
// overall_score, unknown/extra judge fields, duplicate criteria, and
// empty/whitespace rationale. All run on the fake GenaiClient seam (no network).

// runRubricDetail is a tiny helper: drive the rubric-detail path with a canned
// judge JSON body on the given [min,max] scale and return the Result.
func runRubricDetail(t *testing.T, respJSON string, min, max int) Result {
	t.Helper()
	fg := &fakeGenai{respText: respJSON}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
	res, err := eng.Run(context.Background(), rubricTemplate(), rubricInstance(), WithRubricDetail(min, max))
	if err != nil {
		t.Fatalf("Run(scale %d-%d): %v", min, max, err)
	}
	return res
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

// TestRunRubricStructuredDuplicateCriteria locks the current behavior: the path
// trusts the judge's list and does NOT deduplicate — two entries for the same
// group+criterion both survive (each still independently clamped). If dedup is
// ever desired this test documents that it is not happening today.
func TestRunRubricStructuredDuplicateCriteria(t *testing.T) {
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"dup","score":2,"rationale":"first"},
			{"group":"clarity","criterion":"dup","score":9,"rationale":"second"}
		],
		"overall_score": 3,
		"explanation": "dupes"
	}`
	res := runRubricDetail(t, resp, 1, 5)

	pc := res.CustomOutput["per_criterion"].([]any)
	if len(pc) != 2 {
		t.Fatalf("duplicate criteria collapsed: got %d entries, want 2", len(pc))
	}
	if got := pcScores(t, res); !reflect.DeepEqual(got, []any{2, 5}) {
		t.Errorf("duplicate scores = %v, want [2 5] (each clamped independently)", got)
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
