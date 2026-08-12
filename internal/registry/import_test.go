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

// TestImportInsertOnlySkipsExisting proves P2.1 reconciliation is insert-only:
// an already-present id is skipped and recorded, not overwritten.
func TestImportInsertOnlySkipsExisting(t *testing.T) {
	store := newFakeStore()
	store.items["google-brand/video-brand-alignment"] = &MetricTemplate{
		ID:   "google-brand/video-brand-alignment",
		Name: "pre-existing local edit",
	}
	svc := NewService(store)

	report, err := svc.Import(ctx(), fixtureTree, ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 0 || report.Skipped != 1 {
		t.Fatalf("report = %d inserted, %d skipped; want 0 inserted, 1 skipped", report.Inserted, report.Skipped)
	}
	if got := store.items["google-brand/video-brand-alignment"]; got.Name != "pre-existing local edit" {
		t.Errorf("existing template was overwritten: Name = %q", got.Name)
	}
	if store.putCalls != 0 {
		t.Errorf("Put called %d times; insert-only skip must not write", store.putCalls)
	}
}

func TestImportReimportIsIdempotent(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	if _, err := svc.Import(ctx(), fixtureTree, ImportOptions{}); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	report, err := svc.Import(ctx(), fixtureTree, ImportOptions{})
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if report.Inserted != 0 || report.Skipped != 1 {
		t.Errorf("re-import report = %d inserted, %d skipped; want 0/1", report.Inserted, report.Skipped)
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
