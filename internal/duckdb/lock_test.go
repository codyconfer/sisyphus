package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// holdSideLock takes one of the lock files beside path on a descriptor of its
// own. Advisory locks are per-descriptor, so this contends with the Handle in
// this same process exactly as another process would.
func holdSideLock(t *testing.T, path, suffix string) {
	t.Helper()
	f, err := openSideFile(path, suffix)
	if err != nil {
		t.Fatalf("open %s%s: %v", path, suffix, err)
	}
	held, err := tryLock(f)
	if err != nil {
		t.Fatalf("lock %s%s: %v", path, suffix, err)
	}
	if !held {
		t.Fatalf("%s%s was already locked", path, suffix)
	}
	t.Cleanup(func() {
		_ = unlock(f)
		_ = f.Close()
	})
}

func TestHandleContentionReportsErrLocked(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")
	holdSideLock(t, path, lockSuffix)

	h := NewHandle(path, testSchema, Options{Timeout: 50 * time.Millisecond})
	t.Cleanup(func() { _ = h.Close() })
	err := h.Ensure(ctx)
	if err == nil {
		t.Fatal("Ensure succeeded while the database lock was held")
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("Ensure error %v does not match ErrLocked", err)
	}
	if !strings.Contains(err.Error(), "is locked by another process") {
		t.Fatalf("Ensure error %q no longer names the contention; backup.held still reads the text", err)
	}
}

func TestHandleQueueTimeoutReportsErrLocked(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")
	holdSideLock(t, path, wantSuffix)

	h := NewHandle(path, testSchema, Options{Timeout: 50 * time.Millisecond})
	t.Cleanup(func() { _ = h.Close() })
	err := h.Ensure(ctx)
	if err == nil {
		t.Fatal("Ensure succeeded while the queue lock was held")
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("Ensure error %v does not match ErrLocked", err)
	}
	if !strings.Contains(err.Error(), "timed out queueing") {
		t.Fatalf("Ensure error %q no longer names the queue wait", err)
	}
}

func TestAcquireLockContention(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")
	holdSideLock(t, path, lockSuffix)

	l, err := AcquireLock(ctx, path, 50*time.Millisecond)
	if err == nil {
		l.Release()
		t.Fatal("AcquireLock succeeded while the database lock was held")
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("AcquireLock error %v does not match ErrLocked", err)
	}
	if !strings.Contains(err.Error(), "is locked by another process") {
		t.Fatalf("AcquireLock error %q no longer names the contention", err)
	}
}

func TestAcquireLockQueueTimeout(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")
	holdSideLock(t, path, wantSuffix)

	l, err := AcquireLock(ctx, path, 50*time.Millisecond)
	if err == nil {
		l.Release()
		t.Fatal("AcquireLock succeeded while the queue lock was held")
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("AcquireLock error %v does not match ErrLocked", err)
	}
	if !strings.Contains(err.Error(), "timed out queueing") {
		t.Fatalf("AcquireLock error %q no longer names the queue wait", err)
	}
}

func TestAcquireLockExcludesHandles(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")

	l, err := AcquireLock(ctx, path, time.Second)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	t.Cleanup(l.Release)

	h := NewHandle(path, testSchema, Options{Timeout: 50 * time.Millisecond})
	t.Cleanup(func() { _ = h.Close() })
	err = h.Ensure(ctx)
	if err == nil {
		t.Fatal("Ensure succeeded while AcquireLock held the database")
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("Ensure error %v does not match ErrLocked", err)
	}

	l.Release()
	if err := h.Ensure(ctx); err != nil {
		t.Fatalf("Ensure after Release: %v", err)
	}

	l.Release()
	var nilLock *Lock
	nilLock.Release()
}

func TestAcquireLockWaitsOutAnIdleHandle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")

	h := NewHandle(path, testSchema, Options{Idle: 20 * time.Millisecond, Timeout: 10 * time.Second})
	t.Cleanup(func() { _ = h.Close() })
	if err := put(ctx, h, "k", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}

	l, err := AcquireLock(ctx, path, 5*time.Second)
	if err != nil {
		t.Fatalf("AcquireLock while a handle was idling out: %v", err)
	}
	l.Release()
}

func TestAsLockedRecognisesDuckDBLockDiagnostics(t *testing.T) {
	for _, marker := range duckLockMarkers {
		err := asLocked(fmt.Errorf("checkpointing: IO Error: %s by another process", marker))
		if !errors.Is(err, ErrLocked) {
			t.Fatalf("%q was not recognised as contention", marker)
		}
		if !strings.Contains(err.Error(), marker) {
			t.Fatalf("wrapping lost the original text: %q", err)
		}
		if !strings.HasPrefix(err.Error(), "checkpointing: ") {
			t.Fatalf("wrapping lost the caller's context: %q", err)
		}
	}
	if err := asLocked(errors.New("IO Error: not a valid DuckDB database file")); errors.Is(err, ErrLocked) {
		t.Fatal("a corrupt-file error must not be reported as contention")
	}
	if asLocked(nil) != nil {
		t.Fatal("asLocked(nil) must stay nil")
	}
	if errors.Is(ErrClosed, ErrLocked) {
		t.Fatal("ErrClosed must not imply ErrLocked: a closed handle is not contention")
	}
}

func TestHandleWrapsLockedErrorFromOperation(t *testing.T) {
	ctx := context.Background()
	h := newTestHandle(t, filepath.Join(t.TempDir(), "a.duckdb"))
	inner := errors.New("checkpointing: IO Error: Could not set lock on file")
	err := h.Do(ctx, func(*sql.DB) error { return inner })
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("Do error %v does not match ErrLocked", err)
	}
	if !errors.Is(err, inner) {
		t.Fatalf("Do error %v lost the original error", err)
	}
}

