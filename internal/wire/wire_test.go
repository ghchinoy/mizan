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

package wire

// wire_test.go unit-tests the composition root's client/engine construction
// WITHOUT opening real gRPC clients: it substitutes the newNativeClient /
// newGenaiClient seams with fakes and asserts the config -> client/engine wiring.
//
// The headline assertions are the native-vs-global endpoint selection (NewEngine
// builds one native client at cfg.Location and a DISTINCT one at
// eval.GenaiLocation, wired via eval.WithGlobalClient) and that both native
// clients are closed on shutdown. Routing behavior is then confirmed end-to-end
// by running the engine and checking WHICH fake received the call — a regional
// judge stays regional, a known global-only judge goes to the global client.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
)

// TestOpenService builds a real SQLite-backed registry.Service at a temp path and
// confirms the returned service is usable and its close function succeeds — the
// composition root's other half (no gRPC involved, pure-Go sqlite).
func TestOpenService(t *testing.T) {
	cfg := &config.Config{RegistryDBPath: filepath.Join(t.TempDir(), "registry.db")}
	svc, closeFn, err := OpenService(cfg)
	if err != nil {
		t.Fatalf("OpenService: %v", err)
	}
	if svc == nil {
		t.Fatal("OpenService returned nil service")
	}
	// A trivial round-trip proves the store is wired and open.
	if _, err := svc.List(context.Background(), registry.ListFilter{}); err != nil {
		t.Errorf("List on fresh service: %v", err)
	}
	if err := closeFn(); err != nil {
		t.Errorf("close: %v", err)
	}
}

// TestOpenService_WiresCodecAndInjectsSyncConfig checks the P2 composition-root
// wiring (design §3.1/§3.11): the ONLY place that knows the concrete codec/sync
// wiring assembles a YAML-backed service with a SyncConfig derived from config,
// so cmd/* never imports the codec/sync packages.
//
// TEETH DISCLOSURE — the two halves differ in how much they prove:
//   - SyncConfig half: DISCRIMINATING. NewService defaults SyncConfig to the
//     zero value, so dropping WithSyncConfig from OpenService makes this fail.
//   - Codec half: SMOKE / END-STATE only. NewService already defaults the codec
//     to YAMLCodec (service.go), so this cannot detect a dropped WithCodec — it
//     merely confirms the service ends up YAML-backed. The WithCodec *mechanism*
//     is proven to have teeth by registry.TestNewService_CodecInjectionHasTeeth
//     (a sentinel codec that the default never returns). Do not read the codec
//     half here as proof-of-injection.
func TestOpenService_WiresCodecAndInjectsSyncConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		RegistryDBPath:       filepath.Join(dir, "registry.db"),
		PackCacheDir:         filepath.Join(dir, "packcache"),
		DefaultTemplatesRepo: "github.com/ghchinoy/mizan-templates",
	}
	svc, closeFn, err := OpenService(cfg)
	if err != nil {
		t.Fatalf("OpenService: %v", err)
	}
	defer func() { _ = closeFn() }()

	// Codec half (smoke/end-state): the service is YAML-backed. This does NOT
	// prove WithCodec injection (default is also YAML) — see the teeth test in
	// package registry referenced above.
	codec := svc.Codec()
	if _, ok := codec.(registry.YAMLCodec); !ok {
		t.Errorf("OpenService codec = %T, want registry.YAMLCodec", codec)
	}
	if got := codec.Ext(); got != "yaml" {
		t.Errorf("codec Ext() = %q, want %q", got, "yaml")
	}

	// SyncConfig half (discriminating): derived from config, field-for-field.
	// NewService defaults this to the zero value, so removing WithSyncConfig from
	// OpenService makes this assertion fail — real teeth.
	want := registry.SyncConfig{
		PackCacheDir:         cfg.PackCacheDir,
		DefaultTemplatesRepo: cfg.DefaultTemplatesRepo,
	}
	if got := svc.SyncConfig(); got != want {
		t.Errorf("OpenService injected SyncConfig = %+v, want %+v", got, want)
	}
}

// builtNative records one newNativeClient construction so a test can assert the
// (location, apiEndpoint) each native client was built for and inspect the fake.
type builtNative struct {
	location    string
	apiEndpoint string
	client      *evaltest.FakeEvaluationClient
}

