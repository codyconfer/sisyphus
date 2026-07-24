package secret

import (
	"context"
	"errors"

	"github.com/zalando/go-keyring"
)

type keyringStore struct {
	service string
}

func newKeyringStore(service string) keyringStore {
	if service == "" {
		service = "sisyphus"
	}
	return keyringStore{service: service}
}

func (keyringStore) Name() string { return "os-keyring" }

func (k keyringStore) Get(ctx context.Context, key string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	v, err := keyring.Get(k.service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (k keyringStore) Set(ctx context.Context, key, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return keyring.Set(k.service, key, value)
}
