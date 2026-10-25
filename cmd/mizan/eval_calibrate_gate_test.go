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
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval"
)

func TestCalibrateGateCLIValidation(t *testing.T) {
	t.Cleanup(func() { outputFormat = outputTable })

	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing_input",
			args:    []string{"eval", "calibrate-gate"},
			wantErr: "--input is required",
		},
		{
			name:    "invalid_method",
			args:    []string{"eval", "calibrate-gate", "--input", "foo.json", "--method", "bogus"},
			wantErr: "invalid --method",
		},
		{
			name:    "invalid_folds",
			args:    []string{"eval", "calibrate-gate", "--input", "foo.json", "--folds", "0"},
			wantErr: "--folds must be >= 1",
		},
		{
			name:    "invalid_target_risk_high",
			args:    []string{"eval", "calibrate-gate", "--input", "foo.json", "--target-risk", "1.0"},
			wantErr: "--target-risk must be in [0, 1)",
		},
		{
			name:    "invalid_target_risk_negative",
			args:    []string{"eval", "calibrate-gate", "--input", "foo.json", "--target-risk", "-0.1"},
			wantErr: "--target-risk must be in [0, 1)",
		},
		{
			name:    "invalid_mcnemar_alpha",
			args:    []string{"eval", "calibrate-gate", "--input", "foo.json", "--mcnemar-alpha", "1.2"},
			wantErr: "--mcnemar-alpha must be in [0, 1)",
		},
		{
			name:    "both_constraints_disabled",
			args:    []string{"eval", "calibrate-gate", "--input", "foo.json", "--target-risk", "0", "--mcnemar-alpha", "0"},
			wantErr: "at least one of --target-risk or --mcnemar-alpha must be greater than 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := executeRoot(t, tc.args...)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil (out=%q)", tc.wantErr, out)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestCalibrateGateRealToxicityAndInstructionFollowing(t *testing.T) {
	t.Cleanup(func() { outputFormat = outputTable })

	toxPath := filepath.Join("..", "..", "docs", "experiments", "judge-eval", "runs", "20260926", "j4_vs_j1_toxicity_prompt.json")
	out, err := executeRoot(t,
		"--output", "json",
		"eval", "calibrate-gate",
		"--input", toxPath,
		"--mcnemar-alpha", "0.10",
		"--bootstrap", "200",
	)
	if err != nil {
		t.Fatalf("calibrate-gate toxicity_prompt failed: %v\nout=%s", err, out)
	}
	var toxRep CalibrateGateReport
	if err := json.Unmarshal([]byte(out), &toxRep); err != nil {
		t.Fatalf("unmarshal toxicity report: %v\nout=%s", err, out)
	}
	if toxRep.PairedScoredItems != 76 {
		t.Fatalf("PairedScoredItems = %d, want 76", toxRep.PairedScoredItems)
	}
	if toxRep.Baseline.ParityAchieved || toxRep.Baseline.McNemarP >= 0.10 {
		t.Fatalf("expected uncalibrated toxicity run to fail parity (p < 0.10), got p=%.4f", toxRep.Baseline.McNemarP)
	}
	if !toxRep.Recommended.FullSample.Feasible {
		t.Fatalf("expected feasible full-sample threshold, got %+v", toxRep.Recommended.FullSample)
	}
	if toxRep.Recommended.FullSample.McNemarP <= 0.10 {
		t.Fatalf("expected calibrated McNemar p > 0.10, got %.4f", toxRep.Recommended.FullSample.McNemarP)
	}
	if toxRep.Recommended.FullSample.EscalationCount != 3 {
		t.Fatalf("EscalationCount = %d, want 3 (3.95%% escalation)", toxRep.Recommended.FullSample.EscalationCount)
	}
	if !strings.HasPrefix(toxRep.RecommendedCLIFlag, "--cascade-threshold ") {
		t.Fatalf("RecommendedCLIFlag = %q, want --cascade-threshold prefix", toxRep.RecommendedCLIFlag)
	}

	outRisk, err := executeRoot(t,
		"--output", "json",
		"eval", "calibrate-gate",
		"--input", toxPath,
		"--target-risk", "0.15",
		"--mcnemar-alpha", "0.10",
		"--bootstrap", "0",
	)
	if err != nil {
		t.Fatalf("calibrate-gate toxicity_prompt with --target-risk 0.15 failed: %v", err)
	}
	var toxRiskRep CalibrateGateReport
	if err := json.Unmarshal([]byte(outRisk), &toxRiskRep); err != nil {
		t.Fatalf("unmarshal toxicity risk report: %v", err)
	}
	if !toxRiskRep.Recommended.FullSample.Feasible || toxRiskRep.Recommended.FullSample.SelectiveRisk > 0.15+1e-9 {
		t.Fatalf("expected selective risk <= 0.15, got %+v", toxRiskRep.Recommended.FullSample)
	}

	pairPath := filepath.Join("..", "..", "docs", "experiments", "judge-eval", "runs", "20260926", "j4_vs_j1_pairwise_instruction_following.json")
	outPair, err := executeRoot(t,
		"--output", "json",
		"eval", "calibrate-gate",
		"--input", pairPath,
		"--target-risk", "0.10",
		"--mcnemar-alpha", "0.10",
		"--bootstrap", "200",
	)
	if err != nil {
		t.Fatalf("calibrate-gate pairwise_instruction_following failed: %v", err)
	}
	var pairRep CalibrateGateReport
	if err := json.Unmarshal([]byte(outPair), &pairRep); err != nil {
		t.Fatalf("unmarshal pairwise report: %v", err)
	}
	if pairRep.PairedScoredItems != 84 {
		t.Fatalf("PairedScoredItems = %d, want 84", pairRep.PairedScoredItems)
	}
	if pairRep.Baseline.McNemarP >= 0.01 {
		t.Fatalf("expected uncalibrated pairwise McNemar p < 0.01, got %.4f", pairRep.Baseline.McNemarP)
	}
	if !pairRep.Recommended.FullSample.Feasible {
		t.Fatalf("expected feasible pairwise threshold, got %+v", pairRep.Recommended.FullSample)
	}
	if pairRep.Recommended.FullSample.SelectiveRisk > 0.10+1e-9 {
		t.Fatalf("expected selective risk <= 0.10, got %.4f", pairRep.Recommended.FullSample.SelectiveRisk)
	}
	if pairRep.Recommended.FullSample.McNemarP <= 0.10 {
		t.Fatalf("expected calibrated McNemar p > 0.10, got %.4f", pairRep.Recommended.FullSample.McNemarP)
	}
	if pairRep.Recommended.FullSample.EscalationRate >= 0.35 {
		t.Fatalf("expected escalation rate < 0.35, got %.4f", pairRep.Recommended.FullSample.EscalationRate)
	}
}

