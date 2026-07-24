package sealed

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "sealed.duckdb"), Options{
		Namespace:   "tokens",
		KeyProvider: func(context.Context) ([]byte, error) { return testKey(), nil },
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPutGetDelete(t *testing.T) {
	s := openTemp(t)
	if _, ok, err := s.Get(context.Background(), "github"); ok || err != nil {
		t.Fatalf("expected miss, ok=%v err=%v", ok, err)
	}
	want := Entry{AccessToken: "a1", RefreshToken: "r1", Scope: "repo", Expiry: time.Now().Add(time.Hour)}
	if err := s.Put(context.Background(), "github", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get(context.Background(), "github")
	if err != nil || !ok || got.AccessToken != "a1" || got.Scope != "repo" {
		t.Fatalf("round-trip = %+v ok=%v err=%v", got, ok, err)
	}
	if err := s.Delete(context.Background(), "github"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get(context.Background(), "github"); ok {
		t.Fatal("expected miss after delete")
	}
}

func TestStoredValueIsEncrypted(t *testing.T) {
	s := openTemp(t)
	if err := s.Put(context.Background(), "github", Entry{AccessToken: "super-secret-token"}); err != nil {
		t.Fatal(err)
	}
	e, ok, err := s.kv.Get(context.Background(), "tokens", "github")
	if err != nil || !ok {
		t.Fatalf("raw get: ok=%v err=%v", ok, err)
	}
	if strings.Contains(e.Value, "super-secret-token") {
		t.Fatal("plaintext leaked into kv value")
	}
	var probe map[string]any
	if json.Unmarshal([]byte(e.Value), &probe) == nil {
		t.Fatal("raw value should not be plaintext JSON")
	}
}

func TestNilStore(t *testing.T) {
	var s *Store
	if _, ok, err := s.Get(context.Background(), "x"); ok || err != nil {
		t.Fatalf("nil Get = ok %v err %v", ok, err)
	}
	if err := s.Put(context.Background(), "x", Entry{AccessToken: "a"}); err == nil {
		t.Fatal("nil Put should error")
	}
}
