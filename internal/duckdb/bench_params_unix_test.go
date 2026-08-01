//go:build !windows

package duckdb

import "time"

// Enough work to span many handoffs and measure the steady state rather than
// one acquisition. The budget pins the shape, not a benchmark number: hosted
// CI takes roughly three and a half seconds, while the re-queueing regression
// this catches measured seven times slower.
const (
	contendOps    = 320
	contendBudget = 5 * time.Second
)
