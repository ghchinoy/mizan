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
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

		// Additive RFC-0001 fields (PR C): populated so the "loses nothing"
		// round-trip actually exercises rating_rubric / rubric_detail /
		// rubric_provenance (scope §3.4, §7 acceptance criterion 1/7).
		RatingRubric: map[string]map[string]string{
			"quality": {"1": "bad", "5": "good"},
		},
		RubricDetail: &registry.RubricDetail{
			Scale: &registry.RubricScale{Min: 1, Max: 5},
		},
		RubricProvenance: &registry.RubricProvenance{
			Method:         "adaptive-generated",
			GeneratorModel: "gemini-2.5-flash",
			Recipe:         "default",
			PromptTemplate: "draft a rubric for: {{response}}",
			SampleInputRef: "sample-001",
			GeneratedAt:    ts,
			APIVersion:     "mizan.dev/v1alpha1",
			RubricMeta: []registry.RubricMeta{
				{Group: "quality", Criterion: "Is it clear?", Type: "boolean", Importance: "high"},
				{Group: "quality", Criterion: "Is it correct?", Type: "boolean", Importance: "high"},
			},
		},

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

// TestPutGetRoundTripMixedOrigin proves the additive per-criterion RubricMeta.Origin
// (CUJ 9 union provenance, design §6.6) round-trips through the JSON TEXT
// rubric_provenance column with NO schema migration — an additive struct field
// inside the whole-object JSON persists for free. A mixed-origin template is Put
// then Get; the Origin values come back byte-identical and in order.
func TestPutGetRoundTripMixedOrigin(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ts := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	want := registry.MetricTemplate{
		ID:                   "acme/union",
		Name:                 "Adaptive rubric union",
		Version:              "0.1.0",
		Kind:                 registry.KindRubric,
		Modalities:           []registry.Modality{registry.ModalityText},
		Inputs:               []registry.InputSpec{{Name: "response", Modality: registry.ModalityText, Required: true}},
		MetricPromptTemplate: "Assess the response: {{response}}",
		RubricGroups: map[string][]string{
			"general_quality": {"Answers the question directly", "Uses the brand palette"},
		},
		RubricProvenance: &registry.RubricProvenance{
			Method:         "adaptive-generated",
			GeneratorModel: "gemini-2.5-flash",
			Recipe:         "general_quality_v1",
			SampleInputRef: `inline:"Explain the offer" sha256:abc123`,
			GeneratedAt:    ts,
			APIVersion:     "v1beta1:generateInstanceRubrics",
			RubricMeta: []registry.RubricMeta{
				{Group: "general_quality", Criterion: "Answers the question directly", Type: "CONTENT", Importance: "HIGH", Origin: registry.OriginAdaptiveGenerated},
				{Group: "general_quality", Criterion: "Uses the brand palette", Origin: registry.OriginHandAuthored},
			},
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
	if got.RubricProvenance == nil {
		t.Fatal("provenance dropped on round-trip")
	}
	gotMeta := got.RubricProvenance.RubricMeta
	if len(gotMeta) != 2 {
		t.Fatalf("RubricMeta len = %d, want 2", len(gotMeta))
	}
	if gotMeta[0].Origin != registry.OriginAdaptiveGenerated || gotMeta[1].Origin != registry.OriginHandAuthored {
		t.Errorf("Origin not round-tripped byte-identical: got[0]=%q got[1]=%q", gotMeta[0].Origin, gotMeta[1].Origin)
	}
	if !reflect.DeepEqual(gotMeta, want.RubricProvenance.RubricMeta) {
		t.Errorf("mixed-origin RubricMeta mismatch:\n got: %#v\nwant: %#v", gotMeta, want.RubricProvenance.RubricMeta)
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
	// The additive RFC-0001 fields (PR C) must also round-trip nil→nil: a nil map
	// / nil pointer marshals to "null" and unmarshalIf must skip it, not
	// reconstruct an empty non-nil value (scope §4 nil round-trip semantics).
	if got.RatingRubric != nil || got.RubricDetail != nil || got.RubricProvenance != nil {
		t.Errorf("expected nil rating_rubric/rubric_detail/rubric_provenance, got: %#v", *got)
	}
}

// userVersion reads the SQLite PRAGMA user_version from a Store's DB.
func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var uv int
	if err := db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&uv); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	return uv
}

// v1Schema is the metric_templates table exactly as schema v1 created it —
// WITHOUT the three v2 columns (rating_rubric, rubric_detail, rubric_provenance).
// It stands in for a DB deployed before PR C so the migration path is exercised
// against the real pre-v2 shape (scope §6 / acceptance criterion 4).
const v1Schema = `
CREATE TABLE IF NOT EXISTS metric_templates (
    id                     TEXT PRIMARY KEY,
    name                   TEXT NOT NULL,
    description            TEXT NOT NULL DEFAULT '',
    version                TEXT NOT NULL DEFAULT '',
    authors                TEXT NOT NULL DEFAULT '[]',
    maintainers            TEXT NOT NULL DEFAULT '[]',
    license                TEXT NOT NULL DEFAULT '',
    tags                   TEXT NOT NULL DEFAULT '[]',
    kind                   TEXT NOT NULL,
    modalities             TEXT NOT NULL DEFAULT '[]',
    inputs                 TEXT NOT NULL DEFAULT '[]',
    metric_prompt_template TEXT NOT NULL DEFAULT '',
    system_instruction     TEXT NOT NULL DEFAULT '',
    candidate_field_name   TEXT NOT NULL DEFAULT '',
    baseline_field_name    TEXT NOT NULL DEFAULT '',
    rubric_groups          TEXT NOT NULL DEFAULT 'null',
    response_schema        TEXT NOT NULL DEFAULT 'null',
    autorater_model        TEXT NOT NULL DEFAULT '',
    sampling_count         INTEGER NOT NULL DEFAULT 0,
    flip_enabled           INTEGER NOT NULL DEFAULT 0,
    source                 TEXT NOT NULL DEFAULT '',
    content_hash           TEXT NOT NULL DEFAULT '',
    dirty                  INTEGER NOT NULL DEFAULT 0,
    created_at             TIMESTAMP,
    updated_at             TIMESTAMP,
    imported_at            TIMESTAMP
);
`

// TestMigrateV1ToV2 opens a DB created at user_version=1 with the pre-v2 table
// (no new columns), then reopens it via Open (which runs migrate) and asserts:
// the version becomes 2, the pre-existing row survives with the three new fields
// nil, a subsequent populated Put/Get round-trips, and a second Open is a no-op.
func TestMigrateV1ToV2(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "registry.db")

	// Build a v1 DB by hand: the pre-PR-C schema + user_version=1 + one row.
	seedTS := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	{
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open raw v1 db: %v", err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.ExecContext(ctx, v1Schema); err != nil {
			t.Fatalf("create v1 schema: %v", err)
		}
		if _, err := db.ExecContext(ctx, "PRAGMA user_version = 1;"); err != nil {
			t.Fatalf("set user_version=1: %v", err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO metric_templates (id, name, kind, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			"legacy/pointwise", "Legacy", string(registry.KindPointwise), seedTS, seedTS,
		); err != nil {
			t.Fatalf("seed v1 row: %v", err)
		}
		if uv := userVersion(t, db); uv != 1 {
			t.Fatalf("precondition: user_version = %d, want 1", uv)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close raw v1 db: %v", err)
		}
	}

	// Open triggers migrate(): v1 → v2.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open (migrate v1→v2): %v", err)
	}
	if uv := userVersion(t, s.db); uv != 2 {
		t.Errorf("after migrate: user_version = %d, want 2", uv)
	}

	// The pre-existing row survives and reads back with the three new fields nil.
	legacy, err := s.Get(ctx, "legacy/pointwise")
	if err != nil {
		t.Fatalf("Get legacy row after migrate: %v", err)
	}
	if legacy.Name != "Legacy" || !legacy.CreatedAt.Equal(seedTS) {
		t.Errorf("legacy row not intact: %#v", *legacy)
	}
	if legacy.RatingRubric != nil || legacy.RubricDetail != nil || legacy.RubricProvenance != nil {
		t.Errorf("migrated legacy row: expected nil additive fields, got %#v", *legacy)
	}

	// A populated template now round-trips through the migrated columns.
	want := fullTemplate()
	if err := s.Put(ctx, &want); err != nil {
		t.Fatalf("Put after migrate: %v", err)
	}
	got, err := s.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get after migrate: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("post-migrate round-trip mismatch:\n got: %#v\nwant: %#v", *got, want)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A second Open must be a no-op: version stays 2, data is intact.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if uv := userVersion(t, s2.db); uv != 2 {
		t.Errorf("second Open: user_version = %d, want 2", uv)
	}
	if _, err := s2.Get(ctx, "legacy/pointwise"); err != nil {
		t.Errorf("legacy row missing after second Open: %v", err)
	}
	again, err := s2.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get populated after second Open: %v", err)
	}
	if !reflect.DeepEqual(*again, want) {
		t.Errorf("second-open round-trip mismatch:\n got: %#v\nwant: %#v", *again, want)
	}
}

