//go:build windows

package duckdb

import "time"

// Windows pays the full yield interval on each handoff, so fewer operations
// still exercise many real handoffs without turning the test into a minute-long
// benchmark. The budget leaves runner headroom while catching handoff thrash.
const (
	contendOps    = 32
	contendBudget = 20 * time.Second
)
