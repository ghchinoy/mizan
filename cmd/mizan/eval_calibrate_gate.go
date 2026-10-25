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
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/registry"
)

const (
	calibrateGateSchemaVersion = 1
	gateMethodHesitation       = "hesitation"
	gateMethodConformal        = "conformal"
	gateMethodBoth             = "both"
	noEscalationThreshold      = 1.01
)

// CalibrateGateReport is the machine-readable output of `mizan eval calibrate-gate`.
type CalibrateGateReport struct {
	SchemaVersion              int                     `json:"schema_version"`
	Inputs                     []string                `json:"inputs"`
	Suite                      string                  `json:"suite,omitempty"`
	Method                     string                  `json:"method"`
	Folds                      int                     `json:"folds"`
	CrossValidated             bool                    `json:"cross_validated"`
	Seed                       int64                   `json:"seed"`
	TargetRisk                 float64                 `json:"target_risk"`
	McNemarAlpha               float64                 `json:"mcnemar_alpha"`
	TotalItems                 int                     `json:"total_items"`
	PairedScoredItems          int                     `json:"paired_scored_items"`
	Baseline                   GateBaselineSummary     `json:"baseline"`
	Recommended                GateCalibrationResult   `json:"recommended"`
	Alternatives               []GateCalibrationResult `json:"alternatives,omitempty"`
	CascadeHesitationThreshold float64                 `json:"cascade_hesitation_threshold"`
	RecommendedCLIFlagMethod   string                  `json:"recommended_cli_flag_method,omitempty"`
	RecommendedCLIFlag         string                  `json:"recommended_cli_flag"`
}

// GateBaselineSummary reports uncalibrated accuracy and Paired McNemar parity
// between baseline Engine A (frontier) and fast Engine B (zero escalation).
type GateBaselineSummary struct {
	EngineA        string    `json:"engine_a,omitempty"`
	EngineB        string    `json:"engine_b,omitempty"`
	ACorrect       int       `json:"a_correct"`
	BCorrect       int       `json:"b_correct"`
	AAccuracy      *float64  `json:"a_accuracy,omitempty"`
	BAccuracy      *float64  `json:"b_accuracy,omitempty"`
	AAccuracyCI95  *Interval `json:"a_accuracy_ci95,omitempty"`
	BAccuracyCI95  *Interval `json:"b_accuracy_ci95,omitempty"`
	FastErrorRate  *float64  `json:"fast_error_rate,omitempty"`
	OnlyACorrect   int       `json:"only_a_correct"`
	OnlyBCorrect   int       `json:"only_b_correct"`
	McNemarP       float64   `json:"mcnemar_exact_p"`
	ParityAchieved bool      `json:"parity_achieved"`
}

// GateOperatingPoint summarizes cascade performance at a specific threshold.
type GateOperatingPoint struct {
	Threshold          float64   `json:"threshold"`
	EscalationCount    int       `json:"escalation_count"`
	KeptCount          int       `json:"kept_count"`
	EscalationRate     float64   `json:"escalation_rate"`
	SelectiveRisk      float64   `json:"selective_risk"`
	HybridCorrect      int       `json:"hybrid_correct"`
	HybridAccuracy     *float64  `json:"hybrid_accuracy,omitempty"`
	HybridAccuracyCI95 *Interval `json:"hybrid_accuracy_ci95,omitempty"`
	OnlyACorrect       int       `json:"only_a_correct"`
	OnlyHybridCorrect  int       `json:"only_hybrid_correct"`
	McNemarP           float64   `json:"mcnemar_exact_p"`
	RiskAchieved       bool      `json:"risk_achieved"`
	ParityAchieved     bool      `json:"parity_achieved"`
	Feasible           bool      `json:"feasible"`
}

// GateCalibrationResult holds full-sample, out-of-fold CV, and Pareto curve
// metrics for one uncertainty scoring method.
type GateCalibrationResult struct {
	Method              string               `json:"method"`
	ConformalQuantile   *float64             `json:"conformal_quantile,omitempty"`
	FullSample          GateOperatingPoint   `json:"full_sample"`
	OutOfFold           GateOperatingPoint   `json:"out_of_fold"`
	FoldThresholds      []float64            `json:"fold_thresholds,omitempty"`
	MeanFoldThreshold   *float64             `json:"mean_fold_threshold,omitempty"`
	MinFoldThreshold    *float64             `json:"min_fold_threshold,omitempty"`
	MaxFoldThreshold    *float64             `json:"max_fold_threshold,omitempty"`
	InfeasibleFolds     int                  `json:"infeasible_folds"`
	ZeroEscalationFolds int                  `json:"zero_escalation_folds,omitempty"`
	Sweep               []GateOperatingPoint `json:"sweep"`
}

type calibrateGateOptions struct {
	inputs         []string
	suitePath      string
	method         string
	folds          int
	seed           int64
	targetRisk     float64
	mcnemarAlpha   float64
	scoreTolerance float64
	bootstrapIters int
	outputFile     string
}

type calibrationItem struct {
	id          string
	sourceOrder int
	engineA     string
	engineB     string
	aCorrect    bool
	bCorrect    bool
	hesitation  float64
	conformal   float64
}

