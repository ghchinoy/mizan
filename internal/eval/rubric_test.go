package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/registry"
)

func rubricTemplate() registry.MetricTemplate {
	return registry.MetricTemplate{
		ID:                   "test/ad-quality",
		Name:                 "Ad Quality",
		Kind:                 registry.KindRubric,
		Modalities:           []registry.Modality{registry.ModalityText},
		MetricPromptTemplate: "Evaluate this ad copy: {{copy}}",
		SystemInstruction:    "Be strict.",
		AutoraterModel:       "gemini-2.5-flash",
		SamplingCount:        2,
		RubricGroups: map[string][]string{
			"clarity": {"The message is unambiguous", "No jargon"},
			"tone":    {"Matches a professional brand voice"},
		},
	}
}

func TestRunRubricSuccess(t *testing.T) {
	fc := &fakeClient{
		resp: &aiplatformpb.EvaluateInstancesResponse{
			EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
				PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{
					Score:       proto.Float32(3.0),
					Explanation: "Clear but slightly informal.",
				},
			},
		},
	}
	eng := NewEngine(fc, "my-project", "us-central1")

	res, err := eng.Run(context.Background(), rubricTemplate(), Instance{
		Fields: map[string]AssetRef{
			"copy": {Modality: registry.ModalityText, Text: "Buy now, save big."},
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

	req := fc.gotReq
	if req == nil {
		t.Fatal("client did not receive a request")
	}
	pin := req.GetPointwiseMetricInput()
	if pin == nil {
		t.Fatal("rubric should materialize a pointwise metric input on the native path")
	}
	// The rubric groups are rendered inline into the prompt, deterministically
	// ordered (clarity before tone), with their criteria.
	prompt := pin.GetMetricSpec().GetMetricPromptTemplate()
	if !strings.Contains(prompt, "Evaluate this ad copy") {
		t.Errorf("prompt missing base template: %q", prompt)
	}
	for _, want := range []string{"clarity", "tone", "The message is unambiguous", "No jargon", "professional brand voice"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing rubric content %q\nprompt: %s", want, prompt)
		}
	}
	if strings.Index(prompt, "## clarity") > strings.Index(prompt, "## tone") {
		t.Errorf("rubric groups not deterministically ordered:\n%s", prompt)
	}
	// System instruction, autorater expansion, and sampling still flow through.
	if pin.GetMetricSpec().GetSystemInstruction() != "Be strict." {
		t.Errorf("SystemInstruction = %q", pin.GetMetricSpec().GetSystemInstruction())
	}
	wantModel := "projects/my-project/locations/us-central1/publishers/google/models/gemini-2.5-flash"
	if got := req.GetAutoraterConfig().GetAutoraterModel(); got != wantModel {
		t.Errorf("AutoraterModel = %q, want %q", got, wantModel)
	}
	if got := req.GetAutoraterConfig().GetSamplingCount(); got != 2 {
		t.Errorf("SamplingCount = %d, want 2", got)
	}
	// The JSON instance carries the substituted variable.
	if ji := pin.GetInstance().GetJsonInstance(); !strings.Contains(ji, "Buy now") {
		t.Errorf("JsonInstance = %q", ji)
	}
}

func TestRunRubricNoGroups(t *testing.T) {
	tmpl := rubricTemplate()
	tmpl.RubricGroups = nil
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")
	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"copy": {Text: "x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "rubric groups") {
		t.Fatalf("want no-rubric-groups error, got %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client should not be called without rubric groups")
	}
}

func TestRunRubricRejectsMultimodal(t *testing.T) {
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")
	_, err := eng.Run(context.Background(), rubricTemplate(), Instance{
		Fields: map[string]AssetRef{
			"copy": {Modality: registry.ModalityImage, GCSUri: "gs://b/x.jpg"},
		},
	})
	if !errors.Is(err, errNotImplemented) {
		t.Fatalf("want errNotImplemented for multimodal rubric field, got %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client should not be called for an unsupported multimodal field")
	}
}

func TestRenderRubricGroupsDeterministic(t *testing.T) {
	groups := map[string][]string{
		"b-group": {"crit b1"},
		"a-group": {"crit a1", "crit a2"},
	}
	got1 := renderRubricGroups(groups)
	got2 := renderRubricGroups(groups)
	if got1 != got2 {
		t.Error("renderRubricGroups is not deterministic")
	}
	if strings.Index(got1, "a-group") > strings.Index(got1, "b-group") {
		t.Errorf("groups not sorted:\n%s", got1)
	}
}
