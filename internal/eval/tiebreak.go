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
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// TieBreakMode controls how discrete score and rubric ties are resolved.
type TieBreakMode string

const (
	// TieBreakNone preserves discrete/default score comparisons.
	TieBreakNone TieBreakMode = "none"
	// TieBreakExpectedGrade resolves ties and continuous score metrics using E[r] = sum(g * p_g).
	TieBreakExpectedGrade TieBreakMode = "expected-grade"
)

// ParseTieBreakMode validates a --tie-break flag value ("" aliases "none").
func ParseTieBreakMode(raw string) (TieBreakMode, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(TieBreakNone):
		return TieBreakNone, true
	case string(TieBreakExpectedGrade):
		return TieBreakExpectedGrade, true
	default:
		return "", false
	}
}

// ExpectedGradeFromProbabilities computes E[r] = sum(g * p_g) / sum(p_g) over
// finite numeric keys g in probs with p_g > 0. Non-numeric keys (such as token
// fallback strings) are ignored. Returns (0, false) when no positive numeric
// probability mass exists.
func ExpectedGradeFromProbabilities(probs map[string]float64) (float64, bool) {
	if len(probs) == 0 {
		return 0, false
	}
	var weightedSum, weightTotal float64
	for k, p := range probs {
		if p <= 0 || math.IsNaN(p) || math.IsInf(p, 0) {
			continue
		}
		g, err := strconv.ParseFloat(strings.TrimSpace(k), 64)
		if err != nil || math.IsNaN(g) || math.IsInf(g, 0) {
			continue
		}
		weightedSum += g * p
		weightTotal += p
	}
	if weightTotal <= 0 {
		return 0, false
	}
	return weightedSum / weightTotal, true
}

// ExpectedPassFractionFromCriteria computes discrete passed count, total criteria,
// and continuous mean P(yes) across a CustomOutput["per_criterion"] slice.
// Supports both structured v2 entries carrying explicit "p_yes" and v1 entries
// carrying boolean "passed" plus top-1 "confidence".
func ExpectedPassFractionFromCriteria(perCriterion []any) (passed, total int, expectedFrac float64, ok bool) {
	if len(perCriterion) == 0 {
		return 0, 0, 0, false
	}
	var sumPYes float64
	for _, elem := range perCriterion {
		m, isMap := elem.(map[string]any)
		if !isMap {
			continue
		}
		critPassed, hasPassed := m["passed"].(bool)
		if pYes, hasPYes := customFloat(m["p_yes"]); hasPYes && pYes >= 0 && pYes <= 1 {
			total++
			if critPassed {
				passed++
			} else if !hasPassed && pYes >= 0.5 {
				passed++
			}
			sumPYes += pYes
			continue
		}
		conf, hasConf := customFloat(m["confidence"])
		if hasPassed && hasConf && conf >= 0 && conf <= 1 {
			total++
			if critPassed {
				passed++
				sumPYes += conf
			} else {
				sumPYes += 1.0 - conf
			}
		}
	}
	if total == 0 {
		return 0, 0, 0, false
	}
	return passed, total, sumPYes / float64(total), true
}

// ResolveExpectedGrade extracts the probability-weighted expected grade E[r]
// from CustomOutput when available, falling back to score. The boolean return
// reports whether a probability-weighted signal was found in CustomOutput.
func ResolveExpectedGrade(score *float32, custom map[string]any) (grade float64, fromProb bool) {
	if custom != nil {
		if eg, ok := customFloat(custom["expected_grade"]); ok {
			return eg, true
		}
		if probs, ok := extractProbMap(custom["probabilities"]); ok {
			if eg, ok := ExpectedGradeFromProbabilities(probs); ok {
				return eg, true
			}
		}
		minScale, maxScale := inferRubricScale(score, custom)
		if epf, ok := customFloat(custom["expected_pass_fraction"]); ok {
			return minScale + epf*(maxScale-minScale), true
		}
		if rawCrit, ok := custom["per_criterion"].([]any); ok {
			if _, _, epf, ok := ExpectedPassFractionFromCriteria(rawCrit); ok {
				return minScale + epf*(maxScale-minScale), true
			}
		}
		if pYes, ok := customFloat(custom["p_yes"]); ok {
			return pYes, true
		}
	}
	if score != nil {
		return float64(*score), false
	}
	return 0, false
}

// CompareScoresTieBreak compares two items by their primary score first, and
// when |scoreA - scoreB| <= eps (for example, 3/4 vs 3/4 binary rubric criteria
// or identical discrete Likert levels), resolves the tie via ResolveExpectedGrade.
// Returns -1 if A < B, +1 if A > B, or 0 if still tied within eps.
func CompareScoresTieBreak(scoreA *float32, customA map[string]any, scoreB *float32, customB map[string]any, eps float64) int {
	if eps <= 0 {
		eps = 1e-6
	}
	var sA, sB float64
	if scoreA != nil {
		sA = float64(*scoreA)
	}
	if scoreB != nil {
		sB = float64(*scoreB)
	}
	if math.Abs(sA-sB) > eps {
		if sA < sB {
			return -1
		}
		return 1
	}
	egA, okA := ResolveExpectedGrade(scoreA, customA)
	egB, okB := ResolveExpectedGrade(scoreB, customB)
	if !okA && !okB {
		return 0
	}
	if math.Abs(egA-egB) <= eps {
		return 0
	}
	if egA < egB {
		return -1
	}
	return 1
}

func inferRubricScale(score *float32, custom map[string]any) (float64, float64) {
	if minV, okMin := customFloat(custom["scale_min"]); okMin {
		if maxV, okMax := customFloat(custom["scale_max"]); okMax && maxV > minV {
			return minV, maxV
		}
	}
	// Default Mizan rubric scale is 1..5 (defaultRubricScaleMin=1, defaultRubricScaleMax=5).
	// If discrete score is in [0, 1] and pass_fraction equals score, preserve 0..1.
	if score != nil {
		s := float64(*score)
		if pf, ok := customFloat(custom["pass_fraction"]); ok && math.Abs(s-pf) <= 1e-5 && s <= 1.0 {
			return 0, 1
		}
		if rawCrit, ok := custom["per_criterion"].([]any); ok {
			if passed, total, _, ok := ExpectedPassFractionFromCriteria(rawCrit); ok && total > 0 {
				frac := float64(passed) / float64(total)
				if math.Abs(s-(1.0+frac*4.0)) <= 1e-3 {
					return 1, 5
				}
				if math.Abs(s-frac) <= 1e-4 && s <= 1.0 {
					return 0, 1
				}
			}
		}
	}
	return float64(defaultRubricScaleMin), float64(defaultRubricScaleMax)
}

func extractProbMap(raw any) (map[string]float64, bool) {
	switch m := raw.(type) {
	case map[string]float64:
		return m, len(m) > 0
	case map[string]any:
		out := make(map[string]float64, len(m))
		for k, v := range m {
			if f, ok := customFloat(v); ok {
				out[k] = f
			}
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}

func customFloat(v any) (float64, bool) {
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