type flatCalibrationRecord struct {
	ID              string          `json:"id"`
	Kind            string          `json:"kind,omitempty"`
	Expected        json.RawMessage `json:"expected,omitempty"`
	BaselineCorrect *bool           `json:"baseline_correct,omitempty"`
	FastCorrect     *bool           `json:"fast_correct,omitempty"`
	Score           *float32        `json:"score,omitempty"`
	Passed          *bool           `json:"passed,omitempty"`
	Selection       string          `json:"selection,omitempty"`
	Confidence      *float32        `json:"confidence,omitempty"`
	Probabilities   map[string]any  `json:"probabilities,omitempty"`
	Hesitation      *float64        `json:"hesitation,omitempty"`
	CustomOutput    map[string]any  `json:"custom_output,omitempty"`
}

func newEvalCalibrateGateCmd() *cobra.Command {
	o := calibrateGateOptions{
		method:         gateMethodHesitation,
		folds:          5,
		seed:           20260925,
		targetRisk:     0.0,
		mcnemarAlpha:   0.10,
		scoreTolerance: 0.5,
		bootstrapIters: 2000,
	}

	cmd := &cobra.Command{
		Use:   "calibrate-gate --input <report.json|results.jsonl> [flags]",
		Short: "Calibrate cascade escalation thresholds from batch comparison reports under risk and McNemar parity constraints",
		Long: "Compute cross-validated fast-to-frontier judge escalation thresholds offline\n" +
			"from one or more batch comparison reports or JSONL evaluation logs.\n\n" +
			"For each candidate threshold tau, items with uncertainty >= tau escalate to\n" +
			"Engine A (frontier baseline) while lower-uncertainty items keep Engine B\n" +
			"(fast judge). The sweep selects the largest feasible threshold (minimum\n" +
			"escalation rate) satisfying selective risk <= --target-risk (when set) and\n" +
			"exact two-sided Paired McNemar p > --mcnemar-alpha.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateCalibrateGateOptions(&o); err != nil {
				return err
			}
			rep, err := runCalibrateGate(o)
			if err != nil {
				return err
			}
			if o.outputFile != "" {
				data, err := json.MarshalIndent(rep, "", "  ")
				if err != nil {
					return fmt.Errorf("marshal calibration report: %w", err)
				}
				data = append(data, '\n')
				if err := os.WriteFile(o.outputFile, data, 0o644); err != nil { //nolint:gosec // G306: CLI report output file is intended to be readable
					return fmt.Errorf("write %s: %w", o.outputFile, err)
				}
			}
			if outputFormat == outputJSON {
				return printJSON(cmd.OutOrStdout(), rep)
			}
			return renderCalibrateGateTable(cmd.OutOrStdout(), rep)
		},
	}

	f := cmd.Flags()
	f.StringArrayVar(&o.inputs, "input", nil, "batch comparison JSON report or JSONL evaluation log path (repeatable, required)")
	f.StringVar(&o.suitePath, "suite", "", "optional JSONL dataset path to backfill gold expected labels by item id")
	f.StringVar(&o.method, "method", o.method, "uncertainty score for gating: hesitation | conformal | both")
	f.IntVar(&o.folds, "folds", o.folds, "number of seeded cross-validation folds (1 runs in-sample sweep without splitting)")
	f.Int64Var(&o.seed, "seed", o.seed, "deterministic RNG seed for cross-validation fold assignment and bootstrap CIs")
	f.Float64Var(&o.targetRisk, "target-risk", o.targetRisk, "maximum selective error rate on non-escalated fast predictions in (0, 1); 0 disables")
	f.Float64Var(&o.mcnemarAlpha, "mcnemar-alpha", o.mcnemarAlpha, "minimum exact two-sided Paired McNemar p-value for parity in [0, 1); 0 disables")
	f.Float64Var(&o.scoreTolerance, "score-tolerance", o.scoreTolerance, "numeric tolerance |pred - expected| <= tol when scoring ungraded inputs")
	f.IntVar(&o.bootstrapIters, "bootstrap", o.bootstrapIters, "percentile bootstrap iterations for 95% accuracy CIs (0 disables)")
	f.StringVar(&o.outputFile, "output-file", "", "write full JSON calibration report to this path")
	return cmd
}

func validateCalibrateGateOptions(o *calibrateGateOptions) error {
	var cleaned []string
	for _, p := range o.inputs {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		return fmt.Errorf("--input is required")
	}
	o.inputs = cleaned

	o.method = strings.ToLower(strings.TrimSpace(o.method))
	switch o.method {
	case gateMethodHesitation, gateMethodConformal, gateMethodBoth:
	default:
		return fmt.Errorf("invalid --method %q (want hesitation|conformal|both)", o.method)
	}
	if o.folds < 1 {
		return fmt.Errorf("--folds must be >= 1 (got %d)", o.folds)
	}
	if o.targetRisk < 0 || o.targetRisk >= 1 || math.IsNaN(o.targetRisk) {
		return fmt.Errorf("--target-risk must be in [0, 1) (got %v)", o.targetRisk)
	}
	if o.mcnemarAlpha < 0 || o.mcnemarAlpha >= 1 || math.IsNaN(o.mcnemarAlpha) {
		return fmt.Errorf("--mcnemar-alpha must be in [0, 1) (got %v)", o.mcnemarAlpha)
	}
	if o.targetRisk == 0 && o.mcnemarAlpha == 0 {
		return fmt.Errorf("at least one of --target-risk or --mcnemar-alpha must be greater than 0")
	}
	if o.scoreTolerance < 0 || math.IsNaN(o.scoreTolerance) {
		return fmt.Errorf("--score-tolerance must be >= 0 (got %v)", o.scoreTolerance)
	}
	if o.bootstrapIters < 0 {
		return fmt.Errorf("--bootstrap must be >= 0 (got %d)", o.bootstrapIters)
	}
	return nil
}

