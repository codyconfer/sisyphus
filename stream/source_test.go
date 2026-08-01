package stream

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSourceDedupe(t *testing.T) {
	id := func(s string) string { return s }
	cases := []struct {
		name  string
		key   func(string) string
		polls [][]string
		want  [][]string
	}{
		{
			name:  "no key passes every emission through",
			polls: [][]string{{"a", "b"}, {"a", "b"}},
			want:  [][]string{{"a", "b"}, {"a", "b"}},
		},
		{
			name:  "key baselines the first poll then emits only fresh items",
			key:   id,
			polls: [][]string{{"a", "b"}, {"a", "b", "c"}, {"c", "d"}},
			want:  [][]string{{"c"}, {"d"}},
		},
		{
			name:  "fully duplicate polls are dropped",
			key:   id,
			polls: [][]string{{"a"}, {"a"}, {"a", "b"}},
			want:  [][]string{{"b"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			n := 0
			src := Source[string]{
				Interval: time.Millisecond,
				Key:      c.key,
				Step: func(context.Context) ([]string, error) {
					if n >= len(c.polls) {
						return nil, nil
					}
					items := c.polls[n]
					n++
					return items, nil
				},
			}
			ch := src.Run(ctx)
			timeout := time.After(2 * time.Second)
			for i, want := range c.want {
				select {
				case e := <-ch:
					if e.Err != nil {
						t.Fatalf("emission %d: unexpected error %v", i, e.Err)
					}
					if len(e.Items) != len(want) {
						t.Fatalf("emission %d = %v, want %v", i, e.Items, want)
					}
					for j := range want {
						if e.Items[j] != want[j] {
							t.Fatalf("emission %d = %v, want %v", i, e.Items, want)
						}
					}
				case <-timeout:
					t.Fatalf("timed out waiting for emission %d of %v", i, c.want)
				}
			}
		})
	}
}

func TestSourcePersistentDedupeRestoresFromKV(t *testing.T) {
	id := func(s string) string { return s }
	store := newMemKV()

	// A prior run baselined {a, b} into the store.
	ctx := context.Background()
	NewPersistentDeduper(ctx, id, store, "seen").Unseen(ctx, []string{"a", "b"})

	rctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	src := Source[string]{
		Interval:  time.Millisecond,
		Key:       id,
		KV:        store,
		Namespace: "seen",
		Step: func(context.Context) ([]string, error) {
			return []string{"a", "b", "c"}, nil
		},
	}
	select {
	case e := <-src.Run(rctx):
		if e.Err != nil {
			t.Fatal(e.Err)
		}
		if len(e.Items) != 1 || e.Items[0] != "c" {
			t.Fatalf("restored source should emit only unseen items, got %v", e.Items)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no emission: a restored seen set must not re-baseline and swallow new items")
	}
}

func TestSourceStepTimeoutFires(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	src := Source[string]{
		Interval:    time.Hour,
		StepTimeout: 20 * time.Millisecond,
		Step: func(ctx context.Context) ([]string, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	select {
	case e := <-src.Run(ctx):
		if !errors.Is(e.Err, context.DeadlineExceeded) {
			t.Fatalf("blocked step should time out, got %v", e.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a step blocked on its context never returned: the per-step timeout did not fire")
	}
}

func TestSourceErrorsPassThroughDedupe(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	boom := errors.New("boom")
	src := Source[string]{
		Interval: time.Millisecond,
		Key:      func(s string) string { return s },
		Step:     func(context.Context) ([]string, error) { return nil, boom },
	}
	select {
	case e := <-src.Run(ctx):
		if !errors.Is(e.Err, boom) {
			t.Fatalf("error emission = %v, want %v unchanged", e.Err, boom)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dedupe stage swallowed an error emission")
	}
}

func TestSourceAdaptiveUsesReturnedInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	n := 0
	src := Source[int]{
		Interval:    time.Hour,
		StepTimeout: time.Minute,
		StepAdaptive: func(ctx context.Context) ([]int, time.Duration, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("adaptive step ran without the configured per-step deadline")
			}
			n++
			return []int{n}, time.Millisecond, nil
		},
	}
	ch := src.Run(ctx)
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
			t.Fatalf("adaptive source did not honor the returned interval; got %d emissions", got)
		}
	}
}
