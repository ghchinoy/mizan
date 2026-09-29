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

package main

import (
	"context"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
)

// TestScoreRunNativeKinds: computation/prebuilt runs are graded like score
// kinds (numeric within tolerance), except that a textual verdict label is
// compared against Passed and a pairwise_* preference against Selection.
func TestScoreRunNativeKinds(t *testing.T) {
	f := func(v float32) *float32 { return &v }
	b := func(v bool) *bool { return &v }
	cases := []struct {
		name     string
		kind     registry.MetricKind
		run      EngineRun
		expected string
		tol      float64
		want     *bool // nil = not scored
		wantPred string
	}{
		{"computation numeric exact", registry.KindComputation, EngineRun{Score: f(1)}, "1", 0, b(true), "1.00"},
		{"computation numeric miss", registry.KindComputation, EngineRun{Score: f(0)}, "1", 0, b(false), "0.00"},
		{"computation numeric within tol", registry.KindComputation, EngineRun{Score: f(0.42)}, "0.4", 0.05, b(true), "0.42"},
		// "1"/"0" stay numeric even when Passed is set.
		{"computation 0 is numeric", registry.KindComputation, EngineRun{Score: f(0), Passed: b(false)}, "0", 0, b(true), "0.00"},
		{"computation PASS vs Passed", registry.KindComputation, EngineRun{Score: f(0.42), Passed: b(true)}, "PASS", 0, b(true), "0.42"},
		{"computation false vs Passed", registry.KindComputation, EngineRun{Score: f(0.42), Passed: b(true)}, "false", 0, b(false), "0.42"},
		{"computation verdict without threshold", registry.KindComputation, EngineRun{Score: f(0.42)}, "PASS", 0, nil, "0.42"},
		{"prebuilt likert", registry.KindPrebuilt, EngineRun{Score: f(4.2)}, "4", 0.5, b(true), "4.20"},
		{"prebuilt FAIL vs Passed", registry.KindPrebuilt, EngineRun{Score: f(2), Passed: b(false)}, "FAIL", 0.5, b(true), "2.00"},
		{"prebuilt pairwise", registry.KindPrebuilt, EngineRun{Selection: "CANDIDATE"}, "B", 0, b(true), "CANDIDATE"},
		{"prebuilt pairwise miss", registry.KindPrebuilt, EngineRun{Selection: "TIE"}, "BASELINE", 0, b(false), "TIE"},
		{"errored run not scored", registry.KindComputation, EngineRun{Error: "boom"}, "1", 0, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := c.run
			scoreRun(&r, c.kind, c.expected, c.tol)
			if r.Prediction != c.wantPred {
				t.Errorf("Prediction = %q, want %q", r.Prediction, c.wantPred)
			}
			switch {
			case c.want == nil && r.Correct != nil:
				t.Errorf("Correct = %v, want unscored", *r.Correct)
			case c.want != nil && r.Correct == nil:
				t.Errorf("Correct = nil, want %v", *c.want)
			case c.want != nil && *r.Correct != *c.want:
				t.Errorf("Correct = %v, want %v", *r.Correct, *c.want)
			}
		})
	}
}

// TestCheckAgreementNativeKinds covers score and preference agreement.
func TestCheckAgreementNativeKinds(t *testing.T) {
	f := func(v float32) *float32 { return &v }
	if !checkAgreement(registry.KindComputation, eval.Result{Score: f(0.42)}, eval.Result{Score: f(0.42)}, 0) {
		t.Error("equal computation scores should agree")
	}
	if checkAgreement(registry.KindPrebuilt, eval.Result{Score: f(5)}, eval.Result{Score: f(3)}, 0.5) {
		t.Error("prebuilt 5 vs 3 should not agree at tol 0.5")
	}
	if !checkAgreement(registry.KindPrebuilt, eval.Result{PairwiseChoice: "CANDIDATE"}, eval.Result{PairwiseChoice: "CANDIDATE"}, 0) {
		t.Error("same pairwise preference should agree")
	}
	if checkAgreement(registry.KindPrebuilt, eval.Result{PairwiseChoice: "CANDIDATE"}, eval.Result{PairwiseChoice: "TIE"}, 0) {
		t.Error("different pairwise preferences should not agree")
	}
}

// TestCompareLocalEngineComputation runs a local-vs-local comparison of a
// computation metric through executeSingleComparison on a CLIENT-FREE engine:
// the local engine needs no credential, and runOne adds no diffusion/rubric
// options for it (a diffusion knob would not break local, but a stray model
// engine option would signal the wrong wiring).
func TestCompareLocalEngineComputation(t *testing.T) {
	eng := eval.NewEngine(nil, "proj", "us-central1")
	tmpl := &registry.MetricTemplate{
		ID:   "computation/exact",
		Kind: registry.KindComputation,
		Inputs: []registry.InputSpec{
			{Name: "response", Modality: registry.ModalityText, Required: true},
			{Name: "reference", Modality: registry.ModalityText, Required: true},
		},
		Native: &registry.NativeMetricSpec{Metric: "exact_match"},
	}
	inst := eval.Instance{Fields: map[string]eval.AssetRef{
		"response":  {Modality: registry.ModalityText, Text: "Paris"},
		"reference": {Modality: registry.ModalityText, Text: "Paris"},
	}}
	o := compareOptions{engineA: "local", engineB: "LOCAL", serial: true, samples: 4, mirror: true}
	cmp := executeSingleComparison(context.Background(), eng, tmpl, inst, o)
	if !cmp.EngineA.ok() || !cmp.EngineB.ok() {
		t.Fatalf("errors: A=%q B=%q", cmp.EngineA.Error, cmp.EngineB.Error)
	}
	if !cmp.Agreement || cmp.EngineA.Score == nil || *cmp.EngineA.Score != 1 {
		t.Errorf("agreement=%v scoreA=%v, want true/1", cmp.Agreement, cmp.EngineA.Score)
	}
	if cmp.EngineA.Custom["engine"] != "local" {
		t.Errorf("engine = %v, want local", cmp.EngineA.Custom["engine"])
	}

	// Diffusion on a computation metric is a clear per-engine error.
	o.engineB = "diffusion"
	cmp = executeSingleComparison(context.Background(), eng, tmpl, inst, o)
	if cmp.EngineB.ok() || cmp.EngineA.Error != "" {
		t.Errorf("A err=%q B err=%q, want only B to fail", cmp.EngineA.Error, cmp.EngineB.Error)
	}
}
