package configdb

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/codyconfer/sisyphus/config"
)

// Concurrent imports must each archive their own predecessor exactly once, or
// the history chain loses a link. Handle.Do holds its mutex across fn, so
// Import's read of the current snapshot and the transaction that archives it are
// one step; this pins the outcome so a change to Do's locking cannot quietly
// take it away.
func TestConcurrentImportArchivesEveryPredecessor(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	const writers = 6
	if err := s.Import(ctx, "doc", []byte("v0"), config.FormatJSON); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Import(ctx, "doc", []byte("v"+strconv.Itoa(i+1)), config.FormatJSON); err != nil {
				t.Errorf("import %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	hist, err := s.History(ctx, "doc", 100)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	cur, ok, err := s.Current(ctx, "doc")
	if err != nil || !ok {
		t.Fatalf("Current = ok %v, err %v", ok, err)
	}

	// Every write but the last one standing is archived, each exactly once.
	if len(hist) != writers {
		t.Errorf("history has %d snapshots, want %d", len(hist), writers)
	}
	seen := map[string]int{cur.Content: 1}
	for _, v := range hist {
		seen[v.Content]++
	}
	for i := range writers + 1 {
		v := "v" + strconv.Itoa(i)
		if seen[v] != 1 {
			t.Errorf("%s appears %d times across history+current, want 1", v, seen[v])
		}
	}
}