// TestHandleYieldDoesNotHoldMutex pins the handoff pause to the database rather
// than to the Handle: while one caller waits for another process to take over,
// unrelated callers of the same Handle must still get through.
func TestHandleYieldDoesNotHoldMutex(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")

	prev := yieldToWaiter
	yieldToWaiter = 2 * time.Second
	t.Cleanup(func() { yieldToWaiter = prev })

	h := NewHandle(path, testSchema, Options{
		Idle:    time.Minute,
		Timeout: 10 * time.Second,
		MaxHold: time.Nanosecond,
	})
	t.Cleanup(func() { _ = h.Close() })

	if err := put(ctx, h, "seed", "1"); err != nil {
		t.Fatalf("seed put: %v", err)
	}

	yielded := make(chan error, 1)
	go func() { yielded <- put(ctx, h, "yielder", "1") }()

	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	if err := put(ctx, h, "other", "1"); err != nil {
		t.Fatalf("unrelated put during the handoff pause: %v", err)
	}
	elapsed := time.Since(start)
	select {
	case err := <-yielded:
		t.Fatalf("the yielding operation finished first (in %s, err=%v): the pause is "+
			"still serialised behind the Handle mutex", elapsed, err)
	default:
	}
	if err := <-yielded; err != nil {
		t.Fatalf("yielding put: %v", err)
	}
}

func TestHandleYieldObservesClose(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")

	prev := yieldToWaiter
	yieldToWaiter = time.Second
	t.Cleanup(func() { yieldToWaiter = prev })

	sentinel := errors.New("gone")
	h := NewHandle(path, testSchema, Options{
		Idle:        time.Minute,
		Timeout:     10 * time.Second,
		MaxHold:     time.Nanosecond,
		Unavailable: sentinel,
	})
	if err := put(ctx, h, "seed", "1"); err != nil {
		t.Fatalf("seed put: %v", err)
	}

	yielded := make(chan error, 1)
	go func() { yielded <- put(ctx, h, "yielder", "1") }()
	time.Sleep(200 * time.Millisecond)
	if err := h.Close(); err != nil {
		t.Fatalf("close during the handoff pause: %v", err)
	}
	if err := <-yielded; !errors.Is(err, sentinel) {
		t.Fatalf("operation resumed across a Close: got %v, want %v", err, sentinel)
	}
}

func TestHandleReusesIdleTimer(t *testing.T) {
	ctx := context.Background()
	h := NewHandle(filepath.Join(t.TempDir(), "a.duckdb"), testSchema,
		Options{Idle: time.Minute, Timeout: 10 * time.Second})
	t.Cleanup(func() { _ = h.Close() })

	if err := put(ctx, h, "k", "1"); err != nil {
		t.Fatalf("first put: %v", err)
	}
	first := h.armedTimer(t)
	if err := put(ctx, h, "k", "2"); err != nil {
		t.Fatalf("second put: %v", err)
	}
	if second := h.armedTimer(t); second != first {
		t.Fatal("each operation allocated a new idle timer; the existing one should be reset")
	}
}

func (h *Handle) armedTimer(t *testing.T) *time.Timer {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.timer == nil {
		t.Fatal("no idle timer armed after an operation")
	}
	return h.timer
}

const repeatedArmingScope = "this test pins only that repeated arming still ends in a close; five puts " +
	"finish inside one 40ms window, so onIdle runs once with gen == armed and pending == 1 — the " +
	"stale-wakeup guards it names are covered by TestHandleIgnoresStaleIdleWakeup"

func TestHandleIdleCloseAfterRepeatedArming(t *testing.T) {
	t.Log(repeatedArmingScope)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")
	h := NewHandle(path, testSchema, Options{Idle: 40 * time.Millisecond, Timeout: 10 * time.Second})
	t.Cleanup(func() { _ = h.Close() })

	for i := 0; i < 5; i++ {
		if err := put(ctx, h, "k", "v"); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		open := h.db != nil
		h.mu.Unlock()
		if !open {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatal("the database was never closed after the idle window; a reused timer's " +
				"wakeup was dropped")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *Handle) isOpen() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.db != nil
}

const staleWakeupContract = "a timer that has already expired cannot be called off, so its goroutine " +
	"reaches the mutex after the handle was reused; onIdle is driven directly here because " +
	"reproducing either guard by sleeping is what makes a timing test pass for the wrong reason"

func TestHandleIgnoresStaleIdleWakeup(t *testing.T) {
	t.Log(staleWakeupContract)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")
	h := NewHandle(path, testSchema, Options{Idle: time.Hour, Timeout: 10 * time.Second})
	t.Cleanup(func() { _ = h.Close() })

	if err := put(ctx, h, "k", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if !h.isOpen() {
		t.Fatal("database is not open after a put; nothing below is meaningful")
	}

	h.mu.Lock()
	h.gen++
	h.pending = 1
	h.mu.Unlock()
	h.onIdle()
	if !h.isOpen() {
		t.Fatal("a wakeup armed for an earlier generation closed the database; the handle had been " +
			"used again since, so an in-flight caller just lost its connection")
	}

	h.mu.Lock()
	h.armed = h.gen
	h.pending = 2
	h.mu.Unlock()
	h.onIdle()
	if !h.isOpen() {
		t.Fatal("a wakeup closed the database while a second firing was still owed; the later wakeup " +
			"is the one that owns the decision")
	}

	h.onIdle()
	if h.isOpen() {
		t.Fatal("the last outstanding wakeup did not close the database, so an idle handle holds the " +
			"file lock forever")
	}
}
