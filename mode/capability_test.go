package mode

import (
	"context"
	"errors"
	"testing"
)

func TestSupportedIgnoresDaemonCapabilityForOtherModes(t *testing.T) {
	for _, m := range []Mode{ModeCLI, ModeDeck, Mode("app-defined")} {
		if !Supported(m) {
			t.Errorf("Supported(%q) = false, want true in every build", m)
		}
	}
}

func TestSupportedTracksDaemonCapability(t *testing.T) {
	for _, m := range []Mode{ModeServe, ModeDaemon} {
		if got := Supported(m); got != DaemonSupported {
			t.Errorf("Supported(%q) = %v, want %v", m, got, DaemonSupported)
		}
	}
}

func gateCallCounter(called *bool) GateHooks {
	mark := func(context.Context) error {
		*called = true
		return nil
	}
	return GateHooks{
		Classify: func(context.Context) AuthState {
			*called = true
			return AuthUnauthorized
		},
		ServeUnauthorized:  mark,
		DaemonUnauthorized: mark,
	}
}

func TestGateDaemonModesMatchCapability(t *testing.T) {
	for _, m := range []Mode{ModeServe, ModeDaemon} {
		called := false
		err := Gate(context.Background(), m, gateCallCounter(&called))
		switch {
		case DaemonSupported:
			if err != nil {
				t.Errorf("Gate(%q) = %v, want nil", m, err)
			}
			if !called {
				t.Errorf("Gate(%q) did not run its hooks", m)
			}
		default:
			if !errors.Is(err, ErrUnsupportedMode) {
				t.Errorf("Gate(%q) = %v, want ErrUnsupportedMode", m, err)
			}
			if called {
				t.Errorf("Gate(%q) ran hooks for an unsupported mode", m)
			}
		}
	}
}
