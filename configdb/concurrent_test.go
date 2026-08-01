package configdb

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const childHold = 5 * time.Second

func TestRevisionChangesOnWrite(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "config.duckdb"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	if _, ok := s.Generation(); ok {
		t.Fatal("Generation() reported a marker before any write")
	}
	if err := s.Import(ctx, "config", []byte("a: 1\n"), "yaml"); err != nil {
		t.Fatalf("import: %v", err)
	}
	first, ok := s.Generation()
	if !ok || first == "" {
		t.Fatalf("Generation() after import = %q, %v", first, ok)
	}
	if err := s.Import(ctx, "config", []byte("a: 2\n"), "yaml"); err != nil {
		t.Fatalf("second import: %v", err)
	}
	second, _ := s.Generation()
	if second == first {
		t.Fatalf("Generation() unchanged across writes: %q", second)
	}
	if err := s.Forget(ctx, "config"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	third, _ := s.Generation()
	if third == second {
		t.Fatalf("Generation() unchanged across Forget: %q", third)
	}
}

func TestApplyWhileAnotherProcessHoldsStore(t *testing.T) {
	if path := os.Getenv("CONFIGDB_TEST_HOLD"); path != "" {
		holdForChild(t, path)
		return
	}

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "config.duckdb")

	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate test binary: %v", err)
	}
	child := exec.Command(self, "-test.run", "TestApplyWhileAnotherProcessHoldsStore")
	child.Env = append(os.Environ(), "CONFIGDB_TEST_HOLD="+path)
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
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open while another process holds the store: %v", err)
	}
	defer s.Close()

	before, _ := s.Generation()
	if err := s.Import(ctx, "config", []byte("role: work\n"), "yaml"); err != nil {
		t.Fatalf("import while another process holds the store: %v", err)
	}
	if elapsed := time.Since(start); elapsed > childHold/2 {
		t.Fatalf("open+import took %s against a live holder; the store is not being released between operations", elapsed)
	}

	after, ok := s.Generation()
	if !ok {
		t.Fatal("Generation() reported no marker after import")
	}
	if after == before {
		t.Fatalf("Generation() unchanged after import: %q", after)
	}

	v, found, err := s.Current(ctx, "config")
	if err != nil || !found {
		t.Fatalf("Current after import = found %v, err %v", found, err)
	}
	if v.Content != "role: work\n" {
		t.Fatalf("Current content = %q, want %q", v.Content, "role: work\n")
	}
}

func holdForChild(t *testing.T, path string) {
	ctx := context.Background()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("child open: %v", err)
	}
	defer s.Close()
	if err := s.Import(ctx, "directives", []byte(`{"a.yaml":"x"}`), "collection"); err != nil {
		t.Fatalf("child import: %v", err)
	}
	if err := os.WriteFile(path+".childready", []byte("1"), 0o600); err != nil {
		t.Fatalf("child ready marker: %v", err)
	}
	for deadline := time.Now().Add(childHold); time.Now().Before(deadline); {
		if _, _, err := s.Current(ctx, "directives"); err != nil {
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