func TestCalibrateGateAlreadyAtParityZeroEscalation(t *testing.T) {
	t.Cleanup(func() { outputFormat = outputTable })

	groundPath := filepath.Join("..", "..", "docs", "experiments", "judge-eval", "runs", "20260926", "j4_vs_j1_groundedness.json")
	out, err := executeRoot(t,
		"--output", "json",
		"eval", "calibrate-gate",
		"--input", groundPath,
		"--bootstrap", "0",
	)
	if err != nil {
		t.Fatalf("calibrate-gate groundedness failed: %v", err)
	}
	var rep CalibrateGateReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("unmarshal groundedness report: %v", err)
	}
	if !rep.Baseline.ParityAchieved {
		t.Fatalf("expected groundedness baseline parity achieved, got p=%.4f", rep.Baseline.McNemarP)
	}
	if rep.Recommended.FullSample.EscalationCount != 0 || rep.Recommended.FullSample.EscalationRate != 0 {
		t.Fatalf("expected 0 escalations when baseline already satisfies parity, got count=%d rate=%.4f",
			rep.Recommended.FullSample.EscalationCount, rep.Recommended.FullSample.EscalationRate)
	}
}

func TestCalibrateGateLegacyV1WithCalibrationSuiteAndBothMethods(t *testing.T) {
	t.Cleanup(func() { outputFormat = outputTable })

	v1Path := filepath.Join("..", "..", "docs", "experiments", "benchmark_results_gce.json")
	v1OrigPath := filepath.Join("..", "..", "docs", "experiments", "benchmark_results.json")
	suitePath := filepath.Join("..", "..", "docs", "experiments", "calibration_suite.jsonl")

	args := []string{
		"--output", "json",
		"eval", "calibrate-gate",
		"--input", v1Path,
		"--input", v1OrigPath,
		"--suite", suitePath,
		"--method", "both",
		"--target-risk", "0.20",
		"--mcnemar-alpha", "0.10",
		"--folds", "4",
		"--seed", "42",
		"--bootstrap", "100",
	}
	out1, err := executeRoot(t, args...)
	if err != nil {
		t.Fatalf("calibrate-gate legacy v1 + suite failed: %v\nout=%s", err, out1)
	}
	out2, err := executeRoot(t, args...)
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if out1 != out2 {
		t.Fatalf("expected byte-identical deterministic output across repeated seeded runs")
	}

	var rep CalibrateGateReport
	if err := json.Unmarshal([]byte(out1), &rep); err != nil {
		t.Fatalf("unmarshal v1 calibration report: %v", err)
	}
	if rep.PairedScoredItems == 0 {
		t.Fatalf("expected paired scored items > 0 after joining calibration_suite.jsonl")
	}
	if len(rep.Alternatives) != 1 {
		t.Fatalf("expected 1 alternative method in --method both mode, got %d", len(rep.Alternatives))
	}
	var sawConformal bool
	for _, m := range append([]GateCalibrationResult{rep.Recommended}, rep.Alternatives...) {
		if m.Method == gateMethodConformal {
			sawConformal = true
			if m.ConformalQuantile == nil || math.IsNaN(*m.ConformalQuantile) {
				t.Fatalf("expected finite conformal_quantile when --target-risk > 0, got %v", m.ConformalQuantile)
			}
		}
	}
	if !sawConformal {
		t.Fatalf("expected conformal result in report")
	}
}