// columnExists reports whether table has a column of the given name (via
// PRAGMA table_info), used to assert a rolled-back ALTER left no trace.
func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "PRAGMA table_info("+table+")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name, typ  string
			notNull    int
			dfltValue  sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows: %v", err)
	}
	return false
}

// TestMigrateV1ToV2Atomic proves the v1→v2 migration is transactional: when an
// ALTER fails partway (here rubric_provenance already exists — the shape an
// interrupted pre-transaction migration would have left behind), the WHOLE
// migration rolls back. user_version stays 1 and the earlier, individually
// successful ALTERs (rating_rubric/rubric_detail) are undone, so the DB is never
// stranded half-migrated. Without the transaction wrap this DB would be bricked
// (open re-runs the ALTERs and dies on "duplicate column name" forever).
func TestMigrateV1ToV2Atomic(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "registry.db")

	// Build a v1 DB and pre-add ONLY rubric_provenance (the 3rd ALTER's target),
	// leaving user_version=1. migrate()'s 1st/2nd ALTERs will succeed inside the
	// txn and the 3rd will hit "duplicate column name", forcing a full rollback.
	{
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open raw v1 db: %v", err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.ExecContext(ctx, v1Schema); err != nil {
			t.Fatalf("create v1 schema: %v", err)
		}
		if _, err := db.ExecContext(ctx,
			"ALTER TABLE metric_templates ADD COLUMN rubric_provenance TEXT NOT NULL DEFAULT 'null'"); err != nil {
			t.Fatalf("pre-add rubric_provenance: %v", err)
		}
		if _, err := db.ExecContext(ctx, "PRAGMA user_version = 1;"); err != nil {
			t.Fatalf("set user_version=1: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close raw v1 db: %v", err)
		}
	}

	// Open runs migrate(), which must fail on the duplicate rubric_provenance.
	s, err := Open(path)
	if err == nil {
		_ = s.Close()
		t.Fatal("Open: want migration error (duplicate column), got nil")
	}

	// The failed migration must have rolled back cleanly: version still 1, and the
	// earlier ALTERs undone (rating_rubric absent) — proving atomicity, not a
	// half-applied schema.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen raw db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if uv := userVersion(t, db); uv != 1 {
		t.Errorf("after failed migration: user_version = %d, want 1 (rolled back)", uv)
	}
	if columnExists(t, db, "metric_templates", "rating_rubric") {
		t.Error("rating_rubric present after rollback: migration was NOT atomic (partial schema left behind)")
	}
	if columnExists(t, db, "metric_templates", "rubric_detail") {
		t.Error("rubric_detail present after rollback: migration was NOT atomic (partial schema left behind)")
	}
}

