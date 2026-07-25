package mode

import (
	"context"
	"errors"
	"testing"
)

func TestGateCLI(t *testing.T) {
	var called string
	hooks := GateHooks{
		Classify: func(context.Context) AuthState { return AuthUnauthenticated },
		CLIUnauthenticated: func(context.Context) error {
			called = "unauth"
			return nil
		},
	}
	if err := Gate(context.Background(), ModeCLI, hooks); err != nil {
		t.Fatal(err)
	}
	if called != "unauth" {
		t.Fatalf("called = %q", called)
	}
}

func TestGateCLIUnauthorizedSoft(t *testing.T) {
	hooks := GateHooks{
		Classify: func(context.Context) AuthState { return AuthUnauthorized },
		CLIUnauthorized: func(context.Context) error {
			return errors.New("not ready")
		},
		BlockingErrors: false,
	}
	if err := Gate(context.Background(), ModeCLI, hooks); err != nil {
		t.Fatalf("soft unauthorized should not fail: %v", err)
	}
}

func TestGateCLIUnauthorizedHard(t *testing.T) {
	hooks := GateHooks{
		Classify: func(context.Context) AuthState { return AuthUnauthorized },
		CLIUnauthorized: func(context.Context) error {
			return errors.New("not ready")
		},
		BlockingErrors: true,
	}
	if err := Gate(context.Background(), ModeCLI, hooks); err == nil {
		t.Fatal("hard unauthorized should fail")
	}
}

func TestGateDeck(t *testing.T) {
	called := false
	hooks := GateHooks{
		Classify: func(context.Context) AuthState { return AuthUnauthenticated },
		DeckRequire: func(context.Context) error {
			called = true
			return nil
		},
	}
	if err := Gate(context.Background(), ModeDeck, hooks); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("deck require not called")
	}
}
