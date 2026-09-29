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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/wire"
)

// compareReportSchemaVersion is bumped whenever the batch report shape changes.
// v1 (2026-09-19) reported inter-engine agreement only; v2 scores each engine
// against the gold label first.
const compareReportSchemaVersion = 2

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
	Error       string         `json:"error,omitempty"`

	// Gold-label scoring (dataset mode only).
	Prediction string `json:"prediction,omitempty"`
	Correct    *bool  `json:"correct,omitempty"`
}

func (r EngineRun) ok() bool { return r.Error == "" }

// CompareDatasetItem represents one item in a JSONL benchmark dataset.
// Expected may be a JSON string, number or boolean; it is normalized to a
// string. Kind-specific interpretation:
//   - boul: PASS/FAIL, true/false, yes/no
//   - choice: the option name
//   - score / pointwise / rubric: a number on the template's scale
//   - pairwise: BASELINE/CANDIDATE/TIE (A/B accepted)
type CompareDatasetItem struct {
	ID       string            `json:"id"`
	Metric   string            `json:"metric"`
	Tier     string            `json:"tier,omitempty"`
	Category string            `json:"category,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
	Files    map[string]string `json:"files,omitempty"`
	GCS      map[string]string `json:"gcs,omitempty"`
	Expected string            `json:"-"`
	Source   string            `json:"source,omitempty"`
}

// UnmarshalJSON normalizes Expected from string, number or bool.
func (c *CompareDatasetItem) UnmarshalJSON(b []byte) error {
	type alias CompareDatasetItem
	var raw struct {
		alias
		Expected json.RawMessage `json:"expected"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*c = CompareDatasetItem(raw.alias)
	c.Expected = normalizeExpected(raw.Expected)
	return nil
}

func normalizeExpected(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(string(raw))
}

// CompareCaseResult holds the individual comparison result for a dataset item.
type CompareCaseResult struct {
	ID         string              `json:"id"`
	Tier       string              `json:"tier,omitempty"`
	Category   string              `json:"category,omitempty"`
	Expected   string              `json:"expected,omitempty"`
	Comparison EngineCompareResult `json:"comparison"`
}

// CompareRunMeta records everything needed to reproduce or audit a run.
type CompareRunMeta struct {
	Tool              string    `json:"tool"`
	Revision          string    `json:"revision,omitempty"`
	RevisionModified  bool      `json:"revision_modified,omitempty"`
	GoVersion         string    `json:"go_version"`
	Platform          string    `json:"platform"`
	StartedAt         time.Time `json:"started_at"`
	FinishedAt        time.Time `json:"finished_at"`
	Dataset           string    `json:"dataset"`
	DatasetSHA256     string    `json:"dataset_sha256"`
	EngineA           string    `json:"engine_a"`
	EngineB           string    `json:"engine_b"`
	ModelA            string    `json:"model_a,omitempty"`
	ModelB            string    `json:"model_b,omitempty"`
	DiffusionEndpoint string    `json:"diffusion_endpoint,omitempty"`
	DiffusionBackend  string    `json:"diffusion_backend,omitempty"`
	DiffusionAuth     string    `json:"diffusion_auth,omitempty"`
	DiffusionSamples  int       `json:"diffusion_samples,omitempty"`
	DiffusionMirror   bool      `json:"diffusion_mirror,omitempty"`
	MirrorSide        string    `json:"diffusion_mirror_side,omitempty"`
	AllowFallback     bool      `json:"allow_diffusion_fallback,omitempty"`
	Workers           int       `json:"workers"`
	Warmup            int       `json:"warmup"`
	Serial            bool      `json:"serial_engines"`
	ScoreTolerance    float64   `json:"score_tolerance"`
	BootstrapIters    int       `json:"bootstrap_iters"`
	BootstrapSeed     int64     `json:"bootstrap_seed"`
	Notes             string    `json:"notes,omitempty"`
}

// EngineSummary aggregates one engine's performance over a batch.
type EngineSummary struct {
	Engine         string         `json:"engine"`
	Models         map[string]int `json:"models,omitempty"`
	Cases          int            `json:"cases"`
	Errors         int            `json:"errors"`
	Scored         int            `json:"scored"` // gold present and no error
	Correct        int            `json:"correct"`
	Accuracy       *float64       `json:"accuracy,omitempty"`
	AccuracyCI95   *Interval      `json:"accuracy_ci95,omitempty"`
	LatencyP50Ms   float64        `json:"latency_p50_ms"`
	LatencyP95Ms   float64        `json:"latency_p95_ms"`
	LatencyMeanMs  float64        `json:"latency_mean_ms"`
	ServerP50Ms    *float64       `json:"server_p50_ms,omitempty"`
	ReadoutModes   map[string]int `json:"readout_modes,omitempty"`
	BackendsUsed   map[string]int `json:"backends_used,omitempty"`
	Calibration    *CalibSummary  `json:"calibration,omitempty"`
	ScoreMetrics   *ScoreSummary  `json:"score_metrics,omitempty"`
	PositionConsis *float64       `json:"pairwise_position_consistency,omitempty"`
	ErrorSamples   []string       `json:"error_samples,omitempty"`
}

// CalibSummary reports top-1 calibration for cases where the engine reported a confidence.
type CalibSummary struct {
	N     int      `json:"n"`
	ECE10 *float64 `json:"ece_10bin"`
	Brier *float64 `json:"brier_top1"`
}

// ScoreSummary reports Likert agreement with gold for score-like kinds.
type ScoreSummary struct {
	N        int      `json:"n"`
	MAE      *float64 `json:"mae"`
	Spearman *float64 `json:"spearman"`
	Pearson  *float64 `json:"pearson"`
}

