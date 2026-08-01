// Package kv is a generic namespaced key/value store in a single DuckDB
// file, with an optional expiry per entry. Expired rows are lazily deleted:
// Get drops one it hits, List and Namespaces sweep before reading, and Sweep
// can be called on its own.
package kv

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/codyconfer/sisyphus/duckopt"
	"github.com/codyconfer/sisyphus/internal/duckdb"
	"github.com/codyconfer/sisyphus/storeerr"
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
// It wraps storeerr.ErrUnavailable, so both errors.Is checks match.
var ErrUnavailable = fmt.Errorf("kv %w", storeerr.ErrUnavailable)

// Entry is one stored value with its expiry (zero means never expires) and
// last-write time.
type Entry struct {
	Value   string
	Expiry  time.Time
	Updated time.Time
}

// Store is a namespaced key/value store with per-entry expiry in one DuckDB
// file.
//
// A nil *Store is a valid no-op: every method returns ErrUnavailable, except
// Close, which returns nil.
type Store struct {
	h *duckdb.Handle
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

// Get returns the entry stored under namespace/key. An entry past its expiry
// is reported as a miss and deleted on the spot (best-effort), so a read is
// also a write when it lands on an expired row.
func (s *Store) Get(ctx context.Context, namespace, key string) (Entry, bool, error) {
	if s == nil || s.h == nil {
		return Entry{}, false, ErrUnavailable
	}
	var e Entry
	var found bool
	err := s.h.Do(ctx, func(db *sql.DB) error {
		e, found = Entry{}, false
		var expiry, updated sql.NullTime
		var got Entry
		err := db.QueryRowContext(ctx,
			`SELECT value, expiry, updated_at FROM kv WHERE namespace = ? AND key = ?`, namespace, key,
		).Scan(&got.Value, &expiry, &updated)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		got.Expiry, got.Updated = expiry.Time, updated.Time
		if expired(got.Expiry) {
			_, _ = db.ExecContext(ctx, `DELETE FROM kv WHERE namespace = ? AND key = ?`, namespace, key)
			return nil
		}
		e, found = got, true
		return nil
	})
	if err != nil {
		return Entry{}, false, err
	}
	return e, found, nil
}

// Put stores value under namespace/key, replacing any existing entry. A zero
// expiry means the entry never expires.
func (s *Store) Put(ctx context.Context, namespace, key, value string, expiry time.Time) error {
	if s == nil || s.h == nil {
		return ErrUnavailable
	}
	return s.h.Do(ctx, func(db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
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
	})
}

// Delete removes the entry under namespace/key. Deleting a key that does not
// exist is not an error.
func (s *Store) Delete(ctx context.Context, namespace, key string) error {
	if s == nil || s.h == nil {
		return ErrUnavailable
	}
	return s.h.Do(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM kv WHERE namespace = ? AND key = ?`, namespace, key)
		return err
	})
}

// List returns every live entry in namespace, keyed by key. It sweeps
// expired rows (store-wide) first, so listing is also a write.
func (s *Store) List(ctx context.Context, namespace string) (map[string]Entry, error) {
	if s == nil || s.h == nil {
		return nil, ErrUnavailable
	}
	var out map[string]Entry
	err := s.h.Do(ctx, func(db *sql.DB) error {
		if _, err := sweepOn(ctx, db); err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, `SELECT key, value, expiry, updated_at FROM kv WHERE namespace = ?`, namespace)
		if err != nil {
			return err
		}
		defer rows.Close()
		found := map[string]Entry{}
		for rows.Next() {
			var key string
			var e Entry
			var expiry, updated sql.NullTime
			if err := rows.Scan(&key, &e.Value, &expiry, &updated); err != nil {
				return err
			}
			e.Expiry, e.Updated = expiry.Time, updated.Time
			found[key] = e
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

// Namespaces lists the namespaces that currently hold entries, expired ones swept first.
func (s *Store) Namespaces(ctx context.Context) ([]string, error) {
	if s == nil || s.h == nil {
		return nil, ErrUnavailable
	}
	var out []string
	err := s.h.Do(ctx, func(db *sql.DB) error {
		if _, err := sweepOn(ctx, db); err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, `SELECT DISTINCT namespace FROM kv ORDER BY namespace`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var found []string
		for rows.Next() {
			var ns string
			if err := rows.Scan(&ns); err != nil {
				return err
			}
			found = append(found, ns)
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

// NamespaceStat summarizes one namespace.
type NamespaceStat struct {
	Namespace string
	// Entries counts live rows: those with no expiry, or an expiry still ahead.
	Entries int64
	// Expired counts rows past their expiry that are still on disk. They are
	// excluded from Entries, Fresh, Oldest and Newest, and disappear on the next
	// Sweep, List or Namespaces call.
	Expired int64
	// Fresh counts live rows updated within the window passed to Stats. It is 0
	// when that window is not positive.
	Fresh int64
	// Oldest and Newest bracket updated_at across live rows, zero when there are
	// none.
	Oldest time.Time
	Newest time.Time
}

// Stats summarizes every namespace holding rows, ordered by name, in one
// read-only aggregate. Unlike List and Namespaces it neither sweeps nor writes,
// so a stats panel costs a single handle acquisition however many namespaces
// exist. fresh, when positive, is the window that NamespaceStat.Fresh counts
// against updated_at; pass 0 to skip it.
func (s *Store) Stats(ctx context.Context, fresh time.Duration) ([]NamespaceStat, error) {
	if s == nil || s.h == nil {
		return nil, ErrUnavailable
	}
	var out []NamespaceStat
	err := s.h.Do(ctx, func(db *sql.DB) error {
		now := time.Now()
		cutoff := now.Add(-fresh)
		rows, err := db.QueryContext(ctx, `
SELECT namespace,
       count(*) FILTER (WHERE live)                     AS entries,
       count(*) FILTER (WHERE NOT live)                 AS expired,
       count(*) FILTER (WHERE live AND updated_at >= ?) AS fresh,
       min(updated_at) FILTER (WHERE live)              AS oldest,
       max(updated_at) FILTER (WHERE live)              AS newest
FROM (SELECT namespace, updated_at, (expiry IS NULL OR expiry > ?) AS live FROM kv)
GROUP BY namespace
ORDER BY namespace`, cutoff, now)
		if err != nil {
			return err
		}
		defer rows.Close()
		var found []NamespaceStat
		for rows.Next() {
			var st NamespaceStat
			var oldest, newest sql.NullTime
			if err := rows.Scan(&st.Namespace, &st.Entries, &st.Expired, &st.Fresh, &oldest, &newest); err != nil {
				return err
			}
			st.Oldest, st.Newest = oldest.Time, newest.Time
			if fresh <= 0 {
				st.Fresh = 0
			}
			found = append(found, st)
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

// Clear deletes every entry in a namespace and returns how many rows went.
// An empty namespace is not a no-op: it wipes the whole store.
func (s *Store) Clear(ctx context.Context, namespace string) (int64, error) {
	if s == nil || s.h == nil {
		return 0, ErrUnavailable
	}
	var n int64
	err := s.h.Do(ctx, func(db *sql.DB) error {
		var res sql.Result
		var err error
		if namespace == "" {
			res, err = db.ExecContext(ctx, `DELETE FROM kv`)
		} else {
			res, err = db.ExecContext(ctx, `DELETE FROM kv WHERE namespace = ?`, namespace)
		}
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// Sweep deletes all expired entries. Safe to call periodically; List also sweeps.
func (s *Store) Sweep(ctx context.Context) (int64, error) {
	if s == nil || s.h == nil {
		return 0, ErrUnavailable
	}
	var n int64
	err := s.h.Do(ctx, func(db *sql.DB) error {
		var err error
		n, err = sweepOn(ctx, db)
		return err
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

func sweepOn(ctx context.Context, db *sql.DB) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM kv WHERE expiry IS NOT NULL AND expiry <= ?`, time.Now())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func expired(expiry time.Time) bool {
	return !expiry.IsZero() && !expiry.After(time.Now())
}
