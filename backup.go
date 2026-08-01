package sisyphus

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/codyconfer/sisyphus/backup"
	"github.com/codyconfer/sisyphus/internal/crypt"
	"github.com/codyconfer/sisyphus/secret"
)

// SecretRef selects the secret-manager entry holding a backup key: which
// Backend to use (any spelling secret.ParseBackend accepts), the entry Name
// (defaulting to "backup-key"), and the Service namespace for keyring-style
// backends.
type SecretRef struct {
	Backend string
	Name    string
	Service string
}

// BackupSpec describes one encrypted backup: which Files to archive and
// which Secret entry escrows the encryption key.
type BackupSpec struct {
	Files  []string
	Secret SecretRef
}

// Backup archives spec.Files into a tar, encrypts it with AES-256-GCM, and
// returns the sealed bytes plus the name of the secret store that holds the
// key. The key is created and escrowed on first use (under spec.Secret.Name,
// "backup-key" when empty) and never travels with the archive. Backup is
// independent of ConfigStore, so it works without opening the config DB.
func Backup(ctx context.Context, spec BackupSpec) (sealed []byte, storeName string, err error) {
	store, err := secret.Open(ctx, secret.Backend(spec.Secret.Backend), spec.Secret.Service)
	if err != nil {
		return nil, "", err
	}
	key, err := getOrCreateKey(ctx, store, spec.Secret.Name)
	if err != nil {
		return nil, "", err
	}
	archive, err := backup.Archive(ctx, spec.Files)
	if err != nil {
		return nil, "", err
	}
	sealed, err = crypt.Encrypt(archive, key)
	if err != nil {
		return nil, "", err
	}
	return sealed, store.Name(), nil
}

// RestoreSpec describes one restore: the Sealed archive bytes, the Secret
// entry holding its key, and the DestDir the files are written into.
type RestoreSpec struct {
	Sealed  []byte
	Secret  SecretRef
	DestDir string
}

// Restore decrypts spec.Sealed with the escrowed key and swaps the archived
// files into spec.DestDir, returning the restored basenames plus the name of
// the secret store that supplied the key. A missing key is an error — restore
// never generates one. Like Backup it is independent of ConfigStore, so a
// corrupt config DB does not block restoring it.
func Restore(ctx context.Context, spec RestoreSpec) (names []string, storeName string, err error) {
	store, err := secret.Open(ctx, secret.Backend(spec.Secret.Backend), spec.Secret.Service)
	if err != nil {
		return nil, "", err
	}
	key, err := readKey(ctx, store, spec.Secret.Name)
	if err != nil {
		return nil, "", err
	}
	names, err = backup.Restore(ctx, spec.Sealed, key, spec.DestDir)
	return names, store.Name(), err
}

func keyName(name string) string {
	if name == "" {
		return "backup-key"
	}
	return name
}

func readKey(ctx context.Context, store secret.Store, name string) ([]byte, error) {
	name = keyName(name)
	v, ok, err := store.Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("%s: reading %q: %w", store.Name(), name, err)
	}
	if !ok {
		return nil, fmt.Errorf("no backup key %q found in %s; cannot decrypt", name, store.Name())
	}
	return base64.StdEncoding.DecodeString(v)
}

func getOrCreateKey(ctx context.Context, store secret.Store, name string) ([]byte, error) {
	name = keyName(name)
	v, err := secret.GetOrCreate(ctx, store, name, func() (string, error) {
		k, err := crypt.NewKey()
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(k), nil
	})
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(v)
}
