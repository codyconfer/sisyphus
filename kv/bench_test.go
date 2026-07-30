package kv

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const benchHoldBudget = 6 * time.Second

const benchValue = `{"kind":"pull_request","title":"Fix the flaky integration test in the audit path",` +
	`"repo":"codyconfer/munin","author":"codyconfer","url":"https://github.com/codyconfer/munin/pull/1234"}`

type benchHold struct {
	b   *testing.B
	dir string
	s   *Store
	at  time.Time
}

func newBenchHold(b *testing.B) *benchHold {
	b.Helper()
	h := &benchHold{b: b, dir: b.TempDir()}
	h.open()
	return h
}

func (h *benchHold) open() {
	h.b.Helper()
	s, err := Open(context.Background(), filepath.Join(h.dir, "kv.duckdb"))
	if err != nil {
		h.b.Fatalf("Open: %v", err)
	}
	h.s, h.at = s, time.Now()
}

func (h *benchHold) reopen() {
	h.b.Helper()
	if err := h.s.Close(); err != nil {
		h.b.Fatalf("Close: %v", err)
	}
	h.open()
}

func (h *benchHold) recycleIfStale() {
	if time.Since(h.at) <= benchHoldBudget {
		return
	}
	h.b.StopTimer()
	h.reopen()
	h.b.StartTimer()
}

func (h *benchHold) close() {
	if err := h.s.Close(); err != nil {
		h.b.Fatalf("Close: %v", err)
	}
}

func (h *benchHold) fill(ns string, n int) {
	h.b.Helper()
	ctx := context.Background()
	for i := range n {
		if err := h.s.Put(ctx, ns, "key-"+strconv.Itoa(i), benchValue, time.Time{}); err != nil {
			h.b.Fatalf("Put: %v", err)
		}
	}
}

func BenchmarkPutWarmHandle(b *testing.B) {
	ctx := context.Background()
	h := newBenchHold(b)
	defer h.close()
	i := 0
	b.ReportAllocs()
	for b.Loop() {
		h.recycleIfStale()
		if err := h.s.Put(ctx, "bench", "key-"+strconv.Itoa(i), benchValue, time.Time{}); err != nil {
			b.Fatalf("Put: %v", err)
		}
		i++
	}
}

func BenchmarkPutOverwriteWarmHandle(b *testing.B) {
	ctx := context.Background()
	h := newBenchHold(b)
	defer h.close()
	h.fill("bench", 1)
	h.reopen()
	b.ReportAllocs()
	for b.Loop() {
		h.recycleIfStale()
		if err := h.s.Put(ctx, "bench", "key-0", benchValue, time.Time{}); err != nil {
			b.Fatalf("Put: %v", err)
		}
	}
}

func BenchmarkGetWarmHandle(b *testing.B) {
	ctx := context.Background()
	const entries = 100
	h := newBenchHold(b)
	defer h.close()
	h.fill("bench", entries)
	h.reopen()
	i := 0
	b.ReportAllocs()
	for b.Loop() {
		h.recycleIfStale()
		key := "key-" + strconv.Itoa(i%entries)
		i++
		e, ok, err := h.s.Get(ctx, "bench", key)
		if err != nil || !ok || e.Value != benchValue {
			b.Fatalf("Get(%s) = %q ok=%v err=%v", key, e.Value, ok, err)
		}
	}
}

func BenchmarkGetMissWarmHandle(b *testing.B) {
	ctx := context.Background()
	h := newBenchHold(b)
	defer h.close()
	h.fill("bench", 100)
	h.reopen()
	b.ReportAllocs()
	for b.Loop() {
		h.recycleIfStale()
		e, ok, err := h.s.Get(ctx, "bench", "absent")
		if err != nil || ok || e.Value != "" {
			b.Fatalf("Get(absent) = %q ok=%v err=%v", e.Value, ok, err)
		}
	}
}

func BenchmarkListWarmHandle(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			ctx := context.Background()
			h := newBenchHold(b)
			defer h.close()
			h.fill("bench", n)
			h.reopen()
			b.ReportAllocs()
			for b.Loop() {
				h.recycleIfStale()
				got, err := h.s.List(ctx, "bench")
				if err != nil || len(got) != n || got["key-0"].Value != benchValue {
					b.Fatalf("List = %d entries, %v, want %d", len(got), err, n)
				}
			}
		})
	}
}

func BenchmarkOpenClose(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()
	i := 0
	b.ReportAllocs()
	for b.Loop() {
		path := filepath.Join(dir, "kv-"+strconv.Itoa(i)+".duckdb")
		i++
		s, err := Open(ctx, path)
		if err != nil {
			b.Fatalf("Open: %v", err)
		}
		if err := s.Close(); err != nil {
			b.Fatalf("Close: %v", err)
		}
	}
}
