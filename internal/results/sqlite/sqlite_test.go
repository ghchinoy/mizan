package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/results"
)

func i(v int) *int           { return &v }
func f32(v float32) *float32 { return &v }

// fullResult exercises every field of results.Result so the round-trip test
// proves persistence loses nothing.
func fullResult(runID string, runAt time.Time) results.Result {
	return results.Result{
		RunID:   runID,
		RunAt:   runAt,
		RunKind: results.RunKindSingle,
		Mizan:   results.MizanBuild{Version: "v1.2.3", Commit: "abc1234", Date: "2026-08-16"},
		Invocation: results.Invocation{
			Command: "eval run", ProjectID: "proj-123", Location: "us-central1",
			HostLabel: "host-1", Actor: "jane",
		},
		Template: results.TemplateRef{
			ID: "google-brand/helpfulness", Version: "1.2.3", ContentHash: "deadbeef",
			Kind: registry.KindRubric, Source: "pack:google-brand@origin",
		},
		Autorater: results.AppliedAutorater{
			Model: "gemini-2.5-flash", SamplingCount: 4, FlipEnabled: true,
			EffectiveHost: "global", Location: "global", ModelSource: "template",
		},
		Rubric: &results.RubricRef{
			Method: "authored", ScaleMin: i(1), ScaleMax: i(5), DetailMode: true,
		},
		Inputs: []results.StoredInput{
			{Field: "response", Modality: registry.ModalityText, ContentHash: "h1", Mode: results.ModeInline, Inline: "hello"},
			{Field: "image", Modality: registry.ModalityImage, ContentHash: "h2", Mode: results.ModeReference, URI: "gs://b/x.png", MimeType: "image/png"},
		},
		Outcome: results.Outcome{
			Score: f32(0.9), Explanation: "solid", RubricDetail: true,
			CustomOutput: map[string]any{"k": "v"}, Warnings: []string{"note"},
			DurationNS: int64(1500 * time.Millisecond),
			TokenUsage: &results.TokenUsage{PromptTokens: 10, CandidatesTokens: 20, TotalTokens: 30},
		},
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "results.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestPutGetRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runAt := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	want := fullResult("01AAAAAAAAAAAAAAAAAAAAAAAA", runAt)

	if err := s.Put(ctx, &want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, want.RunID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", *got, want)
	}
}

func TestGetNotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.Get(context.Background(), "does-not-exist")
	if !errors.Is(err, results.ErrNotFound) {
		t.Fatalf("Get err = %v, want ErrNotFound", err)
	}
}

func TestPutImmutableRejectsDuplicate(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	r := fullResult("01BBBBBBBBBBBBBBBBBBBBBBBB", time.Now().UTC())
	if err := s.Put(ctx, &r); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	if err := s.Put(ctx, &r); err == nil {
		t.Fatal("second Put on same RunID succeeded, want error (results are immutable)")
	}
}

func TestPutNilAndEmptyID(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Put(ctx, nil); err == nil {
		t.Error("Put(nil) = nil error, want error")
	}
	if err := s.Put(ctx, &results.Result{}); err == nil {
		t.Error("Put(empty RunID) = nil error, want error")
	}
}

// TestPutMarshalErrorNoRow proves Put propagates a json.Marshal failure (an
// unmarshalable value in a caller-supplied field) instead of silently
// persisting "null", and that NO row is written when it does.
func TestPutMarshalErrorNoRow(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	r := fullResult("01HHHHHHHHHHHHHHHHHHHHHHHH", time.Now().UTC())
	// A channel value cannot be JSON-marshaled; carry it in CustomOutput.
	r.Outcome.CustomOutput = map[string]any{"bad": make(chan int)}

	err := s.Put(ctx, &r)
	if err == nil {
		t.Fatal("Put with unmarshalable field = nil error, want marshal error")
	}
	var jsonErr *json.UnsupportedTypeError
	if !errors.As(err, &jsonErr) {
		t.Errorf("Put err = %v, want a json.UnsupportedTypeError in the chain", err)
	}

	// Nothing must have been persisted.
	if _, err := s.Get(ctx, r.RunID); !errors.Is(err, results.ErrNotFound) {
		t.Errorf("Get after failed Put err = %v, want ErrNotFound (no row written)", err)
	}
}

func TestJSONWholeRecordRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	orig := fullResult("01CCCCCCCCCCCCCCCCCCCCCCCC", time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC))

	// marshal -> Put -> Get -> marshal must equal the original marshalling.
	origJSON, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal orig: %v", err)
	}
	if err := s.Put(ctx, &orig); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, orig.RunID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	gotJSON, err := json.Marshal(*got)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	if string(origJSON) != string(gotJSON) {
		t.Errorf("JSON round-trip mismatch:\n orig=%s\n got=%s", origJSON, gotJSON)
	}
}