// TestStoreImplementsInterface is a compile-time assertion that *Store still
// satisfies registry.Store — the additive-field work must not have changed the
// interface (mirrors the production-side var _ in sqlite.go).
func TestStoreImplementsInterface(t *testing.T) {
	var _ registry.Store = (*Store)(nil)
}

// TestReconcileUnchangedIdempotent proves the §7.5 second-order fix: once the
// additive fields persist, contentHash(local) — recomputed by Service.reconcileOne
// on the store-loaded copy — matches the incoming pack's hash, so a byte-identical
// re-import of a provenance-bearing template is classified Unchanged (not
// Conflicted/Updated as it was while the fields were dropped on write).
func TestReconcileUnchangedIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	svc := registry.NewService(s)

	// Marshal a provenance-bearing template into a single-pack directory laid out
	// as GitPackBackend.Load expects (<pack>/templates/*.yaml).
	tmpl := fullTemplate()
	data, err := registry.NewYAMLCodec().Marshal(&tmpl)
	if err != nil {
		t.Fatalf("Marshal template: %v", err)
	}
	packDir := t.TempDir()
	tmplDir := filepath.Join(packDir, "templates")
	if err := os.MkdirAll(tmplDir, 0o700); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "helpfulness.yaml"), data, 0o600); err != nil {
		t.Fatalf("write template yaml: %v", err)
	}

	// First import inserts the template.
	rep, err := svc.Import(ctx, packDir, registry.ImportOptions{})
	if err != nil {
		t.Fatalf("first Import: %v", err)
	}
	if rep.Inserted != 1 || rep.Unchanged != 0 {
		t.Fatalf("first Import: inserted=%d unchanged=%d, want inserted=1 unchanged=0", rep.Inserted, rep.Unchanged)
	}

	// Re-importing the identical pack must be a no-op: reconcileOne recomputes the
	// hash on the store-loaded copy, which now carries the additive fields.
	rep, err = svc.Import(ctx, packDir, registry.ImportOptions{})
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if rep.Unchanged != 1 || rep.Updated != 0 || rep.Conflicted != 0 {
		t.Errorf("re-import: unchanged=%d updated=%d conflicted=%d, want unchanged=1 (idempotent re-import must not be reclassified)",
			rep.Unchanged, rep.Updated, rep.Conflicted)
	}
}

