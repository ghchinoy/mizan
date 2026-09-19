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
	if !checkAgreement(registry.KindBoul, eval.Result{Passed: &bTrue}, eval.Result{Passed: &bTrue}) {
		t.Error("boul both true should agree")
	}
	if checkAgreement(registry.KindBoul, eval.Result{Passed: &bTrue}, eval.Result{Passed: &bFalse}) {
		t.Error("boul true vs false should disagree")
	}

	// 2. Choice agreement
	if !checkAgreement(registry.KindChoice, eval.Result{ChoiceSelection: "billing"}, eval.Result{ChoiceSelection: "billing"}) {
		t.Error("choice same selection should agree")
	}
	if checkAgreement(registry.KindChoice, eval.Result{ChoiceSelection: "billing"}, eval.Result{ChoiceSelection: "technical"}) {
		t.Error("choice different selection should disagree")
	}

	// 3. Score agreement
	if !checkAgreement(registry.KindScore, eval.Result{Score: &scoreHigh}, eval.Result{Score: &scoreClose}) {
		t.Error("score 4.5 vs 4.0 should agree (within 1.0 delta)")
	}
	if checkAgreement(registry.KindScore, eval.Result{Score: &scoreHigh}, eval.Result{Score: &scoreLow}) {
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

func TestRenderBatchReportTable(t *testing.T) {
	bTrue := true
	rep := CompareBatchReport{
		TotalCases:       10,
		Agreements:       9,
		AgreementPct:     90.0,
		AvgSpeedupFactor: 7.8,
		EngineAAvgMs:     7800.0,
		EngineBAvgMs:     1000.0,
		TierBreakdown: map[string]TierReport{
			"unambiguous": {Total: 6, Agreements: 6, AgreementPct: 100.0},
			"ambiguous":   {Total: 4, Agreements: 3, AgreementPct: 75.0},
		},
		Cases: []CompareCaseResult{
			{
				ID:   "c-1",
				Tier: "ambiguous",
				Comparison: EngineCompareResult{
					Kind:      "boul",
					Agreement: false,
					EngineA:   EngineRun{Engine: "vertex", Passed: &bTrue},
					EngineB:   EngineRun{Engine: "diffusion"},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := renderBatchReportTable(&buf, rep, "vertex", "diffusion"); err != nil {
		t.Fatalf("renderBatchReportTable: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "Overall Agreement:") || !strings.Contains(out, "9 / 10 (90.0%)") {
		t.Errorf("missing overall agreement:\n%s", out)
	}
	if !strings.Contains(out, "Average Speedup Factor:") || !strings.Contains(out, "7.8x") {
		t.Errorf("missing speedup factor:\n%s", out)
	}
	if !strings.Contains(out, "DIVERGENT CASES (1 of 10):") {
		t.Errorf("missing divergent cases:\n%s", out)
	}
}
