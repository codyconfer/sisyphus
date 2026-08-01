// Package configdb is a versioned, name-keyed config-document store in a
// single DuckDB file: one current snapshot per name (store_current) plus its
// archived predecessors (store_history). It is the source of truth the root
// sisyphus.ConfigStore reconciles files against, and it maintains a sidecar
// generation marker so pollers can detect writes without opening the
// database.
package configdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/codyconfer/sisyphus/config"
	"github.com/codyconfer/sisyphus/duckopt"
	"github.com/codyconfer/sisyphus/internal/duckdb"
	"github.com/codyconfer/sisyphus/storeerr"
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
// It is the shared storeerr.ErrUnavailable, so both errors.Is checks match.
var ErrUnavailable = storeerr.ErrUnavailable

// Snapshot is one stored content snapshot of a named config document.
type Snapshot struct {
	Name    string
	Hash    string
	Format  config.Format
	Content string
	At      time.Time
}

// Store is a versioned config-document store in one DuckDB file.
//
// A nil *Store is a valid no-op: reads report absent with a nil error, writes
// return ErrUnavailable, and Close returns nil.
type Store struct {
	h     *duckdb.Handle
	revMu sync.Mutex
}

// Open opens (or creates) the store's DuckDB file at path and ensures its
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

// Hash returns the snapshot hash for content in format: a hex SHA-256 over
// format and content together, so the same bytes in a different format hash
// differently. It is the value Import stores in Snapshot.Hash.
func Hash(format config.Format, content []byte) string {
	h := sha256.New()
	h.Write([]byte(format))
	h.Write([]byte{0})
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// Current returns the current snapshot of the named document. On a nil or
// closed store it reports absent with a nil error.
func (s *Store) Current(ctx context.Context, name string) (Snapshot, bool, error) {
	if s == nil || s.h == nil {
		return Snapshot{}, false, nil
	}
	var v Snapshot
	var found bool
	err := s.h.Do(ctx, func(db *sql.DB) error {
		var err error
		v, found, err = currentOn(ctx, db, name)
		return err
	})
	if err != nil {
		return Snapshot{}, false, err
	}
	return v, found, nil
}

func currentOn(ctx context.Context, db *sql.DB, name string) (Snapshot, bool, error) {
	v := Snapshot{Name: name}
	err := db.QueryRowContext(ctx,
		`SELECT hash, format, content, applied_at FROM store_current WHERE name = ?`, name,
	).Scan(&v.Hash, &v.Format, &v.Content, &v.At)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	return v, true, nil
}

// Import makes content the named document's current snapshot, archiving the
// previous one (if any) into history in the same transaction, then bumps the
// generation marker. A marker-write failure after commit is reported wrapped
// in ErrGenerationMarker: the data change itself has already stuck.
func (s *Store) Import(ctx context.Context, name string, content []byte, format config.Format) error {
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
				cur.Name, cur.Hash, string(cur.Format), cur.Content, time.Now(),
			); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM store_current WHERE name = ?`, name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO store_current (name, hash, format, content, applied_at) VALUES (?, ?, ?, ?, ?)`,
			name, hash, string(format), string(content), time.Now(),
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

// Forget deletes the named document's current snapshot and its entire
// history in one transaction, then bumps the generation marker.
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

// History returns up to limit archived snapshots of the named document,
// newest first. A limit <= 0 means 50. On a nil or closed store it returns
// nil with a nil error.
func (s *Store) History(ctx context.Context, name string, limit int) ([]Snapshot, error) {
	if s == nil || s.h == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	var out []Snapshot
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
			v := Snapshot{Name: name}
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
