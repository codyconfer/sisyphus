package daemon

import (
	"context"
	"errors"
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
