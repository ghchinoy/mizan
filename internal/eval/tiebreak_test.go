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

package eval

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval/diffusion"
	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
)

func TestParseTieBreakMode(t *testing.T) {
	for raw, want := range map[string]TieBreakMode{
		"":               TieBreakNone,
		"none":           TieBreakNone,
		" NONE ":         TieBreakNone,
		"expected-grade": TieBreakExpectedGrade,
		"Expected-Grade": TieBreakExpectedGrade,
	} {
		got, ok := ParseTieBreakMode(raw)
		if !ok || got != want {
			t.Errorf("ParseTieBreakMode(%q) = (%q, %v), want (%q, true)", raw, got, ok, want)
		}
	}
	if _, ok := ParseTieBreakMode("bogus"); ok {
		t.Error("ParseTieBreakMode(\"bogus\") succeeded, want false")
	}
}

func TestExpectedGradeFromProbabilities(t *testing.T) {
	t.Run("skewed_likert_1_to_5", func(t *testing.T) {
		probs := map[string]float64{
			"1": 0.05,
			"2": 0.10,
			"3": 0.15,
			"4": 0.30,
			"5": 0.40,
		}
		got, ok := ExpectedGradeFromProbabilities(probs)
		if !ok {
			t.Fatal("ExpectedGradeFromProbabilities returned ok=false")
		}
		want := 0.05*1 + 0.10*2 + 0.15*3 + 0.30*4 + 0.40*5
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("got %.6f, want %.6f", got, want)
		}
	})

	t.Run("negative_scale_verbosity", func(t *testing.T) {
		probs := map[string]float64{
			"-2": 0.10,
			"-1": 0.20,
			"0":  0.50,
			"1":  0.15,
			"2":  0.05,
		}
		got, ok := ExpectedGradeFromProbabilities(probs)
		if !ok {
			t.Fatal("ExpectedGradeFromProbabilities returned ok=false")
		}
		want := -0.20 - 0.20 + 0.15 + 0.10
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("got %.6f, want %.6f", got, want)
		}
	})

	t.Run("skips_non_numeric_fallback_tokens", func(t *testing.T) {
		// Mirrors score-02 in docs/experiments/benchmark_results_gce.json where
		// token-logprob fallback emitted a non-numeric "It" key alongside "1", "2", "3".
		probs := map[string]float64{
			"1":  5.797033993190473e-07,
			"2":  0.9999948740268748,
			"3":  7.975933270994777e-07,
			"It": 3.370581504056393e-07,
		}
		got, ok := ExpectedGradeFromProbabilities(probs)
		if !ok {
			t.Fatal("ExpectedGradeFromProbabilities returned ok=false")
		}
		if math.Abs(got-2.0) > 1e-4 {
			t.Errorf("got %.8f, want ~2.0", got)
		}
	})

	t.Run("empty_or_non_numeric_only", func(t *testing.T) {
		if _, ok := ExpectedGradeFromProbabilities(nil); ok {
			t.Error("nil map: want ok=false")
		}
		if _, ok := ExpectedGradeFromProbabilities(map[string]float64{"It": 0.8, "The": 0.2}); ok {
			t.Error("non-numeric map: want ok=false")
		}
	})
}

