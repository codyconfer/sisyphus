//go:build !windows

package duckdb

import (
	"sync"

	"golang.org/x/sys/unix"
)

var umask struct {
	mu   sync.Mutex
	refs int
	old  int
}

// secureUmask narrows the process umask so that a file DuckDB creates while it
// opens a database is private from the moment it exists, and returns a function
// that restores the previous mask.
//
// The umask is process-global, so this is only used where nothing else can do
// the job: the database file itself cannot be pre-created (DuckDB rejects a
// zero-length file as "not a valid DuckDB database file"), so without this its
// mode would be public between DuckDB's open and the chmod that follows. Files
// this package opens itself pass an explicit 0600 to os.OpenFile instead — a
// umask can only clear permission bits, never add them — and DuckDB's WAL,
// which outlives the open, is chmod'd after every operation. Callers must keep
// the window as short as possible: an unrelated goroutine creating a file
// inside it inherits the narrowed mask.
func secureUmask() func() {
	umask.mu.Lock()
	defer umask.mu.Unlock()
	if umask.refs == 0 {
		umask.old = unix.Umask(0o177)
	}
	umask.refs++
	done := false
	return func() {
		umask.mu.Lock()
		defer umask.mu.Unlock()
		if done || umask.refs == 0 {
			return
		}
		done = true
		umask.refs--
		if umask.refs == 0 {
			unix.Umask(umask.old)
		}
	}
}
