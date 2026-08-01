package stream

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

// Cursor is a single kv-backed string position (a "resume from here" marker)
// under one namespace/key. A nil *Cursor, or one built over a nil store, is a
// valid no-op: Load returns "" and Save and Clear return nil.
type Cursor struct {
	kv  KV
	ns  string
	key string
}

// NewCursor returns a Cursor stored under ns/key in store. A nil store gives
// a no-op cursor.
func NewCursor(store KV, ns, key string) *Cursor {
	return &Cursor{kv: store, ns: ns, key: key}
}

// Load returns the saved position. It deliberately has no error result: a
// read failure is swallowed and reported as "", the same as no saved
// position, so a broken store degrades to starting over rather than failing.
// Callers that must tell the two apart need to query the store directly.
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

// Save stores value as the new position, without expiry.
func (c *Cursor) Save(ctx context.Context, value string) error {
	if c == nil || c.kv == nil {
		return nil
	}
	return c.kv.Put(ctx, c.ns, c.key, value, time.Time{})
}

// Clear deletes the saved position, so the next Load returns "".
func (c *Cursor) Clear(ctx context.Context) error {
	if c == nil || c.kv == nil {
		return nil
	}
	return c.kv.Delete(ctx, c.ns, c.key)
}

// seenInitKey marks a namespace whose deduper has completed its first pass,
// so an empty restore is distinguishable from a fresh start. The legacy
// "sisyphus:"-prefixed spelling is still read so existing state migrates
// without a one-time re-notification.
const (
	seenInitKey       = "_deduper:init"
	legacySeenInitKey = "sisyphus:deduper:init"
)

// NewPersistentDeduper returns a Deduper that mirrors its seen keys into
// store under namespace, so restarts do not re-announce old items. Existing
// keys are loaded up front; if any state (or the first-pass marker) is found
// the deduper skips the baseline first pass and Unseen returns new items
// immediately. A failure to list the namespace is swallowed and the deduper
// starts fresh, as does a nil store (leaving a purely in-memory deduper).
func NewPersistentDeduper[T any](ctx context.Context, key func(T) string, store KV, namespace string) *Deduper[T] {
	d := &Deduper[T]{key: key, keys: map[string]bool{}, max: defaultSeenCap, first: true, kv: store, ns: namespace}
	if store != nil {
		if existing, err := store.List(ctx, namespace); err == nil {
			for _, marker := range []string{seenInitKey, legacySeenInitKey} {
				if _, ok := existing[marker]; ok {
					d.first = false
					delete(existing, marker)
				}
			}
			if len(existing) > 0 {
				d.first = false
			}
			for k := range existing {
				d.keys[k] = true
				d.order = append(d.order, k)
			}
			d.evict(ctx)
		}
	}
	return d
}

// PollAdaptive is Poll with a step-controlled cadence: each step may return
// the delay until the next step (a value <= 0 keeps the current one, which
// starts at interval, or 60s when interval <= 0). Error and item semantics
// match Poll: errors are emitted in-band and polling continues, and the
// returned delay is honored even when the step also errored.
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
