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
	"testing"
	"time"
)

// scored builds a minimal Result carrying a template id and a score.
func scored(id string, score *float32, at time.Time) Result {
	return Result{
		Template: TemplateRef{ID: id, Version: "1.0.0"},
		RunAt:    at,
		Outcome:  Outcome{Score: score},
	}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestSummarizeStats proves Summarize reports the correct n/mean/min/max/stddev
// per template (grouped by id+version), ordered by id.
func TestSummarizeStats(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	rs := []Result{
		scored("ns/a", f32(1.0), now),
		scored("ns/a", f32(3.0), now),
		scored("ns/a", f32(5.0), now),
		scored("ns/b", f32(4.0), now),
	}

	got := Summarize(rs, nil)
	if len(got) != 2 {
		t.Fatalf("want 2 template summaries, got %d", len(got))
	}
	// Ordered by id: ns/a then ns/b.
	a := got[0]
	if a.TemplateID != "ns/a" || a.N != 3 || a.NUnscored != 0 {
		t.Fatalf("ns/a: id=%s n=%d unscored=%d", a.TemplateID, a.N, a.NUnscored)
	}
	if !approx(*a.Mean, 3.0) || !approx(*a.Min, 1.0) || !approx(*a.Max, 5.0) {
		t.Errorf("ns/a mean/min/max = %v/%v/%v, want 3/1/5", *a.Mean, *a.Min, *a.Max)
	}
	// Population stddev of {1,3,5} = sqrt(8/3) ≈ 1.632993...
	if !approx(*a.Stddev, math.Sqrt(8.0/3.0)) {
		t.Errorf("ns/a stddev = %v, want %v", *a.Stddev, math.Sqrt(8.0/3.0))
	}
	b := got[1]
	if b.TemplateID != "ns/b" || b.N != 1 || !approx(*b.Mean, 4.0) || !approx(*b.Stddev, 0.0) {
		t.Errorf("ns/b summary wrong: %+v", b)
	}
}

// TestSummarizeThreshold proves the pass/fail/passRate breakdown is correct and
// that a result scoring exactly the threshold counts as a pass (>=).
func TestSummarizeThreshold(t *testing.T) {
	now := time.Now().UTC()
	rs := []Result{
		scored("ns/a", f32(0.9), now),
		scored("ns/a", f32(0.5), now), // == threshold -> pass
		scored("ns/a", f32(0.1), now),
		scored("ns/a", f32(0.5), now), // == threshold -> pass
	}
	thr := 0.5
	got := Summarize(rs, &thr)
	if len(got) != 1 || got[0].Threshold == nil {
		t.Fatalf("expected one summary with threshold stats, got %+v", got)
	}
	ts := got[0].Threshold
	if ts.Pass != 3 || ts.Fail != 1 {
		t.Errorf("pass/fail = %d/%d, want 3/1", ts.Pass, ts.Fail)
	}
	if !approx(ts.PassRate, 0.75) {
		t.Errorf("passRate = %v, want 0.75", ts.PassRate)
	}
	if ts.Value != 0.5 {
		t.Errorf("threshold value = %v, want 0.5", ts.Value)
	}
}

// TestSummarizeExcludesUnscored proves nil-score (and non-finite) results are
// excluded from the numeric statistics and counted as n_unscored, and that a
// template whose results are ALL unscored still appears with nil stats.
func TestSummarizeExcludesUnscored(t *testing.T) {
	now := time.Now().UTC()
	nan := float32(math.NaN())
	rs := []Result{
		scored("ns/a", f32(2.0), now),
		scored("ns/a", nil, now),  // genai error, no score
		scored("ns/a", &nan, now), // non-finite -> excluded
		scored("ns/b", nil, now),  // ALL unscored
	}
	got := Summarize(rs, nil)
	if len(got) != 2 {
		t.Fatalf("want 2 summaries, got %d", len(got))
	}
	a := got[0]
	if a.N != 1 || a.NUnscored != 2 {
		t.Errorf("ns/a n=%d unscored=%d, want 1/2", a.N, a.NUnscored)
	}
	if a.Mean == nil || !approx(*a.Mean, 2.0) {
		t.Errorf("ns/a mean should be 2.0 (only the finite score), got %v", a.Mean)
	}
	b := got[1]
	if b.N != 0 || b.NUnscored != 1 {
		t.Errorf("ns/b n=%d unscored=%d, want 0/1", b.N, b.NUnscored)
	}
	if b.Mean != nil || b.Min != nil || b.Max != nil || b.Stddev != nil {
		t.Errorf("ns/b (all-unscored) must report nil stats, got mean=%v", b.Mean)
	}
}

// TestSummarizeHistogram proves the score-distribution buckets count values
// across [min,max] with an inclusive final bucket.
func TestSummarizeHistogram(t *testing.T) {
	now := time.Now().UTC()
	rs := []Result{
		scored("ns/a", f32(0.0), now),
		scored("ns/a", f32(0.0), now),
		scored("ns/a", f32(1.0), now), // max lands in the final (inclusive) bucket
	}
	got := Summarize(rs, nil)
	if len(got[0].Buckets) != summaryBuckets {
		t.Fatalf("want %d buckets, got %d", summaryBuckets, len(got[0].Buckets))
	}
	var total int
	for _, b := range got[0].Buckets {
		total += b.Count
	}
	if total != 3 {
		t.Errorf("histogram counts total %d, want 3", total)
	}
	if got[0].Buckets[0].Count != 2 {
		t.Errorf("two 0.0 scores should fall in the first bucket, got %d", got[0].Buckets[0].Count)
	}
	if got[0].Buckets[summaryBuckets-1].Count != 1 {
		t.Errorf("the max score should fall in the inclusive final bucket, got %d", got[0].Buckets[summaryBuckets-1].Count)
	}
}

// TestTrendBuckets proves Trend buckets by day and by week with correct per-bucket
// means, chronological order, and n_unscored counting.
func TestTrendBuckets(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time {
		return time.Date(y, m, day, 10, 0, 0, 0, time.UTC)
	}
	rs := []Result{
		scored("ns/a", f32(2.0), d(2026, 9, 14)), // Mon
		scored("ns/a", f32(4.0), d(2026, 9, 14)), // same day -> mean 3
		scored("ns/a", nil, d(2026, 9, 14)),      // unscored
		scored("ns/a", f32(6.0), d(2026, 9, 16)), // Wed (same ISO week as Mon 14th)
	}

	// Day buckets: 2026-09-14 (mean 3, 1 unscored) and 2026-09-16 (mean 6).
	day := Trend(rs, TrendDay)
	if len(day) != 2 {
		t.Fatalf("want 2 day buckets, got %d: %+v", len(day), day)
	}
	if day[0].Bucket != "2026-09-14" || day[1].Bucket != "2026-09-16" {
		t.Fatalf("day buckets not chronological: %s, %s", day[0].Bucket, day[1].Bucket)
	}
	if day[0].N != 2 || day[0].NUnscored != 1 || !approx(*day[0].Mean, 3.0) {
		t.Errorf("day[0] = n%d unscored%d mean%v, want 2/1/3", day[0].N, day[0].NUnscored, *day[0].Mean)
	}
	if !approx(*day[1].Mean, 6.0) {
		t.Errorf("day[1] mean = %v, want 6", *day[1].Mean)
	}

	// Week buckets: all four share the ISO week starting Mon 2026-09-14; mean of
	// {2,4,6} = 4, with one unscored.
	week := Trend(rs, TrendWeek)
	if len(week) != 1 {
		t.Fatalf("want 1 week bucket, got %d: %+v", len(week), week)
	}
	if week[0].Bucket != "2026-09-14" {
		t.Errorf("week bucket key = %s, want the Monday 2026-09-14", week[0].Bucket)
	}
	if week[0].N != 3 || week[0].NUnscored != 1 || !approx(*week[0].Mean, 4.0) {
		t.Errorf("week[0] = n%d unscored%d mean%v, want 3/1/4", week[0].N, week[0].NUnscored, *week[0].Mean)
	}
}

// TestTrendPerCriterion proves per-criterion means are parsed from the persisted
// CustomOutput.per_criterion blob and averaged per bucket. Scores are supplied as
// float64 (the type a JSON round-trip through the store yields).
func TestTrendPerCriterion(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	mk := func(overall float32, clarity, accuracy float64) Result {
		return Result{
			Template: TemplateRef{ID: "ns/rubric", Version: "1.0.0"},
			RunAt:    now,
			Outcome: Outcome{
				Score: f32(overall),
				CustomOutput: map[string]any{
					"per_criterion": []any{
						map[string]any{"group": "quality", "criterion": "clarity", "score": clarity},
						map[string]any{"group": "quality", "criterion": "accuracy", "score": accuracy},
					},
				},
			},
		}
	}
	rs := []Result{mk(3, 4, 2), mk(4, 2, 4)}

	pts := Trend(rs, TrendDay)
	if len(pts) != 1 {
		t.Fatalf("want 1 bucket, got %d", len(pts))
	}
	pc := pts[0].PerCriterion
	if len(pc) != 2 {
		t.Fatalf("want 2 per-criterion means, got %d: %+v", len(pc), pc)
	}
	// Sorted by group then criterion: accuracy before clarity.
	if pc[0].Criterion != "accuracy" || !approx(pc[0].Mean, 3.0) {
		t.Errorf("accuracy mean = %v (want 3.0), label=%q", pc[0].Mean, pc[0].Criterion)
	}
	if pc[1].Criterion != "clarity" || !approx(pc[1].Mean, 3.0) {
		t.Errorf("clarity mean = %v (want 3.0), label=%q", pc[1].Mean, pc[1].Criterion)
	}
}

// TestTrendNoPerCriterion proves a bucket with no per_criterion data reports a
// nil PerCriterion (no fabricated rows).
func TestTrendNoPerCriterion(t *testing.T) {
	now := time.Now().UTC()
	pts := Trend([]Result{scored("ns/a", f32(1.0), now)}, TrendDay)
	if len(pts) != 1 || pts[0].PerCriterion != nil {
		t.Errorf("expected nil PerCriterion for non-rubric results, got %+v", pts)
	}
}

// TestParsePerCriterionRobust proves the parser tolerates the numeric encodings a
// value may carry and skips malformed entries rather than failing.
func TestParsePerCriterionRobust(t *testing.T) {
	co := map[string]any{
		"per_criterion": []any{
			map[string]any{"group": "g", "criterion": "int", "score": 4},          // int
			map[string]any{"group": "g", "criterion": "f64", "score": float64(3)}, // float64
			map[string]any{"group": "g", "criterion": "bad"},                      // missing score -> skipped
			"not-a-map", // skipped
		},
	}
	got := parsePerCriterion(co)
	if len(got) != 2 {
		t.Fatalf("want 2 parsed entries, got %d: %+v", len(got), got)
	}
	if got[0].score != 4 || got[1].score != 3 {
		t.Errorf("parsed scores = %v, %v; want 4, 3", got[0].score, got[1].score)
	}
	// A nil/absent blob yields nil.
	if parsePerCriterion(nil) != nil || parsePerCriterion(map[string]any{}) != nil {
		t.Error("empty/absent CustomOutput should parse to nil per-criterion")
	}
}

// TestSummarizeEmpty proves an empty input yields an empty (non-panicking) result.
func TestSummarizeEmpty(t *testing.T) {
	if got := Summarize(nil, nil); len(got) != 0 {
		t.Errorf("Summarize(nil) = %+v, want empty", got)
	}
	if got := Trend(nil, TrendDay); len(got) != 0 {
		t.Errorf("Trend(nil) = %+v, want empty", got)
	}
}