func TestListFilters(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)

	mk := func(id, runID, ver string, kind registry.MetricKind, at time.Time) {
		r := results.Result{
			RunID: runID, RunAt: at, RunKind: results.RunKindSingle,
			Template: results.TemplateRef{ID: id, Version: ver, Kind: kind},
		}
		if err := s.Put(ctx, &r); err != nil {
			t.Fatalf("Put %s: %v", runID, err)
		}
	}
	mk("google-brand/help", "01D0000000000000000000000A", "1.0.0", registry.KindRubric, base)
	mk("google-brand/help", "01D0000000000000000000000B", "2.0.0", registry.KindRubric, base.Add(time.Hour))
	mk("acme/tone", "01D0000000000000000000000C", "1.0.0", registry.KindPointwise, base.Add(2*time.Hour))

	// No filter: all three, newest first.
	all, err := s.List(ctx, results.ResultFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List all = %d, want 3", len(all))
	}
	if all[0].RunAt.Before(all[1].RunAt) || all[1].RunAt.Before(all[2].RunAt) {
		t.Errorf("List not ordered newest-first: %v", []time.Time{all[0].RunAt, all[1].RunAt, all[2].RunAt})
	}

	// TemplateID.
	byID, _ := s.List(ctx, results.ResultFilter{TemplateID: "google-brand/help"})
	if len(byID) != 2 {
		t.Errorf("by template id = %d, want 2", len(byID))
	}
	// TemplateVersion.
	byVer, _ := s.List(ctx, results.ResultFilter{TemplateID: "google-brand/help", TemplateVersion: "2.0.0"})
	if len(byVer) != 1 || byVer[0].Template.Version != "2.0.0" {
		t.Errorf("by version = %+v, want 1 with v2.0.0", byVer)
	}
	// Namespace.
	byNS, _ := s.List(ctx, results.ResultFilter{Namespace: "acme"})
	if len(byNS) != 1 || byNS[0].Template.ID != "acme/tone" {
		t.Errorf("by namespace = %+v, want acme/tone", byNS)
	}
	// Kind.
	byKind, _ := s.List(ctx, results.ResultFilter{Kind: registry.KindPointwise})
	if len(byKind) != 1 || byKind[0].Template.Kind != registry.KindPointwise {
		t.Errorf("by kind = %+v, want 1 pointwise", byKind)
	}
	// Since / Until.
	since, _ := s.List(ctx, results.ResultFilter{Since: base.Add(90 * time.Minute)})
	if len(since) != 1 {
		t.Errorf("since = %d, want 1", len(since))
	}
	until, _ := s.List(ctx, results.ResultFilter{Until: base.Add(30 * time.Minute)})
	if len(until) != 1 {
		t.Errorf("until = %d, want 1", len(until))
	}
	// Limit.
	lim, _ := s.List(ctx, results.ResultFilter{Limit: 2})
	if len(lim) != 2 {
		t.Errorf("limit = %d, want 2", len(lim))
	}
	// ScorecardRunID is always empty in Phase 1: a non-empty filter matches none.
	sc, _ := s.List(ctx, results.ResultFilter{ScorecardRunID: "sc-1"})
	if len(sc) != 0 {
		t.Errorf("scorecard filter = %d, want 0 (Phase 1)", len(sc))
	}
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	r := fullResult("01E0000000000000000000000A", time.Now().UTC())
	if err := s.Put(ctx, &r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete(ctx, r.RunID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, r.RunID); !errors.Is(err, results.ErrNotFound) {
		t.Errorf("Get after Delete err = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, r.RunID); !errors.Is(err, results.ErrNotFound) {
		t.Errorf("Delete absent err = %v, want ErrNotFound", err)
	}
}

func TestListChangedSince(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	mk := func(runID string, at time.Time) {
		r := results.Result{RunID: runID, RunAt: at, Template: results.TemplateRef{ID: "ns/x"}}
		if err := s.Put(ctx, &r); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	mk("01F0000000000000000000000A", t0)
	mk("01F0000000000000000000000B", t0.Add(time.Hour))

	got, err := s.ListChangedSince(ctx, t0.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("ListChangedSince: %v", err)
	}
	if len(got) != 1 || got[0].RunID != "01F0000000000000000000000B" {
		t.Errorf("ListChangedSince = %+v, want the later result only", got)
	}
}

func TestMemoryDB(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	r := fullResult("01G0000000000000000000000A", time.Now().UTC())
	if err := s.Put(ctx, &r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := s.Get(ctx, r.RunID); err != nil {
		t.Fatalf("Get from :memory:: %v", err)
	}
}

func TestMaxOpenConns(t *testing.T) {
	s := newStore(t)
	if got := s.db.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", got)
	}
}

// TestFileMode mirrors the registry/sqlite test: the results DB must be
// owner-only (0600) in an owner-only dir (0700), since it holds recorded inputs.
func TestFileMode(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	path := filepath.Join(sub, "results.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	di, err := os.Stat(sub)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("db dir perm = %04o, want 0700", perm)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("db file perm = %04o, want 0600", perm)
	}
}
