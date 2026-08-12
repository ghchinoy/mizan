package eval

import (
	"context"
	"os"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/registry"
)

// packFixture is the scaffolded template, read from the registry package's
// testdata so this test proves the WHOLE codec→model→eval contract on the real
// fixture (not a hand-built struct).
const packFixture = "../registry/testdata/packs/google-brand/templates/video-brand-alignment.yaml"

// TestImportedTemplateMaterializesThroughEngine is the creds-free stand-in for
// the live `mizan eval run` leg of the P2.1 acceptance. It unmarshals the pack
// template exactly as Import does (registry.YAMLCodec), then runs it through the
// eval engine with a scripted fake client — proving the imported template
// materializes into a valid Vertex request (autorater model expanded, prompt +
// system instruction + declared inputs all carried) and yields a score, WITHOUT
// a live GCP call. See the PR note: the live run was not executed (no ADC in the
// build env); this guards the same codec→model→store→eval alignment offline.
func TestImportedTemplateMaterializesThroughEngine(t *testing.T) {
	data, err := os.ReadFile(packFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	tmpl, err := registry.NewYAMLCodec().Unmarshal(data)
	if err != nil {
		t.Fatalf("codec Unmarshal: %v", err)
	}

	fc := &fakeClient{
		resp: &aiplatformpb.EvaluateInstancesResponse{
			EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
				PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{
					Score:       proto.Float32(4.0),
					Explanation: "On brand.",
				},
			},
		},
	}
	eng := NewEngine(fc, "my-project", "us-central1")

	// Both inputs supplied as text so the eval stays offline (no GCS stager); the
	// engine binds by field name, which is what the codec→model mapping must get
	// right. The template declares response as video, but the supplied AssetRef
	// modality is what the request uses.
	res, err := eng.Run(context.Background(), *tmpl, Instance{
		Fields: map[string]AssetRef{
			"response":        {Modality: registry.ModalityText, Text: "A 15s product spot."},
			"brand_guideline": {Modality: registry.ModalityText, Text: "Warm, minimal, logo bottom-right."},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Score == nil || *res.Score != 4.0 {
		t.Fatalf("Score = %v, want 4.0", res.Score)
	}

	req := fc.gotReq
	if req == nil {
		t.Fatal("engine did not materialize a request")
	}
	// The publisher-relative autorater id from the pack expands to the full
	// resource name at eval time.
	wantModel := "projects/my-project/locations/us-central1/publishers/google/models/gemini-2.5-pro"
	if got := req.GetAutoraterConfig().GetAutoraterModel(); got != wantModel {
		t.Errorf("AutoraterModel = %q, want %q", got, wantModel)
	}
	if got := req.GetAutoraterConfig().GetSamplingCount(); got != 4 {
		t.Errorf("SamplingCount = %d, want 4", got)
	}
	pin := req.GetPointwiseMetricInput()
	if pin == nil {
		t.Fatal("request has no pointwise metric input")
	}
	if got := pin.GetMetricSpec().GetMetricPromptTemplate(); got != tmpl.MetricPromptTemplate {
		t.Errorf("prompt not carried through: %q", got)
	}
	ji := pin.GetInstance().GetJsonInstance()
	if !strings.Contains(ji, `"response"`) || !strings.Contains(ji, `"brand_guideline"`) {
		t.Errorf("instance missing declared inputs: %q", ji)
	}
}

// TestImportedTemplateFieldsAlignWithEngine proves the codec output's declared
// placeholders are exactly what the engine expects — the client-side contract
// that guards against a codec/model drift silently dropping a field.
func TestImportedTemplateFieldsAlignWithEngine(t *testing.T) {
	data, err := os.ReadFile(packFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	tmpl, err := registry.NewYAMLCodec().Unmarshal(data)
	if err != nil {
		t.Fatalf("codec Unmarshal: %v", err)
	}

	expected := expectedInstanceFields(*tmpl)
	inst := Instance{Fields: map[string]AssetRef{
		"response":        {Modality: registry.ModalityText, Text: "x"},
		"brand_guideline": {Modality: registry.ModalityText, Text: "y"},
	}}
	if err := validateInstanceFields(expected, *tmpl, inst); err != nil {
		t.Fatalf("imported template fields do not align with engine expectations: %v", err)
	}
}
