package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"

	_ "github.com/marcboeker/go-duckdb/v2"
)

func Open(ctx context.Context, path, schema string) (*sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	restore := secureUmask()
	db, err := sql.Open("duckdb", path)
	if err != nil {
		restore()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if schema != "" {
		if _, err := db.ExecContext(ctx, schema); err != nil {
			restore()
			db.Close()
			return nil, err
		}
	}
	restore()
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path+".wal", 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		db.Close()
		return nil, err
	}
	return db, nil
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