func TestCalibrateGateJSONLInputTableAndOutputFile(t *testing.T) {
	t.Cleanup(func() { outputFormat = outputTable })

	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "eval_log.jsonl")
	outFilePath := filepath.Join(dir, "calibrated_gate.json")

	lines := []string{
		`{"id":"item-1","baseline_correct":true,"fast_correct":true,"probabilities":{"PASS":0.97,"FAIL":0.03}}`,
		`{"id":"item-2","baseline_correct":true,"fast_correct":true,"probabilities":{"PASS":0.95,"FAIL":0.05}}`,
		`{"id":"item-3","baseline_correct":true,"fast_correct":true,"probabilities":{"PASS":0.92,"FAIL":0.08}}`,
		`{"id":"item-4","baseline_correct":true,"fast_correct":false,"probabilities":{"PASS":0.52,"FAIL":0.48}}`,
		`{"id":"item-5","baseline_correct":true,"fast_correct":false,"probabilities":{"PASS":0.51,"FAIL":0.49}}`,
		`{"id":"item-6","baseline_correct":true,"fast_correct":false,"probabilities":{"PASS":0.50,"FAIL":0.50}}`,
	}
	if err := os.WriteFile(jsonlPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write jsonl fixture: %v", err)
	}

	tableOut, err := executeRoot(t,
		"--output", "table",
		"eval", "calibrate-gate",
		"--input", jsonlPath,
		"--target-risk", "0.10",
		"--mcnemar-alpha", "0.10",
		"--folds", "3",
		"--bootstrap", "50",
		"--output-file", outFilePath,
	)
	if err != nil {
		t.Fatalf("calibrate-gate jsonl + table failed: %v\nout=%s", err, tableOut)
	}
	for _, want := range []string{"CALIBRATE GATE SUMMARY", "RECOMMENDED GATE", "PARETO SWEEP", "--cascade-threshold"} {
		if !strings.Contains(tableOut, want) {
			t.Errorf("table output missing %q:\n%s", want, tableOut)
		}
	}

	written, err := os.ReadFile(outFilePath)
	if err != nil {
		t.Fatalf("read --output-file: %v", err)
	}
	var rep CalibrateGateReport
	if err := json.Unmarshal(written, &rep); err != nil {
		t.Fatalf("unmarshal written JSON report: %v", err)
	}
	if rep.PairedScoredItems != 6 {
		t.Fatalf("PairedScoredItems = %d, want 6", rep.PairedScoredItems)
	}
	if rep.Recommended.FullSample.EscalationCount != 3 || rep.Recommended.FullSample.SelectiveRisk != 0 {
		t.Fatalf("expected 3 high-entropy errors escalated with 0 selective risk, got %+v", rep.Recommended.FullSample)
	}
}

