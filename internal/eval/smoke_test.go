package eval

// smoke_test.go is the creds-free, end-to-end engine smoke (R-SMOKE). It drives
// Engine.Run for EVERY metric kind through the two narrow client seams — using
// the reusable evaltest.FakeEvaluationClient / evaltest.FakeGenaiClient (internal/eval/evaltest) so no
// GCP credentials, network, or live clients are involved — and asserts the mapped
// Result for each. The renderer half (that each of these Results renders through
// the actual CLI renderers without error) lives in cmd/mizan/smoke_test.go, since
// the renderers are unexported in package main and cannot be reached from here;
// together the two files assert both the engine mapping AND the renderer output
// for every kind, creds-free, under the normal `go test ./...`.
//
// These are deliberately NOT gated behind a build tag: they must run in ordinary
// CI (the R-CI pipeline) with no project, secret, or network access.

import (
	"context"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"

	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
)

const (
	smokeProject  = "smoke-project"
	smokeLocation = "us-central1"
)

// TestSmokePointwiseNative drives the native pointwise path end-to-end and
// asserts the {score, explanation} mapping.
func TestSmokePointwiseNative(t *testing.T) {
	fc := &evaltest.FakeEvaluationClient{}
	fc.PushResponse(evaltest.NewPointwiseResponse(4.5, "Clear and correct."))
	eng := NewEngine(fc, smokeProject, smokeLocation)

	res, err := eng.Run(context.Background(), pointwiseTemplate(), Instance{
		Fields: map[string]AssetRef{
			"response": {Modality: registry.ModalityText, Text: "To reset your password, click the link."},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Score == nil || *res.Score != 4.5 {
		t.Errorf("Score = %v, want 4.5", res.Score)
	}
	if res.Explanation != "Clear and correct." {
		t.Errorf("Explanation = %q", res.Explanation)
	}
	// Native path: no token usage, exactly one EvaluateInstances call.
	if res.Stats.TokenUsage != nil {
		t.Errorf("native pointwise carried token usage: %+v", res.Stats.TokenUsage)
	}
	if fc.Calls() != 1 {
		t.Errorf("EvaluateInstances calls = %d, want 1", fc.Calls())
	}
}

// TestSmokeRubricNative drives the native rubric path (no --rubric-detail) and
// asserts it maps to {score, explanation} via the same native seam as pointwise.
func TestSmokeRubricNative(t *testing.T) {
	fc := &evaltest.FakeEvaluationClient{}
	fc.PushResponse(evaltest.NewPointwiseResponse(3.0, "Clear but slightly informal."))
	eng := NewEngine(fc, smokeProject, smokeLocation)

	res, err := eng.Run(context.Background(), rubricTemplate(), Instance{
		Fields: map[string]AssetRef{
			"copy": {Modality: registry.ModalityText, Text: "Save big today."},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Score == nil || *res.Score != 3.0 {
		t.Errorf("Score = %v, want 3.0", res.Score)
	}
	if res.Explanation != "Clear but slightly informal." {
		t.Errorf("Explanation = %q", res.Explanation)
	}
	if res.RubricDetail {
		t.Error("native rubric should not set RubricDetail")
	}
	if fc.Calls() != 1 {
		t.Errorf("EvaluateInstances calls = %d, want 1", fc.Calls())
	}
}

// TestSmokeRubricDetailGenai drives the rubric per-criterion transparency path
// (KindRubric + WithRubricDetail) through the genai structured-output seam. It
// asserts RubricDetail==true, per_criterion is present, overall_score is surfaced
// to Result.Score, and out-of-range judge scores are clamped into the [1,5]
// scale.
func TestSmokeRubricDetailGenai(t *testing.T) {
	// overall_score 9 and per-criterion scores 7 / 0 are out of the [1,5] scale
	// and must be clamped to 5 / 5 / 1 respectively.
	judgeJSON := `{
		"per_criterion": [
			{"group":"clarity","criterion":"The message is unambiguous","score":7,"rationale":"over-scale"},
			{"group":"clarity","criterion":"No jargon","score":0,"rationale":"under-scale"},
			{"group":"tone","criterion":"Matches a professional brand voice","score":4,"rationale":"in-scale"}
		],
		"overall_score": 9,
		"explanation": "Strong overall."
	}`
	fg := &evaltest.FakeGenaiClient{}
	fg.PushJSON(judgeJSON, nil)
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, smokeProject, smokeLocation, WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), rubricTemplate(), Instance{
		Fields: map[string]AssetRef{
			"copy": {Modality: registry.ModalityText, Text: "Save big today."},
		},
	}, WithRubricDetail(1, 5))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.RubricDetail {
		t.Error("RubricDetail = false, want true")
	}
	// overall_score clamped to 5 and surfaced to Score.
	if res.Score == nil || *res.Score != 5 {
		t.Errorf("Score = %v, want 5 (overall_score clamped)", res.Score)
	}
	if f, ok := res.CustomOutput["overall_score"].(float64); !ok || f != 5 {
		t.Errorf("CustomOutput[overall_score] = %v, want 5.0", res.CustomOutput["overall_score"])
	}
	pc, ok := res.CustomOutput["per_criterion"].([]any)
	if !ok || len(pc) != 3 {
		t.Fatalf("per_criterion = %v, want 3 entries", res.CustomOutput["per_criterion"])
	}
	// Per-criterion scores are coerced to int and clamped into [1,5].
	wantScores := []int{5, 1, 4}
	for i, item := range pc {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("per_criterion[%d] not an object: %v", i, item)
		}
		if got, ok := m["score"].(int); !ok || got != wantScores[i] {
			t.Errorf("per_criterion[%d].score = %v, want %d", i, m["score"], wantScores[i])
		}
	}
	// genai path: exactly one GenerateContent call, targeting the bare model id.
	if fg.Calls() != 1 {
		t.Fatalf("GenerateContent calls = %d, want 1", fg.Calls())
	}
	if got := fg.LastCall().Model; got != "gemini-2.5-flash" {
		t.Errorf("genai model = %q, want gemini-2.5-flash", got)
	}
}

// TestSmokeCustomSchemaGenai drives the custom_schema genai path and asserts the
// JSON output is parsed into CustomOutput, RawOutput is set, and token usage is
// captured into Stats (genai-only).
func TestSmokeCustomSchemaGenai(t *testing.T) {
	fg := &evaltest.FakeGenaiClient{}
	fg.PushJSON(
		`{"overall_score":8.5,"compliant":true,"flagged_issues":[],"explanation":"On brand."}`,
		evaltest.NewTokenUsage(120, 34, 154),
	)
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, smokeProject, smokeLocation, WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{
			"creative": {Modality: registry.ModalityText, Text: "A calm blue banner."},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CustomOutput["compliant"] != true {
		t.Errorf("compliant = %v, want true", res.CustomOutput["compliant"])
	}
	if got := res.CustomOutput["overall_score"]; got != 8.5 {
		t.Errorf("overall_score = %v, want 8.5", got)
	}
	if len(res.RawOutput) != 1 {
		t.Fatalf("RawOutput = %v, want one raw string", res.RawOutput)
	}
	tu := res.Stats.TokenUsage
	if tu == nil {
		t.Fatal("Stats.TokenUsage is nil, want genai token usage")
	}
	if tu.PromptTokens != 120 || tu.CandidatesTokens != 34 || tu.TotalTokens != 154 {
		t.Errorf("TokenUsage = %+v, want {120,34,154}", tu)
	}
}

// TestSmokePairwiseNative drives the native pairwise path and asserts the choice
// enum maps to Result.PairwiseChoice.
func TestSmokePairwiseNative(t *testing.T) {
	fc := &evaltest.FakeEvaluationClient{}
	fc.PushResponse(evaltest.NewPairwiseResponse(aiplatformpb.PairwiseChoice_CANDIDATE, "Candidate is more helpful."))
	eng := NewEngine(fc, smokeProject, smokeLocation)

	res, err := eng.Run(context.Background(), pairwiseTemplate(), pairwiseInstance())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PairwiseChoice != "CANDIDATE" {
		t.Errorf("PairwiseChoice = %q, want CANDIDATE", res.PairwiseChoice)
	}
	if res.Explanation != "Candidate is more helpful." {
		t.Errorf("Explanation = %q", res.Explanation)
	}
	if fc.Calls() != 1 {
		t.Errorf("EvaluateInstances calls = %d, want 1", fc.Calls())
	}
}

// TestSmokeGenaiRetryPath proves the fake-client error scripting drives the genai
// retry/backoff path creds-free: a scripted RESOURCE_EXHAUSTED followed by a
// success yields the mapped Result after exactly two calls. This is the seam
// R-GAPS builds error-path coverage on.
func TestSmokeGenaiRetryPath(t *testing.T) {
	fg := &evaltest.FakeGenaiClient{}
	fg.PushError(evaltest.NewResourceExhausted())
	fg.PushJSON(`{"overall_score":7,"compliant":true,"flagged_issues":[],"explanation":"ok"}`, nil)
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, smokeProject, smokeLocation, WithGenaiClient(fg))
	fastRetry(eng) // keep the backoff instant for the scripted 429 → success path

	res, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{
			"creative": {Modality: registry.ModalityText, Text: "A calm blue banner."},
		},
	})
	if err != nil {
		t.Fatalf("Run after retry: %v", err)
	}
	if res.CustomOutput["compliant"] != true {
		t.Errorf("compliant = %v, want true", res.CustomOutput["compliant"])
	}
	if fg.Calls() != 2 {
		t.Errorf("GenerateContent calls = %d, want 2 (one 429 + one success)", fg.Calls())
	}
}
