package daemon

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/codyconfer/sisyphus/kv"
)

type memKV struct {
	data map[string]map[string]kv.Entry
}

func newMemKV() *memKV { return &memKV{data: map[string]map[string]kv.Entry{}} }

func (m *memKV) Get(_ context.Context, ns, key string) (kv.Entry, bool, error) {
	e, ok := m.data[ns][key]
	return e, ok, nil
}

func (m *memKV) Put(_ context.Context, ns, key, value string, expiry time.Time) error {
	if m.data[ns] == nil {
		m.data[ns] = map[string]kv.Entry{}
	}
	m.data[ns][key] = kv.Entry{Value: value, Expiry: expiry}
	return nil
}

func (m *memKV) Delete(_ context.Context, ns, key string) error {
	delete(m.data[ns], key)
	return nil
}

func (m *memKV) List(_ context.Context, ns string) (map[string]kv.Entry, error) {
	out := map[string]kv.Entry{}
	maps.Copy(out, m.data[ns])
	return out, nil
}

func TestCursorRoundTrip(t *testing.T) {
	store := newMemKV()
	c := NewCursor(store, "github", "last_modified")
	if got := c.Load(context.Background()); got != "" {
		t.Fatalf("empty cursor should load empty, got %q", got)
	}
	if err := c.Save(context.Background(), "Wed, 22 Jul 2026"); err != nil {
		t.Fatal(err)
	}
	if got := NewCursor(store, "github", "last_modified").Load(context.Background()); got != "Wed, 22 Jul 2026" {
		t.Fatalf("cursor did not persist, got %q", got)
	}
	if err := c.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.Load(context.Background()); got != "" {
		t.Fatalf("cleared cursor should load empty, got %q", got)
	}
}

func TestNilCursorSafe(t *testing.T) {
	var c *Cursor
	if got := c.Load(context.Background()); got != "" {
		t.Fatalf("nil cursor Load should be empty, got %q", got)
	}
	if err := c.Save(context.Background(), "x"); err != nil {
		t.Fatalf("nil cursor Save should no-op, got %v", err)
	}
}

func TestPersistentDeduperSurvivesRestart(t *testing.T) {
	store := newMemKV()
	id := func(s string) string { return s }

	first := NewPersistentDeduper(id, store, "seen")
	if out := first.Fresh([]string{"a", "b"}); len(out) != 0 {
		t.Fatalf("first run should baseline (emit nothing), got %v", out)
	}

	restarted := NewPersistentDeduper(id, store, "seen")
	out := restarted.Fresh([]string{"a", "b", "c"})
	if len(out) != 1 || out[0] != "c" {
		t.Fatalf("after restart only new items should emit, got %v", out)
	}
}

func TestPollAdaptiveUsesReturnedInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	calls := 0
	ch := PollAdaptive(ctx, time.Hour, func(context.Context) ([]int, time.Duration, error) {
		calls++
		if calls >= 2 {
			return []int{calls}, time.Millisecond, nil
		}
		return []int{calls}, time.Millisecond, nil
	})

	got := 0
	timeout := time.After(2 * time.Second)
	for got < 2 {
		select {
		case e := <-ch:
			if e.Err != nil {
				t.Fatal(e.Err)
			}
			got += len(e.Items)
		case <-timeout:
			t.Fatalf("adaptive poll did not honor short interval; got %d emissions", got)
		}
	}
}
