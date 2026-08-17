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

// Package sqlite provides the default SQLite-backed registry.Store.
//
// It uses the pure-Go modernc.org/sqlite driver (a drop-in database/sql
// backend) so that `go install .../cmd/mizan` is cgo-free — a requirement of
// the rev-3 templates CI, which `go install`s the validator in a stock
// setup-go runner with no C toolchain (design/collaboration-design.md §3.7,
// architecture-final.md §8).
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// Registers the pure-Go "sqlite" driver with database/sql (side-effect
	// import); no symbols are referenced directly.
	_ "modernc.org/sqlite"

	"github.com/ghchinoy/mizan/internal/registry"
)

// Store is the SQLite-backed implementation of registry.Store.
type Store struct {
	db *sql.DB
}

var _ registry.Store = (*Store)(nil)

// Open returns a Store backed by the SQLite database at path. Parent
// directories are created as needed. Pass ":memory:" for an ephemeral DB.
func Open(path string) (*Store, error) {
	if path != ":memory:" && path != "" {
		if dir := filepath.Dir(path); dir != "" {
			// 0700: the DB holds private template bodies; keep it owner-only.
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("sqlite: create db dir: %w", err)
			}
		}
	}
	// modernc registers itself under the driver name "sqlite".
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	// Restrict the DB file to owner-only (modernc creates it 0644 by default).
	// The file is created lazily, so touch it via a ping before chmod. Skip for
	// the in-memory / anonymous DBs which have no file on disk.
	if path != ":memory:" && path != "" {
		if err := db.Ping(); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite: open: %w", err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite: chmod db file: %w", err)
		}
	}
	// A single connection avoids "database is locked" on file-backed DBs and is
	// required for an in-memory DB to persist across statements.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// migrate brings the schema up to v2 (idempotent, version-gated via
