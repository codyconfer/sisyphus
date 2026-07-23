package secret

import (
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

func (k keyringStore) Get(key string) (string, bool, error) {
	v, err := keyring.Get(k.service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (k keyringStore) Set(key, value string) error {
	return keyring.Set(k.service, key, value)
}