func TestCompareScoresTieBreakRubric3Of4Tie(t *testing.T) {
	// Both items pass 3 of 4 binary rubric criteria, so discrete score on 1..5 is
	// 1 + 0.75 * 4 = 4.00 for both. Item A has strong P(yes) mass while Item B
	// barely clears 0.5 on three criteria and fails the fourth hard.
	tiedScoreA := float32(4.0)
	tiedScoreB := float32(4.0)

	customA := map[string]any{
		"scale_min": 1,
		"scale_max": 5,
		"per_criterion": []any{
			map[string]any{"passed": true, "p_yes": 0.98},
			map[string]any{"passed": true, "p_yes": 0.96},
			map[string]any{"passed": true, "p_yes": 0.95},
			map[string]any{"passed": false, "p_yes": 0.45},
		},
	}
	customB := map[string]any{
		"scale_min": 1,
		"scale_max": 5,
		"per_criterion": []any{
			map[string]any{"passed": true, "p_yes": 0.55},
			map[string]any{"passed": true, "p_yes": 0.52},
			map[string]any{"passed": true, "p_yes": 0.51},
			map[string]any{"passed": false, "p_yes": 0.04},
		},
	}

	egA, okA := ResolveExpectedGrade(&tiedScoreA, customA)
	egB, okB := ResolveExpectedGrade(&tiedScoreB, customB)
	if !okA || !okB {
		t.Fatalf("ResolveExpectedGrade ok = (%v, %v), want (true, true)", okA, okB)
	}
	// Item A: mean P(yes) = 0.835 -> 1 + 0.835*4 = 4.34
	// Item B: mean P(yes) = 0.405 -> 1 + 0.405*4 = 2.62
	if math.Abs(egA-4.34) > 1e-6 || math.Abs(egB-2.62) > 1e-6 {
		t.Fatalf("expected grades = (%.4f, %.4f), want (4.3400, 2.6200)", egA, egB)
	}
	if cmp := CompareScoresTieBreak(&tiedScoreA, customA, &tiedScoreB, customB, 1e-6); cmp != 1 {
		t.Errorf("CompareScoresTieBreak(A, B) = %d, want +1", cmp)
	}
	if cmp := CompareScoresTieBreak(&tiedScoreB, customB, &tiedScoreA, customA, 1e-6); cmp != -1 {
		t.Errorf("CompareScoresTieBreak(B, A) = %d, want -1", cmp)
	}

	// Primary discrete score ordering must never be inverted when non-tied:
	// 4/4 (score 5.0) beats 3/4 (score 4.0) regardless of probability mass.
	higherDiscrete := float32(5.0)
	if cmp := CompareScoresTieBreak(&higherDiscrete, customB, &tiedScoreA, customA, 1e-6); cmp != 1 {
		t.Errorf("non-tied discrete 5.0 vs 4.0: got %d, want +1", cmp)
	}
}

func TestExpectedPassFractionLegacyConfidenceFallback(t *testing.T) {
	// Legacy v1 per_criterion entries (as in benchmark_results.json img-03) have
	// boolean "passed" and top-1 "confidence" without "p_yes".
	rawJSON := `[
		{"passed": false, "confidence": 0.90},
		{"passed": true, "confidence": 0.80}
	]`
	var crit []any
	if err := json.Unmarshal([]byte(rawJSON), &crit); err != nil {
		t.Fatal(err)
	}
	passed, total, epf, ok := ExpectedPassFractionFromCriteria(crit)
	if !ok || passed != 1 || total != 2 {
		t.Fatalf("got passed=%d total=%d ok=%v, want 1, 2, true", passed, total, ok)
	}
	// Item 0 failed with 0.90 confidence -> P(yes)=0.10; Item 1 passed with 0.80 -> P(yes)=0.80; mean=0.45.
	if math.Abs(epf-0.45) > 1e-9 {
		t.Errorf("expectedFrac = %.6f, want 0.45", epf)
	}
}

