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

// global_route_test.go covers the R-GLOBAL auto-routing of a global-only judge
// onto the GLOBAL eval host. It uses the reusable R-SMOKE fakes
// (evaltest.FakeEvaluationClient) injected as TWO DISTINCT clients — one for the regional
// host, one for the global host — so the test can assert WHICH client received
// the call. The raw host<->endpoint mapping (NewClient) is invisible to the fake
// and is unit-tested separately by R-GAPS at the wire level; here we prove the
// GLOBAL client is the one used and the request Location is global.

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
)

// autoraterNotFound builds the SPECIFIC gRPC error the Eval Service returns when
// a global-only autorater cannot be resolved on a regional host (spike row 2),
// matching the real wire shape (codes.NotFound + the "autorater model" message).
func autoraterNotFound(resource string) error {
	return status.New(codes.NotFound,
		"Failed to make GenerateContent request to autorater model "+resource+
			". Autorater model not found. Please check that the model exists and try again.").Err()
}

// newRoutingEngine builds an engine with distinct regional + global fakes and a
// discardable notice writer, at the given configured location.
func newRoutingEngine(location string, regional, global *evaltest.FakeEvaluationClient) *Engine {
	return NewEngine(regional, "my-project", location,
		WithGlobalClient(global),
		WithNoticeWriter(&strings.Builder{}),
	)
}

func globalOnlyTemplate() registry.MetricTemplate {
	t := pointwiseTemplate()
	t.AutoraterModel = "gemini-3.5-flash" // a KNOWN global-only judge (prefix table)
	return t
}

func helpfulnessInstance() Instance {
	return Instance{Fields: map[string]AssetRef{
		"response": {Modality: registry.ModalityText, Text: "Paris is the capital of France."},
	}}
}

// 1. global-only judge (prefix fast-path) -> routes straight to the GLOBAL client
// with Location=.../locations/global; the regional client is never called.
func TestRoute_GlobalOnlyModel_RoutesGlobal(t *testing.T) {
	regional := &evaltest.FakeEvaluationClient{}
	global := (&evaltest.FakeEvaluationClient{}).PushResponse(evaltest.NewPointwiseResponse(5, "great"))
	eng := newRoutingEngine("us-central1", regional, global)

	res, err := eng.Run(context.Background(), globalOnlyTemplate(), helpfulnessInstance())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Score == nil || *res.Score != 5 {
		t.Errorf("Score = %v, want 5", res.Score)
	}
	if regional.Calls() != 0 {
		t.Errorf("regional client got %d calls, want 0 (global-only must skip the regional host)", regional.Calls())
	}
	if global.Calls() != 1 {
		t.Fatalf("global client got %d calls, want 1", global.Calls())
	}
	req := global.LastRequest()
	if want := "projects/my-project/locations/global"; req.Location != want {
		t.Errorf("global request Location = %q, want %q", req.Location, want)
	}
	if got := req.GetAutoraterConfig().GetAutoraterModel(); !strings.Contains(got, "/locations/global/") {
		t.Errorf("autorater = %q, want it expanded at locations/global", got)
	}
}

