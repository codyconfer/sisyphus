// Package secret escrows small string secrets (typically encryption keys) in
// an external secret manager: the Bitwarden CLI (bw), the 1Password CLI
// (op), or the OS keyring. Open selects the backend — BackendAuto probes
// them in that order — and returns a uniform Store interface over it.
package secret

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Store is one secret backend: named for messages, with get/set by key.
// Get reports a missing key as found=false with a nil error.
type Store interface {
	Name() string
	Get(ctx context.Context, key string) (value string, found bool, err error)
	Set(ctx context.Context, key, value string) error
}

var (
	bwAvailable = bwConfigured
	opAvailable = opConfigured
)

// Backend names a secret storage backend.
type Backend string

const (
	// BackendAuto picks the first configured backend: Bitwarden, then
	// 1Password, then the OS keyring. The empty string means the same.
	BackendAuto Backend = "auto"
	// BackendBitwarden uses the Bitwarden CLI (bw), which must be logged in
	// and unlocked.
	BackendBitwarden Backend = "bitwarden"
	// Backend1Password uses the 1Password CLI (op), which must be signed in.
	Backend1Password Backend = "1password"
	// BackendKeyring uses the OS keyring under the service name given to Open.
	BackendKeyring Backend = "keyring"
)

// Backends lists the canonical backend names accepted by Open.
func Backends() []string {
	return []string{
		string(BackendAuto),
		string(BackendBitwarden),
		string(Backend1Password),
		string(BackendKeyring),
	}
}

// ParseBackend maps a user-supplied backend name to a Backend. Besides the
// canonical names from Backends it accepts the aliases "bw" (bitwarden) and
// "op" (1password), and the empty string (auto).
func ParseBackend(s string) (Backend, bool) {
	switch s {
	case "", string(BackendAuto):
		return BackendAuto, true
	case string(BackendBitwarden), "bw":
		return BackendBitwarden, true
	case string(Backend1Password), "op":
		return Backend1Password, true
	case string(BackendKeyring):
		return BackendKeyring, true
	default:
		return "", false
	}
}

// Open returns the Store for backend. Any spelling accepted by ParseBackend
// (including the aliases and the empty string) is accepted here.
func Open(ctx context.Context, backend Backend, service string) (Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, ok := ParseBackend(string(backend))
	if !ok {
		return nil, fmt.Errorf("unknown secret backend %q (want one of: %s)", backend, strings.Join(Backends(), ", "))
	}
	switch b {
	case BackendBitwarden:
		if bwAvailable(ctx) {
			return bwStore{}, nil
		}
		return nil, errors.New("bitwarden CLI (bw) is not configured/unlocked; run `bw login` and set BW_SESSION")
	case Backend1Password:
		if opAvailable(ctx) {
			return opStore{}, nil
		}
		return nil, errors.New("1Password CLI (op) is not configured; run `op signin`")
	case BackendKeyring:
		return newKeyringStore(service), nil
	default: // BackendAuto
		switch {
		case bwAvailable(ctx):
			return bwStore{}, nil
		case opAvailable(ctx):
			return opStore{}, nil
		default:
			return newKeyringStore(service), nil
		}
	}
}

// GetOrCreate returns the value stored under key, generating one with gen
// and storing it when the key does not exist yet. It is a read-then-write,
// not an atomic operation: two concurrent callers can each generate a value,
// with the later Set winning.
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
