package kv

import (
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

var errUnavailable = errors.New("kv store unavailable")

type Entry struct {
	Value   string
	Expiry  time.Time
	Updated time.Time
}

type Store struct {
	mu sync.Mutex
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := duckdb.Open(path, schema)
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

func (s *Store) Get(namespace, key string) (Entry, bool, error) {
	if s == nil || s.db == nil {
		return Entry{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var e Entry
	var expiry, updated sql.NullTime
	err := s.db.QueryRow(
		`SELECT value, expiry, updated_at FROM kv WHERE namespace = ? AND key = ?`, namespace, key,
	).Scan(&e.Value, &expiry, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	e.Expiry, e.Updated = expiry.Time, updated.Time
	return e, true, nil
}

func (s *Store) Put(namespace, key, value string, expiry time.Time) error {
	if s == nil || s.db == nil {
		return errUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM kv WHERE namespace = ? AND key = ?`, namespace, key); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO kv (namespace, key, value, expiry, updated_at) VALUES (?, ?, ?, ?, ?)`,
		namespace, key, value, duckdb.NullTime(expiry), time.Now(),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Delete(namespace, key string) error {
	if s == nil || s.db == nil {
		return errUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM kv WHERE namespace = ? AND key = ?`, namespace, key)
	return err
}

func (s *Store) List(namespace string) (map[string]Entry, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT key, value, expiry, updated_at FROM kv WHERE namespace = ?`, namespace)
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
