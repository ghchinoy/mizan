package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/results"
	"github.com/ghchinoy/mizan/internal/wire"
)

// TestStoreResultRecordErrorNonFatal proves a Record error (as opposed to the
// already-covered store-OPEN error) is also strictly non-fatal: the store opens
// cleanly but the INSERT fails, storeResult returns normally with a
// `warning: failed to persist result` line on stderr, and nothing is persisted.
//
// The failure is induced by handing storeResult a cmd whose Context is already
// cancelled: OpenResultService opens the DB without that context (migrate runs on
// context.Background), so Open succeeds, but Service.Record threads cmd.Context()
// into the sqlite ExecContext, which fails on a cancelled context — exercising the
// Record-error branch specifically.
func TestStoreResultRecordErrorNonFatal(t *testing.T) {
	cfg := sqliteConfig(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Record so the INSERT (ExecContext) fails, not the open
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)

	score := float32(2)
	storeResult(cmd, cfg, "eval run",
		registry.MetricTemplate{ID: "ns/x", Kind: registry.KindPointwise},
		eval.Instance{}, eval.Result{Score: &score})

	if !strings.Contains(errBuf.String(), "failed to persist result") {
		t.Errorf("expected a non-fatal record-error warning, got: %q", errBuf.String())
	}

	// A fresh (uncancelled) service must see zero rows: the failed Record persisted
	// nothing, and the eval itself was never affected (no panic, no exit change).
	svc, closeSvc, err := wire.OpenResultService(cfg)
	if err != nil {
		t.Fatalf("OpenResultService: %v", err)
	}
	defer func() { _ = closeSvc() }()
	rs, err := svc.List(context.Background(), results.ResultFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rs) != 0 {
		t.Errorf("record failed but %d row(s) persisted; want 0", len(rs))
	}
}

// seedResult persists one result into the results store at dbPath via the real
// write hook (no live API), so the list/show RunE wiring tests exercise the whole
// flag -> ResultFilter -> Service.List/Get path over a temp sqlite db.
func seedResult(t *testing.T, dbPath, id, version string) {
	t.Helper()
	cfg := &config.Config{
		ResultsBackend:   "sqlite",
		ResultsDBPath:    dbPath,
		ResultsRetention: "hybrid",
	}
	cmd, errBuf := newTestCmd()
	score := float32(3)
	storeResult(cmd, cfg, "eval run",
		registry.MetricTemplate{ID: id, Version: version, ContentHash: "abc123", Kind: registry.KindPointwise},
		eval.Instance{Fields: map[string]eval.AssetRef{
			"response": {Modality: registry.ModalityText, Text: "hi"},
		}},
		eval.Result{
			Score:       &score,
			Explanation: "good",
			Applied:     &eval.AppliedAutorater{Model: "gemini-x", ModelSource: "template"},
		})
	if errBuf.Len() != 0 {
		t.Fatalf("seed %q: unexpected warning: %q", id, errBuf.String())
	}
}

// TestResultsListFilterMappingRunE drives `results list`'s RunE end to end over a
// seeded temp db and proves the flags map onto results.ResultFilter correctly:
//   - --metric  -> TemplateID (exact match, one row)
//   - --namespace -> Namespace (all rows in that namespace)
//   - --limit   -> Limit (row cap)
//   - a bad --since is rejected locally with a crisp error (no store call)
func TestResultsListFilterMappingRunE(t *testing.T) {
	prev := outputFormat
	outputFormat = outputTable
	defer func() { outputFormat = prev }()

	dbPath := filepath.Join(t.TempDir(), "results.db")
	seedResult(t, dbPath, "brand-a/quality", "1.0.0")
	seedResult(t, dbPath, "brand-a/tone", "1.0.0")
	seedResult(t, dbPath, "brand-b/quality", "2.0.0")

	t.Setenv("MIZAN_RESULTS_BACKEND", "sqlite")
	t.Setenv("MIZAN_RESULTS_DB", dbPath)

	runList := func(t *testing.T, flags map[string]string) (string, error) {
		t.Helper()
		cmd := newResultsListCmd()
		cmd.SetContext(context.Background())
		var out, errb bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errb)
		for k, v := range flags {
			if err := cmd.Flags().Set(k, v); err != nil {
				t.Fatalf("set --%s=%s: %v", k, v, err)
			}
		}
		// Run BEFORE reading out: Go evaluates return operands left to right, so
		// returning out.String() alongside the RunE call would capture the buffer
		// before RunE writes to it.
		err := cmd.RunE(cmd, nil)
		return out.String(), err
	}

	t.Run("metric maps to TemplateID (exact)", func(t *testing.T) {
		out, err := runList(t, map[string]string{"metric": "brand-a/quality"})
		if err != nil {
			t.Fatalf("list --metric: %v", err)
		}
		if !strings.Contains(out, "brand-a/quality@1.0.0") {
			t.Errorf("expected the matching metric row:\n%s", out)
		}
		if strings.Contains(out, "brand-a/tone") || strings.Contains(out, "brand-b/quality") {
			t.Errorf("--metric is an exact filter but other rows leaked:\n%s", out)
		}
	})

	t.Run("namespace maps to Namespace", func(t *testing.T) {
		out, err := runList(t, map[string]string{"namespace": "brand-a"})
		if err != nil {
			t.Fatalf("list --namespace: %v", err)
		}
		if !strings.Contains(out, "brand-a/quality") || !strings.Contains(out, "brand-a/tone") {
			t.Errorf("namespace filter dropped an in-namespace row:\n%s", out)
		}
		if strings.Contains(out, "brand-b/quality") {
			t.Errorf("namespace filter leaked an out-of-namespace row:\n%s", out)
		}
	})

	t.Run("limit caps the rows", func(t *testing.T) {
		out, err := runList(t, map[string]string{"limit": "1"})
		if err != nil {
			t.Fatalf("list --limit: %v", err)
		}
		// One header line + exactly one data row (three seeded, capped to one).
		dataRows := 0
		for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
			if strings.HasPrefix(ln, "RUN ID") || strings.TrimSpace(ln) == "" {
				continue
			}
			dataRows++
		}
		if dataRows != 1 {
			t.Errorf("--limit 1 returned %d data rows:\n%s", dataRows, out)
		}
	})

	t.Run("bad --since is rejected locally", func(t *testing.T) {
		_, err := runList(t, map[string]string{"since": "not-a-date"})
		if err == nil {
			t.Fatal("expected an error for a garbage --since")
		}
		if !strings.Contains(err.Error(), "invalid --since") {
			t.Errorf("error = %v, want a crisp --since parse error", err)
		}
	})
}

