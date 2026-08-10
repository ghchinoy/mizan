package main

// smoke_test.go is the renderer half of the creds-free end-to-end smoke
// (R-SMOKE). The engine-mapping half lives in internal/eval/smoke_test.go, which
// drives Engine.Run for every metric kind through the fake client seams and
// asserts the mapped Result. The CLI renderers (renderResult /
// renderRubricDetailResult) are unexported in package main and cannot be reached
// from package eval, so this file constructs the SAME per-kind Result shapes the
// engine produces and asserts each renders through the actual renderers into
// non-empty, well-formed output — no credentials, network, or live clients.
//
// Together the two files satisfy the R-SMOKE requirement: for EVERY kind, both
// the engine mapping AND the renderer output are asserted, creds-free, under the
// normal `go test ./...` (deliberately not behind a build tag).

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/eval"
)

// ptr is a tiny helper for the *float32 Score field.
func ptr(f float32) *float32 { return &f }

// renderTable renders res through the real renderResult in table mode and returns
// the output, failing the test on a render error.
func renderTable(t *testing.T, res eval.Result, showStats bool) string {
	t.Helper()
	outputFormat = outputTable
	var buf bytes.Buffer
	if err := renderResult(&buf, res, showStats); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	return buf.String()
}

// TestSmokeRenderPointwise renders a native pointwise Result.
func TestSmokeRenderPointwise(t *testing.T) {
	out := renderTable(t, eval.Result{Score: ptr(4.5), Explanation: "Clear and correct."}, false)
	for _, want := range []string{"Score:", "4.5", "Explanation:", "Clear and correct."} {
		if !strings.Contains(out, want) {
			t.Errorf("pointwise render missing %q in:\n%s", want, out)
		}
	}
}

// TestSmokeRenderRubricNative renders a native rubric Result (same {score,
// explanation} shape as pointwise).
func TestSmokeRenderRubricNative(t *testing.T) {
	out := renderTable(t, eval.Result{Score: ptr(3), Explanation: "Clear but slightly informal."}, false)
	for _, want := range []string{"Score:", "3", "Explanation:", "Clear but slightly informal."} {
		if !strings.Contains(out, want) {
			t.Errorf("rubric-native render missing %q in:\n%s", want, out)
		}
	}
}

// TestSmokeRenderRubricDetail renders a rubric-detail Result: renderResult must
// route on RubricDetail to renderRubricDetailResult, producing the per-criterion
// table.
func TestSmokeRenderRubricDetail(t *testing.T) {
	res := eval.Result{
		Score:        ptr(5),
		RubricDetail: true,
		CustomOutput: map[string]any{
			"overall_score": float64(5),
			"explanation":   "Strong overall.",
			"per_criterion": []any{
				map[string]any{"group": "clarity", "criterion": "The message is unambiguous", "score": 5, "rationale": "clamped"},
				map[string]any{"group": "tone", "criterion": "Matches a professional brand voice", "score": 4, "rationale": "in-scale"},
			},
		},
	}
	out := renderTable(t, res, false)
	for _, want := range []string{"Score:", "5", "Strong overall.", "Per-criterion:", "GROUP", "CRITERION", "clarity", "tone", "The message is unambiguous"} {
		if !strings.Contains(out, want) {
			t.Errorf("rubric-detail render missing %q in:\n%s", want, out)
		}
	}
}

// TestSmokeRenderCustomSchema renders a custom_schema Result with --stats on, so
// the genai token-usage footer is exercised.
func TestSmokeRenderCustomSchema(t *testing.T) {
	res := eval.Result{
		Explanation:  "On brand.",
		CustomOutput: map[string]any{"compliant": true, "overall_score": 8.5},
		RawOutput:    []string{`{"compliant":true}`},
		Stats: eval.Stats{
			Duration:   9 * time.Millisecond,
			TokenUsage: &eval.TokenUsage{PromptTokens: 120, CandidatesTokens: 34, TotalTokens: 154},
		},
	}
	out := renderTable(t, res, true)
	for _, want := range []string{"CustomOutput[compliant]:", "true", "Duration:", "Tokens:", "prompt=120", "total=154"} {
		if !strings.Contains(out, want) {
			t.Errorf("custom_schema render missing %q in:\n%s", want, out)
		}
	}
}

// TestSmokeRenderPairwise renders a native pairwise Result: the Choice row must
// surface the mapped PairwiseChoice.
func TestSmokeRenderPairwise(t *testing.T) {
	out := renderTable(t, eval.Result{PairwiseChoice: "CANDIDATE", Explanation: "Candidate is more helpful."}, false)
	for _, want := range []string{"Choice:", "CANDIDATE", "Explanation:", "Candidate is more helpful."} {
		if !strings.Contains(out, want) {
			t.Errorf("pairwise render missing %q in:\n%s", want, out)
		}
	}
}

// TestSmokeRenderJSONAllKinds proves every kind's Result also renders through the
// JSON output path without error (the CLI's -o json mode).
func TestSmokeRenderJSONAllKinds(t *testing.T) {
	outputFormat = outputJSON
	defer func() { outputFormat = outputTable }()

	results := map[string]eval.Result{
		"pointwise":     {Score: ptr(4.5), Explanation: "ok"},
		"rubric-native": {Score: ptr(3), Explanation: "ok"},
		"rubric-detail": {Score: ptr(5), RubricDetail: true, CustomOutput: map[string]any{"overall_score": float64(5), "per_criterion": []any{}}},
		"custom-schema": {CustomOutput: map[string]any{"compliant": true}, RawOutput: []string{"{}"}},
		"pairwise":      {PairwiseChoice: "CANDIDATE", Explanation: "ok"},
	}
	for kind, res := range results {
		var buf bytes.Buffer
		if err := renderResult(&buf, res, true); err != nil {
			t.Errorf("renderResult(json, %s): %v", kind, err)
		}
		if strings.TrimSpace(buf.String()) == "" {
			t.Errorf("renderResult(json, %s) produced empty output", kind)
		}
	}
}
