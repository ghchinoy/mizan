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
	"errors"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	gax "github.com/googleapis/gax-go/v2"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/asset"
	"github.com/ghchinoy/mizan/internal/registry"
)

// fakeClient is a mock EvaluationClient. It captures the request and returns a
// canned response, so the engine's spec materialization and result mapping are
// tested without a live API call.
type fakeClient struct {
	gotReq *aiplatformpb.EvaluateInstancesRequest
	resp   *aiplatformpb.EvaluateInstancesResponse
	err    error
}

func (f *fakeClient) EvaluateInstances(_ context.Context, req *aiplatformpb.EvaluateInstancesRequest, _ ...gax.CallOption) (*aiplatformpb.EvaluateInstancesResponse, error) {
	f.gotReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func pointwiseTemplate() registry.MetricTemplate {
	return registry.MetricTemplate{
		ID:                   "test/helpfulness",
		Name:                 "Helpfulness",
		Kind:                 registry.KindPointwise,
		Modalities:           []registry.Modality{registry.ModalityText},
		MetricPromptTemplate: "Rate the helpfulness of this response: {{response}}",
		SystemInstruction:    "Be strict.",
		AutoraterModel:       "gemini-2.5-flash",
		SamplingCount:        4,
	}
}

func TestRunPointwiseSuccess(t *testing.T) {
	fc := &fakeClient{
		resp: &aiplatformpb.EvaluateInstancesResponse{
			EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
				PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{
					Score:       proto.Float32(4.5),
					Explanation: "Clear and correct.",
				},
			},
		},
	}
	eng := NewEngine(fc, "my-project", "us-central1")

	res, err := eng.Run(context.Background(), pointwiseTemplate(), Instance{
		Fields: map[string]AssetRef{
			"response": {Modality: registry.ModalityText, Text: "To reset your password, click the link."},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Result mapping.
	if res.Score == nil || *res.Score != 4.5 {
		t.Errorf("Score = %v, want 4.5", res.Score)
	}
	if res.Explanation != "Clear and correct." {
		t.Errorf("Explanation = %q", res.Explanation)
	}

	// Request materialization.
	req := fc.gotReq
	if req == nil {
		t.Fatal("client did not receive a request")
	}
	if want := "projects/my-project/locations/us-central1"; req.Location != want {
		t.Errorf("Location = %q, want %q", req.Location, want)
	}
	// Autorater model must be expanded to the full resource name.
	wantModel := "projects/my-project/locations/us-central1/publishers/google/models/gemini-2.5-flash"
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
	if got := pin.GetMetricSpec().GetMetricPromptTemplate(); got != pointwiseTemplate().MetricPromptTemplate {
		t.Errorf("MetricPromptTemplate = %q", got)
	}
	if got := pin.GetMetricSpec().GetSystemInstruction(); got != "Be strict." {
		t.Errorf("SystemInstruction = %q", got)
	}
	// JSON instance carries the substituted variable key/value.
	ji := pin.GetInstance().GetJsonInstance()
	if !strings.Contains(ji, `"response"`) || !strings.Contains(ji, "reset your password") {
		t.Errorf("JsonInstance = %q, missing expected key/value", ji)
	}
}

func TestRunPointwiseMissingVariable(t *testing.T) {
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")
	_, err := eng.Run(context.Background(), pointwiseTemplate(), Instance{
		Fields: map[string]AssetRef{}, // no "response"
	})
	if err == nil {
		t.Fatal("expected error for missing variable")
	}
	if !strings.Contains(err.Error(), "response") {
		t.Errorf("error should name the missing variable: %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client should not be called when validation fails")
	}
}

func TestRunPointwiseMultimodalGCS(t *testing.T) {
	// A pre-staged gs:// asset with a known extension resolves its MIME without a
	// Stager and materializes a native ContentMap (gs:// FileData) instance.
	fc := &fakeClient{
		resp: &aiplatformpb.EvaluateInstancesResponse{
			EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
				PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{
					Score:       proto.Float32(3),
					Explanation: "ok",
				},
			},
		},
	}
	eng := NewEngine(fc, "p", "us-central1")
	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Describe {{response}}"
	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{
			"response": {Modality: registry.ModalityImage, GCSUri: "gs://bucket/cat.jpg"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cm := fc.gotReq.GetPointwiseMetricInput().GetInstance().GetContentMapInstance()
	if cm == nil {
		t.Fatal("expected a ContentMap instance for a multimodal field")
	}
	fd := cm.GetValues()["response"].GetContents()[0].GetParts()[0].GetFileData()
	if fd == nil || fd.GetFileUri() != "gs://bucket/cat.jpg" {
		t.Errorf("FileData = %+v, want gs://bucket/cat.jpg", fd)
	}
	if fd.GetMimeType() != "image/jpeg" {
		t.Errorf("MimeType = %q, want image/jpeg (resolved from extension)", fd.GetMimeType())
	}
}

func TestRunPointwiseLocalFileNoStager(t *testing.T) {
	// A local FilePath with no Stager configured must fail with a clear
	// ErrNoBucket-style error before any API call.
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")
	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Describe {{response}}"
	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{
			"response": {Modality: registry.ModalityImage, FilePath: "/tmp/cat.jpg"},
		},
	})
	if !errors.Is(err, asset.ErrNoBucket) {
		t.Fatalf("want asset.ErrNoBucket, got %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client should not be called when staging is unavailable")
	}
}

func TestRunUnknownKind(t *testing.T) {
	// An unknown kind is a distinct, clearly-worded error.
	eng := NewEngine(&fakeClient{}, "p", "us-central1")
	tmpl := pointwiseTemplate()
	tmpl.Kind = "bogus"
	if _, err := eng.Run(context.Background(), tmpl, Instance{}); err == nil || !strings.Contains(err.Error(), "unknown metric kind") {
		t.Errorf("unknown kind: got %v", err)
	}
}

func TestExpandAutoraterModel(t *testing.T) {
	cases := []struct {
		name    string
		model   string
		want    string
		wantErr bool
	}{
		{"bare", "gemini-2.5-flash", "projects/p/locations/us-central1/publishers/google/models/gemini-2.5-flash", false},
		{"publisher-relative", "publishers/google/models/gemini-2.5-pro", "projects/p/locations/us-central1/publishers/google/models/gemini-2.5-pro", false},
		{"already-full", "projects/other/locations/europe-west1/publishers/google/models/gemini-2.5-flash", "projects/other/locations/europe-west1/publishers/google/models/gemini-2.5-flash", false},
		{"empty", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := expandAutoraterModel(tc.model, "p", "us-central1")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractVars(t *testing.T) {
	got := extractVars("Compare {{ candidate }} against {{baseline}} and {{candidate}} again")
	want := []string{"candidate", "baseline"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}

func TestBuildJSONInstance(t *testing.T) {
	// No variables -> empty JSON object (API rejects a non-empty instance).
	ji, err := buildJSONInstance("no vars here", Instance{})
	if err != nil {
		t.Fatalf("no-vars: %v", err)
	}
	if ji != "{}" {
		t.Errorf("no-vars: got %q, want {}", ji)
	}

	// All present -> JSON object with the referenced keys.
	ji, err = buildJSONInstance("{{a}} and {{b}}", Instance{Fields: map[string]AssetRef{
		"a": {Text: "x"}, "b": {Text: "y"},
	}})
	if err != nil {
		t.Fatalf("all-present: %v", err)
	}
	if !strings.Contains(ji, `"a":"x"`) || !strings.Contains(ji, `"b":"y"`) {
		t.Errorf("all-present: got %q", ji)
	}

	// Missing -> error naming the variable.
	if _, err := buildJSONInstance("{{missing}}", Instance{}); err == nil {
		t.Error("missing: expected error")
	}
}
