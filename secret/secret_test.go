package secret

import (
	"errors"
	"strings"
	"testing"
)

func TestOpSetKeepsSecretOffArgv(t *testing.T) {
	const secret = "super-secret-value"
	args := opCreateArgs()
	for _, a := range args {
		if strings.Contains(a, secret) {
			t.Fatalf("secret leaked into argv: %q", a)
		}
	}
	tmpl, err := opTemplate("mykey", secret)
	if err != nil {
		t.Fatalf("opTemplate: %v", err)
	}
	if !strings.Contains(string(tmpl), secret) {
		t.Fatal("template should carry the secret on stdin")
	}
}

var errFake = errors.New("fake")

type fakeStore struct {
	m       map[string]string
	setErr  error
	getErr  error
	setHits int
}

func (f *fakeStore) Name() string { return "fake" }
func (f *fakeStore) Get(k string) (string, bool, error) {
	if f.getErr != nil {
		return "", false, f.getErr
	}
	v, ok := f.m[k]
	return v, ok, nil
}
func (f *fakeStore) Set(k, v string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.m[k] = v
	f.setHits++
	return nil
}

func stub(bw, op bool) func() {
	origBW, origOP := bwAvailable, opAvailable
	bwAvailable = func() bool { return bw }
	opAvailable = func() bool { return op }
	return func() { bwAvailable, opAvailable = origBW, origOP }
}

func TestResolveAutoPriority(t *testing.T) {
	defer stub(true, true)()
	if s, err := Resolve("auto", ""); err != nil || s.Name() != "bitwarden" {
		t.Fatalf("auto with bw+op = %v, %v", s, err)
	}
}

func TestResolveAutoFallsThrough(t *testing.T) {
	restore := stub(false, true)
	if s, err := Resolve("", ""); err != nil || s.Name() != "1password" {
		t.Fatalf("auto with op only = %v, %v", s, err)
	}
	restore()

	defer stub(false, false)()
	if s, err := Resolve("auto", ""); err != nil || s.Name() != "os-keyring" {
		t.Fatalf("auto with neither = %v, %v", s, err)
	}
}

func TestResolveExplicitUnconfigured(t *testing.T) {
	defer stub(false, false)()
	if _, err := Resolve("bitwarden", ""); err == nil {
		t.Error("explicit bitwarden when unavailable should error")
	}
	if _, err := Resolve("1password", ""); err == nil {
		t.Error("explicit 1password when unavailable should error")
	}
	if _, err := Resolve("nonsense", ""); err == nil {
		t.Error("unknown backend should error")
	}
}

func TestKeyringServiceInjected(t *testing.T) {
	defer stub(false, false)()
	s, err := Resolve("keyring", "myapp")
	if err != nil {
		t.Fatalf("Resolve keyring: %v", err)
	}
	ks, ok := s.(keyringStore)
	if !ok {
		t.Fatalf("want keyringStore, got %T", s)
	}
	if ks.service != "myapp" {
		t.Errorf("service = %q, want myapp", ks.service)
	}
	if got := newKeyringStore("").service; got != "sisyphus" {
		t.Errorf("default service = %q, want sisyphus", got)
	}
}

func TestGetOrCreate(t *testing.T) {
	fs := &fakeStore{m: map[string]string{}}
	genCalls := 0
	gen := func() (string, error) { genCalls++; return "generated", nil }

	v, err := GetOrCreate(fs, "k", gen)
	if err != nil || v != "generated" || fs.setHits != 1 {
		t.Fatalf("first GetOrCreate = %q, %v (sets=%d)", v, err, fs.setHits)
	}
	v, err = GetOrCreate(fs, "k", gen)
	if err != nil || v != "generated" || genCalls != 1 || fs.setHits != 1 {
		t.Fatalf("second GetOrCreate regenerated: v=%q gen=%d sets=%d", v, genCalls, fs.setHits)
	}
}

func TestGetOrCreateReadErrorDoesNotMint(t *testing.T) {
	fs := &fakeStore{m: map[string]string{}, getErr: errFake}
	genCalls := 0
	gen := func() (string, error) { genCalls++; return "generated", nil }
	if _, err := GetOrCreate(fs, "k", gen); err == nil {
		t.Fatal("want error when Get fails")
	}
	if genCalls != 0 || fs.setHits != 0 {
		t.Errorf("must not mint on read error: gen=%d sets=%d", genCalls, fs.setHits)
	}
}

func TestOpNotFoundClassifier(t *testing.T) {
	cases := map[string]bool{
		`"x" isn't an item`: true,
		"item not found":    true,
		"no item matching":  true,
		"doesn't exist":     true,
		"connection refused": false,
		"vault is locked":    false,
	}
	for in, want := range cases {
		if got := opNotFound(in); got != want {
			t.Errorf("opNotFound(%q) = %v, want %v", in, got, want)
		}
	}
}
