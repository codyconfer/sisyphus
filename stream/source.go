package stream

import (
	"context"
	"time"
)

// Source composes the standard poll pipeline every consumer otherwise
// rebuilds by hand: a step polled at Interval, optionally bounded by a
// per-step timeout, with successful emissions filtered through an
// (optionally persistent) Deduper.
type Source[T any] struct {
	Interval    time.Duration
	StepTimeout time.Duration                          // 0 = no per-step timeout
	Step        func(ctx context.Context) ([]T, error) // required unless StepAdaptive is set
	// StepAdaptive, when set, takes precedence over Step and drives the loop
	// via PollAdaptive: the step's returned duration reschedules the next poll.
	StepAdaptive func(ctx context.Context) ([]T, time.Duration, error)
	Key          func(T) string // non-nil enables dedupe
	KV           KV             // optional; nil = in-memory dedupe
	Namespace    string         // KV namespace for persistent dedupe
}

// Run starts the pipeline and returns its emission stream. The channel
// closes when ctx is done. Errors pass through unchanged as Emission{Err: …};
// when Key is non-nil, item emissions are reduced to fresh items only and
// emissions left empty by dedupe are dropped.
func (s Source[T]) Run(ctx context.Context) <-chan Emission[T] {
	em := s.poll(ctx)
	if s.Key == nil {
		return em
	}
	out := make(chan Emission[T])
	go func() {
		defer close(out)
		var d *Deduper[T]
		for e := range em {
			if e.Err == nil {
				if d == nil {
					d = s.deduper(ctx)
				}
				e.Items = d.Unseen(ctx, e.Items)
				if len(e.Items) == 0 {
					continue
				}
			}
			select {
			case out <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func (s Source[T]) deduper(ctx context.Context) *Deduper[T] {
	if s.KV != nil {
		return NewPersistentDeduper(ctx, s.Key, s.KV, s.Namespace)
	}
	return NewDeduper(s.Key)
}

func (s Source[T]) poll(ctx context.Context) <-chan Emission[T] {
	if s.StepAdaptive != nil {
		step := s.StepAdaptive
		if timeout := s.StepTimeout; timeout > 0 {
			inner := step
			step = func(ctx context.Context) ([]T, time.Duration, error) {
				sctx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				return inner(sctx)
			}
		}
		return PollAdaptive(ctx, s.Interval, step)
	}
	step := s.Step
	if timeout := s.StepTimeout; timeout > 0 {
		inner := step
		step = func(ctx context.Context) ([]T, error) {
			sctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return inner(sctx)
		}
	}
	return Poll(ctx, s.Interval, step)
}
