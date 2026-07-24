package configdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/codyconfer/sisyphus/internal/duckdb"
)

const schema = `
CREATE SEQUENCE IF NOT EXISTS store_hist_seq;
CREATE TABLE IF NOT EXISTS store_current (
  name       VARCHAR PRIMARY KEY,
  hash       VARCHAR,
  format     VARCHAR,
  content    VARCHAR,
  applied_at TIMESTAMP
);
CREATE TABLE IF NOT EXISTS store_history (
  id          BIGINT PRIMARY KEY DEFAULT nextval('store_hist_seq'),
  name        VARCHAR,
  hash        VARCHAR,
  format      VARCHAR,
  content     VARCHAR,
  archived_at TIMESTAMP
);
`

// ErrUnavailable is returned when a method is called on a nil or closed store.
var ErrUnavailable = errors.New("store unavailable")

type Version struct {
	Name    string
	Hash    string
	Format  string
	Content string
	At      time.Time
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

func Hash(format string, content []byte) string {
	h := sha256.New()
	h.Write([]byte(format))
	h.Write([]byte{0})
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Store) Current(ctx context.Context, name string) (Version, bool, error) {
	if s == nil || s.db == nil {
		return Version{}, false, nil
	}
	if err := ctx.Err(); err != nil {
		return Version{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentLocked(ctx, name)
}

func (s *Store) currentLocked(ctx context.Context, name string) (Version, bool, error) {
	v := Version{Name: name}
	err := s.db.QueryRowContext(ctx,
		`SELECT hash, format, content, applied_at FROM store_current WHERE name = ?`, name,
	).Scan(&v.Hash, &v.Format, &v.Content, &v.At)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, false, nil
	}
	if err != nil {
		return Version{}, false, err
	}
	return v, true, nil
}

func (s *Store) Import(ctx context.Context, name string, content []byte, format string) error {
	if s == nil || s.db == nil {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, hasCur, err := s.currentLocked(ctx, name)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if hasCur {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO store_history (name, hash, format, content, archived_at) VALUES (?, ?, ?, ?, ?)`,
			cur.Name, cur.Hash, cur.Format, cur.Content, time.Now(),
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM store_current WHERE name = ?`, name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO store_current (name, hash, format, content, applied_at) VALUES (?, ?, ?, ?, ?)`,
		name, Hash(format, content), format, string(content), time.Now(),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) History(ctx context.Context, name string, limit int) ([]Version, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx,
		`SELECT hash, format, content, archived_at FROM store_history
		 WHERE name = ? ORDER BY archived_at DESC LIMIT ?`, name, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		v := Version{Name: name}
		if err := rows.Scan(&v.Hash, &v.Format, &v.Content, &v.At); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
