package sealed

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	if _, ok, err := s.Get(context.Background(), "x"); ok || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil Get = ok %v err %v, want ErrUnavailable", ok, err)
	}
	if err := s.Put(context.Background(), "x", Entry{AccessToken: "a"}); err == nil {
		t.Fatal("nil Put should error")
	}
}

func TestClosedStoreOperationsError(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	if err := s.Put(ctx, "github", Entry{AccessToken: "a1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok, err := s.Get(ctx, "github"); ok || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed Get = ok %v err %v, want ErrUnavailable", ok, err)
	}
	if err := s.Put(ctx, "github", Entry{AccessToken: "a2"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed Put err = %v, want ErrUnavailable", err)
	}
	if err := s.Delete(ctx, "github"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed Delete err = %v, want ErrUnavailable", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close err = %v, want nil", err)
	}
}

func TestRefreshTokenOutlivesAccessTokenExpiry(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	want := Entry{
		AccessToken:  "stale",
		RefreshToken: "r1",
		Scope:        "repo",
		Expiry:       time.Now().Add(-time.Hour),
	}
	if err := s.Put(ctx, "google", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get(ctx, "google")
	if err != nil || !ok {
		t.Fatalf("Get after access-token expiry = ok %v err %v, want the entry back", ok, err)
	}
	if got.RefreshToken != "r1" {
		t.Fatalf("RefreshToken = %q, want r1", got.RefreshToken)
	}
	if !got.Expiry.Equal(want.Expiry) {
		t.Fatalf("Expiry = %v, want %v preserved in the payload", got.Expiry, want.Expiry)
	}
}

// TestExpiredEntryWithoutRefreshTokenSurvivesRead pins the Expiry/TTL split: an
// expired access token with nothing to refresh it is still a record worth
// keeping, because its Scope is what a caller needs to re-request the same
// permissions. Erasing it on read turns "expired" into "never logged in".
func TestExpiredEntryWithoutRefreshTokenSurvivesRead(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	want := Entry{AccessToken: "stale", Scope: "channels:read", Expiry: time.Now().Add(-time.Hour)}
	if err := s.Put(ctx, "slack", want); err != nil {
		t.Fatal(err)
	}
	for i, when := range []string{"first", "second"} {
		got, ok, err := s.Get(ctx, "slack")
		if err != nil || !ok {
			t.Fatalf("%s Get = ok %v err %v, want the expired entry back", when, ok, err)
		}
		if got.Scope != want.Scope {
			t.Fatalf("%s Get Scope = %q, want %q preserved so permissions can be re-requested", when, got.Scope, want.Scope)
		}
		if !got.Expiry.Equal(want.Expiry) {
			t.Fatalf("%s Get Expiry = %v, want %v", when, got.Expiry, want.Expiry)
		}
		if i == 0 {
			if e, ok, err := s.kv.Get(ctx, "tokens", "slack"); !ok || err != nil || e.Value == "" {
				t.Fatalf("row deleted by the first read: ok %v err %v", ok, err)
			}
		}
	}
}

// TestTTLDiscardsTheRecord is the other half: TTL, and only TTL, is what makes
// a row collectable.
func TestTTLDiscardsTheRecord(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	if err := s.Put(ctx, "ephemeral", Entry{AccessToken: "a", TTL: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Get(ctx, "ephemeral"); ok || err != nil {
		t.Fatalf("Get = ok %v err %v, want a miss for a record past its TTL", ok, err)
	}
	future := Entry{AccessToken: "a", TTL: time.Now().Add(time.Hour)}
	if err := s.Put(ctx, "later", future); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get(ctx, "later")
	if err != nil || !ok {
		t.Fatalf("Get = ok %v err %v, want a record still inside its TTL", ok, err)
	}
	if !got.TTL.Equal(future.TTL) {
		t.Fatalf("TTL = %v, want %v round-tripped in the payload", got.TTL, future.TTL)
	}
}

// TestPlaintextValueIsRejected covers the removed cleartext fallback: a row that
// does not authenticate under the current key must never be parsed as JSON,
// otherwise anyone who can write the database can substitute a credential.
func TestPlaintextValueIsRejected(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	if err := s.kv.Put(ctx, "tokens", "github", `{"access_token":"forged","scope":"repo"}`, time.Time{}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get(ctx, "github")
	if ok || got.AccessToken != "" {
		t.Fatalf("Get accepted a plaintext row: %+v ok=%v", got, ok)
	}
	if !errors.Is(err, ErrUndecodable) {
		t.Fatalf("Get error = %v, want ErrUndecodable", err)
	}
}

// TestTamperedCiphertextIsReported makes sure the GCM authentication failure
// itself surfaces rather than being swallowed.
func TestTamperedCiphertextIsReported(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	if err := s.Put(ctx, "github", Entry{AccessToken: "real"}); err != nil {
		t.Fatal(err)
	}
	e, ok, err := s.kv.Get(ctx, "tokens", "github")
	if err != nil || !ok {
		t.Fatalf("raw get: ok=%v err=%v", ok, err)
	}
	raw, err := base64.StdEncoding.DecodeString(e.Value)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xff
	if err := s.kv.Put(ctx, "tokens", "github", base64.StdEncoding.EncodeToString(raw), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Get(ctx, "github"); ok || !errors.Is(err, ErrUndecodable) {
		t.Fatalf("Get = ok %v err %v, want ErrUndecodable for a failed MAC", ok, err)
	}
}

func TestUndecodableValueIsNotReportedAsAMiss(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	if err := s.kv.Put(ctx, "tokens", "github", "!!neither-base64-nor-json!!", time.Time{}); err != nil {
		t.Fatal(err)
	}
	_, ok, err := s.Get(ctx, "github")
	if ok {
		t.Fatal("Get should not report a value it cannot decode as present")
	}
	if !errors.Is(err, ErrUndecodable) {
		t.Fatalf("Get error = %v, want ErrUndecodable so a lost key is distinguishable from a miss", err)
	}
}
