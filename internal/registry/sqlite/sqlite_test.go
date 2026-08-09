package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/registry"
)

// fullTemplate exercises every field of MetricTemplate so the round-trip test
// proves CRUD loses nothing (acceptance criterion 5).
func fullTemplate() registry.MetricTemplate {
	ts := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	return registry.MetricTemplate{
		ID:          "google-brand/helpfulness",
		Name:        "Helpfulness",
		Description: "Scores how helpful a response is.",
		Version:     "1.2.3",
		Authors:     []registry.Author{{Name: "Jane Doe", Email: "jane@example.com"}},
		Maintainers: []string{"google-brand-team"},
		License:     "Apache-2.0",
		Tags:        []string{"quality", "helpfulness"},

		Kind:       registry.KindPointwise,
		Modalities: []registry.Modality{registry.ModalityText},
		Inputs: []registry.InputSpec{
			{Name: "response", Modality: registry.ModalityText, Required: true},
			{Name: "context", Modality: registry.ModalityText, Required: false},
		},
		MetricPromptTemplate: "Rate the helpfulness of: {{response}}",
		SystemInstruction:    "Be strict.",
		CandidateFieldName:   "candidate",
		BaselineFieldName:    "baseline",
		RubricGroups: map[string][]string{
			"quality": {"Is it clear?", "Is it correct?"},
		},
		ResponseSchema: &registry.Schema{JSON: `{"type":"object"}`},
		AutoraterModel: "gemini-2.5-flash",
		SamplingCount:  4,
		FlipEnabled:    true,

		Source:      "pack:google-brand@github.com/ghchinoy/mizan-templates",
		ContentHash: "abc123",
		Dirty:       true,
		CreatedAt:   ts,
		UpdatedAt:   ts,
		ImportedAt:  ts,
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	// Use a temp file DB (modernc :memory: works too, but a file proves the
	// mkdir + on-disk path used in production).
	path := filepath.Join(t.TempDir(), "registry.db")
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

	want := fullTemplate()
	if err := s.Put(ctx, &want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("round-trip mismatch:\n got: %#v\nwant: %#v", *got, want)
	}
}

func TestGetNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.Get(context.Background(), "nope/x"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("Get missing: want ErrNotFound, got %v", err)
	}
}

func TestPutUpsert(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	tmpl := fullTemplate()
	if err := s.Put(ctx, &tmpl); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Update a field and re-put; CreatedAt must be preserved.
	tmpl.Name = "Helpfulness v2"
	tmpl.UpdatedAt = time.Time{} // force store to stamp a new updated_at
	if err := s.Put(ctx, &tmpl); err != nil {
		t.Fatalf("Put update: %v", err)
	}

	got, err := s.Get(ctx, tmpl.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Helpfulness v2" {
		t.Errorf("Name not updated: %q", got.Name)
	}
	if !got.CreatedAt.Equal(fullTemplate().CreatedAt) {
		t.Errorf("CreatedAt not preserved on upsert: %v", got.CreatedAt)
	}

	all, err := s.List(ctx, registry.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("upsert created a duplicate: got %d rows", len(all))
	}
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	tmpl := fullTemplate()
	if err := s.Put(ctx, &tmpl); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete(ctx, tmpl.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, tmpl.ID); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("after delete: want ErrNotFound, got %v", err)
	}
	if err := s.Delete(ctx, tmpl.ID); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("delete missing: want ErrNotFound, got %v", err)
	}
}

func TestListFilters(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	a := fullTemplate()
	a.ID = "google-brand/a"
	a.Source = "local"
	a.Dirty = false
	a.Kind = registry.KindPointwise
	a.Modalities = []registry.Modality{registry.ModalityText}

	b := fullTemplate()
	b.ID = "other/b"
	b.Source = "pack:other"
	b.Dirty = true
	b.Kind = registry.KindPairwise
	b.Modalities = []registry.Modality{registry.ModalityImage}

	for _, tm := range []registry.MetricTemplate{a, b} {
		tm := tm
		if err := s.Put(ctx, &tm); err != nil {
			t.Fatalf("Put %s: %v", tm.ID, err)
		}
	}

	cases := []struct {
		name   string
		filter registry.ListFilter
		wantID []string
	}{
		{"all", registry.ListFilter{}, []string{"google-brand/a", "other/b"}},
		{"namespace", registry.ListFilter{Namespace: "google-brand"}, []string{"google-brand/a"}},
		{"source", registry.ListFilter{Source: "local"}, []string{"google-brand/a"}},
		{"dirty", registry.ListFilter{DirtyOnly: true}, []string{"other/b"}},
		{"kind", registry.ListFilter{Kinds: []registry.MetricKind{registry.KindPairwise}}, []string{"other/b"}},
		{"modality", registry.ListFilter{Modalities: []registry.Modality{registry.ModalityText}}, []string{"google-brand/a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.List(ctx, tc.filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var ids []string
			for _, g := range got {
				ids = append(ids, g.ID)
			}
			if !reflect.DeepEqual(ids, tc.wantID) {
				t.Errorf("List(%+v) = %v, want %v", tc.filter, ids, tc.wantID)
			}
		})
	}
}

func TestListChangedSince(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	old := fullTemplate()
	old.ID = "ns/old"
	old.UpdatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	recent := fullTemplate()
	recent.ID = "ns/recent"
	recent.UpdatedAt = time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)

	for _, tm := range []registry.MetricTemplate{old, recent} {
		tm := tm
		if err := s.Put(ctx, &tm); err != nil {
			t.Fatalf("Put %s: %v", tm.ID, err)
		}
	}

	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	got, err := s.ListChangedSince(ctx, since)
	if err != nil {
		t.Fatalf("ListChangedSince: %v", err)
	}
	if len(got) != 1 || got[0].ID != "ns/recent" {
		t.Errorf("ListChangedSince = %v, want [ns/recent]", ids(got))
	}
}

func ids(ts []registry.MetricTemplate) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}