func runCalibrateGate(o calibrateGateOptions) (CalibrateGateReport, error) {
	suiteMap := map[string]CompareDatasetItem{}
	if strings.TrimSpace(o.suitePath) != "" {
		loaded, err := loadCalibrationSuiteMap(o.suitePath)
		if err != nil {
			return CalibrateGateReport{}, err
		}
		suiteMap = loaded
	}

	var (
		totalItems int
		paired     []calibrationItem
		engineA    string
		engineB    string
	)
	for _, path := range o.inputs {
		items, rawCount, engA, engB, err := loadCalibrationFile(path, suiteMap, o.scoreTolerance, len(paired))
		if err != nil {
			return CalibrateGateReport{}, err
		}
		totalItems += rawCount
		paired = append(paired, items...)
		if engineA == "" && engA != "" {
			engineA = engA
		}
		if engineB == "" && engB != "" {
			engineB = engB
		}
	}
	if len(paired) == 0 {
		return CalibrateGateReport{}, fmt.Errorf("no paired scored items found in input (pass --suite when gold labels are omitted)")
	}

	sort.SliceStable(paired, func(i, j int) bool {
		if paired[i].id != paired[j].id {
			return paired[i].id < paired[j].id
		}
		return paired[i].sourceOrder < paired[j].sourceOrder
	})

	baseline := summarizeGateBaseline(paired, engineA, engineB, o.mcnemarAlpha, o.bootstrapIters, o.seed)

	hesScores := make([]float64, len(paired))
	for i, it := range paired {
		hesScores[i] = it.hesitation
	}

	var (
		rec          GateCalibrationResult
		hesRes       GateCalibrationResult
		alternatives []GateCalibrationResult
		crossVal     bool
	)
	switch o.method {
	case gateMethodHesitation:
		hesRes, crossVal = calibrateSingleMethod(paired, gateMethodHesitation, o)
		rec = hesRes
	case gateMethodConformal:
		rec, crossVal = calibrateSingleMethod(paired, gateMethodConformal, o)
		// compare-engines --engine-b cascade gates on Shannon hesitation; calibrate
		// hesitation alongside conformal so RecommendedCLIFlag stays on the entropy scale.
		hesRes, _ = calibrateSingleMethod(paired, gateMethodHesitation, o)
	case gateMethodBoth:
		var cvH, cvC bool
		hesRes, cvH = calibrateSingleMethod(paired, gateMethodHesitation, o)
		confRes, cvC := calibrateSingleMethod(paired, gateMethodConformal, o)
		crossVal = cvH || cvC
		if prefersConformalOverHesitation(confRes, hesRes) {
			rec = confRes
			alternatives = []GateCalibrationResult{hesRes}
		} else {
			rec = hesRes
			alternatives = []GateCalibrationResult{confRes}
		}
	}

	cliTauStr := formatThresholdForCLI(hesRes.FullSample.Threshold, hesScores)

	return CalibrateGateReport{
		SchemaVersion:              calibrateGateSchemaVersion,
		Inputs:                     o.inputs,
		Suite:                      o.suitePath,
		Method:                     o.method,
		Folds:                      o.folds,
		CrossValidated:             crossVal,
		Seed:                       o.seed,
		TargetRisk:                 o.targetRisk,
		McNemarAlpha:               o.mcnemarAlpha,
		TotalItems:                 totalItems,
		PairedScoredItems:          len(paired),
		Baseline:                   baseline,
		Recommended:                rec,
		Alternatives:               alternatives,
		CascadeHesitationThreshold: hesRes.FullSample.Threshold,
		RecommendedCLIFlagMethod:   gateMethodHesitation,
		RecommendedCLIFlag:         fmt.Sprintf("--cascade-threshold %s", cliTauStr),
	}, nil
}

func prefersConformalOverHesitation(conf, hes GateCalibrationResult) bool {
	if conf.FullSample.Feasible != hes.FullSample.Feasible {
		return conf.FullSample.Feasible
	}
	if math.Abs(conf.FullSample.EscalationRate-hes.FullSample.EscalationRate) > 1e-9 {
		return conf.FullSample.EscalationRate < hes.FullSample.EscalationRate
	}
	if math.Abs(conf.OutOfFold.EscalationRate-hes.OutOfFold.EscalationRate) > 1e-9 {
		return conf.OutOfFold.EscalationRate < hes.OutOfFold.EscalationRate
	}
	return false
}

func loadCalibrationSuiteMap(path string) (map[string]CompareDatasetItem, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: user-supplied CLI suite path
	if err != nil {
		return nil, fmt.Errorf("read suite %s: %w", path, err)
	}
	out := map[string]CompareDatasetItem{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		var item CompareDatasetItem
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, fmt.Errorf("parse suite %s line %d: %w", path, lineNo, err)
		}
		if item.ID != "" {
			out[item.ID] = item
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan suite %s: %w", path, err)
	}
	return out, nil
}

