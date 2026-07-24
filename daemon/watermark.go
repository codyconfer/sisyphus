package daemon

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

func NewWatermark(store KV, ns, key string) *Watermark {
	return &Watermark{kv: store, ns: ns, key: key}
}

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

func (w *Watermark) Save(ctx context.Context, t time.Time) error {
	if w == nil || w.kv == nil {
		return nil
	}
	return w.kv.Put(ctx, w.ns, w.key, strconv.FormatInt(t.Unix(), 10), time.Time{})
}
