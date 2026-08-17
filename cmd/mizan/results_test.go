package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/results"
)

func float32p(f float32) *float32 { return &f }

func sampleStoredResult() results.Result {
	return results.Result{
		RunID: "01J000000000000000000RUN01",
		RunAt: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
		Template: results.TemplateRef{
			ID:          "google-brand/quality",
			Version:     "1.2.0",
			ContentHash: "abc123",
			Kind:        registry.KindPointwise,
		},
		Autorater: results.AppliedAutorater{
			Model:         "publishers/google/models/gemini-2.5-pro",
			EffectiveHost: "regional",
			Location:      "us-central1",
			ModelSource:   "template",
		},
		Inputs: []results.StoredInput{{
			Field:       "response",
			Modality:    registry.ModalityText,
			ContentHash: "deadbeef",
			Mode:        results.ModeInline,
			Inline:      "the answer",
		}},
		Outcome: results.Outcome{Score: float32p(4), Explanation: "good"},
	}
}

// TestParseSince proves both RFC3339 and bare-date forms parse, and a garbage
// value is rejected locally.
func TestParseSince(t *testing.T) {
	if _, err := parseSince("2026-08-17T12:00:00Z"); err != nil {
		t.Errorf("RFC3339 parse failed: %v", err)
	}
	d, err := parseSince("2026-08-17")
	if err != nil {
		t.Fatalf("date parse failed: %v", err)
	}
	if d.Hour() != 0 || d.Location() != time.UTC {
		t.Errorf("bare date not midnight UTC: %v", d)
	}
	if _, err := parseSince("not-a-time"); err == nil {
		t.Error("expected an error for a garbage --since value")
	}
}

// TestRenderResultListTable proves the table carries the run id, metric@version,
// outcome, and model.
func TestRenderResultListTable(t *testing.T) {
	prev := outputFormat
	outputFormat = outputTable
	defer func() { outputFormat = prev }()

	var out, errb bytes.Buffer
	if err := renderResultList(&out, &errb, []results.Result{sampleStoredResult()}); err != nil {
		t.Fatalf("renderResultList: %v", err)
	}
	s := out.String()
	for _, want := range []string{"01J000000000000000000RUN01", "google-brand/quality@1.2.0", "gemini-2.5-pro"} {
		if !strings.Contains(s, want) {
			t.Errorf("table missing %q:\n%s", want, s)
		}
	}
}

// TestRenderResultListEmpty proves an empty set prints a friendly note on stderr
// (table mode) and `[]` in JSON mode (never null).
func TestRenderResultListEmpty(t *testing.T) {
	prev := outputFormat
	defer func() { outputFormat = prev }()

	outputFormat = outputTable
	var out, errb bytes.Buffer
	if err := renderResultList(&out, &errb, nil); err != nil {
		t.Fatalf("renderResultList: %v", err)
	}
	if !strings.Contains(errb.String(), "no results found") {
		t.Errorf("expected a friendly empty note on stderr, got: %q", errb.String())
	}

	outputFormat = outputJSON
	out.Reset()
	errb.Reset()
	if err := renderResultList(&out, &errb, nil); err != nil {
		t.Fatalf("renderResultList json: %v", err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("empty JSON = %q, want []", out.String())
	}
}

// TestRenderResultListJSONRoundTrip proves -o json emits the whole []Result.
func TestRenderResultListJSONRoundTrip(t *testing.T) {
	prev := outputFormat
	outputFormat = outputJSON
	defer func() { outputFormat = prev }()

	var out, errb bytes.Buffer
	if err := renderResultList(&out, &errb, []results.Result{sampleStoredResult()}); err != nil {
		t.Fatalf("renderResultList: %v", err)
	}
	var got []results.Result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json round-trip: %v\n%s", err, out.String())
	}
	if len(got) != 1 || got[0].Template.ContentHash != "abc123" {
		t.Errorf("json did not round-trip: %+v", got)
	}
}

