package stream

import (
	"context"
	"testing"
	"time"
)

func TestWatermarkRoundTrip(t *testing.T) {
	kv := newMemKV()
	w := NewWatermark(kv, "sched", "ntr")
	if !w.Load(context.Background()).IsZero() {
		t.Fatal("expected zero watermark")
	}
	at := time.Unix(1_700_000_000, 0).UTC()
	if err := w.Save(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	got := w.Load(context.Background())
	if !got.Equal(at) {
		t.Fatalf("Load = %v, want %v", got, at)
	}
}
