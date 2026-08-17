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

// permdenied_route_test.go covers friction #4: a project-level autorater
// PermissionDenied must be CLASSIFIED (isAutoraterPermissionDenied) and turned
// into an ACTIONABLE error at the three evaluateRouted error sites, instead of
// passing Vertex's misleading "expect a delay and retry" string through verbatim.
// It also pins the (correct) behavior that a PermissionDenied does NOT trigger
// the global-host retry — only an autorater NotFound does. All errors are
// fabricated status errors; no network.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ghchinoy/mizan/internal/eval/evaltest"
)

// autoraterPermissionDenied builds the SPECIFIC gRPC error the Eval Service
// returns when the inner autorater GenerateContent is denied at the project
// level, matching the real wire shape (codes.PermissionDenied + the misleading
// "new project, expect a delay and retry" tail).
func autoraterPermissionDenied(resource string) error {
	return status.New(codes.PermissionDenied,
		"Failed to make GenerateContent request to autorater model "+resource+
			". If you're using a new project, expect a delay and retry the request in a few minutes.").Err()
}

// --- classifier unit coverage -------------------------------------------------

func TestIsAutoraterPermissionDenied(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			"permission-denied + autorater-model marker",
			autoraterPermissionDenied("projects/p/locations/global/publishers/google/models/gemini-2.5-flash"),
			true,
		},
		{
			"permission-denied + GenerateContent marker only",
			status.New(codes.PermissionDenied, "Failed to make GenerateContent request.").Err(),
			true,
		},
		{
			"permission-denied WITHOUT autorater/GenerateContent marker",
			status.New(codes.PermissionDenied, "caller lacks aiplatform.locations.evaluateInstances").Err(),
			false,
		},
		{
			"NotFound + autorater marker (that's the OTHER classifier)",
			autoraterNotFound("projects/p/locations/us-central1/publishers/google/models/gemini-2.5-flash"),
			false,
		},
		{
			"other code (ResourceExhausted) + marker",
			status.New(codes.ResourceExhausted, "autorater model quota exceeded for GenerateContent").Err(),
			false,
		},
		// GAP-1: the spec calls out Internal/Unavailable/InvalidArgument by name —
		// the classifier keys STRICTLY on codes.PermissionDenied, so even when the
		// autorater/GenerateContent marker is present these transient/other codes
		// must NOT be misclassified as the actionable permission-denied condition.
		{
			"other code (Internal) + marker",
			status.New(codes.Internal, "internal error handling GenerateContent request to autorater model").Err(),
			false,
		},
		{
			"other code (Unavailable) + marker",
			status.New(codes.Unavailable, "autorater model backend temporarily unavailable for GenerateContent").Err(),
			false,
		},
		{
			"other code (InvalidArgument) + marker",
			status.New(codes.InvalidArgument, "invalid GenerateContent request to autorater model").Err(),
			false,
		},
		// GAP-2: a wrapped/nested status error must still be classified — the fix
		// relies on status.FromError, which unwraps via errors.As. A plain non-status
		// error (no gRPC status anywhere in the chain) must stay false.
		{
			"wrapped permission-denied + marker (status.FromError unwrap)",
			fmt.Errorf("evaluate call failed: %w",
				autoraterPermissionDenied("projects/p/locations/global/publishers/google/models/gemini-2.5-flash")),
			true,
		},
		{
			"doubly-wrapped permission-denied + marker (nested unwrap)",
			fmt.Errorf("outer: %w", fmt.Errorf("inner: %w",
				status.New(codes.PermissionDenied, "Failed to make GenerateContent request.").Err())),
			true,
		},
		{
			"plain non-status error (no gRPC status in chain)",
			fmt.Errorf("autorater model GenerateContent boom"),
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAutoraterPermissionDenied(tc.err); got != tc.want {
				t.Errorf("isAutoraterPermissionDenied(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// assertActionable checks the actionable message carries project/location/model
// and the three concrete fixes, while preserving the raw underlying string.
func assertActionable(t *testing.T, err error, project, location, model, rawMarker string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an actionable error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		project,
		location,
		model,
		"project IAM/enablement issue, NOT a transient delay",
		"Vertex AI API is enabled",
		"roles/aiplatform.serviceAgent",
		"gcp-sa-aiplatform.iam.gserviceaccount.com",
		"roles/storage.objectViewer",
		rawMarker, // the raw underlying error is appended, not discarded
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("actionable error missing %q\nfull: %v", want, err)
		}
	}
	// The misleading raw "retry" framing must not be presented as our guidance.
	if !strings.Contains(msg, "raw:") {
		t.Errorf("actionable error should append the raw error under a (raw: ...) tail: %v", err)
	}
}

// --- site 3: regional attempt PermissionDenied -> actionable, NO retry --------

func TestRoute_PermissionDenied_RegionalSite_ActionableNoRetry(t *testing.T) {
	regional := (&evaltest.FakeEvaluationClient{}).PushError(
		autoraterPermissionDenied("projects/my-project/locations/us-central1/publishers/google/models/gemini-2.5-flash"))
	global := &evaltest.FakeEvaluationClient{}
	eng := newRoutingEngine("us-central1", regional, global)

	_, err := eng.Run(context.Background(), pointwiseTemplate(), helpfulnessInstance())
	assertActionable(t, err, "my-project", "us-central1", "gemini-2.5-flash", "expect a delay and retry")

	// Deliverable #3: a PermissionDenied is NOT host-dependent — it must NOT
	// trigger the global-host retry (unlike an autorater NotFound).
	if global.Calls() != 0 {
		t.Errorf("global client got %d calls, want 0 (PermissionDenied must NOT retry global)", global.Calls())
	}
	if regional.Calls() != 1 {
		t.Errorf("regional client got %d calls, want 1", regional.Calls())
	}
}

// --- site 1: global-only fast-path PermissionDenied -> actionable -------------

func TestRoute_PermissionDenied_GlobalOnlyFastPath_Actionable(t *testing.T) {
	regional := &evaltest.FakeEvaluationClient{}
	global := (&evaltest.FakeEvaluationClient{}).PushError(
		autoraterPermissionDenied("projects/my-project/locations/global/publishers/google/models/gemini-3.5-flash"))
	eng := newRoutingEngine("us-central1", regional, global)

	_, err := eng.Run(context.Background(), globalOnlyTemplate(), helpfulnessInstance())
	// location echoes the configured region (kept for labeling); model is the
	// global-only judge routed via the fast-path.
	assertActionable(t, err, "my-project", "us-central1", "gemini-3.5-flash", "expect a delay and retry")

	if regional.Calls() != 0 {
		t.Errorf("regional client got %d calls, want 0 (global-only fast-path skips regional)", regional.Calls())
	}
	if global.Calls() != 1 {
		t.Errorf("global client got %d calls, want 1", global.Calls())
	}
}

// --- site 2: regional NotFound retries global, global PermissionDenied ---------

func TestRoute_PermissionDenied_OnGlobalRetry_Actionable(t *testing.T) {
	// Regional attempt is an autorater NotFound (this DOES retry global), and the
	// global retry is itself denied at the project level -> actionable guidance.
	regional := (&evaltest.FakeEvaluationClient{}).PushError(
		autoraterNotFound("projects/my-project/locations/us-central1/publishers/google/models/gemini-4.0-preview"))
	global := (&evaltest.FakeEvaluationClient{}).PushError(
		autoraterPermissionDenied("projects/my-project/locations/global/publishers/google/models/gemini-4.0-preview"))
	eng := newRoutingEngine("us-central1", regional, global)

	tmpl := pointwiseTemplate()
	tmpl.AutoraterModel = "gemini-4.0-preview" // global-only but NOT in the prefix table

	_, err := eng.Run(context.Background(), tmpl, helpfulnessInstance())
	assertActionable(t, err, "my-project", "us-central1", "gemini-4.0-preview", "expect a delay and retry")

	if regional.Calls() != 1 {
		t.Errorf("regional client got %d calls, want 1 (one regional attempt before retry)", regional.Calls())
	}
	if global.Calls() != 1 {
		t.Errorf("global client got %d calls, want 1 (the NotFound-triggered retry)", global.Calls())
	}
}

// --- narrowness: non-autorater PermissionDenied keeps verbatim behavior -------

func TestRoute_NonAutoraterPermissionDenied_PassesThroughVerbatim(t *testing.T) {
	raw := "caller lacks aiplatform.locations.evaluateInstances on the project"
	regional := (&evaltest.FakeEvaluationClient{}).PushError(
		status.New(codes.PermissionDenied, raw).Err())
	global := &evaltest.FakeEvaluationClient{}
	eng := newRoutingEngine("us-central1", regional, global)

	_, err := eng.Run(context.Background(), pointwiseTemplate(), helpfulnessInstance())
	if err == nil {
		t.Fatal("expected the non-autorater PermissionDenied to surface")
	}
	msg := err.Error()
	if !strings.Contains(msg, raw) {
		t.Errorf("non-autorater PermissionDenied should pass through verbatim: %v", err)
	}
	if strings.Contains(msg, "roles/aiplatform.serviceAgent") {
		t.Errorf("non-autorater PermissionDenied must NOT get autorater-specific guidance: %v", err)
	}
	if global.Calls() != 0 {
		t.Errorf("global client got %d calls, want 0", global.Calls())
	}
}
