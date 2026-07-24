package kv

import (
	"context"
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

func TestNilStore(t *testing.T) {
	var s *Store
	if _, ok, err := s.Get(context.Background(), "n", "k"); ok || err != nil {
		t.Errorf("nil Get = ok %v err %v", ok, err)
	}
	if err := s.Put(context.Background(), "n", "k", "v", time.Time{}); err == nil {
		t.Error("nil Put should error")
	}
	if err := s.Delete(context.Background(), "n", "k"); err == nil {
		t.Error("nil Delete should error")
	}
	if list, err := s.List(context.Background(), "n"); err != nil || list != nil {
		t.Errorf("nil List = %v %v", list, err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("nil Close = %v", err)
	}
}
