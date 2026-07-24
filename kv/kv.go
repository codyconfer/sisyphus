package kv

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/codyconfer/sisyphus/internal/duckdb"
)

const schema = `
CREATE TABLE IF NOT EXISTS kv (
  namespace  VARCHAR,
  key        VARCHAR,
  value      VARCHAR,
  expiry     TIMESTAMP,
  updated_at TIMESTAMP,
  PRIMARY KEY (namespace, key)
);
`

// ErrUnavailable is returned when a method is called on a nil or closed store.
var ErrUnavailable = errors.New("kv store unavailable")

type Entry struct {
	Value   string
	Expiry  time.Time
	Updated time.Time
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

func (s *Store) Get(ctx context.Context, namespace, key string) (Entry, bool, error) {
	if s == nil || s.db == nil {
		return Entry{}, false, nil
	}
	if err := ctx.Err(); err != nil {
		return Entry{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var e Entry
	var expiry, updated sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT value, expiry, updated_at FROM kv WHERE namespace = ? AND key = ?`, namespace, key,
	).Scan(&e.Value, &expiry, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	e.Expiry, e.Updated = expiry.Time, updated.Time
	if expired(e.Expiry) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM kv WHERE namespace = ? AND key = ?`, namespace, key)
		return Entry{}, false, nil
	}
	return e, true, nil
}

func (s *Store) Put(ctx context.Context, namespace, key, value string, expiry time.Time) error {
	if s == nil || s.db == nil {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM kv WHERE namespace = ? AND key = ?`, namespace, key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO kv (namespace, key, value, expiry, updated_at) VALUES (?, ?, ?, ?, ?)`,
		namespace, key, value, duckdb.NullTime(expiry), time.Now(),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Delete(ctx context.Context, namespace, key string) error {
	if s == nil || s.db == nil {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM kv WHERE namespace = ? AND key = ?`, namespace, key)
	return err
}

func (s *Store) List(ctx context.Context, namespace string) (map[string]Entry, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.sweepLocked(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT key, value, expiry, updated_at FROM kv WHERE namespace = ?`, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Entry{}
	for rows.Next() {
		var key string
		var e Entry
		var expiry, updated sql.NullTime
		if err := rows.Scan(&key, &e.Value, &expiry, &updated); err != nil {
			return nil, err
		}
		e.Expiry, e.Updated = expiry.Time, updated.Time
		out[key] = e
	}
	return out, rows.Err()
}

// Sweep deletes all expired entries. Safe to call periodically; List also sweeps.
func (s *Store) Sweep(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sweepLocked(ctx)
}

func (s *Store) sweepLocked(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM kv WHERE expiry IS NOT NULL AND expiry <= ?`, time.Now())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func expired(expiry time.Time) bool {
	return !expiry.IsZero() && !expiry.After(time.Now())
}
