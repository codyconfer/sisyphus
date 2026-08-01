// Package journal is a generic activity log in a single DuckDB file: runs
// (optionally nested one level, parent/child) and per-run records, each
// carrying a free-form string attribute map. Retention is explicit — Prune by
// age or Retain by count — and rewrites the tables so the file actually
// shrinks.
package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/codyconfer/sisyphus/duckopt"
	"github.com/codyconfer/sisyphus/internal/duckdb"
	"github.com/codyconfer/sisyphus/storeerr"
	"github.com/codyconfer/sisyphus/tabular"
)

const runsColumns = `
  id          BIGINT PRIMARY KEY DEFAULT nextval('journal_run_seq'),
  parent_id   BIGINT,
  kind        VARCHAR,
  name        VARCHAR,
  started_at  TIMESTAMP,
  finished_at TIMESTAMP,
  count       INTEGER,
  error       VARCHAR,
  attrs       VARCHAR
`

const recordsColumns = `
  id     BIGINT PRIMARY KEY DEFAULT nextval('journal_rec_seq'),
  run_id BIGINT,
  ts     TIMESTAMP,
  attrs  VARCHAR
`

const schema = `
CREATE SEQUENCE IF NOT EXISTS journal_run_seq;
CREATE TABLE IF NOT EXISTS runs (` + runsColumns + `);
CREATE SEQUENCE IF NOT EXISTS journal_rec_seq;
CREATE TABLE IF NOT EXISTS records (` + recordsColumns + `);
`

const recordChunk = 200

const recordInsertPrefix = `INSERT INTO records (run_id, ts, attrs) VALUES `

var fullRecordInsert = recordInsert(recordChunk)

// ErrUnavailable is returned when a method is called on a nil or closed store.
// It wraps storeerr.ErrUnavailable, so both errors.Is checks match.
var ErrUnavailable = fmt.Errorf("journal %w", storeerr.ErrUnavailable)

// Run is one unit of logged activity. A Run with ParentID 0 is a top-level
// (parent) run; a non-zero ParentID nests it under that parent. Count is the
// run's own item count; FinishRun overwrites a parent's Count with the sum of
// its children's.
type Run struct {
	ID       int64
	ParentID int64
	Kind     string
	Name     string
	Started  time.Time
	Finished time.Time
	Count    int
	Error    string
	Attrs    map[string]string
}

// Record is one timestamped detail row attached to a run, described entirely
// by its attribute map.
type Record struct {
	Ts    time.Time
	Attrs map[string]string
}

// Result is a tabular ad-hoc query response.
type Result = tabular.Result

// Store is an append-mostly run/record journal in one DuckDB file.
//
// A nil *Store is a valid no-op: every method returns ErrUnavailable, except
// Close, which returns nil.
type Store struct {
	h *duckdb.Handle
}

// Open opens (or creates) the journal's DuckDB file at path and ensures its
// schema. The file is owner-only (0600) and single-writer.
func Open(ctx context.Context, path string, opts ...duckopt.Option) (*Store, error) {
	h := duckdb.NewHandle(path, schema, duckdb.OptionsFrom(duckopt.Build(opts...), ErrUnavailable))
	if err := h.Ensure(ctx); err != nil {
		return nil, err
	}
	return &Store{h: h}, nil
}

// Close releases the database. It is safe on a nil *Store and returns nil.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return s.h.Close()
}

