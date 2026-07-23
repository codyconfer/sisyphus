package daemon

import (
	"reflect"
	"testing"
)

func TestStateStringAndSet(t *testing.T) {
	want := map[State]string{
		StateInactive: "inactive",
		StateRunning:  "running",
		StateNotify:   "notify",
		StateWarn:     "warn",
		StateError:    "error",
	}
	if len(States()) != 5 {
		t.Fatalf("States() = %d, want 5", len(States()))
	}
	for s, name := range want {
		if s.String() != name {
			t.Errorf("State(%d).String() = %q, want %q", s, s.String(), name)
		}
	}
	if State(99).String() != "unknown" {
		t.Errorf("out-of-range state should be unknown")
	}
}

func TestStateIconsRegisterGetMissing(t *testing.T) {
	si := NewStateIcons()
	if got := si.Missing(); !reflect.DeepEqual(got, States()) {
		t.Fatalf("empty registry Missing() = %v, want all states", got)
	}
	if si.Complete() {
		t.Fatal("empty registry should not be complete")
	}

	for _, s := range States() {
		si.Register(s, "image/png", []byte(s.String()))
	}
	if got := si.Missing(); got != nil {
		t.Fatalf("full registry Missing() = %v, want nil", got)
	}
	if !si.Complete() {
		t.Fatal("full registry should be complete")
	}

	a, ok := si.Get(StateWarn)
	if !ok || a.Name != "state:warn" || a.MIME != "image/png" || string(a.Bytes) != "warn" {
		t.Fatalf("Get(StateWarn) = %+v, ok=%v", a, ok)
	}
}

func TestStateIconsPartialMissing(t *testing.T) {
	si := NewStateIcons()
	si.Set(StateRunning, Asset{Name: "run", Bytes: []byte{1}})
	si.Set(StateError, Asset{Name: "err", Bytes: []byte{2}})
	got := si.Missing()
	want := []State{StateInactive, StateNotify, StateWarn}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Missing() = %v, want %v", got, want)
	}
}

func TestDefaultStateIconRegistry(t *testing.T) {
	RegisterStateIcon(StateNotify, "image/svg+xml", []byte("<svg/>"))
	got, ok := StateIcon(StateNotify)
	if !ok || got.MIME != "image/svg+xml" {
		t.Fatalf("StateIcon(StateNotify) = %+v, ok=%v", got, ok)
	}
}
