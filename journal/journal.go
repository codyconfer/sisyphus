package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codyconfer/sisyphus/internal/duckdb"
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

var ErrUnavailable = errors.New("journal store unavailable")

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

type Record struct {
	Ts    time.Time
	Attrs map[string]string
}

type Result struct {
	Columns []string
	Rows    [][]string
}

type Store struct {
	h *duckdb.Handle
}

type Option func(*duckdb.Options)

func WithIdle(d time.Duration) Option { return func(o *duckdb.Options) { o.Idle = d } }

func WithTimeout(d time.Duration) Option { return func(o *duckdb.Options) { o.Timeout = d } }

func WithMaxHold(d time.Duration) Option { return func(o *duckdb.Options) { o.MaxHold = d } }

func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	o := duckdb.Options{Unavailable: ErrUnavailable}
	for _, fn := range opts {
		fn(&o)
	}
	h := duckdb.NewHandle(path, schema, o)
	if err := h.Ensure(ctx); err != nil {
		return nil, err
	}
	return &Store{h: h}, nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return s.h.Close()
}

func (s *Store) Begin(ctx context.Context, kind, name string, attrs map[string]string) (int64, error) {
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

func (s *Store) RollUp(ctx context.Context, id int64) error {
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

func (s *Store) Prune(ctx context.Context, before time.Time) (int, error) {
	if s == nil || s.h == nil {
		return 0, ErrUnavailable
	}
	if before.IsZero() {
		return 0, nil
	}
	return s.evict(ctx, olderThan, before)
}

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

func (s *Store) Children(ctx context.Context, parentID int64) ([]Run, error) {
	if s == nil || s.h == nil {
		return nil, ErrUnavailable
	}
	return s.queryRuns(ctx, `SELECT id, kind, name, started_at, finished_at, count, error, attrs
		FROM runs WHERE parent_id = ? ORDER BY started_at`, parentID)
}

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

func (s *Store) Query(ctx context.Context, query string, args ...any) (Result, error) {
	if s == nil || s.h == nil {
		return Result{}, ErrUnavailable
	}
	var res Result
	err := s.h.Do(ctx, func(db *sql.DB) error {
		var err error
		res, err = queryDB(ctx, db, query, args...)
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

func queryDB(ctx context.Context, db *sql.DB, query string, args ...any) (Result, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return Result{}, err
	}
	var data [][]string
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return Result{}, err
		}
		row := make([]string, len(cols))
		for i, c := range cells {
			row[i] = cellString(c)
		}
		data = append(data, row)
	}
	if err := rows.Err(); err != nil {
		return Result{}, err
	}
	return Result{Columns: cols, Rows: data}, nil
}

func cellString(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(t)
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
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
