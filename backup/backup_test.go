package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptRoundTrip(t *testing.T) {
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("duckdb bytes here")
	sealed, err := Encrypt(plain, key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("ciphertext should not contain plaintext")
	}
	got, err := Decrypt(sealed, key)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip = %q, %v", got, err)
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	k1, _ := NewKey()
	k2, _ := NewKey()
	sealed, _ := Encrypt([]byte("secret"), k1)
	if _, err := Decrypt(sealed, k2); err == nil {
		t.Fatal("decrypt with the wrong key must fail (GCM auth)")
	}
}

func TestArchiveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "config.duckdb")
	b := filepath.Join(dir, "audit.duckdb")
	os.WriteFile(a, []byte("AAA"), 0o600)
	os.WriteFile(b, []byte("BBB"), 0o600)

	arc, err := Archive([]string{a, b, filepath.Join(dir, "tokens.duckdb")})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := Extract(arc)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || string(entries["config.duckdb"]) != "AAA" || string(entries["audit.duckdb"]) != "BBB" {
		t.Fatalf("archive entries = %v", entries)
	}
}

func TestArchiveEmptyErrors(t *testing.T) {
	if _, err := Archive([]string{filepath.Join(t.TempDir(), "nope.duckdb")}); err == nil {
		t.Fatal("expected error when no files exist")
	}
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "config.duckdb"), []byte("CFG"), 0o600)
	os.WriteFile(filepath.Join(src, "tokens.duckdb"), []byte("TOK"), 0o600)

	key, _ := NewKey()
	arc, err := Archive([]string{filepath.Join(src, "config.duckdb"), filepath.Join(src, "tokens.duckdb")})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := Encrypt(arc, key)
	if err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	names, err := Restore(sealed, key, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("restored %v, want 2 files", names)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "config.duckdb")); string(b) != "CFG" {
		t.Errorf("config.duckdb = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "tokens.duckdb")); string(b) != "TOK" {
		t.Errorf("tokens.duckdb = %q", b)
	}
}

func TestRestoreWrongKeyFails(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "a.duckdb"), []byte("x"), 0o600)
	k1, _ := NewKey()
	arc, _ := Archive([]string{filepath.Join(src, "a.duckdb")})
	sealed, _ := Encrypt(arc, k1)
	k2, _ := NewKey()
	if _, err := Restore(sealed, k2, t.TempDir()); err == nil {
		t.Fatal("restore with wrong key must fail")
	}
}

func TestEncryptBadKeyLength(t *testing.T) {
	if _, err := Encrypt([]byte("x"), make([]byte, 16)); err == nil {
		t.Fatal("expected error for wrong key length")
	}
	if _, err := Decrypt(make([]byte, 100), make([]byte, 16)); err == nil {
		t.Fatal("expected error for wrong key length")
	}
}

func TestDecryptShortCiphertext(t *testing.T) {
	key, _ := NewKey()
	if _, err := Decrypt([]byte{1, 2, 3}, key); err == nil {
		t.Fatal("expected error for short ciphertext")
	}
}