func TestCalibrateGateGCEScientificThresholdFormattingAndCascadeFidelity(t *testing.T) {
	t.Cleanup(func() { outputFormat = outputTable })

	gcePath := filepath.Join("..", "..", "docs", "experiments", "benchmark_results_gce.json")
	jsonOut, err := executeRoot(t,
		"--output", "json",
		"eval", "calibrate-gate",
		"--input", gcePath,
		"--target-risk", "0.10",
		"--bootstrap", "0",
	)
	if err != nil {
		t.Fatalf("calibrate-gate gce json failed: %v\nout=%s", err, jsonOut)
	}
	var rep CalibrateGateReport
	if err := json.Unmarshal([]byte(jsonOut), &rep); err != nil {
		t.Fatalf("unmarshal gce report: %v", err)
	}
	if !strings.HasPrefix(rep.RecommendedCLIFlag, "--cascade-threshold ") {
		t.Fatalf("RecommendedCLIFlag = %q, want --cascade-threshold prefix", rep.RecommendedCLIFlag)
	}
	if rep.RecommendedCLIFlag == "--cascade-threshold 0.0000" {
		t.Fatalf("RecommendedCLIFlag truncated positive sub-1e-4 threshold to 0.0000")
	}
	cliTau, err := strconv.ParseFloat(strings.TrimPrefix(rep.RecommendedCLIFlag, "--cascade-threshold "), 64)
	if err != nil || cliTau <= 0 {
		t.Fatalf("invalid parsed CLI threshold from %q: tau=%v err=%v", rep.RecommendedCLIFlag, cliTau, err)
	}

	gceItems, _, _, _, err := loadCalibrationFile(gcePath, nil, 0.5, 0)
	if err != nil {
		t.Fatalf("loadCalibrationFile gce: %v", err)
	}
	escalatedCal := 0
	for _, it := range gceItems {
		if it.hesitation >= cliTau {
			escalatedCal++
		}
	}
	if escalatedCal != rep.Recommended.FullSample.EscalationCount {
		t.Fatalf("calibrated hesitation escalation with %q = %d/%d, want %d/%d",
			rep.RecommendedCLIFlag, escalatedCal, len(gceItems),
			rep.Recommended.FullSample.EscalationCount, rep.PairedScoredItems)
	}

	rawBytes, err := os.ReadFile(gcePath)
	if err != nil {
		t.Fatalf("read gce report: %v", err)
	}
	var batch CompareBatchReport
	if err := json.Unmarshal(rawBytes, &batch); err != nil {
		t.Fatalf("unmarshal gce CompareBatchReport: %v", err)
	}
	probCases := 0
	probEscalated := 0
	for _, c := range batch.Cases {
		if c.Comparison.EngineB.Custom["probabilities"] == nil {
			continue // legacy v1 rubric img-03 predates per_criterion["entropy"]
		}
		probCases++
		h, ok := hesitationOf(eval.Result{CustomOutput: c.Comparison.EngineB.Custom})
		wantEsc := computeHesitationSignal(c.Comparison.EngineB) >= cliTau
		gotEsc := !ok || h >= cliTau
		if gotEsc != wantEsc {
			t.Fatalf("case %s: hesitationOf escalation=%v (h=%.10g) != calibrated=%v at %q",
				c.ID, gotEsc, h, wantEsc, rep.RecommendedCLIFlag)
		}
		if gotEsc {
			probEscalated++
		}
	}
	if probCases != 31 || probEscalated != 18 {
		t.Fatalf("probability-backed GCE cases escalated = %d/%d, want 18/31 (not 31/31)", probEscalated, probCases)
	}

	tableOut, err := executeRoot(t,
		"--output", "table",
		"eval", "calibrate-gate",
		"--input", gcePath,
		"--target-risk", "0.10",
		"--bootstrap", "0",
	)
	if err != nil {
		t.Fatalf("calibrate-gate gce table failed: %v", err)
	}
	zeroRows := 0
	inSweep := false
	for _, line := range strings.Split(tableOut, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "THRESHOLD") {
			inSweep = true
			continue
		}
		if inSweep && trimmed == "" {
			break
		}
		if inSweep && strings.HasPrefix(trimmed, "0.0000") {
			zeroRows++
		}
	}
	if zeroRows > 1 {
		t.Fatalf("expected at most 1 true-zero sweep row labeled 0.0000, got %d:\n%s", zeroRows, tableOut)
	}

	// Synthetic 50-item sub-1e-4 round-trip: 13 uncertain items with hesitation ~1.77e-06..5.0e-05
	// and 37 confident correct items with hesitation < 5.0e-07 -> exact 13/50 (26.0%) escalation.
	dir := t.TempDir()
	sub50Path := filepath.Join(dir, "sub50.jsonl")
	var lines []string
	for i := 0; i < 50; i++ {
		if i < 13 {
			// p_no = 7.48e-8 + i*4.0e-8 produces normalized binary entropy ~1.77e-06..1.25e-05.
			pFail := 7.48e-8 + float64(i)*4.0e-8
			fastCorr := i >= 6 // 6 errors among lowest-uncertainty uncertain items
			lines = append(lines, fmt.Sprintf(
				`{"id":"u-%02d","baseline_correct":true,"fast_correct":%v,"probabilities":{"PASS":%.12f,"FAIL":%.12f}}`,
				i, fastCorr, 1.0-pFail, pFail,
			))
		} else {
			pFail := 1.0e-8
			lines = append(lines, fmt.Sprintf(
				`{"id":"k-%02d","baseline_correct":true,"fast_correct":true,"probabilities":{"PASS":%.12f,"FAIL":%.12f}}`,
				i, 1.0-pFail, pFail,
			))
		}
	}
	if err := os.WriteFile(sub50Path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write sub50 fixture: %v", err)
	}
	subOut, err := executeRoot(t,
		"--output", "json",
		"eval", "calibrate-gate",
		"--input", sub50Path,
		"--target-risk", "0.02",
		"--bootstrap", "0",
	)
	if err != nil {
		t.Fatalf("calibrate-gate sub50 failed: %v\nout=%s", err, subOut)
	}
	var subRep CalibrateGateReport
	if err := json.Unmarshal([]byte(subOut), &subRep); err != nil {
		t.Fatalf("unmarshal sub50: %v", err)
	}
	subTau, err := strconv.ParseFloat(strings.TrimPrefix(subRep.RecommendedCLIFlag, "--cascade-threshold "), 64)
	if err != nil || subTau <= 0 {
		t.Fatalf("bad sub50 CLI threshold %q: %v", subRep.RecommendedCLIFlag, err)
	}
	if subRep.Recommended.FullSample.EscalationCount != 13 {
		t.Fatalf("sub50 EscalationCount = %d, want 13 (26.0%%)", subRep.Recommended.FullSample.EscalationCount)
	}
	loadedItems, _, _, _, err := loadCalibrationFile(sub50Path, nil, 0.5, 0)
	if err != nil {
		t.Fatalf("loadCalibrationFile sub50: %v", err)
	}
	roundTripEsc := 0
	for _, it := range loadedItems {
		if it.hesitation >= subTau {
			roundTripEsc++
		}
	}
	if roundTripEsc != 13 {
		t.Fatalf("sub50 round-trip escalation = %d/50, want 13/50 (CLI flag %q)", roundTripEsc, subRep.RecommendedCLIFlag)
	}
}

