package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/codyconfer/sisyphus/internal/duckdb"
)

// ErrUnavailable is returned when a method is called on a nil or closed DB.
var ErrUnavailable = errors.New("store unavailable")

// DB is a plugin-owned DuckDB file opened with an optional schema. Paths should
// be included in the app backup set.
type DB struct {
	mu   sync.Mutex
	db   *sql.DB
	path string
}

// Open opens (or creates) a DuckDB database at path, applying schema when
// non-empty. The file is chmod 0600 and limited to a single writer.
func Open(ctx context.Context, path, schema string) (*DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := duckdb.Open(ctx, path, schema)
	if err != nil {
		return nil, err
	}
	RegisterBackupPath(path)
	return &DB{db: db, path: path}, nil
}

// Path returns the on-disk database path for backup inclusion.
func (d *DB) Path() string {
	if d == nil {
		return ""
	}
	return d.path
}

func (d *DB) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.db == nil {
		return nil
	}
	err := d.db.Close()
	d.db = nil
	return err
}

// Query runs a SQL statement and returns a string table.
func (d *DB) Query(ctx context.Context, query string, args ...any) (Result, error) {
	if d == nil || d.db == nil {
		return Result{}, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return queryDB(ctx, d.db, query, args...)
}

// Exec runs a non-returning SQL statement.
func (d *DB) Exec(ctx context.Context, query string, args ...any) error {
	if d == nil || d.db == nil {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.ExecContext(ctx, query, args...)
	return err
}
