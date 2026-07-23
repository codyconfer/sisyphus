package daemon

import (
	"reflect"
	"testing"
)

func TestAssetsRegisterGetNames(t *testing.T) {
	a := NewAssets()
	a.Register(Asset{Name: "tray", MIME: "image/png", Bytes: []byte{1, 2, 3}})
	a.Register(Asset{Name: "notify", MIME: "image/svg+xml", Bytes: []byte("<svg/>")})

	got, ok := a.Get("tray")
	if !ok || got.MIME != "image/png" || !reflect.DeepEqual(got.Bytes, []byte{1, 2, 3}) {
		t.Fatalf("Get(tray) = %+v, ok=%v", got, ok)
	}
	if _, ok := a.Get("missing"); ok {
		t.Fatal("Get(missing) should report not found")
	}
	if names := a.Names(); !reflect.DeepEqual(names, []string{"notify", "tray"}) {
		t.Fatalf("Names() = %v, want sorted [notify tray]", names)
	}
}

func TestAssetsRegisterOverwrites(t *testing.T) {
	a := NewAssets()
	a.Register(Asset{Name: "icon", Bytes: []byte("v1")})
	a.Register(Asset{Name: "icon", Bytes: []byte("v2")})
	got, _ := a.Get("icon")
	if string(got.Bytes) != "v2" {
		t.Fatalf("overwrite failed: %q", got.Bytes)
	}
	if len(a.Names()) != 1 {
		t.Fatalf("Names() = %v, want 1", a.Names())
	}
}

func TestDefaultAssetRegistry(t *testing.T) {
	RegisterAsset(Asset{Name: "pkg-level", MIME: "image/png", Bytes: []byte{9}})
	got, ok := LookupAsset("pkg-level")
	if !ok || got.Bytes[0] != 9 {
		t.Fatalf("LookupAsset = %+v, ok=%v", got, ok)
	}
	found := false
	for _, n := range AssetNames() {
		if n == "pkg-level" {
			found = true
		}
	}
	if !found {
		t.Fatalf("AssetNames() missing pkg-level: %v", AssetNames())
	}
}
