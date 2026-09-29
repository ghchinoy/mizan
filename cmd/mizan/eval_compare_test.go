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
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
)

func TestCheckAgreement(t *testing.T) {
	bTrue := true
	bFalse := false
	scoreHigh := float32(4.5)
	scoreClose := float32(4.0)
	scoreLow := float32(1.0)

	// 1. Boul agreement
	if !checkAgreement(registry.KindBoul, eval.Result{Passed: &bTrue}, eval.Result{Passed: &bTrue}, 1) {
		t.Error("boul both true should agree")
	}
	if checkAgreement(registry.KindBoul, eval.Result{Passed: &bTrue}, eval.Result{Passed: &bFalse}, 1) {
		t.Error("boul true vs false should disagree")
	}

	// 2. Choice agreement
	if !checkAgreement(registry.KindChoice, eval.Result{ChoiceSelection: "billing"}, eval.Result{ChoiceSelection: "billing"}, 1) {
		t.Error("choice same selection should agree")
	}
	if checkAgreement(registry.KindChoice, eval.Result{ChoiceSelection: "billing"}, eval.Result{ChoiceSelection: "technical"}, 1) {
		t.Error("choice different selection should disagree")
	}

	// 3. Score agreement
	if !checkAgreement(registry.KindScore, eval.Result{Score: &scoreHigh}, eval.Result{Score: &scoreClose}, 1) {
		t.Error("score 4.5 vs 4.0 should agree (within 1.0 delta)")
	}
	if checkAgreement(registry.KindScore, eval.Result{Score: &scoreHigh}, eval.Result{Score: &scoreLow}, 1) {
		t.Error("score 4.5 vs 1.0 should disagree (outside 1.0 delta)")
	}
}

