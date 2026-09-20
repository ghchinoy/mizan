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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/wire"
)

// EngineCompareResult holds side-by-side execution metrics from two engines.
type EngineCompareResult struct {
	MetricID      string    `json:"metric_id"`
	Kind          string    `json:"kind"`
	Agreement     bool      `json:"agreement"`
	SpeedupFactor float64   `json:"speedup_factor"`
	EngineA       EngineRun `json:"engine_a"`
	EngineB       EngineRun `json:"engine_b"`
}

// EngineRun holds result and telemetry from one engine's run.
type EngineRun struct {
	Engine      string         `json:"engine"`
	Model       string         `json:"model,omitempty"`
	Passed      *bool          `json:"passed,omitempty"`
	Score       *float32       `json:"score,omitempty"`
	Confidence  *float32       `json:"confidence,omitempty"`
	Selection   string         `json:"selection,omitempty"`
	Explanation string         `json:"explanation,omitempty"`
	DurationMs  float64        `json:"duration_ms"`
	Custom      map[string]any `json:"custom_output,omitempty"`
}

// CompareDatasetItem represents one item in a JSONL benchmark dataset.
type CompareDatasetItem struct {
	ID       string            `json:"id"`
	Metric   string            `json:"metric"`
	Tier     string            `json:"tier,omitempty"`
	Category string            `json:"category,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
	Files    map[string]string `json:"files,omitempty"`
	GCS      map[string]string `json:"gcs,omitempty"`
	Expected string            `json:"expected,omitempty"`
}

// CompareBatchReport summarizes the aggregated outcomes of a batch cross-engine benchmark.
type CompareBatchReport struct {
	TotalCases       int                   `json:"total_cases"`
	Agreements       int                   `json:"agreements"`
	AgreementPct     float64               `json:"agreement_pct"`
	AvgSpeedupFactor float64               `json:"avg_speedup_factor"`
	EngineAAvgMs     float64               `json:"engine_a_avg_ms"`
	EngineBAvgMs     float64               `json:"engine_b_avg_ms"`
	TierBreakdown    map[string]TierReport `json:"tier_breakdown,omitempty"`
	Cases            []CompareCaseResult   `json:"cases"`
}

// TierReport holds agreement metrics for one benchmark difficulty tier.
type TierReport struct {
	Total        int     `json:"total"`
	Agreements   int     `json:"agreements"`
	AgreementPct float64 `json:"agreement_pct"`
}

// CompareCaseResult holds the individual comparison result for a dataset item.
type CompareCaseResult struct {
	ID         string              `json:"id"`
	Tier       string              `json:"tier,omitempty"`
	Category   string              `json:"category,omitempty"`
	Expected   string              `json:"expected,omitempty"`
	Comparison EngineCompareResult `json:"comparison"`
}

func newEvalCompareEnginesCmd() *cobra.Command {
	var (
		metric            string
		datasetPath       string
		outputFile        string
		engineA           string
		engineB           string
		modelA            string
		modelB            string
		diffusionEndpoint string
		fields            []string
		files             []string
		gcs               []string
		noStore           bool
	)
	cmd := &cobra.Command{
		Use:   "compare-engines (--metric <id> | --dataset <path.jsonl>) [--field key=value] [--file key=/path] [--engine-a vertex] [--engine-b diffusion]",
		Short: "Compare evaluation engines side-by-side in parallel (e.g. Vertex AI vs DiffusionGemma)",
		Long: "Execute an evaluation template across two evaluation engines concurrently\n" +
			"and compare their verdicts, latency, confidence, and speedup factor.\n\n" +
			"Default compares Engine A (Vertex AI Gemini) with Engine B (DiffusionGemma).\n" +
			"Pass --dataset <path.jsonl> to run a multi-case benchmark suite with statistical aggregation.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (metric == "") == (datasetPath == "") {
				return fmt.Errorf("exactly one of --metric or --dataset is required")
			}
			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			if diffusionEndpoint != "" {
				cfg.DiffusionEndpoint = diffusionEndpoint
			}
			if modelB != "" && strings.EqualFold(engineB, "diffusion") {
				cfg.DiffusionModel = modelB
			} else if modelA != "" && strings.EqualFold(engineA, "diffusion") {
				cfg.DiffusionModel = modelA
			}
			applyProjectOverride(cfg, projectOverride)

			svc, closeSvc, err := wire.OpenService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			eng, closeEng, err := openEngine(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeEng() }()

			if datasetPath != "" {
				return runCompareDataset(cmd, cfg, eng, svc, datasetPath, engineA, engineB, modelA, modelB, outputFile)
			}

			tmpl, err := svc.Get(cmd.Context(), metric)
			if err != nil {
				return err
			}

			inst, err := buildInstance(fields, files, gcs)
			if err != nil {
				return err
			}

			return runCompareEngines(cmd, cfg, eng, tmpl, inst, engineA, engineB, modelA, modelB, noStore)
		},
	}
	cmd.Flags().StringVar(&metric, "metric", "", "template id (<namespace>/<slug>) (mutually exclusive with --dataset)")
	cmd.Flags().StringVar(&datasetPath, "dataset", "", "path to JSONL evaluation dataset to compare in batch")
	cmd.Flags().StringVar(&outputFile, "output-file", "", "optional path to save structured JSON benchmark report")
	cmd.Flags().StringVar(&engineA, "engine-a", "vertex", "engine A: vertex (or genai) or diffusion")
	cmd.Flags().StringVar(&engineB, "engine-b", "diffusion", "engine B: vertex or diffusion")
	cmd.Flags().StringVar(&modelA, "model-a", "", "model override for engine A")
	cmd.Flags().StringVar(&modelB, "model-b", "", "model override for engine B")
	cmd.Flags().StringVar(&diffusionEndpoint, "diffusion-endpoint", "", "override DiffusionGemma server endpoint")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "instance field as key=value (repeatable)")
	cmd.Flags().StringArrayVar(&files, "file", nil, "local asset as key=/path (repeatable)")
	cmd.Flags().StringArrayVar(&gcs, "gcs", nil, "pre-staged asset as key=gs://... (repeatable)")
	cmd.Flags().BoolVar(&noStore, "no-store", false, "do not persist comparison results to results store")

	return cmd
}

func runCompareEngines(cmd *cobra.Command, cfg *config.Config, eng *eval.Engine, tmpl *registry.MetricTemplate, inst eval.Instance, engineA, engineB, modelA, modelB string, noStore bool) error {
	cmp, err := executeSingleComparison(cmd.Context(), eng, tmpl, inst, engineA, engineB, modelA, modelB)
	if err != nil {
		return err
	}

	if outputFormat == outputJSON {
		return printJSON(cmd.OutOrStdout(), cmp)
	}

	return renderCompareTable(cmd.OutOrStdout(), cmp)
}

func executeSingleComparison(ctx context.Context, eng *eval.Engine, tmpl *registry.MetricTemplate, inst eval.Instance, engineA, engineB, modelA, modelB string) (EngineCompareResult, error) {
	var (
		resA eval.Result
		resB eval.Result
		durA time.Duration
		durB time.Duration
	)

	g, gctx := errgroup.WithContext(ctx)

	// Engine A
	g.Go(func() error {
		start := time.Now()
		var opts []eval.RunOption
		opts = append(opts, eval.WithEngine(engineA))
		if modelA != "" {
			opts = append(opts, eval.WithModel(modelA))
		}
		if tmpl.Kind == registry.KindRubric {
			opts = append(opts, eval.WithRubricDetailDefaultScale())
		}
		var err error
		resA, err = eng.Run(gctx, *tmpl, inst, opts...)
		durA = time.Since(start)
		return err
	})

	// Engine B
	g.Go(func() error {
		start := time.Now()
		var opts []eval.RunOption
		opts = append(opts, eval.WithEngine(engineB))
		if modelB != "" {
			opts = append(opts, eval.WithModel(modelB))
		}
		var err error
		resB, err = eng.Run(gctx, *tmpl, inst, opts...)
		durB = time.Since(start)
		return err
	})

	if err := g.Wait(); err != nil {
		return EngineCompareResult{}, fmt.Errorf("eval comparison failed: %w", err)
	}

	agreement := checkAgreement(tmpl.Kind, resA, resB)
	var speedup float64
	if durB > 0 {
		speedup = float64(durA) / float64(durB)
	}

	return EngineCompareResult{
		MetricID:      tmpl.ID,
		Kind:          string(tmpl.Kind),
		Agreement:     agreement,
		SpeedupFactor: speedup,
		EngineA: EngineRun{
			Engine:      engineA,
			Model:       modelA,
			Passed:      resA.Passed,
			Score:       resA.Score,
			Confidence:  resA.Confidence,
			Selection:   resA.ChoiceSelection,
			Explanation: resA.Explanation,
			DurationMs:  float64(durA.Milliseconds()),
			Custom:      resA.CustomOutput,
		},
		EngineB: EngineRun{
			Engine:      engineB,
			Model:       modelB,
			Passed:      resB.Passed,
			Score:       resB.Score,
			Confidence:  resB.Confidence,
			Selection:   resB.ChoiceSelection,
			Explanation: resB.Explanation,
			DurationMs:  float64(durB.Milliseconds()),
			Custom:      resB.CustomOutput,
		},
	}, nil
}

func runCompareDataset(cmd *cobra.Command, cfg *config.Config, eng *eval.Engine, svc *registry.Service, datasetPath, engineA, engineB, modelA, modelB, outputFile string) error {
	f, err := os.Open(datasetPath)
	if err != nil {
		return fmt.Errorf("open dataset %q: %w", datasetPath, err)
	}
	defer f.Close()

	tmplCache := make(map[string]*registry.MetricTemplate)
	var (
		cases        []CompareCaseResult
		totalAgree   int
		totalSpeedup float64
		totalAms     float64
		totalBms     float64
		tierCounts   = make(map[string]*TierReport)
	)

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var item CompareDatasetItem
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return fmt.Errorf("line %d: invalid JSON: %w", lineNum, err)
		}
		if item.Metric == "" {
			return fmt.Errorf("line %d: missing required 'metric' field", lineNum)
		}

		tmpl, ok := tmplCache[item.Metric]
		if !ok {
			t, err := svc.Get(cmd.Context(), item.Metric)
			if err != nil {
				return fmt.Errorf("line %d: get template %q: %w", lineNum, item.Metric, err)
			}
			tmpl = t
			tmplCache[item.Metric] = tmpl
		}

		var fieldArgs, fileArgs, gcsArgs []string
		for k, v := range item.Fields {
			fieldArgs = append(fieldArgs, fmt.Sprintf("%s=%s", k, v))
		}
		for k, v := range item.Files {
			fileArgs = append(fileArgs, fmt.Sprintf("%s=%s", k, v))
		}
		for k, v := range item.GCS {
			gcsArgs = append(gcsArgs, fmt.Sprintf("%s=%s", k, v))
		}

		inst, err := buildInstance(fieldArgs, fileArgs, gcsArgs)
		if err != nil {
			return fmt.Errorf("line %d (%s): build instance: %w", lineNum, item.ID, err)
		}

		cmp, err := executeSingleComparison(cmd.Context(), eng, tmpl, inst, engineA, engineB, modelA, modelB)
		if err != nil {
			return fmt.Errorf("line %d (%s): compare: %w", lineNum, item.ID, err)
		}

		if cmp.Agreement {
			totalAgree++
		}
		totalSpeedup += cmp.SpeedupFactor
		totalAms += cmp.EngineA.DurationMs
		totalBms += cmp.EngineB.DurationMs

		matchStr := "AGREE"
		if !cmp.Agreement {
			matchStr = "DIVERGE"
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "eval: [%02d] %s (%s) → %s | A: %.0fms, B: %.0fms (%.1fx)\n",
			lineNum, item.ID, item.Metric, matchStr, cmp.EngineA.DurationMs, cmp.EngineB.DurationMs, cmp.SpeedupFactor)

		tier := item.Tier
		if tier == "" {
			tier = "unclassified"
		}
		tr, ok := tierCounts[tier]
		if !ok {
			tr = &TierReport{}
			tierCounts[tier] = tr
		}
		tr.Total++
		if cmp.Agreement {
			tr.Agreements++
		}

		cases = append(cases, CompareCaseResult{
			ID:         item.ID,
			Tier:       item.Tier,
			Category:   item.Category,
			Expected:   item.Expected,
			Comparison: cmp,
		})
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read dataset: %w", err)
	}

	if len(cases) == 0 {
		return fmt.Errorf("dataset %q contains no valid evaluation cases", datasetPath)
	}

	n := len(cases)
	tierBreakdown := make(map[string]TierReport, len(tierCounts))
	for k, v := range tierCounts {
		if v.Total > 0 {
			v.AgreementPct = (float64(v.Agreements) / float64(v.Total)) * 100.0
		}
		tierBreakdown[k] = *v
	}

	report := CompareBatchReport{
		TotalCases:       n,
		Agreements:       totalAgree,
		AgreementPct:     (float64(totalAgree) / float64(n)) * 100.0,
		AvgSpeedupFactor: totalSpeedup / float64(n),
		EngineAAvgMs:     totalAms / float64(n),
		EngineBAvgMs:     totalBms / float64(n),
		TierBreakdown:    tierBreakdown,
		Cases:            cases,
	}

	if outputFile != "" {
		outBytes, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal report: %w", err)
		}
		if err := os.WriteFile(outputFile, outBytes, 0644); err != nil {
			return fmt.Errorf("write output file %q: %w", outputFile, err)
		}
	}

	if outputFormat == outputJSON {
		return printJSON(cmd.OutOrStdout(), report)
	}

	return renderBatchReportTable(cmd.OutOrStdout(), report, engineA, engineB)
}

func renderBatchReportTable(w io.Writer, rep CompareBatchReport, engineA, engineB string) error {
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "================================================================================")
	fmt.Fprintf(tw, "BATCH ENGINE COMPARISON REPORT: %s vs %s\n", strings.ToUpper(engineA), strings.ToUpper(engineB))
	fmt.Fprintln(tw, "================================================================================")
	fmt.Fprintf(tw, "Total Cases Evaluated:\t%d\n", rep.TotalCases)
	fmt.Fprintf(tw, "Overall Agreement:\t%d / %d (%.1f%%)\n", rep.Agreements, rep.TotalCases, rep.AgreementPct)
	fmt.Fprintf(tw, "Engine A Average Latency:\t%.1f ms\n", rep.EngineAAvgMs)
	fmt.Fprintf(tw, "Engine B Average Latency:\t%.1f ms\n", rep.EngineBAvgMs)
	fmt.Fprintf(tw, "Average Speedup Factor:\t%.1fx\n\n", rep.AvgSpeedupFactor)

	fmt.Fprintln(tw, "DIFFICULTY TIER BREAKDOWN:")
	fmt.Fprintln(tw, "TIER\tTOTAL\tAGREE\tAGREEMENT %")
	fmt.Fprintln(tw, "----\t-----\t-----\t-----------")

	tiers := make([]string, 0, len(rep.TierBreakdown))
	for t := range rep.TierBreakdown {
		tiers = append(tiers, t)
	}
	sort.Strings(tiers)
	for _, t := range tiers {
		tb := rep.TierBreakdown[t]
		fmt.Fprintf(tw, "%s\t%d\t%d\t%.1f%%\n", t, tb.Total, tb.Agreements, tb.AgreementPct)
	}

	var divergent []CompareCaseResult
	for _, c := range rep.Cases {
		if !c.Comparison.Agreement {
			divergent = append(divergent, c)
		}
	}

	if len(divergent) > 0 {
		fmt.Fprintf(tw, "\nDIVERGENT CASES (%d of %d):\n", len(divergent), rep.TotalCases)
		fmt.Fprintln(tw, "ID\tTIER\tKIND\tENGINE A\tENGINE B")
		fmt.Fprintln(tw, "--\t----\t----\t--------\t--------")
		for _, d := range divergent {
			valA, valB := "-", "-"
			if d.Comparison.EngineA.Passed != nil {
				if *d.Comparison.EngineA.Passed {
					valA = "PASS"
				} else {
					valA = "FAIL"
				}
			} else if d.Comparison.EngineA.Selection != "" {
				valA = d.Comparison.EngineA.Selection
			} else if d.Comparison.EngineA.Score != nil {
				valA = fmt.Sprintf("%.1f", *d.Comparison.EngineA.Score)
			}

			if d.Comparison.EngineB.Passed != nil {
				if *d.Comparison.EngineB.Passed {
					valB = "PASS"
				} else {
					valB = "FAIL"
				}
			} else if d.Comparison.EngineB.Selection != "" {
				valB = d.Comparison.EngineB.Selection
			} else if d.Comparison.EngineB.Score != nil {
				valB = fmt.Sprintf("%.1f", *d.Comparison.EngineB.Score)
			}

			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", d.ID, d.Tier, d.Comparison.Kind, valA, valB)
		}
	}

	return tw.Flush()
}

func checkAgreement(kind registry.MetricKind, a, b eval.Result) bool {
	switch kind {
	case registry.KindBoul:
		if a.Passed != nil && b.Passed != nil {
			return *a.Passed == *b.Passed
		}
		return false
	case registry.KindChoice:
		return a.ChoiceSelection != "" && a.ChoiceSelection == b.ChoiceSelection
	case registry.KindScore, registry.KindPointwise:
		if a.Score != nil && b.Score != nil {
			return math.Abs(float64(*a.Score-*b.Score)) <= 1.0
		}
		return false
	default:
		if a.Score != nil && b.Score != nil {
			return math.Abs(float64(*a.Score-*b.Score)) <= 1.0
		}
		return a.Explanation != "" && b.Explanation != ""
	}
}

func renderCompareTable(w io.Writer, cmp EngineCompareResult) error {
	tw := newTabWriter(w)
	fmt.Fprintf(tw, "Metric:\t%s (%s)\n", cmp.MetricID, cmp.Kind)
	agreeStr := "DIVERGENT (Mismatch)"
	if cmp.Agreement {
		agreeStr = "AGREEMENT (Match)"
	}
	fmt.Fprintf(tw, "Verdict Agreement:\t%s\n", agreeStr)
	fmt.Fprintf(tw, "Speedup Factor:\t%.1fx\n\n", cmp.SpeedupFactor)

	colA := strings.ToUpper(cmp.EngineA.Engine)
	colB := strings.ToUpper(cmp.EngineB.Engine)
	fmt.Fprintf(tw, "DIMENSION\tENGINE A (%s)\tENGINE B (%s)\n", colA, colB)
	fmt.Fprintln(tw, "---------\t-------------\t-------------")

	if cmp.EngineA.Passed != nil || cmp.EngineB.Passed != nil {
		pA, pB := "-", "-"
		if cmp.EngineA.Passed != nil {
			if *cmp.EngineA.Passed {
				pA = "PASS"
			} else {
				pA = "FAIL"
			}
		}
		if cmp.EngineB.Passed != nil {
			if *cmp.EngineB.Passed {
				pB = "PASS"
			} else {
				pB = "FAIL"
			}
		}
		fmt.Fprintf(tw, "Passed:\t%s\t%s\n", pA, pB)
	}

	if cmp.EngineA.Selection != "" || cmp.EngineB.Selection != "" {
		sA, sB := "-", "-"
		if cmp.EngineA.Selection != "" {
			sA = cmp.EngineA.Selection
		}
		if cmp.EngineB.Selection != "" {
			sB = cmp.EngineB.Selection
		}
		fmt.Fprintf(tw, "Selection:\t%s\t%s\n", sA, sB)
	}

	if cmp.EngineA.Score != nil || cmp.EngineB.Score != nil {
		sA, sB := "-", "-"
		if cmp.EngineA.Score != nil {
			sA = fmt.Sprintf("%.2f", *cmp.EngineA.Score)
		}
		if cmp.EngineB.Score != nil {
			sB = fmt.Sprintf("%.2f", *cmp.EngineB.Score)
		}
		fmt.Fprintf(tw, "Score:\t%s\t%s\n", sA, sB)
	}

	if cmp.EngineA.Confidence != nil || cmp.EngineB.Confidence != nil {
		cA, cB := "-", "-"
		if cmp.EngineA.Confidence != nil {
			cA = fmt.Sprintf("%.1f%%", *cmp.EngineA.Confidence*100)
		}
		if cmp.EngineB.Confidence != nil {
			cB = fmt.Sprintf("%.1f%%", *cmp.EngineB.Confidence*100)
			if stderr, ok := cmp.EngineB.Custom["stderr"].(float64); ok && stderr > 0 {
				cB += fmt.Sprintf(" (stderr: ±%.4f)", stderr)
			}
		}
		fmt.Fprintf(tw, "Confidence:\t%s\t%s\n", cA, cB)
	}

	fmt.Fprintf(tw, "Duration:\t%.1f ms\t%.1f ms\n", cmp.EngineA.DurationMs, cmp.EngineB.DurationMs)
	explA := sanitizeCell(firstLine(cmp.EngineA.Explanation))
	explB := sanitizeCell(firstLine(cmp.EngineB.Explanation))
	if explA == "" {
		explA = "-"
	}
	if explB == "" {
		explB = "-"
	}
	fmt.Fprintf(tw, "Explanation:\t%s\t%s\n", explA, explB)

	return tw.Flush()
}
