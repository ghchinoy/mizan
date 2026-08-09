package eval

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/registry"
)

// These tests close network-free, cgo-free gaps in the WI-P1-5 suite: the genai
// backoff context-cancellation path, full custom-output key mapping, custom_schema
// input-validation and empty-response error paths, the rubric path's OWN result
// mapping/error branches (rubric has separate code from pointwise), and the
// toGenaiInlinePart error branches. All use fakes; none touch the network.

// TestRunCustomSchemaContextCanceledDuringBackoff proves that a canceled context
// aborts the retry loop with the context error rather than sleeping out the full
// backoff schedule. The brief calls out the backoff/retry path explicitly.
func TestRunCustomSchemaContextCanceledDuringBackoff(t *testing.T) {
	fg := &fakeGenai{failN: 99} // always RESOURCE_EXHAUSTED
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
	fastRetry(eng)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before the first backoff sleep

	_, err := eng.Run(ctx, customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{"creative": {Text: "x"}},
	})
	if err == nil {
		t.Fatal("expected an error when the context is canceled")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error = %v, want context cancellation", err)
	}
	// The first attempt runs (fake ignores ctx), then the canceled ctx short-circuits
	// the sleep before any further attempt: exactly one call, not maxAttempts.
	if fg.calls != 1 {
		t.Errorf("calls = %d, want 1 (canceled before retry)", fg.calls)
	}
}

// TestRunCustomSchemaAllRequiredKeys asserts every required key of the schema is
// present in CustomOutput with the correct decoded Go type. The existing success
// test only spot-checks two keys; the brief requires "all required keys".
func TestRunCustomSchemaAllRequiredKeys(t *testing.T) {
	fg := &fakeGenai{respText: `{"overall_score":7,"compliant":true,"flagged_issues":["a","b"],"explanation":"ok"}`}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{"creative": {Text: "x"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, key := range []string{"overall_score", "compliant", "flagged_issues", "explanation"} {
		if _, ok := res.CustomOutput[key]; !ok {
			t.Errorf("CustomOutput missing required key %q", key)
		}
	}
	if got := res.CustomOutput["overall_score"]; got != float64(7) {
		t.Errorf("overall_score = %v (%T), want float64(7)", got, got)
	}
	issues, ok := res.CustomOutput["flagged_issues"].([]any)
	if !ok || len(issues) != 2 {
		t.Errorf("flagged_issues = %v, want a 2-element array", res.CustomOutput["flagged_issues"])
	}
	if res.CustomOutput["explanation"] != "ok" {
		t.Errorf("explanation = %v, want %q", res.CustomOutput["explanation"], "ok")
	}
}

// TestRunCustomSchemaEmptyResponse exercises the empty-model-output error path
// through the engine (parseCustomOutput's empty branch).
func TestRunCustomSchemaEmptyResponse(t *testing.T) {
	fg := &fakeGenai{respText: "   "} // whitespace -> empty after trim
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	_, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{"creative": {Text: "x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty-response error, got %v", err)
	}
}

// TestRunCustomSchemaNoResponseSchema proves a custom_schema template with no
// ResponseSchema fails fast before any genai call.
func TestRunCustomSchemaNoResponseSchema(t *testing.T) {
	fg := &fakeGenai{respText: "{}"}
	tmpl := customSchemaTemplate()
	tmpl.ResponseSchema = nil
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"creative": {Text: "x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "response schema") {
		t.Fatalf("want no-response-schema error, got %v", err)
	}
	if fg.calls != 0 {
		t.Error("genai client must not be called when the schema is missing")
	}
}

// TestRunRubricClientError proves the rubric path (separate code from pointwise)
// wraps the backend error with rubric-specific context.
func TestRunRubricClientError(t *testing.T) {
	fc := &fakeClient{err: context.DeadlineExceeded}
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), rubricTemplate(), Instance{
		Fields: map[string]AssetRef{"copy": {Text: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error when the rubric client fails")
	}
	if !strings.Contains(err.Error(), "rubric") {
		t.Errorf("error should carry rubric context: %v", err)
	}
}

// TestRunRubricNoResultInResponse proves the rubric path reports a missing
// pointwise result rather than silently mapping a zero Result. This is rubric's
// OWN mapping branch, distinct from the pointwise one.
func TestRunRubricNoResultInResponse(t *testing.T) {
	fc := &fakeClient{resp: &aiplatformpb.EvaluateInstancesResponse{}}
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), rubricTemplate(), Instance{
		Fields: map[string]AssetRef{"copy": {Text: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "no pointwise metric result") {
		t.Fatalf("want missing-rubric-result error, got %v", err)
	}
}

// TestRunRubricEmptyPromptTemplate proves rubric rejects an empty prompt template
// before calling the client (rubric's own guard).
func TestRunRubricEmptyPromptTemplate(t *testing.T) {
	tmpl := rubricTemplate()
	tmpl.MetricPromptTemplate = ""
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"copy": {Text: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "empty metric prompt template") {
		t.Fatalf("want empty-prompt error, got %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client must not be called when the rubric template is invalid")
	}
}

// TestRunRubricMissingVariable proves rubric performs the same client-side
// variable/instance-key parity check as pointwise.
func TestRunRubricMissingVariable(t *testing.T) {
	fc := &fakeClient{
		resp: &aiplatformpb.EvaluateInstancesResponse{
			EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
				PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{Score: proto.Float32(1)},
			},
		},
	}
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), rubricTemplate(), Instance{Fields: map[string]AssetRef{}})
	if err == nil || !strings.Contains(err.Error(), "copy") {
		t.Fatalf("want missing-variable error naming 'copy', got %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client must not be called when a rubric variable is missing")
	}
}

// TestToGenaiInlinePartErrors covers the converter's error branches: an AssetRef
// with no usable content, and an unreadable local file path.
func TestToGenaiInlinePartErrors(t *testing.T) {
	// No text, no file, no gs:// URI.
	if _, err := toGenaiInlinePart(AssetRef{Modality: registry.ModalityImage}); err == nil {
		t.Error("empty asset ref should error")
	}
	// Unreadable local file.
	missing := filepath.Join(t.TempDir(), "does-not-exist.bin")
	if _, err := toGenaiInlinePart(AssetRef{Modality: registry.ModalityImage, FilePath: missing}); err == nil {
		t.Error("unreadable file path should error")
	} else if !strings.Contains(err.Error(), "read asset") {
		t.Errorf("error should mention read asset: %v", err)
	}
}
