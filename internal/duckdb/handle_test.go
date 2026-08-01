package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const testSchema = `CREATE TABLE IF NOT EXISTS t (k VARCHAR PRIMARY KEY, v VARCHAR);`

func newTestHandle(t *testing.T, path string) *Handle {
	t.Helper()
	h := NewHandle(path, testSchema, Options{Idle: 20 * time.Millisecond, Timeout: 10 * time.Second})
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func put(ctx context.Context, h *Handle, k, v string) error {
	return h.Do(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO t VALUES (?, ?)`, k, v)
		return err
	})
}

func get(ctx context.Context, h *Handle, k string) (string, error) {
	var v string
	err := h.Do(ctx, func(db *sql.DB) error {
		return db.QueryRowContext(ctx, `SELECT v FROM t WHERE k = ?`, k).Scan(&v)
	})
	return v, err
}

func TestHandleRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newTestHandle(t, filepath.Join(t.TempDir(), "a.duckdb"))
	if err := put(ctx, h, "k", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := get(ctx, h, "k")
	if err != nil || got != "v" {
		t.Fatalf("get = %q, %v; want \"v\", nil", got, err)
	}
}

func TestHandleReopensAfterIdle(t *testing.T) {
	ctx := context.Background()
	h := newTestHandle(t, filepath.Join(t.TempDir(), "a.duckdb"))
	if err := put(ctx, h, "k", "first"); err != nil {
		t.Fatalf("put: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := put(ctx, h, "k", "second"); err != nil {
		t.Fatalf("put after idle: %v", err)
	}
	got, err := get(ctx, h, "k")
	if err != nil || got != "second" {
		t.Fatalf("get = %q, %v; want \"second\", nil", got, err)
	}
}

func TestHandleCloseIsTerminal(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("gone")
	h := NewHandle(filepath.Join(t.TempDir(), "a.duckdb"), testSchema, Options{Unavailable: sentinel})
	if err := h.Ensure(ctx); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := put(ctx, h, "k", "v"); !errors.Is(err, sentinel) {
		t.Fatalf("put after close = %v, want %v", err, sentinel)
	}
}

func TestHandleSamePathSameProcess(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")
	a := newTestHandle(t, path)
	b := newTestHandle(t, path)

	if err := put(ctx, a, "k", "from-a"); err != nil {
		t.Fatalf("a put: %v", err)
	}
	start := time.Now()
	got, err := get(ctx, b, "k")
	if err != nil {
		t.Fatalf("b get: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("b waited %s for a lock held by its own process", elapsed)
	}
	if got != "from-a" {
		t.Fatalf("b saw %q, want %q", got, "from-a")
	}
}

func TestHandleCrossProcessWrite(t *testing.T) {
	if path := os.Getenv("HANDLE_TEST_HOLD"); path != "" {
		holdForChild(t, path)
		return
	}

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")

	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate test binary: %v", err)
	}
	child := exec.Command(self, "-test.run", "TestHandleCrossProcessWrite")
	child.Env = append(os.Environ(), "HANDLE_TEST_HOLD="+path)
	child.Stdout, child.Stderr = os.Stderr, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	})
	waitFor(t, path+".childready")

	h := newTestHandle(t, path)
	start := time.Now()
	if err := put(ctx, h, "parent", "wrote"); err != nil {
		t.Fatalf("parent write while child holds the store: %v", err)
	}
	if elapsed := time.Since(start); elapsed > childHold/2 {
		t.Fatalf("parent waited %s for a live child; the idle window is not releasing the file", elapsed)
	}
	got, err := get(ctx, h, "child")
	if err != nil {
		t.Fatalf("parent read of the child's row: %v", err)
	}
	if got != "wrote" {
		t.Fatalf("parent saw child row %q, want %q", got, "wrote")
	}
}

const childHold = 5 * time.Second

func holdForChild(t *testing.T, path string) {
	ctx := context.Background()
	h := NewHandle(path, testSchema, Options{Timeout: 10 * time.Second})
	defer h.Close()
	if err := put(ctx, h, "child", "wrote"); err != nil {
		t.Fatalf("child write: %v", err)
	}
	if err := os.WriteFile(path+".childready", []byte("1"), 0o600); err != nil {
		t.Fatalf("child ready marker: %v", err)
	}
	for deadline := time.Now().Add(childHold); time.Now().Before(deadline); {
		if _, err := get(ctx, h, "child"); err != nil {
			t.Fatalf("child read: %v", err)
		}
		time.Sleep(DefaultIdle / 5)
	}
}

func waitFor(t testing.TB, path string) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
