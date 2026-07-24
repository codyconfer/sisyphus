package sisyphus

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/codyconfer/sisyphus/backup"
	"github.com/codyconfer/sisyphus/secret"
)

type BackupSpec struct {
	Files         []string
	SecretBackend string
	SecretName    string
	SecretService string
}

func Backup(ctx context.Context, spec BackupSpec) (sealed []byte, storeName string, err error) {
	store, err := secret.Resolve(ctx, spec.SecretBackend, spec.SecretService)
	if err != nil {
		return nil, "", err
	}
	key, err := getOrCreateKey(ctx, store, spec.SecretName)
	if err != nil {
		return nil, "", err
	}
	archive, err := backup.Archive(spec.Files)
	if err != nil {
		return nil, "", err
	}
	sealed, err = backup.Encrypt(archive, key)
	if err != nil {
		return nil, "", err
	}
	return sealed, store.Name(), nil
}

type RestoreSpec struct {
	Sealed        []byte
	SecretBackend string
	SecretName    string
	SecretService string
	DestDir       string
}

func Restore(ctx context.Context, spec RestoreSpec) (names []string, storeName string, err error) {
	store, err := secret.Resolve(ctx, spec.SecretBackend, spec.SecretService)
	if err != nil {
		return nil, "", err
	}
	key, err := readKey(ctx, store, spec.SecretName)
	if err != nil {
		return nil, "", err
	}
	names, err = backup.Restore(spec.Sealed, key, spec.DestDir)
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
		k, err := backup.NewKey()
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
