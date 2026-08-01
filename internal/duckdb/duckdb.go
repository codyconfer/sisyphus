// Package duckdb is the shared DuckDB plumbing under every sisyphus store:
// opening files owner-only (database and WAL alike), the Handle that
// coordinates single-writer access across processes via sidecar lock files,
// and small SQL helpers (NULL adapters, QueryTable).
package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"

	_ "github.com/marcboeker/go-duckdb/v2"
)

// walSuffix names the write-ahead log DuckDB keeps beside a database file.
// DuckDB creates, unlinks and recreates it on its own — it is never opened by
// this package — so its mode has to be corrected after the fact.
const walSuffix = ".wal"

// Open opens (or creates) the DuckDB file at path with a single connection,
// applies schema when non-empty, and narrows the database and its WAL to
// owner-only permissions. Lock errors are tagged so errors.Is(err, ErrLocked)
// matches. Most callers should go through a Handle rather than call this.
func Open(ctx context.Context, path, schema string) (*sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	restore := secureUmask()
	db, err := sql.Open("duckdb", path)
	if err != nil {
		restore()
		return nil, asLocked(err)
	}
	db.SetMaxOpenConns(1)
	if schema != "" {
		if _, err := db.ExecContext(ctx, schema); err != nil {
			restore()
			db.Close()
			return nil, asLocked(err)
		}
	}
	restore()
	if err := securePerm(path); err != nil {
		db.Close()
		return nil, err
	}
	if err := secureWAL(path); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// securePerm narrows path to owner-only. A file DuckDB has already created can
// only be fixed after the fact, so a missing file is not an error.
func securePerm(path string) error {
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// secureWAL narrows the write-ahead log beside path.
//
// The WAL holds unflushed rows — secrets included — and DuckDB creates it with
// whatever the ambient umask allows. It appears on the first write after an
// open, not at open time, and every CHECKPOINT unlinks it so the next write
// creates a fresh file: securing it once is not enough, which is why Handle.Do
// repeats this after every operation.
func secureWAL(path string) error {
	return securePerm(path + walSuffix)
}

// NullTime adapts t for a nullable TIMESTAMP argument: SQL NULL for the zero
// time, t itself otherwise.
func NullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// NullInt adapts v for a nullable BIGINT argument: SQL NULL for 0, v
// otherwise.
func NullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// NullStr adapts v for a nullable VARCHAR argument: SQL NULL for the empty
// string, v otherwise.
func NullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}
