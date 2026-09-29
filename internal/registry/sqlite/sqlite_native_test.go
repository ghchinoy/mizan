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

// TestPutGetRoundTripNative proves a kind:computation template's NativeMetricSpec
// round-trips through the additive native_metric column byte-for-byte, including
// the optional *float64 PassThreshold.
func TestPutGetRoundTripNative(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ts := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	thr := 0.5

	want := registry.MetricTemplate{
		ID:      "computation/rouge-l",
		Name:    "ROUGE-L",
		Version: "1.0.0",
		Kind:    registry.KindComputation,
		Inputs: []registry.InputSpec{
			{Name: "response", Modality: registry.ModalityText, Required: true},
			{Name: "reference", Modality: registry.ModalityText, Required: true},
		},
		Native: &registry.NativeMetricSpec{
			Metric:        "rouge",
			RougeType:     registry.RougeTypeLsum,
			PassThreshold: &thr,
		},
		CreatedAt: ts,
		UpdatedAt: ts,
	}
	if err := s.Put(ctx, &want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Native == nil || !reflect.DeepEqual(got.Native, want.Native) {
		t.Fatalf("native round-trip mismatch:\n got: %#v\nwant: %#v", got.Native, want.Native)
	}
	if got.Kind != registry.KindComputation {
		t.Errorf("Kind = %q, want computation", got.Kind)
	}
}

// TestMigrateV4ToV5 builds a DB at the v4 shape (no native_metric column), then
// re-opens it: the migration must add the column in place, keep existing rows
// (reading back a nil Native), and leave the DB at user_version 5.
func TestMigrateV4ToV5(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v4.db")

	// Start from the current shape, then roll it back to v4 by dropping the v5
	// column (modernc sqlite supports DROP COLUMN) and resetting the version.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	legacy := registry.MetricTemplate{ID: "legacy/pointwise", Name: "Legacy", Kind: registry.KindPointwise}
	if err := s.Put(ctx, &legacy); err != nil {
		t.Fatalf("Put legacy: %v", err)
	}
	for _, q := range []string{
		"ALTER TABLE metric_templates DROP COLUMN native_metric",
		"PRAGMA user_version = 4",
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open (migrate v4→v5): %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if uv := userVersion(t, s2.db); uv != 5 {
		t.Errorf("user_version = %d, want 5", uv)
	}
	got, err := s2.Get(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("Get legacy after migrate: %v", err)
	}
	if got.Native != nil {
		t.Errorf("legacy row Native = %#v, want nil", got.Native)
	}
}
