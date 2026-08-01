package journal

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/codyconfer/sisyphus/duckopt"
)

const benchHoldBudget = time.Hour

var benchBase = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func benchRecords(n int) []Record {
	recs := make([]Record, n)
	for i := range recs {
		id := strconv.Itoa(i)
		recs[i] = Record{
			Ts: benchBase.Add(time.Duration(i) * time.Second),
			Attrs: map[string]string{
				"signal":   "github",
				"kind":     "pull_request",
				"title":    "Fix the flaky integration test in the audit path #" + id,
				"subtitle": "codyconfer/munin - opened by codyconfer",
				"url":      "https://github.com/codyconfer/munin/pull/" + id,
			},
		}
	}
	return recs
}

func benchRun(count int) Run {
	return Run{
		Kind:     "query",
		Name:     "review-queue",
		Started:  benchBase,
		Finished: benchBase.Add(time.Second),
		Count:    count,
		Attrs:    map[string]string{"role": "reviewer"},
	}
}

func openBenchStore(b *testing.B, dir string) *Store {
	b.Helper()
	s, err := Open(context.Background(), filepath.Join(dir, "journal.duckdb"), duckopt.WithMaxHold(benchHoldBudget))
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	return s
}

func BenchmarkAddWarmHandle(b *testing.B) {
	for _, n := range []int{10, 200, 1000} {
		b.Run(fmt.Sprintf("records=%d", n), func(b *testing.B) {
			ctx := context.Background()
			dir := b.TempDir()
			recs := benchRecords(n)
			run := benchRun(n)
			s := openBenchStore(b, dir)
			defer func() { _ = s.Close() }()
			heldSince := time.Now()
			var lastID int64
			b.ReportAllocs()
			for b.Loop() {
				if time.Since(heldSince) > benchHoldBudget {
					b.StopTimer()
					if err := s.Close(); err != nil {
						b.Fatalf("Close: %v", err)
					}
					s = openBenchStore(b, dir)
					heldSince = time.Now()
					b.StartTimer()
				}
				id, err := s.Add(ctx, run, recs)
				if err != nil {
					b.Fatalf("Add: %v", err)
				}
				lastID = id
			}
			if lastID == 0 {
				b.Fatal("Add returned no run id")
			}
		})
	}
}

func BenchmarkAddColdOpen(b *testing.B) {
	for _, n := range []int{10, 200} {
		b.Run(fmt.Sprintf("records=%d", n), func(b *testing.B) {
			ctx := context.Background()
			dir := b.TempDir()
			recs := benchRecords(n)
			run := benchRun(n)
			i := 0
			var lastID int64
			b.ReportAllocs()
			for b.Loop() {
				path := filepath.Join(dir, "journal-"+strconv.Itoa(i)+".duckdb")
				i++
				s, err := Open(ctx, path)
				if err != nil {
					b.Fatalf("Open: %v", err)
				}
				id, err := s.Add(ctx, run, recs)
				if err != nil {
					b.Fatalf("Add: %v", err)
				}
				if err := s.Close(); err != nil {
					b.Fatalf("Close: %v", err)
				}
				lastID = id
			}
			if lastID == 0 {
				b.Fatal("Add returned no run id")
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
		path := filepath.Join(dir, "journal-"+strconv.Itoa(i)+".duckdb")
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