func loadCalibrationFile(path string, suite map[string]CompareDatasetItem, scoreTol float64, offset int) ([]calibrationItem, int, string, string, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: user-supplied CLI input path
	if err != nil {
		return nil, 0, "", "", fmt.Errorf("read input %s: %w", path, err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, 0, "", "", fmt.Errorf("input %s is empty", path)
	}

	var probe struct {
		Cases *json.RawMessage `json:"cases"`
	}
	if trimmed[0] == '{' && json.Unmarshal(trimmed, &probe) == nil && probe.Cases != nil {
		var rep CompareBatchReport
		if err := json.Unmarshal(trimmed, &rep); err != nil {
			return nil, 0, "", "", fmt.Errorf("parse batch report %s: %w", path, err)
		}
		items := make([]calibrationItem, 0, len(rep.Cases))
		for idx, c := range rep.Cases {
			if item, ok := caseToCalibrationItem(c, suite, scoreTol, offset+idx); ok {
				items = append(items, item)
			}
		}
		return items, len(rep.Cases), rep.Meta.EngineA, rep.Meta.EngineB, nil
	}

	sc := bufio.NewScanner(bytes.NewReader(trimmed))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var (
		items    []calibrationItem
		rawCount int
		engA     string
		engB     string
		lineNo   int
	)
	for sc.Scan() {
		lineNo++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		rawCount++

		var caseProbe struct {
			Comparison *json.RawMessage `json:"comparison"`
		}
		if json.Unmarshal(line, &caseProbe) == nil && caseProbe.Comparison != nil {
			var c CompareCaseResult
			if err := json.Unmarshal(line, &c); err != nil {
				return nil, 0, "", "", fmt.Errorf("parse case record %s line %d: %w", path, lineNo, err)
			}
			if item, ok := caseToCalibrationItem(c, suite, scoreTol, offset+rawCount-1); ok {
				if engA == "" {
					engA = item.engineA
				}
				if engB == "" {
					engB = item.engineB
				}
				items = append(items, item)
			}
			continue
		}

		var flat flatCalibrationRecord
		if err := json.Unmarshal(line, &flat); err != nil {
			return nil, 0, "", "", fmt.Errorf("parse jsonl %s line %d: %w", path, lineNo, err)
		}
		if item, ok := flatToCalibrationItem(flat, suite, scoreTol, offset+rawCount-1); ok {
			items = append(items, item)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, 0, "", "", fmt.Errorf("scan input %s: %w", path, err)
	}
	return items, rawCount, engA, engB, nil
}

func caseToCalibrationItem(c CompareCaseResult, suite map[string]CompareDatasetItem, scoreTol float64, order int) (calibrationItem, bool) {
	if c.Expected == "" {
		if s, ok := suite[c.ID]; ok && s.Expected != "" {
			c.Expected = s.Expected
		}
	}
	ea := c.Comparison.EngineA
	eb := c.Comparison.EngineB
	kind := registry.MetricKind(c.Comparison.Kind)
	if c.Expected != "" {
		if ea.Correct == nil {
			scoreRun(&ea, kind, c.Expected, scoreTol)
		}
		if eb.Correct == nil {
			scoreRun(&eb, kind, c.Expected, scoreTol)
		}
	}
	if !ea.ok() || !eb.ok() || ea.Skipped || eb.Skipped || ea.Correct == nil || eb.Correct == nil {
		return calibrationItem{}, false
	}
	hes, conf := extractRunUncertainties(eb)
	return calibrationItem{
		id:          c.ID,
		sourceOrder: order,
		engineA:     ea.Engine,
		engineB:     eb.Engine,
		aCorrect:    *ea.Correct,
		bCorrect:    *eb.Correct,
		hesitation:  hes,
		conformal:   conf,
	}, true
}

func flatToCalibrationItem(r flatCalibrationRecord, suite map[string]CompareDatasetItem, scoreTol float64, order int) (calibrationItem, bool) {
	expected := normalizeExpected(r.Expected)
	kindStr := r.Kind
	if s, ok := suite[r.ID]; ok {
		if expected == "" {
			expected = s.Expected
		}
	}
	fastCorrect := r.FastCorrect
	if fastCorrect == nil && expected != "" && kindStr != "" {
		run := EngineRun{
			Engine:     "fast",
			Score:      r.Score,
			Passed:     r.Passed,
			Selection:  r.Selection,
			Confidence: r.Confidence,
			Custom:     r.CustomOutput,
		}
		scoreRun(&run, registry.MetricKind(kindStr), expected, scoreTol)
		fastCorrect = run.Correct
	}
	if r.BaselineCorrect == nil || fastCorrect == nil {
		return calibrationItem{}, false
	}
	custom := map[string]any{}
	for k, v := range r.CustomOutput {
		custom[k] = v
	}
	if len(r.Probabilities) > 0 {
		custom["probabilities"] = r.Probabilities
	}
	if r.Hesitation != nil {
		custom["cascade_hesitation"] = *r.Hesitation
	}
	hes, conf := extractRunUncertainties(EngineRun{
		Confidence: r.Confidence,
		Custom:     custom,
	})
	return calibrationItem{
		id:          r.ID,
		sourceOrder: order,
		engineA:     "baseline",
		engineB:     "fast",
		aCorrect:    *r.BaselineCorrect,
		bCorrect:    *fastCorrect,
		hesitation:  hes,
		conformal:   conf,
	}, true
}

func extractRunUncertainties(run EngineRun) (hesitation, conformal float64) {
	hesitation = computeHesitationSignal(run)
	conformal = computeConformalSignal(run)
	return hesitation, conformal
}

func computeHesitationSignal(run EngineRun) float64 {
	if run.Custom != nil {
		if rawH, ok := asGateFloat(run.Custom["cascade_hesitation"]); ok {
			return clampUnit(rawH)
		}
		if probs := extractProbabilityValues(run.Custom["probabilities"]); len(probs) >= 2 {
			var sum float64
			var posCount int
			for _, p := range probs {
				if p > 0 {
					sum += p
					posCount++
				}
			}
			if sum > 0 {
				if posCount <= 1 {
					return 0.0
				}
				var h float64
				for _, p := range probs {
					if p > 0 {
						pn := p / sum
						h -= pn * math.Log(pn)
					}
				}
				return clampUnit(h / math.Log(float64(posCount)))
			}
		}
		if per, ok := run.Custom["per_criterion"].([]any); ok && len(per) > 0 {
			var sumEnt float64
			var entCount int
			var sumConf float64
			var confCount int
			for _, elem := range per {
				m, ok := elem.(map[string]any)
				if !ok {
					continue
				}
				if e, ok := asGateFloat(m["entropy"]); ok {
					sumEnt += e / math.Ln2
					entCount++
				} else if c, ok := asGateFloat(m["confidence"]); ok {
					sumConf += binaryNormalizedEntropy(c)
					confCount++
				}
			}
			if entCount == len(per) {
				return clampUnit(sumEnt / float64(entCount))
			}
			if entCount+confCount > 0 {
				return clampUnit((sumEnt + sumConf) / float64(entCount+confCount))
			}
		}
	}
	if run.Confidence != nil {
		return binaryNormalizedEntropy(float64(*run.Confidence))
	}
	return 1.0
}

func computeConformalSignal(run EngineRun) float64 {
	if run.Custom != nil {
		if probs := extractProbabilityValues(run.Custom["probabilities"]); len(probs) > 0 {
			var sum, maxP float64
			for _, p := range probs {
				if p > 0 {
					sum += p
					if p > maxP {
						maxP = p
					}
				}
			}
			if sum > 0 {
				return clampUnit(1.0 - maxP/sum)
			}
		}
		if per, ok := run.Custom["per_criterion"].([]any); ok && len(per) > 0 {
			var sum float64
			var count int
			for _, elem := range per {
				m, ok := elem.(map[string]any)
				if !ok {
					continue
				}
				if c, ok := asGateFloat(m["confidence"]); ok {
					sum += 1.0 - clampUnit(c)
					count++
				}
			}
			if count > 0 {
				return clampUnit(sum / float64(count))
			}
		}
	}
	if run.Confidence != nil {
		return clampUnit(1.0 - clampUnit(float64(*run.Confidence)))
	}
	return 1.0
}

func extractProbabilityValues(raw any) []float64 {
	switch m := raw.(type) {
	case map[string]float64:
		keys := make([]string, 0, len(m))
		for k, v := range m {
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		out := make([]float64, 0, len(keys))
		for _, k := range keys {
			out = append(out, m[k])
		}
		return out
	case map[string]any:
		type kv struct {
			k string
			v float64
		}
		pairs := make([]kv, 0, len(m))
		for k, v := range m {
			if f, ok := asGateFloat(v); ok {
				pairs = append(pairs, kv{k: k, v: f})
			}
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].k < pairs[j].k })
		out := make([]float64, 0, len(pairs))
		for _, p := range pairs {
			out = append(out, p.v)
		}
		return out
	default:
		return nil
	}
}

func binaryNormalizedEntropy(conf float64) float64 {
	p := math.Min(math.Max(conf, 1e-9), 1.0-1e-9)
	h := -(p*math.Log(p) + (1.0-p)*math.Log(1.0-p)) / math.Ln2
	return clampUnit(h)
}

func clampUnit(v float64) float64 {
	if math.IsNaN(v) {
		return 1.0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func asGateFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return x, true
	case float32:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	default:
		return 0, false
	}
}

func summarizeGateBaseline(items []calibrationItem, engineA, engineB string, mcnemarAlpha float64, bootIters int, seed int64) GateBaselineSummary {
	n := len(items)
	var aCorr, bCorr, onlyA, onlyB int
	aVec := make([]float64, n)
	bVec := make([]float64, n)
	for i, it := range items {
		if it.aCorrect {
			aCorr++
			aVec[i] = 1
		}
		if it.bCorrect {
			bCorr++
			bVec[i] = 1
		}
		if it.aCorrect && !it.bCorrect {
			onlyA++
		} else if !it.aCorrect && it.bCorrect {
			onlyB++
		}
	}
	pVal := mcnemarExact(onlyA, onlyB)
	out := GateBaselineSummary{
		EngineA:        engineA,
		EngineB:        engineB,
		ACorrect:       aCorr,
		BCorrect:       bCorr,
		AAccuracy:      finite(float64(aCorr) / float64(n)),
		BAccuracy:      finite(float64(bCorr) / float64(n)),
		FastErrorRate:  finite(float64(n-bCorr) / float64(n)),
		OnlyACorrect:   onlyA,
		OnlyBCorrect:   onlyB,
		McNemarP:       pVal,
		ParityAchieved: mcnemarAlpha == 0 || pVal > mcnemarAlpha,
	}
	if bootIters > 0 {
		ciA := bootstrapMeanCI(aVec, bootIters, seed)
		ciB := bootstrapMeanCI(bVec, bootIters, seed+1)
		out.AAccuracyCI95 = &ciA
		out.BAccuracyCI95 = &ciB
	}
	return out
}

func itemUncertainty(it calibrationItem, method string) float64 {
	if method == gateMethodConformal {
		return it.conformal
	}
	return it.hesitation
}

func calibrateSingleMethod(items []calibrationItem, method string, o calibrateGateOptions) (GateCalibrationResult, bool) {
	n := len(items)
	scores := make([]float64, n)
	for i, it := range items {
		scores[i] = itemUncertainty(it, method)
	}

	var conformalQ *float64
	if method == gateMethodConformal && o.targetRisk > 0 && n > 0 {
		qLevel := math.Min(1.0, math.Ceil(float64(n+1)*(1.0-o.targetRisk))/float64(n))
		q := percentile(scores, qLevel)
		conformalQ = &q
	}

	fullTau := selectOptimalThreshold(items, scores, o.targetRisk, o.mcnemarAlpha)
	fullPoint := evaluateOperatingPoint(items, scores, fullTau, o.targetRisk, o.mcnemarAlpha, o.bootstrapIters, o.seed)
	sweep := buildSweepCurve(items, scores, fullTau, conformalQ, o.targetRisk, o.mcnemarAlpha)

	if o.folds < 2 || n < 2 {
		return GateCalibrationResult{
			Method:            method,
			ConformalQuantile: conformalQ,
			FullSample:        fullPoint,
			OutOfFold:         fullPoint,
			Sweep:             sweep,
		}, false
	}

	effFolds := o.folds
	if effFolds > n {
		effFolds = n
	}
	rng := rand.New(rand.NewPCG(uint64(o.seed), uint64(o.seed)^0x9e3779b97f4a7c15)) //nolint:gosec // G404: deterministic cross-validation fold shuffle
	perm := rng.Perm(n)
	foldOf := make([]int, n)
	for rank, idx := range perm {
		foldOf[idx] = rank % effFolds
	}

	foldTaus := make([]float64, effFolds)
	oofEscalated := make([]bool, n)
	for k := 0; k < effFolds; k++ {
		calItems := make([]calibrationItem, 0, n)
		calScores := make([]float64, 0, n)
		for i := 0; i < n; i++ {
			if foldOf[i] != k {
				calItems = append(calItems, items[i])
				calScores = append(calScores, scores[i])
			}
		}
		tauK := selectOptimalThreshold(calItems, calScores, o.targetRisk, o.mcnemarAlpha)
		foldTaus[k] = tauK
		for i := 0; i < n; i++ {
			if foldOf[i] == k {
				oofEscalated[i] = scores[i] >= tauK
			}
		}
	}

	meanTau, minTau, maxTau, infeasibleFolds := summarizeFoldThresholds(foldTaus)

	oofPoint := summarizeEscalationDecisions(items, oofEscalated, meanTau, o.targetRisk, o.mcnemarAlpha, o.bootstrapIters, o.seed+2)

	return GateCalibrationResult{
		Method:              method,
		ConformalQuantile:   conformalQ,
		FullSample:          fullPoint,
		OutOfFold:           oofPoint,
		FoldThresholds:      foldTaus,
		MeanFoldThreshold:   finite(meanTau),
		MinFoldThreshold:    finite(minTau),
		MaxFoldThreshold:    finite(maxTau),
		InfeasibleFolds:     infeasibleFolds,
		ZeroEscalationFolds: infeasibleFolds,
		Sweep:               sweep,
	}, true
}

func summarizeFoldThresholds(foldTaus []float64) (meanTau, minTau, maxTau float64, infeasibleFolds int) {
	if len(foldTaus) == 0 {
		return 0, 0, 0, 0
	}
	minTau = foldTaus[0]
	maxTau = foldTaus[0]
	var activeSum float64
	var activeCount int
	for _, t := range foldTaus {
		if t < minTau {
			minTau = t
		}
		if t > maxTau {
			maxTau = t
		}
		if math.IsNaN(t) || math.IsInf(t, 0) || t > 1.0 || t < 0.0 {
			infeasibleFolds++
		} else {
			activeSum += t
			activeCount++
		}
	}
	if activeCount > 0 {
		meanTau = activeSum / float64(activeCount)
	} else {
		meanTau = noEscalationThreshold
	}
	return meanTau, minTau, maxTau, infeasibleFolds
}

func candidateThresholdGrid(scores []float64, extra *float64) []float64 {
	raw := make([]float64, 0, len(scores)+4)
	raw = append(raw, noEscalationThreshold, 0.0)
	raw = append(raw, scores...)
	if extra != nil && !math.IsNaN(*extra) && !math.IsInf(*extra, 0) {
		raw = append(raw, *extra)
	}
	sort.Slice(raw, func(i, j int) bool { return raw[i] > raw[j] })
	dedup := make([]float64, 0, len(raw))
	for _, v := range raw {
		if len(dedup) == 0 || math.Abs(dedup[len(dedup)-1]-v) > 1e-12 {
			dedup = append(dedup, v)
		}
	}
	return dedup
}

func selectOptimalThreshold(items []calibrationItem, scores []float64, targetRisk, mcnemarAlpha float64) float64 {
	grid := candidateThresholdGrid(scores, nil)
	for _, tau := range grid {
		pt := evaluateOperatingPoint(items, scores, tau, targetRisk, mcnemarAlpha, 0, 0)
		if pt.Feasible {
			return tau
		}
	}
	return 0.0
}

func evaluateOperatingPoint(items []calibrationItem, scores []float64, tau, targetRisk, mcnemarAlpha float64, bootIters int, seed int64) GateOperatingPoint {
	escalated := make([]bool, len(items))
	for i, s := range scores {
		escalated[i] = s >= tau
	}
	return summarizeEscalationDecisions(items, escalated, tau, targetRisk, mcnemarAlpha, bootIters, seed)
}

func summarizeEscalationDecisions(items []calibrationItem, escalated []bool, tau, targetRisk, mcnemarAlpha float64, bootIters int, seed int64) GateOperatingPoint {
	n := len(items)
	var escCount, keptCount, keptErrors, hybridCorrect, onlyA, onlyHybrid int
	hybridVec := make([]float64, n)
	for i, it := range items {
		var hCorr bool
		if escalated[i] {
			escCount++
			hCorr = it.aCorrect
		} else {
			keptCount++
			hCorr = it.bCorrect
			if !it.bCorrect {
				keptErrors++
			}
		}
		if hCorr {
			hybridCorrect++
			hybridVec[i] = 1
		}
		if it.aCorrect && !hCorr {
			onlyA++
		} else if !it.aCorrect && hCorr {
			onlyHybrid++
		}
	}
	var selRisk float64
	if keptCount > 0 {
		selRisk = float64(keptErrors) / float64(keptCount)
	}
	pVal := mcnemarExact(onlyA, onlyHybrid)
	riskOK := targetRisk == 0 || selRisk <= targetRisk+1e-12
	parityOK := mcnemarAlpha == 0 || pVal > mcnemarAlpha
	pt := GateOperatingPoint{
		Threshold:         tau,
		EscalationCount:   escCount,
		KeptCount:         keptCount,
		EscalationRate:    float64(escCount) / float64(n),
		SelectiveRisk:     selRisk,
		HybridCorrect:     hybridCorrect,
		HybridAccuracy:    finite(float64(hybridCorrect) / float64(n)),
		OnlyACorrect:      onlyA,
		OnlyHybridCorrect: onlyHybrid,
		McNemarP:          pVal,
		RiskAchieved:      riskOK,
		ParityAchieved:    parityOK,
		Feasible:          riskOK && parityOK,
	}
	if bootIters > 0 && n > 0 {
		ci := bootstrapMeanCI(hybridVec, bootIters, seed)
		pt.HybridAccuracyCI95 = &ci
	}
	return pt
}

func buildSweepCurve(items []calibrationItem, scores []float64, optimalTau float64, conformalQ *float64, targetRisk, mcnemarAlpha float64) []GateOperatingPoint {
	grid := candidateThresholdGrid(scores, conformalQ)
	const maxInterior = 8
	chosen := make([]float64, 0, maxInterior+4)
	chosen = append(chosen, noEscalationThreshold, optimalTau, 0.0)
	if conformalQ != nil {
		chosen = append(chosen, *conformalQ)
	}
	if len(grid) <= maxInterior+2 {
		chosen = append(chosen, grid...)
	} else {
		for k := 1; k <= maxInterior; k++ {
			idx := k * (len(grid) - 1) / (maxInterior + 1)
			chosen = append(chosen, grid[idx])
		}
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i] > chosen[j] })
	var taus []float64
	for _, v := range chosen {
		if len(taus) == 0 || math.Abs(taus[len(taus)-1]-v) > 1e-12 {
			taus = append(taus, v)
		}
	}
	out := make([]GateOperatingPoint, 0, len(taus))
	for _, tau := range taus {
		out = append(out, evaluateOperatingPoint(items, scores, tau, targetRisk, mcnemarAlpha, 0, 0))
	}
	return out
}

