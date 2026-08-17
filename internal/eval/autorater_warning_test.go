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

// autorater_warning_test.go covers H3 (RFC-0001 §5.4): the genai/global
// structured path emits a NON-FATAL warning when a template declares
// AutoraterConfig fields it cannot honor (SamplingCount>1 or FlipEnabled). The
// native path honors those fields and must NOT warn.

import (
	"context"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/registry"
)

func warningsContain(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

// TestGenaiPathAutoraterWarningsUnit exercises the boundary conditions of the
// warning helper directly: SamplingCount<=1 and FlipEnabled=false are no-ops;
// SamplingCount>1 and FlipEnabled=true each produce exactly one warning.
func TestGenaiPathAutoraterWarningsUnit(t *testing.T) {
	cases := []struct {
		name     string
		sampling int32
		flip     bool
		wantN    int
		wantSubs []string
	}{
		{"none", 0, false, 0, nil},
		{"single-sample", 1, false, 0, nil},
		{"sampling-two", 2, false, 1, []string{"samplingCount=2", "genai/global structured path"}},
		{"flip-only", 1, true, 1, []string{"flipEnabled", "genai/global structured path"}},
		{"both", 4, true, 2, []string{"samplingCount=4", "flipEnabled"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpl := registry.MetricTemplate{SamplingCount: tc.sampling, FlipEnabled: tc.flip}
			got := genaiPathAutoraterWarnings(tmpl)
			if len(got) != tc.wantN {
				t.Fatalf("warnings = %v, want %d", got, tc.wantN)
			}
			for _, sub := range tc.wantSubs {
				if !warningsContain(got, sub) {
					t.Errorf("warnings %v missing %q", got, sub)
				}
			}
		})
	}
}

// customSchemaJSON is a minimal valid custom_schema judge response.
const customSchemaJSON = `{"overall_score":5,"explanation":"ok"}`

// TestCustomSchemaWarnsOnSamplingCount proves the genai custom_schema path
// surfaces the samplingCount warning when the template declares SamplingCount>1.
func TestCustomSchemaWarnsOnSamplingCount(t *testing.T) {
	tmpl := customSchemaTemplate()
	tmpl.SamplingCount = 3
	fg := &fakeGenai{respText: customSchemaJSON}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"creative": {Modality: registry.ModalityText, Text: "x"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !warningsContain(res.Warnings, "samplingCount=3") {
		t.Errorf("Warnings = %v, want a samplingCount warning", res.Warnings)
	}
}

// TestCustomSchemaWarnsOnFlipEnabled proves the genai custom_schema path surfaces
// the flipEnabled warning.
func TestCustomSchemaWarnsOnFlipEnabled(t *testing.T) {
	tmpl := customSchemaTemplate()
	tmpl.FlipEnabled = true
	fg := &fakeGenai{respText: customSchemaJSON}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"creative": {Modality: registry.ModalityText, Text: "x"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !warningsContain(res.Warnings, "flipEnabled") {
		t.Errorf("Warnings = %v, want a flipEnabled warning", res.Warnings)
	}
}

// TestCustomSchemaNoWarningWhenAbsent proves a clean template (single sample, no
// flip) produces no genai-path autorater warning.
func TestCustomSchemaNoWarningWhenAbsent(t *testing.T) {
	tmpl := customSchemaTemplate() // no SamplingCount, no FlipEnabled
	fg := &fakeGenai{respText: customSchemaJSON}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"creative": {Modality: registry.ModalityText, Text: "x"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", res.Warnings)
	}
}

// rubricDetailFullSetJSON returns the full authored (group,criterion) set so
// reconciliation is a happy-path no-op — isolating the H3 warning.
const rubricDetailFullSetJSON = `{
	"per_criterion": [
		{"group":"clarity","criterion":"The message is unambiguous","score":4,"rationale":"x"},
		{"group":"clarity","criterion":"No jargon","score":5,"rationale":"x"},
		{"group":"tone","criterion":"Matches a professional brand voice","score":3,"rationale":"x"}
	],
	"overall_score": 4,
	"explanation": "ok"
}`

// TestRubricDetailWarnsOnSamplingCount proves the rubric-detail (genai/global
// structured) path also surfaces the samplingCount warning, alongside its normal
// (here empty) reconciliation warnings.
func TestRubricDetailWarnsOnSamplingCount(t *testing.T) {
	tmpl := rubricTemplate()
	tmpl.SamplingCount = 3
	fg := &fakeGenai{respText: rubricDetailFullSetJSON}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), tmpl, rubricInstance(), WithRubricDetail(1, 5))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !warningsContain(res.Warnings, "samplingCount=3") {
		t.Errorf("Warnings = %v, want a samplingCount warning on the rubric-detail path", res.Warnings)
	}
}

// TestNativePathDoesNotWarn proves the native path (which HONORS SamplingCount)
// never emits the genai-path warning, even with SamplingCount>1. This is the
// control that pins §5.4's asymmetry: the warning is genai-path-only.
func TestNativePathDoesNotWarn(t *testing.T) {
	fc := &fakeClient{
		resp: &aiplatformpb.EvaluateInstancesResponse{
			EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
				PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{
					Score:       proto.Float32(4.0),
					Explanation: "fine",
				},
			},
		},
	}
	tmpl := pointwiseTemplate() // SamplingCount: 4, native path
	eng := NewEngine(fc, "p", "us-central1")

	res, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"response": {Modality: registry.ModalityText, Text: "ok"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The native path applies SamplingCount for real (proof it is honored)...
	if got := fc.gotReq.GetAutoraterConfig().GetSamplingCount(); got != 4 {
		t.Errorf("native SamplingCount = %d, want 4 (honored)", got)
	}
	// ...and therefore must NOT warn that it was ignored.
	if warningsContain(res.Warnings, "ignored on the genai/global structured path") {
		t.Errorf("native path emitted a genai-path warning: %v", res.Warnings)
	}
}
