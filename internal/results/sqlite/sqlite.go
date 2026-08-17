// Package sqlite provides the default SQLite-backed results.ResultStore.
//
// It mirrors internal/registry/sqlite precisely: the pure-Go modernc.org/sqlite
// driver (so `go install .../cmd/mizan` stays cgo-free — architecture-final.md
// §8, no second SQL driver), a single connection (SetMaxOpenConns(1)), an
// owner-only DB file (0600) in an owner-only dir (0700), and PRAGMA
// user_version=1. Results are immutable: Put is an insert keyed by RunID, never
// an upsert (design/eval-results-store-design.md §4.5).
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

	"github.com/ghchinoy/mizan/internal/results"
)

// Store is the SQLite-backed implementation of results.ResultStore.
type Store struct {
	db *sql.DB
}

var _ results.ResultStore = (*Store)(nil)

// Open returns a Store backed by the SQLite database at path. Parent directories
// are created as needed. Pass ":memory:" for an ephemeral DB. The results DB is a
// SEPARATE file from the registry DB (own lifecycle/retention/backup).
func Open(path string) (*Store, error) {
	if path != ":memory:" && path != "" {
		if dir := filepath.Dir(path); dir != "" {
			// 0700: the DB holds recorded eval inputs/outputs; keep it owner-only.
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

// migrate applies schema migration v1 (idempotent). The big sub-structs are
// stored as JSON TEXT columns (like registry's metric_templates composites);
// flat columns carry the query surface (run_id PK, run_at, template id/version,
// kind, namespace, scorecard_run_id).
func (s *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS results (
    run_id           TEXT PRIMARY KEY,
    run_at           TIMESTAMP,
    run_kind         TEXT NOT NULL DEFAULT '',
    template_id      TEXT NOT NULL DEFAULT '',
    template_version TEXT NOT NULL DEFAULT '',
    kind             TEXT NOT NULL DEFAULT '',
    namespace        TEXT NOT NULL DEFAULT '',
    scorecard_run_id TEXT NOT NULL DEFAULT '',
    -- JSON-composite columns (the big sub-structs)
    mizan            TEXT NOT NULL DEFAULT 'null', -- JSON MizanBuild
    invocation       TEXT NOT NULL DEFAULT 'null', -- JSON Invocation
    template         TEXT NOT NULL DEFAULT 'null', -- JSON TemplateRef
    autorater        TEXT NOT NULL DEFAULT 'null', -- JSON AppliedAutorater
    rubric           TEXT NOT NULL DEFAULT 'null', -- JSON *RubricRef
    inputs           TEXT NOT NULL DEFAULT 'null', -- JSON []StoredInput
    outcome          TEXT NOT NULL DEFAULT 'null'  -- JSON Outcome
);
CREATE INDEX IF NOT EXISTS idx_results_run_at ON results(run_at);
CREATE INDEX IF NOT EXISTS idx_results_template_id ON results(template_id);
CREATE INDEX IF NOT EXISTS idx_results_namespace ON results(namespace);
CREATE INDEX IF NOT EXISTS idx_results_scorecard ON results(scorecard_run_id);
`
	if _, err := s.db.ExecContext(ctx, "PRAGMA user_version = 1;"); err != nil {
		return fmt.Errorf("sqlite: set user_version: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("sqlite: migrate v1: %w", err)
	}
	return nil
}

// Put inserts a result by RunID. Results are immutable: a RunID that already
// exists is rejected (the PRIMARY KEY constraint fails and the error is
// surfaced), never silently overwritten.
func (s *Store) Put(ctx context.Context, r *results.Result) error {
	if r == nil {
		return errors.New("sqlite: nil result")
	}
	if r.RunID == "" {
		return errors.New("sqlite: result RunID is required")
	}

	// Serialize the JSON-composite columns up front so a marshal failure aborts
	// the insert (no partial/corrupt row) instead of silently persisting "null".
	mizanJSON, err := marshalJSON("mizan", r.Mizan)
	if err != nil {
		return err
	}
	invocationJSON, err := marshalJSON("invocation", r.Invocation)
	if err != nil {
		return err
	}
	templateJSON, err := marshalJSON("template", r.Template)
	if err != nil {
		return err
	}
	autoraterJSON, err := marshalJSON("autorater", r.Autorater)
	if err != nil {
		return err
	}
	rubricJSON, err := marshalJSON("rubric", r.Rubric)
	if err != nil {
		return err
	}
	inputsJSON, err := marshalJSON("inputs", r.Inputs)
	if err != nil {
		return err
	}
	outcomeJSON, err := marshalJSON("outcome", r.Outcome)
	if err != nil {
		return err
	}

	const q = `
INSERT INTO results (
    run_id, run_at, run_kind, template_id, template_version, kind, namespace,
    scorecard_run_id, mizan, invocation, template, autorater, rubric, inputs, outcome
) VALUES (
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?, ?, ?
)`
	_, err = s.db.ExecContext(ctx, q,
		r.RunID, r.RunAt.UTC(), string(r.RunKind), r.Template.ID, r.Template.Version,
		string(r.Template.Kind), namespaceOf(r.Template.ID),
		// scorecard_run_id: always empty in Phase 1 (single results only). The
		// column + filter exist now so scorecard persistence is additive (§4.6).
		"", mizanJSON, invocationJSON,
		templateJSON, autoraterJSON, rubricJSON,
		inputsJSON, outcomeJSON,
	)
	if err != nil {
		return fmt.Errorf("sqlite: put %q (results are immutable; duplicate RunID?): %w", r.RunID, err)
	}
	return nil
}

// Get returns the result with the given RunID, or results.ErrNotFound.
func (s *Store) Get(ctx context.Context, runID string) (*results.Result, error) {
	row := s.db.QueryRowContext(ctx, selectCols+" FROM results WHERE run_id = ?", runID)
	r, err := scanResult(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, results.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get %q: %w", runID, err)
	}
	return r, nil
}

// List returns results matching the filter, newest first (run_at desc, run_id
// desc as a stable tiebreak).
func (s *Store) List(ctx context.Context, f results.ResultFilter) ([]results.Result, error) {
	var (
		where []string
		args  []any
	)
	if f.TemplateID != "" {
		where = append(where, "template_id = ?")
		args = append(args, f.TemplateID)
	}
	if f.TemplateVersion != "" {
		where = append(where, "template_version = ?")
		args = append(args, f.TemplateVersion)
	}
	if f.Namespace != "" {
		where = append(where, "namespace = ?")
		args = append(args, f.Namespace)
	}
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, string(f.Kind))
	}
	if !f.Since.IsZero() {
		where = append(where, "run_at >= ?")
		args = append(args, f.Since.UTC())
	}
	if !f.Until.IsZero() {
		where = append(where, "run_at <= ?")
		args = append(args, f.Until.UTC())
	}
	if f.ScorecardRunID != "" {
		where = append(where, "scorecard_run_id = ?")
		args = append(args, f.ScorecardRunID)
	}

	q := selectCols + " FROM results"
	if len(where) > 0 {
		// where holds only constant SQL fragments built above; every user value is
		// bound through args placeholders, so this is not an injection point.
		q += " WHERE " + strings.Join(where, " AND ") //nolint:gosec // G202: constant clause fragments only; values are parameterized via args
	}
	q += " ORDER BY run_at DESC, run_id DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list: %w", err)
	}
	defer rows.Close()

	var out []results.Result
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list scan: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ListChangedSince returns results whose run_at is at or after since (newest
// first). Results are append-only, so this is the sync/incremental primitive.
func (s *Store) ListChangedSince(ctx context.Context, since time.Time) ([]results.Result, error) {
	rows, err := s.db.QueryContext(ctx,
		selectCols+" FROM results WHERE run_at >= ? ORDER BY run_at DESC, run_id DESC", since.UTC())
	if err != nil {
		return nil, fmt.Errorf("sqlite: list changed: %w", err)
	}
	defer rows.Close()

	var out []results.Result
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list changed scan: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// Delete removes a result by RunID, returning results.ErrNotFound if absent.
func (s *Store) Delete(ctx context.Context, runID string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM results WHERE run_id = ?", runID)
	if err != nil {
		return fmt.Errorf("sqlite: delete %q: %w", runID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: delete %q: %w", runID, err)
	}
	if n == 0 {
		return results.ErrNotFound
	}
	return nil
}

const selectCols = `SELECT
    run_id, run_at, run_kind,
    mizan, invocation, template, autorater, rubric, inputs, outcome`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanResult(sc scanner) (*results.Result, error) {
	var (
		r        results.Result
		runKind  string
		runAt    sql.NullTime
		mizan    string
		invoc    string
		template string
		autorat  string
		rubric   string
		inputs   string
		outcome  string
	)
	if err := sc.Scan(
		&r.RunID, &runAt, &runKind,
		&mizan, &invoc, &template, &autorat, &rubric, &inputs, &outcome,
	); err != nil {
		return nil, err
	}
	r.RunKind = results.RunKind(runKind)
	if runAt.Valid {
		r.RunAt = runAt.Time.UTC()
	}
	if err := unmarshalIf(mizan, &r.Mizan); err != nil {
		return nil, err
	}
	if err := unmarshalIf(invoc, &r.Invocation); err != nil {
		return nil, err
	}
	if err := unmarshalIf(template, &r.Template); err != nil {
		return nil, err
	}
	if err := unmarshalIf(autorat, &r.Autorater); err != nil {
		return nil, err
	}
	if err := unmarshalIf(rubric, &r.Rubric); err != nil {
		return nil, err
	}
	if err := unmarshalIf(inputs, &r.Inputs); err != nil {
		return nil, err
	}
	if err := unmarshalIf(outcome, &r.Outcome); err != nil {
		return nil, err
	}
	return &r, nil
}

// namespaceOf returns the namespace portion of a template id ("<ns>/<slug>"),
// or "" if the id has no "/".
func namespaceOf(id string) string {
	if i := strings.IndexByte(id, '/'); i >= 0 {
		return id[:i]
	}
	return ""
}

// marshalJSON serializes a JSON-composite column value. A marshal failure is
// propagated (never silently coerced to "null"): the domain types are plain
// data, but a caller-supplied field such as Outcome.CustomOutput can carry an
// unmarshalable value, and persisting a corrupt row would be worse than
// rejecting the write. The field name is included so Put's error names the
// offending column.
func marshalJSON(field string, v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("results/sqlite: marshal %s: %w", field, err)
	}
	return string(b), nil
}

func unmarshalIf(s string, dst any) error {
	if s == "" || s == "null" {
		return nil
	}
	return json.Unmarshal([]byte(s), dst)
}