// TestScanMalformedJSONFailsClosed proves scanTemplate fails closed on a
// corrupt additive column: a non-JSON value in rating_rubric must make Get
// return an error (from unmarshalIf), not panic and not silently drop to a zero
// value. Guards the fail-closed contract for the three new columns (brief probe:
// malformed JSON on read).
func TestScanMalformedJSONFailsClosed(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	tmpl := fullTemplate()
	if err := s.Put(ctx, &tmpl); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Corrupt the rating_rubric column with non-JSON (bypasses Put's mustJSON).
	if _, err := s.db.ExecContext(ctx,
		`UPDATE metric_templates SET rating_rubric = ? WHERE id = ?`,
		"{not valid json", tmpl.ID,
	); err != nil {
		t.Fatalf("corrupt column: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Get panicked on malformed JSON, want a returned error: %v", r)
		}
	}()
	if _, err := s.Get(ctx, tmpl.ID); err == nil {
		t.Fatal("Get: want error on malformed rating_rubric JSON, got nil (silent data loss)")
	}
}

// TestScanOversizedJSONColumnRejected proves the byte cap (defense-in-depth,
// CWE-770): a JSON TEXT column whose raw bytes exceed maxJSONColumnBytes makes
// Get return a clear error naming the column instead of unmarshaling a
// pathologically large row. The oversized value is written directly with SQL —
// NOT via Put — because the normal write path is schema-bounded and cannot
// produce one, which is exactly the tampering/corruption case the cap guards.
func TestScanOversizedJSONColumnRejected(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	tmpl := fullTemplate()
	if err := s.Put(ctx, &tmpl); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// A syntactically valid JSON string one byte over the cap: proves the size
	// check fires BEFORE (and independently of) json.Unmarshal.
	oversized := `"` + strings.Repeat("x", maxJSONColumnBytes) + `"`
	if _, err := s.db.ExecContext(ctx,
		`UPDATE metric_templates SET rubric_provenance = ? WHERE id = ?`,
		oversized, tmpl.ID,
	); err != nil {
		t.Fatalf("write oversized column: %v", err)
	}

	_, err := s.Get(ctx, tmpl.ID)
	if err == nil {
		t.Fatal("Get: want error on oversized rubric_provenance column, got nil")
	}
	if !strings.Contains(err.Error(), "rubric_provenance") || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("Get error = %q, want it to name the rubric_provenance column and the cap", err)
	}
}

