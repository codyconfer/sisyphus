package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/codyconfer/sisyphus/internal/duckdb"
)

const schema = `
CREATE SEQUENCE IF NOT EXISTS journal_run_seq;
CREATE TABLE IF NOT EXISTS runs (
  id          BIGINT PRIMARY KEY DEFAULT nextval('journal_run_seq'),
  parent_id   BIGINT,
  kind        VARCHAR,
  name        VARCHAR,
  started_at  TIMESTAMP,
  finished_at TIMESTAMP,
  count       INTEGER,
  error       VARCHAR,
  attrs       VARCHAR
);
CREATE SEQUENCE IF NOT EXISTS journal_rec_seq;
CREATE TABLE IF NOT EXISTS records (
  id     BIGINT PRIMARY KEY DEFAULT nextval('journal_rec_seq'),
  run_id BIGINT,
  ts     TIMESTAMP,
  attrs  VARCHAR
);
`

// ErrUnavailable is returned when a method is called on a nil or closed store.
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

// Result is a tabular ad-hoc query response.
type Result struct {
	Columns []string
	Rows    [][]string
}

type Store struct {
	mu sync.Mutex
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := duckdb.Open(ctx, path, schema)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Begin(ctx context.Context, kind, name string, attrs map[string]string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO runs (kind, name, started_at, attrs) VALUES (?, ?, ?, ?) RETURNING id`,
		kind, name, time.Now(), marshalAttrs(attrs),
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) RollUp(ctx context.Context, id int64) error {
	if s == nil || s.db == nil || id == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET finished_at = ?,
		   count = coalesce((SELECT sum(count) FROM runs WHERE parent_id = ?), 0)
		 WHERE id = ?`, time.Now(), id, id)
	return err
}

func (s *Store) Add(ctx context.Context, run Run, records []Record) (int64, error) {
	if s == nil || s.db == nil {
		return 0, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO runs (parent_id, kind, name, started_at, finished_at, count, error, attrs)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		duckdb.NullInt(run.ParentID), run.Kind, run.Name, run.Started, run.Finished,
		run.Count, duckdb.NullStr(run.Error), marshalAttrs(run.Attrs),
	).Scan(&id); err != nil {
		return 0, err
	}
	for _, r := range records {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO records (run_id, ts, attrs) VALUES (?, ?, ?)`,
			id, duckdb.NullTime(r.Ts), marshalAttrs(r.Attrs),
		); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) Recent(ctx context.Context, limit int) ([]Run, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	return s.queryRuns(ctx, `SELECT id, kind, name, started_at, finished_at, count, error, attrs
		FROM runs WHERE parent_id IS NULL ORDER BY started_at DESC LIMIT ?`, limit)
}

func (s *Store) Children(ctx context.Context, parentID int64) ([]Run, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	return s.queryRuns(ctx, `SELECT id, kind, name, started_at, finished_at, count, error, attrs
		FROM runs WHERE parent_id = ? ORDER BY started_at`, parentID)
}

func (s *Store) Get(ctx context.Context, id int64) (Run, bool, error) {
	if s == nil || s.db == nil {
		return Run{}, false, nil
	}
	runs, err := s.queryRuns(ctx, `SELECT id, kind, name, started_at, finished_at, count, error, attrs
		FROM runs WHERE id = ?`, id)
	if err != nil || len(runs) == 0 {
		return Run{}, false, err
	}
	return runs[0], true, nil
}

func (s *Store) Records(ctx context.Context, runID int64) ([]Record, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT ts, attrs FROM records WHERE run_id = ? ORDER BY ts DESC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		var ts sql.NullTime
		var attrs sql.NullString
		if err := rows.Scan(&ts, &attrs); err != nil {
			return nil, err
		}
		r.Ts, r.Attrs = ts.Time, unmarshalAttrs(attrs.String)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Query runs an ad-hoc SQL statement against the open journal and returns a
// string table. Intended for read-only inspection UIs.
func (s *Store) Query(ctx context.Context, query string, args ...any) (Result, error) {
	if s == nil || s.db == nil {
		return Result{}, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return queryDB(ctx, s.db, query, args...)
}

func (s *Store) queryRuns(ctx context.Context, query string, args ...any) ([]Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var name, errText, attrs sql.NullString
		var fin sql.NullTime
		var count sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Kind, &name, &r.Started, &fin, &count, &errText, &attrs); err != nil {
			return nil, err
		}
		r.Name, r.Finished, r.Count, r.Error = name.String, fin.Time, int(count.Int64), errText.String
		r.Attrs = unmarshalAttrs(attrs.String)
		out = append(out, r)
	}
	return out, rows.Err()
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
