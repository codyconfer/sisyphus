package kv

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "kv.duckdb"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPutGetDelete(t *testing.T) {
	s := openTemp(t)
	if _, ok, err := s.Get(context.Background(), "ns", "k"); ok || err != nil {
		t.Fatalf("expected miss, ok=%v err=%v", ok, err)
	}
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := s.Put(context.Background(), "ns", "k", "v1", exp); err != nil {
		t.Fatal(err)
	}
	e, ok, err := s.Get(context.Background(), "ns", "k")
	if err != nil || !ok || e.Value != "v1" {
		t.Fatalf("get = %+v ok=%v err=%v", e, ok, err)
	}
	if !e.Expiry.Equal(exp) {
		t.Errorf("expiry = %v, want %v", e.Expiry, exp)
	}
	if err := s.Put(context.Background(), "ns", "k", "v2", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if e, _, _ := s.Get(context.Background(), "ns", "k"); e.Value != "v2" || !e.Expiry.IsZero() {
		t.Fatalf("after upsert = %+v", e)
	}
	if err := s.Delete(context.Background(), "ns", "k"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get(context.Background(), "ns", "k"); ok {
		t.Fatal("expected miss after delete")
	}
}

func TestNamespacesIsolated(t *testing.T) {
	s := openTemp(t)
	_ = s.Put(context.Background(), "a", "k", "A", time.Time{})
	_ = s.Put(context.Background(), "b", "k", "B", time.Time{})
	if e, _, _ := s.Get(context.Background(), "a", "k"); e.Value != "A" {
		t.Errorf("a/k = %q", e.Value)
	}
	if e, _, _ := s.Get(context.Background(), "b", "k"); e.Value != "B" {
		t.Errorf("b/k = %q", e.Value)
	}
	list, err := s.List(context.Background(), "a")
	if err != nil || len(list) != 1 || list["k"].Value != "A" {
		t.Fatalf("List(a) = %v, %v", list, err)
	}
}

func TestTTLExpiryAndSweep(t *testing.T) {
	s := openTemp(t)
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := s.Put(context.Background(), "ns", "old", "v", past); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Get(context.Background(), "ns", "old"); err != nil || ok {
		t.Fatalf("expired Get should miss, ok=%v err=%v", ok, err)
	}
	if err := s.Put(context.Background(), "ns", "old2", "v2", past); err != nil {
		t.Fatal(err)
	}
	n, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("Sweep removed %d, want >= 1", n)
	}
	list, err := s.List(context.Background(), "ns")
	if err != nil || len(list) != 0 {
		t.Fatalf("List after sweep = %v, %v", list, err)
	}
}

func TestNamespacesLists(t *testing.T) {
	s := openTemp(t)
	if ns, err := s.Namespaces(context.Background()); err != nil || len(ns) != 0 {
		t.Fatalf("empty Namespaces = %v, %v", ns, err)
	}
	_ = s.Put(context.Background(), "b", "k1", "v", time.Time{})
	_ = s.Put(context.Background(), "a", "k1", "v", time.Time{})
	_ = s.Put(context.Background(), "a", "k2", "v", time.Time{})
	ns, err := s.Namespaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 2 || ns[0] != "a" || ns[1] != "b" {
		t.Fatalf("Namespaces = %v, want [a b]", ns)
	}
}

func TestNamespacesSkipsExpired(t *testing.T) {
	s := openTemp(t)
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	_ = s.Put(context.Background(), "gone", "k", "v", past)
	_ = s.Put(context.Background(), "live", "k", "v", time.Time{})
	ns, err := s.Namespaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 1 || ns[0] != "live" {
		t.Fatalf("Namespaces = %v, want [live]", ns)
	}
}

func TestClear(t *testing.T) {
	s := openTemp(t)
	_ = s.Put(context.Background(), "a", "k1", "v", time.Time{})
	_ = s.Put(context.Background(), "a", "k2", "v", time.Time{})
	_ = s.Put(context.Background(), "b", "k1", "v", time.Time{})

	n, err := s.Clear(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("Clear(a) removed %d, want 2", n)
	}
	if list, _ := s.List(context.Background(), "a"); len(list) != 0 {
		t.Fatalf("ns a should be empty, got %v", list)
	}
	if list, _ := s.List(context.Background(), "b"); len(list) != 1 {
		t.Fatalf("ns b should survive, got %v", list)
	}

	if n, err := s.Clear(context.Background(), "nosuch"); err != nil || n != 0 {
		t.Fatalf("Clear(nosuch) = %d, %v, want 0, nil", n, err)
	}

	n, err = s.Clear(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("Clear(all) removed %d, want 1", n)
	}
	if ns, _ := s.Namespaces(context.Background()); len(ns) != 0 {
		t.Fatalf("store should be empty, got %v", ns)
	}
}

func TestStats(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	if err := s.Put(ctx, "a", "k1", "v", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "a", "k2", "v", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "a", "gone", "v", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "b", "k1", "v", time.Time{}); err != nil {
		t.Fatal(err)
	}

	stats, err := s.Stats(ctx, time.Minute)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if len(stats) != 2 || stats[0].Namespace != "a" || stats[1].Namespace != "b" {
		t.Fatalf("Stats = %+v, want a then b", stats)
	}
	a := stats[0]
	if a.Entries != 2 {
		t.Errorf("a.Entries = %d, want 2 live rows", a.Entries)
	}
	if a.Expired != 1 {
		t.Errorf("a.Expired = %d, want 1", a.Expired)
	}
	if a.Fresh != 2 {
		t.Errorf("a.Fresh = %d, want both rows inside the window", a.Fresh)
	}
	if a.Oldest.IsZero() || a.Newest.IsZero() || a.Newest.Before(a.Oldest) {
		t.Errorf("a oldest/newest = %v/%v", a.Oldest, a.Newest)
	}
	if stats[1].Entries != 1 {
		t.Errorf("b.Entries = %d, want 1", stats[1].Entries)
	}

	if stats, err := s.Stats(ctx, 0); err != nil {
		t.Fatal(err)
	} else if stats[0].Fresh != 0 {
		t.Errorf("Fresh with no window = %d, want 0", stats[0].Fresh)
	}

	// Stats is read-only: the expired row it reported is still there.
	if n, err := s.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("Sweep after Stats = %d %v, want the expired row still present", n, err)
	}
	stats, err = s.Stats(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if stats[0].Expired != 0 {
		t.Errorf("a.Expired after sweep = %d, want 0", stats[0].Expired)
	}
}

func TestStatsEmpty(t *testing.T) {
	stats, err := openTemp(t).Stats(context.Background(), time.Minute)
	if err != nil || len(stats) != 0 {
		t.Fatalf("Stats on an empty store = %+v %v", stats, err)
	}
}

func TestNilStore(t *testing.T) {
	var s *Store
	if _, ok, err := s.Get(context.Background(), "n", "k"); ok || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Get = ok %v err %v, want ErrUnavailable", ok, err)
	}
	if err := s.Put(context.Background(), "n", "k", "v", time.Time{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Put = %v, want ErrUnavailable", err)
	}
	if err := s.Delete(context.Background(), "n", "k"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Delete = %v, want ErrUnavailable", err)
	}
	if list, err := s.List(context.Background(), "n"); list != nil || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil List = %v %v, want ErrUnavailable", list, err)
	}
	if n, err := s.Sweep(context.Background()); n != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Sweep = %d %v, want ErrUnavailable", n, err)
	}
	if ns, err := s.Namespaces(context.Background()); ns != nil || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Namespaces = %v %v, want ErrUnavailable", ns, err)
	}
	if n, err := s.Clear(context.Background(), ""); n != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Clear = %d %v, want ErrUnavailable", n, err)
	}
	if st, err := s.Stats(context.Background(), 0); st != nil || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Stats = %v %v, want ErrUnavailable", st, err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("nil Close = %v", err)
	}
}

func TestClosedStoreUnavailable(t *testing.T) {
	s := openTemp(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Get(context.Background(), "n", "k"); ok || !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Get = ok %v err %v, want ErrUnavailable", ok, err)
	}
	if err := s.Put(context.Background(), "n", "k", "v", time.Time{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Put = %v, want ErrUnavailable", err)
	}
	if err := s.Delete(context.Background(), "n", "k"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Delete = %v, want ErrUnavailable", err)
	}
	if list, err := s.List(context.Background(), "n"); list != nil || !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed List = %v %v, want ErrUnavailable", list, err)
	}
	if n, err := s.Sweep(context.Background()); n != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Sweep = %d %v, want ErrUnavailable", n, err)
	}
	if ns, err := s.Namespaces(context.Background()); ns != nil || !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Namespaces = %v %v, want ErrUnavailable", ns, err)
	}
	if n, err := s.Clear(context.Background(), ""); n != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Clear = %d %v, want ErrUnavailable", n, err)
	}
}
