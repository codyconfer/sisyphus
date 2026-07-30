package journal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const childHold = 5 * time.Second

func TestRecordWhileAnotherProcessHoldsJournal(t *testing.T) {
	if path := os.Getenv("JOURNAL_TEST_HOLD"); path != "" {
		holdForChild(t, path)
		return
	}

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "audit.duckdb")

	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate test binary: %v", err)
	}
	child := exec.Command(self, "-test.run", "TestRecordWhileAnotherProcessHoldsJournal")
	child.Env = append(os.Environ(), "JOURNAL_TEST_HOLD="+path)
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
		t.Fatalf("open while another process holds the journal: %v", err)
	}
	defer s.Close()

	id, err := s.Begin(ctx, "flight", "morning", nil)
	if err != nil {
		t.Fatalf("begin while another process holds the journal: %v", err)
	}
	if err := s.RollUp(ctx, id); err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if elapsed := time.Since(start); elapsed > childHold/2 {
		t.Fatalf("open+record took %s against a live holder; the journal is not being released between operations", elapsed)
	}

	runs, err := s.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	var names []string
	for _, r := range runs {
		names = append(names, r.Name)
	}
	if len(runs) < 2 {
		t.Fatalf("recent = %v, want both this process's run and the child's", names)
	}
}

func holdForChild(t *testing.T, path string) {
	ctx := context.Background()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("child open: %v", err)
	}
	defer s.Close()
	if _, err := s.Begin(ctx, "flight", "child", nil); err != nil {
		t.Fatalf("child begin: %v", err)
	}
	if err := os.WriteFile(path+".childready", []byte("1"), 0o600); err != nil {
		t.Fatalf("child ready marker: %v", err)
	}
	for deadline := time.Now().Add(childHold); time.Now().Before(deadline); {
		if _, err := s.Recent(ctx, 5); err != nil {
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
