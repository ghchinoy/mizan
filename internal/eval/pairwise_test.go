package eval

import (
	"context"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"

	"github.com/ghchinoy/mizan/internal/asset"
	"github.com/ghchinoy/mizan/internal/registry"
)

func pairwiseTemplate() registry.MetricTemplate {
	return registry.MetricTemplate{
		ID:         "test/pairwise-quality",
		Name:       "Pairwise Quality",
		Kind:       registry.KindPairwise,
		Modalities: []registry.Modality{registry.ModalityText},
		// A valid pairwise template references the prompt plus the baseline and
		// candidate field-name placeholders (the API rejects instance keys not
		// present in the template — confirmed live).
		MetricPromptTemplate: "Question {{prompt}}. Baseline: {{baseline}} Candidate: {{candidate}}. Which is better?",
		SystemInstruction:    "Be fair.",
		AutoraterModel:       "gemini-2.5-flash",
		BaselineFieldName:    "baseline",
		CandidateFieldName:   "candidate",
	}
}

func pairwiseResp(choice aiplatformpb.PairwiseChoice, explanation string) *aiplatformpb.EvaluateInstancesResponse {
	return &aiplatformpb.EvaluateInstancesResponse{
		EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PairwiseMetricResult{
			PairwiseMetricResult: &aiplatformpb.PairwiseMetricResult{
				PairwiseChoice: choice,
				Explanation:    explanation,
			},
		},
	}
}

func pairwiseInstance() Instance {
	return Instance{Fields: map[string]AssetRef{
		"baseline":  {Modality: registry.ModalityText, Text: "Answer A"},
		"candidate": {Modality: registry.ModalityText, Text: "Answer B"},
		"prompt":    {Modality: registry.ModalityText, Text: "What is 2+2?"},
	}}
}

// TestRunPairwiseChoiceMapping proves the PairwiseChoice enum (BASELINE=1,
// CANDIDATE=2, TIE=3, confirmed live) maps to the surfaced string for ALL THREE
// choices, plus the unspecified fallback.
func TestRunPairwiseChoiceMapping(t *testing.T) {
	cases := []struct {
		choice aiplatformpb.PairwiseChoice
		want   string
	}{
		{aiplatformpb.PairwiseChoice_BASELINE, "BASELINE"},
		{aiplatformpb.PairwiseChoice_CANDIDATE, "CANDIDATE"},
		{aiplatformpb.PairwiseChoice_TIE, "TIE"},
		{aiplatformpb.PairwiseChoice_PAIRWISE_CHOICE_UNSPECIFIED, "UNSPECIFIED"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			fc := &fakeClient{resp: pairwiseResp(tc.choice, "because")}
			eng := NewEngine(fc, "p", "us-central1")
			res, err := eng.Run(context.Background(), pairwiseTemplate(), pairwiseInstance())
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.PairwiseChoice != tc.want {
				t.Errorf("PairwiseChoice = %q, want %q", res.PairwiseChoice, tc.want)
			}
			if res.Explanation != "because" {
				t.Errorf("Explanation = %q", res.Explanation)
			}
		})
	}
}

// TestRunPairwiseSpecAndDefaults proves the spec materialization and the pairwise
// defaults: FlipEnabled defaults on and SamplingCount defaults to >= 4 when the
// template leaves them unset.
func TestRunPairwiseSpecAndDefaults(t *testing.T) {
	fc := &fakeClient{resp: pairwiseResp(aiplatformpb.PairwiseChoice_CANDIDATE, "b")}
	eng := NewEngine(fc, "my-project", "us-central1")

	_, err := eng.Run(context.Background(), pairwiseTemplate(), pairwiseInstance())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	req := fc.gotReq
	if req == nil {
		t.Fatal("client did not receive a request")
	}
	pin := req.GetPairwiseMetricInput()
	if pin == nil {
		t.Fatal("request has no pairwise metric input")
	}
	spec := pin.GetMetricSpec()
	if spec.GetBaselineResponseFieldName() != "baseline" {
		t.Errorf("BaselineResponseFieldName = %q", spec.GetBaselineResponseFieldName())
	}
	if spec.GetCandidateResponseFieldName() != "candidate" {
		t.Errorf("CandidateResponseFieldName = %q", spec.GetCandidateResponseFieldName())
	}
	if spec.GetSystemInstruction() != "Be fair." {
		t.Errorf("SystemInstruction = %q", spec.GetSystemInstruction())
	}
	// Defaults: FlipEnabled on, SamplingCount >= 4.
	ac := req.GetAutoraterConfig()
	if !ac.GetFlipEnabled() {
		t.Error("FlipEnabled should default to true")
	}
	if ac.GetSamplingCount() != pairwiseDefaultSamplingCount {
		t.Errorf("SamplingCount = %d, want default %d", ac.GetSamplingCount(), pairwiseDefaultSamplingCount)
	}
	wantModel := "projects/my-project/locations/us-central1/publishers/google/models/gemini-2.5-flash"
	if ac.GetAutoraterModel() != wantModel {
		t.Errorf("AutoraterModel = %q, want %q", ac.GetAutoraterModel(), wantModel)
	}
	// Text instance carries baseline, candidate, and the prompt var.
	ji := pin.GetInstance().GetJsonInstance()
	for _, want := range []string{`"baseline"`, `"candidate"`, `"prompt"`, "Answer A", "Answer B"} {
		if !strings.Contains(ji, want) {
			t.Errorf("JsonInstance missing %q: %s", want, ji)
		}
	}
}