func formatThreshold(v float64) string {
	if v == 0 {
		return "0.0000"
	}
	return strconv.FormatFloat(v, 'g', 6, 64)
}

func formatThresholdForCLI(tau float64, scores []float64) string {
	if tau <= 0 {
		return "0.0000"
	}
	if tau >= noEscalationThreshold {
		return "1.01"
	}
	lower := 0.0
	for _, s := range scores {
		if s < tau && s > lower {
			lower = s
		}
	}
	for _, prec := range []int{6, 8, 10, 12} {
		cand := strconv.FormatFloat(tau, 'g', prec, 64)
		if parsed, err := strconv.ParseFloat(cand, 64); err == nil && parsed > lower && parsed <= tau {
			return cand
		}
		exp := math.Floor(math.Log10(tau))
		scale := math.Pow10(prec - 1 - int(exp))
		if scale > 0 && !math.IsInf(scale, 0) {
			floored := math.Floor(tau*scale) / scale
			candFloor := strconv.FormatFloat(floored, 'g', prec, 64)
			if parsed, err := strconv.ParseFloat(candFloor, 64); err == nil && parsed > lower && parsed <= tau {
				return candFloor
			}
		}
	}
	return strconv.FormatFloat(tau, 'g', -1, 64)
}

func renderCalibrateGateTable(w io.Writer, rep CalibrateGateReport) error {
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "CALIBRATE GATE SUMMARY")
	fmt.Fprintf(tw, "Inputs:\t%s\n", strings.Join(rep.Inputs, ", "))
	if rep.Suite != "" {
		fmt.Fprintf(tw, "Suite:\t%s\n", rep.Suite)
	}
	fmt.Fprintf(tw, "Items:\t%d paired scored (%d total)\n", rep.PairedScoredItems, rep.TotalItems)
	riskDesc := "disabled"
	if rep.TargetRisk > 0 {
		riskDesc = fmt.Sprintf("<= %.1f%%", 100*rep.TargetRisk)
	}
	parityDesc := "disabled"
	if rep.McNemarAlpha > 0 {
		parityDesc = fmt.Sprintf("p > %.2f", rep.McNemarAlpha)
	}
	fmt.Fprintf(tw, "Constraints:\tselective risk %s, Paired McNemar %s\n", riskDesc, parityDesc)
	if rep.CrossValidated {
		fmt.Fprintf(tw, "Validation:\t%d-fold seeded CV (seed=%d)\n", rep.Folds, rep.Seed)
	} else {
		fmt.Fprintf(tw, "Validation:\tin-sample sweep (folds=%d)\n", rep.Folds)
	}

	fmt.Fprintln(tw, "\nUNCALIBRATED BASELINE")
	fmt.Fprintf(tw, "Baseline (Engine A):\t%d/%d (%.1f%%)\n",
		rep.Baseline.ACorrect, rep.PairedScoredItems, gatePct(rep.Baseline.AAccuracy))
	fmt.Fprintf(tw, "Fast (Engine B):\t%d/%d (%.1f%%)\n",
		rep.Baseline.BCorrect, rep.PairedScoredItems, gatePct(rep.Baseline.BAccuracy))
	fmt.Fprintf(tw, "McNemar exact p:\t%.4f (onlyA=%d, onlyB=%d, parity=%s)\n",
		rep.Baseline.McNemarP, rep.Baseline.OnlyACorrect, rep.Baseline.OnlyBCorrect, gateYesNo(rep.Baseline.ParityAchieved))

	rec := rep.Recommended
	fmt.Fprintln(tw, "\nRECOMMENDED GATE")
	fmt.Fprintf(tw, "Method:\t%s\n", rec.Method)
	if rec.Method == gateMethodConformal {
		fmt.Fprintf(tw, "Full-sample threshold (offline 1-max_p):\t%s\n", formatThreshold(rec.FullSample.Threshold))
	} else {
		fmt.Fprintf(tw, "Full-sample threshold:\t%s\n", formatThreshold(rec.FullSample.Threshold))
	}
	if rec.MinFoldThreshold != nil && rec.MaxFoldThreshold != nil {
		fmt.Fprintf(tw, "CV fold thresholds:\tmin=%s, max=%s\n", formatThreshold(*rec.MinFoldThreshold), formatThreshold(*rec.MaxFoldThreshold))
	}
	fmt.Fprintf(tw, "Full-sample escalation:\t%d/%d (%.1f%%)\n",
		rec.FullSample.EscalationCount, rep.PairedScoredItems, 100*rec.FullSample.EscalationRate)
	fmt.Fprintf(tw, "Full-sample selective risk:\t%.1f%%\n", 100*rec.FullSample.SelectiveRisk)
	fmt.Fprintf(tw, "Full-sample hybrid acc:\t%.1f%% (McNemar p=%.4f, feasible=%s)\n",
		gatePct(rec.FullSample.HybridAccuracy), rec.FullSample.McNemarP, gateYesNo(rec.FullSample.Feasible))
	if rep.CrossValidated {
		fmt.Fprintf(tw, "Out-of-fold escalation:\t%d/%d (%.1f%%)\n",
			rec.OutOfFold.EscalationCount, rep.PairedScoredItems, 100*rec.OutOfFold.EscalationRate)
		fmt.Fprintf(tw, "Out-of-fold selective risk:\t%.1f%%\n", 100*rec.OutOfFold.SelectiveRisk)
		fmt.Fprintf(tw, "Out-of-fold hybrid acc:\t%.1f%% (McNemar p=%.4f)\n",
			gatePct(rec.OutOfFold.HybridAccuracy), rec.OutOfFold.McNemarP)
	}

	fmt.Fprintln(tw, "\nPARETO SWEEP")
	fmt.Fprintln(tw, "THRESHOLD\tESCALATION\tSELECTIVE RISK\tHYBRID ACC\tMCNEMAR P\tFEASIBLE")
	for _, pt := range rec.Sweep {
		fmt.Fprintf(tw, "%s\t%d/%d (%.1f%%)\t%.1f%%\t%.1f%%\t%.4f\t%s\n",
			formatThreshold(pt.Threshold),
			pt.EscalationCount, rep.PairedScoredItems, 100*pt.EscalationRate,
			100*pt.SelectiveRisk,
			gatePct(pt.HybridAccuracy),
			pt.McNemarP,
			gateYesNo(pt.Feasible),
		)
	}
	if rec.Method == gateMethodConformal {
		fmt.Fprintf(tw, "\nRecommended CLI flag (hesitation gate):\t%s\n", rep.RecommendedCLIFlag)
	} else {
		fmt.Fprintf(tw, "\nRecommended CLI flag:\t%s\n", rep.RecommendedCLIFlag)
	}
	return tw.Flush()
}

func gatePct(v *float64) float64 {
	if v == nil {
		return 0
	}
	return 100 * (*v)
}

func gateYesNo(b bool) string {
	if b {
		return "YES"
	}
	return "NO"
}
