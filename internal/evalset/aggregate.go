package evalset

// aggregate computes the scalar aggregation over the OK members that carry a
// numeric score (Result.Score != nil). Non-scalar members (Score == nil, e.g.
// pairwise/custom_schema) are RUN and appear in the scorecard rows, but are
// EXCLUDED here. When no member carries a numeric score, Aggregate.Score is nil
// (never NaN/crash). No score normalization is applied.
//
// Failed counts every member whose Status != OK; Scored counts the numeric
// members that fed the aggregate.
func aggregate(method AggregationMethod, threshold *float64, members []MemberResult) Aggregate {
	agg := Aggregate{Method: method, Threshold: threshold}

	var scored []MemberResult
	for _, m := range members {
		if m.Status != OK {
			agg.Failed++
			continue
		}
		if m.Score != nil {
			scored = append(scored, m)
		}
	}
	agg.Scored = len(scored)

	if len(scored) > 0 {
		score := computeScore(method, scored)
		agg.Score = &score
	}

	if threshold != nil {
		pass := agg.Score != nil && float64(*agg.Score) >= *threshold
		agg.Passed = &pass
	}

	return agg
}

// computeScore applies the aggregation method to a non-empty slice of scored
// members. An unknown method falls back to the arithmetic mean; FromDoc already
// rejects unsupported methods, so this only guards against a zero-value method.
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
	default: // AggMean and zero-value fall back to arithmetic mean
		var sum float64
		for _, m := range scored {
			sum += float64(*m.Score)
		}
		return float32(sum / float64(len(scored)))
	}
}