// PairedSummary compares the two engines on cases both scored.
type PairedSummary struct {
	N             int       `json:"n"`
	Agreements    int       `json:"agreements"`
	AgreementPct  float64   `json:"agreement_pct"`
	Kappa         *float64  `json:"cohen_kappa,omitempty"`
	BothCorrect   int       `json:"both_correct"`
	OnlyACorrect  int       `json:"only_a_correct"`
	OnlyBCorrect  int       `json:"only_b_correct"`
	BothWrong     int       `json:"both_wrong"`
	McNemarP      *float64  `json:"mcnemar_exact_p,omitempty"`
	AccDiffAminB  *float64  `json:"accuracy_diff_a_minus_b,omitempty"`
	AccDiffCI95   *Interval `json:"accuracy_diff_ci95,omitempty"`
	MedianSpeedup *float64  `json:"median_latency_ratio_a_over_b,omitempty"`
}

// GroupSummary is accuracy per engine within a kind / tier / category slice.
type GroupSummary struct {
	N        int      `json:"n"`
	ACorrect int      `json:"a_correct"`
	BCorrect int      `json:"b_correct"`
	AScored  int      `json:"a_scored"`
	BScored  int      `json:"b_scored"`
	AAcc     *float64 `json:"a_accuracy,omitempty"`
	BAcc     *float64 `json:"b_accuracy,omitempty"`
}

// CompareBatchReport summarizes a batch cross-engine benchmark (schema v2).
type CompareBatchReport struct {
	SchemaVersion int                     `json:"schema_version"`
	Meta          CompareRunMeta          `json:"meta"`
	TotalCases    int                     `json:"total_cases"`
	EngineA       EngineSummary           `json:"engine_a"`
	EngineB       EngineSummary           `json:"engine_b"`
	Paired        PairedSummary           `json:"paired"`
	ByKind        map[string]GroupSummary `json:"by_kind,omitempty"`
	ByTier        map[string]GroupSummary `json:"by_tier,omitempty"`
	ByCategory    map[string]GroupSummary `json:"by_category,omitempty"`
	Cases         []CompareCaseResult     `json:"cases"`
}

type compareOptions struct {
	engineA, engineB string
	modelA, modelB   string
	samples          int
	mirror           bool
	mirrorSide       string // a | b | both
	allowFallback    bool
	serial           bool
	workers          int
	scoreTolerance   float64
	bootstrapIters   int
	bootstrapSeed    int64
	limit            int
	warmup           int
	noStore          bool
	failFast         bool
	redactEndpoints  bool
	notes            string
}

func newEvalCompareEnginesCmd() *cobra.Command {
	var (
		metric            string
		datasetPath       string
		outputFile        string
		diffusionEndpoint string
		diffusionBackend  string
		diffusionAuth     string
		diffusionTimeout  string
		fields            []string
		files             []string
		gcs               []string
		o                 compareOptions
	)
	cmd := &cobra.Command{
		Use:   "compare-engines (--metric <id> | --dataset <path.jsonl>) [--field key=value] [--file key=/path] [--engine-a vertex] [--engine-b diffusion]",
		Short: "Compare evaluation engines side-by-side (e.g. Vertex AI Gemini vs DiffusionGemma)",
		Long: "Execute an evaluation template on two engines and compare their verdicts, latency and confidence.\n\n" +
			"With --dataset <path.jsonl>, each engine is scored against the item's \"expected\" gold label\n" +
			"(accuracy with a bootstrap 95% CI, calibration, Likert MAE/Spearman), then the engines are\n" +
			"compared with each other (paired McNemar test, Cohen's kappa, agreement). Inter-engine agreement\n" +
			"is secondary: two engines that are both wrong agree.\n\n" +
			"Diffusion results must come from a structured-readout envelope; a free-form JSON reply (e.g.\n" +
			"from raw vLLM) is an error unless --allow-diffusion-fallback is set.",
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
			if diffusionBackend != "" {
				cfg.DiffusionBackend = diffusionBackend
			}
			if diffusionAuth != "" {
				cfg.DiffusionAuth = diffusionAuth
			}
			if diffusionTimeout != "" {
				cfg.DiffusionTimeout = diffusionTimeout
			}
			if o.modelB != "" && isDiffusionEngine(o.engineB) {
				cfg.DiffusionModel = o.modelB
			} else if o.modelA != "" && isDiffusionEngine(o.engineA) {
				cfg.DiffusionModel = o.modelA
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
				return runCompareDataset(cmd, cfg, eng, svc, datasetPath, outputFile, o)
			}

			tmpl, err := svc.Get(cmd.Context(), metric)
			if err != nil {
				return err
			}
			inst, err := buildInstance(fields, files, gcs)
			if err != nil {
				return err
			}
			cmp := executeSingleComparison(cmd.Context(), eng, tmpl, inst, o)
			if !o.noStore {
				storeComparison(cmd, cfg, tmpl, inst, cmp, o, &sync.Mutex{})
			}
			if outputFormat == outputJSON {
				return printJSON(cmd.OutOrStdout(), cmp)
			}
			return renderCompareTable(cmd.OutOrStdout(), cmp)
		},
	}
	f := cmd.Flags()
	f.StringVar(&metric, "metric", "", "template id (<namespace>/<slug>) (mutually exclusive with --dataset)")
	f.StringVar(&datasetPath, "dataset", "", "path to JSONL evaluation dataset to compare in batch")
	f.StringVar(&outputFile, "output-file", "", "optional path to save the structured JSON benchmark report")
	f.StringVar(&o.engineA, "engine-a", "vertex", "engine A: vertex (or genai), diffusion, or local (kind:computation only; credential-free)")
	f.StringVar(&o.engineB, "engine-b", "diffusion", "engine B: vertex, diffusion, or local (kind:computation only; credential-free)")
	f.StringVar(&o.modelA, "model-a", "", "model override for engine A")
	f.StringVar(&o.modelB, "model-b", "", "model override for engine B")
	f.StringVar(&diffusionEndpoint, "diffusion-endpoint", "", "override DiffusionGemma endpoint (structured server, dgem gateway, or Vertex dedicated endpoint URL)")
	f.StringVar(&diffusionBackend, "diffusion-backend", "", "X-DGem-Backend for a dgem gateway: vertex | cloudrun | vertex_first (pin one backend for benchmarks)")
	f.StringVar(&diffusionAuth, "diffusion-auth", "", "diffusion auth: auto | none | access-token | id-token (default from config: auto)")
	f.StringVar(&diffusionTimeout, "diffusion-timeout", "", "per-request diffusion timeout (Go duration, default 120s)")
	f.IntVar(&o.samples, "diffusion-samples", 0, "noise draws averaged per question on the diffusion engine (0 = server default)")
	f.BoolVar(&o.mirror, "diffusion-mirror", false, "pairwise: also read with baseline/candidate swapped and average (position-bias control)")
	f.StringVar(&o.mirrorSide, "diffusion-mirror-side", "both", "which diffusion engine gets --diffusion-mirror: a | b | both (lets one run compare mirror vs no mirror)")
	f.BoolVar(&o.allowFallback, "allow-diffusion-fallback", false, "accept diffusion replies without a structured readout envelope (token-logprob approximations)")
	f.BoolVar(&o.serial, "serial", false, "run engine A then engine B per case instead of concurrently (cleaner latency)")
	f.IntVar(&o.workers, "workers", 4, "cases evaluated concurrently (dataset mode)")
	f.Float64Var(&o.scoreTolerance, "score-tolerance", 0.5, "score/rubric cases count as correct when |prediction - expected| <= tolerance")
	f.IntVar(&o.bootstrapIters, "bootstrap", 2000, "bootstrap resamples for 95% confidence intervals (0 disables)")
	f.Int64Var(&o.bootstrapSeed, "seed", 20260925, "bootstrap RNG seed")
	f.IntVar(&o.limit, "limit", 0, "evaluate at most N dataset items (0 = all)")
	f.IntVar(&o.warmup, "warmup", 1, "untimed warm-up comparisons on the first item before the batch (token minting, connection setup, cold replicas)")
	f.BoolVar(&o.failFast, "fail-fast", false, "abort the batch on the first engine error (default: record the error and continue)")
	f.BoolVar(&o.redactEndpoints, "redact-endpoints", true, "replace endpoint hosts with their kind (vertex-dedicated, gateway, cloudrun, local) in the report")
	f.StringVar(&o.notes, "notes", "", "free-form note recorded in the report metadata")
	f.StringArrayVar(&fields, "field", nil, "instance field as key=value (repeatable)")
	f.StringArrayVar(&files, "file", nil, "local asset as key=/path (repeatable)")
	f.StringArrayVar(&gcs, "gcs", nil, "pre-staged asset as key=gs://... (repeatable)")
	f.BoolVar(&o.noStore, "no-store", false, "do not persist per-engine results to the eval results store")

	return cmd
}

