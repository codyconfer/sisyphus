package sisyphus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestBackupRestoreThroughKeyring(t *testing.T) {
	keyring.MockInit()

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "config.duckdb"), []byte("CFG"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "tokens.duckdb"), []byte("TOK"), 0o600); err != nil {
		t.Fatal(err)
	}

	sealed, storeName, err := Backup(context.Background(), BackupSpec{
		Files:         []string{filepath.Join(src, "config.duckdb"), filepath.Join(src, "tokens.duckdb")},
		SecretBackend: "keyring",
		SecretService: "testapp",
		SecretName:    "backup-key",
	})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if storeName != "os-keyring" {
		t.Errorf("storeName = %q", storeName)
	}

	dst := t.TempDir()
	names, _, err := Restore(context.Background(), RestoreSpec{
		Sealed:        sealed,
		SecretBackend: "keyring",
		SecretService: "testapp",
		SecretName:    "backup-key",
		DestDir:       dst,
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("restored %v, want 2", names)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "config.duckdb")); string(b) != "CFG" {
		t.Errorf("config.duckdb = %q", b)
	}
}

func TestRestoreMissingKeyFails(t *testing.T) {
	keyring.MockInit()
	_, _, err := Restore(context.Background(), RestoreSpec{
		Sealed:        []byte("whatever"),
		SecretBackend: "keyring",
		SecretService: "testapp",
		SecretName:    "absent-key",
		DestDir:       t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error when key is absent")
	}
}
