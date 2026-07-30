package secret

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type Store interface {
	Name() string
	Get(ctx context.Context, key string) (value string, found bool, err error)
	Set(ctx context.Context, key, value string) error
}

var (
	bwAvailable = bwConfigured
	opAvailable = opConfigured
)

var backends = []string{"auto", "bitwarden", "bw", "1password", "op", "keyring"}

func Backends() []string {
	out := make([]string, len(backends))
	copy(out, backends)
	return out
}

func ValidBackend(backend string) bool {
	if backend == "" {
		return true
	}
	for _, b := range backends {
		if backend == b {
			return true
		}
	}
	return false
}

func Resolve(ctx context.Context, backend, service string) (Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch backend {
	case "bitwarden", "bw":
		if bwAvailable(ctx) {
			return bwStore{}, nil
		}
		return nil, errors.New("bitwarden CLI (bw) is not configured/unlocked; run `bw login` and set BW_SESSION")
	case "1password", "op":
		if opAvailable(ctx) {
			return opStore{}, nil
		}
		return nil, errors.New("1Password CLI (op) is not configured; run `op signin`")
	case "keyring":
		return newKeyringStore(service), nil
	case "", "auto":
		switch {
		case bwAvailable(ctx):
			return bwStore{}, nil
		case opAvailable(ctx):
			return opStore{}, nil
		default:
			return newKeyringStore(service), nil
		}
	default:
		return nil, fmt.Errorf("unknown secret backend %q (want one of: %s)", backend, strings.Join(Backends(), ", "))
	}
}

func GetOrCreate(ctx context.Context, s Store, key string, gen func() (string, error)) (string, error) {
	v, ok, err := s.Get(ctx, key)
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
	if err := s.Set(ctx, key, v); err != nil {
		return "", fmt.Errorf("%s: storing %q: %w", s.Name(), key, err)
	}
	return v, nil
}
