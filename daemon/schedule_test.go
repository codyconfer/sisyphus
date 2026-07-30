package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunAtPastFiresImmediately(t *testing.T) {
	ctx := context.Background()
	var n atomic.Int32
	if err := RunAt(ctx, time.Now().Add(-time.Second), func(context.Context) error {
		n.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 {
		t.Fatalf("runs = %d", n.Load())
	}
}

func TestScheduleCatchUpAndCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var n atomic.Int32
	go func() {
		_ = Schedule(ctx, 10*time.Millisecond, ScheduleJob{
			Next: func(context.Context, time.Time) (Due, error) {
				if n.Load() == 0 {
					return Due{Ready: true}, nil
				}
				return Due{At: time.Now().Add(time.Hour)}, nil
			},
			Run: func(context.Context) error {
				n.Add(1)
				cancel()
				return nil
			},
		})
	}()
	deadline := time.After(2 * time.Second)
	for {
		if n.Load() >= 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("schedule did not fire")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestScheduleContinuesAfterTransientErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var nextCalls, runs atomic.Int32
	go func() {
		_ = Schedule(ctx, 5*time.Millisecond, ScheduleJob{
			Next: func(context.Context, time.Time) (Due, error) {
				n := nextCalls.Add(1)
				if n < 3 {
					return Due{}, errors.New("duckdb lock")
				}
				if runs.Load() == 0 {
					return Due{Ready: true}, nil
				}
				return Due{At: time.Now().Add(time.Hour)}, nil
			},
			Run: func(context.Context) error {
				runs.Add(1)
				cancel()
				return nil
			},
		})
	}()
	deadline := time.After(2 * time.Second)
	for {
		if runs.Load() >= 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("schedule aborted on transient Next error (nextCalls=%d)", nextCalls.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestScheduleBacksOffExponentiallyAndReportsErrors(t *testing.T) {
	const interval = 20 * time.Millisecond
	const want = 4
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	boom := errors.New("expired token")
	var mu sync.Mutex
	var gotErrs []error
	var gotFails []int
	var gotDelays []time.Duration
	var nextCalls atomic.Int32

	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		_ = Schedule(ctx, interval, ScheduleJob{
			Name: "failing",
			Next: func(context.Context, time.Time) (Due, error) {
				nextCalls.Add(1)
				return Due{}, boom
			},
			Run: func(context.Context) error { return nil },
			OnError: func(err error, fails int, retryIn time.Duration) {
				mu.Lock()
				gotErrs = append(gotErrs, err)
				gotFails = append(gotFails, fails)
				gotDelays = append(gotDelays, retryIn)
				n := len(gotDelays)
				mu.Unlock()
				if n >= want {
					cancel()
				}
			},
		})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Schedule did not return after cancel")
	}
	elapsed := time.Since(start)

	mu.Lock()
	defer mu.Unlock()
	if len(gotDelays) != want {
		t.Fatalf("OnError calls = %d, want %d", len(gotDelays), want)
	}
	wantDelays := []time.Duration{interval, 2 * interval, 4 * interval, 8 * interval}
	for i, d := range gotDelays {
		if d != wantDelays[i] {
			t.Fatalf("retryIn[%d] = %v, want %v (delays=%v)", i, d, wantDelays[i], gotDelays)
		}
		if gotFails[i] != i+1 {
			t.Fatalf("fails[%d] = %d, want %d", i, gotFails[i], i+1)
		}
		if !errors.Is(gotErrs[i], boom) {
			t.Fatalf("err[%d] = %v, want %v", i, gotErrs[i], boom)
		}
	}
	if n := nextCalls.Load(); n != want {
		t.Fatalf("Next calls = %d, want %d (one per attempt)", n, want)
	}
	if floor := interval + 2*interval + 4*interval; elapsed < floor {
		t.Fatalf("elapsed = %v, want >= %v: backoff was reported but not applied", elapsed, floor)
	}
}

func TestScheduleBackoffCap(t *testing.T) {
	if got := scheduleBackoff(time.Second, 1); got != time.Second {
		t.Fatalf("backoff(1s, 1) = %v, want 1s", got)
	}
	if got := scheduleBackoff(time.Second, 4); got != 8*time.Second {
		t.Fatalf("backoff(1s, 4) = %v, want 8s", got)
	}
	if got := scheduleBackoff(time.Second, 40); got != maxScheduleBackoff {
		t.Fatalf("backoff(1s, 40) = %v, want %v", got, maxScheduleBackoff)
	}
	if got := scheduleBackoff(time.Hour, 5); got != time.Hour {
		t.Fatalf("backoff(1h, 5) = %v, want 1h (never below interval)", got)
	}
}

func TestScheduleFailingJobDoesNotDisturbHealthyJob(t *testing.T) {
	const interval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var failingNext, healthyNext, healthyRuns atomic.Int32
	failing := ScheduleJob{
		Name: "failing",
		Next: func(context.Context, time.Time) (Due, error) {
			failingNext.Add(1)
			return Due{}, errors.New("expired token")
		},
		Run:     func(context.Context) error { return nil },
		OnError: func(error, int, time.Duration) {},
	}
	healthy := ScheduleJob{
		Name: "healthy",
		Next: func(_ context.Context, now time.Time) (Due, error) {
			healthyNext.Add(1)
			return Due{At: now.Add(10 * time.Minute)}, nil
		},
		Run: func(context.Context) error {
			healthyRuns.Add(1)
			return nil
		},
	}

	done := make(chan error, 1)
	go func() { done <- Schedule(ctx, interval, failing, healthy) }()

	timer := time.NewTimer(400 * time.Millisecond)
	<-timer.C
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Schedule err = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Schedule did not return after cancel")
	}

	if n := healthyNext.Load(); n > 2 {
		t.Fatalf("healthy Next calls = %d, want <= 2: a sibling's failure collapsed its schedule to interval", n)
	}
	if n := healthyRuns.Load(); n != 0 {
		t.Fatalf("healthy Run calls = %d, want 0 (due in 10m)", n)
	}
	if n := failingNext.Load(); n < 2 {
		t.Fatalf("failing Next calls = %d, want >= 2 (loop must keep retrying)", n)
	}
	if n := failingNext.Load(); n > 6 {
		t.Fatalf("failing Next calls = %d, want <= 6 over 400ms with backoff", n)
	}
}

func TestScheduleLogsFailuresWithoutHook(t *testing.T) {
	const interval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Schedule(ctx, interval, ScheduleJob{
			Name: "oauth-source",
			Next: func(context.Context, time.Time) (Due, error) {
				if calls.Add(1) >= 2 {
					cancel()
				}
				return Due{}, errors.New("token expired")
			},
			Run: func(context.Context) error { return nil },
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Schedule did not return after cancel")
	}

	out := buf.String()
	for _, want := range []string{"scheduled job failed", "oauth-source", "token expired", "retry_in"} {
		if !strings.Contains(out, want) {
			t.Fatalf("slog output %q missing %q", out, want)
		}
	}
}

func TestScheduleResetsFailureStreakOnSuccess(t *testing.T) {
	const interval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	boom := errors.New("duckdb lock")
	var mu sync.Mutex
	var gotFails []int
	var gotDelays []time.Duration
	var attempt atomic.Int32
	var runs atomic.Int32

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Schedule(ctx, interval, ScheduleJob{
			Name: "flappy",
			Next: func(context.Context, time.Time) (Due, error) {
				switch attempt.Add(1) {
				case 1, 2:
					return Due{}, boom
				case 3:
					return Due{Ready: true}, nil
				default:
					return Due{}, boom
				}
			},
			Run: func(context.Context) error {
				runs.Add(1)
				return nil
			},
			OnError: func(_ error, fails int, retryIn time.Duration) {
				mu.Lock()
				gotFails = append(gotFails, fails)
				gotDelays = append(gotDelays, retryIn)
				n := len(gotFails)
				mu.Unlock()
				if n >= 3 {
					cancel()
				}
			},
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Schedule did not return after cancel")
	}

	mu.Lock()
	defer mu.Unlock()
	if runs.Load() != 1 {
		t.Fatalf("Run calls = %d, want 1", runs.Load())
	}
	wantFails := []int{1, 2, 1}
	if len(gotFails) != len(wantFails) {
		t.Fatalf("fails = %v, want %v", gotFails, wantFails)
	}
	for i, want := range wantFails {
		if gotFails[i] != want {
			t.Fatalf("fails = %v, want %v: a success must clear the streak", gotFails, wantFails)
		}
	}
	if gotDelays[2] != interval {
		t.Fatalf("retryIn after the success = %v, want %v: backoff must restart from interval", gotDelays[2], interval)
	}
}

func TestScheduleReportsThroughInjectedLogger(t *testing.T) {
	const interval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var fallback bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&fallback, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	var mu sync.Mutex
	var host bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&syncWriter{mu: &mu, buf: &host}, &slog.HandlerOptions{Level: slog.LevelWarn}))

	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Schedule(ctx, interval, ScheduleJob{
			Name:   "oauth-source",
			Logger: logger,
			Next: func(context.Context, time.Time) (Due, error) {
				if calls.Add(1) >= 2 {
					cancel()
				}
				return Due{}, errors.New("token expired")
			},
			Run: func(context.Context) error { return nil },
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Schedule did not return after cancel")
	}

	mu.Lock()
	defer mu.Unlock()
	out := host.String()
	for _, want := range []string{"scheduled job failed", "oauth-source", "token expired", "retry_in"} {
		if !strings.Contains(out, want) {
			t.Fatalf("injected logger output %q missing %q", out, want)
		}
	}
	if fallback.Len() != 0 {
		t.Fatalf("slog.Default also got %q: an injected logger must take over reporting", fallback.String())
	}
}

type syncWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func TestScheduleDoesNotReportShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var reports atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Schedule(ctx, 10*time.Millisecond, ScheduleJob{
			Name: "shutting-down",
			Next: func(c context.Context, _ time.Time) (Due, error) {
				cancel()
				return Due{}, c.Err()
			},
			Run: func(context.Context) error { return nil },
			OnError: func(error, int, time.Duration) {
				reports.Add(1)
			},
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Schedule did not return after cancel")
	}
	if n := reports.Load(); n != 0 {
		t.Fatalf("failure reports during shutdown = %d, want 0", n)
	}
}

func TestScheduleReportsJobOwnDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reported := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Schedule(ctx, 10*time.Millisecond, ScheduleJob{
			Name: "slow-source",
			Next: func(parent context.Context, _ time.Time) (Due, error) {
				own, stop := context.WithTimeout(parent, time.Millisecond)
				defer stop()
				<-own.Done()
				return Due{}, own.Err()
			},
			Run: func(context.Context) error { return nil },
			OnError: func(err error, _ int, _ time.Duration) {
				select {
				case reported <- err:
				default:
				}
				cancel()
			},
		})
	}()
	select {
	case err := <-reported:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("reported err = %v, want %v", err, context.DeadlineExceeded)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a job's own timeout must still be reported as a failure")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Schedule did not return after cancel")
	}
}
