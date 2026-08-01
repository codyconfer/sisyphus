package daemon

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func Context(parent context.Context) (context.Context, func()) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

func FanIn[T any](ctx context.Context, chans ...<-chan T) <-chan T {
	out := make(chan T)
	var wg sync.WaitGroup
	for _, ch := range chans {
		wg.Add(1)
		go func(c <-chan T) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case v, ok := <-c:
					if !ok {
						return
					}
					select {
					case out <- v:
					case <-ctx.Done():
						return
					}
				}
			}
		}(ch)
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

type Emission[T any] struct {
	Items []T
	Err   error
}

func Poll[T any](ctx context.Context, interval time.Duration, step func(ctx context.Context) ([]T, error)) <-chan Emission[T] {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	out := make(chan Emission[T])
	go func() {
		defer close(out)
		send := func(e Emission[T]) bool {
			select {
			case out <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		tick := func() bool {
			items, err := step(ctx)
			if err != nil {
				return send(Emission[T]{Err: err})
			}
			if len(items) == 0 {
				return true
			}
			return send(Emission[T]{Items: items})
		}
		if !tick() {
			return
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !tick() {
					return
				}
			}
		}
	}()
	return out
}

const defaultSeenCap = 8192

type Deduper[T any] struct {
	key   func(T) string
	keys  map[string]bool
	order []string
	max   int
	first bool
	kv    KV
	ns    string
}

func NewDeduper[T any](key func(T) string) *Deduper[T] {
	return &Deduper[T]{key: key, keys: map[string]bool{}, max: defaultSeenCap, first: true}
}

func (d *Deduper[T]) Fresh(ctx context.Context, items []T) []T {
	var out []T
	for _, it := range items {
		k := d.key(it)
		if d.keys[k] {
			continue
		}
		d.keys[k] = true
		d.order = append(d.order, k)
		if d.kv != nil {
			_ = d.kv.Put(ctx, d.ns, k, "1", time.Time{})
		}
		if !d.first {
			out = append(out, it)
		}
		d.evict(ctx)
	}
	if d.first {
		d.first = false
		if d.kv != nil {
			_ = d.kv.Put(ctx, d.ns, seenInitKey, "1", time.Time{})
		}
	}
	return out
}

func (d *Deduper[T]) evict(ctx context.Context) {
	if d.max <= 0 {
		return
	}
	for len(d.order) > d.max {
		oldest := d.order[0]
		d.order = d.order[1:]
		delete(d.keys, oldest)
		if d.kv != nil {
			_ = d.kv.Delete(ctx, d.ns, oldest)
		}
	}
}

func Run[T any](ctx context.Context, events <-chan T, handle func(ctx context.Context, ev T) error) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if err := handle(ctx, ev); err != nil {
				return err
			}
		}
	}
}
