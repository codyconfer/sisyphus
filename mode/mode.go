package mode

import "context"

// Mode names the operating surface of an application. Apps choose which modes
// they expose; the gate framework is mode-agnostic.
type Mode string

const (
	ModeCLI    Mode = "cli"
	ModeServe  Mode = "serve"
	ModeDaemon Mode = "daemon"
	ModeDeck   Mode = "deck"
)

// AuthState is the coarse authorization classification used by Gate.
type AuthState int

const (
	AuthUnauthenticated AuthState = iota
	AuthUnauthorized
	AuthAuthorized
)

// GateHooks are injectable policy callbacks. GitHub (or other) policy lives in
// the app; sisyphus only orchestrates the per-mode gate flow.
type GateHooks struct {
	// Classify returns the current auth state. Required.
	Classify func(ctx context.Context) AuthState

	// CLIUnauthenticated runs guided auth for CLI mode (e.g. login + onboard).
	CLIUnauthenticated func(ctx context.Context) error
	// CLIUnauthorized is called when authenticated but not fully authorized.
	// If EnforceHard is true and this returns an error, the gate fails.
	CLIUnauthorized func(ctx context.Context) error
	// EnforceHard, when true, treats CLIUnauthorized errors as fatal.
	EnforceHard bool

	// ServeUnauthorized warns (non-fatal) when serve mode is not authorized.
	ServeUnauthorized func(ctx context.Context) error
	// DaemonUnauthorized warns (non-fatal) when daemon mode is not authorized.
	DaemonUnauthorized func(ctx context.Context) error
	// DeckRequire runs the interactive onboarding requirement for deck mode.
	DeckRequire func(ctx context.Context) error
}

// Gate applies mode-specific authorization policy via hooks.
func Gate(ctx context.Context, m Mode, hooks GateHooks) error {
	if hooks.Classify == nil {
		return nil
	}
	state := hooks.Classify(ctx)
	switch m {
	case ModeServe:
		if state != AuthAuthorized && hooks.ServeUnauthorized != nil {
			return hooks.ServeUnauthorized(ctx)
		}
		return nil
	case ModeDaemon:
		if state != AuthAuthorized && hooks.DaemonUnauthorized != nil {
			return hooks.DaemonUnauthorized(ctx)
		}
		return nil
	case ModeDeck:
		if hooks.DeckRequire != nil {
			return hooks.DeckRequire(ctx)
		}
		return nil
	default: // ModeCLI and unknown
		switch state {
		case AuthUnauthenticated:
			if hooks.CLIUnauthenticated != nil {
				return hooks.CLIUnauthenticated(ctx)
			}
			return nil
		case AuthUnauthorized:
			if hooks.CLIUnauthorized != nil {
				err := hooks.CLIUnauthorized(ctx)
				if hooks.EnforceHard {
					return err
				}
				return nil
			}
			return nil
		default:
			return nil
		}
	}
}
