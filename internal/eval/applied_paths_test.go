package eval

// applied_paths_test.go closes two behavioral gaps around Result.Applied that the
// PR's statement coverage hides (eval-results-store-design §4.3):
//   - Engine.Run stamps Applied on EVERY path, including a dispatch/API ERROR
//     (the "set on both the success and timing path" contract), not just success.
//   - the pointer+omitempty JSON contract: a Result NOT produced by Run (nil
//     Applied) omits "applied" entirely, keeping existing golden output unchanged,
//     while a populated Applied is serialized.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval/evaltest"
)

// A dispatch/API error must NOT drop the autorater-as-applied: Run stamps Applied
// on the error path too, so the results store records WHAT actually ran even for a
// failed run. This guards the "set on EVERY path" contract that success-only tests
// leave unasserted (a regression to `return Result{}, err` keeps 100% of the
// existing tests green).
func TestRun_PopulatesApplied_OnDispatchError(t *testing.T) {
	fc := (&evaltest.FakeEvaluationClient{}).PushError(errors.New("boom: judge unavailable"))
	eng := NewEngine(fc, "my-project", "us-central1")

	res, err := eng.Run(context.Background(), pointwiseTemplate(), helpfulnessInstance())
	if err == nil {
		t.Fatal("Run must return the dispatch error (got nil)")
	}
	if res.Applied == nil {
		t.Fatal("Run must populate Result.Applied even on a failed run (got nil)")
	}
	got := res.Applied
	if got.Model != "gemini-2.5-flash" {
		t.Errorf("Applied.Model = %q, want gemini-2.5-flash", got.Model)
	}
	if got.EffectiveHost != "regional" {
		t.Errorf("Applied.EffectiveHost = %q, want regional", got.EffectiveHost)
	}
	if got.Location != "us-central1" {
		t.Errorf("Applied.Location = %q, want us-central1", got.Location)
	}
	if got.ModelSource != modelSourceTemplate {
		t.Errorf("Applied.ModelSource = %q, want %q", got.ModelSource, modelSourceTemplate)
	}
}

// The pointer+omitempty choice exists so a Result NOT produced by Run (a literal
// in a test or a renderer golden) serializes EXACTLY as before — the "applied" key
// must be ABSENT when Applied is nil. This nails the golden-preservation contract
// at the unit level instead of relying only on the distant cmd golden suite.
func TestResult_JSON_OmitsAppliedWhenNil(t *testing.T) {
	b, err := json.Marshal(Result{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), "applied") {
		t.Errorf("nil Applied must be omitted from JSON, got %s", b)
	}
}

// The other half of the contract: a Run-populated Applied IS serialized, under the
// documented "applied" key, so the results store can persist it.
func TestResult_JSON_IncludesAppliedWhenSet(t *testing.T) {
	res := Result{Applied: &AppliedAutorater{
		Model:         "gemini-2.5-flash",
		EffectiveHost: "regional",
		Location:      "us-central1",
		ModelSource:   modelSourceTemplate,
	}}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"applied"`) {
		t.Errorf("populated Applied must be serialized under \"applied\", got %s", b)
	}
	if !strings.Contains(string(b), "gemini-2.5-flash") {
		t.Errorf("Applied.Model must be serialized, got %s", b)
	}
}
