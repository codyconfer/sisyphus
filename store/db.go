package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/codyconfer/sisyphus/internal/duckdb"
)

// ErrUnavailable is returned when a method is called on a nil or closed DB.
var ErrUnavailable = errors.New("store unavailable")

// DB is a plugin-owned DuckDB file opened with an optional schema. Paths should
// be included in the app backup set.
type DB struct {
	h *duckdb.Handle
}

// Open opens (or creates) a DuckDB database at path, applying schema when
// non-empty. The file is chmod 0600 and limited to a single writer.
type Option func(*duckdb.Options)

func WithIdle(d time.Duration) Option { return func(o *duckdb.Options) { o.Idle = d } }

func WithTimeout(d time.Duration) Option { return func(o *duckdb.Options) { o.Timeout = d } }

func WithMaxHold(d time.Duration) Option { return func(o *duckdb.Options) { o.MaxHold = d } }

func Open(ctx context.Context, path, schema string, opts ...Option) (*DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o := duckdb.Options{Unavailable: ErrUnavailable}
	for _, fn := range opts {
		fn(&o)
	}
	h := duckdb.NewHandle(path, schema, o)
	if err := h.Ensure(ctx); err != nil {
		return nil, err
	}
	RegisterBackupPath(path)
	return &DB{h: h}, nil
}

// Path returns the on-disk database path for backup inclusion.
func (d *DB) Path() string {
	if d == nil {
		return ""
	}
	return d.h.Path()
}

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
		res, err = queryDB(ctx, db, query, args...)
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
