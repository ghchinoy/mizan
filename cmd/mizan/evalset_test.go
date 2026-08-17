package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/evalset"
)

func f32(v float32) *float32 { return &v }
func f64(v float64) *float64 { return &v }

// sampleResult is a mixed EvalSetResult: one OK scored member, one errored
// member (carrying untrusted note text), a weighted-mean aggregate over the one
// scored member, and a PASSED verdict.
func sampleResult() evalset.EvalSetResult {
	return evalset.EvalSetResult{
		SetID:      "quickstart/answer-quality",
		Version:    "1.0.0",
		AssetClass: "text-answer",
		Members: []evalset.MemberResult{
			{MetricID: "quickstart/response-helpfulness", Status: evalset.OK, Weight: 2, Score: f32(0.82)},
			{MetricID: "quickstart/response-conciseness", Status: evalset.Errored, Weight: 1, Error: "boom\t\x1b[31mred\x1b[0m"},
		},
		Aggregate: evalset.Aggregate{Method: evalset.AggWeightedMean, Score: f32(0.82), Threshold: f64(0.6), Scored: 1},
		Verdict:   evalset.Passed,
	}
}

// TestRenderScorecardTable checks the table has the header, a row per member, the
// aggregate line, and the verdict — and that untrusted note text is sanitized
// (no tabs / ANSI escapes leak into the tabwriter layout).
func TestRenderScorecardTable(t *testing.T) {
	outputFormat = outputTable
	var buf bytes.Buffer
	if err := renderScorecard(&buf, sampleResult(), false); err != nil {
		t.Fatalf("renderScorecard: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"EvalSet: quickstart/answer-quality (v1.0.0)",
		"asset-class: text-answer",
		"MEMBER", "STATUS", "WEIGHT", "SCORE", "NOTE",
		"quickstart/response-helpfulness",
		"quickstart/response-conciseness",
		"0.82",
		"weighted-mean over 1 scored",
		"threshold: 0.6",
		"PASSED",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\n---\n%s", want, out)
		}
	}
	// Untrusted note text must be sanitized: no ESC byte, no embedded tab spill.
	if strings.Contains(out, "\x1b") {
		t.Errorf("ANSI escape leaked into scorecard: %q", out)
	}
	if strings.Contains(out, "boom\tred") {
		t.Errorf("raw tab leaked into note cell: %q", out)
	}
}

// TestRenderScorecardJSON checks --output json serializes the whole result.
func TestRenderScorecardJSON(t *testing.T) {
	outputFormat = outputJSON
	defer func() { outputFormat = outputTable }()
	var buf bytes.Buffer
	if err := renderScorecard(&buf, sampleResult(), false); err != nil {
		t.Fatalf("renderScorecard: %v", err)
	}
	var got evalset.EvalSetResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("json unmarshal: %v (out=%s)", err, buf.String())
	}
	if got.SetID != "quickstart/answer-quality" || got.Verdict != evalset.Passed {
		t.Errorf("json round-trip mismatch: %+v", got)
	}
	if len(got.Members) != 2 {
		t.Errorf("want 2 members in json, got %d", len(got.Members))
	}
}

// TestEvalSetGateError verifies the opt-in gate exit rule: non-nil error ONLY
// when Gate && Verdict==Failed.
func TestEvalSetGateError(t *testing.T) {
	cases := []struct {
		name    string
		gate    bool
		verdict evalset.SetVerdict
		wantErr bool
	}{
		{"gate off, failed", false, evalset.Failed, false},
		{"gate off, passed", false, evalset.Passed, false},
		{"gate on, passed", true, evalset.Passed, false},
		{"gate on, failed", true, evalset.Failed, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := evalSetGateError(evalset.EvalSetResult{SetID: "x/y", Gate: tc.gate, Verdict: tc.verdict})
			if tc.wantErr && err == nil {
				t.Fatal("want gate error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want nil, got %v", err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "eval-set gate failed") {
				t.Errorf("unexpected gate error text: %v", err)
			}
		})
	}
}