func TestCalibrateGateConformalAndBothEmitHesitationCompatibleCLIFlag(t *testing.T) {
	t.Cleanup(func() { outputFormat = outputTable })

	pairPath := filepath.Join("..", "..", "docs", "experiments", "judge-eval", "runs", "20260926", "j4_vs_j1_pairwise_instruction_following.json")
	runMode := func(method string) CalibrateGateReport {
		t.Helper()
		out, err := executeRoot(t,
			"--output", "json",
			"eval", "calibrate-gate",
			"--input", pairPath,
			"--method", method,
			"--target-risk", "0.10",
			"--mcnemar-alpha", "0.10",
			"--bootstrap", "0",
		)
		if err != nil {
			t.Fatalf("calibrate-gate --method %s failed: %v\nout=%s", method, err, out)
		}
		var rep CalibrateGateReport
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("unmarshal --method %s: %v", method, err)
		}
		return rep
	}

	hesRep := runMode("hesitation")
	confRep := runMode("conformal")
	bothRep := runMode("both")

	if confRep.Recommended.Method != gateMethodConformal || confRep.Recommended.ConformalQuantile == nil {
		t.Fatalf("expected conformal recommended summary with conformal_quantile, got %+v", confRep.Recommended)
	}
	if confRep.RecommendedCLIFlag != hesRep.RecommendedCLIFlag || bothRep.RecommendedCLIFlag != hesRep.RecommendedCLIFlag {
		t.Fatalf("CLI flag mismatch across methods: hes=%q conf=%q both=%q",
			hesRep.RecommendedCLIFlag, confRep.RecommendedCLIFlag, bothRep.RecommendedCLIFlag)
	}
	if confRep.RecommendedCLIFlagMethod != gateMethodHesitation || bothRep.RecommendedCLIFlagMethod != gateMethodHesitation {
		t.Fatalf("expected RecommendedCLIFlagMethod=hesitation, got conf=%q both=%q",
			confRep.RecommendedCLIFlagMethod, bothRep.RecommendedCLIFlagMethod)
	}

	cliTau, err := strconv.ParseFloat(strings.TrimPrefix(bothRep.RecommendedCLIFlag, "--cascade-threshold "), 64)
	if err != nil {
		t.Fatalf("parse CLI flag %q: %v", bothRep.RecommendedCLIFlag, err)
	}
	items, _, _, _, err := loadCalibrationFile(pairPath, nil, 0.5, 0)
	if err != nil {
		t.Fatalf("loadCalibrationFile pairwise: %v", err)
	}
	esc := 0
	for _, it := range items {
		if it.hesitation >= cliTau {
			esc++
		}
	}
	if esc != hesRep.Recommended.FullSample.EscalationCount {
		t.Fatalf("hesitation cascade escalations at %q = %d/%d, want %d/%d",
			bothRep.RecommendedCLIFlag, esc, len(items),
			hesRep.Recommended.FullSample.EscalationCount, hesRep.PairedScoredItems)
	}

	confTable, err := executeRoot(t,
		"--output", "table",
		"eval", "calibrate-gate",
		"--input", pairPath,
		"--method", "conformal",
		"--target-risk", "0.10",
		"--mcnemar-alpha", "0.10",
		"--bootstrap", "0",
	)
	if err != nil {
		t.Fatalf("conformal table failed: %v", err)
	}
	if !strings.Contains(confTable, "offline 1-max_p") || !strings.Contains(confTable, "Recommended CLI flag (hesitation gate):") {
		t.Errorf("conformal table missing offline/hesitation labels:\n%s", confTable)
	}
}

