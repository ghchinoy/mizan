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

package evalset

import "math"

// aggregate computes the scalar aggregation over the OK members that carry a
// finite numeric score (Result.Score != nil and neither NaN nor Inf). Non-scalar
// members (Score == nil, e.g. pairwise/custom_schema) and members whose score is
// non-finite are RUN and appear in the scorecard rows, but are EXCLUDED here.
// When no member carries a finite numeric score, Aggregate.Score is nil (never
// NaN/crash). No score normalization is applied.
//
// Failed counts only members that Errored or are Missing (design §9); Skipped
// members (fail-fast never ran them) are neither scored nor failures. Scored
// counts the finite-numeric members that fed the aggregate.
func aggregate(method AggregationMethod, threshold *float64, members []MemberResult) Aggregate {
	agg := Aggregate{Method: method, Threshold: threshold}

	var scored []MemberResult
	for _, m := range members {
		if m.Status != OK {
			// Only real failures (Errored/Missing) count as Failed; a Skipped member
			// did not run and is not a failure (design §9).
			if m.Status == Errored || m.Status == Missing {
				agg.Failed++
			}
			continue
		}
		// Exclude non-finite scores (NaN/Inf) from aggregation so they cannot
		// propagate into the aggregate or silently pass a threshold.
		if m.Score != nil && isFinite(float64(*m.Score)) {
			scored = append(scored, m)
		}
	}
	agg.Scored = len(scored)

	if len(scored) > 0 {
		score := computeScore(method, scored)
		agg.Score = &score
	}

	if threshold != nil {
		// A nil or non-finite aggregate never silently passes a threshold.
		pass := agg.Score != nil && isFinite(float64(*agg.Score)) && float64(*agg.Score) >= *threshold
		agg.Passed = &pass
	}

	return agg
}

// isFinite reports whether f is a real, comparable number (neither NaN nor
// ±Inf). Used to keep non-finite member/aggregate scores out of aggregation and
// out of threshold comparisons (which are unreliable for NaN).
func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// computeScore applies the aggregation method to a non-empty slice of scored
// members. An empty/zero-value method is treated as the arithmetic mean — this
// matches FromDoc, which normalizes an empty/absent method to AggMean (design
// §9). FromDoc rejects any unsupported NON-empty method, so the default branch
// here only ever handles AggMean and the (already-normalized) empty method.
func computeScore(method AggregationMethod, scored []MemberResult) float32 {
	switch method {
	case AggMin:
		min := *scored[0].Score
		for _, m := range scored[1:] {
			if *m.Score < min {
				min = *m.Score
			}
		}
		return min
	case AggWeightedMean:
		var wsum, weight float64
		for _, m := range scored {
			wsum += m.Weight * float64(*m.Score)
			weight += m.Weight
		}
		if weight == 0 {
			return 0
		}
		return float32(wsum / weight)
	default: // AggMean and the normalized empty method: arithmetic mean
		var sum float64
		for _, m := range scored {
			sum += float64(*m.Score)
		}
		return float32(sum / float64(len(scored)))
	}
}
