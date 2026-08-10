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

// TestRenderResultPairwiseJSONOmitsWarningsWhenNil is the flip-OFF counterpart to
// the renderResult_pairwise_json golden (flip ON, "warnings" present): a pairwise
// Result with no Warnings must NOT serialize a "warnings" key (omitempty), so a
// JSON consumer sees the caveat if and only if flip is in effect (eval-triage #4).
func TestRenderResultPairwiseJSONOmitsWarningsWhenNil(t *testing.T) {
	prev := outputFormat
	outputFormat = outputJSON
	defer func() { outputFormat = prev }()

	res := eval.Result{PairwiseChoice: "CANDIDATE", Explanation: "Candidate is more helpful."}
	var buf bytes.Buffer
	if err := renderResult(&buf, res, false); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "warnings") {
		t.Errorf("pairwise JSON without Warnings should omit the \"warnings\" key, got: %q", out)
	}
	if !strings.Contains(out, `"PairwiseChoice": "CANDIDATE"`) {
		t.Errorf("pairwise JSON missing the Choice: %q", out)
	}
}

// TestRenderResultPairwiseTableKeepsWarningsOffStdout proves the text-mode
// contract: renderResult (stdout) prints the Choice and Explanation but never the
// Warnings text and never a "Score:" line for a pairwise result. The flip caveat
// is emitted to stderr by the command (eval.go), so leaking it into the stdout
// table would corrupt machine-parsed output; this guards that separation and the
// Score-line suppression together (eval-triage #4).
func TestRenderResultPairwiseTableKeepsWarningsOffStdout(t *testing.T) {
	prev := outputFormat
	outputFormat = outputTable
	defer func() { outputFormat = prev }()

	res := eval.Result{
		PairwiseChoice: "CANDIDATE",
		Explanation:    "Candidate is more helpful.",
		Warnings:       []string{"pairwise flip is enabled: the Choice is the de-biased, authoritative verdict."},
	}
	var buf bytes.Buffer
	if err := renderResult(&buf, res, false); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Choice:") || !strings.Contains(out, "CANDIDATE") {
		t.Errorf("pairwise table missing Choice: %q", out)
	}
	if strings.Contains(out, "Score:") {
		t.Errorf("pairwise table should suppress the Score line, got: %q", out)
	}
	if strings.Contains(out, "flip is enabled") || strings.Contains(out, "authoritative") {
		t.Errorf("warnings must not leak into the stdout table (they go to stderr), got: %q", out)
	}
}

// rubricDetailResult builds a rubric per-criterion result as the engine would
// produce it (overall_score mapped to Score; the full structure in CustomOutput).
func rubricDetailResult() eval.Result {
	score := float32(4)
	return eval.Result{
		Score:        &score,
		RubricDetail: true,
		CustomOutput: map[string]any{
			"per_criterion": []any{
				map[string]any{"group": "clarity", "criterion": "The message is unambiguous", "score": 4, "rationale": "mostly clear"},
				map[string]any{"group": "tone", "criterion": "Matches a professional brand voice", "score": 3, "rationale": "a bit casual"},
			},
			"overall_score": float64(4),
			"explanation":   "Solid ad copy.",
		},
	}
}

// TestRenderResultRubricDetailTable verifies the dedicated table renderer prints
// the overall Score + Explanation (from CustomOutput) plus a per-criterion table
// with group/criterion/score/rationale, and does NOT fall back to the generic
// CustomOutput[...] blob rendering.
func TestRenderResultRubricDetailTable(t *testing.T) {
	outputFormat = outputTable
	var buf bytes.Buffer
	if err := renderResult(&buf, rubricDetailResult(), false); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Score:", "Explanation:", "Solid ad copy.",
		"Per-criterion:", "GROUP", "CRITERION", "SCORE", "RATIONALE",
		"clarity", "The message is unambiguous", "mostly clear", "tone",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rubric-detail table missing %q\noutput:\n%s", want, out)
		}
	}
	// The generic CustomOutput fallback must NOT be used for rubric-detail.
	if strings.Contains(out, "CustomOutput[per_criterion]") {
		t.Errorf("generic CustomOutput rendering leaked into rubric-detail table:\n%s", out)
	}
}

// TestRenderResultGenericCustomOutputUnchanged proves a non-rubric custom_schema
// result still uses the generic CustomOutput[...] rendering (no regression).
func TestRenderResultGenericCustomOutputUnchanged(t *testing.T) {
	outputFormat = outputTable
	res := eval.Result{
		Explanation:  "ok",
		CustomOutput: map[string]any{"compliant": true, "overall_score": float64(8)},
	}
	var buf bytes.Buffer
	if err := renderResult(&buf, res, false); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "CustomOutput[compliant]") {
		t.Errorf("generic CustomOutput rendering regressed:\n%s", out)
	}
	if strings.Contains(out, "Per-criterion:") {
		t.Errorf("non-rubric result should not use the per-criterion renderer:\n%s", out)
	}
}

