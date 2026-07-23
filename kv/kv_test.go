package kv

import (
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "kv.duckdb"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPutGetDelete(t *testing.T) {
	s := openTemp(t)
	if _, ok, err := s.Get("ns", "k"); ok || err != nil {
		t.Fatalf("expected miss, ok=%v err=%v", ok, err)
	}
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := s.Put("ns", "k", "v1", exp); err != nil {
		t.Fatal(err)
	}
	e, ok, err := s.Get("ns", "k")
	if err != nil || !ok || e.Value != "v1" {
		t.Fatalf("get = %+v ok=%v err=%v", e, ok, err)
	}
	if !e.Expiry.Equal(exp) {
		t.Errorf("expiry = %v, want %v", e.Expiry, exp)
	}
	if err := s.Put("ns", "k", "v2", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if e, _, _ := s.Get("ns", "k"); e.Value != "v2" || !e.Expiry.IsZero() {
		t.Fatalf("after upsert = %+v", e)
	}
	if err := s.Delete("ns", "k"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get("ns", "k"); ok {
		t.Fatal("expected miss after delete")
	}
}

func TestNamespacesIsolated(t *testing.T) {
	s := openTemp(t)
	_ = s.Put("a", "k", "A", time.Time{})
	_ = s.Put("b", "k", "B", time.Time{})
	if e, _, _ := s.Get("a", "k"); e.Value != "A" {
		t.Errorf("a/k = %q", e.Value)
	}
	if e, _, _ := s.Get("b", "k"); e.Value != "B" {
		t.Errorf("b/k = %q", e.Value)
	}
	list, err := s.List("a")
	if err != nil || len(list) != 1 || list["k"].Value != "A" {
		t.Fatalf("List(a) = %v, %v", list, err)
	}
}

func TestNilStore(t *testing.T) {
	var s *Store
	if _, ok, err := s.Get("n", "k"); ok || err != nil {
		t.Errorf("nil Get = ok %v err %v", ok, err)
	}
	if err := s.Put("n", "k", "v", time.Time{}); err == nil {
		t.Error("nil Put should error")
	}
	if err := s.Delete("n", "k"); err == nil {
		t.Error("nil Delete should error")
	}
	if list, err := s.List("n"); err != nil || list != nil {
		t.Errorf("nil List = %v %v", list, err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("nil Close = %v", err)
	}
}