// TestRunPairwiseHonorsTemplateSampling proves an explicit SamplingCount is
// honored (not overridden by the default).
func TestRunPairwiseHonorsTemplateSampling(t *testing.T) {
	fc := &fakeClient{resp: pairwiseResp(aiplatformpb.PairwiseChoice_TIE, "")}
	tmpl := pairwiseTemplate()
	tmpl.SamplingCount = 8
	eng := NewEngine(fc, "p", "us-central1")
	if _, err := eng.Run(context.Background(), tmpl, pairwiseInstance()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fc.gotReq.GetAutoraterConfig().GetSamplingCount(); got != 8 {
		t.Errorf("SamplingCount = %d, want 8 (template value honored)", got)
	}
}

// TestRunPairwiseMissingFieldNames proves a pairwise template without baseline
// and candidate field names fails fast before any API call.
func TestRunPairwiseMissingFieldNames(t *testing.T) {
	fc := &fakeClient{}
	tmpl := pairwiseTemplate()
	tmpl.BaselineFieldName = ""
	eng := NewEngine(fc, "p", "us-central1")
	_, err := eng.Run(context.Background(), tmpl, pairwiseInstance())
	if err == nil || !strings.Contains(err.Error(), "BaselineFieldName") {
		t.Fatalf("want field-name error, got %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client should not be called when field names are missing")
	}
}

// TestRunPairwiseMissingVariable proves var/instance-key parity holds: a missing
// candidate value errors before the API call.
func TestRunPairwiseMissingVariable(t *testing.T) {
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")
	inst := Instance{Fields: map[string]AssetRef{
		"baseline": {Text: "A"},
		"prompt":   {Text: "q"},
		// candidate missing
	}}
	_, err := eng.Run(context.Background(), pairwiseTemplate(), inst)
	if err == nil || !strings.Contains(err.Error(), "candidate") {
		t.Fatalf("want missing-variable error naming 'candidate', got %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client should not be called when a variable is missing")
	}
}

// TestRunPairwisePlaceholderValidation proves the client-side fail-fast check on
// the metric prompt template: a template missing the baseline and/or candidate
// {{placeholder}} errors before any API call naming what is missing, while a
// template that references both passes (rev-5 nit 3). It must NOT reject valid
// templates.
func TestRunPairwisePlaceholderValidation(t *testing.T) {
	cases := []struct {
		name    string
		prompt  string
		wantErr string // substring; "" means the template is valid
	}{
		{
			name:    "both present is valid",
			prompt:  "Question {{prompt}}. Baseline: {{baseline}} Candidate: {{candidate}}. Which is better?",
			wantErr: "",
		},
		{
			name:    "missing baseline placeholder",
			prompt:  "Question {{prompt}}. Candidate: {{candidate}}. Which is better?",
			wantErr: "baseline {{baseline}}",
		},
		{
			name:    "missing candidate placeholder",
			prompt:  "Question {{prompt}}. Baseline: {{baseline}}. Which is better?",
			wantErr: "candidate {{candidate}}",
		},
		{
			name:    "missing both placeholders",
			prompt:  "Which response is better overall?",
			wantErr: "baseline {{baseline}} and candidate {{candidate}}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{resp: pairwiseResp(aiplatformpb.PairwiseChoice_BASELINE, "ok")}
			eng := NewEngine(fc, "p", "us-central1")
			tmpl := pairwiseTemplate()
			tmpl.MetricPromptTemplate = tc.prompt
			_, err := eng.Run(context.Background(), tmpl, pairwiseInstance())
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("valid template rejected: %v", err)
				}
				if fc.gotReq == nil {
					t.Error("client should be called for a valid template")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
			if fc.gotReq != nil {
				t.Error("client must not be called when a placeholder is missing")
			}
		})
	}
}

