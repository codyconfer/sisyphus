package journal

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Concurrent prunes must not both claim the same rows: the totals they return
// have to add up to what the store actually held, not more. Handle.Do holds its
// mutex across fn, so evict's count and its deletes are one step; this pins the
// outcome so a change to Do's locking cannot quietly take it away.
func TestConcurrentPruneCountsWhatItDeleted(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	cutoff := fixtureBase.Add(24 * time.Hour)

	const runs = 12
	for i := range runs {
		addRun(t, s, "old", fixtureBase.Add(time.Duration(i)*time.Minute), 1)
	}

	const pruners = 4
	var wg sync.WaitGroup
	totals := make([]int, pruners)
	for i := range pruners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := s.Prune(ctx, cutoff)
			if err != nil {
				t.Errorf("Prune %d: %v", i, err)
			}
			totals[i] = n
		}()
	}
	wg.Wait()

	var sum int
	for _, n := range totals {
		sum += n
	}
	if sum != runs {
		t.Errorf("prunes reported %d removals in total (%v), want %d", sum, totals, runs)
	}
	if left := tableCount(t, s, "runs"); left != 0 {
		t.Errorf("%d runs left, want 0", left)
	}
}