// PRAGMA user_version). The provenance/sync columns (source, content_hash,
// imported_at, updated_at, dirty) were included at v1 so P2's sync layer needed
// no migration churn (collab §3.9). v2 adds the three additive RFC-0001 template
// fields — rating_rubric, rubric_detail, rubric_provenance — as JSON TEXT columns
// (registry-provenance-persistence-scope §4/§6): fresh DBs are born v2-shaped by
// the CREATE TABLE below; existing v1 DBs gain the columns in place via ALTER.
func (s *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS metric_templates (
    id                     TEXT PRIMARY KEY,
    name                   TEXT NOT NULL,
    description            TEXT NOT NULL DEFAULT '',
    version                TEXT NOT NULL DEFAULT '',
    authors                TEXT NOT NULL DEFAULT '[]',   -- JSON []Author
    maintainers            TEXT NOT NULL DEFAULT '[]',   -- JSON []string
    license                TEXT NOT NULL DEFAULT '',
    tags                   TEXT NOT NULL DEFAULT '[]',   -- JSON []string
    kind                   TEXT NOT NULL,
    modalities             TEXT NOT NULL DEFAULT '[]',   -- JSON []Modality
    inputs                 TEXT NOT NULL DEFAULT '[]',   -- JSON []InputSpec
    metric_prompt_template TEXT NOT NULL DEFAULT '',
    system_instruction     TEXT NOT NULL DEFAULT '',
    candidate_field_name   TEXT NOT NULL DEFAULT '',
    baseline_field_name    TEXT NOT NULL DEFAULT '',
    rubric_groups          TEXT NOT NULL DEFAULT 'null', -- JSON map[string][]string
    response_schema        TEXT NOT NULL DEFAULT 'null', -- JSON *Schema
    autorater_model        TEXT NOT NULL DEFAULT '',
    sampling_count         INTEGER NOT NULL DEFAULT 0,
    flip_enabled           INTEGER NOT NULL DEFAULT 0,
    -- additive RFC-0001 template fields (v2; scope §4)
    rating_rubric          TEXT NOT NULL DEFAULT 'null', -- JSON map[string]map[string]string
    rubric_detail          TEXT NOT NULL DEFAULT 'null', -- JSON *RubricDetail
    rubric_provenance      TEXT NOT NULL DEFAULT 'null', -- JSON *RubricProvenance
    -- provenance / sync (collab §3.9)
    source                 TEXT NOT NULL DEFAULT '',
    content_hash           TEXT NOT NULL DEFAULT '',
    dirty                  INTEGER NOT NULL DEFAULT 0,
    created_at             TIMESTAMP,
    updated_at             TIMESTAMP,
    imported_at            TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_metric_templates_source ON metric_templates(source);
`
	var uv int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&uv); err != nil {
		return fmt.Errorf("sqlite: read user_version: %w", err)
	}

	// Run the schema build, the v1→v2 column adds, and the version bump inside a
	// SINGLE transaction so migration is atomic: SQLite DDL (CREATE/ALTER) and the
	// user_version header write all participate in the transaction and roll back
	// together on any failure. Without this, a crash after some ALTERs but before
	// user_version=2 would leave the DB at v1 with columns already added, so the
	// next Open re-runs the ALTERs and fails with "duplicate column name" — a
	// bricked DB. With it, an interrupted migration rolls back cleanly to v1 and
	// the next Open migrates fresh.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin migration: %w", err)
	}
	// Roll back on every non-commit path; a Rollback after a successful Commit is
	// a harmless no-op (sql.ErrTxDone), so the flag keeps the happy path clean.
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// CREATE TABLE IF NOT EXISTS builds fresh DBs at v2 shape; it is a no-op for
	// an existing table (which is missing the v2 columns and needs the ALTERs).
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("sqlite: migrate base schema: %w", err)
	}
	// Existing v1 DBs: the table already exists WITHOUT the v2 columns → add them.
	// ADD COLUMN is a cheap metadata-only op; existing rows take the 'null'
	// default, which unmarshalIf reads back as nil — exactly what they returned
	// before (they never held these fields). No data is rewritten or dropped.
	if uv == 1 {
		for _, alter := range []string{
			"ALTER TABLE metric_templates ADD COLUMN rating_rubric     TEXT NOT NULL DEFAULT 'null'",
			"ALTER TABLE metric_templates ADD COLUMN rubric_detail     TEXT NOT NULL DEFAULT 'null'",
			"ALTER TABLE metric_templates ADD COLUMN rubric_provenance TEXT NOT NULL DEFAULT 'null'",
		} {
			if _, err := tx.ExecContext(ctx, alter); err != nil {
				return fmt.Errorf("sqlite: migrate v2 alter: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 2;"); err != nil {
		return fmt.Errorf("sqlite: set user_version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit migration: %w", err)
	}
	committed = true
	return nil
}

// Put upserts a template by ID. CreatedAt is preserved on first insert;
// UpdatedAt is set to now when zero.
func (s *Store) Put(ctx context.Context, t *registry.MetricTemplate) error {
	if t == nil {
		return errors.New("sqlite: nil template")
	}
	if t.ID == "" {
		return errors.New("sqlite: template ID is required")
	}
	now := time.Now().UTC()
	created := t.CreatedAt
	if created.IsZero() {
		created = now
	}
	updated := t.UpdatedAt
	if updated.IsZero() {
		updated = now
	}

	authors := mustJSON(t.Authors)
	maintainers := mustJSON(t.Maintainers)
	tags := mustJSON(t.Tags)
	modalities := mustJSON(t.Modalities)
	inputs := mustJSON(t.Inputs)
	rubric := mustJSON(t.RubricGroups)
	schema := mustJSON(t.ResponseSchema)
	ratingRubric := mustJSON(t.RatingRubric)
	rubricDetail := mustJSON(t.RubricDetail)
	rubricProvenance := mustJSON(t.RubricProvenance)

	const q = `
INSERT INTO metric_templates (
    id, name, description, version, authors, maintainers, license, tags,
    kind, modalities, inputs, metric_prompt_template, system_instruction,
    candidate_field_name, baseline_field_name, rubric_groups, response_schema,
    autorater_model, sampling_count, flip_enabled,
    rating_rubric, rubric_detail, rubric_provenance,
    source, content_hash, dirty, created_at, updated_at, imported_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?,
    ?, ?, ?, ?,
    ?, ?, ?,
    ?, ?, ?,
    ?, ?, ?, ?, ?, ?
)
ON CONFLICT(id) DO UPDATE SET
    name=excluded.name, description=excluded.description, version=excluded.version,
    authors=excluded.authors, maintainers=excluded.maintainers, license=excluded.license,
    tags=excluded.tags, kind=excluded.kind, modalities=excluded.modalities,
    inputs=excluded.inputs, metric_prompt_template=excluded.metric_prompt_template,
    system_instruction=excluded.system_instruction, candidate_field_name=excluded.candidate_field_name,
    baseline_field_name=excluded.baseline_field_name, rubric_groups=excluded.rubric_groups,
    response_schema=excluded.response_schema, autorater_model=excluded.autorater_model,
    sampling_count=excluded.sampling_count, flip_enabled=excluded.flip_enabled,
    rating_rubric=excluded.rating_rubric, rubric_detail=excluded.rubric_detail,
    rubric_provenance=excluded.rubric_provenance,
    source=excluded.source, content_hash=excluded.content_hash, dirty=excluded.dirty,
    updated_at=excluded.updated_at, imported_at=excluded.imported_at
`
	_, err := s.db.ExecContext(ctx, q,
		t.ID, t.Name, t.Description, t.Version, authors, maintainers, t.License, tags,
		string(t.Kind), modalities, inputs, t.MetricPromptTemplate, t.SystemInstruction,
		t.CandidateFieldName, t.BaselineFieldName, rubric, schema,
		t.AutoraterModel, t.SamplingCount, boolToInt(t.FlipEnabled),
		ratingRubric, rubricDetail, rubricProvenance,
		t.Source, t.ContentHash, boolToInt(t.Dirty), created, updated, nullTime(t.ImportedAt),
	)
	if err != nil {
		return fmt.Errorf("sqlite: put %q: %w", t.ID, err)
	}
	// Reflect the persisted timestamps back to the caller.
	t.CreatedAt = created
	t.UpdatedAt = updated
	return nil
}

// Get returns the template with the given ID, or registry.ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (*registry.MetricTemplate, error) {
	row := s.db.QueryRowContext(ctx, selectCols+" FROM metric_templates WHERE id = ?", id)
	t, err := scanTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, registry.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get %q: %w", id, err)
	}
	return t, nil
}

// List returns templates matching the filter, ordered by ID.
func (s *Store) List(ctx context.Context, f registry.ListFilter) ([]registry.MetricTemplate, error) {
	var (
		where []string
		args  []any
	)
	if f.Source != "" {
		where = append(where, "source = ?")
		args = append(args, f.Source)
	}
	if f.DirtyOnly {
		where = append(where, "dirty = 1")
	}
	if f.Namespace != "" {
		// Escape LIKE metacharacters so a namespace containing %, _ or \ is
		// matched literally (a bare "%" must not match every row).
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Namespace)
		where = append(where, `id LIKE ? ESCAPE '\'`)
		args = append(args, esc+"/%")
	}
	q := selectCols + " FROM metric_templates"
	if len(where) > 0 {
		// where holds only constant SQL fragments built above; every user value
		// is bound through args placeholders, so this is not an injection point.
		q += " WHERE " + strings.Join(where, " AND ") //nolint:gosec // G202: constant clause fragments only; values are parameterized via args
	}
	q += " ORDER BY id"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list: %w", err)
	}
	defer rows.Close()

	var out []registry.MetricTemplate
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list scan: %w", err)
		}
		if !matchesInMemory(t, f) {
			continue
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// ListChangedSince returns templates whose updated_at is at or after since.
func (s *Store) ListChangedSince(ctx context.Context, since time.Time) ([]registry.MetricTemplate, error) {
	rows, err := s.db.QueryContext(ctx,
		selectCols+" FROM metric_templates WHERE updated_at >= ? ORDER BY updated_at", since.UTC())
	if err != nil {
		return nil, fmt.Errorf("sqlite: list changed: %w", err)
	}
	defer rows.Close()

	var out []registry.MetricTemplate
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list changed scan: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Delete removes a template by ID, returning registry.ErrNotFound if absent.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM metric_templates WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("sqlite: delete %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: delete %q: %w", id, err)
	}
	if n == 0 {
		return registry.ErrNotFound
	}
	return nil
}

