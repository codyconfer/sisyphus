package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codyconfer/sisyphus/kv"
)

const childHold = 5 * time.Second

// TestQueryWhileAnotherProcessHoldsDB is the ad-hoc-query equivalent of the
// journal's cross-process test: with a holder alive, Query must queue for the
// handoff and succeed rather than surfacing DuckDB's own lock error.
func TestQueryWhileAnotherProcessHoldsDB(t *testing.T) {
	if path := os.Getenv("STORE_TEST_HOLD"); path != "" {
		holdForChild(t, path)
		return
	}

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "held.duckdb")

	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate test binary: %v", err)
	}
	child := exec.Command(self, "-test.run", "TestQueryWhileAnotherProcessHoldsDB")
	child.Env = append(os.Environ(), "STORE_TEST_HOLD="+path)
	child.Stdout, child.Stderr = os.Stderr, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	})
	waitFor(t, path+".childready")

	start := time.Now()
	res, err := Query(ctx, path, `SELECT value FROM kv WHERE namespace = ? AND key = ?`, "ns", "child")
	if err != nil {
		if strings.Contains(err.Error(), "Conflicting lock") {
			t.Fatalf("Query bypassed the lock handoff and hit DuckDB's own lock: %v", err)
		}
		t.Fatalf("Query while another process holds the database: %v", err)
	}
	if elapsed := time.Since(start); elapsed > childHold/2 {
		t.Fatalf("Query took %s against a live holder; it is not getting a handoff", elapsed)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "wrote" {
		t.Fatalf("Query = %+v, want the child's row", res)
	}
}

// TestQuerySharesAnOpenHandleInTheSameProcess guards the case a naive fix
// breaks: an in-process holder must be shared, not waited on.
func TestQuerySharesAnOpenHandleInTheSameProcess(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "same.duckdb")
	s, err := kv.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Put(ctx, "ns", "k", "v", time.Time{}); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	res, err := Query(ctx, path, `SELECT value FROM kv WHERE namespace = ?`, "ns")
	if err != nil {
		t.Fatalf("Query against a handle this process holds: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Query waited %s on a lock held by its own process", elapsed)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "v" {
		t.Fatalf("Query = %+v", res)
	}
	if err := s.Put(ctx, "ns", "k2", "v2", time.Time{}); err != nil {
		t.Fatalf("holder unusable after Query: %v", err)
	}
}

func holdForChild(t *testing.T, path string) {
	ctx := context.Background()
	s, err := kv.Open(ctx, path)
	if err != nil {
		t.Fatalf("child open: %v", err)
	}
	defer s.Close()
	if err := s.Put(ctx, "ns", "child", "wrote", time.Time{}); err != nil {
		t.Fatalf("child put: %v", err)
	}
	if err := os.WriteFile(path+".childready", []byte("1"), 0o600); err != nil {
		t.Fatalf("child ready marker: %v", err)
	}
	for deadline := time.Now().Add(childHold); time.Now().Before(deadline); {
		if _, _, err := s.Get(ctx, "ns", "child"); err != nil {
			t.Fatalf("child read: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitFor(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
