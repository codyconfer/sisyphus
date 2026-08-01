package configdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "config.duckdb"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestBumpConcurrentWritersDoNotClobberEachOther(t *testing.T) {
	s := openStore(t)

	const writers, rounds = 8, 100
	errs := make(chan error, writers*rounds)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				if err := s.bump("w" + strconv.Itoa(w) + "-r" + strconv.Itoa(r)); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)

	failed := 0
	for err := range errs {
		if failed == 0 {
			t.Errorf("concurrent bump failed: %v", err)
		}
		failed++
	}
	if failed > 0 {
		t.Fatalf("%d of %d concurrent bumps failed", failed, writers*rounds)
	}
	if rev, ok := s.Generation(); !ok || rev == "" {
		t.Fatalf("Generation() after concurrent bumps = %q, %v", rev, ok)
	}
	if left := stagingLeftovers(t, s); len(left) != 0 {
		t.Fatalf("staging files left behind: %v", left)
	}
}

func TestBumpIgnoresFixedTempPath(t *testing.T) {
	s := openStore(t)
	if err := os.Mkdir(s.h.Path()+revSuffix+".tmp", 0o700); err != nil {
		t.Fatalf("occupy the shared temp path: %v", err)
	}

	if err := s.bump("after-the-temp-path-is-taken"); err != nil {
		t.Fatalf("bump through a shared temp path: %v", err)
	}
	if rev, ok := s.Generation(); !ok || rev == "" {
		t.Fatalf("Generation() = %q, %v", rev, ok)
	}
}

func TestBumpFailureReportsCommittedChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce POSIX directory write bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s := openStore(t)
	ctx := context.Background()
	if err := s.Import(ctx, "config", []byte("a: 1\n"), "yaml"); err != nil {
		t.Fatalf("import: %v", err)
	}
	dir := filepath.Dir(s.h.Path())
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := s.bump("unwritable")
	if err == nil {
		t.Fatal("bump reported success with an unwritable directory")
	}
	if !errors.Is(err, ErrGenerationMarker) {
		t.Fatalf("bump error = %v, want one matching ErrGenerationMarker", err)
	}
}

func stagingLeftovers(t *testing.T, s *Store) []string {
	t.Helper()
	dir, base := filepath.Split(s.h.Path() + revSuffix)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var left []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "."+base+".") || e.Name() == base+".tmp" {
			left = append(left, e.Name())
		}
	}
	return left
}
