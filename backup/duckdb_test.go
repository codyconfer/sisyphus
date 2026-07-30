package backup

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

func TestArchiveBacksUpRowsStillInTheWAL(t *testing.T) {
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
		t.Fatalf("restored copy has %d rows, want 234: Archive must CHECKPOINT the source so rows "+
			"committed only to its write-ahead log are in the file it copies", got)
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

var restoreKeepers = []string{"k1.duckdb", "k2.duckdb", "k4.duckdb", "k5.duckdb", "k6.duckdb"}

var blockerPositions = []struct {
	where string
	base  string
	at    int
}{
	{"first", "aa-blocked.duckdb", 0},
	{"middle", "k3-blocked.duckdb", 2},
	{"last", "zz-blocked.duckdb", 5},
}

func seedRestoreFixture(t *testing.T, dir, src, blocker string) ([]byte, []byte, map[string]string) {
	t.Helper()
	want := map[string]string{}
	paths := []string{}
	for _, base := range append(append([]string{}, restoreKeepers...), blocker) {
		p := filepath.Join(src, base)
		if err := os.WriteFile(p, []byte("new "+base), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	for _, base := range restoreKeepers {
		want[base] = "old " + base
		if err := os.WriteFile(filepath.Join(dir, base), []byte(want[base]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sealed, key := sealArchive(t, paths...)
	return sealed, key, want
}

func assertUnchanged(t *testing.T, dir string, want map[string]string) {
	t.Helper()
	for base, w := range want {
		got, err := os.ReadFile(filepath.Join(dir, base))
		if err != nil {
			t.Fatalf("reading %s: %v", base, err)
		}
		if string(got) != w {
			t.Errorf("%s = %q, want %q: a failed restore must leave the previous state intact", base, got, w)
		}
	}
}

func assertNoRestoreDirs(t *testing.T, dir string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".restore-") {
			t.Errorf("temporary directory %s left behind", e.Name())
		}
	}
}

func TestRestoreFailureLeavesEveryDestinationIntact(t *testing.T) {
	for _, pos := range blockerPositions {
		t.Run(pos.where, func(t *testing.T) {
			dir, src := t.TempDir(), t.TempDir()
			sealed, key, want := seedRestoreFixture(t, dir, src, pos.base)
			if err := os.MkdirAll(filepath.Join(dir, pos.base, "child"), 0o700); err != nil {
				t.Fatal(err)
			}

			order := append(append([]string{}, restoreKeepers...), pos.base)
			sort.Strings(order)
			if order[pos.at] != pos.base {
				t.Fatalf("fixture: blocker %s is at position %d of %v, not %d",
					pos.base, indexOf(order, pos.base), order, pos.at)
			}

			if _, err := Restore(context.Background(), sealed, key, dir); err == nil {
				t.Fatal("expected restore to fail when a destination cannot be replaced")
			}
			assertUnchanged(t, dir, want)
			if _, err := os.Stat(filepath.Join(dir, pos.base, "child")); err != nil {
				t.Errorf("%s/child did not survive the failed restore: %v", pos.base, err)
			}
			assertNoRestoreDirs(t, dir)
		})
	}
}

func indexOf(all []string, want string) int {
	for i, s := range all {
		if s == want {
			return i
		}
	}
	return -1
}

func TestRestoreFailureKeepsRowsHeldOnlyInTheWAL(t *testing.T) {
	for _, base := range []string{"keep.duckdb", "plugin.db"} {
		t.Run(base, func(t *testing.T) {
			dir, src := t.TempDir(), t.TempDir()
			path := filepath.Join(dir, base)
			writeRows(t, path, 400, false)
			if walBytes(t, path) == 0 {
				t.Skip("duckdb left no write-ahead log; fixture cannot reproduce the bug")
			}

			replacement := filepath.Join(src, base)
			writeRows(t, replacement, 7, true)
			blocker := filepath.Join(src, "zz-blocked.duckdb")
			if err := os.WriteFile(blocker, []byte("new blocker"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(dir, "zz-blocked.duckdb", "child"), 0o700); err != nil {
				t.Fatal(err)
			}

			sealed, key := sealArchive(t, replacement, blocker)
			if _, err := Restore(context.Background(), sealed, key, dir); err == nil {
				t.Fatal("expected restore to fail when a destination cannot be replaced")
			}
			if got := countRows(t, path); got != 400 {
				t.Fatalf("%s has %d rows after a failed restore, want 400: the committed rows in "+
					"%s.wal must be replayed or set aside, never deleted", base, got, base)
			}
			assertNoRestoreDirs(t, dir)
		})
	}
}

func TestRestoreRefusesWhenALockSidecarIsUnusable(t *testing.T) {
	for _, base := range []string{"v.duckdb", "plugin.db"} {
		t.Run(base, func(t *testing.T) {
			dir, src := t.TempDir(), t.TempDir()
			path := filepath.Join(dir, base)
			writeRows(t, path, 400, false)
			if walBytes(t, path) == 0 {
				t.Skip("duckdb left no write-ahead log; fixture cannot reproduce the bug")
			}
			if err := os.MkdirAll(path+".wait", 0o700); err != nil {
				t.Fatal(err)
			}

			replacement := filepath.Join(src, base)
			writeRows(t, replacement, 7, true)
			sealed, key := sealArchive(t, replacement)

			_, err := Restore(context.Background(), sealed, key, dir)
			if err == nil {
				t.Fatalf("restore replaced %s although %s.wait is a directory, so no process holding "+
					"the database could have been detected", base, base)
			}
			if !strings.Contains(err.Error(), base+".wait") {
				t.Errorf("error = %v, want it to name %s.wait", err, base)
			}
			if got := countRows(t, path); got != 400 {
				t.Fatalf("%s has %d rows after a refused restore, want 400", base, got)
			}
			assertNoRestoreDirs(t, dir)
		})
	}
}

func TestArchiveRefusesADatabaseItCannotCheckpoint(t *testing.T) {
	for _, tc := range []struct {
		name  string
		clean bool
	}{{"with-wal", false}, {"without-wal", true}} {
		t.Run(tc.name, func(t *testing.T) {
			if os.Geteuid() == 0 {
				t.Skip("root ignores directory permissions")
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "ro.duckdb")
			writeRows(t, path, 40, tc.clean)
			if tc.clean && walBytes(t, path) != 0 {
				t.Skip("fixture left a write-ahead log behind a clean close")
			}
			if !tc.clean && walBytes(t, path) == 0 {
				t.Skip("duckdb left no write-ahead log; fixture cannot reproduce the bug")
			}
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

			_, err := Archive(context.Background(), []string{path})
			if err == nil {
				t.Fatal("Archive copied a live database raw instead of refusing: a database that " +
					"cannot be checkpointed must not be downgraded to a plain read")
			}
			if !strings.Contains(err.Error(), "write access") {
				t.Errorf("error = %v, want it to say a backup needs write access to the database", err)
			}
			if strings.Contains(err.Error(), "stop other munin processes") {
				t.Errorf("error = %v, must not blame a competing process for a permission failure", err)
			}
		})
	}
}

func TestArchiveTreatsAnyDatabaseHeaderAsADatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.db")
	writeRows(t, path, 400, true)
	if walBytes(t, path) != 0 {
		t.Skip("fixture left a write-ahead log, which the name gate already reacts to")
	}

	arc, err := Archive(context.Background(), []string{path})
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, statErr := os.Stat(path + ".lock"); statErr != nil {
		t.Fatalf("plugin.db was copied as a plain file: no database lock was taken (%v); a database "+
			"must be recognised by its header, not by a .duckdb name", statErr)
	}
	entries, err := Extract(arc)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "plugin.db")
	if err := os.WriteFile(dst, entries["plugin.db"], 0o600); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, dst); got != 400 {
		t.Fatalf("archived copy of plugin.db has %d rows, want 400", got)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
