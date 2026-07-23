package daemon

import (
	"sort"
	"sync"
)

type Asset struct {
	Name  string
	MIME  string
	Bytes []byte
}

type Assets struct {
	mu sync.RWMutex
	m  map[string]Asset
}

func NewAssets() *Assets {
	return &Assets{m: make(map[string]Asset)}
}

func (a *Assets) Register(asset Asset) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.m[asset.Name] = asset
}

func (a *Assets) Get(name string) (Asset, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	asset, ok := a.m[name]
	return asset, ok
}

func (a *Assets) Names() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	names := make([]string, 0, len(a.m))
	for n := range a.m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var defaultAssets = NewAssets()

func RegisterAsset(asset Asset) { defaultAssets.Register(asset) }

func LookupAsset(name string) (Asset, bool) { return defaultAssets.Get(name) }

func AssetNames() []string { return defaultAssets.Names() }
