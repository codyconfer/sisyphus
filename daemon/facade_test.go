package daemon

import (
	"context"
	"testing"
	"time"
)

// TestFacadeForwards drives one call through each forwarded group so a drift
// between the facade and the new packages fails loudly here rather than in a
// consumer.
func TestFacadeForwards(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// stream
	d := NewDeduper(func(s string) string { return s })
	if got := d.Unseen(ctx, []string{"a"}); len(got) != 0 {
		t.Fatalf("first pass = %v, want the baseline swallowed", got)
	}
	if got := d.Unseen(ctx, []string{"a", "b"}); len(got) != 1 || got[0] != "b" {
		t.Fatalf("Deduper.Unseen = %v, want [b]", got)
	}
	subj := NewSubject[int]()
	ch := subj.Subscribe(1)
	subj.Next(42)
	if v := <-ch; v != 42 {
		t.Fatalf("Subject forwarded %d, want 42", v)
	}
	subj.Close()
	merged := FanIn(ctx, func() <-chan int {
		c := make(chan int, 1)
		c <- 7
		close(c)
		return c
	}())
	if v := <-merged; v != 7 {
		t.Fatalf("FanIn forwarded %d, want 7", v)
	}
	if kv := ScopedKV(nil, "p/"); kv != nil {
		t.Fatal("ScopedKV(nil) should be nil")
	}
	var src Source[string]
	_ = src // the generic alias itself is the assertion

	// ipc
	if ErrInUse == nil {
		t.Fatal("ErrInUse should forward the ipc sentinel")
	}
	if IsListening("", t.TempDir()+"/nope.sock") {
		t.Fatal("IsListening on a missing socket should be false")
	}

	// schedule
	ranCh := make(chan struct{})
	sctx, scancel := context.WithCancel(ctx)
	go func() {
		_ = Schedule(sctx, time.Millisecond, ScheduleJob{
			Name: "smoke",
			Next: func(_ context.Context, now time.Time) (Due, error) { return Due{At: now}, nil },
			Run: func(context.Context) error {
				select {
				case ranCh <- struct{}{}:
				default:
				}
				scancel()
				return nil
			},
		})
	}()
	select {
	case <-ranCh:
	case <-time.After(5 * time.Second):
		t.Fatal("Schedule facade did not run the job")
	}

	// tray
	SetStateIcon(StateWarn, Asset{Name: "state:warn", MIME: "image/png", Bytes: []byte{1}})
	if a, ok := StateIcon(StateWarn); !ok || a.Name != "state:warn" {
		t.Fatalf("StateIcon = %+v ok=%v", a, ok)
	}
	if len(States()) != 5 || DefaultStateIcons() == nil {
		t.Fatal("tray forwards are broken")
	}
}
