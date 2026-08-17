package wire

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/results"
)

// TestOpenResultServiceSQLite verifies the composition root builds a working
// results.Service over the SQLite backend and returns a usable close func — and
// that cmd/* need only the results.Service (never results/sqlite).
func TestOpenResultServiceSQLite(t *testing.T) {
	cfg := &config.Config{
		ResultsBackend:   "sqlite",
		ResultsDBPath:    filepath.Join(t.TempDir(), "results.db"),
		ResultsRetention: "hybrid",
	}
	svc, closeFn, err := OpenResultService(cfg)
	if err != nil {
		t.Fatalf("OpenResultService: %v", err)
	}
	if svc == nil {
		t.Fatal("nil service")
	}
	t.Cleanup(func() { _ = closeFn() })

	// End-to-end sanity: Record then List through the wired service.
	rec, err := svc.Record(context.Background(), results.RecordInput{
		Command:  "eval run",
		Template: registry.MetricTemplate{ID: "ns/x", Kind: registry.KindPointwise},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, err := svc.Get(context.Background(), rec.RunID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RunID != rec.RunID {
		t.Errorf("Get RunID = %q, want %q", got.RunID, rec.RunID)
	}
}

// TestOpenResultServiceDefaultBackend confirms an empty backend defaults to
// sqlite (matching cfg.ResultsBackend's default).
func TestOpenResultServiceDefaultBackend(t *testing.T) {
	cfg := &config.Config{ResultsDBPath: filepath.Join(t.TempDir(), "results.db")}
	svc, closeFn, err := OpenResultService(cfg)
	if err != nil {
		t.Fatalf("OpenResultService: %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })
	if svc == nil {
		t.Fatal("nil service")
	}
}

// TestOpenResultServiceFirestoreNotImplemented confirms firestore returns a
// clear Phase-1 not-implemented error (no firestore backend is built).
func TestOpenResultServiceFirestoreNotImplemented(t *testing.T) {
	cfg := &config.Config{ResultsBackend: "firestore"}
	_, _, err := OpenResultService(cfg)
	if err == nil {
		t.Fatal("OpenResultService(firestore) = nil error, want not-implemented error")
	}
}

// TestOpenResultServiceUnknownBackend confirms an unknown backend is rejected.
func TestOpenResultServiceUnknownBackend(t *testing.T) {
	cfg := &config.Config{ResultsBackend: "bogus"}
	_, _, err := OpenResultService(cfg)
	if err == nil {
		t.Fatal("OpenResultService(bogus) = nil error, want error")
	}
}
