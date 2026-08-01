package stream

import (
	"context"
	"time"

	"github.com/codyconfer/sisyphus/kv"
)

// ScopedKV confines store to namespaces under prefix. The KV interface spans
// every namespace, so a component handed a raw handle could read or clear
// another component's state; scope it first when crossing a trust boundary.
// A nil store scopes to nil.
func ScopedKV(store KV, prefix string) KV {
	if store == nil {
		return nil
	}
	return scopedKV{kv: store, prefix: prefix}
}

type scopedKV struct {
	kv     KV
	prefix string
}

func (s scopedKV) scope(namespace string) string { return s.prefix + namespace }

func (s scopedKV) Get(ctx context.Context, namespace, key string) (kv.Entry, bool, error) {
	return s.kv.Get(ctx, s.scope(namespace), key)
}

func (s scopedKV) Put(ctx context.Context, namespace, key, value string, expiry time.Time) error {
	return s.kv.Put(ctx, s.scope(namespace), key, value, expiry)
}

func (s scopedKV) Delete(ctx context.Context, namespace, key string) error {
	return s.kv.Delete(ctx, s.scope(namespace), key)
}

func (s scopedKV) List(ctx context.Context, namespace string) (map[string]kv.Entry, error) {
	return s.kv.List(ctx, s.scope(namespace))
}
