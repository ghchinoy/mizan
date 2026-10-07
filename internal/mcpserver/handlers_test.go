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

package mcpserver

// These tests exercise the SDK-agnostic tool handlers directly (no transport, no
// SDK types), per design §8: an eval.Engine over the network-free
// evaltest.FakeEvaluationClient, a registry.Service over a temp-file SQLite store
// seeded with a pointwise and a pairwise template, and a results.Service over a
// temp-file SQLite store. No live cloud, no ADC.

import (
	"context"
	"path/filepath"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
	registrysqlite "github.com/ghchinoy/mizan/internal/registry/sqlite"
	"github.com/ghchinoy/mizan/internal/results"
	resultssqlite "github.com/ghchinoy/mizan/internal/results/sqlite"
)

const (
	pointwiseID = "test/helpfulness"
	pairwiseID  = "test/preference"
)

func pointwiseTemplate() registry.MetricTemplate {
	return registry.MetricTemplate{
		ID:                   pointwiseID,
		Name:                 "Helpfulness",
		Description:          "Rate response helpfulness.",
		Version:              "1.0.0",
		Kind:                 registry.KindPointwise,
		Modalities:           []registry.Modality{registry.ModalityText},
		Inputs:               []registry.InputSpec{{Name: "response", Modality: registry.ModalityText, Required: true}},
		MetricPromptTemplate: "Rate the helpfulness of this response: {{response}}",
		AutoraterModel:       "gemini-2.5-flash",
		SamplingCount:        4,
		Tags:                 []string{"quality"},
	}
}

func pairwiseTemplate() registry.MetricTemplate {
	return registry.MetricTemplate{
		ID:                   pairwiseID,
		Name:                 "Preference",
		Description:          "Which response is better.",
		Version:              "1.0.0",
		Kind:                 registry.KindPairwise,
		Modalities:           []registry.Modality{registry.ModalityText},
		MetricPromptTemplate: "Which is better, {{baseline}} or {{candidate}}?",
		BaselineFieldName:    "baseline",
		CandidateFieldName:   "candidate",
		AutoraterModel:       "gemini-2.5-flash",
		SamplingCount:        4,
	}
}

// harness bundles the handlers under test with the seeded services so individual
// tests can assert on persistence.
type harness struct {
	h       *handlers
	results *results.Service
}

// newHarness builds a fully network-free handler set: a registry.Service over a
// temp SQLite store seeded with both templates, an engine over the fake
// evaluation client (scripted by the caller), and a results.Service over a temp
// SQLite store.
func newHarness(t *testing.T, fake *evaltest.FakeEvaluationClient) harness {
	t.Helper()
	ctx := context.Background()

	regStore, err := registrysqlite.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("open registry store: %v", err)
	}
	t.Cleanup(func() { _ = regStore.Close() })
	for _, tmpl := range []registry.MetricTemplate{pointwiseTemplate(), pairwiseTemplate()} {
		tmpl := tmpl
		if err := regStore.Put(ctx, &tmpl); err != nil {
			t.Fatalf("seed template %s: %v", tmpl.ID, err)
		}
	}
	reg := registry.NewService(regStore)

	resStore, err := resultssqlite.Open(filepath.Join(t.TempDir(), "results.db"))
	if err != nil {
		t.Fatalf("open results store: %v", err)
	}
	t.Cleanup(func() { _ = resStore.Close() })
	res := results.NewService(resStore)

	eng := eval.NewEngine(fake, "proj", "us-central1")

	return harness{
		h: &handlers{deps: Deps{
			Registry: reg,
			Engine:   eng,
			Results:  res,
			Config:   &config.Config{ProjectID: "proj", Location: "us-central1"},
		}},
		results: res,
	}
}

func boolPtr(b bool) *bool { return &b }

