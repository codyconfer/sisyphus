package kv

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Get's read of an expired row and its lazy delete of that row must land as one
// step against other callers, so a Put racing the read is never dropped.
// Handle.Do holds its mutex across fn, which is what provides that; this pins
// the outcome so a change to Do's locking cannot quietly take it away.
func TestGetExpiredDoesNotDropConcurrentPut(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	const rounds = 200
	var lost int
	for i := range rounds {
		if err := s.Put(ctx, "ns", "k", "stale", time.Now().Add(-time.Hour)); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, _, err := s.Get(ctx, "ns", "k"); err != nil {
				t.Errorf("Get: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := s.Put(ctx, "ns", "k", "fresh", time.Time{}); err != nil {
				t.Errorf("Put: %v", err)
			}
		}()
		wg.Wait()

		e, ok, err := s.Get(ctx, "ns", "k")
		if err != nil {
			t.Fatalf("read back %d: %v", i, err)
		}
		if !ok || e.Value != "fresh" {
			lost++
		}
	}
	if lost != 0 {
		t.Errorf("committed Put lost in %d of %d rounds", lost, rounds)
	}
}
