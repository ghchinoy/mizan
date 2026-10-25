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
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvalidTieBreakRejectedOffline(t *testing.T) {
	out, err := executeRoot(t, "eval", "compare-engines", "--metric", "ns/m", "--tie-break", "bogus")
	if err == nil {
		t.Fatalf("expected error for invalid --tie-break, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "invalid --tie-break") {
		t.Errorf("error = %v, want mention of invalid --tie-break", err)
	}

	out, err = executeRoot(t, "eval", "compare-engines", "--metric", "ns/m", "--from-report", "some.json")
	if err == nil {
		t.Fatalf("expected mutual exclusivity error, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "exactly one of --metric, --dataset, or --from-report is required") {
		t.Errorf("error = %v", err)
	}
}

func TestFromReportBenchmarkResultsTieBreak(t *testing.T) {
	t.Run("benchmark_results_rubric_img03", func(t *testing.T) {
		reportPath := filepath.Join("..", "..", "docs", "experiments", "benchmark_results.json")
		out, err := executeRoot(t, "--output", "json", "eval", "compare-engines",
			"--from-report", reportPath,
			"--tie-break", "expected-grade",
			"--bootstrap", "0")
		if err != nil {
			t.Fatalf("compare-engines --from-report: %v\nout:\n%s", err, out)
		}
		var rep CompareBatchReport
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("unmarshal CompareBatchReport: %v\nout:\n%s", err, out)
		}
		if rep.Meta.TieBreak != "expected-grade" {
			t.Errorf("Meta.TieBreak = %q, want expected-grade", rep.Meta.TieBreak)
		}
		foundImg03 := false
		for _, c := range rep.Cases {
			if c.ID != "img-03" {
				continue
			}
			foundImg03 = true
			// img-03 has 4/6 passed criteria with legacy confidence values whose
			// mean P(yes) is ~0.5367, mapping onto 1..5 as ~3.1468.
			tbs, ok := toF(c.Comparison.EngineB.Custom["tie_break_score"])
			if !ok {
				t.Fatalf("img-03 missing tie_break_score in Custom: %#v", c.Comparison.EngineB.Custom)
			}
			if math.Abs(tbs-3.1468) > 0.01 {
				t.Errorf("img-03 tie_break_score = %.4f, want ~3.1468", tbs)
			}
		}
		if !foundImg03 {
			t.Fatal("img-03 not found in benchmark_results.json")
		}
	})

	t.Run("benchmark_results_gce_non_numeric_prob_key", func(t *testing.T) {
		reportPath := filepath.Join("..", "..", "docs", "experiments", "benchmark_results_gce.json")
		out, err := executeRoot(t, "--output", "json", "eval", "compare-engines",
			"--from-report", reportPath,
			"--tie-break", "expected-grade",
			"--bootstrap", "0")
		if err != nil {
			t.Fatalf("compare-engines --from-report gce: %v\nout:\n%s", err, out)
		}
		var rep CompareBatchReport
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("unmarshal CompareBatchReport: %v", err)
		}
		foundScore02 := false
		for _, c := range rep.Cases {
			if c.ID != "score-02" {
				continue
			}
			foundScore02 = true
			tbs, ok := toF(c.Comparison.EngineB.Custom["tie_break_score"])
			if !ok {
				t.Fatalf("score-02 missing tie_break_score in Custom: %#v", c.Comparison.EngineB.Custom)
			}
			if math.Abs(tbs-2.0) > 1e-3 {
				t.Errorf("score-02 tie_break_score = %.6f, want ~2.0 (skipping non-numeric 'It' key)", tbs)
			}
		}
		if !foundScore02 {
			t.Fatal("score-02 not found in benchmark_results_gce.json")
		}
	})
}