// TestRunPairwiseModelPrecedence proves the WI-F3 model-resolution chain also
// governs the pairwise native path (a SEPARATE materialization from pointwise):
// a --model override wins, an empty template inherits the config default, and
// with neither set the built-in is used — each reaching the expanded
// AutoraterModel resource name. Without this, a pairwise-only regression in the
// resolution wiring would go uncaught, since the other precedence tests exercise
// only the pointwise and genai paths.
func TestRunPairwiseModelPrecedence(t *testing.T) {
	cases := []struct {
		name        string
		override    string
		tmplModel   string
		configModel string
		wantBare    string
	}{
		{"flag override wins", "gemini-flag", "gemini-tmpl", "gemini-cfg", "gemini-flag"},
		{"template when no flag", "", "gemini-tmpl", "gemini-cfg", "gemini-tmpl"},
		{"config default when empty template", "", "", "gemini-cfg", "gemini-cfg"},
		{"built-in fallback", "", "", "", BuiltinDefaultModel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{resp: pairwiseResp(aiplatformpb.PairwiseChoice_BASELINE, "ok")}
			eng := NewEngine(fc, "proj", "us-central1", WithDefaultModel(tc.configModel))
			tmpl := pairwiseTemplate()
			tmpl.AutoraterModel = tc.tmplModel

			if _, err := eng.Run(context.Background(), tmpl, pairwiseInstance(), WithModel(tc.override)); err != nil {
				t.Fatalf("Run: %v", err)
			}
			want := "projects/proj/locations/us-central1/publishers/google/models/" + tc.wantBare
			if got := fc.gotReq.GetAutoraterConfig().GetAutoraterModel(); got != want {
				t.Errorf("AutoraterModel = %q, want %q", got, want)
			}
		})
	}
}

// TestRunPairwiseStatsDuration proves Duration is populated on the pairwise
// native path too (WI-F4: timing is measured centrally in Engine.Run for every
// kind) and that TokenUsage stays nil (native carries no usage metadata).
func TestRunPairwiseStatsDuration(t *testing.T) {
	fc := &fakeClient{resp: pairwiseResp(aiplatformpb.PairwiseChoice_TIE, "")}
	eng := NewEngine(fc, "p", "us-central1")
	res, err := eng.Run(context.Background(), pairwiseTemplate(), pairwiseInstance())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stats.Duration <= 0 {
		t.Errorf("Duration = %v, want > 0", res.Stats.Duration)
	}
	if res.Stats.TokenUsage != nil {
		t.Errorf("native pairwise TokenUsage = %+v, want nil", res.Stats.TokenUsage)
	}
}

// TestRunPairwiseMultimodalGCS proves multimodal pairwise: media baseline and
// candidate produce a ContentMap with gs:// FileData for each.
func TestRunPairwiseMultimodalGCS(t *testing.T) {
	fs := &fakeStager{result: asset.StageResult{GCSUri: "gs://bkt/base.png", MIME: "image/png"}}
	fc := &fakeClient{resp: pairwiseResp(aiplatformpb.PairwiseChoice_BASELINE, "")}
	eng := NewEngine(fc, "p", "us-central1", WithStager(fs))

	tmpl := pairwiseTemplate()
	// A valid multimodal pairwise template still references the baseline and
	// candidate placeholders (the ContentMap binds those keys to the images).
	tmpl.MetricPromptTemplate = "Which image is better: {{baseline}} or {{candidate}}?"
	inst := Instance{Fields: map[string]AssetRef{
		"baseline":  {Modality: registry.ModalityImage, GCSUri: "gs://bkt/base.png"},
		"candidate": {Modality: registry.ModalityImage, GCSUri: "gs://bkt/cand.png"},
	}}
	if _, err := eng.Run(context.Background(), tmpl, inst); err != nil {
		t.Fatalf("Run: %v", err)
	}
	cm := fc.gotReq.GetPairwiseMetricInput().GetInstance().GetContentMapInstance()
	if cm == nil {
		t.Fatal("expected a ContentMap instance for multimodal pairwise")
	}
	for _, key := range []string{"baseline", "candidate"} {
		fd := cm.GetValues()[key].GetContents()[0].GetParts()[0].GetFileData()
		if fd == nil || fd.GetMimeType() != "image/png" {
			t.Errorf("%s FileData = %+v, want image/png", key, fd)
		}
	}
}

// TestRunPairwiseNoResult proves a response with no pairwise result errors.
func TestRunPairwiseNoResult(t *testing.T) {
	fc := &fakeClient{resp: &aiplatformpb.EvaluateInstancesResponse{}}
	eng := NewEngine(fc, "p", "us-central1")
	_, err := eng.Run(context.Background(), pairwiseTemplate(), pairwiseInstance())
	if err == nil || !strings.Contains(err.Error(), "no pairwise metric result") {
		t.Fatalf("want no-result error, got %v", err)
	}
}
