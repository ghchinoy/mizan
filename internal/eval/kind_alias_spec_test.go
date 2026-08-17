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

// kind_alias_spec_test.go closes the ITEM-C gap the parse-boundary unit tests
// cannot reach: it proves the EMITTED Vertex request is byte-identical whether
// the caller spelled the kind vernacularly (single/compare) or canonically
// (pointwise/pairwise). Because the vernacular aliases are folded to a canonical
// registry.MetricKind by registry.NormalizeKind BEFORE the template reaches the
// engine, the engine must build the exact same PointwiseMetricSpec /
// PairwiseMetricSpec either way. These tests drive the real engine spec
// materialization (via the fakeClient that captures the outgoing request) and
// compare the two requests with proto.Equal — a regression guard that an alias
// spelling can never change what Vertex actually receives.

import (
	"context"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/registry"
)

// mustNormalize folds a user-supplied kind spelling to its canonical MetricKind,
// failing the test if the spelling is rejected. It models exactly what the CLI
// parse boundary does before a template ever reaches the engine.
func mustNormalize(t *testing.T, spelling string) registry.MetricKind {
	t.Helper()
	k, err := registry.NormalizeKind(spelling)
	if err != nil {
		t.Fatalf("NormalizeKind(%q): %v", spelling, err)
	}
	return k
}

// TestVertexSpecIdenticalSingleVsPointwise proves that a template authored with
// `--kind single` and one authored with `--kind pointwise` produce a
// byte-identical EvaluateInstancesRequest (PointwiseMetricSpec), so the Vertex
// wire request is unaffected by the vernacular spelling.
func TestVertexSpecIdenticalSingleVsPointwise(t *testing.T) {
	inst := Instance{Fields: map[string]AssetRef{
		"response": {Modality: registry.ModalityText, Text: "To reset your password, click the link."},
	}}

	run := func(kindSpelling string) *aiplatformpb.EvaluateInstancesRequest {
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
		tmpl := pointwiseTemplate()
		tmpl.Kind = mustNormalize(t, kindSpelling)
		if _, err := eng.Run(context.Background(), tmpl, inst); err != nil {
			t.Fatalf("Run(--kind %s): %v", kindSpelling, err)
		}
		if fc.gotReq == nil {
			t.Fatalf("Run(--kind %s): client received no request", kindSpelling)
		}
		return fc.gotReq
	}

	single := run("single")
	pointwise := run("pointwise")

	// The vernacular spelling must resolve to the pointwise wire shape.
	if single.GetPointwiseMetricInput() == nil {
		t.Fatalf("`--kind single` did not emit a PointwiseMetricInput (got %T)", single.GetMetricInputs())
	}
	if single.GetPairwiseMetricInput() != nil {
		t.Fatalf("`--kind single` unexpectedly emitted a PairwiseMetricInput")
	}
	// And it must be byte-identical to the canonical pointwise request.
	if !proto.Equal(single, pointwise) {
		t.Errorf("emitted Vertex request differs between `--kind single` and `--kind pointwise`:\n single=%v\n pointwise=%v",
			single, pointwise)
	}
}

// TestVertexSpecIdenticalCompareVsPairwise is the pairwise counterpart: `--kind
// compare` and `--kind pairwise` must yield a byte-identical PairwiseMetricSpec
// request.
func TestVertexSpecIdenticalCompareVsPairwise(t *testing.T) {
	run := func(kindSpelling string) *aiplatformpb.EvaluateInstancesRequest {
		fc := &fakeClient{resp: pairwiseResp(aiplatformpb.PairwiseChoice_CANDIDATE, "b")}
		eng := NewEngine(fc, "my-project", "us-central1")
		tmpl := pairwiseTemplate()
		tmpl.Kind = mustNormalize(t, kindSpelling)
		if _, err := eng.Run(context.Background(), tmpl, pairwiseInstance()); err != nil {
			t.Fatalf("Run(--kind %s): %v", kindSpelling, err)
		}
		if fc.gotReq == nil {
			t.Fatalf("Run(--kind %s): client received no request", kindSpelling)
		}
		return fc.gotReq
	}

	compare := run("compare")
	pairwise := run("pairwise")

	if compare.GetPairwiseMetricInput() == nil {
		t.Fatalf("`--kind compare` did not emit a PairwiseMetricInput (got %T)", compare.GetMetricInputs())
	}
	if compare.GetPointwiseMetricInput() != nil {
		t.Fatalf("`--kind compare` unexpectedly emitted a PointwiseMetricInput")
	}
	if !proto.Equal(compare, pairwise) {
		t.Errorf("emitted Vertex request differs between `--kind compare` and `--kind pairwise`:\n compare=%v\n pairwise=%v",
			compare, pairwise)
	}
}

// TestUnnormalizedAliasNeverReachesSpec is the negative guard: an alias spelling
// that was NOT normalized (i.e. leaked past the parse boundary onto
// MetricTemplate.Kind) must fail in the engine rather than silently building a
// pointwise/pairwise spec. This proves normalization is load-bearing — the
// engine dispatches only on canonical kinds.
func TestUnnormalizedAliasNeverReachesSpec(t *testing.T) {
	for _, leaked := range []registry.MetricKind{"single", "compare"} {
		fc := &fakeClient{}
		eng := NewEngine(fc, "p", "us-central1")
		tmpl := pointwiseTemplate()
		tmpl.Kind = leaked
		_, err := eng.Run(context.Background(), tmpl, Instance{Fields: map[string]AssetRef{
			"response": {Modality: registry.ModalityText, Text: "x"},
		}})
		if err == nil {
			t.Errorf("engine accepted un-normalized alias kind %q; want an unknown-kind error", leaked)
		}
		if fc.gotReq != nil {
			t.Errorf("engine emitted a Vertex request for un-normalized alias kind %q; none should be sent", leaked)
		}
	}
}