func TestListMetrics(t *testing.T) {
	hz := newHarness(t, &evaltest.FakeEvaluationClient{})
	out, err := hz.h.list(context.Background(), ListMetricsIn{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out.Metrics) != 2 {
		t.Fatalf("got %d metrics, want 2", len(out.Metrics))
	}
	var found bool
	for _, m := range out.Metrics {
		if m.ID != pointwiseID {
			continue
		}
		found = true
		if m.Name != "Helpfulness" {
			t.Errorf("name = %q, want Helpfulness", m.Name)
		}
		if m.Kind != string(registry.KindPointwise) {
			t.Errorf("kind = %q, want pointwise", m.Kind)
		}
		if len(m.Modalities) != 1 || m.Modalities[0] != "text" {
			t.Errorf("modalities = %v, want [text]", m.Modalities)
		}
	}
	if !found {
		t.Errorf("pointwise metric %q not in list", pointwiseID)
	}
}

func TestListMetricsKindFilter(t *testing.T) {
	hz := newHarness(t, &evaltest.FakeEvaluationClient{})
	out, err := hz.h.list(context.Background(), ListMetricsIn{Kinds: []string{string(registry.KindPairwise)}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out.Metrics) != 1 || out.Metrics[0].ID != pairwiseID {
		t.Fatalf("kind filter = %+v, want only %q", out.Metrics, pairwiseID)
	}
}

func TestGetMetric(t *testing.T) {
	hz := newHarness(t, &evaltest.FakeEvaluationClient{})
	out, err := hz.h.get(context.Background(), GetMetricIn{ID: pointwiseID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if out.ID != pointwiseID || out.Name != "Helpfulness" || out.Kind != "pointwise" {
		t.Errorf("got %+v", out)
	}
	if len(out.Modalities) != 1 || out.Modalities[0] != "text" {
		t.Errorf("modalities = %v, want [text]", out.Modalities)
	}
	if len(out.Inputs) != 1 || out.Inputs[0].Name != "response" || !out.Inputs[0].Required {
		t.Errorf("inputs = %+v, want one required 'response'", out.Inputs)
	}
}

func TestGetMetricUnknownIsToolError(t *testing.T) {
	hz := newHarness(t, &evaltest.FakeEvaluationClient{})
	if _, err := hz.h.get(context.Background(), GetMetricIn{ID: "test/nope"}); err == nil {
		t.Fatal("expected an error for an unknown metric id (tool error)")
	}
}

func TestEvalRunPersistsAndMaps(t *testing.T) {
	fake := (&evaltest.FakeEvaluationClient{}).PushResponse(
		evaltest.NewPointwiseResponse(4.5, "Clear and correct."))
	hz := newHarness(t, fake)

	out, err := hz.h.run(context.Background(), EvalRunIn{
		Metric: pointwiseID,
		Fields: map[string]FieldValue{"response": {Text: "Click the reset link."}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.Score == nil || *out.Score != 4.5 {
		t.Errorf("score = %v, want 4.5", out.Score)
	}
	if out.Explanation != "Clear and correct." {
		t.Errorf("explanation = %q", out.Explanation)
	}
	if out.RunID == "" {
		t.Fatal("expected a non-empty runId (store default-on)")
	}
	// The persisted result round-trips by its ULID runId.
	stored, err := hz.results.Get(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("results.Get(%q): %v", out.RunID, err)
	}
	if stored.RunID != out.RunID {
		t.Errorf("stored RunID = %q, want %q", stored.RunID, out.RunID)
	}
	if stored.Template.ID != pointwiseID {
		t.Errorf("stored template id = %q, want %q", stored.Template.ID, pointwiseID)
	}
}

func TestEvalRunStoreFalseSkipsPersistence(t *testing.T) {
	fake := (&evaltest.FakeEvaluationClient{}).PushResponse(
		evaltest.NewPointwiseResponse(3.0, "ok"))
	hz := newHarness(t, fake)

	out, err := hz.h.run(context.Background(), EvalRunIn{
		Metric: pointwiseID,
		Fields: map[string]FieldValue{"response": {Text: "fine"}},
		Store:  boolPtr(false),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.RunID != "" {
		t.Errorf("runId = %q, want empty when store=false", out.RunID)
	}
	all, err := hz.results.List(context.Background(), results.ResultFilter{})
	if err != nil {
		t.Fatalf("results.List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("results store has %d entries, want 0 when store=false", len(all))
	}
}

func TestEvalRunMissingFieldIsToolError(t *testing.T) {
	fake := (&evaltest.FakeEvaluationClient{}).PushResponse(
		evaltest.NewPointwiseResponse(1.0, "x"))
	hz := newHarness(t, fake)
	// The pointwise template references {{response}}; omit it.
	if _, err := hz.h.run(context.Background(), EvalRunIn{
		Metric: pointwiseID,
		Fields: map[string]FieldValue{"wrong": {Text: "y"}},
	}); err == nil {
		t.Fatal("expected an error when a required field is missing")
	}
}

func TestEvalRunBadFieldShapeIsToolError(t *testing.T) {
	hz := newHarness(t, &evaltest.FakeEvaluationClient{})
	// A field with neither text/file/gcs set is an error before the engine runs.
	if _, err := hz.h.run(context.Background(), EvalRunIn{
		Metric: pointwiseID,
		Fields: map[string]FieldValue{"response": {}},
	}); err == nil {
		t.Fatal("expected an error for an empty field value")
	}
}

func TestEvalPairwiseMapsChoice(t *testing.T) {
	cases := []struct {
		name   string
		choice aiplatformpb.PairwiseChoice
		want   string
	}{
		{"baseline", aiplatformpb.PairwiseChoice_BASELINE, "BASELINE"},
		{"candidate", aiplatformpb.PairwiseChoice_CANDIDATE, "CANDIDATE"},
		{"tie", aiplatformpb.PairwiseChoice_TIE, "TIE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := (&evaltest.FakeEvaluationClient{}).PushResponse(
				evaltest.NewPairwiseResponse(tc.choice, "because"))
			hz := newHarness(t, fake)

			out, err := hz.h.pairwise(context.Background(), EvalPairwiseIn{
				Metric:    pairwiseID,
				Baseline:  FieldValue{Text: "answer A"},
				Candidate: FieldValue{Text: "answer B"},
			})
			if err != nil {
				t.Fatalf("pairwise: %v", err)
			}
			if out.PairwiseChoice != tc.want {
				t.Errorf("pairwiseChoice = %q, want %q", out.PairwiseChoice, tc.want)
			}
			if out.Explanation != "because" {
				t.Errorf("explanation = %q", out.Explanation)
			}
			if out.RunID == "" {
				t.Error("expected a non-empty runId (store default-on)")
			}
		})
	}
}

func TestEvalRunUsesEngineForOnOverride(t *testing.T) {
	// Default engine returns 1.0; a per-call override must route through EngineFor's
	// engine instead, which returns a distinguishable 9.9.
	defFake := (&evaltest.FakeEvaluationClient{}).PushResponse(
		evaltest.NewPointwiseResponse(1.0, "default"))
	hz := newHarness(t, defFake)

	factoryFake := (&evaltest.FakeEvaluationClient{}).PushResponse(
		evaltest.NewPointwiseResponse(9.9, "override"))
	var gotProject, gotLocation string
	var closed bool
	hz.h.deps.EngineFor = func(_ context.Context, project, location string) (*eval.Engine, func() error, error) {
		gotProject, gotLocation = project, location
		return eval.NewEngine(factoryFake, "proj2", "us-east1"), func() error { closed = true; return nil }, nil
	}

	out, err := hz.h.run(context.Background(), EvalRunIn{
		Metric:   pointwiseID,
		Fields:   map[string]FieldValue{"response": {Text: "x"}},
		Project:  "proj2",
		Location: "us-east1",
		Store:    boolPtr(false),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.Score == nil || *out.Score != 9.9 {
		t.Errorf("score = %v, want 9.9 (factory-built engine)", out.Score)
	}
	if gotProject != "proj2" || gotLocation != "us-east1" {
		t.Errorf("factory got project=%q location=%q, want proj2/us-east1", gotProject, gotLocation)
	}
	if !closed {
		t.Error("closeFn was not invoked")
	}
	if defFake.Calls() != 0 {
		t.Errorf("default engine received %d calls; want the factory engine to be used", defFake.Calls())
	}
	if factoryFake.Calls() != 1 {
		t.Errorf("factory engine received %d calls, want 1", factoryFake.Calls())
	}
}

func TestEvalRunOverrideWithoutFactoryIsToolError(t *testing.T) {
	hz := newHarness(t, &evaltest.FakeEvaluationClient{})
	hz.h.deps.EngineFor = nil // explicit: no per-call engine factory configured
	if _, err := hz.h.run(context.Background(), EvalRunIn{
		Metric:  pointwiseID,
		Fields:  map[string]FieldValue{"response": {Text: "x"}},
		Project: "other-proj",
	}); err == nil {
		t.Fatal("expected a tool error when an override is requested but EngineFor is nil")
	}
}

func TestEvalPairwiseNonPairwiseMetricIsToolError(t *testing.T) {
	fake := (&evaltest.FakeEvaluationClient{}).PushResponse(
		evaltest.NewPointwiseResponse(1.0, "x"))
	hz := newHarness(t, fake)
	if _, err := hz.h.pairwise(context.Background(), EvalPairwiseIn{
		Metric:    pointwiseID, // not a pairwise metric
		Baseline:  FieldValue{Text: "a"},
		Candidate: FieldValue{Text: "b"},
	}); err == nil {
		t.Fatal("expected an error when the metric is not pairwise")
	}
}
