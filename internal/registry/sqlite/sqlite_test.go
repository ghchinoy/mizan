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

package sqlite

import (
	"context"
	"errors"
	"os"
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

// TestPutGetRoundTripMinimalNil locks the nil-JSON behavior for a minimal
// pointwise template whose slice/map/pointer fields are nil (Tags, Authors,
// Maintainers, Modalities, Inputs, RubricGroups, ResponseSchema). These persist
// as JSON "null" and must round-trip back to nil (not to empty non-nil values),
// so reflect.DeepEqual against the nil-valued input holds.
func TestPutGetRoundTripMinimalNil(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	ts := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	want := registry.MetricTemplate{
		ID:                   "minimal/pointwise",
		Name:                 "Minimal",
		Kind:                 registry.KindPointwise,
		MetricPromptTemplate: "Rate: {{response}}",
		AutoraterModel:       "gemini-2.5-flash",
		CreatedAt:            ts,
		UpdatedAt:            ts,
		// All slice/map/pointer fields intentionally left nil.
	}
	if err := s.Put(ctx, &want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("minimal round-trip mismatch:\n got: %#v\nwant: %#v", *got, want)
	}
	// Explicitly assert the nilable fields came back nil (not empty non-nil).
	if got.Tags != nil || got.Authors != nil || got.Maintainers != nil ||
		got.Modalities != nil || got.Inputs != nil || got.RubricGroups != nil ||
		got.ResponseSchema != nil {
		t.Errorf("expected nil slices/maps/pointer, got: %#v", *got)
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

// TestOpenSetsRestrictivePerms verifies the DB dir is 0700 and the DB file is
// 0600 (LOW security finding: registry DB must not be world-readable).
func TestOpenSetsRestrictivePerms(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "cfg", "mizan")
	path := filepath.Join(sub, "registry.db")
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

// TestListNamespaceEscapesWildcards verifies LIKE metacharacters in a namespace
// filter are matched literally (a bare "%" must not match everything).
func TestListNamespaceEscapesWildcards(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, id := range []string{"team/a", "team/b", "other/c"} {
		tm := registry.MetricTemplate{ID: id, Name: id, Kind: registry.KindPointwise}
		if err := s.Put(ctx, &tm); err != nil {
			t.Fatalf("Put %s: %v", id, err)
		}
	}

	// A literal "%" namespace must match nothing (there is no id "%/...").
	got, err := s.List(ctx, registry.ListFilter{Namespace: "%"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("namespace %q matched %d rows, want 0 (wildcard must be escaped)", "%", len(got))
	}

	// A real namespace still filters correctly.
	got, err = s.List(ctx, registry.ListFilter{Namespace: "team"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("namespace team matched %d rows, want 2", len(got))
	}
}
