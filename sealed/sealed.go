package sealed

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
type Entry struct {
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
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
	var out Entry
	if sealed, derr := base64.StdEncoding.DecodeString(e.Value); derr == nil {
		if plain, derr := backup.Decrypt(sealed, key); derr == nil {
			if json.Unmarshal(plain, &out) == nil {
				return out, true, nil
			}
		}
	}
	if json.Unmarshal([]byte(e.Value), &out) == nil {
		return out, true, nil
	}
	return Entry{}, false, ErrUndecodable
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
	return k.Put(ctx, s.ns, name, v, rowExpiry(e))
}

func rowExpiry(e Entry) time.Time {
	if e.RefreshToken != "" {
		return time.Time{}
	}
	return e.Expiry
}

func (s *Store) Delete(ctx context.Context, name string) error {
	k := s.handle()
	if k == nil {
		return ErrUnavailable
	}
	return k.Delete(ctx, s.ns, name)
}