func TestFromReportJudgeEvalLikertTieResolution(t *testing.T) {
	reportPath := filepath.Join("..", "..", "docs", "experiments", "judge-eval", "runs", "20260926", "j4_vs_j1_likert_coherence.json")

	// 1. With --tie-break expected-grade, all 100 items resolve to distinct predictions
	// (vs only 5 discrete argmax_level values).
	outTB, err := executeRoot(t, "--output", "json", "eval", "compare-engines",
		"--from-report", reportPath,
		"--tie-break", "expected-grade",
		"--bootstrap", "0")
	if err != nil {
		t.Fatalf("compare-engines expected-grade: %v\nout:\n%s", err, outTB)
	}
	var repTB CompareBatchReport
	if err := json.Unmarshal([]byte(outTB), &repTB); err != nil {
		t.Fatalf("unmarshal expected-grade report: %v", err)
	}
	if repTB.EngineB.ScoreMetrics == nil {
		t.Fatal("EngineB.ScoreMetrics is nil")
	}
	if repTB.EngineB.ScoreMetrics.UniquePredictions != 100 {
		t.Errorf("UniquePredictions = %d, want 100", repTB.EngineB.ScoreMetrics.UniquePredictions)
	}

	argmaxLevels := map[string]bool{}
	for _, c := range repTB.Cases {
		if lvl, ok := c.Comparison.EngineB.Custom["argmax_level"].(string); ok && lvl != "" {
			argmaxLevels[lvl] = true
		}
	}
	if len(argmaxLevels) != 5 {
		t.Errorf("distinct argmax_level count = %d, want 5", len(argmaxLevels))
	}

	// 2. With default --tie-break none, tie_break metadata and unique_predictions stay unset.
	outNone, err := executeRoot(t, "--output", "json", "eval", "compare-engines",
		"--from-report", reportPath,
		"--bootstrap", "0")
	if err != nil {
		t.Fatalf("compare-engines default: %v\nout:\n%s", err, outNone)
	}
	var repNone CompareBatchReport
	if err := json.Unmarshal([]byte(outNone), &repNone); err != nil {
		t.Fatalf("unmarshal default report: %v", err)
	}
	if repNone.Meta.TieBreak != "" {
		t.Errorf("default Meta.TieBreak = %q, want empty", repNone.Meta.TieBreak)
	}
	if repNone.EngineB.ScoreMetrics != nil && repNone.EngineB.ScoreMetrics.UniquePredictions != 0 {
		t.Errorf("default UniquePredictions = %d, want 0 (omitted)", repNone.EngineB.ScoreMetrics.UniquePredictions)
	}
}

func TestFromReportDominantNonNumericFallbackPreservesDiscreteScore(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "dominant_non_numeric.json")
	payload := `{
  "total_cases": 2,
  "cases": [
    {
      "id": "c-tail-5",
      "tier": "core",
      "category": "q",
      "expected": "1",
      "comparison": {
        "metric_id": "m/r",
        "kind": "rubric",
        "agreement": true,
        "engine_a": {"engine": "vertex", "score": 1.0},
        "engine_b": {"engine": "diffusion", "score": 1.0, "custom_output": {"probabilities": {"It": 0.999, "5": 0.001}}}
      }
    },
    {
      "id": "c-tail-1",
      "tier": "core",
      "category": "q",
      "expected": "5",
      "comparison": {
        "metric_id": "m/r",
        "kind": "rubric",
        "agreement": true,
        "engine_a": {"engine": "vertex", "score": 5.0},
        "engine_b": {"engine": "diffusion", "score": 5.0, "custom_output": {"probabilities": {"It": 0.999, "1": 0.001}}}
      }
    }
  ]
}`
	if err := os.WriteFile(reportPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out, err := executeRoot(t, "--output", "json", "eval", "compare-engines",
		"--from-report", reportPath,
		"--tie-break", "expected-grade",
		"--bootstrap", "0")
	if err != nil {
		t.Fatalf("compare-engines: %v\nout:\n%s", err, out)
	}
	var rep CompareBatchReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(rep.Cases) != 2 {
		t.Fatalf("len(Cases) = %d, want 2", len(rep.Cases))
	}
	for _, c := range rep.Cases {
		eb := c.Comparison.EngineB
		if eb.Correct == nil || !*eb.Correct {
			t.Errorf("case %s: engine_b.correct = %v, want true (prediction=%q)", c.ID, eb.Correct, eb.Prediction)
		}
		if _, has := eb.Custom["tie_break_score"]; has {
			t.Errorf("case %s: unexpected tie_break_score %v on dominant non-numeric token map", c.ID, eb.Custom["tie_break_score"])
		}
	}
}
