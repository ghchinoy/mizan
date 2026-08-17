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

package registry

// service_test.go covers registry.Service — the CRUD façade over a Store — using
// an in-memory fake Store. It exercises the validation and precedence logic the
// Service itself owns (ID-required guards, create-vs-update existence rules,
// timestamp handling) plus the pass-through of underlying Store errors.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeStore is an in-memory Store for exercising Service logic. getErr, when set,
// is returned by Get for any id (to drive the non-ErrNotFound branch in Create);
// putErr / deleteErr / listErr similarly force those operations to fail.
type fakeStore struct {
	items     map[string]*MetricTemplate
	getErr    error
	putErr    error
	deleteErr error
	listErr   error

	putCalls    int
	deleteCalls int
	lastList    ListFilter
}

func newFakeStore() *fakeStore { return &fakeStore{items: map[string]*MetricTemplate{}} }

func (s *fakeStore) Get(_ context.Context, id string) (*MetricTemplate, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	t, ok := s.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *t
	return &cp, nil
}

func (s *fakeStore) List(_ context.Context, f ListFilter) ([]MetricTemplate, error) {
	s.lastList = f
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]MetricTemplate, 0, len(s.items))
	for _, t := range s.items {
		out = append(out, *t)
	}
	return out, nil
}

func (s *fakeStore) Put(_ context.Context, t *MetricTemplate) error {
	s.putCalls++
	if s.putErr != nil {
		return s.putErr
	}
	cp := *t
	s.items[t.ID] = &cp
	return nil
}

func (s *fakeStore) Delete(_ context.Context, id string) error {
	s.deleteCalls++
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.items, id)
	return nil
}

func (s *fakeStore) ListChangedSince(_ context.Context, _ time.Time) ([]MetricTemplate, error) {
	return nil, nil
}

func ctx() context.Context { return context.Background() }

// --- Create -------------------------------------------------------------------

