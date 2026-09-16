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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/results"
)

// seedResultAt persists one result with a controllable score, RunAt and optional
// per-criterion CustomOutput via the real write hook (no live API), so the B3
// summary/trend verbs are exercised end to end over a temp sqlite db. A nil score
// seeds an unscored result (the genai-error shape B3 must exclude and count).
func seedResultAt(t *testing.T, dbPath, id, version string, score *float32, at time.Time, custom map[string]any) {
	t.Helper()
	cfg := &config.Config{
		ResultsBackend:   "sqlite",
		ResultsDBPath:    dbPath,
		ResultsRetention: "hybrid",
	}
	cmd, errBuf := newTestCmd()
	res := eval.Result{
		Score:        score,
		Explanation:  "seed",
		CustomOutput: custom,
		RubricDetail: custom != nil,
		Applied:      &eval.AppliedAutorater{Model: "gemini-x", ModelSource: "template"},
	}
	storeResult(cmd, cfg, "eval run",
		registry.MetricTemplate{ID: id, Version: version, ContentHash: "abc123", Kind: registry.KindPointwise},
		eval.Instance{Fields: map[string]eval.AssetRef{
			"response": {Modality: registry.ModalityText, Text: "hi"},
		}},
		res, storeHookOpts{RunAt: at})
	if errBuf.Len() != 0 {
		t.Fatalf("seed %q: unexpected warning: %q", id, errBuf.String())
	}
}

func fp(v float32) *float32 { return &v }

// TestResultsSummaryE2E drives `results summary` end to end over a seeded store
// (§9 B3): correct n/mean/min/max/stddev per template, --threshold pass/fail/
// passRate, and nil-score results excluded and counted as n_unscored.
func TestResultsSummaryE2E(t *testing.T) {
	cleanConfigEnv(t)
	dir := t.TempDir()
	resultsDB := filepath.Join(dir, "results.db")
	t.Setenv("MIZAN_RESULTS_BACKEND", "sqlite")
	t.Setenv("MIZAN_RESULTS_DB", resultsDB)

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	seedResultAt(t, resultsDB, "ns/a", "1.0.0", fp(1.0), now, nil)
	seedResultAt(t, resultsDB, "ns/a", "1.0.0", fp(3.0), now, nil)
	seedResultAt(t, resultsDB, "ns/a", "1.0.0", fp(5.0), now, nil)
	seedResultAt(t, resultsDB, "ns/a", "1.0.0", nil, now, nil) // unscored -> excluded, counted

	out, err := executeRoot(t, "--output", "json", "results", "summary", "--metric", "ns/a")
	if err != nil {
		t.Fatalf("results summary: %v (out=%q)", err, out)
	}
	var got []results.TemplateSummary
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode summary %q: %v", out, err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 summary, got %d: %s", len(got), out)
	}
	s := got[0]
	if s.N != 3 || s.NUnscored != 1 {
		t.Errorf("n/unscored = %d/%d, want 3/1", s.N, s.NUnscored)
	}
	if s.Mean == nil || math.Abs(*s.Mean-3.0) > 1e-9 {
		t.Errorf("mean = %v, want 3.0", s.Mean)
	}
	if s.Min == nil || *s.Min != 1.0 || s.Max == nil || *s.Max != 5.0 {
		t.Errorf("min/max = %v/%v, want 1/5", s.Min, s.Max)
	}
	if s.Stddev == nil || math.Abs(*s.Stddev-math.Sqrt(8.0/3.0)) > 1e-9 {
		t.Errorf("stddev = %v, want %v", s.Stddev, math.Sqrt(8.0/3.0))
	}

	// --threshold reports pass/fail/passRate (scores >= threshold pass).
	tout, err := executeRoot(t, "--output", "json", "results", "summary", "--metric", "ns/a", "--threshold", "3")
	if err != nil {
		t.Fatalf("results summary --threshold: %v (out=%q)", err, tout)
	}
	var tgot []results.TemplateSummary
	if err := json.Unmarshal([]byte(tout), &tgot); err != nil {
		t.Fatalf("decode threshold summary: %v", err)
	}
	ts := tgot[0].Threshold
	if ts == nil {
		t.Fatalf("expected threshold stats, got nil")
	}
	// scores {1,3,5} vs threshold 3 -> pass 2 (3,5), fail 1 (1).
	if ts.Pass != 2 || ts.Fail != 1 || math.Abs(ts.PassRate-2.0/3.0) > 1e-9 {
		t.Errorf("threshold stats = pass%d fail%d rate%v, want 2/1/0.667", ts.Pass, ts.Fail, ts.PassRate)
	}
}

