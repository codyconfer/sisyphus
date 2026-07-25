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

func TestGateCLIUnauthorizedDefault(t *testing.T) {
	hooks := GateHooks{
		Classify: func(context.Context) AuthState { return AuthUnauthorized },
		CLIUnauthorized: func(context.Context) error {
			return errors.New("not ready")
		},
		AllOrNothingAuth: false,
	}
	if err := Gate(context.Background(), ModeCLI, hooks); err != nil {
		t.Fatalf("default unauthorized should not fail: %v", err)
	}
}

func TestGateCLIUnauthorizedAllOrNothing(t *testing.T) {
	hooks := GateHooks{
		Classify: func(context.Context) AuthState { return AuthUnauthorized },
		CLIUnauthorized: func(context.Context) error {
			return errors.New("not ready")
		},
		AllOrNothingAuth: true,
	}
	if err := Gate(context.Background(), ModeCLI, hooks); err == nil {
		t.Fatal("all-or-nothing auth should reject unauthorized cli")
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