func isDiffusionEngine(e string) bool {
	e = strings.ToLower(strings.TrimSpace(e))
	return e == "diffusion" || e == "diffgemma"
}

// isLocalEngine reports whether e selects the credential-free local engine
// (kind:computation's default: pure Go, no model, no network).
func isLocalEngine(e string) bool {
	return strings.ToLower(strings.TrimSpace(e)) == "local"
}

func runOne(ctx context.Context, eng *eval.Engine, tmpl *registry.MetricTemplate, inst eval.Instance, side, engine, model string, o compareOptions) (eval.Result, EngineRun) {
	opts := []eval.RunOption{eval.WithEngine(engine)}
	if model != "" {
		opts = append(opts, eval.WithModel(model))
	}
	// The local engine takes no model-engine knobs: no diffusion sampling/mirror
	// and no rubric-detail routing (it serves only model-free kinds).
	switch {
	case isLocalEngine(engine):
		// no extra options
	case isDiffusionEngine(engine):
		mirror := o.mirror && (o.mirrorSide == "" || o.mirrorSide == "both" || o.mirrorSide == side)
		opts = append(opts, eval.WithDiffusionSamples(o.samples), eval.WithDiffusionFallback(o.allowFallback), eval.WithDiffusionMirror(mirror))
	case tmpl.Kind == registry.KindRubric:
		opts = append(opts, eval.WithRubricDetailDefaultScale())
	}
	start := time.Now()
	res, err := eng.Run(ctx, *tmpl, inst, opts...)
	dur := time.Since(start)
	run := EngineRun{
		Engine:      engine,
		Model:       model,
		Passed:      res.Passed,
		Score:       res.Score,
		Confidence:  res.Confidence,
		Selection:   res.ChoiceSelection,
		Explanation: res.Explanation,
		DurationMs:  float64(dur.Microseconds()) / 1000,
		Custom:      res.CustomOutput,
	}
	if res.PairwiseChoice != "" {
		run.Selection = res.PairwiseChoice
	}
	if res.Applied != nil && res.Applied.Model != "" {
		run.Model = res.Applied.Model
	}
	if err != nil {
		run.Error = err.Error()
	}
	if o.redactEndpoints && run.Custom != nil {
		if ep, ok := run.Custom["endpoint"].(string); ok {
			run.Custom["endpoint"] = endpointKind(ep)
		}
	}
	return res, run
}

func executeSingleComparison(ctx context.Context, eng *eval.Engine, tmpl *registry.MetricTemplate, inst eval.Instance, o compareOptions) EngineCompareResult {
	var (
		resA, resB eval.Result
		runA, runB EngineRun
	)
	if o.serial {
		resA, runA = runOne(ctx, eng, tmpl, inst, "a", o.engineA, o.modelA, o)
		resB, runB = runOne(ctx, eng, tmpl, inst, "b", o.engineB, o.modelB, o)
	} else {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); resA, runA = runOne(ctx, eng, tmpl, inst, "a", o.engineA, o.modelA, o) }()
		go func() { defer wg.Done(); resB, runB = runOne(ctx, eng, tmpl, inst, "b", o.engineB, o.modelB, o) }()
		wg.Wait()
	}

	cmp := EngineCompareResult{
		MetricID: tmpl.ID,
		Kind:     string(tmpl.Kind),
		EngineA:  runA,
		EngineB:  runB,
	}
	if runA.ok() && runB.ok() {
		cmp.Agreement = checkAgreement(tmpl.Kind, resA, resB, o.scoreTolerance)
		if runB.DurationMs > 0 {
			cmp.SpeedupFactor = runA.DurationMs / runB.DurationMs
		}
	}
	return cmp
}

