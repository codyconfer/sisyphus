package sealed

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/codyconfer/sisyphus/backup"
	"github.com/codyconfer/sisyphus/kv"
	"github.com/codyconfer/sisyphus/secret"
)

// ErrUnavailable is returned when a method is called on a nil or closed store.
var ErrUnavailable = errors.New("sealed store unavailable")

// ErrUndecodable is returned when a stored value exists but cannot be decrypted
// with the current key, so callers can tell a lost key from a missing entry.
var ErrUndecodable = errors.New("sealed value cannot be decrypted with the current key")

// Entry is an app-agnostic credential-shaped blob stored encrypted at rest.
//
// Expiry and TTL are deliberately separate and must not be conflated:
//
//   - Expiry describes the credential. It is when the access token stops being
//     accepted by the remote service. It is payload only: an entry whose access
//     token expired is still stored, still returned by Get, and still carries
//     its Scope and RefreshToken, so a caller can report "expired" and
//     re-request exactly the permissions it had.
//   - TTL describes the record. It is when the stored row itself becomes
//     garbage and may be deleted, which happens on the next read or sweep.
//     Leave it zero to keep the record until it is explicitly deleted.
//
// Setting TTL from Expiry destroys the credential's metadata at the moment a
// caller most needs it, so do that only for records that are genuinely
// worthless once expired.
type Entry struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
	// Expiry is when the access token stops working. Payload only; it never
	// deletes the record.
	Expiry time.Time `json:"expiry,omitempty"`
	// TTL is when the record may be discarded. Zero means keep it forever.
	TTL time.Time `json:"ttl,omitempty"`
}

// Options configures Open. Key material comes from the OS keyring (via secret)
// unless KeyProvider is set.
type Options struct {
	Namespace      string
	KeyringService string
	KeyName        string
	KeyProvider    func(context.Context) ([]byte, error)
}

// Store is an encrypted key/value store backed by kv.Store. Values are AES-GCM
// sealed; the encryption key is escrowed in the OS keyring (or KeyProvider).
type Store struct {
	mu          sync.RWMutex
	kv          *kv.Store
	ns          string
	keyProvider func(context.Context) ([]byte, error)
	keyMu       sync.Mutex
	key         []byte
}

func Open(ctx context.Context, path string, opts Options) (*Store, error) {
	ns := opts.Namespace
	if ns == "" {
		ns = "sealed"
	}
	k, err := kv.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	provider := opts.KeyProvider
	if provider == nil {
		svc := opts.KeyringService
		if svc == "" {
			svc = "sisyphus"
		}
		keyName := opts.KeyName
		if keyName == "" {
			keyName = "sealed-key"
		}
		provider = func(ctx context.Context) ([]byte, error) {
			return keyringKey(ctx, svc, keyName)
		}
	}
	return &Store{kv: k, ns: ns, keyProvider: provider}, nil
}

func keyringKey(ctx context.Context, service, keyName string) ([]byte, error) {
	store, err := secret.Resolve(ctx, "keyring", service)
	if err != nil {
		return nil, err
	}
	v, err := secret.GetOrCreate(ctx, store, keyName, func() (string, error) {
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

func (s *Store) handle() *kv.Store {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.kv
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	k := s.kv
	s.kv = nil
	s.mu.Unlock()
	if k == nil {
		return nil
	}
	return k.Close()
}

func (s *Store) encryptionKey(ctx context.Context) ([]byte, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if s.key != nil {
		return s.key, nil
	}
	k, err := s.keyProvider(ctx)
	if err != nil {
		return nil, err
	}
	s.key = k
	return k, nil
}

// Get returns the entry stored under name. A stored value must decrypt and
// authenticate under the current key: there is no cleartext fallback, because a
// value that failed its GCM tag and a value that was never sealed are
// indistinguishable on the wire, and reading the second means anyone who can
// write the database can substitute a credential without holding the key.
// Anything that does not authenticate is reported as ErrUndecodable wrapping
// the underlying failure, never as a miss and never as cleartext.
func (s *Store) Get(ctx context.Context, name string) (Entry, bool, error) {
	k := s.handle()
	if k == nil {
		return Entry{}, false, nil
	}
	e, ok, err := k.Get(ctx, s.ns, name)
	if err != nil || !ok {
		return Entry{}, ok, err
	}
	key, err := s.encryptionKey(ctx)
	if err != nil {
		return Entry{}, false, err
	}
	raw, derr := base64.StdEncoding.DecodeString(e.Value)
	if derr != nil {
		return Entry{}, false, fmt.Errorf("%w: %w", ErrUndecodable, derr)
	}
	plain, derr := backup.Decrypt(raw, key)
	if derr != nil {
		return Entry{}, false, fmt.Errorf("%w: %w", ErrUndecodable, derr)
	}
	var out Entry
	if derr := json.Unmarshal(plain, &out); derr != nil {
		return Entry{}, false, fmt.Errorf("%w: %w", ErrUndecodable, derr)
	}
	return out, true, nil
}

func (s *Store) Put(ctx context.Context, name string, e Entry) error {
	k := s.handle()
	if k == nil {
		return ErrUnavailable
	}
	key, err := s.encryptionKey(ctx)
	if err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	sealed, err := backup.Encrypt(b, key)
	if err != nil {
		return err
	}
	v := base64.StdEncoding.EncodeToString(sealed)
	return k.Put(ctx, s.ns, name, v, e.TTL)
}

func (s *Store) Delete(ctx context.Context, name string) error {
	k := s.handle()
	if k == nil {
		return ErrUnavailable
	}
	return k.Delete(ctx, s.ns, name)
}
