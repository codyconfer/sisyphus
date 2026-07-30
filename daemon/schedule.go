package daemon

import (
	"context"
	"log/slog"
	"time"
)

const maxScheduleBackoff = 5 * time.Minute

const maxScheduleFails = 64

type Due struct {
	At    time.Time
	Ready bool
}

type ScheduleJob struct {
	Name    string
	Next    func(ctx context.Context, now time.Time) (Due, error)
	Run     func(ctx context.Context) error
	OnError func(err error, fails int, retryIn time.Duration)
	Logger  *slog.Logger
}

type scheduleState struct {
	fails int
	at    time.Time
}

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
				if ctx.Err() != nil {
					return nil
				}
				note(st.fail(job, interval, now, err))
				continue
			}
			if due.Ready || (!due.At.IsZero() && !due.At.After(now)) {
				if err := job.Run(ctx); err != nil {
					if ctx.Err() != nil {
						return nil
					}
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

func (st *scheduleState) fail(job ScheduleJob, interval time.Duration, now time.Time, err error) time.Time {
	if st.fails < maxScheduleFails {
		st.fails++
	}
	d := scheduleBackoff(interval, st.fails)
	st.at = now.Add(d)
	reportScheduleError(job, err, st.fails, d)
	return st.at
}

func (st *scheduleState) succeed(at time.Time) {
	st.fails = 0
	st.at = at
}

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
	logger := job.Logger
	if logger == nil {
		logger = slog.Default()
	}
	name := job.Name
	if name == "" {
		name = "job"
	}
	logger.Warn("scheduled job failed",
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
