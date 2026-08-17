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

// TestRenderScorecardMissingSkippedStatuses covers the member-status labels the
// happy-path sample never exercises: a Missing member (template id unresolved), a
// Skipped member (fail-fast aborted before it), and an unrecognized status value
// that must fall through to its raw label. Each of these members carries no
// numeric score, so the SCORE cell must render as "-".
func TestRenderScorecardMissingSkippedStatuses(t *testing.T) {
	outputFormat = outputTable
	res := evalset.EvalSetResult{
		SetID: "quickstart/answer-quality",
		Members: []evalset.MemberResult{
			{MetricID: "quickstart/unknown-metric", Status: evalset.Missing, Weight: 1, Error: "template not found"},
			{MetricID: "quickstart/response-conciseness", Status: evalset.Skipped, Weight: 1},
			{MetricID: "quickstart/weird-member", Status: evalset.MemberStatus("bizarre"), Weight: 1},
		},
		Aggregate: evalset.Aggregate{Method: evalset.AggMin, Scored: 0},
		Verdict:   evalset.Failed,
	}
	var buf bytes.Buffer
	if err := renderScorecard(&buf, res, false); err != nil {
		t.Fatalf("renderScorecard: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"missing", // evalset.Missing label
		"skipped", // evalset.Skipped label
		"bizarre", // unrecognized status falls through to its raw string
		"template not found",
		"FAILED",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\n---\n%s", want, out)
		}
	}
	// Every member here is scoreless: the SCORE column must show the "-" placeholder
	// and there must be no stray numeric score.
	if !strings.Contains(out, "-") {
		t.Errorf("scoreless members should render \"-\" in SCORE cell\n---\n%s", out)
	}
}

// TestRenderScorecardNoAggregationNoThreshold covers the aggregate-line branches
// the sample never hits: a manifest with NO aggregation block (Method=="") and NO
// numeric aggregate score (Score==nil) must render "Aggregate (over N scored): -"
// with the method token omitted, and with NO threshold clause when Threshold==nil.
func TestRenderScorecardNoAggregationNoThreshold(t *testing.T) {
	outputFormat = outputTable
	res := evalset.EvalSetResult{
		SetID:     "quickstart/answer-quality",
		Members:   []evalset.MemberResult{{MetricID: "quickstart/x", Status: evalset.Errored, Weight: 1, Error: "boom"}},
		Aggregate: evalset.Aggregate{Scored: 0}, // no Method, no Score, no Threshold
		Verdict:   evalset.Failed,
	}
	var buf bytes.Buffer
	if err := renderScorecard(&buf, res, false); err != nil {
		t.Fatalf("renderScorecard: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Aggregate (over 0 scored): -") {
		t.Errorf("want method-less aggregate line \"Aggregate (over 0 scored): -\"\n---\n%s", out)
	}
	if strings.Contains(out, "weighted-mean") || strings.Contains(out, "over 0 scored): 0") {
		t.Errorf("no method/score should be rendered when absent\n---\n%s", out)
	}
	if strings.Contains(out, "threshold:") {
		t.Errorf("no threshold clause should appear when Threshold is nil\n---\n%s", out)
	}
	if !strings.Contains(out, "FAILED") {
		t.Errorf("verdict FAILED should still print\n---\n%s", out)
	}
}

// TestEvalRunRejectsBothMetricAndSet proves the mutual-exclusivity guard rejects
// supplying BOTH --metric and --set (the sibling of the neither-supplied case in
// TestEvalRunRequiresMetric). The run stops at the guard before any config load,
// DB open, or live API call, so it is network- and cgo-free.
func TestEvalRunRejectsBothMetricAndSet(t *testing.T) {
	out, err := executeRoot(t, "eval", "run", "--metric", "ns/slug", "--set", "some/path.yaml")
	if err == nil {
		t.Fatalf("expected error when both --metric and --set are supplied, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "exactly one of --metric or --set is required") {
		t.Errorf("error = %v, want exactly one of --metric or --set is required", err)
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
