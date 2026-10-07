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

// remediation_test.go covers the review-finding fixes (issue #114): the
// local-file transport gate (AllowLocalFiles), the custom_schema responseSchema
// projection in mizan_get_metric, the non-fatal store-failure warning path, and
// the optional per-call project-override allowlist. All reuse the network-free
// harness (fake eval client + temp-file SQLite). No live cloud, no ADC.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
	registrysqlite "github.com/ghchinoy/mizan/internal/registry/sqlite"
)

// TestAssetRefLocalFileGate proves the field-assembly helper gates local file:
// inputs on the transport trust flag: rejected (tool error) when AllowLocalFiles
// is false, accepted (mapped to AssetRef.FilePath) when true. text: and gcs: are
// unaffected either way.
func TestAssetRefLocalFileGate(t *testing.T) {
	// file: rejected when local files are disabled.
	if _, err := assetRef("response", FieldValue{File: "/etc/passwd"}, false); err == nil {
		t.Fatal("expected an error for a local file: input when AllowLocalFiles=false")
	}
	// file: accepted when local files are enabled.
	ref, err := assetRef("response", FieldValue{File: "/tmp/x.png"}, true)
	if err != nil {
		t.Fatalf("file: input should be accepted when AllowLocalFiles=true: %v", err)
	}
	if ref.FilePath != "/tmp/x.png" {
		t.Errorf("file ref = %+v, want FilePath=/tmp/x.png", ref)
	}
	// gcs: unaffected by the gate.
	if _, err := assetRef("response", FieldValue{GCS: "gs://b/o"}, false); err != nil {
		t.Errorf("gcs: input should be unaffected by the gate: %v", err)
	}
}

// TestEvalRunLocalFileRejectedWhenDisabled proves the gate surfaces as a tool
// error at the handler layer BEFORE the engine runs when AllowLocalFiles=false
// (the HTTP default).
func TestEvalRunLocalFileRejectedWhenDisabled(t *testing.T) {
	fake := &evaltest.FakeEvaluationClient{}
	hz := newHarness(t, fake)
	hz.h.deps.AllowLocalFiles = false

	_, err := hz.h.run(context.Background(), EvalRunIn{
		Metric: pointwiseID,
		Fields: map[string]FieldValue{"response": {File: "/etc/passwd"}},
	})
	if err == nil {
		t.Fatal("expected a tool error for a local file: input when AllowLocalFiles=false")
	}
	if !strings.Contains(err.Error(), "local file inputs are disabled") {
		t.Errorf("error = %v, want the local-file-disabled message", err)
	}
	if fake.Calls() != 0 {
		t.Errorf("engine called %d times; want 0 (the gate must precede the run)", fake.Calls())
	}
}

// TestEvalRunStoreFailureWarning proves the non-fatal store-failure path: with no
// results store configured (Deps.Results == nil) and store default-on, the run
// still returns its eval result but with an empty runId and a warning, rather than
// failing the eval (mirrors the CLI storeResult policy).
func TestEvalRunStoreFailureWarning(t *testing.T) {
	fake := (&evaltest.FakeEvaluationClient{}).PushResponse(
		evaltest.NewPointwiseResponse(4.2, "fine"))
	hz := newHarness(t, fake)
	hz.h.deps.Results = nil // store misconfigured -> record() returns a warning

	out, err := hz.h.run(context.Background(), EvalRunIn{
		Metric: pointwiseID,
		Fields: map[string]FieldValue{"response": {Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("run should not fail on a store error: %v", err)
	}
	if out.Score == nil || *out.Score != 4.2 {
		t.Errorf("score = %v, want 4.2 (eval result still returned)", out.Score)
	}
	if out.RunID != "" {
		t.Errorf("runId = %q, want empty when the store is not configured", out.RunID)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "not persisted") {
		t.Errorf("warnings = %v, want one 'not persisted' warning", out.Warnings)
	}
}

// TestGetMetricProjectsResponseSchema proves mizan_get_metric surfaces the
// responseSchema for a custom_schema metric (design §4), projecting
// MetricTemplate.ResponseSchema.JSON.
func TestGetMetricProjectsResponseSchema(t *testing.T) {
	ctx := context.Background()
	const schemaJSON = `{"properties":{"score":{"type":"number"}},"type":"object"}`
	const customID = "test/custom"

	regStore, err := registrysqlite.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("open registry store: %v", err)
	}
	t.Cleanup(func() { _ = regStore.Close() })
	tmpl := registry.MetricTemplate{
		ID:                   customID,
		Name:                 "Custom",
		Version:              "1.0.0",
		Kind:                 registry.KindCustomSchema,
		Modalities:           []registry.Modality{registry.ModalityText},
		MetricPromptTemplate: "Score {{response}}",
		ResponseSchema:       &registry.Schema{JSON: schemaJSON},
		AutoraterModel:       "gemini-2.5-flash",
	}
	if err := regStore.Put(ctx, &tmpl); err != nil {
		t.Fatalf("seed custom_schema template: %v", err)
	}

	h := &handlers{deps: Deps{Registry: registry.NewService(regStore)}}
	out, err := h.get(ctx, GetMetricIn{ID: customID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if out.ResponseSchema == "" {
		t.Fatal("responseSchema not projected for a custom_schema metric")
	}
	// The registry canonicalizes the schema JSON on import; assert the key is
	// present rather than byte-equality.
	if !strings.Contains(out.ResponseSchema, `"score"`) {
		t.Errorf("responseSchema = %q, want it to carry the schema", out.ResponseSchema)
	}
}

// TestEvalRunProjectAllowlist proves the optional per-call project-override
// allowlist: an in-list project is routed through EngineFor; an out-of-list
// project is rejected with a tool error before any engine is built. An empty
// list (the default) allows any project.
func TestEvalRunProjectAllowlist(t *testing.T) {
	fake := (&evaltest.FakeEvaluationClient{}).PushResponse(
		evaltest.NewPointwiseResponse(1.0, "default"))
	hz := newHarness(t, fake)
	hz.h.deps.AllowedProjects = []string{"allowed-proj"}

	var factoryCalled bool
	hz.h.deps.EngineFor = func(_ context.Context, _, _ string) (*eval.Engine, func() error, error) {
		factoryCalled = true
		f := (&evaltest.FakeEvaluationClient{}).PushResponse(evaltest.NewPointwiseResponse(2.0, "override"))
		return eval.NewEngine(f, "allowed-proj", "us-central1"), func() error { return nil }, nil
	}

	// Out-of-list project is rejected before the factory is reached.
	if _, err := hz.h.run(context.Background(), EvalRunIn{
		Metric:  pointwiseID,
		Fields:  map[string]FieldValue{"response": {Text: "x"}},
		Project: "forbidden-proj",
		Store:   boolPtr(false),
	}); err == nil {
		t.Fatal("expected a tool error for a project outside the allowlist")
	}
	if factoryCalled {
		t.Error("EngineFor must not be called for an out-of-list project")
	}

	// In-list project routes through the factory engine.
	out, err := hz.h.run(context.Background(), EvalRunIn{
		Metric:  pointwiseID,
		Fields:  map[string]FieldValue{"response": {Text: "x"}},
		Project: "allowed-proj",
		Store:   boolPtr(false),
	})
	if err != nil {
		t.Fatalf("run with an allowed project: %v", err)
	}
	if !factoryCalled {
		t.Error("EngineFor should be called for an in-list project")
	}
	if out.Score == nil || *out.Score != 2.0 {
		t.Errorf("score = %v, want 2.0 (factory engine)", out.Score)
	}
}