func TestRenderCompareTable(t *testing.T) {
	bTrue := true
	confA := float32(0.95)
	confB := float32(0.98)

	cmp := EngineCompareResult{
		MetricID:      "safety/pii-check",
		Kind:          "boul",
		Agreement:     true,
		SpeedupFactor: 8.2,
		EngineA: EngineRun{
			Engine:      "vertex",
			Model:       "gemini-2.5-flash",
			Passed:      &bTrue,
			Confidence:  &confA,
			Explanation: "Vertex found no PII.",
			DurationMs:  7240.0,
		},
		EngineB: EngineRun{
			Engine:      "diffusion",
			Model:       "diffgemma-26b-a4b-it-q4",
			Passed:      &bTrue,
			Confidence:  &confB,
			Explanation: "DiffusionGemma slot verdict: yes",
			DurationMs:  882.0,
			Custom: map[string]any{
				"stderr": 0.0042,
			},
		},
	}

	var buf bytes.Buffer
	if err := renderCompareTable(&buf, cmp); err != nil {
		t.Fatalf("renderCompareTable: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "Metric:") || !strings.Contains(out, "safety/pii-check") {
		t.Errorf("missing metric id in output:\n%s", out)
	}
	if !strings.Contains(out, "Verdict Agreement:") || !strings.Contains(out, "AGREEMENT (Match)") {
		t.Errorf("missing agreement status in output:\n%s", out)
	}
	if !strings.Contains(out, "Speedup Factor:") || !strings.Contains(out, "8.2x") {
		t.Errorf("missing speedup factor in output:\n%s", out)
	}
	if !strings.Contains(out, "Passed:") || !strings.Contains(out, "PASS") {
		t.Errorf("missing PASS row in output:\n%s", out)
	}
	if !strings.Contains(out, "stderr: ±0.0042") {
		t.Errorf("missing stderr in output:\n%s", out)
	}
}

func TestCompareEnginesOutputJSON(t *testing.T) {
	bTrue := true
	cmp := EngineCompareResult{
		MetricID:      "safety/pii-check",
		Kind:          "boul",
		Agreement:     true,
		SpeedupFactor: 6.5,
		EngineA: EngineRun{
			Engine:     "vertex",
			Passed:     &bTrue,
			DurationMs: 6500.0,
		},
		EngineB: EngineRun{
			Engine:     "diffusion",
			Passed:     &bTrue,
			DurationMs: 1000.0,
		},
	}

	var buf bytes.Buffer
	prev := outputFormat
	outputFormat = outputJSON
	defer func() { outputFormat = prev }()

	if err := printJSON(&buf, cmp); err != nil {
		t.Fatalf("printJSON: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `"speedup_factor": 6.5`) || !strings.Contains(out, `"agreement": true`) {
		t.Errorf("json missing expected fields:\n%s", out)
	}
}

func TestScoreRunAgainstGold(t *testing.T) {
	bFalse := false
	sc := float32(3.4)
	cases := []struct {
		kind     registry.MetricKind
		run      EngineRun
		expected string
		want     *bool
	}{
		{registry.KindBoul, EngineRun{Passed: &bFalse}, "FAIL", ptrBool(true)},
		{registry.KindBoul, EngineRun{Passed: &bFalse}, "true", ptrBool(false)},
		{registry.KindChoice, EngineRun{Selection: "Billing"}, "billing", ptrBool(true)},
		{registry.KindPairwise, EngineRun{Selection: "CANDIDATE"}, "B", ptrBool(true)},
		{registry.KindScore, EngineRun{Score: &sc}, "3", ptrBool(true)},
		{registry.KindScore, EngineRun{Score: &sc}, "4", ptrBool(false)},
		{registry.KindChoice, EngineRun{Selection: "x"}, "", nil},
		{registry.KindChoice, EngineRun{Selection: "x", Error: "boom"}, "x", nil},
	}
	for i, c := range cases {
		r := c.run
		scoreRun(&r, c.kind, c.expected, 0.5)
		if (r.Correct == nil) != (c.want == nil) || (r.Correct != nil && *r.Correct != *c.want) {
			t.Errorf("case %d: Correct = %v, want %v", i, r.Correct, c.want)
		}
	}
}

func ptrBool(b bool) *bool { return &b }

func TestBuildBatchReportAccuracyFirst(t *testing.T) {
	yes, no := true, false
	mk := func(id string, aOK, bOK bool, agree bool) CompareCaseResult {
		return CompareCaseResult{ID: id, Tier: "t", Expected: "PASS", Comparison: EngineCompareResult{
			Kind: "boul", Agreement: agree,
			EngineA: EngineRun{Engine: "vertex", Passed: &yes, Prediction: "PASS", Correct: ptrBool(aOK), DurationMs: 900},
			EngineB: EngineRun{Engine: "diffusion", Passed: &no, Prediction: "FAIL", Correct: ptrBool(bOK), DurationMs: 150,
				Custom: map[string]any{"readout_mode": "envelope", "backend_used": "vertex", "server_ms": 60.0}},
		}}
	}
	cases := []CompareCaseResult{mk("1", true, true, true), mk("2", true, false, false), mk("3", false, false, true), mk("4", true, false, false)}
	cases = append(cases, CompareCaseResult{ID: "5", Comparison: EngineCompareResult{Kind: "boul",
		EngineA: EngineRun{Engine: "vertex", Error: "429"}, EngineB: EngineRun{Engine: "diffusion", Error: "429"}}})
	rep := buildBatchReport(CompareRunMeta{}, cases, compareOptions{engineA: "vertex", engineB: "diffusion", bootstrapIters: 200, bootstrapSeed: 1})

	if rep.EngineA.Correct != 3 || rep.EngineA.Scored != 4 || rep.EngineA.Errors != 1 {
		t.Errorf("engine A = %+v", rep.EngineA)
	}
	if rep.EngineB.Correct != 1 || rep.EngineB.ReadoutModes["envelope"] != 4 || rep.EngineB.BackendsUsed["vertex"] != 4 {
		t.Errorf("engine B = %+v", rep.EngineB)
	}
	p := rep.Paired
	if p.N != 4 || p.BothCorrect != 1 || p.OnlyACorrect != 2 || p.OnlyBCorrect != 0 || p.BothWrong != 1 {
		t.Errorf("paired = %+v", p)
	}
	if p.McNemarP == nil || *p.McNemarP != 0.5 {
		t.Errorf("McNemar p = %v, want 0.5", p.McNemarP)
	}
	if p.MedianSpeedup == nil || *p.MedianSpeedup != 6 {
		t.Errorf("median ratio = %v, want 6", p.MedianSpeedup)
	}

	var buf bytes.Buffer
	if err := renderBatchReportTable(&buf, rep); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Accuracy vs gold:", "3/4 (75.0%)", "1/4 (25.0%)", "McNemar exact p=0.500", "MISSES / ERRORS"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestStats(t *testing.T) {
	if k := cohenKappa([]string{"a", "a", "b", "b"}, []string{"a", "a", "b", "b"}); k != 1 {
		t.Errorf("kappa = %v, want 1", k)
	}
	if r := spearman([]float64{1, 2, 3, 4}, []float64{10, 20, 30, 40}); math.Abs(r-1) > 1e-12 {
		t.Errorf("spearman = %v", r)
	}
	if p := mcnemarExact(0, 6); math.Abs(p-0.03125) > 1e-9 {
		t.Errorf("mcnemar(0,6) = %v, want 0.03125", p)
	}
	ci := bootstrapMeanCI([]float64{1, 1, 1, 0}, 500, 7)
	if ci.Lo > 0.75 || ci.Hi < 0.75 {
		t.Errorf("ci = %+v", ci)
	}
	if e := ece10([]float64{0.95, 0.95}, []bool{true, true}); math.Abs(e-0.05) > 1e-9 {
		t.Errorf("ece = %v", e)
	}
}

func TestEndpointKind(t *testing.T) {
	for in, want := range map[string]string{
		"http://127.0.0.1:8080/v1": "local",
		"https://1.us-central1-2.prediction.vertexai.goog/v1/projects/p/locations/l/endpoints/1": "vertex-dedicated",
		"https://dgemma-gateway-abc-uc.a.run.app/v1":                                             "gateway",
		"https://dgemma-abc-uc.a.run.app/v1":                                                     "cloudrun",
	} {
		if got := endpointKind(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestDatasetExpectedNormalization(t *testing.T) {
	var it CompareDatasetItem
	for raw, want := range map[string]string{`{"metric":"m","expected":3}`: "3", `{"metric":"m","expected":true}`: "true", `{"metric":"m","expected":" PASS "}`: "PASS", `{"metric":"m"}`: ""} {
		if err := json.Unmarshal([]byte(raw), &it); err != nil {
			t.Fatal(err)
		}
		if it.Expected != want {
			t.Errorf("%s -> %q, want %q", raw, it.Expected, want)
		}
	}
}