// 2. regional judge -> stays regional; the global client is NEVER touched.
func TestRoute_RegionalModel_StaysRegional(t *testing.T) {
	regional := (&evaltest.FakeEvaluationClient{}).PushResponse(evaltest.NewPointwiseResponse(4, "ok"))
	global := &evaltest.FakeEvaluationClient{}
	eng := newRoutingEngine("us-central1", regional, global)

	if _, err := eng.Run(context.Background(), pointwiseTemplate(), helpfulnessInstance()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if global.Calls() != 0 {
		t.Errorf("global client got %d calls, want 0 (a regional judge must not route global)", global.Calls())
	}
	if regional.Calls() != 1 {
		t.Fatalf("regional client got %d calls, want 1", regional.Calls())
	}
	if want := "projects/my-project/locations/us-central1"; regional.LastRequest().Location != want {
		t.Errorf("regional request Location = %q, want %q", regional.LastRequest().Location, want)
	}
}

// 3. self-correcting retry: a global-only judge NOT in the prefix table is
// attempted regionally, 404s with the SPECIFIC autorater-not-found error, and is
// transparently retried on the GLOBAL client -> success. Regional gets 1 call,
// global gets 1 call.
func TestRoute_AutoraterNotFound_RetriesGlobal(t *testing.T) {
	regional := (&evaltest.FakeEvaluationClient{}).PushError(
		autoraterNotFound("projects/my-project/locations/us-central1/publishers/google/models/gemini-4.0-preview"))
	global := (&evaltest.FakeEvaluationClient{}).PushResponse(evaltest.NewPointwiseResponse(3, "recovered"))
	eng := newRoutingEngine("us-central1", regional, global)

	tmpl := pointwiseTemplate()
	tmpl.AutoraterModel = "gemini-4.0-preview" // global-only but NOT in the prefix table

	res, err := eng.Run(context.Background(), tmpl, helpfulnessInstance())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Score == nil || *res.Score != 3 {
		t.Errorf("Score = %v, want 3", res.Score)
	}
	if regional.Calls() != 1 {
		t.Errorf("regional client got %d calls, want 1 (one regional attempt before the retry)", regional.Calls())
	}
	if global.Calls() != 1 {
		t.Fatalf("global client got %d calls, want 1 (the transparent retry)", global.Calls())
	}
	if want := "projects/my-project/locations/global"; global.LastRequest().Location != want {
		t.Errorf("retry request Location = %q, want %q", global.LastRequest().Location, want)
	}
	// The retry re-expands the autorater at locations/global (not the failed
	// regional path).
	if got := global.LastRequest().GetAutoraterConfig().GetAutoraterModel(); !strings.Contains(got, "/locations/global/") {
		t.Errorf("retry autorater = %q, want it expanded at locations/global", got)
	}
}

// 4. narrowness: a NON-autorater NotFound (a different NotFound message) does NOT
// trigger a global retry — it fails as before, and the global client is untouched.
func TestRoute_NonAutoraterNotFound_DoesNotRetry(t *testing.T) {
	regional := (&evaltest.FakeEvaluationClient{}).PushError(
		status.New(codes.NotFound, "Metric template not found in the registry.").Err())
	global := &evaltest.FakeEvaluationClient{}
	eng := newRoutingEngine("us-central1", regional, global)

	_, err := eng.Run(context.Background(), pointwiseTemplate(), helpfulnessInstance())
	if err == nil {
		t.Fatal("expected the non-autorater NotFound to surface as an error")
	}
	if global.Calls() != 0 {
		t.Errorf("global client got %d calls, want 0 (a non-autorater NotFound must not trigger a global retry)", global.Calls())
	}
	if regional.Calls() != 1 {
		t.Errorf("regional client got %d calls, want 1", regional.Calls())
	}
}

// 4b. narrowness: a non-NotFound error (e.g. RESOURCE_EXHAUSTED) does not retry.
func TestRoute_OtherError_DoesNotRetry(t *testing.T) {
	regional := (&evaltest.FakeEvaluationClient{}).PushError(
		status.New(codes.ResourceExhausted, "quota exceeded").Err())
	global := &evaltest.FakeEvaluationClient{}
	eng := newRoutingEngine("us-central1", regional, global)

	if _, err := eng.Run(context.Background(), pointwiseTemplate(), helpfulnessInstance()); err == nil {
		t.Fatal("expected the RESOURCE_EXHAUSTED error to surface")
	}
	if global.Calls() != 0 {
		t.Errorf("global client got %d calls, want 0", global.Calls())
	}
}

// 5. both hosts fail: the global retry error surfaces the ORIGINAL regional error
// plus context (so the actionable autorater-not-found is not lost).
func TestRoute_BothFail_ReturnsOriginalPlusContext(t *testing.T) {
	regional := (&evaltest.FakeEvaluationClient{}).PushError(
		autoraterNotFound("projects/my-project/locations/us-central1/publishers/google/models/gemini-4.0-preview"))
	global := (&evaltest.FakeEvaluationClient{}).PushError(
		status.New(codes.PermissionDenied, "caller lacks aiplatform.endpoints.predict").Err())
	eng := newRoutingEngine("us-central1", regional, global)

	tmpl := pointwiseTemplate()
	tmpl.AutoraterModel = "gemini-4.0-preview"

	_, err := eng.Run(context.Background(), tmpl, helpfulnessInstance())
	if err == nil {
		t.Fatal("expected an error when both regional and global attempts fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Autorater model not found") {
		t.Errorf("error should preserve the original autorater-not-found cause: %v", err)
	}
	if !strings.Contains(msg, "global-host retry also failed") {
		t.Errorf("error should note the failed global retry: %v", err)
	}
}

// 6. the pre-flight echo reflects global up front for a KNOWN global-only judge
// (prefix fast-path), and stays regional for an ordinary judge.
func TestResolve_GlobalOnlyModel_EchoesGlobal(t *testing.T) {
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "p", "us-central1", WithGlobalClient(&evaltest.FakeEvaluationClient{}))

	got := eng.Resolve(globalOnlyTemplate(), "", false)
	if got.Location != globalLocation {
		t.Errorf("global-only pre-flight Location = %q, want %q", got.Location, globalLocation)
	}
	if got.Path != "native" {
		t.Errorf("global-only pre-flight Path = %q, want native", got.Path)
	}

	reg := eng.Resolve(pointwiseTemplate(), "", false)
	if reg.Location != "us-central1" {
		t.Errorf("regional pre-flight Location = %q, want us-central1", reg.Location)
	}
}

// 7. when the engine is already configured at location=global, a global-only
// judge simply runs on the primary client (which IS the global host); no separate
// global client is required and the fast-path does not need it.
func TestRoute_AlreadyGlobal_UsesPrimaryClient(t *testing.T) {
	primary := (&evaltest.FakeEvaluationClient{}).PushResponse(evaltest.NewPointwiseResponse(5, "ok"))
	// No global client injected; location is already global.
	eng := NewEngine(primary, "my-project", "global", WithNoticeWriter(&strings.Builder{}))

	if _, err := eng.Run(context.Background(), globalOnlyTemplate(), helpfulnessInstance()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if primary.Calls() != 1 {
		t.Fatalf("primary client got %d calls, want 1", primary.Calls())
	}
	if want := "projects/my-project/locations/global"; primary.LastRequest().Location != want {
		t.Errorf("request Location = %q, want %q", primary.LastRequest().Location, want)
	}
}
