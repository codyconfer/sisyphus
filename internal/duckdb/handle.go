package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultIdle is how long a Handle keeps a database open after its last
// operation. Consecutive operations coalesce into one open; after the window
// closes the file and its lock are released so another process can take them.
const DefaultIdle = 250 * time.Millisecond

// DefaultTimeout bounds how long Do waits for another process to release the
// database before giving up.
const DefaultTimeout = 5 * time.Second

// DefaultMaxHold is a backstop on how long a Handle keeps the database open
// across back-to-back operations.
//
// The idle window alone is not enough: a caller that works more often than the
// window is long — a deck polling its home flight, say — re-arms the timer every
// time and would otherwise hold the file indefinitely. Handing off on demand
// (see wantSuffix) is what normally prevents that; this cap only covers a waiter
// that never announced itself.
const DefaultMaxHold = 10 * time.Second

// yieldToWaiter is how long a Handle stays out of the database after handing it
// off, long enough for the process it yielded to to take the lock. It is a
// variable only so tests can widen it.
var yieldToWaiter = 150 * time.Millisecond

const lockPoll = 10 * time.Millisecond

// ErrClosed is returned by Do after Close. Closing is terminal: a Handle never
// reopens, so a caller that closed a database to move its files underneath
// cannot have it reopened by a stray concurrent operation.
var ErrClosed = errors.New("duckdb: handle closed")

// ErrLocked reports that a database could not be used because another process
// holds it: either this package's cross-process handoff lock, or DuckDB's own
// single-writer lock on the file.
//
// It marks contention alone — a condition that a retry may clear — and never a
// corrupt or unreadable file, so callers may treat it as "busy, try later".
// Every error that carries it keeps its own message; match it with errors.Is
// rather than by inspecting text. ErrClosed does not imply ErrLocked: a closed
// Handle is a caller-side lifecycle error, not contention.
var ErrLocked = errors.New("duckdb: database is locked by another process")

// lockedError tags an error as contention without altering what it says, so
// existing diagnostics keep naming the file, the holder and the wait.
type lockedError struct{ err error }

func (e lockedError) Error() string { return e.err.Error() }

func (e lockedError) Unwrap() []error { return []error{e.err, ErrLocked} }

// duckLockMarkers are the diagnostics DuckDB itself produces when its
// single-writer lock is already taken. DuckDB reports them as plain IO errors,
// so there is nothing else to match on.
var duckLockMarkers = [...]string{
	"Conflicting lock is held",
	"Could not set lock on file",
}

// asLocked marks err as ErrLocked when it is DuckDB reporting its own lock
// conflict. Errors raised by this package are wrapped at their source instead.
func asLocked(err error) error {
	if err == nil || errors.Is(err, ErrLocked) {
		return err
	}
	msg := err.Error()
	for _, marker := range duckLockMarkers {
		if strings.Contains(msg, marker) {
			return lockedError{err}
		}
	}
	return err
}

// Options configures a Handle. The zero value uses DefaultIdle and
// DefaultTimeout, and reports ErrClosed for use-after-close.
type Options struct {
	// Idle is how long to hold the database open after an operation.
	Idle time.Duration
	// Timeout bounds waiting for another process to release the database.
	Timeout time.Duration
	// MaxHold caps how long the database stays open across back-to-back
	// operations, so steady work cannot starve other processes.
	MaxHold time.Duration
	// Unavailable, when set, replaces ErrClosed so wrapping packages can keep
	// reporting their own sentinel for use-after-close.
	Unavailable error
}

// Handle owns one DuckDB file, opening it on demand under a cross-process
// advisory lock and closing it again once idle.
//
// DuckDB allows only one read-write process per file, so a handle that stayed
// open for a process lifetime would lock every other process out. Holding the
// file only around actual work lets independent processes interleave: a
// concurrent one waits for the idle window rather than failing outright.
type Handle struct {
	path    string
	wal     string
	schema  string
	idle    time.Duration
	timeout time.Duration
	maxHold time.Duration
	unavail error

	mu      sync.Mutex
	db      *sql.DB
	timer   *time.Timer
	since   time.Time
	gen     uint64
	armed   uint64
	pending int
	closed  bool
}

