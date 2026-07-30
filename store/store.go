package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/codyconfer/sisyphus/internal/duckdb"
)

// Result is a tabular ad-hoc query response.
type Result struct {
	Columns []string
	Rows    [][]string
}

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
		res, err = queryDB(ctx, db, query, args...)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

func queryDB(ctx context.Context, db *sql.DB, query string, args ...any) (Result, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return Result{}, err
	}
	var data [][]string
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return Result{}, err
		}
		row := make([]string, len(cols))
		for i, c := range cells {
			row[i] = cellString(c)
		}
		data = append(data, row)
	}
	if err := rows.Err(); err != nil {
		return Result{}, err
	}
	return Result{Columns: cols, Rows: data}, nil
}

func cellString(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(t)
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}