func runCompareDataset(cmd *cobra.Command, cfg *config.Config, eng *eval.Engine, svc *registry.Service, datasetPath, outputFile string, o compareOptions) error {
	raw, err := os.ReadFile(datasetPath)
	if err != nil {
		return fmt.Errorf("open dataset %q: %w", datasetPath, err)
	}
	sum := sha256.Sum256(raw)

	var items []CompareDatasetItem
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
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
		if item.ID == "" {
			item.ID = fmt.Sprintf("line-%d", lineNum)
		}
		items = append(items, item)
		if o.limit > 0 && len(items) >= o.limit {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read dataset: %w", err)
	}
	if len(items) == 0 {
		return fmt.Errorf("dataset %q contains no valid evaluation cases", datasetPath)
	}

	// Resolve every template up front so a typo fails before any spend.
	tmplCache := make(map[string]*registry.MetricTemplate)
	for _, it := range items {
		if _, ok := tmplCache[it.Metric]; ok {
			continue
		}
		t, err := svc.Get(cmd.Context(), it.Metric)
		if err != nil {
			return fmt.Errorf("item %s: get template %q: %w", it.ID, it.Metric, err)
		}
		tmplCache[it.Metric] = t
	}

	meta := CompareRunMeta{
		Tool:             "mizan eval compare-engines",
		GoVersion:        runtime.Version(),
		Platform:         runtime.GOOS + "/" + runtime.GOARCH,
		StartedAt:        time.Now().UTC(),
		Dataset:          datasetPath,
		DatasetSHA256:    hex.EncodeToString(sum[:]),
		EngineA:          o.engineA,
		EngineB:          o.engineB,
		ModelA:           o.modelA,
		ModelB:           o.modelB,
		DiffusionSamples: o.samples,
		DiffusionMirror:  o.mirror,
		MirrorSide:       o.mirrorSide,
		AllowFallback:    o.allowFallback,
		Workers:          o.workers,
		Serial:           o.serial,
		ScoreTolerance:   o.scoreTolerance,
		BootstrapIters:   o.bootstrapIters,
		BootstrapSeed:    o.bootstrapSeed,
		Notes:            o.notes,
	}
	if isDiffusionEngine(o.engineA) || isDiffusionEngine(o.engineB) {
		meta.DiffusionEndpoint = cfg.DiffusionEndpoint
		if o.redactEndpoints {
			meta.DiffusionEndpoint = endpointKind(cfg.DiffusionEndpoint)
		}
		meta.DiffusionBackend = cfg.DiffusionBackend
		meta.DiffusionAuth = cfg.DiffusionAuth
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				meta.Revision = s.Value
			case "vcs.modified":
				meta.RevisionModified = s.Value == "true"
			}
		}
	}

	if o.warmup > 0 {
		wt := tmplCache[items[0].Metric]
		var fa, fi, gc []string
		for k, v := range items[0].Fields {
			fa = append(fa, k+"="+v)
		}
		for k, v := range items[0].Files {
			fi = append(fi, k+"="+v)
		}
		for k, v := range items[0].GCS {
			gc = append(gc, k+"="+v)
		}
		if inst, err := buildInstance(fa, fi, gc); err == nil {
			for w := 0; w < o.warmup; w++ {
				wc := executeSingleComparison(cmd.Context(), eng, wt, inst, o)
				fmt.Fprintf(cmd.ErrOrStderr(), "eval: warm-up %d: A %.0fms %s | B %.0fms %s\n", w+1, wc.EngineA.DurationMs, firstLine(wc.EngineA.Error), wc.EngineB.DurationMs, firstLine(wc.EngineB.Error))
			}
		}
	}
	meta.Warmup = o.warmup

	workers := o.workers
	if workers < 1 {
		workers = 1
	}
	cases := make([]CompareCaseResult, len(items))
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		storeMu  sync.Mutex
		firstErr error
		done     int
	)
	sem := make(chan struct{}, workers)
	for i := range items {
		it := items[i]
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			tmpl := tmplCache[it.Metric]
			var fieldArgs, fileArgs, gcsArgs []string
			for k, v := range it.Fields {
				fieldArgs = append(fieldArgs, k+"="+v)
			}
			for k, v := range it.Files {
				fileArgs = append(fileArgs, k+"="+v)
			}
			for k, v := range it.GCS {
				gcsArgs = append(gcsArgs, k+"="+v)
			}
			var cmp EngineCompareResult
			inst, err := buildInstance(fieldArgs, fileArgs, gcsArgs)
			if err != nil {
				cmp = EngineCompareResult{MetricID: it.Metric, Kind: string(tmpl.Kind),
					EngineA: EngineRun{Engine: o.engineA, Error: err.Error()}, EngineB: EngineRun{Engine: o.engineB, Error: err.Error()}}
			} else {
				cmp = executeSingleComparison(ctx, eng, tmpl, inst, o)
				if !o.noStore {
					storeComparison(cmd, cfg, tmpl, inst, cmp, o, &storeMu)
				}
			}
			scoreRun(&cmp.EngineA, tmpl.Kind, it.Expected, o.scoreTolerance)
			scoreRun(&cmp.EngineB, tmpl.Kind, it.Expected, o.scoreTolerance)
			cases[i] = CompareCaseResult{ID: it.ID, Tier: it.Tier, Category: it.Category, Expected: it.Expected, Comparison: cmp}

			mu.Lock()
			defer mu.Unlock()
			done++
			fmt.Fprintf(cmd.ErrOrStderr(), "eval: [%d/%d] %s (%s) A=%s B=%s | A %.0fms, B %.0fms\n",
				done, len(items), it.ID, it.Metric, verdictMark(cmp.EngineA), verdictMark(cmp.EngineB), cmp.EngineA.DurationMs, cmp.EngineB.DurationMs)
			if o.failFast && firstErr == nil && (!cmp.EngineA.ok() || !cmp.EngineB.ok()) {
				firstErr = fmt.Errorf("item %s: A: %s B: %s", it.ID, cmp.EngineA.Error, cmp.EngineB.Error)
				cancel()
			}
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}

	meta.FinishedAt = time.Now().UTC()
	report := buildBatchReport(meta, cases, o)

	if outputFile != "" {
		outBytes, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal report: %w", err)
		}
		if err := os.WriteFile(outputFile, outBytes, 0o644); err != nil { //nolint:gosec // G306: a benchmark report meant to be shared/committed, not a secret
			return fmt.Errorf("write output file %q: %w", outputFile, err)
		}
	}
	if outputFormat == outputJSON {
		return printJSON(cmd.OutOrStdout(), report)
	}
	return renderBatchReportTable(cmd.OutOrStdout(), report)
}

