package daemon

import (
	"context"
	"time"

	"github.com/codyconfer/sisyphus/kv"
)

// KV is the Entry-valued persistence surface used by Cursor and Deduper.
// *kv.Store satisfies this interface.
type KV interface {
	Get(ctx context.Context, namespace, key string) (kv.Entry, bool, error)
	Put(ctx context.Context, namespace, key, value string, expiry time.Time) error
	Delete(ctx context.Context, namespace, key string) error
	List(ctx context.Context, namespace string) (map[string]kv.Entry, error)
}

type Cursor struct {
	kv  KV
	ns  string
	key string
}

func NewCursor(store KV, ns, key string) *Cursor {
	return &Cursor{kv: store, ns: ns, key: key}
}

func (c *Cursor) Load(ctx context.Context) string {
	if c == nil || c.kv == nil {
		return ""
	}
	e, _, err := c.kv.Get(ctx, c.ns, c.key)
	if err != nil {
		return ""
	}
	return e.Value
}

func (c *Cursor) Save(ctx context.Context, value string) error {
	if c == nil || c.kv == nil {
		return nil
	}
	return c.kv.Put(ctx, c.ns, c.key, value, time.Time{})
}

func (c *Cursor) Clear(ctx context.Context) error {
	if c == nil || c.kv == nil {
		return nil
	}
	return c.kv.Delete(ctx, c.ns, c.key)
}

func NewPersistentDeduper[T any](key func(T) string, store KV, namespace string) *Deduper[T] {
	d := &Deduper[T]{key: key, keys: map[string]bool{}, max: defaultSeenCap, first: true, kv: store, ns: namespace}
	if store != nil {
		if existing, err := store.List(context.Background(), namespace); err == nil && len(existing) > 0 {
			for k := range existing {
				d.keys[k] = true
				d.order = append(d.order, k)
			}
			d.first = false
			d.evict()
		}
	}
	return d
}

func PollAdaptive[T any](ctx context.Context, interval time.Duration, step func(ctx context.Context) ([]T, time.Duration, error)) <-chan Emission[T] {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	out := make(chan Emission[T])
	go func() {
		defer close(out)
		cur := interval
		send := func(e Emission[T]) bool {
			select {
			case out <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		tick := func() bool {
			items, next, err := step(ctx)
			if next > 0 {
				cur = next
			}
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
		t := time.NewTimer(cur)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !tick() {
					return
				}
				t.Reset(cur)
			}
		}
	}()
	return out
}
