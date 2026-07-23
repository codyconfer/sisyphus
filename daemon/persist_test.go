package daemon

import (
	"context"
	"maps"
	"testing"
	"time"
)

type memKV struct {
	data map[string]map[string]string
}

func newMemKV() *memKV { return &memKV{data: map[string]map[string]string{}} }

func (m *memKV) Get(ns, key string) (string, bool, error) {
	v, ok := m.data[ns][key]
	return v, ok, nil
}

func (m *memKV) Set(ns, key, value string) error {
	if m.data[ns] == nil {
		m.data[ns] = map[string]string{}
	}
	m.data[ns][key] = value
	return nil
}

func (m *memKV) Delete(ns, key string) error {
	delete(m.data[ns], key)
	return nil
}

func (m *memKV) List(ns string) (map[string]string, error) {
	out := map[string]string{}
	maps.Copy(out, m.data[ns])
	return out, nil
}

func TestCursorRoundTrip(t *testing.T) {
	kv := newMemKV()
	c := NewCursor(kv, "github", "last_modified")
	if got := c.Load(); got != "" {
		t.Fatalf("empty cursor should load empty, got %q", got)
	}
	if err := c.Save("Wed, 22 Jul 2026"); err != nil {
		t.Fatal(err)
	}
	if got := NewCursor(kv, "github", "last_modified").Load(); got != "Wed, 22 Jul 2026" {
		t.Fatalf("cursor did not persist, got %q", got)
	}
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	if got := c.Load(); got != "" {
		t.Fatalf("cleared cursor should load empty, got %q", got)
	}
}

func TestNilCursorSafe(t *testing.T) {
	var c *Cursor
	if got := c.Load(); got != "" {
		t.Fatalf("nil cursor Load should be empty, got %q", got)
	}
	if err := c.Save("x"); err != nil {
		t.Fatalf("nil cursor Save should no-op, got %v", err)
	}
}

func TestPersistentDeduperSurvivesRestart(t *testing.T) {
	kv := newMemKV()
	id := func(s string) string { return s }

	first := NewPersistentDeduper(id, kv, "seen")
	if out := first.Fresh([]string{"a", "b"}); len(out) != 0 {
		t.Fatalf("first run should baseline (emit nothing), got %v", out)
	}

	restarted := NewPersistentDeduper(id, kv, "seen")
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
