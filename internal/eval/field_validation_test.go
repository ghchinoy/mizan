package eval

// field_validation_test.go covers FIX-1: symmetric client-side validation of the
// supplied instance fields against the template's placeholders. Before FIX-1 a
// field whose key matched no {{placeholder}} — or ANY field against a template
// with no placeholders — was silently dropped from the request, so the judge
// never saw the value and returned a confidently-wrong score (the owner's exact
// symptom). These tests pin the cases from design/eval-triage-findings.md
// (Symptom #1: A/B/C/D) for BOTH the native and genai/custom_schema paths, and
// prove the happy-path value provably reaches the request payload.

import (
	"context"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/registry"
)

// pointwiseResp is a canned successful pointwise response so the happy-path runs
// reach the result-mapping stage without a live client.
func pointwiseResp(score float32, explanation string) *aiplatformpb.EvaluateInstancesResponse {
	return &aiplatformpb.EvaluateInstancesResponse{
		EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
			PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{
				Score:       proto.Float32(score),
				Explanation: explanation,
			},
		},
	}
}

// --- Case A: a template with NO {{placeholder}} + a supplied field ---
// This is the owner's exact symptom: extractVars=[] → the field is dropped → the
// judge scores a response it never saw. It MUST now be a hard, client-side error.

func TestValidateFields_CaseA_NoPlaceholderNative(t *testing.T) {
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")

	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Rate how concise this response is from 0 to 1." // no {{...}}

	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"response": {Modality: registry.ModalityText, Text: "The cat sat on the mat."}},
	})
	if err == nil {
		t.Fatal("want error for a no-placeholder template with a supplied field, got nil (silent drop)")
	}
	if !strings.Contains(err.Error(), "no {{placeholders}}") || !strings.Contains(err.Error(), tmpl.ID) || !strings.Contains(err.Error(), "response") {
		t.Errorf("error must name the template id and the dropped field and explain the no-placeholder cause: %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client must NOT be called when the field cannot reach the judge")
	}
}

func TestValidateFields_CaseA_NoPlaceholderGenai(t *testing.T) {
	fg := &fakeGenai{respText: `{"overall_score":1,"compliant":true,"flagged_issues":[],"explanation":"x"}`}
	eng := NewEngine(nil, "p", "global", WithGenaiClient(fg))

	tmpl := customSchemaTemplate()
	tmpl.MetricPromptTemplate = "Assess this creative against the brand guideline." // no {{...}}

	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"creative": {Modality: registry.ModalityText, Text: "A calm blue banner"}},
	})
	if err == nil {
		t.Fatal("want error for a no-placeholder custom_schema template with a supplied field, got nil")
	}
	if !strings.Contains(err.Error(), "no {{placeholders}}") || !strings.Contains(err.Error(), tmpl.ID) {
		t.Errorf("error must name the template id and explain the no-placeholder cause: %v", err)
	}
	if fg.calls != 0 {
		t.Errorf("genai client must NOT be called; got %d calls", fg.calls)
	}
}

// --- Case B: single-brace {response} — extractVars misses it, so it behaves like
// a no-placeholder template. Previously this hard-errored SERVER-side; FIX-1 now
// catches it client-side (fail fast) with actionable guidance. It must still
// ERROR (not silently drop). ---

func TestValidateFields_CaseB_SingleBraceNative(t *testing.T) {
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")

	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Rate conciseness. Response: {response}" // single-brace, not matched

	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"response": {Modality: registry.ModalityText, Text: "The cat sat."}},
	})
	if err == nil {
		t.Fatal("want error for a single-brace (unmatched) placeholder + field, got nil")
	}
	if !strings.Contains(err.Error(), "{{") {
		t.Errorf("error should steer the author toward double-brace {{...}} syntax: %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client must NOT be called; the single-brace template drops the field")
	}
}

// --- Case C: correct template but a MISSPELLED field name. This direction was
// already validated (missing values for template variables); FIX-1 catches it a
// step earlier as an unknown field. It must still ERROR and name the offender. ---

func TestValidateFields_CaseC_MisspelledFieldNative(t *testing.T) {
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), pointwiseTemplate(), Instance{
		Fields: map[string]AssetRef{"respons": {Modality: registry.ModalityText, Text: "The cat sat."}},
	})
	if err == nil {
		t.Fatal("want error for a misspelled field name, got nil")
	}
	if !strings.Contains(err.Error(), "respons") || !strings.Contains(err.Error(), pointwiseTemplate().ID) {
		t.Errorf("error must name the offending key and the template: %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client must NOT be called when a supplied field matches no placeholder")
	}
}

// --- Case D: correct template + an EXTRA unknown field. Previously silently
// dropped (Score 1, no warning). FIX-1 makes it a hard error naming the key. ---

