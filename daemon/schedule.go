package daemon

import (
	"context"
	"log/slog"
	"time"
)

// maxScheduleBackoff caps the per-job exponential retry backoff. A job that
// keeps failing settles at one attempt per cap instead of one per interval.
const maxScheduleBackoff = 5 * time.Minute

// maxScheduleFails bounds the stored failure count so the doubling in
// scheduleBackoff can never overflow.
const maxScheduleFails = 64

// Due describes the next scheduled fire for a job. When Due is zero and Ready
// is false, the scanner waits for Interval (or a default) before asking again.
type Due struct {
	At    time.Time
	Ready bool
}

// ScheduleJob is a persistence-free scheduled unit. Callers own watermarks
// (typically via kv) and implement Next/Run accordingly.
type ScheduleJob struct {
	// Name labels the job in failure reports. Optional.
	Name string
	// Next reports when work is due. Ready means run immediately (catch-up).
	Next func(ctx context.Context, now time.Time) (Due, error)
	// Run executes the due work.
	Run func(ctx context.Context) error
	// OnError observes a failed Next or Run: err is the failure, fails is the
	// number of consecutive failures for this job (1 for the first) and retryIn
	// is how long Schedule waits before retrying this job. When nil, failures
	// are reported through slog.Default at warn level, so a permanently failing
	// job is never silent.
	OnError func(err error, fails int, retryIn time.Duration)
}

// scheduleState is Schedule's per-job bookkeeping: at is the earliest time the
// job may be evaluated again, fails the consecutive Next/Run failure count.
type scheduleState struct {
	fails int
	at    time.Time
}

// Schedule repeatedly scans jobs for due work until ctx is canceled.
// interval is the idle poll when no job reports a future At.
//
// Transient Next/Run errors (e.g. DuckDB lock from a concurrent CLI) do not
// abort the loop. The failing job is retried with exponential backoff —
// interval, 2×interval, 4×interval … capped at maxScheduleBackoff — reset on
// its first success, and every failure is reported through ScheduleJob.OnError
// (or slog when that is nil). Backoff and failures are tracked per job: one
// permanently failing job never changes another job's timing, and a job is not
// asked for Next again before its own next check time. Only ctx cancel ends the
// scanner (returns nil).
func Schedule(ctx context.Context, interval time.Duration, jobs ...ScheduleJob) error {
	if interval <= 0 {
		interval = time.Second
	}
	nowFn := time.Now
	states := make([]scheduleState, len(jobs))
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		now := nowFn()
		var soonest time.Time
		pollSoon := false
		note := func(at time.Time) {
			if at.After(now) {
				soonest = earlier(soonest, at)
				return
			}
			pollSoon = true
		}
		for i := range jobs {
			job := jobs[i]
			if job.Next == nil || job.Run == nil {
				continue
			}
			st := &states[i]
			if !st.at.IsZero() && st.at.After(now) {
				note(st.at)
				continue
			}
			due, err := job.Next(ctx, now)
			if err != nil {
				note(st.fail(job, interval, now, err))
				continue
			}
			if due.Ready || (!due.At.IsZero() && !due.At.After(now)) {
				if err := job.Run(ctx); err != nil {
					note(st.fail(job, interval, now, err))
					continue
				}
				st.succeed(now)
				note(st.at)
				continue
			}
			if due.At.IsZero() {
				st.succeed(now.Add(interval))
			} else {
				st.succeed(due.At)
			}
			note(st.at)
		}
		wait := interval
		if !soonest.IsZero() {
			if d := time.Until(soonest); d > 0 && (!pollSoon || d < wait) {
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

// fail records a failure, schedules the retry with backoff, reports it and
// returns the retry time.
func (st *scheduleState) fail(job ScheduleJob, interval time.Duration, now time.Time, err error) time.Time {
	if st.fails < maxScheduleFails {
		st.fails++
	}
	d := scheduleBackoff(interval, st.fails)
	st.at = now.Add(d)
	reportScheduleError(job, err, st.fails, d)
	return st.at
}

// succeed clears the failure streak and sets the job's next check time.
func (st *scheduleState) succeed(at time.Time) {
	st.fails = 0
	st.at = at
}

// scheduleBackoff returns interval doubled once per consecutive failure past
// the first, capped at maxScheduleBackoff (never below interval).
func scheduleBackoff(interval time.Duration, fails int) time.Duration {
	if interval <= 0 {
		interval = time.Second
	}
	d := interval
	for i := 1; i < fails && d < maxScheduleBackoff; i++ {
		d *= 2
	}
	if d > maxScheduleBackoff && interval < maxScheduleBackoff {
		d = maxScheduleBackoff
	}
	return d
}

func reportScheduleError(job ScheduleJob, err error, fails int, retryIn time.Duration) {
	if job.OnError != nil {
		job.OnError(err, fails, retryIn)
		return
	}
	name := job.Name
	if name == "" {
		name = "job"
	}
	slog.Warn("scheduled job failed",
		"job", name,
		"consecutive_failures", fails,
		"retry_in", retryIn.String(),
		"err", err)
}

func earlier(a, b time.Time) time.Time {
	if b.IsZero() {
		return a
	}
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
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
