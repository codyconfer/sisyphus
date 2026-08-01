// Package duckfile owns plugin-scoped DuckDB files: each plugin opens its own
// database with its own schema and queries it through string tables.
package duckfile

import (
	"context"
	"database/sql"

	"github.com/codyconfer/sisyphus/duckopt"
	"github.com/codyconfer/sisyphus/internal/duckdb"
	"github.com/codyconfer/sisyphus/storeerr"
	"github.com/codyconfer/sisyphus/tabular"
)

// ErrUnavailable is returned when a method is called on a nil or closed DB.
// It is the shared storeerr.ErrUnavailable, so both errors.Is checks match.
var ErrUnavailable = storeerr.ErrUnavailable

// Result is a tabular ad-hoc query response.
type Result = tabular.Result

// DB is a plugin-owned DuckDB file opened with an optional schema. Paths
// should be included in the app backup set: call RegisterBackupPath for every
// database the app must back up — Open does not register anything itself.
//
// A nil *DB is a valid no-op: Path reports "", Query and Exec return
// ErrUnavailable, and Close returns nil.
type DB struct {
	h *duckdb.Handle
}

// Open opens (or creates) a DuckDB database at path, applying schema when
// non-empty. The file is chmod 0600 and limited to a single writer.
func Open(ctx context.Context, path, schema string, opts ...duckopt.Option) (*DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h := duckdb.NewHandle(path, schema, duckdb.OptionsFrom(duckopt.Build(opts...), ErrUnavailable))
	if err := h.Ensure(ctx); err != nil {
		return nil, err
	}
	return &DB{h: h}, nil
}

// Path returns the on-disk database path for backup inclusion.
func (d *DB) Path() string {
	if d == nil {
		return ""
	}
	return d.h.Path()
}

// Close releases the database. It is safe on a nil *DB and returns nil.
// Closing is terminal: further Query and Exec calls return ErrUnavailable.
func (d *DB) Close() error {
	if d == nil {
		return nil
	}
	return d.h.Close()
}

// Query runs a SQL statement and returns a string table.
func (d *DB) Query(ctx context.Context, query string, args ...any) (Result, error) {
	if d == nil || d.h == nil {
		return Result{}, ErrUnavailable
	}
	var res Result
	err := d.h.Do(ctx, func(db *sql.DB) error {
		var err error
		res, err = duckdb.QueryTable(ctx, db, query, args...)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// Exec runs a non-returning SQL statement.
func (d *DB) Exec(ctx context.Context, query string, args ...any) error {
	if d == nil || d.h == nil {
		return ErrUnavailable
	}
	return d.h.Do(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, query, args...)
		return err
	})
}
