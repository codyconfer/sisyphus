//go:build !windows

package duckdb

import "golang.org/x/sys/unix"

func secureUmask() func() {
	old := unix.Umask(0o177)
	return func() { unix.Umask(old) }
}
