package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/eval"
)

// TestRenderResultStatsOff verifies the default (no --stats) table has no stats
// footer.
func TestRenderResultStatsOff(t *testing.T) {
	outputFormat = outputTable
	score := float32(4)
	res := eval.Result{Score: &score, Explanation: "ok", Stats: eval.Stats{Duration: 5 * time.Millisecond}}
	var buf bytes.Buffer
	if err := renderResult(&buf, res, false); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "Duration:") || strings.Contains(out, "Tokens:") {
		t.Errorf("stats footer present with --stats off: %q", out)
	}
}

// TestRenderResultStatsNativeNote verifies --stats prints the duration plus the
// explicit "not available" note when TokenUsage is nil (native path).
func TestRenderResultStatsNativeNote(t *testing.T) {
	outputFormat = outputTable
	score := float32(4)
	res := eval.Result{Score: &score, Explanation: "ok", Stats: eval.Stats{Duration: 7 * time.Millisecond}}
	var buf bytes.Buffer
	if err := renderResult(&buf, res, true); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Duration:") {
		t.Errorf("missing Duration footer: %q", out)
	}
	if !strings.Contains(out, "token usage not available") {
		t.Errorf("missing native 'not available' note: %q", out)
	}
}

// TestRenderResultStatsTokens verifies --stats prints the token breakdown when
// TokenUsage is present (genai path).
func TestRenderResultStatsTokens(t *testing.T) {
	outputFormat = outputTable
	res := eval.Result{
		Explanation:  "ok",
		CustomOutput: map[string]any{"compliant": true},
		Stats: eval.Stats{
			Duration:   9 * time.Millisecond,
			TokenUsage: &eval.TokenUsage{PromptTokens: 10, CandidatesTokens: 20, TotalTokens: 30},
		},
	}
	var buf bytes.Buffer
	if err := renderResult(&buf, res, true); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "prompt=10") || !strings.Contains(out, "candidates=20") || !strings.Contains(out, "total=30") {
		t.Errorf("token breakdown missing/wrong: %q", out)
	}
	if strings.Contains(out, "not available") {
		t.Errorf("unexpected native note on genai path: %q", out)
	}
}

// TestPrintPreflightLine verifies the default-on pre-flight echo is a single
// concise line carrying the resolved project/location/model and path.
func TestPrintPreflightLine(t *testing.T) {
	var buf bytes.Buffer
	printPreflight(&buf, eval.ResolvedTarget{
		Project: "proj", Location: "global", Model: "gemini-3.5-flash", Path: "genai",
	})
	out := buf.String()
	if lines := strings.Count(strings.TrimRight(out, "\n"), "\n"); lines != 0 {
		t.Errorf("pre-flight should be one line, got %q", out)
	}
	for _, want := range []string{"project=proj", "location=global", "model=gemini-3.5-flash", "path=genai"} {
		if !strings.Contains(out, want) {
			t.Errorf("pre-flight missing %q; got %q", want, out)
		}
	}
}
