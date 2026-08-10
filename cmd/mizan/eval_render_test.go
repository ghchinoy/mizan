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

// TestRenderResultJSONIncludesStats proves the JSON output path ALWAYS embeds
// the stats object (duration and, on the genai path, token usage) regardless of
// the --stats flag — the contract stated in renderResult's doc comment. --stats
// governs only the human table footer, so a JSON consumer must not have to pass
// it to see timing/tokens.
func TestRenderResultJSONIncludesStats(t *testing.T) {
	prev := outputFormat
	outputFormat = outputJSON
	defer func() { outputFormat = prev }()

	res := eval.Result{
		Explanation: "ok",
		Stats: eval.Stats{
			Duration:   9 * time.Millisecond,
			TokenUsage: &eval.TokenUsage{PromptTokens: 10, CandidatesTokens: 20, TotalTokens: 30},
		},
	}
	var buf bytes.Buffer
	// showStats=false: the JSON must STILL carry the stats object.
	if err := renderResult(&buf, res, false); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "duration_ns") {
		t.Errorf("JSON missing duration_ns even though stats are always emitted: %q", out)
	}
	for _, want := range []string{"token_usage", "prompt_tokens", "candidates_tokens", "total_tokens"} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON missing %q: %q", want, out)
		}
	}
}

// TestRenderResultJSONOmitsTokenUsageWhenNil proves the native path (nil
// TokenUsage) omits the token_usage key in JSON (the field is `omitempty`),
// while duration is still present.
func TestRenderResultJSONOmitsTokenUsageWhenNil(t *testing.T) {
	prev := outputFormat
	outputFormat = outputJSON
	defer func() { outputFormat = prev }()

	score := float32(4)
	res := eval.Result{Score: &score, Explanation: "ok", Stats: eval.Stats{Duration: 3 * time.Millisecond}}
	var buf bytes.Buffer
	if err := renderResult(&buf, res, true); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "duration_ns") {
		t.Errorf("JSON missing duration_ns: %q", out)
	}
	if strings.Contains(out, "token_usage") {
		t.Errorf("native JSON should omit token_usage (omitempty), got: %q", out)
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

// TestPrintPreflightSanitizesControlChars proves the pre-flight echo stays a
// SINGLE line even when a resolved value carries a control character — e.g. a
// self-supplied fully-qualified --model whose trailing segment contains a
// newline (the fully-qualified passthrough is trusted verbatim by resolution).
// This closes the stderr line-injection vector at the output boundary.
func TestPrintPreflightSanitizesControlChars(t *testing.T) {
	var buf bytes.Buffer
	printPreflight(&buf, eval.ResolvedTarget{
		Project:  "proj",
		Location: "us-central1",
		// bare AND fully-qualified-trailing-segment newline injection attempts.
		Model: "gemini-2.5-flash\ninjected: evil",
		Path:  "native",
	})
	out := buf.String()
	// Exactly one trailing newline, no interior newlines/CRs → one line.
	if strings.Count(out, "\n") != 1 {
		t.Errorf("pre-flight is not a single line, got %q", out)
	}
	if strings.Contains(strings.TrimRight(out, "\n"), "\n") || strings.Contains(out, "\r") {
		t.Errorf("control char leaked into echo: %q", out)
	}
	if strings.Contains(out, "injected: evil\n") && strings.Count(out, "\n") != 1 {
		t.Errorf("injected line survived: %q", out)
	}
	// The visible characters are preserved (only the control char is dropped).
	if !strings.Contains(out, "model=gemini-2.5-flashinjected: evil") {
		t.Errorf("unexpected sanitized model rendering: %q", out)
	}
}