func verdictMark(r EngineRun) string {
	switch {
	case !r.ok():
		return "ERR"
	case r.Correct == nil:
		return r.Prediction
	case *r.Correct:
		return "ok(" + r.Prediction + ")"
	default:
		return "MISS(" + r.Prediction + ")"
	}
}

// storeComparison persists each engine's successful result (serialized: the
// SQLite store is single-writer).
func storeComparison(cmd *cobra.Command, cfg *config.Config, tmpl *registry.MetricTemplate, inst eval.Instance, cmp EngineCompareResult, _ compareOptions, mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	for _, r := range []EngineRun{cmp.EngineA, cmp.EngineB} {
		if !r.ok() {
			continue
		}
		res := eval.Result{Passed: r.Passed, Score: r.Score, Confidence: r.Confidence, ChoiceSelection: r.Selection, Explanation: r.Explanation, CustomOutput: r.Custom}
		res.Applied = &eval.AppliedAutorater{Model: r.Model, EffectiveHost: r.Engine}
		storeResult(cmd, cfg, "eval compare-engines", *tmpl, inst, res, storeHookOpts{NoHostLabel: true})
	}
}

// endpointKind maps an endpoint URL to a coarse, non-identifying label.
func endpointKind(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return "unknown"
	}
	h := strings.ToLower(p.Hostname())
	switch {
	case strings.HasSuffix(h, ".prediction.vertexai.goog") || strings.Contains(p.Path, "/invoke"):
		return "vertex-dedicated"
	case h == "localhost" || h == "127.0.0.1" || h == "::1":
		return "local"
	case strings.HasSuffix(h, ".run.app") && strings.Contains(h, "gateway"):
		return "gateway"
	case strings.HasSuffix(h, ".run.app"):
		return "cloudrun"
	default:
		return "remote"
	}
}

// predictionString renders an engine result as the string compared with gold.
func predictionString(kind registry.MetricKind, r EngineRun) string {
	switch kind {
	case registry.KindBoul:
		if r.Passed != nil {
			if *r.Passed {
				return "PASS"
			}
			return "FAIL"
		}
	case registry.KindChoice, registry.KindPairwise:
		return r.Selection
	default:
		// A pairwise_* prebuilt metric returns a preference, not a score.
		if registry.IsNativeKind(kind) && r.Selection != "" && r.Score == nil {
			return r.Selection
		}
		if r.Score != nil {
			return strconv.FormatFloat(float64(*r.Score), 'f', 2, 64)
		}
	}
	return ""
}

// scoreRun fills Prediction and, when a gold label is present, Correct.
func scoreRun(r *EngineRun, kind registry.MetricKind, expected string, tol float64) {
	if !r.ok() {
		return
	}
	r.Prediction = predictionString(kind, *r)
	if expected == "" {
		return
	}
	var correct bool
	switch kind {
	case registry.KindBoul:
		want, ok := parseBoolLabel(expected)
		if !ok || r.Passed == nil {
			return
		}
		correct = *r.Passed == want
	case registry.KindChoice:
		correct = strings.EqualFold(strings.TrimSpace(r.Selection), expected)
	case registry.KindPairwise:
		correct = normalizePairLabel(r.Selection) == normalizePairLabel(expected)
	case registry.KindComputation, registry.KindPrebuilt:
		c, ok := scoreNativeRun(*r, expected, tol)
		if !ok {
			return
		}
		correct = c
	default:
		want, err := strconv.ParseFloat(expected, 64)
		if err != nil || r.Score == nil {
			return
		}
		correct = math.Abs(float64(*r.Score)-want) <= tol+1e-9
	}
	r.Correct = &correct
}

// scoreNativeRun grades a kind:computation / kind:prebuilt run against a gold
// label. They are scored like score kinds — a numeric expected value counts as
// correct within the score tolerance — with two refinements:
//
//   - a verdict label (PASS/FAIL/true/false/yes/no) is compared against
//     Result.Passed as a boolean, when the template's passThreshold produced one
//     (a bare "1"/"0" stays numeric: it is a legitimate score for exact_match and
//     the trajectory/tool metrics);
//   - a pairwise_* prebuilt run (a Selection, no score) is compared as a
//     BASELINE/CANDIDATE/TIE preference.
//
// ok is false when the label and the run cannot be compared.
func scoreNativeRun(r EngineRun, expected string, tol float64) (correct, ok bool) {
	if want, isVerdict := parseVerdictLabel(expected); isVerdict {
		if r.Passed == nil {
			return false, false
		}
		return *r.Passed == want, true
	}
	if r.Score == nil && r.Selection != "" {
		return normalizePairLabel(r.Selection) == normalizePairLabel(expected), true
	}
	want, err := strconv.ParseFloat(expected, 64)
	if err != nil || r.Score == nil {
		return false, false
	}
	return math.Abs(float64(*r.Score)-want) <= tol+1e-9, true
}

