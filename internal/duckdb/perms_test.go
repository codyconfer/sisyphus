//go:build !windows

package duckdb

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// withPermissiveUmask makes the ambient umask public for the duration of a test,
// so that anything relying on the umask to keep a file private fails visibly
// instead of passing because the developer's shell happened to be strict.
func withPermissiveUmask(t *testing.T) {
	t.Helper()
	old := unix.Umask(0o022)
	t.Cleanup(func() { unix.Umask(old) })
}

func modeOf(t *testing.T, path string) (os.FileMode, bool) {
	t.Helper()
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, false
	}
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm(), true
}

func TestHandleSecuresWALCreatedAfterOpen(t *testing.T) {
	withPermissiveUmask(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")

	first := NewHandle(path, testSchema, Options{})
	if err := put(ctx, first, "k", "v"); err != nil {
		t.Fatalf("first put: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, ok := modeOf(t, path+walSuffix); ok {
		t.Fatalf("%s survived a clean close; this test needs a database whose WAL "+
			"is created by a write after open, not at open", filepath.Base(path)+walSuffix)
	}

	second := newTestHandle(t, path)
	if err := put(ctx, second, "k2", "v2"); err != nil {
		t.Fatalf("put on reopened database: %v", err)
	}
	perm, ok := modeOf(t, path+walSuffix)
	if !ok {
		t.Fatal("no WAL after a write; nothing was verified")
	}
	if perm != 0o600 {
		t.Fatalf("WAL perm = %o, want 600: unflushed rows are readable by other users", perm)
	}
	if perm, _ := modeOf(t, path); perm != 0o600 {
		t.Fatalf("database perm = %o, want 600", perm)
	}
}

func TestHandleSecuresWALRecreatedAfterCheckpoint(t *testing.T) {
	withPermissiveUmask(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")
	h := newTestHandle(t, path)

	if err := put(ctx, h, "k", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := h.Do(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, "CHECKPOINT")
		return err
	}); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if _, ok := modeOf(t, path+walSuffix); ok {
		t.Skip("this DuckDB build keeps the WAL across a checkpoint; nothing to re-secure")
	}
	if err := put(ctx, h, "k2", "v2"); err != nil {
		t.Fatalf("put after checkpoint: %v", err)
	}
	perm, ok := modeOf(t, path+walSuffix)
	if !ok {
		t.Fatal("no WAL after a write; nothing was verified")
	}
	if perm != 0o600 {
		t.Fatalf("recreated WAL perm = %o, want 600", perm)
	}
}

func TestOpenSideFilesArePrivateUnderPermissiveUmask(t *testing.T) {
	withPermissiveUmask(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")
	h := newTestHandle(t, path)
	if err := h.Ensure(ctx); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	for _, suffix := range []string{lockSuffix, wantSuffix} {
		perm, ok := modeOf(t, path+suffix)
		if !ok {
			t.Fatalf("%s was never created", path+suffix)
		}
		if perm != 0o600 {
			t.Fatalf("%s perm = %o, want 600", suffix, perm)
		}
	}
}

// TestSecureUmaskOverlapsWithoutLeaking pins the fix for the umask restore race.
// The umask is process-global, so two databases opening at once both narrow it;
// the second used to record the already-narrowed 0177 as the mask to put back,
// leaving the whole process permanently strict once it finished. Releases here
// run in the order they were taken, which is what concurrent opens do — a
// strictly nested release order hid the bug.
func TestSecureUmaskOverlapsWithoutLeaking(t *testing.T) {
	want := 0o022
	old := unix.Umask(want)
	t.Cleanup(func() { unix.Umask(old) })

	first := secureUmask()
	second := secureUmask()
	if got := currentUmask(); got != 0o177 {
		t.Fatalf("umask under narrowing = %o, want 177", got)
	}
	first()
	if got := currentUmask(); got != 0o177 {
		t.Fatalf("umask after the first restore = %o, want 177 while another open is still "+
			"relying on it", got)
	}
	second()
	if got := currentUmask(); got != want {
		t.Fatalf("umask after both restores = %o, want %o: the narrowed mask leaked into "+
			"every unrelated file this process creates", got, want)
	}
	second()
	if got := currentUmask(); got != want {
		t.Fatalf("umask after a repeated restore = %o, want %o", got, want)
	}
}

func currentUmask() int {
	cur := unix.Umask(0)
	unix.Umask(cur)
	return cur
}