// NewHandle prepares a Handle for path. The file is not touched until the first
// Do, so constructing a Handle never contends with another process.
func NewHandle(path, schema string, opts Options) *Handle {
	h := &Handle{
		path:    path,
		wal:     path + walSuffix,
		schema:  schema,
		idle:    opts.Idle,
		timeout: opts.Timeout,
		maxHold: opts.MaxHold,
		unavail: opts.Unavailable,
	}
	if h.idle <= 0 {
		h.idle = DefaultIdle
	}
	if h.timeout <= 0 {
		h.timeout = DefaultTimeout
	}
	if h.maxHold <= 0 {
		h.maxHold = DefaultMaxHold
	}
	if h.unavail == nil {
		h.unavail = ErrClosed
	}
	return h
}

// Path reports the database file this Handle owns.
func (h *Handle) Path() string {
	if h == nil {
		return ""
	}
	return h.path
}

// Do runs fn against the database, opening it first if it is not already open.
// fn must not call Do again, on this Handle or any other: Do holds both an
// in-process mutex and a file lock for its duration.
func (h *Handle) Do(ctx context.Context, fn func(*sql.DB) error) error {
	if h == nil {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return h.unavail
	}
	h.disarm()
	if h.db != nil && (wanted(h.path) || time.Since(h.since) > h.maxHold) {
		_ = h.shutLocked()
		if err := h.yieldLocked(ctx); err != nil {
			return err
		}
	}
	if h.db == nil {
		if err := h.openLocked(ctx); err != nil {
			return err
		}
	}
	err := asLocked(fn(h.db))
	if walErr := securePerm(h.wal); walErr != nil && err == nil {
		err = walErr
	}
	h.arm()
	return err
}

// yieldLocked stays out of the database long enough for the process this Handle
// just handed off to to take the lock. It is called with h.mu held and returns
// with it held, but drops it while waiting: holding it would stall every other
// caller of this Handle for the whole yield, and they are not who the database
// is being yielded to.
//
// Anything may happen while the mutex is down — another Do can reopen the
// database, the idle timer can close it again, Close can retire the Handle — so
// nothing observed before the gap survives it.
func (h *Handle) yieldLocked(ctx context.Context) error {
	h.mu.Unlock()
	timer := time.NewTimer(yieldToWaiter)
	select {
	case <-ctx.Done():
		timer.Stop()
	case <-timer.C:
	}
	h.mu.Lock()
	if h.closed {
		return h.unavail
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	h.disarm()
	return nil
}

// Ensure opens the database and immediately releases it, so callers that only
// want the file to exist with its schema applied need no operation of their own.
func (h *Handle) Ensure(ctx context.Context) error {
	return h.Do(ctx, func(*sql.DB) error { return nil })
}

// Close releases the database for good. Later calls to Do report the
// unavailable error rather than reopening.
func (h *Handle) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	return h.shutLocked()
}

func (h *Handle) openLocked(ctx context.Context) error {
	if err := acquire(ctx, h.path, h.timeout); err != nil {
		return err
	}
	db, err := Open(ctx, h.path, h.schema)
	if err != nil {
		release(h.path)
		return err
	}
	h.db = db
	h.since = time.Now()
	return nil
}

func (h *Handle) shutLocked() error {
	h.disarm()
	if h.db == nil {
		return nil
	}
	err := h.db.Close()
	h.db = nil
	release(h.path)
	return err
}

// disarm cancels the idle countdown. The timer object is kept for the next arm;
// pending tracks how many firings are still owed a wakeup, because a timer that
// has already expired cannot be called off — its goroutine is on its way to the
// mutex and must be recognised as stale when it arrives.
func (h *Handle) disarm() {
	h.gen++
	if h.timer != nil && h.timer.Stop() {
		h.pending--
	}
}

func (h *Handle) arm() {
	h.armed = h.gen
	if h.timer == nil {
		h.timer = time.AfterFunc(h.idle, h.onIdle)
		h.pending++
		return
	}
	if !h.timer.Reset(h.idle) {
		h.pending++
	}
}

func (h *Handle) onIdle() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pending--
	if h.pending > 0 || h.closed || h.gen != h.armed {
		return
	}
	_ = h.shutLocked()
}

var locks struct {
	mu sync.Mutex
	m  map[string]*lockEntry
}

type lockEntry struct {
	f    *os.File
	refs int
}

