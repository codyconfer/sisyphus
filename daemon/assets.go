package daemon

import (
	"sort"
	"sync"
)

// Asset is one named binary blob (an icon, say) with its MIME type. Bytes
// are held by value so registering packages need not import their consumers.
type Asset struct {
	Name  string
	MIME  string
	Bytes []byte
}

// Assets is a concurrency-safe registry of assets keyed by Asset.Name.
type Assets struct {
	mu sync.RWMutex
	m  map[string]Asset
}

// NewAssets returns an empty registry.
func NewAssets() *Assets {
	return &Assets{m: make(map[string]Asset)}
}

// Register adds asset under its Name, replacing any previous asset with the
// same name.
func (a *Assets) Register(asset Asset) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.m[asset.Name] = asset
}

// Get returns the asset registered under name.
func (a *Assets) Get(name string) (Asset, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	asset, ok := a.m[name]
	return asset, ok
}

// Names returns the registered asset names, sorted.
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

// RegisterAsset adds asset to the process-wide default registry.
func RegisterAsset(asset Asset) { defaultAssets.Register(asset) }

// LookupAsset returns the named asset from the process-wide default registry.
func LookupAsset(name string) (Asset, bool) { return defaultAssets.Get(name) }

// AssetNames returns the sorted names in the process-wide default registry.
func AssetNames() []string { return defaultAssets.Names() }