// TestResultsSummaryTagE2E proves `results summary --tag` aggregates over the
// Phase-2 registry->results join with AND-narrowing (§9 B3).
func TestResultsSummaryTagE2E(t *testing.T) {
	cleanConfigEnv(t)
	dir := t.TempDir()
	registryDB := filepath.Join(dir, "registry.db")
	resultsDB := filepath.Join(dir, "results.db")
	t.Setenv("MIZAN_REGISTRY_DB", registryDB)
	t.Setenv("MIZAN_RESULTS_BACKEND", "sqlite")
	t.Setenv("MIZAN_RESULTS_DB", resultsDB)

	create := func(id string, tags ...string) {
		t.Helper()
		args := []string{"registry", "create", "--id", id, "--kind", "single", "--prompt", "Judge {{response}}"}
		for _, tag := range tags {
			args = append(args, "--tag", tag)
		}
		if out, err := executeRoot(t, args...); err != nil {
			t.Fatalf("registry create %s: %v (out=%q)", id, err, out)
		}
	}
	create("brand-a/quality", "brand", "safety")
	create("brand-b/tone", "brand")
	create("other-c/style", "other")

	now := time.Now().UTC()
	seedResultAt(t, resultsDB, "brand-a/quality", "1.0.0", fp(2.0), now, nil)
	seedResultAt(t, resultsDB, "brand-b/tone", "1.0.0", fp(4.0), now, nil)
	seedResultAt(t, resultsDB, "other-c/style", "1.0.0", fp(1.0), now, nil)

	// --tag brand aggregates BOTH brand templates, not the "other" one.
	out, err := executeRoot(t, "--output", "json", "results", "summary", "--tag", "brand")
	if err != nil {
		t.Fatalf("summary --tag brand: %v (out=%q)", err, out)
	}
	var got []results.TemplateSummary
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ids := map[string]bool{}
	for _, s := range got {
		ids[s.TemplateID] = true
	}
	if !ids["brand-a/quality"] || !ids["brand-b/tone"] || ids["other-c/style"] {
		t.Errorf("--tag brand aggregated the wrong templates: %v", ids)
	}

	// AND-narrowing: --tag brand --tag safety -> only brand-a/quality.
	andOut, err := executeRoot(t, "--output", "json", "results", "summary", "--tag", "brand", "--tag", "safety")
	if err != nil {
		t.Fatalf("summary AND: %v (out=%q)", err, andOut)
	}
	var andGot []results.TemplateSummary
	if err := json.Unmarshal([]byte(andOut), &andGot); err != nil {
		t.Fatalf("decode AND: %v", err)
	}
	if len(andGot) != 1 || andGot[0].TemplateID != "brand-a/quality" {
		t.Errorf("--tag brand --tag safety should narrow to brand-a/quality, got %s", andOut)
	}

	// --metric + --tag is rejected.
	if _, err := executeRoot(t, "results", "summary", "--metric", "brand-a/quality", "--tag", "brand"); err == nil {
		t.Error("expected --metric + --tag to be rejected")
	}
}