func lockKey(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func acquire(ctx context.Context, path string, timeout time.Duration) error {
	key := lockKey(path)

	locks.mu.Lock()
	if e, ok := locks.m[key]; ok {
		e.refs++
		locks.mu.Unlock()
		return nil
	}
	locks.mu.Unlock()

	f, err := flockSidecar(ctx, path, timeout)
	if err != nil {
		return err
	}

	locks.mu.Lock()
	defer locks.mu.Unlock()
	if e, ok := locks.m[key]; ok {
		e.refs++
		_ = unlock(f)
		_ = f.Close()
		return nil
	}
	if locks.m == nil {
		locks.m = map[string]*lockEntry{}
	}
	writePid(f)
	locks.m[key] = &lockEntry{f: f, refs: 1}
	return nil
}

func flockSidecar(ctx context.Context, path string, timeout time.Duration) (*os.File, error) {
	want, err := openSideFile(path, wantSuffix)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = unlock(want)
		_ = want.Close()
	}()
	queued, err := waitLock(ctx, want, timeout)
	if err != nil {
		return nil, err
	}
	if !queued {
		return nil, lockedError{fmt.Errorf("duckdb: timed out queueing for %s (waited %s)",
			filepath.Base(path), timeout)}
	}

	f, err := openSideFile(path, lockSuffix)
	if err != nil {
		return nil, err
	}
	held, err := waitLock(ctx, f, timeout)
	if err != nil || !held {
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		return nil, lockedError{fmt.Errorf("duckdb: %s is locked by another process%s (waited %s)",
			filepath.Base(path), holderSuffix(f.Name()), timeout)}
	}
	return f, nil
}

func writePid(f *os.File) {
	if _, err := f.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err == nil {
		_ = f.Sync()
	}
}

// Lock is a held cross-process lock on a database's sidecar lock file,
// acquired without opening the database itself.
type Lock struct {
	f *os.File
}

// AcquireLock takes the sidecar lock beside the database at path, waiting up
// to timeout for the current holder (whose pid ends up in the error message)
// to let go, and records this process's pid in the lock file. It lets a
// caller such as restore fence off a database file it is about to replace
// without opening it. Release the lock when done.
func AcquireLock(ctx context.Context, path string, timeout time.Duration) (*Lock, error) {
	f, err := flockSidecar(ctx, path, timeout)
	if err != nil {
		return nil, err
	}
	writePid(f)
	return &Lock{f: f}, nil
}

// Release unlocks and closes the lock file. It is safe on a nil *Lock and
// when called more than once.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = unlock(l.f)
	_ = l.f.Close()
	l.f = nil
}

func release(path string) {
	key := lockKey(path)
	locks.mu.Lock()
	defer locks.mu.Unlock()
	e, ok := locks.m[key]
	if !ok {
		return
	}
	e.refs--
	if e.refs > 0 {
		return
	}
	delete(locks.m, key)
	_ = unlock(e.f)
	_ = e.f.Close()
}

const (
	lockSuffix = ".lock"
	wantSuffix = ".wait"
)

// openSideFile opens one of the lock files beside a database, creating it if
// needed.
//
// The explicit 0600 is all the protection these need: a umask can only clear
// permission bits, so a file created with mode 0600 is never wider than that,
// whatever the process umask happens to be. Narrowing the umask here — on a
// path that runs on every operation — bought nothing and mutated global state.
func openSideFile(path, suffix string) (*os.File, error) {
	f, err := os.OpenFile(path+suffix, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func wanted(path string) bool {
	f, err := openSideFile(path, wantSuffix)
	if err != nil {
		return false
	}
	defer f.Close()
	free, err := tryLock(f)
	if err != nil {
		return false
	}
	if free {
		_ = unlock(f)
		return false
	}
	return true
}

func waitLock(ctx context.Context, f *os.File, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		held, err := tryLock(f)
		if err != nil {
			return false, err
		}
		if held {
			return true, nil
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}

func holderSuffix(lockPath string) string {
	b, err := os.ReadFile(lockPath)
	if err != nil {
		return ""
	}
	pid, err := strconv.Atoi(string(trimSpace(b)))
	if err != nil || pid <= 0 {
		return ""
	}
	return " (pid " + strconv.Itoa(pid) + ")"
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}
