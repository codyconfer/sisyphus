package secret

import (
	"errors"
	"fmt"
)

type Store interface {
	Name() string
	Get(key string) (value string, found bool, err error)
	Set(key, value string) error
}

var (
	bwAvailable = bwConfigured
	opAvailable = opConfigured
)

func Resolve(backend, service string) (Store, error) {
	switch backend {
	case "bitwarden", "bw":
		if bwAvailable() {
			return bwStore{}, nil
		}
		return nil, errors.New("Bitwarden CLI (bw) is not configured/unlocked; run `bw login` and set BW_SESSION")
	case "1password", "op":
		if opAvailable() {
			return opStore{}, nil
		}
		return nil, errors.New("1Password CLI (op) is not configured; run `op signin`")
	case "keyring":
		return newKeyringStore(service), nil
	case "", "auto":
		switch {
		case bwAvailable():
			return bwStore{}, nil
		case opAvailable():
			return opStore{}, nil
		default:
			return newKeyringStore(service), nil
		}
	default:
		return nil, fmt.Errorf("unknown secret backend %q (want auto, bitwarden, 1password, or keyring)", backend)
	}
}

func GetOrCreate(s Store, key string, gen func() (string, error)) (string, error) {
	v, ok, err := s.Get(key)
	if err != nil {
		return "", fmt.Errorf("%s: reading %q: %w", s.Name(), key, err)
	}
	if ok {
		return v, nil
	}
	v, err = gen()
	if err != nil {
		return "", err
	}
	if err := s.Set(key, v); err != nil {
		return "", fmt.Errorf("%s: storing %q: %w", s.Name(), key, err)
	}
	return v, nil
}