// TestScanAtByteCapAccepted proves the cap is inclusive at the boundary: a value
// exactly maxJSONColumnBytes long still unmarshals, so a legitimate (if large)
// template is never rejected by an off-by-one.
func TestScanAtByteCapAccepted(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	tmpl := fullTemplate()
	if err := s.Put(ctx, &tmpl); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Build a JSON string value whose raw column bytes are exactly the cap.
	atCap := `"` + strings.Repeat("x", maxJSONColumnBytes-2) + `"`
	if len(atCap) != maxJSONColumnBytes {
		t.Fatalf("test setup: value is %d bytes, want %d", len(atCap), maxJSONColumnBytes)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE metric_templates SET rubric_provenance = ? WHERE id = ?`,
		atCap, tmpl.ID,
	); err != nil {
		t.Fatalf("write at-cap column: %v", err)
	}
	// A bare JSON string is not a valid *RubricProvenance, so a JSON shape error is
	// expected here — what must NOT happen is the cap error, which would mean a
	// value exactly at the boundary was wrongly rejected by the size gate.
	if _, err := s.Get(ctx, tmpl.ID); err != nil && strings.Contains(err.Error(), "cap") {
		t.Fatalf("Get error = %q; a value exactly at the cap must pass the size gate", err)
	}
}

// TestPutGetPartialPopulation proves the three additive fields round-trip
// independently: with only RubricDetail set (RatingRubric/RubricProvenance nil),
// each column is read back to its own field with no cross-column bleed from the
// shared column-order INSERT/SELECT (the design's #1 bug risk). (brief probe:
// partial-population.)
func TestPutGetPartialPopulation(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	want := fullTemplate()
	want.ID = "partial/pointwise"
	want.RatingRubric = nil
	want.RubricProvenance = nil
	want.RubricDetail = &registry.RubricDetail{Scale: &registry.RubricScale{Min: 1, Max: 7}}

	if err := s.Put(ctx, &want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RatingRubric != nil {
		t.Errorf("RatingRubric = %#v, want nil", got.RatingRubric)
	}
	if got.RubricProvenance != nil {
		t.Errorf("RubricProvenance = %#v, want nil", got.RubricProvenance)
	}
	if !reflect.DeepEqual(got.RubricDetail, want.RubricDetail) {
		t.Errorf("RubricDetail round-trip: got %#v want %#v", got.RubricDetail, want.RubricDetail)
	}
}

// TestEmptyNonNilVsNullDistinction proves DEFAULT 'null' semantics distinguish a
// non-nil-but-empty value from an absent (nil) one: an empty non-nil RatingRubric
// map and a non-nil RubricDetail with a nil Scale marshal to "{}" (not "null"),
// which unmarshalIf must reconstruct as non-nil — distinct from the nil→"null"→nil
// path locked by TestPutGetRoundTripMinimalNil. (brief probe: empty-vs-null.)
func TestEmptyNonNilVsNullDistinction(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	want := fullTemplate()
	want.ID = "empty/pointwise"
	want.RatingRubric = map[string]map[string]string{} // non-nil, empty
	want.RubricDetail = &registry.RubricDetail{}       // non-nil, Scale nil
	want.RubricProvenance = nil                        // nil → must stay nil

	if err := s.Put(ctx, &want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RatingRubric == nil {
		t.Error("RatingRubric came back nil, want non-nil empty map (empty≠null)")
	} else if len(got.RatingRubric) != 0 {
		t.Errorf("RatingRubric = %#v, want empty map", got.RatingRubric)
	}
	if got.RubricDetail == nil {
		t.Error("RubricDetail came back nil, want non-nil empty struct (empty≠null)")
	} else if got.RubricDetail.Scale != nil {
		t.Errorf("RubricDetail.Scale = %#v, want nil", got.RubricDetail.Scale)
	}
	if got.RubricProvenance != nil {
		t.Errorf("RubricProvenance = %#v, want nil (null≠empty)", got.RubricProvenance)
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
	// Update a field and re-put; CreatedAt must be preserved. Also mutate ALL
	// THREE additive fields so the ON CONFLICT DO UPDATE SET path is proven to
	// actually update the new columns (rating_rubric/rubric_detail/
	// rubric_provenance), not just Name/CreatedAt — a stale excluded.col clause or
	// a column-order slip in the upsert would otherwise pass silently.
	tmpl.Name = "Helpfulness v2"
	tmpl.UpdatedAt = time.Time{} // force store to stamp a new updated_at
	tmpl.RatingRubric = map[string]map[string]string{"quality": {"1": "poor", "5": "excellent"}}
	tmpl.RubricDetail = &registry.RubricDetail{Scale: &registry.RubricScale{Min: 0, Max: 10}}
	tmpl.RubricProvenance = &registry.RubricProvenance{
		Method:         "hand-authored",
		GeneratorModel: "none",
		SampleInputRef: "sample-002",
		GeneratedAt:    time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		APIVersion:     "mizan.dev/v1alpha1",
	}
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
	// The three additive fields must reflect the UPDATED values, not the originals.
	if !reflect.DeepEqual(got.RatingRubric, tmpl.RatingRubric) {
		t.Errorf("RatingRubric not updated on upsert: got %#v want %#v", got.RatingRubric, tmpl.RatingRubric)
	}
	if !reflect.DeepEqual(got.RubricDetail, tmpl.RubricDetail) {
		t.Errorf("RubricDetail not updated on upsert: got %#v want %#v", got.RubricDetail, tmpl.RubricDetail)
	}
	if !reflect.DeepEqual(got.RubricProvenance, tmpl.RubricProvenance) {
		t.Errorf("RubricProvenance not updated on upsert: got %#v want %#v", got.RubricProvenance, tmpl.RubricProvenance)
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
