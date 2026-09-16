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

package results

import (
	"math"
	"sort"
	"time"
)

// Aggregation is a PURE, read-only computation over a slice of stored Results
// (design §4.C). It never touches the store, the ResultStore interface, or any
// backend: Summarize and Trend run entirely in Go over Service.List output, so
// they behave identically whichever backend produced the slice (the immutable
// append-only SQLite store today, a Firestore backend when that seam is filled).
// No DDL, no migration, no store-interface change is introduced by this file.
//
// Nil scores (Score == nil — e.g. a genai error left no score, or a pairwise
// result carries a choice not a score) are EXCLUDED from every numeric
// aggregation and counted separately as NUnscored, mirroring the exclusion in
// internal/evalset/aggregate.go. Non-finite scores (NaN/±Inf) are treated the
// same way so they cannot poison a mean or silently pass a threshold. Heuristic
// failures score a finite 0.0 and DO count.

// TrendBucket selects the time granularity Trend buckets RunAt into.
type TrendBucket string

const (
	// TrendDay buckets results by UTC calendar day (key "YYYY-MM-DD").
	TrendDay TrendBucket = "day"
	// TrendWeek buckets results by ISO week, keyed by the UTC Monday that starts
	// the week (key "YYYY-MM-DD" of that Monday).
	TrendWeek TrendBucket = "week"
)

// summaryBuckets is the fixed number of score-distribution histogram buckets
// Summarize emits per template (spread evenly across the observed [min,max]).
const summaryBuckets = 5

// ThresholdStats is the pass/fail breakdown of a TemplateSummary, populated only
// when Summarize is called with a non-nil threshold. A scored result PASSES when
// its score is >= the threshold.
type ThresholdStats struct {
	Value    float64 `json:"value"`
	Pass     int     `json:"pass"`
	Fail     int     `json:"fail"`
	PassRate float64 `json:"pass_rate"` // Pass / (Pass+Fail); 0 when no scored results
}

// ScoreBucket is one bar of a score-distribution histogram: [Lo,Hi) except the
// final bucket, whose upper bound is inclusive so the maximum score lands in it.
type ScoreBucket struct {
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
	Count int     `json:"count"`
}

// TemplateSummary is the per-template rollup Summarize emits. Mean/Min/Max/Stddev
// are pointers so a template whose results are ALL unscored (N == 0) reports no
// synthesized statistics (nil), never a misleading zero. Stddev is the population
// standard deviation over the N scored results.
type TemplateSummary struct {
	TemplateID      string          `json:"template_id"`
	TemplateVersion string          `json:"template_version,omitempty"`
	N               int             `json:"n"`          // scored results feeding the statistics
	NUnscored       int             `json:"n_unscored"` // Score == nil or non-finite (excluded)
	Mean            *float64        `json:"mean,omitempty"`
	Min             *float64        `json:"min,omitempty"`
	Max             *float64        `json:"max,omitempty"`
	Stddev          *float64        `json:"stddev,omitempty"`
	Threshold       *ThresholdStats `json:"threshold,omitempty"`
	Buckets         []ScoreBucket   `json:"buckets,omitempty"`
}

// CriterionMean is the mean of one rubric criterion within a trend bucket, parsed
// from the persisted CustomOutput.per_criterion blob.
type CriterionMean struct {
	Group     string  `json:"group"`
	Criterion string  `json:"criterion"`
	N         int     `json:"n"`
	Mean      float64 `json:"mean"`
}

// TrendPoint is one time bucket of Trend output. Mean is a pointer so a bucket
// with only unscored results reports no mean (nil) rather than a misleading zero.
// PerCriterion is populated only for buckets whose results carry a parseable
// CustomOutput.per_criterion array (rubric-detail results); it is nil otherwise.
type TrendPoint struct {
	Bucket       string          `json:"bucket"` // "YYYY-MM-DD" (day) or the week's Monday (week)
	N            int             `json:"n"`
	NUnscored    int             `json:"n_unscored"`
	Mean         *float64        `json:"mean,omitempty"`
	PerCriterion []CriterionMean `json:"per_criterion,omitempty"`
}

