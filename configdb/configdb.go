package configdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
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

func Hash(format string, content []byte) string {
	h := sha256.New()
	h.Write([]byte(format))
	h.Write([]byte{0})
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Store) Current(ctx context.Context, name string) (Version, bool, error) {
	if s == nil || s.h == nil {
		return Version{}, false, nil
	}
	var v Version
	var found bool
	err := s.h.Do(ctx, func(db *sql.DB) error {
		var err error
		v, found, err = currentOn(ctx, db, name)
		return err
	})
	if err != nil {
		return Version{}, false, err
	}
	return v, found, nil
}

func currentOn(ctx context.Context, db *sql.DB, name string) (Version, bool, error) {
	v := Version{Name: name}
	err := db.QueryRowContext(ctx,
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
	if s == nil || s.h == nil {
		return ErrUnavailable
	}
	hash := Hash(format, content)
	err := s.h.Do(ctx, func(db *sql.DB) error {
		cur, hasCur, err := currentOn(ctx, db, name)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
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
			name, hash, format, string(content), time.Now(),
		); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return err
	}
	return s.bump(name + ":" + hash)
}

func (s *Store) Forget(ctx context.Context, name string) error {
	if s == nil || s.h == nil {
		return ErrUnavailable
	}
	err := s.h.Do(ctx, func(db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `DELETE FROM store_current WHERE name = ?`, name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM store_history WHERE name = ?`, name); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return err
	}
	return s.bump("forget:" + name)
}

func (s *Store) History(ctx context.Context, name string, limit int) ([]Version, error) {
	if s == nil || s.h == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	var out []Version
	err := s.h.Do(ctx, func(db *sql.DB) error {
		rows, err := db.QueryContext(ctx,
			`SELECT hash, format, content, archived_at FROM store_history
			 WHERE name = ? ORDER BY archived_at DESC LIMIT ?`, name, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		out = nil
		for rows.Next() {
			v := Version{Name: name}
			if err := rows.Scan(&v.Hash, &v.Format, &v.Content, &v.At); err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
