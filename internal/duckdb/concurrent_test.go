package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"math/rand"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// state snapshots the bookkeeping fields under the mutex.
func (h *Handle) state() (pending int, open, closed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pending, h.db != nil, h.closed
}

// closeWithin reports how long the handle took to release the database, or -1
// if it was still holding it at the deadline.
func closeWithin(h *Handle, limit time.Duration) time.Duration {
	start := time.Now()
	for time.Since(start) < limit {
		if !h.isOpen() {
			return time.Since(start)
		}
		time.Sleep(time.Millisecond)
	}
	return -1
}

func lockHeld(path string) bool {
	locks.mu.Lock()
	defer locks.mu.Unlock()
	_, held := locks.m[lockKey(path)]
	return held
}

// TestDoSerialisesReadModifyWrite pins the guarantee kv, journal and configdb
// build on: Do holds h.mu for the whole of fn, so a read and the write that
// depends on it land as one step without fn wrapping itself in a transaction.
// Each fn here SELECTs a counter, sleeps, then writes back value+1; if Do
// serialises, the final value is exactly writers*rounds.
func TestDoSerialisesReadModifyWrite(t *testing.T) {
	ctx := context.Background()
	h := NewHandle(filepath.Join(t.TempDir(), "rmw.duckdb"), testSchema,
		Options{Idle: 5 * time.Millisecond, Timeout: 20 * time.Second, MaxHold: 10 * time.Millisecond})
	t.Cleanup(func() { _ = h.Close() })

	if err := put(ctx, h, "n", "0"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const writers, rounds = 4, 40
	var inside, overlaps atomic.Int64
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				err := h.Do(ctx, func(db *sql.DB) error {
					if inside.Add(1) != 1 {
						overlaps.Add(1)
					}
					defer inside.Add(-1)
					// A window wide enough that any overlap would be caught.
					return increment(ctx, db, time.Millisecond)
				})
				if err != nil {
					t.Errorf("do: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if n := overlaps.Load(); n != 0 {
		t.Fatalf("%d fns ran concurrently inside Do: the mutex-across-fn contract is gone", n)
	}
	got, err := get(ctx, h, "n")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if want := strconv.Itoa(writers * rounds); got != want {
		t.Fatalf("counter = %s, want %s: increments were lost to an interleaved "+
			"read-modify-write", got, want)
	}
}

// TestReadModifyWriteWithoutDoLosesWrites is the negative control for the two
// tests above and below. It runs the identical workload straight against the
// *sql.DB, which is what a Do that released the mutex around fn would allow.
// The single connection serialises statements but not a pair of them, so
// increments must be lost here — if they are not, those assertions prove
// nothing.
func TestReadModifyWriteWithoutDoLosesWrites(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "raw.duckdb"), testSchema)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO t VALUES ('n','0')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const writers, rounds = 4, 40
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				if err := increment(ctx, db, time.Millisecond); err != nil {
					t.Errorf("increment: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	n, err := counter(ctx, db)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	t.Logf("unserialised counter = %d, want %d if the pair were atomic", n, writers*rounds)
	if n >= writers*rounds {
		t.Fatalf("the unserialised workload lost nothing (%d/%d): the read-modify-write detector "+
			"cannot tell a serialising Do from a non-serialising one", n, writers*rounds)
	}
}

// TestDoSerialisesAcrossTheYield forces the handoff path — MaxHold a nanosecond,
// so every Do shuts, yields and reopens — and still demands that no two fns
// overlap. The yield drops h.mu mid-Do, which is the one place the contract
// could leak.
func TestDoSerialisesAcrossTheYield(t *testing.T) {
	ctx := context.Background()
	prev := yieldToWaiter
	yieldToWaiter = 2 * time.Millisecond
	t.Cleanup(func() { yieldToWaiter = prev })

	h := NewHandle(filepath.Join(t.TempDir(), "y.duckdb"), testSchema,
		Options{Idle: time.Millisecond, Timeout: 20 * time.Second, MaxHold: time.Nanosecond})
	t.Cleanup(func() { _ = h.Close() })
	if err := put(ctx, h, "n", "0"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const writers, rounds = 6, 25
	var inside, overlaps atomic.Int64
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				err := h.Do(ctx, func(db *sql.DB) error {
					if inside.Add(1) != 1 {
						overlaps.Add(1)
					}
					defer inside.Add(-1)
					return increment(ctx, db, 0)
				})
				if err != nil {
					t.Errorf("do: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if n := overlaps.Load(); n != 0 {
		t.Fatalf("%d overlapping fns across the yield window", n)
	}
	got, err := get(ctx, h, "n")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if want := strconv.Itoa(writers * rounds); got != want {
		t.Fatalf("counter = %s, want %s: increments lost across the yield path", got, want)
	}
}

// increment reads the counter, pauses, then writes back one more, with no
// transaction around the pair. The pause is the window an interleaved writer
// would slip into.
func increment(ctx context.Context, db *sql.DB, pause time.Duration) error {
	n, err := counter(ctx, db)
	if err != nil {
		return err
	}
	time.Sleep(pause)
	_, err = db.ExecContext(ctx, `INSERT OR REPLACE INTO t VALUES ('n', ?)`, strconv.Itoa(n+1))
	return err
}

func counter(ctx context.Context, db *sql.DB) (int, error) {
	var s string
	if err := db.QueryRowContext(ctx, `SELECT v FROM t WHERE k='n'`).Scan(&s); err != nil {
		return 0, err
	}
	return strconv.Atoi(s)
}

// TestIdleWakeupCannotCloseUnderRunningOperation runs one fn far longer than the
// idle window and the maxHold backstop, so the timer fires while it is working,
// and requires the connection to stay live for the whole of it. onIdle takes
// h.mu, which Do holds across fn, which is what makes that so.
func TestIdleWakeupCannotCloseUnderRunningOperation(t *testing.T) {
	ctx := context.Background()
	h := NewHandle(filepath.Join(t.TempDir(), "slow.duckdb"), testSchema, Options{
		Idle: 5 * time.Millisecond, Timeout: 10 * time.Second, MaxHold: 5 * time.Millisecond,
	})
	t.Cleanup(func() { _ = h.Close() })
	if err := put(ctx, h, "seed", "1"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := h.Do(ctx, func(db *sql.DB) error {
		deadline := time.Now().Add(500 * time.Millisecond)
		for i := 0; time.Now().Before(deadline); i++ {
			if _, err := db.ExecContext(ctx,
				`INSERT OR REPLACE INTO t VALUES (?, '1')`, "row"+strconv.Itoa(i)); err != nil {
				return err
			}
			time.Sleep(5 * time.Millisecond)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("a long-running fn lost its connection: %v", err)
	}
}

// TestCloseWaitsForInFlightOperation pins that Close does not return until a
// running operation has finished, and that the operation still sees a live
// connection. Do holds h.mu across fn, so Close cannot get in until fn returns.
func TestCloseWaitsForInFlightOperation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.duckdb")
	h := NewHandle(path, testSchema, Options{Idle: time.Millisecond, Timeout: 10 * time.Second})

	entered := make(chan struct{})
	release := make(chan struct{})
	inner := make(chan error, 1)
	go func() {
		inner <- h.Do(ctx, func(db *sql.DB) error {
			close(entered)
			<-release
			// The database must still be usable here: Close is blocked.
			_, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO t VALUES ('late','1')`)
			return err
		})
	}()
	<-entered

	closed := make(chan error, 1)
	go func() { closed <- h.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned (%v) while an operation was still running", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	if err := <-inner; err != nil {
		t.Fatalf("operation that Close waited for: %v", err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close never returned after the operation finished: the drain deadlocked")
	}
	if _, open, _ := h.state(); open {
		t.Fatal("after Close: the database is still open")
	}
	if lockHeld(path) {
		t.Fatal("Close left the cross-process lock held")
	}
}

// TestCloseRacesOperations fires Close at every offset into a storm of
// operations and requires every failure to be a lifecycle sentinel, never a
// connection yanked out from under a live query.
func TestCloseRacesOperations(t *testing.T) {
	for round := range 20 {
		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "a"+strconv.Itoa(round)+".duckdb")
		h := NewHandle(path, testSchema, Options{
			Idle:    time.Millisecond,
			Timeout: 30 * time.Second,
			MaxHold: time.Millisecond,
		})
		if err := h.Ensure(ctx); err != nil {
			t.Fatalf("ensure: %v", err)
		}
		var wg sync.WaitGroup
		for g := range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 20 {
					err := put(ctx, h, "g"+strconv.Itoa(g)+"-"+strconv.Itoa(i), "1")
					if err == nil || errors.Is(err, ErrClosed) || errors.Is(err, ErrLocked) ||
						errors.Is(err, context.Canceled) {
						continue
					}
					t.Errorf("unexpected error racing Close: %v", err)
					return
				}
			}()
		}
		time.Sleep(time.Duration(round) * time.Millisecond)
		if err := h.Close(); err != nil {
			t.Fatalf("round %d: close: %v", round, err)
		}
		wg.Wait()
		pending, open, closed := h.state()
		if open || !closed || pending < 0 {
			t.Fatalf("round %d after Close: open=%v closed=%v pending=%d", round, open, closed, pending)
		}
		if lockHeld(path) {
			t.Fatalf("round %d: lock leaked past Close", round)
		}
	}
}

// TestBusyHandleYieldsToWaiter is the bounded-handoff property for a Handle in
// a tight loop, which the idle window alone never covers: each operation
// re-arms it, so only the demand check at the top of Do lets another process
// in. The waiter is faked by flocking the .wait sidecar on a descriptor of its
// own, which is exactly what another process does.
func TestBusyHandleYieldsToWaiter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "a.duckdb")
	h := NewHandle(path, testSchema, Options{
		Idle: time.Hour, Timeout: 10 * time.Second, MaxHold: time.Hour,
	})
	t.Cleanup(func() { _ = h.Close() })

	if err := put(ctx, h, "seed", "1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	busy := make(chan struct{})
	go func() {
		defer close(busy)
		for i := 0; ctx.Err() == nil; i++ {
			if err := put(ctx, h, "hot-"+strconv.Itoa(i), "1"); err != nil {
				return
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)

	want, err := openSideFile(path, wantSuffix)
	if err != nil {
		t.Fatalf("open .wait: %v", err)
	}
	defer func() { _ = unlock(want); _ = want.Close() }()
	if got, err := waitLock(ctx, want, 5*time.Second); err != nil || !got {
		t.Fatalf("queue on .wait: got=%v err=%v", got, err)
	}

	// Now that the demand is announced, the busy loop must drop the file lock.
	start := time.Now()
	for lockHeld(path) {
		if time.Since(start) > 5*time.Second {
			t.Fatalf("a busy Handle never released the file lock in %s after a waiter queued: "+
				"an independent process would starve until it timed out", time.Since(start))
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Logf("busy handle released after %s", time.Since(start))
	cancel()
	<-busy
}

// TestHandleLifecycleFuzz drives Do, Ensure and the timer paths from many
// goroutines with the windows squeezed to microseconds, then requires the
// Handle to quiesce cleanly: closed, no wakeup owed, no lock held.
func TestHandleLifecycleFuzz(t *testing.T) {
	prev := yieldToWaiter
	yieldToWaiter = 200 * time.Microsecond
	t.Cleanup(func() { yieldToWaiter = prev })

	for round := range 5 {
		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "f.duckdb")
		rng := rand.New(rand.NewSource(int64(round)))
		h := NewHandle(path, testSchema, Options{
			Idle:    time.Duration(1+rng.Intn(3000)) * time.Microsecond,
			Timeout: 30 * time.Second,
			MaxHold: time.Duration(1+rng.Intn(3000)) * time.Microsecond,
		})

		// A watcher on the timer accounting, which is where churn this hard
		// leaks or double-counts wakeups.
		var maxPending atomic.Int64
		stop := make(chan struct{})
		var watch sync.WaitGroup
		watch.Add(1)
		go func() {
			defer watch.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				p, _, _ := h.state()
				if p < 0 {
					t.Errorf("round %d: pending went negative (%d)", round, p)
					return
				}
				for cur := maxPending.Load(); int64(p) > cur; cur = maxPending.Load() {
					if maxPending.CompareAndSwap(cur, int64(p)) {
						break
					}
				}
				time.Sleep(50 * time.Microsecond)
			}
		}()

		var wg sync.WaitGroup
		for g := range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := rand.New(rand.NewSource(int64(round*100 + g)))
				for i := range 60 {
					var err error
					if r.Intn(3) == 0 {
						err = h.Ensure(ctx)
					} else {
						err = put(ctx, h, "g"+strconv.Itoa(g)+"-"+strconv.Itoa(i), "1")
					}
					if err == nil || errors.Is(err, ErrLocked) {
						if r.Intn(7) == 0 {
							time.Sleep(time.Duration(r.Intn(4000)) * time.Microsecond)
						}
						continue
					}
					if strings.Contains(err.Error(), "database is closed") {
						t.Errorf("round %d: connection yanked under a live op: %v", round, err)
					} else {
						t.Errorf("round %d: unexpected error: %v", round, err)
					}
					return
				}
			}()
		}
		wg.Wait()
		close(stop)
		watch.Wait()

		if n := maxPending.Load(); n > 8 {
			t.Fatalf("round %d: pending climbed to %d, the timer accounting is leaking wakeups", round, n)
		}
		if closeWithin(h, 5*time.Second) < 0 {
			t.Fatalf("round %d: handle stuck open after quiescing", round)
		}
		time.Sleep(100 * time.Millisecond)
		if p, open, _ := h.state(); p != 0 || open {
			t.Fatalf("round %d: quiesced with pending=%d open=%v, want 0/false", round, p, open)
		}
		if err := h.Close(); err != nil {
			t.Fatalf("round %d: close: %v", round, err)
		}
		if lockHeld(path) {
			t.Fatalf("round %d: file lock still held after Close", round)
		}
	}
}

// TestCancelledYieldLeavesHandleUsable cancels the context while a Do sits in
// the yield window with h.mu released. Do bails out there without re-arming, so
// this is where a Handle could be left holding the file with no countdown to
// let go of it.
func TestCancelledYieldLeavesHandleUsable(t *testing.T) {
	prev := yieldToWaiter
	yieldToWaiter = 300 * time.Millisecond
	t.Cleanup(func() { yieldToWaiter = prev })

	for round := range 8 {
		path := filepath.Join(t.TempDir(), "cz.duckdb")
		h := NewHandle(path, testSchema, Options{
			Idle: 400 * time.Millisecond, Timeout: 10 * time.Second, MaxHold: time.Nanosecond,
		})
		if err := h.Ensure(context.Background()); err != nil {
			t.Fatalf("round %d: ensure: %v", round, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- put(ctx, h, "k", "1") }()
		time.Sleep(time.Duration(20+round*30) * time.Millisecond)
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("round %d: %v", round, err)
		}
		if closeWithin(h, 3*time.Second) < 0 {
			p, _, _ := h.state()
			t.Fatalf("round %d: a cancelled Do left the handle holding the database with no "+
				"countdown (pending=%d)", round, p)
		}
		if lockHeld(path) {
			t.Fatalf("round %d: cancelled Do leaked the file lock", round)
		}
		if err := put(context.Background(), h, "after", "1"); err != nil {
			t.Fatalf("round %d: handle unusable after a cancelled Do: %v", round, err)
		}
		if err := h.Close(); err != nil {
			t.Fatalf("round %d: close: %v", round, err)
		}
	}
}

// TestFailedOpensLeaveNoWakeupOwed makes every open fail — the sidecar lock is
// held elsewhere — and checks the Handle neither leaks a wakeup nor wedges: it
// reports ErrLocked and stays usable once the lock frees.
func TestFailedOpensLeaveNoWakeupOwed(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "of.duckdb")
	holdSideLock(t, path, lockSuffix)

	h := NewHandle(path, testSchema, Options{
		Idle: 20 * time.Millisecond, Timeout: 30 * time.Millisecond, MaxHold: time.Hour,
	})
	t.Cleanup(func() { _ = h.Close() })
	for i := range 30 {
		if err := put(ctx, h, "k", "1"); !errors.Is(err, ErrLocked) {
			t.Fatalf("attempt %d: %v, want ErrLocked", i, err)
		}
	}
	if p, open, _ := h.state(); p != 0 || open {
		t.Fatalf("after 30 failed opens: pending=%d open=%v, want 0/false", p, open)
	}
}

// TestManyHandlesShareOneFileLock runs several Handles on one file in one
// process, which is what the lock refcount exists for, and checks they neither
// deadlock nor strand the shared lock.
func TestManyHandlesShareOneFileLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "many.duckdb")
	// Apply the schema once up front: several Handles opening a fresh file at
	// the same instant each run CREATE TABLE IF NOT EXISTS, and DuckDB rejects
	// the losers with a catalog write-write conflict. Not what this is about.
	seed := NewHandle(path, testSchema, Options{Timeout: 30 * time.Second})
	if err := seed.Ensure(ctx); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("seed close: %v", err)
	}

	const handles, ops = 4, 40
	hs := make([]*Handle, handles)
	for i := range hs {
		hs[i] = NewHandle(path, testSchema, Options{
			Idle: 30 * time.Millisecond, Timeout: 30 * time.Second, MaxHold: 2 * time.Second,
		})
	}
	var wg sync.WaitGroup
	for i, h := range hs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ops {
				if err := put(ctx, h, "h"+strconv.Itoa(i)+"-"+strconv.Itoa(j), "1"); err != nil {
					t.Errorf("handle %d op %d: %v", i, j, err)
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}
	wg.Wait()

	for i, h := range hs {
		if closeWithin(h, 5*time.Second) < 0 {
			t.Fatalf("handle %d never released", i)
		}
		if err := h.Close(); err != nil {
			t.Fatalf("handle %d close: %v", i, err)
		}
	}
	if lockHeld(path) {
		t.Fatal("the shared file lock outlived every handle: the refcount is unbalanced")
	}
}
