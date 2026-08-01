// Package duckopt carries the DuckDB handle tuning shared by every sisyphus
// store package (kv, configdb, journal, duckfile). Options built here are
// accepted by each package's Open.
package duckopt

import "time"

// O is the tuning applied to a DuckDB handle. The zero value means "use the
// package defaults" for every field.
type O struct {
	// Idle is how long to hold the database open after an operation.
	Idle time.Duration
	// Timeout bounds waiting for another process to release the database.
	Timeout time.Duration
	// MaxHold caps how long the database stays open across back-to-back
	// operations, so steady work cannot starve other processes.
	MaxHold time.Duration
}

// Option adjusts one tuning field.
type Option func(*O)

// WithIdle sets how long the database is held open after an operation.
func WithIdle(d time.Duration) Option { return func(o *O) { o.Idle = d } }

// WithTimeout bounds waiting for another process to release the database.
func WithTimeout(d time.Duration) Option { return func(o *O) { o.Timeout = d } }

// WithMaxHold caps how long the database stays open across back-to-back
// operations.
func WithMaxHold(d time.Duration) Option { return func(o *O) { o.MaxHold = d } }

// Build applies opts to a zero O.
func Build(opts ...Option) O {
	var o O
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	return o
}
