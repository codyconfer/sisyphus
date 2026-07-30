package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codyconfer/sisyphus/internal/duckdb"
)

const seedSchema = `CREATE TABLE IF NOT EXISTS runs(i INTEGER);`

func writeRows(t *testing.T, path string, n int, clean bool) {
	t.Helper()
	ctx := context.Background()
	db, err := duckdb.Open(ctx, path, seedSchema)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	if !clean {
		if _, err := db.ExecContext(ctx, "PRAGMA disable_checkpoint_on_shutdown"); err != nil {
			db.Close()
			t.Fatalf("disabling checkpoint on shutdown: %v", err)
		}
	}
	for i := 0; i < n; i++ {
		if _, err := db.ExecContext(ctx, "INSERT INTO runs VALUES (?)", i); err != nil {
			db.Close()
			t.Fatalf("inserting row %d: %v", i, err)
		}
	}
	if clean {
		if _, err := db.ExecContext(ctx, "CHECKPOINT"); err != nil {
			db.Close()
			t.Fatalf("checkpointing: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("closing %s: %v", path, err)
	}
}

func countRows(t *testing.T, path string) int {
	t.Helper()
	ctx := context.Background()
	db, err := duckdb.Open(ctx, path, "")
	if err != nil {
		t.Fatalf("reopening %s: %v", path, err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM runs").Scan(&n); err != nil {
		t.Fatalf("counting rows in %s: %v", path, err)
	}
	return n
}

func walBytes(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path + ".wal")
	if err != nil {
		return 0
	}
	return st.Size()
}

func sealArchive(t *testing.T, paths ...string) ([]byte, []byte) {
	t.Helper()
	arc, err := Archive(context.Background(), paths)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := Encrypt(arc, key)
	if err != nil {
		t.Fatal(err)
	}
	return sealed, key
}

func TestArchiveCapturesUncheckpointedWAL(t *testing.T) {
	src := t.TempDir()
	path := filepath.Join(src, "x.duckdb")
	writeRows(t, path, 234, false)
	if walBytes(t, path) == 0 {
		t.Skip("duckdb left no write-ahead log; fixture cannot reproduce the bug")
	}

	sealed, key := sealArchive(t, path)

	dst := t.TempDir()
	names, err := Restore(context.Background(), sealed, key, dst)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("restored nothing")
	}
	if got := countRows(t, filepath.Join(dst, "x.duckdb")); got != 234 {
		t.Fatalf("restored copy has %d rows, want 234 (committed rows lived in the WAL at backup time)", got)
	}
}

func TestArchiveCheckpointsBeforeReading(t *testing.T) {
	src := t.TempDir()
	path := filepath.Join(src, "x.duckdb")
	writeRows(t, path, 40, false)

	arc, err := Archive(context.Background(), []string{path})
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	entries, err := Extract(arc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := entries["x.duckdb"]; !ok {
		t.Fatalf("archive entries = %v, want x.duckdb", keysOf(entries))
	}
	dst := filepath.Join(t.TempDir(), "x.duckdb")
	if err := os.WriteFile(dst, entries["x.duckdb"], 0o600); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, dst); got != 40 {
		t.Fatalf("main database file alone has %d rows, want 40", got)
	}
}

func TestRestoreDiscardsStaleWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "y.duckdb")
	writeRows(t, path, 10, true)
	if walBytes(t, path) != 0 {
		t.Fatalf("fixture: %s.wal should be gone after a clean close", path)
	}

	sealed, key := sealArchive(t, path)

	writeRows(t, path, 20, false)
	if walBytes(t, path) == 0 {
		t.Skip("duckdb left no write-ahead log; fixture cannot reproduce the bug")
	}

	if _, err := Restore(context.Background(), sealed, key, dir); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := countRows(t, path); got != 10 {
		t.Fatalf("restored database has %d rows, want 10 (stale post-backup WAL was replayed)", got)
	}
}

func TestRestoreRemovesStaleSidecars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "z.duckdb")
	writeRows(t, path, 3, true)
	sealed, key := sealArchive(t, path)

	if err := os.WriteFile(path+".wal", []byte("stale wal bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "z.duckdb.tmp", "spill"), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := Restore(context.Background(), sealed, key, dir); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(path + ".wal"); !os.IsNotExist(err) {
		t.Errorf("stale %s survived restore (err = %v)", filepath.Base(path)+".wal", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("stale %s survived restore (err = %v)", filepath.Base(path)+".tmp", err)
	}
}

func TestArchiveRefusesOrphanWAL(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "gone.duckdb")
	if err := os.WriteFile(missing+".wal", []byte("orphan wal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(context.Background(), []string{other, missing}); err == nil {
		t.Fatal("archiving a WAL with no database file must fail, not silently succeed")
	}
}

func TestRestoreStagesBeforeReplacing(t *testing.T) {
	dir := t.TempDir()
	src := t.TempDir()
	keep := map[string]string{}
	paths := []string{}
	for _, base := range []string{"k1.duckdb", "k2.duckdb", "k3.duckdb", "k4.duckdb", "k5.duckdb", "k6.duckdb", "k7.duckdb", "k8.duckdb"} {
		p := filepath.Join(src, base)
		if err := os.WriteFile(p, []byte("new "+base), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
		keep[base] = "old " + base
		if err := os.WriteFile(filepath.Join(dir, base), []byte(keep[base]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	blocker := filepath.Join(src, "a-blocked.duckdb")
	if err := os.WriteFile(blocker, []byte("new blocker"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths = append(paths, blocker)
	if err := os.MkdirAll(filepath.Join(dir, "a-blocked.duckdb", "child"), 0o700); err != nil {
		t.Fatal(err)
	}

	sealed, key := sealArchive(t, paths...)

	if _, err := Restore(context.Background(), sealed, key, dir); err == nil {
		t.Fatal("expected restore to fail when a destination cannot be replaced")
	}
	for base, want := range keep {
		got, err := os.ReadFile(filepath.Join(dir, base))
		if err != nil {
			t.Fatalf("reading %s: %v", base, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q: a failed restore must leave the previous state intact", base, got, want)
		}
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() && len(e.Name()) > 9 && e.Name()[:9] == ".restore-" {
			t.Errorf("staging directory %s left behind", e.Name())
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