// matchesInMemory applies the slice-valued filters (Modalities, Kinds) that are
// stored as JSON and therefore not expressible in the SQL WHERE clause.
func matchesInMemory(t *registry.MetricTemplate, f registry.ListFilter) bool {
	if len(f.Kinds) > 0 {
		ok := false
		for _, k := range f.Kinds {
			if t.Kind == k {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(f.Modalities) > 0 {
		ok := false
		for _, want := range f.Modalities {
			for _, have := range t.Modalities {
				if want == have {
					ok = true
					break
				}
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

const selectCols = `SELECT
    id, name, description, version, authors, maintainers, license, tags,
    kind, modalities, inputs, metric_prompt_template, system_instruction,
    candidate_field_name, baseline_field_name, rubric_groups, response_schema,
    autorater_model, sampling_count, flip_enabled,
    rating_rubric, rubric_detail, rubric_provenance,
    source, content_hash, dirty, created_at, updated_at, imported_at`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanTemplate(sc scanner) (*registry.MetricTemplate, error) {
	var (
		t                                            registry.MetricTemplate
		authors, maintainers, tags, modalities       string
		inputs, rubric, schema                       string
		ratingRubric, rubricDetail, rubricProvenance string
		kind                                         string
		flip, dirty                                  int
		createdAt, updatedAt, importedAt             sql.NullTime
	)
	if err := sc.Scan(
		&t.ID, &t.Name, &t.Description, &t.Version, &authors, &maintainers, &t.License, &tags,
		&kind, &modalities, &inputs, &t.MetricPromptTemplate, &t.SystemInstruction,
		&t.CandidateFieldName, &t.BaselineFieldName, &rubric, &schema,
		&t.AutoraterModel, &t.SamplingCount, &flip,
		&ratingRubric, &rubricDetail, &rubricProvenance,
		&t.Source, &t.ContentHash, &dirty, &createdAt, &updatedAt, &importedAt,
	); err != nil {
		return nil, err
	}
	t.Kind = registry.MetricKind(kind)
	t.FlipEnabled = flip != 0
	t.Dirty = dirty != 0
	if createdAt.Valid {
		t.CreatedAt = createdAt.Time.UTC()
	}
	if updatedAt.Valid {
		t.UpdatedAt = updatedAt.Time.UTC()
	}
	if importedAt.Valid {
		t.ImportedAt = importedAt.Time.UTC()
	}

	if err := unmarshalIf(authors, &t.Authors); err != nil {
		return nil, err
	}
	if err := unmarshalIf(maintainers, &t.Maintainers); err != nil {
		return nil, err
	}
	if err := unmarshalIf(tags, &t.Tags); err != nil {
		return nil, err
	}
	if err := unmarshalIf(modalities, &t.Modalities); err != nil {
		return nil, err
	}
	if err := unmarshalIf(inputs, &t.Inputs); err != nil {
		return nil, err
	}
	if err := unmarshalIf(rubric, &t.RubricGroups); err != nil {
		return nil, err
	}
	if err := unmarshalIf(schema, &t.ResponseSchema); err != nil {
		return nil, err
	}
	if err := unmarshalIf(ratingRubric, &t.RatingRubric); err != nil {
		return nil, err
	}
	if err := unmarshalIf(rubricDetail, &t.RubricDetail); err != nil {
		return nil, err
	}
	if err := unmarshalIf(rubricProvenance, &t.RubricProvenance); err != nil {
		return nil, err
	}
	return &t, nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// The domain types are all plain data; marshal cannot realistically fail.
		return "null"
	}
	return string(b)
}

func unmarshalIf(s string, dst any) error {
	if s == "" || s == "null" {
		return nil
	}
	return json.Unmarshal([]byte(s), dst)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullTime maps a zero time.Time to NULL so it round-trips as a zero value.
func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}