// TestResultsTagWindowBeforeLimitE2E is the EM re-review regression (#94): on the
// --tag path the [since, until] window MUST be applied before --limit, so both
// `results summary --tag` and `results list --tag` window consistently with the
// direct --metric path. Seeds four dated results for a tagged template and proves
// `--until T --limit N` returns the newest-N IN-WINDOW rows (not the newest-N
// overall then windowed, which would drop the newest in-window rows).
func TestResultsTagWindowBeforeLimitE2E(t *testing.T) {
	cleanConfigEnv(t)
	dir := t.TempDir()
	registryDB := filepath.Join(dir, "registry.db")
	resultsDB := filepath.Join(dir, "results.db")
	t.Setenv("MIZAN_REGISTRY_DB", registryDB)
	t.Setenv("MIZAN_RESULTS_BACKEND", "sqlite")
	t.Setenv("MIZAN_RESULTS_DB", resultsDB)

	if out, err := executeRoot(t, "registry", "create", "--id", "brand-a/quality",
		"--kind", "single", "--prompt", "Judge {{response}}", "--tag", "brand"); err != nil {
		t.Fatalf("registry create: %v (out=%q)", err, out)
	}

	d := func(day int) time.Time { return time.Date(2026, 9, day, 10, 0, 0, 0, time.UTC) }
	// scores by day: 10->1, 14->2, 16->3, 20->4.
	seedResultAt(t, resultsDB, "brand-a/quality", "1.0.0", fp(1.0), d(10), nil)
	seedResultAt(t, resultsDB, "brand-a/quality", "1.0.0", fp(2.0), d(14), nil)
	seedResultAt(t, resultsDB, "brand-a/quality", "1.0.0", fp(3.0), d(16), nil)
	seedResultAt(t, resultsDB, "brand-a/quality", "1.0.0", fp(4.0), d(20), nil)

	// summary --tag brand --until 2026-09-17 --limit 2 (bare dates parse to UTC
	// midnight — 09-17 00:00 includes the 09-16 10:00 run, excludes 09-20, exactly
	// as the --metric store path treats Until):
	//   window (< 09-17) keeps {1,2,3} -> newest-first 3,2,1 -> limit 2 -> {3,2}.
	//   N=2, mean 2.5. The buggy limit-before-until path would keep the newest 2
	//   overall {4,3}, then drop 4 (> until), yielding N=1, mean 3.
	out, err := executeRoot(t, "--output", "json", "results", "summary",
		"--tag", "brand", "--until", "2026-09-17", "--limit", "2")
	if err != nil {
		t.Fatalf("summary windowed: %v (out=%q)", err, out)
	}
	var got []results.TemplateSummary
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 summary, got %d: %s", len(got), out)
	}
	if got[0].N != 2 {
		t.Errorf("N = %d, want 2 (window before limit); got summary %+v", got[0].N, got[0])
	}
	if got[0].Mean == nil || math.Abs(*got[0].Mean-2.5) > 1e-9 {
		t.Errorf("mean = %v, want 2.5 (newest-2 in-window scores {3,2})", got[0].Mean)
	}

	// summary --tag brand --since 2026-09-14 --until 2026-09-17: window keeps {2,3}.
	winOut, err := executeRoot(t, "--output", "json", "results", "summary",
		"--tag", "brand", "--since", "2026-09-14", "--until", "2026-09-17")
	if err != nil {
		t.Fatalf("summary since+until: %v (out=%q)", err, winOut)
	}
	var win []results.TemplateSummary
	if err := json.Unmarshal([]byte(winOut), &win); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if win[0].N != 2 || win[0].Mean == nil || math.Abs(*win[0].Mean-2.5) > 1e-9 {
		t.Errorf("since+until window: N=%d mean=%v, want 2/2.5", win[0].N, win[0].Mean)
	}

	// list --tag brand --since 2026-09-14 --limit 2 (list has no --until): window
	// (>= 09-14) keeps {2,3,4} -> newest-first 4,3,2 -> limit 2 -> {4,3}.
	listOut, err := executeRoot(t, "--output", "json", "results", "list",
		"--tag", "brand", "--since", "2026-09-14", "--limit", "2")
	if err != nil {
		t.Fatalf("list windowed: %v (out=%q)", err, listOut)
	}
	var listGot []results.Result
	if err := json.Unmarshal([]byte(listOut), &listGot); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listGot) != 2 {
		t.Fatalf("list --since --limit returned %d rows, want 2: %s", len(listGot), listOut)
	}
	// Newest-first within the window: 09-20 (score 4) then 09-16 (score 3).
	if s0, s1 := *listGot[0].Outcome.Score, *listGot[1].Outcome.Score; s0 != 4.0 || s1 != 3.0 {
		t.Errorf("list windowed scores = %v,%v, want 4,3 (newest-2 in-window)", s0, s1)
	}
}

// TestResultsTrendE2E drives `results trend` end to end (§9 B3): correct per-bucket
// means for day and week, and --per-criterion means parsed from persisted
// CustomOutput.
func TestResultsTrendE2E(t *testing.T) {
	cleanConfigEnv(t)
	dir := t.TempDir()
	resultsDB := filepath.Join(dir, "results.db")
	t.Setenv("MIZAN_RESULTS_BACKEND", "sqlite")
	t.Setenv("MIZAN_RESULTS_DB", resultsDB)

	d := func(day int) time.Time { return time.Date(2026, 9, day, 10, 0, 0, 0, time.UTC) }
	seedResultAt(t, resultsDB, "ns/a", "1.0.0", fp(2.0), d(14), nil) // Mon
	seedResultAt(t, resultsDB, "ns/a", "1.0.0", fp(4.0), d(14), nil) // same day -> mean 3
	seedResultAt(t, resultsDB, "ns/a", "1.0.0", fp(6.0), d(16), nil) // Wed, same ISO week

	// Day buckets.
	dayOut, err := executeRoot(t, "--output", "json", "results", "trend", "--metric", "ns/a", "--bucket", "day")
	if err != nil {
		t.Fatalf("trend day: %v (out=%q)", err, dayOut)
	}
	var day []results.TrendPoint
	if err := json.Unmarshal([]byte(dayOut), &day); err != nil {
		t.Fatalf("decode day: %v", err)
	}
	if len(day) != 2 || day[0].Bucket != "2026-09-14" || day[1].Bucket != "2026-09-16" {
		t.Fatalf("day buckets wrong: %s", dayOut)
	}
	if day[0].Mean == nil || math.Abs(*day[0].Mean-3.0) > 1e-9 {
		t.Errorf("day[0] mean = %v, want 3", day[0].Mean)
	}

	// Week buckets: all three collapse into the Mon 2026-09-14 week; mean 4.
	weekOut, err := executeRoot(t, "--output", "json", "results", "trend", "--metric", "ns/a", "--bucket", "week")
	if err != nil {
		t.Fatalf("trend week: %v (out=%q)", err, weekOut)
	}
	var week []results.TrendPoint
	if err := json.Unmarshal([]byte(weekOut), &week); err != nil {
		t.Fatalf("decode week: %v", err)
	}
	if len(week) != 1 || week[0].Bucket != "2026-09-14" || week[0].Mean == nil || math.Abs(*week[0].Mean-4.0) > 1e-9 {
		t.Errorf("week bucket wrong: %s", weekOut)
	}

	// --bucket garbage is rejected locally.
	if _, err := executeRoot(t, "results", "trend", "--metric", "ns/a", "--bucket", "month"); err == nil {
		t.Error("expected an error for --bucket month")
	}
	// --metric is required.
	if _, err := executeRoot(t, "results", "trend"); err == nil {
		t.Error("expected an error when --metric is omitted")
	}
}