// StartRun inserts an open top-level run (started now, not finished) and
// returns its id, which children pass as Run.ParentID and the caller later
// hands to FinishRun.
func (s *Store) StartRun(ctx context.Context, kind, name string, attrs map[string]string) (int64, error) {
	if s == nil || s.h == nil {
		return 0, ErrUnavailable
	}
	var id int64
	err := s.h.Do(ctx, func(db *sql.DB) error {
		return db.QueryRowContext(ctx,
			`INSERT INTO runs (kind, name, started_at, attrs) VALUES (?, ?, ?, ?) RETURNING id`,
			kind, name, time.Now(), marshalAttrs(attrs),
		).Scan(&id)
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// FinishRun stamps the run's finished time and replaces its count with the
// sum of its children's counts (0 when it has none). An id of 0 is a no-op,
// so an early error path can finish a run it never started.
func (s *Store) FinishRun(ctx context.Context, id int64) error {
	if s == nil || s.h == nil {
		return ErrUnavailable
	}
	if id == 0 {
		return nil
	}
	return s.h.Do(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE runs SET finished_at = ?,
			   count = coalesce((SELECT sum(count) FROM runs WHERE parent_id = ?), 0)
			 WHERE id = ?`, time.Now(), id, id)
		return err
	})
}

// Add inserts a completed run together with its records in one transaction
// and returns the new run id. Unlike StartRun it stores run as given —
// Started, Finished, Count, Error and ParentID all come from the caller.
func (s *Store) Add(ctx context.Context, run Run, records []Record) (int64, error) {
	if s == nil || s.h == nil {
		return 0, ErrUnavailable
	}
	var id int64
	err := s.h.Do(ctx, func(db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO runs (parent_id, kind, name, started_at, finished_at, count, error, attrs)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			duckdb.NullInt(run.ParentID), run.Kind, run.Name, run.Started, run.Finished,
			run.Count, duckdb.NullStr(run.Error), marshalAttrs(run.Attrs),
		).Scan(&id); err != nil {
			return err
		}
		if err := insertRecords(ctx, tx, id, records); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

func insertRecords(ctx context.Context, tx *sql.Tx, runID int64, records []Record) error {
	if len(records) == 0 {
		return nil
	}
	owner := any(runID)
	args := make([]any, 0, min(len(records), recordChunk)*3)
	for start := 0; start < len(records); start += recordChunk {
		chunk := records[start:min(start+recordChunk, len(records))]
		query := fullRecordInsert
		if len(chunk) != recordChunk {
			query = recordInsert(len(chunk))
		}
		args = args[:0]
		for _, r := range chunk {
			args = append(args, owner, duckdb.NullTime(r.Ts), marshalAttrs(r.Attrs))
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

func recordInsert(rows int) string {
	var b strings.Builder
	b.Grow(len(recordInsertPrefix) + rows*len("(?, ?, ?),"))
	b.WriteString(recordInsertPrefix)
	for i := range rows {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("(?, ?, ?)")
	}
	return b.String()
}

// Delete removes the run, its child runs, and every record belonging to any
// of them, in one transaction. An id of 0 is a no-op.
func (s *Store) Delete(ctx context.Context, id int64) error {
	if s == nil || s.h == nil {
		return ErrUnavailable
	}
	if id == 0 {
		return nil
	}
	return s.h.Do(ctx, func(db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		stmts := []string{
			`DELETE FROM records WHERE run_id = ? OR run_id IN (SELECT id FROM runs WHERE parent_id = ?)`,
			`DELETE FROM runs WHERE parent_id = ?`,
			`DELETE FROM runs WHERE id = ?`,
		}
		for i, stmt := range stmts {
			args := []any{id}
			if i == 0 {
				args = append(args, id)
			}
			if _, err := tx.ExecContext(ctx, stmt, args...); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
}

const olderThan = `parent_id IS NULL AND started_at < ?`

const beyondNewest = `parent_id IS NULL AND id NOT IN (
	SELECT id FROM runs WHERE parent_id IS NULL ORDER BY started_at DESC, id DESC LIMIT ?)`

// Prune deletes top-level runs started before the given time, along with
// their children and records, and returns how many top-level runs went. When
// anything is deleted the tables are rewritten and the database checkpointed
// so the file shrinks. A zero time deletes nothing.
func (s *Store) Prune(ctx context.Context, before time.Time) (int, error) {
	if s == nil || s.h == nil {
		return 0, ErrUnavailable
	}
	if before.IsZero() {
		return 0, nil
	}
	return s.evict(ctx, olderThan, before)
}

// Retain keeps only the newest maxRuns top-level runs, deleting the rest
// with their children and records, and returns how many top-level runs went.
// Like Prune it compacts the file after deleting. maxRuns <= 0 deletes
// nothing (it does not mean "delete everything").
func (s *Store) Retain(ctx context.Context, maxRuns int) (int, error) {
	if s == nil || s.h == nil {
		return 0, ErrUnavailable
	}
	if maxRuns <= 0 {
		return 0, nil
	}
	return s.evict(ctx, beyondNewest, maxRuns)
}

func (s *Store) evict(ctx context.Context, doomed string, arg any) (int, error) {
	var removed int
	err := s.h.Do(ctx, func(db *sql.DB) error {
		var doomedRuns int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE `+doomed, arg).Scan(&doomedRuns); err != nil {
			return err
		}
		if doomedRuns == 0 {
			return nil
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, st := range evictStatements(doomed) {
			args := make([]any, st.args)
			for i := range args {
				args[i] = arg
			}
			if _, err := tx.ExecContext(ctx, st.query, args...); err != nil {
				return err
			}
		}
		for _, stmt := range compaction {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		removed = doomedRuns
		_, err = db.ExecContext(ctx, `CHECKPOINT`)
		return err
	})
	return removed, err
}

type evictStatement struct {
	query string
	args  int
}

func evictStatements(doomed string) []evictStatement {
	return []evictStatement{
		{`DELETE FROM records WHERE run_id IN (
			SELECT id FROM runs WHERE (` + doomed + `)
			   OR parent_id IN (SELECT id FROM runs WHERE ` + doomed + `))`, 2},
		{`DELETE FROM runs WHERE parent_id IN (SELECT id FROM runs WHERE ` + doomed + `)`, 1},
		{`DELETE FROM runs WHERE ` + doomed, 1},
	}
}

var compaction = append(rewriteTable("records", recordsColumns), rewriteTable("runs", runsColumns)...)

func rewriteTable(table, columns string) []string {
	fresh := table + "_compact"
	return []string{
		`DROP TABLE IF EXISTS ` + fresh,
		`CREATE TABLE ` + fresh + ` (` + columns + `)`,
		`INSERT INTO ` + fresh + ` SELECT * FROM ` + table,
		`DROP TABLE ` + table,
		`ALTER TABLE ` + fresh + ` RENAME TO ` + table,
	}
}

// Recent returns up to limit top-level runs, newest first (limit <= 0 means
// 20). Child runs are not included; fetch them with Children.
func (s *Store) Recent(ctx context.Context, limit int) ([]Run, error) {
	if s == nil || s.h == nil {
		return nil, ErrUnavailable
	}
	if limit <= 0 {
		limit = 20
	}
	return s.queryRuns(ctx, `SELECT id, kind, name, started_at, finished_at, count, error, attrs
		FROM runs WHERE parent_id IS NULL ORDER BY started_at DESC LIMIT ?`, limit)
}

// Children returns the child runs of parentID in start order. The returned
// runs carry ParentID 0 because the query does not re-read it.
func (s *Store) Children(ctx context.Context, parentID int64) ([]Run, error) {
	if s == nil || s.h == nil {
		return nil, ErrUnavailable
	}
	return s.queryRuns(ctx, `SELECT id, kind, name, started_at, finished_at, count, error, attrs
		FROM runs WHERE parent_id = ? ORDER BY started_at`, parentID)
}

// Get returns the run with the given id, reporting found=false (with a nil
// error) when no such run exists.
func (s *Store) Get(ctx context.Context, id int64) (Run, bool, error) {
	if s == nil || s.h == nil {
		return Run{}, false, ErrUnavailable
	}
	runs, err := s.queryRuns(ctx, `SELECT id, kind, name, started_at, finished_at, count, error, attrs
		FROM runs WHERE id = ?`, id)
	if err != nil || len(runs) == 0 {
		return Run{}, false, err
	}
	return runs[0], true, nil
}

// Records returns the records attached to runID, newest first.
func (s *Store) Records(ctx context.Context, runID int64) ([]Record, error) {
	if s == nil || s.h == nil {
		return nil, ErrUnavailable
	}
	var out []Record
	err := s.h.Do(ctx, func(db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `SELECT ts, attrs FROM records WHERE run_id = ? ORDER BY ts DESC`, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		var found []Record
		for rows.Next() {
			var r Record
			var ts sql.NullTime
			var attrs sql.NullString
			if err := rows.Scan(&ts, &attrs); err != nil {
				return err
			}
			r.Ts, r.Attrs = ts.Time, unmarshalAttrs(attrs.String)
			found = append(found, r)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		out = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Query runs an arbitrary SQL statement against the journal database and
// returns the rows as a string table. The SQL is passed to DuckDB verbatim —
// nothing stops a destructive statement — so callers exposing this (e.g. to a
// CLI) should treat it as read-only and restrict what they accept.
func (s *Store) Query(ctx context.Context, query string, args ...any) (Result, error) {
	if s == nil || s.h == nil {
		return Result{}, ErrUnavailable
	}
	var res Result
	err := s.h.Do(ctx, func(db *sql.DB) error {
		var err error
		res, err = duckdb.QueryTable(ctx, db, query, args...)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

func (s *Store) queryRuns(ctx context.Context, query string, args ...any) ([]Run, error) {
	var out []Run
	err := s.h.Do(ctx, func(db *sql.DB) error {
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		var found []Run
		for rows.Next() {
			var r Run
			var name, errText, attrs sql.NullString
			var fin sql.NullTime
			var count sql.NullInt64
			if err := rows.Scan(&r.ID, &r.Kind, &name, &r.Started, &fin, &count, &errText, &attrs); err != nil {
				return err
			}
			r.Name, r.Finished, r.Count, r.Error = name.String, fin.Time, int(count.Int64), errText.String
			r.Attrs = unmarshalAttrs(attrs.String)
			found = append(found, r)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		out = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func marshalAttrs(m map[string]string) any {
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return string(b)
}

func unmarshalAttrs(s string) map[string]string {
	if s == "" {
		return nil
	}
	var m map[string]string
	if json.Unmarshal([]byte(s), &m) != nil {
		return nil
	}
	return m
}
