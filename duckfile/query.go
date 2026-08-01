package duckfile

import (
	"context"
	"database/sql"

	"github.com/codyconfer/sisyphus/internal/duckdb"
)

// Query opens path read/write (DuckDB requirement) without applying a schema
// and runs query, returning a string table. For long-lived plugin stores prefer
// Open(ctx, path, schema). Callers should restrict statements to read-only
// forms at the application layer.
//
// The database is taken through a short-lived handle, so an ad-hoc query queues
// behind whichever process currently holds the file and asks it to hand over,
// rather than failing on DuckDB's own lock. A handle held by this process is
// shared instead of waited on, so a query never blocks on itself.
func Query(ctx context.Context, path, query string, args ...any) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	h := duckdb.NewHandle(path, "", duckdb.Options{Unavailable: ErrUnavailable})
	defer h.Close()
	var res Result
	err := h.Do(ctx, func(db *sql.DB) error {
		var err error
		res, err = duckdb.QueryTable(ctx, db, query, args...)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}