// Summarize groups rs by template (ID + version) and computes, per template, the
// count/mean/min/max/population-stddev of the finite numeric scores plus a
// score-distribution histogram; when threshold is non-nil it also computes the
// pass/fail/passRate breakdown. Unscored results (Score == nil or non-finite) are
// excluded from every statistic and counted as NUnscored. Output is ordered by
// template ID then version for deterministic rendering.
func Summarize(rs []Result, threshold *float64) []TemplateSummary {
	type key struct{ id, version string }
	scores := make(map[key][]float64)
	unscored := make(map[key]int)
	order := make([]key, 0)
	seen := make(map[key]struct{})

	for _, r := range rs {
		k := key{id: r.Template.ID, version: r.Template.Version}
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			order = append(order, k)
		}
		if s, ok := scoreOf(r); ok {
			scores[k] = append(scores[k], s)
		} else {
			unscored[k]++
		}
	}

	sort.Slice(order, func(i, j int) bool {
		if order[i].id != order[j].id {
			return order[i].id < order[j].id
		}
		return order[i].version < order[j].version
	})

	out := make([]TemplateSummary, 0, len(order))
	for _, k := range order {
		vals := scores[k]
		sum := TemplateSummary{
			TemplateID:      k.id,
			TemplateVersion: k.version,
			N:               len(vals),
			NUnscored:       unscored[k],
		}
		if len(vals) > 0 {
			mean, min, max, stddev := stats(vals)
			sum.Mean = &mean
			sum.Min = &min
			sum.Max = &max
			sum.Stddev = &stddev
			sum.Buckets = histogram(vals, min, max)
		}
		if threshold != nil {
			ts := &ThresholdStats{Value: *threshold}
			for _, v := range vals {
				if v >= *threshold {
					ts.Pass++
				} else {
					ts.Fail++
				}
			}
			if n := ts.Pass + ts.Fail; n > 0 {
				ts.PassRate = float64(ts.Pass) / float64(n)
			}
			sum.Threshold = ts
		}
		out = append(out, sum)
	}
	return out
}

// Trend buckets rs by RunAt at the given granularity and emits, per bucket, the
// mean of the finite numeric scores and — when the bucket's results carry a
// parseable CustomOutput.per_criterion array — the per-criterion means. Unscored
// results are excluded from the mean and counted as NUnscored. Buckets are
// emitted in chronological order.
func Trend(rs []Result, bucket TrendBucket) []TrendPoint {
	type acc struct {
		scores       []float64
		unscored     int
		perCriterion map[string][]float64 // "group\x00criterion" -> scores
		labels       map[string][2]string // key -> {group, criterion}
	}
	buckets := make(map[string]*acc)
	order := make([]string, 0)

	for _, r := range rs {
		bk := bucketKey(r.RunAt, bucket)
		a := buckets[bk]
		if a == nil {
			a = &acc{perCriterion: make(map[string][]float64), labels: make(map[string][2]string)}
			buckets[bk] = a
			order = append(order, bk)
		}
		if s, ok := scoreOf(r); ok {
			a.scores = append(a.scores, s)
		} else {
			a.unscored++
		}
		for _, c := range parsePerCriterion(r.Outcome.CustomOutput) {
			ck := c.group + "\x00" + c.criterion
			a.perCriterion[ck] = append(a.perCriterion[ck], c.score)
			a.labels[ck] = [2]string{c.group, c.criterion}
		}
	}

	sort.Strings(order)
	out := make([]TrendPoint, 0, len(order))
	for _, bk := range order {
		a := buckets[bk]
		pt := TrendPoint{Bucket: bk, N: len(a.scores), NUnscored: a.unscored}
		if len(a.scores) > 0 {
			mean, _, _, _ := stats(a.scores)
			pt.Mean = &mean
		}
		if len(a.perCriterion) > 0 {
			ckeys := make([]string, 0, len(a.perCriterion))
			for ck := range a.perCriterion {
				ckeys = append(ckeys, ck)
			}
			sort.Slice(ckeys, func(i, j int) bool {
				li, lj := a.labels[ckeys[i]], a.labels[ckeys[j]]
				if li[0] != lj[0] {
					return li[0] < lj[0]
				}
				return li[1] < lj[1]
			})
			for _, ck := range ckeys {
				vals := a.perCriterion[ck]
				mean, _, _, _ := stats(vals)
				lbl := a.labels[ck]
				pt.PerCriterion = append(pt.PerCriterion, CriterionMean{
					Group:     lbl[0],
					Criterion: lbl[1],
					N:         len(vals),
					Mean:      mean,
				})
			}
		}
		out = append(out, pt)
	}
	return out
}