// parseVerdictLabel recognizes a textual verdict label (NOT "1"/"0", which are
// numeric scores for the native kinds).
func parseVerdictLabel(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pass", "true", "yes":
		return true, true
	case "fail", "false", "no":
		return false, true
	}
	return false, false
}

func parseBoolLabel(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pass", "true", "yes", "1":
		return true, true
	case "fail", "false", "no", "0":
		return false, true
	}
	return false, false
}

func normalizePairLabel(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "BASELINE", "A", "RESPONSE_A", "MODEL_A":
		return "BASELINE"
	case "CANDIDATE", "B", "RESPONSE_B", "MODEL_B":
		return "CANDIDATE"
	case "TIE", "EQUAL", "TIE (BOTHGOOD)", "TIE (BOTHBAD)":
		return "TIE"
	}
	return strings.ToUpper(strings.TrimSpace(s))
}

func buildBatchReport(meta CompareRunMeta, cases []CompareCaseResult, o compareOptions) CompareBatchReport {
	rep := CompareBatchReport{
		SchemaVersion: compareReportSchemaVersion,
		Meta:          meta,
		TotalCases:    len(cases),
		ByKind:        map[string]GroupSummary{},
		ByTier:        map[string]GroupSummary{},
		ByCategory:    map[string]GroupSummary{},
		Cases:         cases,
	}
	rep.EngineA = summarizeEngine(o.engineA, cases, func(c CompareCaseResult) EngineRun { return c.Comparison.EngineA }, o)
	rep.EngineB = summarizeEngine(o.engineB, cases, func(c CompareCaseResult) EngineRun { return c.Comparison.EngineB }, o)

	var (
		p              PairedSummary
		predA, predB   []string
		corrA, corrB   []float64
		latA, latB     []float64
		latRatioSource int
	)
	for _, c := range cases {
		a, b := c.Comparison.EngineA, c.Comparison.EngineB
		addGroup(rep.ByKind, c.Comparison.Kind, a, b)
		addGroup(rep.ByTier, orDefault(c.Tier, "unclassified"), a, b)
		addGroup(rep.ByCategory, orDefault(c.Category, "unclassified"), a, b)
		if !a.ok() || !b.ok() {
			continue
		}
		latA = append(latA, a.DurationMs)
		latB = append(latB, b.DurationMs)
		latRatioSource++
		predA = append(predA, a.Prediction)
		predB = append(predB, b.Prediction)
		p.N++
		if c.Comparison.Agreement {
			p.Agreements++
		}
		if a.Correct != nil && b.Correct != nil {
			ca, cb := boolF(*a.Correct), boolF(*b.Correct)
			corrA = append(corrA, ca)
			corrB = append(corrB, cb)
			switch {
			case *a.Correct && *b.Correct:
				p.BothCorrect++
			case *a.Correct:
				p.OnlyACorrect++
			case *b.Correct:
				p.OnlyBCorrect++
			default:
				p.BothWrong++
			}
		}
	}
	if p.N > 0 {
		p.AgreementPct = 100 * float64(p.Agreements) / float64(p.N)
		p.Kappa = finite(cohenKappa(predA, predB))
	}
	if len(corrA) > 0 {
		p.McNemarP = finite(mcnemarExact(p.OnlyACorrect, p.OnlyBCorrect))
		var d float64
		for i := range corrA {
			d += corrA[i] - corrB[i]
		}
		p.AccDiffAminB = finite(d / float64(len(corrA)))
		if o.bootstrapIters > 0 {
			ci := bootstrapDiffCI(corrA, corrB, o.bootstrapIters, o.bootstrapSeed)
			p.AccDiffCI95 = &ci
		}
	}
	if latRatioSource > 0 {
		if mb := percentile(latB, 0.5); mb > 0 {
			p.MedianSpeedup = finite(percentile(latA, 0.5) / mb)
		}
	}
	rep.Paired = p
	for k, g := range rep.ByKind {
		rep.ByKind[k] = finishGroup(g)
	}
	for k, g := range rep.ByTier {
		rep.ByTier[k] = finishGroup(g)
	}
	for k, g := range rep.ByCategory {
		rep.ByCategory[k] = finishGroup(g)
	}
	return rep
}

