package main

import (
	"bytes"
	"context"
	"os"
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

// newTestCmd returns a cobra.Command with a background context and a captured
// stderr, so storeResult (which writes warnings to cmd.ErrOrStderr) can be
// exercised without a live run.
func newTestCmd() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	return cmd, &errBuf
}

// sqliteConfig returns a config pointing the results store at a fresh temp
// sqlite db (no live API, no network).
func sqliteConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		ProjectID:        "test-project",
		Location:         "us-central1",
		ResultsBackend:   "sqlite",
		ResultsDBPath:    filepath.Join(t.TempDir(), "results.db"),
		ResultsRetention: "hybrid",
	}
}

// TestStoreResultRoundTrip proves the write hook persists exactly one Result with
// the applied autorater copied field-for-field, retrievable via a fresh
// Service.List (the store seam works end to end over a temp sqlite db).
func TestStoreResultRoundTrip(t *testing.T) {
	cfg := sqliteConfig(t)
	cmd, errBuf := newTestCmd()

	tmpl := registry.MetricTemplate{
		ID:          "google-brand/quality",
		Version:     "1.2.0",
		ContentHash: "deadbeef",
		Kind:        registry.KindPointwise,
	}
	inst := eval.Instance{Fields: map[string]eval.AssetRef{
		"response": {Modality: registry.ModalityText, Text: "hello"},
	}}
	score := float32(4)
	res := eval.Result{
		Score:       &score,
		Explanation: "good",
		Applied: &eval.AppliedAutorater{
			Model:         "publishers/google/models/gemini-2.5-pro",
			SamplingCount: 3,
			FlipEnabled:   true,
			EffectiveHost: "regional",
			Location:      "us-central1",
			ModelSource:   "template",
		},
	}

	storeResult(cmd, cfg, "eval run", tmpl, inst, res)
	if errBuf.Len() != 0 {
		t.Fatalf("unexpected warning on a healthy store: %q", errBuf.String())
	}

	svc, closeSvc, err := wire.OpenResultService(cfg)
	if err != nil {
		t.Fatalf("OpenResultService: %v", err)
	}
	defer func() { _ = closeSvc() }()

	rs, err := svc.List(context.Background(), results.ResultFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d results, want exactly 1", len(rs))
	}
	got := rs[0]
	if got.Template.ID != "google-brand/quality" || got.Template.Version != "1.2.0" || got.Template.ContentHash != "deadbeef" {
		t.Errorf("template ref not persisted faithfully: %+v", got.Template)
	}
	if got.Autorater.Model != "publishers/google/models/gemini-2.5-pro" ||
		got.Autorater.SamplingCount != 3 || !got.Autorater.FlipEnabled ||
		got.Autorater.EffectiveHost != "regional" || got.Autorater.Location != "us-central1" ||
		got.Autorater.ModelSource != "template" {
		t.Errorf("applied autorater not copied field-for-field: %+v", got.Autorater)
	}
	if got.Invocation.Command != "eval run" {
		t.Errorf("command = %q, want \"eval run\"", got.Invocation.Command)
	}
	if got.Outcome.Score == nil || *got.Outcome.Score != 4 {
		t.Errorf("outcome score not persisted: %+v", got.Outcome)
	}
}

// TestStoreResultAppliedNilZeroValue proves a nil eval.Result.Applied (a failed
// or applied-less run) maps to a zero-value results.AppliedAutorater rather than
// panicking, and still records the result.
func TestStoreResultAppliedNilZeroValue(t *testing.T) {
	cfg := sqliteConfig(t)
	cmd, errBuf := newTestCmd()

	score := float32(1)
	res := eval.Result{Score: &score, Applied: nil}
	storeResult(cmd, cfg, "eval run",
		registry.MetricTemplate{ID: "ns/x", Version: "0.1.0", Kind: registry.KindPointwise},
		eval.Instance{}, res)
	if errBuf.Len() != 0 {
		t.Fatalf("unexpected warning: %q", errBuf.String())
	}

	svc, closeSvc, err := wire.OpenResultService(cfg)
	if err != nil {
		t.Fatalf("OpenResultService: %v", err)
	}
	defer func() { _ = closeSvc() }()
	rs, err := svc.List(context.Background(), results.ResultFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d results, want 1", len(rs))
	}
	if rs[0].Autorater != (results.AppliedAutorater{}) {
		t.Errorf("nil Applied did not map to zero value: %+v", rs[0].Autorater)
	}
}

// TestStoreResultOpenErrorNonFatal proves a store-open failure (unwritable path)
// is non-fatal: storeResult returns normally and emits a `warning:` on stderr.
func TestStoreResultOpenErrorNonFatal(t *testing.T) {
	cfg := sqliteConfig(t)
	// A path under a non-directory component cannot be created/opened.
	cfg.ResultsDBPath = filepath.Join(t.TempDir(), "not-a-dir", "nested", "results.db")
	// Make the intermediate a file so the dir cannot be created.
	badParent := filepath.Dir(filepath.Dir(cfg.ResultsDBPath))
	if err := os.WriteFile(badParent, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cmd, errBuf := newTestCmd()
	storeResult(cmd, cfg, "eval run",
		registry.MetricTemplate{ID: "ns/x", Kind: registry.KindPointwise},
		eval.Instance{}, eval.Result{})
	if !strings.Contains(errBuf.String(), "warning:") {
		t.Errorf("expected a non-fatal warning on store-open failure, got: %q", errBuf.String())
	}
}

// TestStoreResultUnknownBackendNonFatal proves an unknown/unsupported backend
// (firestore in Phase 1) is a non-fatal warning, not a failed eval.
func TestStoreResultUnknownBackendNonFatal(t *testing.T) {
	cfg := sqliteConfig(t)
	cfg.ResultsBackend = "firestore"
	cmd, errBuf := newTestCmd()
	storeResult(cmd, cfg, "eval run",
		registry.MetricTemplate{ID: "ns/x", Kind: registry.KindPointwise},
		eval.Instance{}, eval.Result{})
	if !strings.Contains(errBuf.String(), "warning:") {
		t.Errorf("expected a non-fatal warning on an unimplemented backend, got: %q", errBuf.String())
	}
}

// TestEvalRunNoStoreFlag proves --no-store exists on eval run and defaults to
// false (persistence is on by default).
func TestEvalRunNoStoreFlag(t *testing.T) {
	cmd := newEvalRunCmd()
	f := cmd.Flags().Lookup("no-store")
	if f == nil {
		t.Fatal("eval run is missing the --no-store flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--no-store default = %q, want false (persistence on by default)", f.DefValue)
	}
	if err := cmd.Flags().Parse([]string{"--metric", "m", "--no-store"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v, _ := cmd.Flags().GetBool("no-store"); !v {
		t.Error("--no-store did not parse to true")
	}
}

// TestEvalPairwiseNoStoreFlag proves --no-store exists on eval pairwise too.
func TestEvalPairwiseNoStoreFlag(t *testing.T) {
	cmd := newEvalPairwiseCmd()
	if cmd.Flags().Lookup("no-store") == nil {
		t.Fatal("eval pairwise is missing the --no-store flag")
	}
}