// builtGenai records one newGenaiClient construction.
type builtGenai struct {
	project  string
	location string
}

// installSeams swaps the wire client-construction seams for fakes and restores
// them at test end. The native factory returns a fresh, sticky-success fake per
// call (so an engine run succeeds on whichever client it routes to) and records
// the construction args. The genai factory returns an inert fake.
func installSeams(t *testing.T) (*[]*builtNative, *[]builtGenai) {
	t.Helper()
	prevNative, prevGenai := newNativeClient, newGenaiClient
	t.Cleanup(func() { newNativeClient, newGenaiClient = prevNative, prevGenai })

	var natives []*builtNative
	var genais []builtGenai

	newNativeClient = func(_ context.Context, location, apiEndpoint string) (closableEvalClient, error) {
		fc := &evaltest.FakeEvaluationClient{Resp: evaltest.NewPointwiseResponse(4, "ok")}
		natives = append(natives, &builtNative{location: location, apiEndpoint: apiEndpoint, client: fc})
		return fc, nil
	}
	newGenaiClient = func(_ context.Context, project, location string) (eval.GenaiClient, error) {
		genais = append(genais, builtGenai{project: project, location: location})
		return &evaltest.FakeGenaiClient{}, nil
	}
	return &natives, &genais
}

// pointwiseTemplate is a minimal valid native pointwise template + instance.
func pointwiseTemplate() (registry.MetricTemplate, eval.Instance) {
	tmpl := registry.MetricTemplate{
		ID:                   "test/pointwise",
		Kind:                 registry.KindPointwise,
		MetricPromptTemplate: "Rate {{response}}",
	}
	inst := eval.Instance{Fields: map[string]eval.AssetRef{
		"response": {Modality: registry.ModalityText, Text: "hello"},
	}}
	return tmpl, inst
}

