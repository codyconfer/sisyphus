// Package tabular holds the string-table result type shared by the sisyphus
// query surfaces (journal.Query, duckfile.Query), plus typed accessors for
// reading cells back out of it.
package tabular

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Result is a tabular ad-hoc query response. Every cell is rendered as a
// string; use Row accessors to read typed values back out.
type Result struct {
	Columns []string
	Rows    [][]string
}

// Row reads one row as a Row of typed accessors. Out-of-range indexes yield a
// nil Row, whose accessors report absence rather than panicking.
func (r Result) Row(i int) Row {
	if i < 0 || i >= len(r.Rows) {
		return nil
	}
	return Row(r.Rows[i])
}

// Row is one result row. Its accessors are bounds-safe: an out-of-range index
// reports absence (empty string, zero value, error) instead of panicking.
type Row []string

// Str returns the cell at i, or "" when i is out of range.
func (r Row) Str(i int) string {
	if i < 0 || i >= len(r) {
		return ""
	}
	return r[i]
}

// Int64 parses the cell at i as a base-10 integer.
func (r Row) Int64(i int) (int64, error) {
	if i < 0 || i >= len(r) {
		return 0, fmt.Errorf("tabular: no column %d in a %d-column row", i, len(r))
	}
	n, err := strconv.ParseInt(strings.TrimSpace(r[i]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("tabular: column %d: %w", i, err)
	}
	return n, nil
}

// Bool reports whether the cell at i holds a truthy value: "true", "t" or "1",
// case-insensitively. Everything else — including NULL and an out-of-range
// index — is false.
func (r Row) Bool(i int) bool {
	switch strings.ToLower(r.Str(i)) {
	case "true", "t", "1":
		return true
	}
	return false
}

// timeLayouts are the spellings DuckDB (and Go's fmt rendering of time.Time)
// produce for timestamp cells, RFC 3339 first.
var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999 -0700",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// Time parses the cell at i as a timestamp, normalized to UTC. It reports
// false for NULL, empty, out-of-range and unparseable cells.
func (r Row) Time(i int) (time.Time, bool) {
	s := strings.TrimSpace(r.Str(i))
	if s == "" || strings.EqualFold(s, "NULL") {
		return time.Time{}, false
	}
	// Strip Go's monotonic-clock suffix (" m=+0.000") if a time.Time was
	// rendered with fmt.
	if i := strings.Index(s, " m="); i > 0 {
		s = s[:i]
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
