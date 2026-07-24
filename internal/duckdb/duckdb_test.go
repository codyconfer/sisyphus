package duckdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenSetsPrivatePerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.duckdb")
	db, err := Open(context.Background(), path, "CREATE TABLE t (id INTEGER)")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
}
