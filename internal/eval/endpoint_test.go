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

// endpoint_test.go covers the host-decides behavior on the self-correcting
// global retry for a FULLY-QUALIFIED regional autorater id.

import (
	"context"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
)

// TestRoute_FullyQualifiedRegionalModel_GlobalRetrySucceeds documents the
// host-decides behavior (R-GLOBAL review FYI): a FULLY-QUALIFIED regional
// autorater id (projects/.../locations/us-central1/.../models/<id>) is NOT
// re-expanded at global on the retry — expandAutoraterModel passes a fully-
// qualified id through verbatim — yet the global retry still succeeds because the
// eval HOST, not the autorater resource path, is what resolves the model. The
// model is chosen NOT in the prefix table so the retry (not the fast-path) fires.
func TestRoute_FullyQualifiedRegionalModel_GlobalRetrySucceeds(t *testing.T) {
	const fqModel = "projects/my-project/locations/us-central1/publishers/google/models/some-future-judge"

	regional := (&evaltest.FakeEvaluationClient{}).PushError(
		autoraterNotFound(fqModel)) // regional host 404s the autorater
	global := (&evaltest.FakeEvaluationClient{}).PushResponse(
		evaltest.NewPointwiseResponse(5, "recovered on global host"))

	eng := NewEngine(regional, "my-project", "us-central1",
		WithGlobalClient(global), WithNoticeWriter(&strings.Builder{}))

	tmpl := registry.MetricTemplate{
		ID:                   "test/pointwise",
		Kind:                 registry.KindPointwise,
		MetricPromptTemplate: "Rate {{response}}",
	}
	inst := Instance{Fields: map[string]AssetRef{
		"response": {Modality: registry.ModalityText, Text: "hello"},
	}}

	res, err := eng.Run(context.Background(), tmpl, inst, WithModel(fqModel))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Score == nil || *res.Score != 5 {
		t.Errorf("Score = %v, want 5 (global retry result)", res.Score)
	}

	// Retry fired: one regional attempt, then one global attempt.
	if regional.Calls() != 1 {
		t.Errorf("regional calls = %d, want 1", regional.Calls())
	}
	if global.Calls() != 1 {
		t.Fatalf("global calls = %d, want 1 (the transparent retry)", global.Calls())
	}
	// The RETRY request carries the global location...
	if want := "projects/my-project/locations/global"; global.LastRequest().Location != want {
		t.Errorf("retry request Location = %q, want %q", global.LastRequest().Location, want)
	}
	// ...but the fully-qualified autorater resource is passed through UNCHANGED
	// (still locations/us-central1) — host-decides: the id is NOT re-expanded.
	autorater := global.LastRequest().GetAutoraterConfig().GetAutoraterModel()
	if autorater != fqModel {
		t.Errorf("autorater model = %q, want the fully-qualified id unchanged %q", autorater, fqModel)
	}
	if !strings.Contains(autorater, "/locations/us-central1/") {
		t.Errorf("autorater model should still name the regional location (host decides), got %q", autorater)
	}
}