// TestRenderResultPerCriterionNotMisrouted proves routing is on the explicit
// Result.RubricDetail signal, NOT the CustomOutput shape: a custom_schema result
// that happens to carry a "per_criterion" array (with RubricDetail=false) is
// rendered by the GENERIC CustomOutput renderer, not the rubric per-criterion
// table (O2).
func TestRenderResultPerCriterionNotMisrouted(t *testing.T) {
	outputFormat = outputTable
	res := eval.Result{
		Explanation:  "custom schema, not rubric",
		RubricDetail: false, // explicit: this is NOT a rubric-detail result
		CustomOutput: map[string]any{
			"per_criterion": []any{
				map[string]any{"group": "g", "criterion": "c", "score": 3, "rationale": "r"},
			},
		},
	}
	var buf bytes.Buffer
	if err := renderResult(&buf, res, false); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "Per-criterion:") {
		t.Errorf("custom_schema with a per_criterion array was misrouted to the rubric table:\n%s", out)
	}
	if !strings.Contains(out, "CustomOutput[per_criterion]") {
		t.Errorf("generic CustomOutput rendering expected for a non-rubric result:\n%s", out)
	}
}

// TestRenderResultRubricDetailSanitizesCells proves judge-controlled cell values
// (criterion, rationale, explanation) are stripped of control chars and ANSI
// escapes before hitting stdout, and that a newline in a cell does NOT spill the
// criterion across multiple table rows (security O1).
func TestRenderResultRubricDetailSanitizesCells(t *testing.T) {
	outputFormat = outputTable
	res := eval.Result{
		RubricDetail: true,
		CustomOutput: map[string]any{
			"per_criterion": []any{
				map[string]any{
					"group":     "clarity",
					"criterion": "line1\nline2", // embedded newline
					"score":     4,
					"rationale": "red\x1b[31mALERT\x1b[0m\ttab", // ANSI + tab
				},
			},
			"explanation": "expl\x1b[1mbold\x1b[0m\nsecond",
		},
	}
	var buf bytes.Buffer
	if err := renderResult(&buf, res, false); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	out := buf.String()

	// No raw control chars or ESC bytes survive.
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("ANSI escape byte leaked to output: %q", out)
	}
	if strings.Contains(out, "\r") {
		t.Errorf("carriage return leaked to output: %q", out)
	}
	// The visible letters survive; only the escape sequence is removed.
	for _, want := range []string{"redALERTtab", "line1line2", "explboldsecond"} {
		if !strings.Contains(out, want) {
			t.Errorf("sanitized text missing %q\noutput:\n%s", want, out)
		}
	}
	// One header row + exactly one data row (the newline in criterion must NOT
	// create a second criterion row). Count non-empty lines under "Per-criterion:".
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var dataRows int
	seenHeader := false
	for _, ln := range lines {
		if strings.Contains(ln, "GROUP") && strings.Contains(ln, "CRITERION") {
			seenHeader = true
			continue
		}
		if seenHeader && strings.TrimSpace(ln) != "" {
			dataRows++
		}
	}
	if dataRows != 1 {
		t.Errorf("expected exactly 1 per-criterion data row, got %d\noutput:\n%s", dataRows, out)
	}
}

// TestRenderResultRubricDetailJSON verifies the JSON output contract: the FULL
// structure (per_criterion + overall_score + explanation) appears in --output
// json.
func TestRenderResultRubricDetailJSON(t *testing.T) {
	prev := outputFormat
	outputFormat = outputJSON
	defer func() { outputFormat = prev }()

	var buf bytes.Buffer
	if err := renderResult(&buf, rubricDetailResult(), false); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"per_criterion", "overall_score", "explanation",
		"group", "criterion", "score", "rationale",
		"The message is unambiguous",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rubric-detail JSON missing %q\noutput:\n%s", want, out)
		}
	}
}

// TestRenderResultRubricDetailPartialEntries proves the per-criterion table
// renderer is defensive: a non-map entry is skipped, and an entry missing
// fields (e.g. no rationale) renders blanks rather than panicking. It also
// covers a nil overall Score rendering as "(none)" and the explanation falling
// back to CustomOutput["explanation"].
func TestRenderResultRubricDetailPartialEntries(t *testing.T) {
	outputFormat = outputTable
	res := eval.Result{
		// Score deliberately nil (judge omitted overall_score upstream).
		RubricDetail: true,
		CustomOutput: map[string]any{
			"per_criterion": []any{
				map[string]any{"group": "clarity", "criterion": "has all", "score": 4, "rationale": "good"},
				map[string]any{"group": "tone", "criterion": "no rationale", "score": 2}, // missing rationale
				"not-a-map-entry", // must be skipped, not panic
			},
			"explanation": "from custom output",
		},
	}
	var buf bytes.Buffer
	if err := renderResult(&buf, res, false); err != nil {
		t.Fatalf("renderResult: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Score:\t(none)") && !strings.Contains(out, "Score: (none)") {
		// tab or space separated depending on tabwriter; accept either.
		if !strings.Contains(out, "(none)") {
			t.Errorf("nil Score should render (none):\n%s", out)
		}
	}
	if !strings.Contains(out, "from custom output") {
		t.Errorf("explanation fallback to CustomOutput missing:\n%s", out)
	}
	for _, want := range []string{"has all", "good", "no rationale"} {
		if !strings.Contains(out, want) {
			t.Errorf("partial per-criterion table missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not-a-map-entry") {
		t.Errorf("non-map per_criterion entry should be skipped, leaked:\n%s", out)
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