func summarizeEngine(name string, cases []CompareCaseResult, pick func(CompareCaseResult) EngineRun, o compareOptions) EngineSummary {
	s := EngineSummary{Engine: name, Models: map[string]int{}, ReadoutModes: map[string]int{}, BackendsUsed: map[string]int{}}
	var (
		lat, server        []float64
		correct            []float64
		conf               []float64
		confCorrect        []bool
		predNum, goldNum   []float64
		posN, posConsisent int
	)
	for _, c := range cases {
		r := pick(c)
		s.Cases++
		if !r.ok() {
			s.Errors++
			if len(s.ErrorSamples) < 5 {
				s.ErrorSamples = append(s.ErrorSamples, c.ID+": "+truncateStr(r.Error, 240))
			}
			continue
		}
		if r.Model != "" {
			s.Models[r.Model]++
		}
		lat = append(lat, r.DurationMs)
		if r.Custom != nil {
			if m, ok := r.Custom["readout_mode"].(string); ok && m != "" {
				s.ReadoutModes[m]++
			}
			if b, ok := r.Custom["backend_used"].(string); ok && b != "" {
				s.BackendsUsed[b]++
			}
			if v, ok := toF(r.Custom["server_ms"]); ok && v > 0 {
				server = append(server, v)
			}
			if pc, ok := r.Custom["position_consistent"].(bool); ok {
				posN++
				if pc {
					posConsisent++
				}
			}
		}
		if r.Correct == nil {
			continue
		}
		s.Scored++
		if *r.Correct {
			s.Correct++
		}
		correct = append(correct, boolF(*r.Correct))
		kind := registry.MetricKind(c.Comparison.Kind)
		if (kind == registry.KindBoul || kind == registry.KindChoice || kind == registry.KindPairwise) && r.Confidence != nil {
			conf = append(conf, float64(*r.Confidence))
			confCorrect = append(confCorrect, *r.Correct)
		}
		if r.Score != nil && kind != registry.KindBoul {
			if g, err := strconv.ParseFloat(c.Expected, 64); err == nil {
				predNum = append(predNum, float64(*r.Score))
				goldNum = append(goldNum, g)
			}
		}
	}
	if s.Scored > 0 {
		s.Accuracy = finite(float64(s.Correct) / float64(s.Scored))
		if o.bootstrapIters > 0 {
			ci := bootstrapMeanCI(correct, o.bootstrapIters, o.bootstrapSeed)
			s.AccuracyCI95 = &ci
		}
	}
	if len(lat) > 0 {
		s.LatencyP50Ms = percentile(lat, 0.5)
		s.LatencyP95Ms = percentile(lat, 0.95)
		var t float64
		for _, v := range lat {
			t += v
		}
		s.LatencyMeanMs = t / float64(len(lat))
	}
	if len(server) > 0 {
		s.ServerP50Ms = finite(percentile(server, 0.5))
	}
	if len(conf) > 0 {
		s.Calibration = &CalibSummary{N: len(conf), ECE10: finite(ece10(conf, confCorrect)), Brier: finite(brierTop1(conf, confCorrect))}
	}
	if len(predNum) > 0 {
		var mae float64
		for i := range predNum {
			mae += math.Abs(predNum[i] - goldNum[i])
		}
		s.ScoreMetrics = &ScoreSummary{N: len(predNum), MAE: finite(mae / float64(len(predNum))), Spearman: finite(spearman(predNum, goldNum)), Pearson: finite(pearson(predNum, goldNum))}
	}
	if posN > 0 {
		s.PositionConsis = finite(float64(posConsisent) / float64(posN))
	}
	if len(s.Models) == 0 {
		s.Models = nil
	}
	if len(s.ReadoutModes) == 0 {
		s.ReadoutModes = nil
	}
	if len(s.BackendsUsed) == 0 {
		s.BackendsUsed = nil
	}
	return s
}

func addGroup(m map[string]GroupSummary, key string, a, b EngineRun) {
	g := m[key]
	g.N++
	if a.Correct != nil {
		g.AScored++
		if *a.Correct {
			g.ACorrect++
		}
	}
	if b.Correct != nil {
		g.BScored++
		if *b.Correct {
			g.BCorrect++
		}
	}
	m[key] = g
}

func finishGroup(g GroupSummary) GroupSummary {
	if g.AScored > 0 {
		g.AAcc = finite(float64(g.ACorrect) / float64(g.AScored))
	}
	if g.BScored > 0 {
		g.BAcc = finite(float64(g.BCorrect) / float64(g.BScored))
	}
	return g
}

