package daemon

import (
	"context"
	"time"
)

// Due describes the next scheduled fire for a job. When Due is zero and Ready
// is false, the scanner waits for Interval (or a default) before asking again.
type Due struct {
	At    time.Time
	Ready bool
}

// ScheduleJob is a persistence-free scheduled unit. Callers own watermarks
// (typically via kv) and implement Next/Run accordingly.
type ScheduleJob struct {
	// Next reports when work is due. Ready means run immediately (catch-up).
	Next func(ctx context.Context, now time.Time) (Due, error)
	// Run executes the due work.
	Run func(ctx context.Context) error
}

// Schedule repeatedly scans jobs for due work until ctx is canceled.
// interval is the idle poll when no job reports a future At.
//
// Transient Next/Run errors (e.g. DuckDB lock from a concurrent CLI) do not
// abort the loop — Schedule backs off for interval and continues. Only ctx
// cancel ends the scanner (returns nil).
func Schedule(ctx context.Context, interval time.Duration, jobs ...ScheduleJob) error {
	if interval <= 0 {
		interval = time.Second
	}
	nowFn := time.Now
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		now := nowFn()
		var soonest time.Time
		hadErr := false
		for _, job := range jobs {
			if job.Next == nil || job.Run == nil {
				continue
			}
			due, err := job.Next(ctx, now)
			if err != nil {
				hadErr = true
				continue
			}
			if due.Ready || (!due.At.IsZero() && !due.At.After(now)) {
				if err := job.Run(ctx); err != nil {
					hadErr = true
					continue
				}
				continue
			}
			if due.At.IsZero() {
				continue
			}
			if soonest.IsZero() || due.At.Before(soonest) {
				soonest = due.At
			}
		}
		wait := interval
		if !hadErr && !soonest.IsZero() {
			if d := time.Until(soonest); d > 0 {
				wait = d
			}
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
}

// RunAt sleeps until at (or returns immediately if at is in the past), then
// invokes fn. Honors ctx cancel.
func RunAt(ctx context.Context, at time.Time, fn func(ctx context.Context) error) error {
	if fn == nil {
		return nil
	}
	if d := time.Until(at); d > 0 {
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return nil
	}
	return fn(ctx)
}