func TestDiffusionExpectedGradeCustomOutput(t *testing.T) {
	t.Run("score_kind", func(t *testing.T) {
		fd := &fakeDiffusionClient{
			resp: &diffusion.StructuredDecisionResponse{
				Answers: map[string]diffusion.QuestionAnswer{
					"score": {
						Type:       "score",
						Level:      "4",
						Score:      3.80,
						Confidence: 0.60,
						Probabilities: map[string]float64{
							"1": 0.02,
							"2": 0.03,
							"3": 0.15,
							"4": 0.60,
							"5": 0.20,
						},
					},
				},
			},
		}
		eng := NewEngine(&evaltest.FakeEvaluationClient{}, "proj-123", "us-central1", WithDiffusionClient(fd))
		tmpl := registry.MetricTemplate{
			ID:                   "quality/clarity",
			Kind:                 registry.KindScore,
			MetricPromptTemplate: "Score clarity 1-5: {{response}}",
		}
		res, err := eng.Run(context.Background(), tmpl, Instance{
			Fields: map[string]AssetRef{"response": {Modality: registry.ModalityText, Text: "Clear text."}},
		}, WithEngine("diffusion"))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		eg, ok := res.CustomOutput["expected_grade"].(float64)
		if !ok {
			t.Fatalf("CustomOutput[expected_grade] missing or wrong type: %#v", res.CustomOutput["expected_grade"])
		}
		want := 0.02*1 + 0.03*2 + 0.15*3 + 0.60*4 + 0.20*5 // 3.93
		if math.Abs(eg-want) > 1e-6 {
			t.Errorf("expected_grade = %.6f, want %.6f", eg, want)
		}
		norm, ok := res.CustomOutput["expected_grade_normalized"].(float64)
		if !ok || math.Abs(norm-(want-1.0)/4.0) > 1e-6 {
			t.Errorf("expected_grade_normalized = %v, want %.6f", res.CustomOutput["expected_grade_normalized"], (want-1.0)/4.0)
		}
	})

	t.Run("rubric_kind", func(t *testing.T) {
		fd := &fakeDiffusionClient{
			resp: &diffusion.StructuredDecisionResponse{
				Answers: map[string]diffusion.QuestionAnswer{
					"g_1": {Type: "boolean", Label: "yes", Confidence: 0.95, Probabilities: map[string]float64{"yes": 0.95, "no": 0.05}},
					"g_2": {Type: "boolean", Label: "no", Confidence: 0.55, Probabilities: map[string]float64{"yes": 0.45, "no": 0.55}},
				},
			},
		}
		eng := NewEngine(&evaltest.FakeEvaluationClient{}, "proj-123", "us-central1", WithDiffusionClient(fd))
		tmpl := registry.MetricTemplate{
			ID:                   "quality/rubric",
			Kind:                 registry.KindRubric,
			MetricPromptTemplate: "Evaluate: {{response}}",
			RubricGroups:         map[string][]string{"g": {"c1", "c2"}},
		}
		res, err := eng.Run(context.Background(), tmpl, Instance{
			Fields: map[string]AssetRef{"response": {Modality: registry.ModalityText, Text: "Sample."}},
		}, WithEngine("diffusion"))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		// Discrete: 1/2 passed -> 1 + 0.5*4 = 3.0. Continuous: mean P(yes)=0.70 -> 1 + 0.70*4 = 3.8.
		if res.Score == nil || math.Abs(float64(*res.Score)-3.0) > 1e-5 {
			t.Fatalf("Score = %v, want 3.0", res.Score)
		}
		eg, ok := res.CustomOutput["expected_grade"].(float64)
		if !ok || math.Abs(eg-3.8) > 1e-6 {
			t.Errorf("expected_grade = %v, want 3.8", res.CustomOutput["expected_grade"])
		}
	})

	t.Run("prebuilt_coherence", func(t *testing.T) {
		fd := &fakeDiffusionClient{
			resp: &diffusion.StructuredDecisionResponse{
				Answers: map[string]diffusion.QuestionAnswer{
					"score": {
						Type:       "score",
						Level:      "3",
						Score:      3.0,
						Confidence: 0.50,
						Probabilities: map[string]float64{
							"1": 0.00,
							"2": 0.25,
							"3": 0.50,
							"4": 0.25,
							"5": 0.00,
						},
					},
				},
			},
		}
		eng := NewEngine(&evaltest.FakeEvaluationClient{}, "proj-123", "us-central1", WithDiffusionClient(fd))
		tmpl := registry.MetricTemplate{
			ID:     "vertex/coherence",
			Kind:   registry.KindPrebuilt,
			Native: &registry.NativeMetricSpec{Metric: "coherence", ResponseField: "response"},
		}
		res, err := eng.Run(context.Background(), tmpl, Instance{
			Fields: map[string]AssetRef{"response": {Modality: registry.ModalityText, Text: "Coherent paragraph."}},
		}, WithEngine("diffusion"))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		eg, ok := res.CustomOutput["expected_grade"].(float64)
		if !ok || math.Abs(eg-3.0) > 1e-6 {
			t.Errorf("expected_grade = %v, want 3.0", res.CustomOutput["expected_grade"])
		}
	})
}
