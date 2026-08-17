// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package eval

import (
	"context"
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
		// NOTE: SamplingCount is deliberately left unset (0 == single sample) on the
		// shared fixture. The native path (runRubric) honors SamplingCount, but the
		// genai/global structured path (runRubricStructured, used by the many
		// --rubric-detail reconcile tests) now emits a non-fatal H3 warning when a
		// template declares SamplingCount>1 (RFC-0001 §5.4). Keeping the shared
		// fixture at a single sample keeps those tests focused on reconciliation;
		// tests that need sampling set it locally (see TestRunRubricSuccess and the
		// autorater-warning tests), mirroring pairwise_test.go's pattern.
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

	// The native rubric path honors SamplingCount; set it locally (the shared
	// fixture leaves it unset — see rubricTemplate()).
	tmpl := rubricTemplate()
	tmpl.SamplingCount = 2
	res, err := eng.Run(context.Background(), tmpl, Instance{
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

func TestRunRubricMultimodalGCS(t *testing.T) {
	// Rubric shares the native pointwise materialization, so a multimodal field
	// (pre-staged gs://) produces a ContentMap instance with gs:// FileData.
	fc := &fakeClient{
		resp: &aiplatformpb.EvaluateInstancesResponse{
			EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
				PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{Score: proto.Float32(2)},
			},
		},
	}
	eng := NewEngine(fc, "p", "us-central1")
	_, err := eng.Run(context.Background(), rubricTemplate(), Instance{
		Fields: map[string]AssetRef{
			"copy": {Modality: registry.ModalityImage, GCSUri: "gs://b/x.jpg"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cm := fc.gotReq.GetPointwiseMetricInput().GetInstance().GetContentMapInstance()
	if cm == nil {
		t.Fatal("expected a ContentMap instance for a multimodal rubric field")
	}
	fd := cm.GetValues()["copy"].GetContents()[0].GetParts()[0].GetFileData()
	if fd == nil || fd.GetFileUri() != "gs://b/x.jpg" || fd.GetMimeType() != "image/jpeg" {
		t.Errorf("FileData = %+v, want gs://b/x.jpg image/jpeg", fd)
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