// TestResultsShowFoundRunE proves `results show <run-id>` on a found result drives
// the RunE found path and renders full provenance (template id+version+hash,
// resolved model, score), and that -o json round-trips the whole Result.
func TestResultsShowFoundRunE(t *testing.T) {
	prev := outputFormat
	defer func() { outputFormat = prev }()

	dbPath := filepath.Join(t.TempDir(), "results.db")
	seedResult(t, dbPath, "brand-a/quality", "1.2.0")

	// Discover the ULID run id the write hook stamped.
	cfg := &config.Config{ResultsBackend: "sqlite", ResultsDBPath: dbPath, ResultsRetention: "hybrid"}
	svc, closeSvc, err := wire.OpenResultService(cfg)
	if err != nil {
		t.Fatalf("OpenResultService: %v", err)
	}
	rs, err := svc.List(context.Background(), results.ResultFilter{})
	_ = closeSvc()
	if err != nil || len(rs) != 1 {
		t.Fatalf("seed/list: err=%v rows=%d", err, len(rs))
	}
	runID := rs[0].RunID

	t.Setenv("MIZAN_RESULTS_BACKEND", "sqlite")
	t.Setenv("MIZAN_RESULTS_DB", dbPath)

	runShow := func(t *testing.T) string {
		t.Helper()
		cmd := newResultsShowCmd()
		cmd.SetContext(context.Background())
		var out, errb bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errb)
		if err := cmd.RunE(cmd, []string{runID}); err != nil {
			t.Fatalf("show %s: %v", runID, err)
		}
		return out.String()
	}

	t.Run("found renders full provenance (table)", func(t *testing.T) {
		outputFormat = outputTable
		s := runShow(t)
		for _, want := range []string{runID, "brand-a/quality", "1.2.0", "abc123", "gemini-x", "Score:"} {
			if !strings.Contains(s, want) {
				t.Errorf("show provenance missing %q:\n%s", want, s)
			}
		}
	})

	t.Run("-o json round-trips the whole Result", func(t *testing.T) {
		outputFormat = outputJSON
		s := runShow(t)
		var got results.Result
		if err := json.Unmarshal([]byte(s), &got); err != nil {
			t.Fatalf("json round-trip: %v\n%s", err, s)
		}
		if got.RunID != runID || got.Template.ContentHash != "abc123" || got.Template.Version != "1.2.0" {
			t.Errorf("json did not round-trip the whole Result: %+v", got)
		}
	})
}

// TestEvalResultJSONNoHookLeak is the no-output-regression guard: the write hook
// must not change `eval run`/`pairwise` stdout, so the RESOLVED autorater
// (eval.Result.Applied, tagged json:"-") and any store-side keys must never
// appear in the eval command's -o json output. This fails loudly if someone drops
// the json:"-" tag or otherwise leaks provenance into eval output.
func TestEvalResultJSONNoHookLeak(t *testing.T) {
	prev := outputFormat
	outputFormat = outputJSON
	defer func() { outputFormat = prev }()

	score := float32(3)
	res := eval.Result{
		Score:       &score,
		Explanation: "ok",
		Applied: &eval.AppliedAutorater{
			Model:       "resolved-model-should-not-leak",
			ModelSource: "template",
		},
	}
	var out bytes.Buffer
	if err := renderResult(&out, res, false); err != nil {
		t.Fatalf("renderResult: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("eval json is not an object: %v\n%s", err, out.String())
	}
	for _, k := range []string{"applied", "Applied", "autorater", "Autorater", "results", "Results"} {
		if _, ok := m[k]; ok {
			t.Errorf("eval -o json leaked hook/provenance key %q: %v", k, m)
		}
	}
	if strings.Contains(out.String(), "resolved-model-should-not-leak") {
		t.Errorf("the resolved autorater model leaked into eval output:\n%s", out.String())
	}
}