func boolF(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func toF(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

func fmtAcc(correct, scored int, ci *Interval) string {
	if scored == 0 {
		return "-"
	}
	s := fmt.Sprintf("%d/%d (%.1f%%)", correct, scored, 100*float64(correct)/float64(scored))
	if ci != nil {
		s += fmt.Sprintf(" [95%% CI %.1f-%.1f]", 100*ci.Lo, 100*ci.Hi)
	}
	return s
}

func fmtPtr(p *float64, format string) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf(format, *p)
}

func renderBatchReportTable(w io.Writer, rep CompareBatchReport) error {
	tw := newTabWriter(w)
	a, b := rep.EngineA, rep.EngineB
	fmt.Fprintln(tw, "================================================================================")
	fmt.Fprintf(tw, "BATCH ENGINE COMPARISON: %s vs %s  (%d cases, schema v%d)\n", strings.ToUpper(a.Engine), strings.ToUpper(b.Engine), rep.TotalCases, rep.SchemaVersion)
	fmt.Fprintln(tw, "================================================================================")
	fmt.Fprintf(tw, "METRIC\tENGINE A (%s)\tENGINE B (%s)\n", a.Engine, b.Engine)
	fmt.Fprintf(tw, "Model(s):\t%s\t%s\n", joinCounts(a.Models), joinCounts(b.Models))
	fmt.Fprintf(tw, "Accuracy vs gold:\t%s\t%s\n", fmtAcc(a.Correct, a.Scored, a.AccuracyCI95), fmtAcc(b.Correct, b.Scored, b.AccuracyCI95))
	fmt.Fprintf(tw, "Errors:\t%d\t%d\n", a.Errors, b.Errors)
	fmt.Fprintf(tw, "Latency p50 / p95 (ms):\t%.0f / %.0f\t%.0f / %.0f\n", a.LatencyP50Ms, a.LatencyP95Ms, b.LatencyP50Ms, b.LatencyP95Ms)
	if a.ServerP50Ms != nil || b.ServerP50Ms != nil {
		fmt.Fprintf(tw, "Server p50 (ms):\t%s\t%s\n", fmtPtr(a.ServerP50Ms, "%.0f"), fmtPtr(b.ServerP50Ms, "%.0f"))
	}
	if a.Calibration != nil || b.Calibration != nil {
		ce := func(c *CalibSummary) string {
			if c == nil {
				return "-"
			}
			return fmt.Sprintf("ECE %s / Brier %s (n=%d)", fmtPtr(c.ECE10, "%.3f"), fmtPtr(c.Brier, "%.3f"), c.N)
		}
		fmt.Fprintf(tw, "Calibration:\t%s\t%s\n", ce(a.Calibration), ce(b.Calibration))
	}
	if a.ScoreMetrics != nil || b.ScoreMetrics != nil {
		sm := func(s *ScoreSummary) string {
			if s == nil {
				return "-"
			}
			return fmt.Sprintf("MAE %s / Spearman %s (n=%d)", fmtPtr(s.MAE, "%.2f"), fmtPtr(s.Spearman, "%.2f"), s.N)
		}
		fmt.Fprintf(tw, "Likert vs gold:\t%s\t%s\n", sm(a.ScoreMetrics), sm(b.ScoreMetrics))
	}
	if len(a.ReadoutModes)+len(b.ReadoutModes) > 0 {
		fmt.Fprintf(tw, "Readout modes:\t%s\t%s\n", joinCounts(a.ReadoutModes), joinCounts(b.ReadoutModes))
	}
	if len(a.BackendsUsed)+len(b.BackendsUsed) > 0 {
		fmt.Fprintf(tw, "Backends used:\t%s\t%s\n", joinCounts(a.BackendsUsed), joinCounts(b.BackendsUsed))
	}
	p := rep.Paired
	fmt.Fprintln(tw, "")
	fmt.Fprintf(tw, "PAIRED (n=%d both succeeded)\t\t\n", p.N)
	fmt.Fprintf(tw, "Accuracy diff A-B:\t%s\t%s\n", fmtPtr(pctPtr(p.AccDiffAminB), "%+.1f pts"), ciStr(p.AccDiffCI95))
	fmt.Fprintf(tw, "Only A right / only B right:\t%d / %d\tMcNemar exact p=%s\n", p.OnlyACorrect, p.OnlyBCorrect, fmtPtr(p.McNemarP, "%.3f"))
	fmt.Fprintf(tw, "Both right / both wrong:\t%d / %d\t\n", p.BothCorrect, p.BothWrong)
	fmt.Fprintf(tw, "Inter-engine agreement:\t%d/%d (%.1f%%)\tkappa=%s\n", p.Agreements, p.N, p.AgreementPct, fmtPtr(p.Kappa, "%.3f"))
	fmt.Fprintf(tw, "Median latency ratio A/B:\t%s\t\n", fmtPtr(p.MedianSpeedup, "%.2fx"))

	renderGroups(tw, "BY KIND", rep.ByKind)
	renderGroups(tw, "BY TIER", rep.ByTier)
	if len(rep.ByCategory) > 1 {
		renderGroups(tw, "BY CATEGORY", rep.ByCategory)
	}

	var miss []CompareCaseResult
	for _, c := range rep.Cases {
		ca, cb := c.Comparison.EngineA.Correct, c.Comparison.EngineB.Correct
		if (ca != nil && !*ca) || (cb != nil && !*cb) || !c.Comparison.EngineA.ok() || !c.Comparison.EngineB.ok() {
			miss = append(miss, c)
		}
	}
	if len(miss) > 0 {
		fmt.Fprintf(tw, "\nMISSES / ERRORS (%d of %d):\n", len(miss), rep.TotalCases)
		fmt.Fprintln(tw, "ID\tKIND\tEXPECTED\tENGINE A\tENGINE B")
		for _, c := range miss {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.ID, c.Comparison.Kind, c.Expected, verdictMark(c.Comparison.EngineA), verdictMark(c.Comparison.EngineB))
		}
	}
	return tw.Flush()
}

func pctPtr(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p * 100
	return &v
}

func ciStr(ci *Interval) string {
	if ci == nil {
		return ""
	}
	return fmt.Sprintf("95%% CI [%+.1f, %+.1f]", 100*ci.Lo, 100*ci.Hi)
}

func renderGroups(tw io.Writer, title string, m map[string]GroupSummary) {
	if len(m) == 0 {
		return
	}
	fmt.Fprintf(tw, "\n%s:\n", title)
	fmt.Fprintln(tw, "GROUP\tN\tA ACC\tB ACC")
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := m[k]
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", k, g.N, fmtAcc(g.ACorrect, g.AScored, nil), fmtAcc(g.BCorrect, g.BScored, nil))
	}
}

func joinCounts(m map[string]int) string {
	if len(m) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, m[k]))
	}
	return strings.Join(parts, " ")
}

// checkAgreement reports whether two successful results agree.
func checkAgreement(kind registry.MetricKind, a, b eval.Result, tol float64) bool {
	switch kind {
	case registry.KindBoul:
		if a.Passed != nil && b.Passed != nil {
			return *a.Passed == *b.Passed
		}
		return false
	case registry.KindChoice:
		return a.ChoiceSelection != "" && strings.EqualFold(a.ChoiceSelection, b.ChoiceSelection)
	case registry.KindPairwise:
		return a.PairwiseChoice != "" && normalizePairLabel(a.PairwiseChoice) == normalizePairLabel(b.PairwiseChoice)
	case registry.KindComputation, registry.KindPrebuilt:
		// pairwise_* prebuilt metrics agree on the preference; the rest on the score.
		if a.PairwiseChoice != "" || b.PairwiseChoice != "" {
			return a.PairwiseChoice != "" && normalizePairLabel(a.PairwiseChoice) == normalizePairLabel(b.PairwiseChoice)
		}
		if a.Score != nil && b.Score != nil {
			return math.Abs(float64(*a.Score-*b.Score)) <= tol+1e-9
		}
		return false
	default:
		if a.Score != nil && b.Score != nil {
			return math.Abs(float64(*a.Score-*b.Score)) <= tol+1e-9
		}
		return false
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
	if !cmp.EngineA.ok() || !cmp.EngineB.ok() {
		fmt.Fprintf(tw, "Error:\t%s\t%s\n", sanitizeCell(firstLine(cmp.EngineA.Error)), sanitizeCell(firstLine(cmp.EngineB.Error)))
	}
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