// TestResultsTrendPerCriterionE2E proves --per-criterion emits per-criterion means
// parsed from the persisted rubric-detail CustomOutput (§9 B3).
func TestResultsTrendPerCriterionE2E(t *testing.T) {
	cleanConfigEnv(t)
	dir := t.TempDir()
	resultsDB := filepath.Join(dir, "results.db")
	t.Setenv("MIZAN_RESULTS_BACKEND", "sqlite")
	t.Setenv("MIZAN_RESULTS_DB", resultsDB)

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	mkCustom := func(clarity, accuracy float64) map[string]any {
		return map[string]any{
			"per_criterion": []any{
				map[string]any{"group": "quality", "criterion": "clarity", "score": clarity},
				map[string]any{"group": "quality", "criterion": "accuracy", "score": accuracy},
			},
			"overall_score": 3.0,
			"explanation":   "ok",
		}
	}
	seedResultAt(t, resultsDB, "ns/rubric", "1.0.0", fp(3.0), now, mkCustom(4, 2))
	seedResultAt(t, resultsDB, "ns/rubric", "1.0.0", fp(4.0), now, mkCustom(2, 4))

	out, err := executeRoot(t, "--output", "json", "results", "trend", "--metric", "ns/rubric", "--per-criterion")
	if err != nil {
		t.Fatalf("trend --per-criterion: %v (out=%q)", err, out)
	}
	var pts []results.TrendPoint
	if err := json.Unmarshal([]byte(out), &pts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("want 1 bucket, got %d: %s", len(pts), out)
	}
	pc := pts[0].PerCriterion
	if len(pc) != 2 {
		t.Fatalf("want 2 per-criterion means, got %d: %s", len(pc), out)
	}
	// Both criteria mean to 3.0; sorted accuracy then clarity.
	if pc[0].Criterion != "accuracy" || math.Abs(pc[0].Mean-3.0) > 1e-9 {
		t.Errorf("accuracy mean wrong: %+v", pc[0])
	}
	if pc[1].Criterion != "clarity" || math.Abs(pc[1].Mean-3.0) > 1e-9 {
		t.Errorf("clarity mean wrong: %+v", pc[1])
	}
}

// TestResultsAggregationNoEvalSetOrCost is the honest-scope guard (§9 B3): the
// aggregation surface must NOT synthesize per-eval-set or cost/token rows. Over a
// store with only single-eval results, neither summary nor trend JSON carries any
// scorecard/cost/token key — proving no fake data is emitted.
func TestResultsAggregationNoEvalSetOrCost(t *testing.T) {
	cleanConfigEnv(t)
	dir := t.TempDir()
	resultsDB := filepath.Join(dir, "results.db")
	t.Setenv("MIZAN_RESULTS_BACKEND", "sqlite")
	t.Setenv("MIZAN_RESULTS_DB", resultsDB)

	seedResultAt(t, resultsDB, "ns/a", "1.0.0", fp(2.0), time.Now().UTC(), nil)

	for _, args := range [][]string{
		{"--output", "json", "results", "summary", "--metric", "ns/a"},
		{"--output", "json", "results", "trend", "--metric", "ns/a"},
	} {
		out, err := executeRoot(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		lower := strings.ToLower(out)
		for _, forbidden := range []string{"scorecard", "eval_set", "evalset", "cost", "token"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("%v output leaked an out-of-scope %q key (no fake data allowed): %s", args, forbidden, out)
			}
		}
	}
}
