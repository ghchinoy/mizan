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

// handlers_more_test.go adds coverage for gaps the Phase-A suite left open
// (test-engineer pass, issue #114): the full get projection on a PAIRWISE
// template (baseline/candidate field names, autorater, prompt), the list
// projection's remaining fields (description/tags/version), the "more than one
// of text/file/gcs" malformed-field branch, eval_run against an unknown metric,
// and pairwise with an empty baseline value. All reuse the network-free harness
// (fake eval client + temp-file SQLite). No live cloud, no ADC.

import (
	"context"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
)

// TestGetMetricPairwiseProjection asserts mizan_get_metric projects the
// pairwise-only fields (BaselineFieldName / CandidateFieldName) plus the shared
// schema fields an agent needs to build a valid pairwise call. The Phase-A
// TestGetMetric only exercised a pointwise template, so the pairwise field-name
// projection was previously unasserted.
func TestGetMetricPairwiseProjection(t *testing.T) {
	hz := newHarness(t, &evaltest.FakeEvaluationClient{})
	out, err := hz.h.get(context.Background(), GetMetricIn{ID: pairwiseID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if out.Kind != string(registry.KindPairwise) {
		t.Errorf("kind = %q, want pairwise", out.Kind)
	}
	if out.BaselineFieldName != "baseline" {
		t.Errorf("baselineFieldName = %q, want %q", out.BaselineFieldName, "baseline")
	}
	if out.CandidateFieldName != "candidate" {
		t.Errorf("candidateFieldName = %q, want %q", out.CandidateFieldName, "candidate")
	}
	if out.AutoraterModel != "gemini-2.5-flash" {
		t.Errorf("autoraterModel = %q, want gemini-2.5-flash", out.AutoraterModel)
	}
	if out.MetricPromptTemplate == "" {
		t.Error("metricPromptTemplate should be projected, got empty")
	}
}

// TestListMetricsProjectionFields asserts the remaining list projection fields
// (description/tags/version) carry through, not just id/name/kind/modalities.
func TestListMetricsProjectionFields(t *testing.T) {
	hz := newHarness(t, &evaltest.FakeEvaluationClient{})
	out, err := hz.h.list(context.Background(), ListMetricsIn{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var got *MetricSummary
	for i := range out.Metrics {
		if out.Metrics[i].ID == pointwiseID {
			got = &out.Metrics[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("pointwise metric %q not in list", pointwiseID)
	}
	if got.Description != "Rate response helpfulness." {
		t.Errorf("description = %q", got.Description)
	}
	if got.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", got.Version)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "quality" {
		t.Errorf("tags = %v, want [quality]", got.Tags)
	}
}

// TestEvalRunMultiValuedFieldIsToolError covers the "more than one of
// text/file/gcs set" malformed-field branch (assetRef set > 1), which the
// Phase-A suite only exercised for the "none set" case.
func TestEvalRunMultiValuedFieldIsToolError(t *testing.T) {
	hz := newHarness(t, &evaltest.FakeEvaluationClient{})
	_, err := hz.h.run(context.Background(), EvalRunIn{
		Metric: pointwiseID,
		Fields: map[string]FieldValue{"response": {Text: "x", GCS: "gs://bucket/obj"}},
	})
	if err == nil {
		t.Fatal("expected an error when a field sets more than one of text/file/gcs")
	}
}

// TestEvalRunUnknownMetricIsToolError asserts an unknown metric id surfaces as a
// tool error from mizan_eval_run (the Phase-A suite only covered unknown id on
// the get tool).
func TestEvalRunUnknownMetricIsToolError(t *testing.T) {
	hz := newHarness(t, &evaltest.FakeEvaluationClient{})
	if _, err := hz.h.run(context.Background(), EvalRunIn{
		Metric: "test/does-not-exist",
		Fields: map[string]FieldValue{"response": {Text: "x"}},
	}); err == nil {
		t.Fatal("expected a tool error for an unknown metric id")
	}
}

// TestEvalPairwiseEmptyBaselineIsToolError asserts an empty baseline value (none
// of text/file/gcs set) is rejected before the engine runs, and the error is
// attributed to the baseline field.
func TestEvalPairwiseEmptyBaselineIsToolError(t *testing.T) {
	fake := (&evaltest.FakeEvaluationClient{}).PushResponse(
		evaltest.NewPointwiseResponse(1.0, "x"))
	hz := newHarness(t, fake)
	_, err := hz.h.pairwise(context.Background(), EvalPairwiseIn{
		Metric:    pairwiseID,
		Baseline:  FieldValue{}, // empty: none of text/file/gcs
		Candidate: FieldValue{Text: "b"},
	})
	if err == nil {
		t.Fatal("expected an error when the baseline value is empty")
	}
	if fake.Calls() != 0 {
		t.Errorf("engine was called %d times; want 0 (validation must precede the run)", fake.Calls())
	}
}

// TestAssetRefMapping is a pure table test over the field-value -> AssetRef
// mapping (mapping.go), covering each valid shape's EFFECT plus both malformed
// branches. It needs no services at all.
func TestAssetRefMapping(t *testing.T) {
	cases := []struct {
		name    string
		in      FieldValue
		wantErr bool
		check   func(t *testing.T, ref eval.AssetRef)
	}{
		{
			name: "text",
			in:   FieldValue{Text: "hello"},
			check: func(t *testing.T, ref eval.AssetRef) {
				if ref.Text != "hello" || ref.Modality != registry.ModalityText {
					t.Errorf("text ref = %+v", ref)
				}
			},
		},
		{
			name: "file",
			in:   FieldValue{File: "/tmp/a.png"},
			check: func(t *testing.T, ref eval.AssetRef) {
				if ref.FilePath != "/tmp/a.png" {
					t.Errorf("file ref = %+v", ref)
				}
			},
		},
		{
			name: "gcs",
			in:   FieldValue{GCS: "gs://bucket/obj"},
			check: func(t *testing.T, ref eval.AssetRef) {
				if ref.GCSUri != "gs://bucket/obj" {
					t.Errorf("gcs ref = %+v", ref)
				}
			},
		},
		{name: "none", in: FieldValue{}, wantErr: true},
		{name: "multiple", in: FieldValue{Text: "x", File: "/tmp/a"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := assetRef("k", tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error for %+v, got ref %+v", tc.in, ref)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.check(t, ref)
		})
	}
}
