package duckdb

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The handoff between two processes is the one path a single-process test
// cannot reach: two Handles in one process share the sidecar lock through the
// locks map, so neither ever sees the other queued and the yield never runs.
// Everything contended here therefore drives a real second process.

// contendEnv names the database a contender should poll. It is set only in the
// child process, and selects the child half of the contention tests.
const contendEnv = "DUCKDB_CONTEND_DB"

// contendPoll is how often the contender comes back for the database: often
// enough to be queued against a working Handle much of the time, not so often
// that it is the same test as a second writer.
const contendPoll = 100 * time.Millisecond

const (
	stopSuffix  = ".stop"
	readySuffix = ".ready"
)

// TestHandleContender is the child half of the contention tests: it reads the
// shared database on a poll until the parent drops a stop file. It is skipped
// unless the parent selected it through contendEnv.
func TestHandleContender(t *testing.T) {
	path := os.Getenv(contendEnv)
	if path == "" {
		t.Skip("child half of the two-process contention tests")
	}
	ctx := context.Background()
	h := NewHandle(path, testSchema, Options{Timeout: 60 * time.Second})
	defer func() { _ = h.Close() }()

	for n := 0; !exists(path + stopSuffix); n++ {
		if err := put(ctx, h, "child-"+strconv.Itoa(n), "1"); err != nil {
			t.Fatalf("child put %d: %v", n, err)
		}
		if n == 0 {
			if err := os.WriteFile(path+readySuffix, []byte("1"), 0o600); err != nil {
				t.Fatalf("child ready marker: %v", err)
			}
		}
		time.Sleep(contendPoll)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// startContender runs a second process polling path and returns once it has had
// the database, so the caller is contended from its first operation.
func startContender(t testing.TB, path string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate test binary: %v", err)
	}
	child := exec.Command(self, "-test.run", "^TestHandleContender$")
	child.Env = append(os.Environ(), contendEnv+"="+path)
	child.Stdout, child.Stderr = os.Stderr, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatalf("start contender: %v", err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(path+stopSuffix, []byte("1"), 0o600)
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	})
	waitFor(t, path+readySuffix)
}

// TestHandleTwoProcessContention is the evidence that two independent processes
// interleave on a single-writer database: both get work done, and neither pays
// so much for the handoff that it would have been better off failing.
func TestHandleTwoProcessContention(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/a.duckdb"
	startContender(t, path)

	h := NewHandle(path, testSchema, Options{Timeout: 60 * time.Second})
	t.Cleanup(func() { _ = h.Close() })

	start := time.Now()
	for i := range contendOps {
		if err := put(ctx, h, "parent-"+strconv.Itoa(i), "1"); err != nil {
			t.Fatalf("parent put %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	t.Logf("%d contended operations in %s (%s each)", contendOps, elapsed, elapsed/contendOps)
	if elapsed > contendBudget {
		t.Fatalf("%d operations against a contended database took %s, over the %s budget: the "+
			"two processes are trading the file faster than either can use it", contendOps,
			elapsed, contendBudget)
	}

	parent, child := countRows(ctx, t, h)
	if parent != contendOps {
		t.Fatalf("%d of this process's rows landed, want %d", parent, contendOps)
	}
	if child == 0 {
		t.Fatal("the other process wrote nothing while this one worked: the database is never " +
			"handed over, so a second mino process would starve until it timed out")
	}
	t.Logf("both sides made progress: %d rows here, %d from the contender", parent, child)
}

func countRows(ctx context.Context, t testing.TB, h *Handle) (parent, child int) {
	t.Helper()
	err := h.Do(ctx, func(db *sql.DB) error {
		return db.QueryRowContext(ctx, `SELECT
			count(*) FILTER (WHERE k LIKE 'parent-%'),
			count(*) FILTER (WHERE k LIKE 'child-%') FROM t`).Scan(&parent, &child)
	})
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return parent, child
}

// BenchmarkContendedDo measures an operation against a database a second process
// keeps asking for, which is the cost the handoff governs. The store-level
// benchmarks in kv and journal only ever see an uncontended handle.
func BenchmarkContendedDo(b *testing.B) {
	ctx := context.Background()
	path := b.TempDir() + "/a.duckdb"
	startContender(b, path)

	h := NewHandle(path, testSchema, Options{Timeout: 60 * time.Second})
	b.Cleanup(func() { _ = h.Close() })

	i := 0
	b.ReportAllocs()
	for b.Loop() {
		if err := put(ctx, h, "parent-"+strconv.Itoa(i), "1"); err != nil {
			b.Fatalf("put %d: %v", i, err)
		}
		i++
	}
}

// BenchmarkConcurrentDo measures callers sharing one Handle. They serialise
// twice over — on the handle mutex Do holds across fn, and on the single
// connection the database is opened with — so this measures the cost of that
// queue, not a speedup to be had by shortening it. Narrowing the mutex to a
// refcount was tried and moved these numbers not at all.
func BenchmarkConcurrentDo(b *testing.B) {
	for _, callers := range []int{1, 4, 16} {
		b.Run(strconv.Itoa(callers), func(b *testing.B) {
			ctx := context.Background()
			h := NewHandle(b.TempDir()+"/a.duckdb", testSchema,
				Options{Timeout: 10 * time.Second, MaxHold: time.Hour})
			b.Cleanup(func() { _ = h.Close() })
			if err := put(ctx, h, "seed", "1"); err != nil {
				b.Fatalf("seed: %v", err)
			}
			b.ReportAllocs()
			for b.Loop() {
				var wg sync.WaitGroup
				for range callers {
					wg.Go(func() {
						if _, err := get(ctx, h, "seed"); err != nil {
							b.Errorf("get: %v", err)
						}
					})
				}
				wg.Wait()
			}
		})
	}
}
