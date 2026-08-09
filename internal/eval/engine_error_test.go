package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
)

// These tests exercise the error and mapping paths of the pointwise engine that
// the happy-path test does not reach. They all use the fake EvaluationClient, so
// no network call is ever made.

func TestRunPointwiseClientError(t *testing.T) {
	backendErr := errors.New("rpc: InvalidArgument")
	fc := &fakeClient{err: backendErr}
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), pointwiseTemplate(), Instance{
		Fields: map[string]AssetRef{"response": {Text: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error when the client fails")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("error should wrap the backend error (errors.Is): got %v", err)
	}
	if !strings.Contains(err.Error(), "EvaluateInstances") {
		t.Errorf("error should mention EvaluateInstances for context: %v", err)
	}
}

func TestRunPointwiseNoResultInResponse(t *testing.T) {
	// A response with no PointwiseMetricResult set (e.g. a pairwise result, or
	// an empty envelope) must be reported, not silently mapped to a zero Result.
	fc := &fakeClient{resp: &aiplatformpb.EvaluateInstancesResponse{}}
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), pointwiseTemplate(), Instance{
		Fields: map[string]AssetRef{"response": {Text: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error when the response has no pointwise result")
	}
	if !strings.Contains(err.Error(), "no pointwise metric result") {
		t.Errorf("error should explain the missing result: %v", err)
	}
}

func TestRunPointwiseEmptyPromptTemplate(t *testing.T) {
	fc := &fakeClient{}
	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = ""
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), tmpl, Instance{})
	if err == nil {
		t.Fatal("expected an error for an empty metric prompt template")
	}
	if !strings.Contains(err.Error(), "empty metric prompt template") {
		t.Errorf("error should name the empty prompt template: %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client must not be called when the template is invalid")
	}
}

func TestRunPointwiseNilClient(t *testing.T) {
	eng := NewEngine(nil, "p", "us-central1")
	_, err := eng.Run(context.Background(), pointwiseTemplate(), Instance{
		Fields: map[string]AssetRef{"response": {Text: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error when no client is configured")
	}
	if !strings.Contains(err.Error(), "no evaluation client") {
		t.Errorf("error should explain the missing client: %v", err)
	}
}

// TestRunPointwiseOmitsOptionalFields proves the optional AutoraterConfig and
// spec fields are left unset when the template does not supply them, rather than
// being sent as empty/zero values.
func TestRunPointwiseOmitsOptionalFields(t *testing.T) {
	fc := &fakeClient{
		resp: &aiplatformpb.EvaluateInstancesResponse{
			EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
				PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{},
			},
		},
	}
	tmpl := pointwiseTemplate()
	tmpl.SystemInstruction = ""
	tmpl.SamplingCount = 0
	eng := NewEngine(fc, "p", "us-central1")

	if _, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"response": {Text: "hi"}},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	spec := fc.gotReq.GetPointwiseMetricInput().GetMetricSpec()
	if spec.SystemInstruction != nil {
		t.Errorf("SystemInstruction should be unset when empty, got %q", spec.GetSystemInstruction())
	}
	if sc := fc.gotReq.GetAutoraterConfig().SamplingCount; sc != nil {
		t.Errorf("SamplingCount should be unset when zero, got %d", *sc)
	}
}

// TestBuildJSONInstanceIgnoresExtraFields confirms that only the variables
// referenced by the template are marshaled — instance fields that the template
// does not reference are dropped, not sent to the API.
func TestBuildJSONInstanceIgnoresExtraFields(t *testing.T) {
	ji, err := buildJSONInstance("only {{a}} here", Instance{Fields: map[string]AssetRef{
		"a":     {Text: "used"},
		"extra": {Text: "ignored"},
	}})
	if err != nil {
		t.Fatalf("buildJSONInstance: %v", err)
	}
	if !strings.Contains(ji, `"a":"used"`) {
		t.Errorf("expected referenced var, got %q", ji)
	}
	if strings.Contains(ji, "extra") || strings.Contains(ji, "ignored") {
		t.Errorf("unreferenced field leaked into JSON instance: %q", ji)
	}
}

func TestExpandAutoraterModelErrors(t *testing.T) {
	cases := []struct {
		name             string
		model, proj, loc string
		wantErrSubstr    string
	}{
		{"missing project", "gemini-2.5-flash", "", "us-central1", "no project ID"},
		{"missing location", "gemini-2.5-flash", "p", "", "no location"},
		{"empty model", "", "p", "us-central1", "empty autorater model id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := expandAutoraterModel(tc.model, tc.proj, tc.loc)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Errorf("error = %v, want substring %q", err, tc.wantErrSubstr)
			}
		})
	}
}

// A fully-qualified model must pass through even when project/location are empty,
// since no expansion is needed.
func TestExpandAutoraterModelFullNameNoProject(t *testing.T) {
	full := "projects/other/locations/europe-west1/publishers/google/models/gemini-2.5-flash"
	got, err := expandAutoraterModel(full, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != full {
		t.Errorf("got %q, want pass-through %q", got, full)
	}
}
