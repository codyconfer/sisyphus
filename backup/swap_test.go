package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codyconfer/sisyphus/internal/duckdb"

	"github.com/codyconfer/sisyphus/internal/crypt"
)

var errInjected = errors.New("injected rename failure")

func failFirstRenameInto(t *testing.T, dir, base string) *bool {
	t.Helper()
	prev := renameFile
	fired := false
	renameFile = func(from, to string) error {
		if !fired && filepath.Dir(to) == dir && filepath.Base(to) == base {
			fired = true
			return errInjected
		}
		return prev(from, to)
	}
	t.Cleanup(func() { renameFile = prev })
	return &fired
}

func failEveryRenameInto(t *testing.T, dir string) {
	t.Helper()
	prev := renameFile
	renameFile = func(from, to string) error {
		if filepath.Dir(to) == dir {
			return errInjected
		}
		return prev(from, to)
	}
	t.Cleanup(func() { renameFile = prev })
}

func TestRestoreRollsBackAFailedRenameAtEveryPosition(t *testing.T) {
	for _, pos := range blockerPositions {
		t.Run(pos.where, func(t *testing.T) {
			dir, src := t.TempDir(), t.TempDir()
			sealed, key, want := seedRestoreFixture(t, dir, src, pos.base)
			want[pos.base] = "old " + pos.base
			if err := os.WriteFile(filepath.Join(dir, pos.base), []byte(want[pos.base]), 0o600); err != nil {
				t.Fatal(err)
			}
			fired := failFirstRenameInto(t, dir, pos.base)

			_, err := Restore(context.Background(), sealed, key, dir)
			if err == nil {
				t.Fatal("expected restore to fail when a rename into the destination fails")
			}
			if !*fired {
				t.Fatal("fixture: the injected rename never ran")
			}
			if !errors.Is(err, errInjected) {
				t.Errorf("error = %v, want it to wrap the injected failure", err)
			}
			assertUnchanged(t, dir, want)
			assertNoRestoreDirs(t, dir)
		})
	}
}

func TestRestoreRollbackReturnsTheWALWithItsDatabase(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "plugin.db")
	if err := os.WriteFile(path, []byte("old main file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".wal", []byte("unreplayed committed rows"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"plugin.db", "zz.duckdb"} {
		if err := os.WriteFile(filepath.Join(src, base), []byte("new "+base), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "zz.duckdb"), []byte("old zz.duckdb"), 0o600); err != nil {
		t.Fatal(err)
	}
	sealed, key := sealArchive(t, filepath.Join(src, "plugin.db"), filepath.Join(src, "zz.duckdb"))
	fired := failFirstRenameInto(t, dir, "zz.duckdb")

	if _, err := Restore(context.Background(), sealed, key, dir); err == nil {
		t.Fatal("expected restore to fail")
	}
	if !*fired {
		t.Fatal("fixture: the injected rename never ran")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "old main file" {
		t.Errorf("plugin.db = %q, %v; want the previous file back", got, err)
	}
	got, err := os.ReadFile(path + ".wal")
	if err != nil {
		t.Fatalf("plugin.db.wal did not survive the failed restore: %v", err)
	}
	if string(got) != "unreplayed committed rows" {
		t.Errorf("plugin.db.wal = %q, want its previous contents", got)
	}
	assertNoRestoreDirs(t, dir)
}

func TestRestoreReportsAnUnrecoverableRollback(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	sealed, key, want := seedRestoreFixture(t, dir, src, "zz-blocked.duckdb")
	failEveryRenameInto(t, dir)

	_, err := Restore(context.Background(), sealed, key, dir)
	if err == nil {
		t.Fatal("expected restore to fail")
	}
	ents, rerr := os.ReadDir(dir)
	if rerr != nil {
		t.Fatal(rerr)
	}
	aside := ""
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), asidePrefix) {
			aside = filepath.Join(dir, e.Name())
		}
	}
	if aside == "" {
		t.Fatal("a rollback that failed must leave the previous files behind, not delete them")
	}
	if !strings.Contains(err.Error(), aside) {
		t.Errorf("error = %v, want it to name %s so the user can recover by hand", err, aside)
	}
	for base, w := range want {
		got, rferr := os.ReadFile(filepath.Join(aside, base))
		if rferr != nil {
			t.Errorf("previous %s is not recoverable: %v", base, rferr)
			continue
		}
		if string(got) != w {
			t.Errorf("%s in the rollback area = %q, want %q", base, got, w)
		}
	}
}

func tarOf(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, data := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Now()}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRestorePlacesAWALEntryWithItsDatabase(t *testing.T) {
	src, dir := t.TempDir(), t.TempDir()
	path := filepath.Join(src, "h.duckdb")
	writeRows(t, path, 12, false)
	if walBytes(t, path) == 0 {
		t.Skip("duckdb left no write-ahead log; fixture cannot reproduce the pairing")
	}
	main, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wal, err := os.ReadFile(path + ".wal")
	if err != nil {
		t.Fatal(err)
	}
	key, err := crypt.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := crypt.Encrypt(tarOf(t, map[string][]byte{"h.duckdb": main, "h.duckdb.wal": wal}), key)
	if err != nil {
		t.Fatal(err)
	}

	names, err := Restore(context.Background(), sealed, key, dir)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("restored %v, want both the database and its write-ahead log", names)
	}
	if got := countRows(t, filepath.Join(dir, "h.duckdb")); got != 12 {
		t.Fatalf("restored database has %d rows, want 12", got)
	}
}

func TestIsDatabaseIgnoresTheFileName(t *testing.T) {
	dir := t.TempDir()
	realDB := filepath.Join(dir, "plugin.db")
	writeRows(t, realDB, 3, true)
	notDB := filepath.Join(dir, "notes.duckdb")
	if err := os.WriteFile(notDB, []byte("plain text, definitely not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.duckdb")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	walOnly := filepath.Join(dir, "wal-only.bin")
	if err := os.WriteFile(walOnly, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(walOnly+".wal", []byte("log"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		path string
		want bool
	}{
		{realDB, true},
		{notDB, false},
		{empty, false},
		{walOnly, true},
		{filepath.Join(dir, "missing.duckdb"), false},
	} {
		got, err := isDatabase(tc.path)
		if err != nil {
			t.Errorf("isDatabase(%s): %v", filepath.Base(tc.path), err)
			continue
		}
		if got != tc.want {
			t.Errorf("isDatabase(%s) = %v, want %v", filepath.Base(tc.path), got, tc.want)
		}
	}
}

func TestHeldClassifiesWithoutMatchingText(t *testing.T) {
	if held(nil) {
		t.Error("held(nil) = true")
	}
	if held(errors.New("permission denied")) {
		t.Error("a permission error must not be reported as contention")
	}
	if !held(duckdb.ErrClosed) {
		t.Error("held(ErrClosed) = false")
	}
	if !held(duckdb.ErrLocked) {
		t.Error("held(ErrLocked) = false")
	}
}
