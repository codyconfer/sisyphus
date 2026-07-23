package daemon

import (
	"context"
	"time"
)

type KV interface {
	Get(namespace, key string) (string, bool, error)
	Set(namespace, key, value string) error
	Delete(namespace, key string) error
	List(namespace string) (map[string]string, error)
}

type Cursor struct {
	kv  KV
	ns  string
	key string
}

func NewCursor(kv KV, ns, key string) *Cursor {
	return &Cursor{kv: kv, ns: ns, key: key}
}

func (c *Cursor) Load() string {
	if c == nil || c.kv == nil {
		return ""
	}
	v, _, err := c.kv.Get(c.ns, c.key)
	if err != nil {
		return ""
	}
	return v
}

func (c *Cursor) Save(value string) error {
	if c == nil || c.kv == nil {
		return nil
	}
	return c.kv.Set(c.ns, c.key, value)
}

func (c *Cursor) Clear() error {
	if c == nil || c.kv == nil {
		return nil
	}
	return c.kv.Delete(c.ns, c.key)
}

func NewPersistentDeduper[T any](key func(T) string, kv KV, namespace string) *Deduper[T] {
	d := &Deduper[T]{key: key, keys: map[string]bool{}, max: defaultSeenCap, first: true, kv: kv, ns: namespace}
	if kv != nil {
		if existing, err := kv.List(namespace); err == nil && len(existing) > 0 {
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