// TestNewEngine_NativeVsGlobalEndpointSelection is the headline wire test: the
// composition root builds TWO distinct native clients — regional (cfg.Location)
// and global (eval.GenaiLocation) — plus one genai client at GenaiLocation, and
// wires the global one via eval.WithGlobalClient so a global-only judge routes to
// it while a regional judge stays on the regional client.
func TestNewEngine_NativeVsGlobalEndpointSelection(t *testing.T) {
	natives, genais := installSeams(t)

	cfg := &config.Config{ProjectID: "proj", Location: "us-central1"}
	eng, closeFn, err := NewEngine(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// Two native clients: regional first, global second (both with no endpoint
	// override, so endpointFor maps them to the regional and global hosts).
	if len(*natives) != 2 {
		t.Fatalf("native clients built = %d, want 2", len(*natives))
	}
	if got := (*natives)[0].location; got != "us-central1" {
		t.Errorf("regional native client location = %q, want %q", got, "us-central1")
	}
	if got := (*natives)[1].location; got != eval.GenaiLocation {
		t.Errorf("global native client location = %q, want %q", got, eval.GenaiLocation)
	}
	if (*natives)[0].client == (*natives)[1].client {
		t.Error("regional and global native clients must be DISTINCT instances")
	}
	for i, bn := range *natives {
		if bn.apiEndpoint != "" {
			t.Errorf("native client %d apiEndpoint = %q, want empty (no override in cfg)", i, bn.apiEndpoint)
		}
	}

	// One genai client, at the global genai location.
	if len(*genais) != 1 {
		t.Fatalf("genai clients built = %d, want 1", len(*genais))
	}
	if got := (*genais)[0].location; got != eval.GenaiLocation {
		t.Errorf("genai client location = %q, want %q", got, eval.GenaiLocation)
	}
	if got := (*genais)[0].project; got != "proj" {
		t.Errorf("genai client project = %q, want %q", got, "proj")
	}

	regional, global := (*natives)[0].client, (*natives)[1].client
	tmpl, inst := pointwiseTemplate()

	// A regional judge stays on the regional client.
	if _, err := eng.Run(context.Background(), tmpl, inst, eval.WithModel("gemini-2.5-flash")); err != nil {
		t.Fatalf("Run(regional model): %v", err)
	}
	if regional.Calls() != 1 || global.Calls() != 0 {
		t.Fatalf("regional judge routing: regional=%d global=%d, want regional=1 global=0",
			regional.Calls(), global.Calls())
	}

	// A known global-only judge routes to the DISTINCT global client wired via
	// WithGlobalClient — proving the endpoint selection is live end-to-end.
	if _, err := eng.Run(context.Background(), tmpl, inst, eval.WithModel("gemini-3.5-flash")); err != nil {
		t.Fatalf("Run(global-only model): %v", err)
	}
	if regional.Calls() != 1 || global.Calls() != 1 {
		t.Fatalf("global-only judge routing: regional=%d global=%d, want regional=1 global=1",
			regional.Calls(), global.Calls())
	}

	// Close must close BOTH native clients.
	if err := closeFn(); err != nil {
		t.Fatalf("closeFn: %v", err)
	}
	if regional.Closed != 1 || global.Closed != 1 {
		t.Errorf("close counts: regional=%d global=%d, want 1 and 1", regional.Closed, global.Closed)
	}
}

// TestNewEngine_HonorsAPIEndpointOverride proves the configured APIEndpoint is
// threaded to BOTH native client constructions (the endpoint allow-list already
// validated it upstream).
func TestNewEngine_HonorsAPIEndpointOverride(t *testing.T) {
	natives, _ := installSeams(t)

	cfg := &config.Config{ProjectID: "proj", Location: "europe-west4", APIEndpoint: "custom-aiplatform.googleapis.com:443"}
	_, closeFn, err := NewEngine(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = closeFn() }()

	if len(*natives) != 2 {
		t.Fatalf("native clients built = %d, want 2", len(*natives))
	}
	for i, bn := range *natives {
		if bn.apiEndpoint != cfg.APIEndpoint {
			t.Errorf("native client %d apiEndpoint = %q, want %q", i, bn.apiEndpoint, cfg.APIEndpoint)
		}
	}
	// Locations are still regional then global regardless of the endpoint override.
	if (*natives)[0].location != "europe-west4" || (*natives)[1].location != eval.GenaiLocation {
		t.Errorf("locations = [%q, %q], want [%q, %q]",
			(*natives)[0].location, (*natives)[1].location, "europe-west4", eval.GenaiLocation)
	}
}

// TestNewEngine_GenaiClientErrorClosesRegional proves the first native client is
// closed when a later construction step fails, so NewEngine leaks no handle on an
// error path.
func TestNewEngine_GenaiClientErrorClosesRegional(t *testing.T) {
	prevNative, prevGenai := newNativeClient, newGenaiClient
	t.Cleanup(func() { newNativeClient, newGenaiClient = prevNative, prevGenai })

	var first *evaltest.FakeEvaluationClient
	newNativeClient = func(_ context.Context, _, _ string) (closableEvalClient, error) {
		fc := &evaltest.FakeEvaluationClient{}
		if first == nil {
			first = fc
		}
		return fc, nil
	}
	wantErr := context.Canceled // any sentinel
	newGenaiClient = func(_ context.Context, _, _ string) (eval.GenaiClient, error) {
		return nil, wantErr
	}

	cfg := &config.Config{ProjectID: "proj", Location: "us-central1"}
	_, _, err := NewEngine(context.Background(), cfg)
	if err == nil {
		t.Fatal("NewEngine: expected error from genai client construction")
	}
	if first == nil || first.Closed != 1 {
		t.Errorf("regional client not closed on genai-error path (first=%v)", first)
	}
}

// TestNewEngine_NativeClientErrorPropagates proves a native client construction
// error is returned and no engine is built.
func TestNewEngine_NativeClientErrorPropagates(t *testing.T) {
	prevNative, prevGenai := newNativeClient, newGenaiClient
	t.Cleanup(func() { newNativeClient, newGenaiClient = prevNative, prevGenai })

	newNativeClient = func(_ context.Context, _, _ string) (closableEvalClient, error) {
		return nil, context.Canceled
	}
	newGenaiClient = func(_ context.Context, _, _ string) (eval.GenaiClient, error) {
		t.Fatal("genai client must not be built when the first native client fails")
		return nil, nil
	}

	cfg := &config.Config{ProjectID: "proj", Location: "us-central1"}
	eng, closeFn, err := NewEngine(context.Background(), cfg)
	if err == nil {
		t.Fatal("NewEngine: expected error")
	}
	if eng != nil || closeFn != nil {
		t.Errorf("on error, engine and closeFn must be nil, got eng!=nil=%t closeFn!=nil=%t", eng != nil, closeFn != nil)
	}
}
