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

func NullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func NullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func NullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}
