package duckdb

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/codyconfer/sisyphus/duckopt"
	"github.com/codyconfer/sisyphus/tabular"
)

// OptionsFrom builds handle Options from the shared store tuning plus the
// calling package's unavailable sentinel.
func OptionsFrom(t duckopt.O, unavailable error) Options {
	return Options{Idle: t.Idle, Timeout: t.Timeout, MaxHold: t.MaxHold, Unavailable: unavailable}
}

// QueryTable runs query and renders every cell as a string.
func QueryTable(ctx context.Context, db *sql.DB, query string, args ...any) (tabular.Result, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return tabular.Result{}, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return tabular.Result{}, err
	}
	var data [][]string
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return tabular.Result{}, err
		}
		row := make([]string, len(cols))
		for i, c := range cells {
			row[i] = cellString(c)
		}
		data = append(data, row)
	}
	if err := rows.Err(); err != nil {
		return tabular.Result{}, err
	}
	return tabular.Result{Columns: cols, Rows: data}, nil
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