// scoreOf returns the finite float64 score of a result, or ok=false when the
// result is unscored (Score == nil) or its score is non-finite (NaN/±Inf).
func scoreOf(r Result) (float64, bool) {
	if r.Outcome.Score == nil {
		return 0, false
	}
	f := float64(*r.Outcome.Score)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// stats returns the arithmetic mean, min, max, and population standard deviation
// of a non-empty slice of finite scores.
func stats(vals []float64) (mean, min, max, stddev float64) {
	min, max = vals[0], vals[0]
	var sum float64
	for _, v := range vals {
		sum += v
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	mean = sum / float64(len(vals))
	var sq float64
	for _, v := range vals {
		d := v - mean
		sq += d * d
	}
	stddev = math.Sqrt(sq / float64(len(vals)))
	return mean, min, max, stddev
}

// histogram spreads vals into summaryBuckets evenly-sized bars across [min,max].
// The final bucket's upper bound is inclusive so max lands in it. When min==max
// (all scores identical, incl. a single value) it returns one bucket holding all.
func histogram(vals []float64, min, max float64) []ScoreBucket {
	if min == max {
		return []ScoreBucket{{Lo: min, Hi: max, Count: len(vals)}}
	}
	width := (max - min) / summaryBuckets
	out := make([]ScoreBucket, summaryBuckets)
	for i := range out {
		out[i] = ScoreBucket{Lo: min + float64(i)*width, Hi: min + float64(i+1)*width}
	}
	out[summaryBuckets-1].Hi = max
	for _, v := range vals {
		idx := int((v - min) / width)
		if idx >= summaryBuckets {
			idx = summaryBuckets - 1 // v == max
		}
		out[idx].Count++
	}
	return out
}

// bucketKey maps a run time to its bucket label at the given granularity. Day
// buckets by UTC calendar date; week buckets by the UTC Monday starting the ISO
// week. Both label as "YYYY-MM-DD".
func bucketKey(t time.Time, bucket TrendBucket) string {
	u := t.UTC()
	switch bucket {
	case TrendWeek:
		// Go's Weekday: Sunday=0..Saturday=6. Shift to Monday-based offset.
		offset := (int(u.Weekday()) + 6) % 7
		monday := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -offset)
		return monday.Format("2006-01-02")
	default: // TrendDay and any unrecognized value default to day granularity.
		return u.Format("2006-01-02")
	}
}

// criterionScore is one parsed per-criterion entry from a CustomOutput blob.
type criterionScore struct {
	group     string
	criterion string
	score     float64
}

// parsePerCriterion extracts the {group, criterion, score} entries from a stored
// CustomOutput.per_criterion array (written by the rubric-detail path,
// internal/eval/rubric_structured.go). It is defensive: it tolerates the
// float64-typed scores a JSON round-trip through the store produces as well as
// in-memory int/float32 values, and skips any malformed entry rather than
// failing. Returns nil when no parseable per_criterion array is present.
func parsePerCriterion(co map[string]any) []criterionScore {
	if co == nil {
		return nil
	}
	raw, ok := co["per_criterion"]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]criterionScore, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		score, ok := toFloat(m["score"])
		if !ok {
			continue
		}
		out = append(out, criterionScore{
			group:     toString(m["group"]),
			criterion: toString(m["criterion"]),
			score:     score,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// toFloat coerces the numeric encodings a JSON/in-memory value may carry (float64
// from JSON, plus int/int64/float32 when built directly in Go) to a finite
// float64. It reports ok=false for a nil, non-numeric, or non-finite value.
func toFloat(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// toString returns v as a string when it is one, else "".
func toString(v any) string {
	s, _ := v.(string)
	return s
}
