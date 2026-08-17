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

import (
	"strings"
	"testing"
)

const fixtureTree = "testdata" // contains packs/google-brand/templates/*.yaml

// TestImportInsertsNewTemplate is the P2.1 acceptance in unit form: importing a
// local pack tree inserts the absent template with the right provenance stamps.
func TestImportInsertsNewTemplate(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	report, err := svc.Import(ctx(), fixtureTree, ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 1 || report.Skipped != 0 {
		t.Fatalf("report = %d inserted, %d skipped; want 1 inserted, 0 skipped", report.Inserted, report.Skipped)
	}
	if len(report.Entries) != 1 || report.Entries[0].Action != ActionInserted {
		t.Fatalf("entries = %+v", report.Entries)
	}

	got := store.items["google-brand/video-brand-alignment"]
	if got == nil {
		t.Fatal("template not stored")
	}
	if got.Kind != KindPointwise {
		t.Errorf("Kind = %q, want pointwise", got.Kind)
	}
	wantSource := "pack:google-brand@testdata"
	if got.Source != wantSource {
		t.Errorf("Source = %q, want %q", got.Source, wantSource)
	}
	if got.ContentHash == "" {
		t.Error("ContentHash not stamped")
	}
	if got.ContentHash != contentHash(got) {
		t.Error("stored ContentHash does not match recomputed hash")
	}
	if got.ImportedAt.IsZero() {
		t.Error("ImportedAt not stamped")
	}
	if got.Dirty {
		t.Error("freshly imported template should not be dirty")
	}
	if got.AutoraterModel != "gemini-2.5-pro" {
		t.Errorf("AutoraterModel = %q", got.AutoraterModel)
	}
}

// TestImportDefaultConflictsOnEqualVersionDivergence proves the D2 default: a
// pre-existing template with the SAME version but different content is reported
// as a conflict and NOT overwritten (fixture version is 1.0.0).
func TestImportDefaultConflictsOnEqualVersionDivergence(t *testing.T) {
	store := newFakeStore()
	store.items["google-brand/video-brand-alignment"] = &MetricTemplate{
		ID:      "google-brand/video-brand-alignment",
		Name:    "pre-existing local edit",
		Version: "1.0.0",
	}
	svc := NewService(store)

	report, err := svc.Import(ctx(), fixtureTree, ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 0 || report.Updated != 0 || report.Conflicted != 1 {
		t.Fatalf("report = %d inserted, %d updated, %d conflicted; want 0/0/1", report.Inserted, report.Updated, report.Conflicted)
	}
	if got := store.items["google-brand/video-brand-alignment"]; got.Name != "pre-existing local edit" {
		t.Errorf("conflicting template was overwritten: Name = %q", got.Name)
	}
	if store.putCalls != 0 {
		t.Errorf("Put called %d times; a conflict must not write", store.putCalls)
	}
}

func TestImportReimportIsNoop(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	if _, err := svc.Import(ctx(), fixtureTree, ImportOptions{}); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	putsAfterFirst := store.putCalls
	report, err := svc.Import(ctx(), fixtureTree, ImportOptions{})
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if report.Inserted != 0 || report.Updated != 0 || report.Unchanged != 1 {
		t.Errorf("re-import report = %d inserted, %d updated, %d unchanged; want 0/0/1", report.Inserted, report.Updated, report.Unchanged)
	}
	if store.putCalls != putsAfterFirst {
		t.Errorf("re-import wrote to the store (%d extra Put calls); an unchanged re-import is a no-op", store.putCalls-putsAfterFirst)
	}
}

func TestImportEmptySourceErrors(t *testing.T) {
	svc := NewService(newFakeStore())
	if _, err := svc.Import(ctx(), "  ", ImportOptions{}); err == nil {
		t.Fatal("empty source: expected error")
	}
}

func TestImportMissingSourceErrors(t *testing.T) {
	svc := NewService(newFakeStore())
	_, err := svc.Import(ctx(), "testdata/does-not-exist", ImportOptions{})
	if err == nil {
		t.Fatal("missing source: expected error")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error should name the bad path: %v", err)
	}
}
