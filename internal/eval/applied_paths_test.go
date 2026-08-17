package eval

// applied_paths_test.go closes two behavioral gaps around Result.Applied that the
// PR's statement coverage hides (eval-results-store-design §4.3):
//   - Engine.Run populates Applied ONLY on success and leaves it nil on EVERY
//     error path (dispatch/API errors and validation errors alike), so a failed
//     run has no applied autorater.
//   - the eval command's -o json output is byte-for-byte unchanged: Result.Applied
//     is tagged json:"-", so even a Run-produced Result (Applied non-nil) never
//     leaks an "applied" key into the JSON the CLI emits (§6).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval/evaltest"
)

// A dispatch/API error must leave Result.Applied nil: "a failed run has no applied
// autorater" holds uniformly across every error path (O2). This guards the
// success-only contract — a regression that stamped Applied before checking err
// would set it here.
func TestRun_LeavesAppliedNil_OnDispatchError(t *testing.T) {
	fc := (&evaltest.FakeEvaluationClient{}).PushError(errors.New("boom: judge unavailable"))
	eng := NewEngine(fc, "my-project", "us-central1")

	res, err := eng.Run(context.Background(), pointwiseTemplate(), helpfulnessInstance())
	if err == nil {
		t.Fatal("Run must return the dispatch error (got nil)")
	}
	if res.Applied != nil {
		t.Errorf("Run must leave Result.Applied nil on a failed run, got %+v", res.Applied)
	}
}

// R1 regression: the eval command's -o json output (renderResult -> printJSON,
// which json-marshals eval.Result) must contain NO "applied" key even for a
// Run-produced Result whose Applied is non-nil. The json:"-" tag pins this so
// adding Applied cannot change the existing command output (§6). The results store
// still reads res.Applied as a Go struct field, unaffected.
func TestRunResult_JSON_HasNoAppliedKey(t *testing.T) {
	fc := (&evaltest.FakeEvaluationClient{}).PushResponse(evaltest.NewPointwiseResponse(4.5, "ok"))
	eng := NewEngine(fc, "my-project", "us-central1")

	res, err := eng.Run(context.Background(), pointwiseTemplate(), helpfulnessInstance())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Applied == nil {
		t.Fatal("precondition: a successful Run must populate Result.Applied (got nil)")
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), "applied") {
		t.Errorf("eval -o json output must not contain an \"applied\" key, got %s", b)
	}
}

// The same guarantee holds for a Result NOT produced by Run (nil Applied): a
// literal in a test or a renderer golden serializes exactly as before, with no
// "applied" key.
func TestResult_JSON_OmitsAppliedWhenNil(t *testing.T) {
	b, err := json.Marshal(Result{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), "applied") {
		t.Errorf("nil Applied must be omitted from JSON, got %s", b)
	}
}