// TestRenderResultDetailProvenance proves show renders the full provenance:
// template id+version+contentHash, applied autorater (model+host+location+source),
// inputs, and outcome.
func TestRenderResultDetailProvenance(t *testing.T) {
	prev := outputFormat
	outputFormat = outputTable
	defer func() { outputFormat = prev }()

	r := sampleStoredResult()
	var out bytes.Buffer
	if err := renderResultDetail(&out, &r); err != nil {
		t.Fatalf("renderResultDetail: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"abc123", // content hash
		"1.2.0",  // version
		"publishers/google/models/gemini-2.5-pro", // resolved model
		"regional",    // effective host
		"us-central1", // location
		"template",    // model source
		"response",    // input field
		"the answer",  // inline input value
		"Score:",      // outcome
	} {
		if !strings.Contains(s, want) {
			t.Errorf("detail missing %q:\n%s", want, s)
		}
	}
}

// TestRenderResultDetailRubricScaleEmpty proves a rubric result with no recorded
// scale (the documented registry-provenance limitation) renders a clear
// "(not recorded)" rather than crashing or synthesizing a scale.
func TestRenderResultDetailRubricScaleEmpty(t *testing.T) {
	prev := outputFormat
	outputFormat = outputTable
	defer func() { outputFormat = prev }()

	r := sampleStoredResult()
	r.Template.Kind = registry.KindRubric
	r.Rubric = &results.RubricRef{Method: "authored"}
	var out bytes.Buffer
	if err := renderResultDetail(&out, &r); err != nil {
		t.Fatalf("renderResultDetail: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "Rubric Method:") || !strings.Contains(s, "authored") {
		t.Errorf("missing rubric method:\n%s", s)
	}
	if !strings.Contains(s, "(not recorded)") {
		t.Errorf("empty rubric scale should render (not recorded):\n%s", s)
	}
}

// TestRenderResultDetailRubricProvenance proves an adaptive-generated rubric
// result renders the provenance-derived fields (method, generator model, recipe,
// and the distinct per-criterion origins) surfaced by RubricRef.
func TestRenderResultDetailRubricProvenance(t *testing.T) {
	prev := outputFormat
	outputFormat = outputTable
	defer func() { outputFormat = prev }()

	r := sampleStoredResult()
	r.Template.Kind = registry.KindRubric
	r.Rubric = &results.RubricRef{
		Method:         "adaptive-generated",
		GeneratorModel: "gemini-2.5-pro",
		Recipe:         "general_quality_v1",
		Origins:        []string{"adaptive-generated", "hand-authored"},
	}
	var out bytes.Buffer
	if err := renderResultDetail(&out, &r); err != nil {
		t.Fatalf("renderResultDetail: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"Rubric Method:", "adaptive-generated",
		"Rubric GeneratorModel:", "gemini-2.5-pro",
		"Rubric Recipe:", "general_quality_v1",
		"Rubric Origins:", "hand-authored",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("detail missing %q:\n%s", want, s)
		}
	}
}

// TestRenderResultDetailSanitizesUntrusted proves judge/input-derived text is run
// through sanitizeCell (ANSI escapes + control chars stripped) before it reaches
// a terminal cell (security O1).
func TestRenderResultDetailSanitizesUntrusted(t *testing.T) {
	prev := outputFormat
	outputFormat = outputTable
	defer func() { outputFormat = prev }()

	r := sampleStoredResult()
	r.Outcome.Explanation = "evil\x1b[31m\x07danger\nnewline"
	r.Inputs[0].Inline = "inline\x1b[0mescape"
	var out bytes.Buffer
	if err := renderResultDetail(&out, &r); err != nil {
		t.Fatalf("renderResultDetail: %v", err)
	}
	s := out.String()
	if strings.Contains(s, "\x1b") || strings.Contains(s, "\x07") {
		t.Errorf("unsanitized control/escape bytes reached output: %q", s)
	}
}

// TestRenderResultListSanitizesUntrusted proves list-table cells (metric id,
// model) are sanitized too.
func TestRenderResultListSanitizesUntrusted(t *testing.T) {
	prev := outputFormat
	outputFormat = outputTable
	defer func() { outputFormat = prev }()

	r := sampleStoredResult()
	r.Autorater.Model = "model\x1b[31mX"
	var out, errb bytes.Buffer
	if err := renderResultList(&out, &errb, []results.Result{r}); err != nil {
		t.Fatalf("renderResultList: %v", err)
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Errorf("unsanitized escape reached list output: %q", out.String())
	}
}

// TestResultsShowNotFound proves `results show` on an unknown run id maps
// results.ErrNotFound to a crisp, non-nil error (non-zero exit).
func TestResultsShowNotFound(t *testing.T) {
	t.Setenv("MIZAN_RESULTS_BACKEND", "sqlite")
	t.Setenv("MIZAN_RESULTS_DB", t.TempDir()+"/results.db")

	cmd := newResultsShowCmd()
	cmd.SetContext(context.Background())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.RunE(cmd, []string{"01J000000000000000000MISSING"})
	if err == nil {
		t.Fatal("expected a non-nil error for an unknown run id")
	}
	if !strings.Contains(err.Error(), "no result with run id") {
		t.Errorf("error = %v, want a crisp not-found message", err)
	}
}
