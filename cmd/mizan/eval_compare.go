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
	"fmt"
	"io"
	"math"
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

func newEvalCompareEnginesCmd() *cobra.Command {
	var (
		metric            string
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
		Use:   "compare-engines --metric <id> [--field key=value] [--file key=/path] [--engine-a vertex] [--engine-b diffusion]",
		Short: "Compare evaluation engines side-by-side in parallel (e.g. Vertex AI vs DiffusionGemma)",
		Long: "Execute an evaluation template across two evaluation engines concurrently\n" +
			"and compare their verdicts, latency, confidence, and speedup factor.\n\n" +
			"Default compares Engine A (Vertex AI Gemini) with Engine B (DiffusionGemma).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if metric == "" {
				return fmt.Errorf("--metric is required")
			}
			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			if diffusionEndpoint != "" {
				cfg.DiffusionEndpoint = diffusionEndpoint
			}
			applyProjectOverride(cfg, projectOverride)

			svc, closeSvc, err := wire.OpenService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			tmpl, err := svc.Get(cmd.Context(), metric)
			if err != nil {
				return err
			}

			inst, err := buildInstance(fields, files, gcs)
			if err != nil {
				return err
			}

			eng, closeEng, err := openEngine(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeEng() }()

			return runCompareEngines(cmd, cfg, eng, tmpl, inst, engineA, engineB, modelA, modelB, noStore)
		},
	}
	cmd.Flags().StringVar(&metric, "metric", "", "template id (<namespace>/<slug>) (required)")
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
	var (
		resA eval.Result
		resB eval.Result
		durA time.Duration
		durB time.Duration
	)

	g, ctx := errgroup.WithContext(cmd.Context())

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
		resA, err = eng.Run(ctx, *tmpl, inst, opts...)
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
		resB, err = eng.Run(ctx, *tmpl, inst, opts...)
		durB = time.Since(start)
		return err
	})

	if err := g.Wait(); err != nil {
		return fmt.Errorf("eval comparison failed: %w", err)
	}

	agreement := checkAgreement(tmpl.Kind, resA, resB)
	var speedup float64
	if durB > 0 {
		speedup = float64(durA) / float64(durB)
	}

	cmp := EngineCompareResult{
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
	}

	if outputFormat == outputJSON {
		return printJSON(cmd.OutOrStdout(), cmp)
	}

	return renderCompareTable(cmd.OutOrStdout(), cmp)
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