func TestValidateFields_CaseD_ExtraFieldNative(t *testing.T) {
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), pointwiseTemplate(), Instance{
		Fields: map[string]AssetRef{
			"response": {Modality: registry.ModalityText, Text: "The cat sat."},
			"bogus":    {Modality: registry.ModalityText, Text: "ignored?"},
		},
	})
	if err == nil {
		t.Fatal("want error for an extra unknown field, got nil (silent drop)")
	}
	if !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), "response") || !strings.Contains(err.Error(), pointwiseTemplate().ID) {
		t.Errorf("error must name the unknown key, the known placeholders, and the template: %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client must NOT be called when an extra field would be dropped")
	}
}

func TestValidateFields_CaseD_ExtraFieldGenai(t *testing.T) {
	fg := &fakeGenai{respText: `{"overall_score":1,"compliant":true,"flagged_issues":[],"explanation":"x"}`}
	eng := NewEngine(nil, "p", "global", WithGenaiClient(fg))

	_, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{
			"creative": {Modality: registry.ModalityText, Text: "A calm blue banner"},
			"bogus":    {Modality: registry.ModalityText, Text: "ignored?"},
		},
	})
	if err == nil {
		t.Fatal("want error for an extra unknown field on the genai path, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), customSchemaTemplate().ID) {
		t.Errorf("error must name the unknown key and the template: %v", err)
	}
	if fg.calls != 0 {
		t.Errorf("genai client must NOT be called; got %d calls", fg.calls)
	}
}

func TestValidateFields_CaseD_ExtraFieldPairwise(t *testing.T) {
	fc := &fakeClient{resp: pairwiseResp(aiplatformpb.PairwiseChoice_BASELINE, "ok")}
	eng := NewEngine(fc, "p", "us-central1")

	inst := pairwiseInstance()
	inst.Fields["bogus"] = AssetRef{Modality: registry.ModalityText, Text: "ignored?"}

	_, err := eng.Run(context.Background(), pairwiseTemplate(), inst)
	if err == nil {
		t.Fatal("want error for an extra unknown field on the pairwise path, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), pairwiseTemplate().ID) {
		t.Errorf("error must name the unknown key and the template: %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client must NOT be called when an extra pairwise field would be dropped")
	}
}

// --- Happy paths: a correct template + a correct field WORKS, and the field's
// value provably reaches the request payload (the guard against over-eager
// rejection AND proof the value is delivered). ---

func TestValidateFields_HappyPathNative(t *testing.T) {
	fc := &fakeClient{resp: pointwiseResp(1, "extremely concise")}
	eng := NewEngine(fc, "p", "us-central1")

	res, err := eng.Run(context.Background(), pointwiseTemplate(), Instance{
		Fields: map[string]AssetRef{"response": {Modality: registry.ModalityText, Text: "The cat sat on the mat."}},
	})
	if err != nil {
		t.Fatalf("valid template+field rejected: %v", err)
	}
	if res.Score == nil || *res.Score != 1 {
		t.Errorf("Score = %v, want 1", res.Score)
	}
	if fc.gotReq == nil {
		t.Fatal("client should be called for a valid template+field")
	}
	// Proof the field reached the request payload (not silently dropped).
	ji := fc.gotReq.GetPointwiseMetricInput().GetInstance().GetJsonInstance()
	if !strings.Contains(ji, `"response"`) || !strings.Contains(ji, "The cat sat on the mat.") {
		t.Errorf("JsonInstance %q does not carry the supplied field value", ji)
	}
}

func TestValidateFields_HappyPathGenai(t *testing.T) {
	fg := &fakeGenai{respText: `{"overall_score":1,"compliant":true,"flagged_issues":[],"explanation":"on brand"}`}
	eng := NewEngine(nil, "p", "global", WithGenaiClient(fg))

	_, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{"creative": {Modality: registry.ModalityText, Text: "A calm blue banner"}},
	})
	if err != nil {
		t.Fatalf("valid custom_schema template+field rejected: %v", err)
	}
	if fg.calls == 0 {
		t.Fatal("genai client should be called for a valid template+field")
	}
	// Proof the field reached the rendered prompt (client-side substitution).
	if len(fg.gotContents) == 0 || len(fg.gotContents[0].Parts) == 0 {
		t.Fatalf("unexpected contents shape: %+v", fg.gotContents)
	}
	if txt := fg.gotContents[0].Parts[0].Text; !strings.Contains(txt, "A calm blue banner") {
		t.Errorf("rendered prompt %q does not carry the supplied field value", txt)
	}
}

func TestValidateFields_HappyPathPairwise(t *testing.T) {
	fc := &fakeClient{resp: pairwiseResp(aiplatformpb.PairwiseChoice_CANDIDATE, "ok")}
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), pairwiseTemplate(), pairwiseInstance())
	if err != nil {
		t.Fatalf("valid pairwise template+fields rejected: %v", err)
	}
	if fc.gotReq == nil {
		t.Fatal("client should be called for a valid pairwise template+fields")
	}
	ji := fc.gotReq.GetPairwiseMetricInput().GetInstance().GetJsonInstance()
	if !strings.Contains(ji, "Answer A") || !strings.Contains(ji, "Answer B") {
		t.Errorf("pairwise JsonInstance %q does not carry the supplied baseline/candidate values", ji)
	}
}
