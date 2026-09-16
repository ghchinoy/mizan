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
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/registry"
)

func TestPutNilTemplate(t *testing.T) {
	s := newStore(t)
	if err := s.Put(context.Background(), nil); err == nil {
		t.Fatal("Put(nil) should return an error")
	}
}

func TestPutEmptyID(t *testing.T) {
	s := newStore(t)
	tmpl := fullTemplate()
	tmpl.ID = ""
	if err := s.Put(context.Background(), &tmpl); err == nil {
		t.Fatal("Put with empty ID should return an error")
	}
}

// TestPutStampsTimestampsWhenZero verifies the store fills CreatedAt/UpdatedAt
// when the caller leaves them zero, and reflects them back onto the argument.
func TestPutStampsTimestampsWhenZero(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	tmpl := fullTemplate()
	tmpl.CreatedAt = time.Time{}
	tmpl.UpdatedAt = time.Time{}

	before := time.Now().UTC().Add(-time.Second)
	if err := s.Put(ctx, &tmpl); err != nil {
		t.Fatalf("Put: %v", err)
	}
	after := time.Now().UTC().Add(time.Second)

	// Reflected back onto the caller's struct.
	if tmpl.CreatedAt.IsZero() || tmpl.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not stamped back: created=%v updated=%v", tmpl.CreatedAt, tmpl.UpdatedAt)
	}
	if tmpl.CreatedAt.Before(before) || tmpl.CreatedAt.After(after) {
		t.Errorf("CreatedAt %v not within [%v,%v]", tmpl.CreatedAt, before, after)
	}

	got, err := s.Get(ctx, tmpl.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.CreatedAt.Equal(tmpl.CreatedAt) {
		t.Errorf("persisted CreatedAt %v != stamped %v", got.CreatedAt, tmpl.CreatedAt)
	}
}

// TestZeroImportedAtRoundTrips proves a zero ImportedAt is stored as SQL NULL
// and read back as a zero time.Time (not a spurious epoch value).
func TestZeroImportedAtRoundTrips(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	tmpl := fullTemplate()
	tmpl.ImportedAt = time.Time{}
	if err := s.Put(ctx, &tmpl); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, tmpl.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.ImportedAt.IsZero() {
		t.Errorf("ImportedAt = %v, want zero (stored as NULL)", got.ImportedAt)
	}
}

// TestJSONColumnsRoundTrip isolates the JSON-encoded columns (slices, maps,
// nested pointers) to prove they survive a store/load cycle intact.
func TestJSONColumnsRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	tmpl := fullTemplate()
	if err := s.Put(ctx, &tmpl); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, tmpl.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got.Authors, tmpl.Authors) {
		t.Errorf("Authors round-trip: got %#v want %#v", got.Authors, tmpl.Authors)
	}
	if !reflect.DeepEqual(got.RubricGroups, tmpl.RubricGroups) {
		t.Errorf("RubricGroups round-trip: got %#v want %#v", got.RubricGroups, tmpl.RubricGroups)
	}
	if !reflect.DeepEqual(got.ResponseSchema, tmpl.ResponseSchema) {
		t.Errorf("ResponseSchema round-trip: got %#v want %#v", got.ResponseSchema, tmpl.ResponseSchema)
	}
	if !reflect.DeepEqual(got.Inputs, tmpl.Inputs) {
		t.Errorf("Inputs round-trip: got %#v want %#v", got.Inputs, tmpl.Inputs)
	}
}

// TestMigrationSetsUserVersion confirms migration records PRAGMA user_version = 3
// (v2 added the additive RFC-0001 columns; v3 (B2) adds the heuristic column), the
// marker later migrations key off.
func TestMigrationSetsUserVersion(t *testing.T) {
	s := newStore(t)
	var version int
	if err := s.db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != 3 {
		t.Errorf("user_version = %d, want 3", version)
	}
}

// TestOpenCreatesParentDir proves Open mkdirs a missing parent directory for a
// file-backed DB path.
func TestOpenCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "sub", "registry.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open with missing parent dirs: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Sanity: the store is usable.
	if err := s.Put(context.Background(), ptr(fullTemplate())); err != nil {
		t.Fatalf("Put after Open: %v", err)
	}
}

func ptr(t registry.MetricTemplate) *registry.MetricTemplate { return &t }

// TestOpenInMemory proves the ":memory:" path works and persists across
// statements (single-conn pool), an option the config layer relies on for tests.
func TestOpenInMemory(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	tmpl := fullTemplate()
	if err := s.Put(context.Background(), &tmpl); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := s.Get(context.Background(), tmpl.ID); err != nil {
		t.Fatalf("Get from in-memory store: %v", err)
	}
}