func TestCreate_Success_SetsTimestamps(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	before := time.Now().UTC()
	// Kind is required by the strict-schema pre-check Create now runs.
	if err := svc.Create(ctx(), MetricTemplate{ID: "ns/a", Name: "A", Kind: KindPointwise}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	after := time.Now().UTC()

	got := store.items["ns/a"]
	if got == nil {
		t.Fatal("template not stored")
	}
	if got.CreatedAt.Before(before) || got.CreatedAt.After(after) {
		t.Errorf("CreatedAt %v not within [%v, %v]", got.CreatedAt, before, after)
	}
	if !got.UpdatedAt.Equal(got.CreatedAt) {
		t.Errorf("on create, UpdatedAt (%v) should equal CreatedAt (%v)", got.UpdatedAt, got.CreatedAt)
	}
}

func TestCreate_PreservesSuppliedCreatedAt(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	orig := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := svc.Create(ctx(), MetricTemplate{ID: "ns/a", Kind: KindPointwise, CreatedAt: orig}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := store.items["ns/a"]
	if !got.CreatedAt.Equal(orig) {
		t.Errorf("CreatedAt = %v, want preserved %v", got.CreatedAt, orig)
	}
	// UpdatedAt is always refreshed on create.
	if got.UpdatedAt.Equal(orig) {
		t.Errorf("UpdatedAt should be refreshed, not the supplied CreatedAt %v", orig)
	}
}

func TestCreate_EmptyID(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)
	if err := svc.Create(ctx(), MetricTemplate{}); err == nil {
		t.Fatal("Create with empty ID: expected error")
	}
	if store.putCalls != 0 {
		t.Errorf("Put called %d times on validation failure, want 0", store.putCalls)
	}
}

func TestCreate_AlreadyExists(t *testing.T) {
	store := newFakeStore()
	store.items["ns/a"] = &MetricTemplate{ID: "ns/a"}
	svc := NewService(store)

	err := svc.Create(ctx(), MetricTemplate{ID: "ns/a"})
	if err == nil {
		t.Fatal("Create over existing ID: expected error")
	}
	if store.putCalls != 0 {
		t.Errorf("Put called %d times on duplicate, want 0", store.putCalls)
	}
}

func TestCreate_StoreGetErrorPropagates(t *testing.T) {
	store := newFakeStore()
	sentinel := errors.New("db down")
	store.getErr = sentinel
	svc := NewService(store)

	err := svc.Create(ctx(), MetricTemplate{ID: "ns/a"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Create: got %v, want the underlying store error %v", err, sentinel)
	}
	if store.putCalls != 0 {
		t.Errorf("Put called despite a Get error, want 0")
	}
}

// --- Create parity fast-follows (ContentHash + strict schema) -----------------

// TestCreate_ComputesContentHash pins Item 1: a directly-authored template must
// be persisted with a non-empty ContentHash computed the same way the import path
// (stampImported) computes it, not left empty.
func TestCreate_ComputesContentHash(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	in := MetricTemplate{
		ID:           "ns/hashed",
		Name:         "Hashed",
		Version:      "1.0.0",
		Kind:         KindRubric,
		RubricGroups: map[string][]string{"quality": {"is good"}},
	}
	if err := svc.Create(ctx(), in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := store.items["ns/hashed"]
	if got == nil {
		t.Fatal("template not stored")
	}
	if got.ContentHash == "" {
		t.Fatal("ContentHash is empty; Create must stamp it like the import path")
	}
	// contentHash excludes lifecycle/provenance fields, so the stored copy hashes
	// identically to the input content computed independently.
	if want := contentHash(&in); got.ContentHash != want {
		t.Errorf("ContentHash = %q, want %q", got.ContentHash, want)
	}
}

// TestCreate_RejectsSchemaInvalidEnum pins Item 2: an enum value the strict
// schema forbids (a modality outside text|image|audio|video|music) is rejected
// before anything is written, at parity with the pack/import gate.
func TestCreate_RejectsSchemaInvalidEnum(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	bad := MetricTemplate{
		ID:         "ns/bad",
		Kind:       KindPointwise,
		Modalities: []Modality{"hologram"},
	}
	if err := svc.Create(ctx(), bad); err == nil {
		t.Fatal("Create with an invalid modality: expected a schema error")
	}
	if store.putCalls != 0 {
		t.Errorf("Put called %d times on a schema-invalid template, want 0", store.putCalls)
	}
}

// TestCreate_RejectsSchemaInvalidID confirms an id that violates the
// "<namespace>/<slug>" pattern (rejected by the import ingest boundary) is now
// also rejected on the authoring path via the strict-schema pre-check.
func TestCreate_RejectsSchemaInvalidID(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	bad := MetricTemplate{ID: "Bad_ID", Kind: KindPointwise}
	if err := svc.Create(ctx(), bad); err == nil {
		t.Fatal("Create with a malformed id: expected a schema error")
	}
	if store.putCalls != 0 {
		t.Errorf("Put called %d times on a malformed id, want 0", store.putCalls)
	}
}

// TestCreate_MissingKindRejected confirms the strict schema's spec.kind
// requirement is enforced on the authoring path (spec.kind is required, like the
// import codec requires it).
func TestCreate_MissingKindRejected(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	if err := svc.Create(ctx(), MetricTemplate{ID: "ns/nokind"}); err == nil {
		t.Fatal("Create without a kind: expected a schema error")
	}
	if store.putCalls != 0 {
		t.Errorf("Put called %d times without a kind, want 0", store.putCalls)
	}
}

// --- Update -------------------------------------------------------------------

func TestUpdate_Success_PreservesCreatedAt(t *testing.T) {
	store := newFakeStore()
	created := time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC)
	store.items["ns/a"] = &MetricTemplate{ID: "ns/a", Name: "old", CreatedAt: created, UpdatedAt: created}
	svc := NewService(store)

	before := time.Now().UTC()
	if err := svc.Update(ctx(), MetricTemplate{ID: "ns/a", Name: "new"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := store.items["ns/a"]
	if got.Name != "new" {
		t.Errorf("Name = %q, want %q", got.Name, "new")
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want preserved %v", got.CreatedAt, created)
	}
	if got.UpdatedAt.Before(before) {
		t.Errorf("UpdatedAt = %v, want refreshed (>= %v)", got.UpdatedAt, before)
	}
}

func TestUpdate_EmptyID(t *testing.T) {
	svc := NewService(newFakeStore())
	if err := svc.Update(ctx(), MetricTemplate{}); err == nil {
		t.Fatal("Update with empty ID: expected error")
	}
}

func TestUpdate_NotFound(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)
	err := svc.Update(ctx(), MetricTemplate{ID: "ns/missing"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update missing: got %v, want ErrNotFound", err)
	}
	if store.putCalls != 0 {
		t.Errorf("Put called for a missing template, want 0")
	}
}

// --- Get / List / Delete pass-through -----------------------------------------

func TestGet_PassThrough(t *testing.T) {
	store := newFakeStore()
	store.items["ns/a"] = &MetricTemplate{ID: "ns/a", Name: "A"}
	svc := NewService(store)

	got, err := svc.Get(ctx(), "ns/a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "A" {
		t.Errorf("Name = %q, want A", got.Name)
	}

	if _, err := svc.Get(ctx(), "ns/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing: got %v, want ErrNotFound", err)
	}
}

func TestList_PassesFilter(t *testing.T) {
	store := newFakeStore()
	store.items["ns/a"] = &MetricTemplate{ID: "ns/a"}
	svc := NewService(store)

	filter := ListFilter{Kinds: []MetricKind{KindRubric}, Namespace: "ns"}
	got, err := svc.List(ctx(), filter)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("List returned %d items, want 1", len(got))
	}
	if store.lastList.Namespace != "ns" || len(store.lastList.Kinds) != 1 {
		t.Errorf("filter not passed through to store: %+v", store.lastList)
	}
}

func TestList_ErrorPropagates(t *testing.T) {
	store := newFakeStore()
	sentinel := errors.New("list failed")
	store.listErr = sentinel
	svc := NewService(store)
	if _, err := svc.List(ctx(), ListFilter{}); !errors.Is(err, sentinel) {
		t.Fatalf("List: got %v, want %v", err, sentinel)
	}
}

func TestDelete_PassThrough(t *testing.T) {
	store := newFakeStore()
	store.items["ns/a"] = &MetricTemplate{ID: "ns/a"}
	svc := NewService(store)

	if err := svc.Delete(ctx(), "ns/a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if store.deleteCalls != 1 {
		t.Errorf("Delete calls = %d, want 1", store.deleteCalls)
	}
	if _, ok := store.items["ns/a"]; ok {
		t.Error("template still present after Delete")
	}
}

func TestDelete_ErrorPropagates(t *testing.T) {
	store := newFakeStore()
	sentinel := errors.New("delete failed")
	store.deleteErr = sentinel
	svc := NewService(store)
	if err := svc.Delete(ctx(), "ns/a"); !errors.Is(err, sentinel) {
		t.Fatalf("Delete: got %v, want %v", err, sentinel)
	}
}
