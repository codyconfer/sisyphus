package daemon

import (
	"context"
	"sort"
	"testing"
	"time"
)

func TestFanInMergesAndCloses(t *testing.T) {
	ctx := context.Background()
	a := make(chan int, 2)
	b := make(chan int, 2)
	a <- 1
	a <- 2
	b <- 3
	close(a)
	close(b)
	out := FanIn(ctx, a, b)
	var got []int
	for v := range out {
		got = append(got, v)
	}
	sort.Ints(got)
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("FanIn merged = %v, want [1 2 3]", got)
	}
}

func TestPollBaselinesThenEmits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := 0
	ch := Poll(ctx, time.Millisecond, func(context.Context) ([]int, error) {
		n++
		return []int{n}, nil
	})
	first := <-ch
	if len(first.Items) != 1 || first.Items[0] != 1 {
		t.Fatalf("first emission = %+v", first)
	}
}

func TestDeduperFirstIsBaseline(t *testing.T) {
	d := NewDeduper(func(s string) string { return s })
	if got := d.Fresh([]string{"a", "b"}); got != nil {
		t.Fatalf("first Fresh = %v, want nil baseline", got)
	}
	got := d.Fresh([]string{"a", "b", "c"})
	if len(got) != 1 || got[0] != "c" {
		t.Fatalf("second Fresh = %v, want [c]", got)
	}
}

func TestDeduperEvictsOldestOverCap(t *testing.T) {
	d := &Deduper[string]{key: func(s string) string { return s }, keys: map[string]bool{}, max: 2}
	d.Fresh([]string{"a"})
	d.Fresh([]string{"b"})
	d.Fresh([]string{"c"})
	if len(d.keys) != 2 {
		t.Fatalf("cap should bound seen keys to 2, got %d", len(d.keys))
	}
	if d.keys["a"] {
		t.Fatal("oldest key should be evicted past the cap")
	}
	if out := d.Fresh([]string{"a"}); len(out) != 1 || out[0] != "a" {
		t.Fatalf("re-seen evicted key should emit again, got %v", out)
	}
}

func TestPersistentDeduperEvictsFromStore(t *testing.T) {
	kv := newMemKV()
	d := &Deduper[string]{key: func(s string) string { return s }, keys: map[string]bool{}, max: 2, kv: kv, ns: "seen"}
	d.Fresh([]string{"a", "b", "c", "d"})
	stored, _ := kv.List("seen")
	if len(stored) != 2 {
		t.Fatalf("persistent seen set should be bounded to 2, got %d", len(stored))
	}
	if _, ok := stored["a"]; ok {
		t.Fatal("evicted key should be deleted from the store")
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan int)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, events, func(context.Context, int) error { return nil }) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on cancel", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}
