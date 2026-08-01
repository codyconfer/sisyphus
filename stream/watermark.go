package stream

import (
	"context"
	"strconv"
	"time"
)

// Watermark is a kv-backed last-run stamp for Scheduled catch-up.
type Watermark struct {
	kv  KV
	ns  string
	key string
}

// NewWatermark returns a Watermark stored under ns/key in store. A nil store
// gives a no-op watermark: Load returns the zero time and Save returns nil.
func NewWatermark(store KV, ns, key string) *Watermark {
	return &Watermark{kv: store, ns: ns, key: key}
}

// Load returns the saved stamp at second precision. Like Cursor.Load it has
// no error result: a read failure, a missing entry, and an unparsable value
// all come back as the zero time, so schedulers fall back to "never ran".
func (w *Watermark) Load(ctx context.Context) time.Time {
	if w == nil || w.kv == nil {
		return time.Time{}
	}
	e, ok, err := w.kv.Get(ctx, w.ns, w.key)
	if err != nil || !ok || e.Value == "" {
		return time.Time{}
	}
	unix, err := strconv.ParseInt(e.Value, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(unix, 0)
}

// Save stores t as the new stamp, truncated to whole seconds (it is kept as
// a Unix timestamp).
func (w *Watermark) Save(ctx context.Context, t time.Time) error {
	if w == nil || w.kv == nil {
		return nil
	}
	return w.kv.Put(ctx, w.ns, w.key, strconv.FormatInt(t.Unix(), 10), time.Time{})
}