func TestComputeHesitationSignalNormalizationAndFoldSentinelMean(t *testing.T) {
	if got := computeHesitationSignal(EngineRun{Custom: map[string]any{
		"probabilities": map[string]float64{"yes": 2.0, "no": 2.0},
	}}); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("unnormalized equal weights hesitation = %.6f, want 1.0", got)
	}

	if got := computeHesitationSignal(EngineRun{Custom: map[string]any{
		"probabilities": map[string]float64{"a": 0.5, "b": 0.5, "c": 0.0, "d": 0.0, "e": 0.0},
	}}); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("zero-padded 2-active-option tie hesitation = %.6f, want 1.0", got)
	}

	if got := computeHesitationSignal(EngineRun{Custom: map[string]any{
		"probabilities": map[string]float64{"a": 1.0, "b": 0.0},
	}}); math.Abs(got-0.0) > 1e-9 {
		t.Errorf("degenerate point-mass hesitation = %.6f, want 0.0", got)
	}

	if got := computeHesitationSignal(EngineRun{Custom: map[string]any{
		"probabilities": map[string]float64{"a": 0.0, "b": 0.0},
	}}); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("all-zero probability map with nil Confidence hesitation = %.6f, want 1.0", got)
	}

	normDist := map[string]float64{"BASELINE": 0.65, "CANDIDATE": 0.25, "TIE": 0.10}
	gotSig := computeHesitationSignal(EngineRun{Custom: map[string]any{"probabilities": normDist}})
	wantHes, ok := hesitationOf(eval.Result{CustomOutput: map[string]any{"probabilities": normDist}})
	if !ok || math.Abs(gotSig-wantHes) > 1e-12 {
		t.Errorf("normalized distribution parity: computeHesitationSignal=%.12f vs hesitationOf=%.12f (ok=%v)", gotSig, wantHes, ok)
	}

	meanTau, minTau, maxTau, infFolds := summarizeFoldThresholds([]float64{0.10, 0.10, 0.10, 0.10, noEscalationThreshold})
	if math.Abs(meanTau-0.10) > 1e-12 || math.Abs(minTau-0.10) > 1e-12 || math.Abs(maxTau-noEscalationThreshold) > 1e-12 || infFolds != 1 {
		t.Errorf("summarizeFoldThresholds(4 feasible + 1 sentinel) = (mean=%.6f, min=%.6f, max=%.6f, inf=%d), want (0.10, 0.10, 1.01, 1)",
			meanTau, minTau, maxTau, infFolds)
	}

	allSentinelMean, _, _, allInf := summarizeFoldThresholds([]float64{noEscalationThreshold, noEscalationThreshold})
	if math.Abs(allSentinelMean-noEscalationThreshold) > 1e-12 || allInf != 2 {
		t.Errorf("summarizeFoldThresholds(all sentinel) = (mean=%.6f, inf=%d), want (1.01, 2)", allSentinelMean, allInf)
	}
}
