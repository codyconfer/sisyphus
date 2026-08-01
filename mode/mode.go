package mode

import (
	"context"
	"errors"
	"fmt"
)

// Mode names the operating surface of an application. Apps choose which modes
// they expose; the gate framework is mode-agnostic.
type Mode string

const (
	// ModeCLI is a one-shot foreground command.
	ModeCLI Mode = "cli"
	// ModeServe is a long-running foreground server; needs daemon support.
	ModeServe Mode = "serve"
	// ModeDaemon is a background service; needs daemon support.
	ModeDaemon Mode = "daemon"
	// ModeDeck is an interactive dashboard surface.
	ModeDeck Mode = "deck"
)

// ErrUnsupportedMode is returned by Gate for a mode this build cannot run.
var ErrUnsupportedMode = errors.New("mode: unsupported by this build")

// Supported reports whether m can run in this build. ModeServe and ModeDaemon
// need daemon support and are unavailable under the `nodaemon` build tag; every
// other mode, including app-defined ones, is always supported.
func Supported(m Mode) bool {
	switch m {
	case ModeServe, ModeDaemon:
		return DaemonSupported
	default:
		return true
	}
}

// AuthState is the coarse authorization classification used by Gate.
type AuthState int

const (
	// AuthUnauthenticated means no valid identity or login.
	AuthUnauthenticated AuthState = iota
	// AuthUnauthorized means authenticated but missing a required approval,
	// membership, scope, or onboarding step.
	AuthUnauthorized
	// AuthAuthorized means fully allowed.
	AuthAuthorized
)

// Policy decides what happens with the error a CLIUnauthorized hook returns.
type Policy int

const (
	// PolicyWarn (the zero value) discards the CLIUnauthorized error and lets
	// the command continue.
	PolicyWarn Policy = iota
	// PolicyBlock propagates the CLIUnauthorized error and blocks execution.
	PolicyBlock
)

// GateHooks are injectable policy callbacks. GitHub (or other) policy lives in
// the app; sisyphus only orchestrates the per-mode gate flow.
type GateHooks struct {
	// Classify returns the current auth state. Required.
	Classify func(ctx context.Context) AuthState

	// CLIUnauthenticated runs guided auth for CLI mode (e.g. login + onboard).
	CLIUnauthenticated func(ctx context.Context) error
	// CLIUnauthorized is called when authenticated but not fully authorized.
	// If UnauthorizedPolicy is PolicyBlock and this returns an error, the
	// gate fails.
	CLIUnauthorized func(ctx context.Context) error
	// UnauthorizedPolicy, when PolicyBlock, makes CLIUnauthorized errors block
	// execution. The default, PolicyWarn, discards them.
	UnauthorizedPolicy Policy

	// ServeUnauthorized warns (non-fatal) when serve mode is not authorized.
	ServeUnauthorized func(ctx context.Context) error
	// DaemonUnauthorized warns (non-fatal) when daemon mode is not authorized.
	DaemonUnauthorized func(ctx context.Context) error
	// DeckRequire runs the interactive onboarding requirement for deck mode.
	DeckRequire func(ctx context.Context) error
}

// Gate applies mode-specific authorization policy via hooks. It fails closed
// on a mode this build does not support, wrapping ErrUnsupportedMode, so a
// daemon-free binary cannot be talked into running daemon-mode policy.
func Gate(ctx context.Context, m Mode, hooks GateHooks) error {
	if !Supported(m) {
		return fmt.Errorf("%w: %s", ErrUnsupportedMode, m)
	}
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
	default:
		switch state {
		case AuthUnauthenticated:
			if hooks.CLIUnauthenticated != nil {
				return hooks.CLIUnauthenticated(ctx)
			}
			return nil
		case AuthUnauthorized:
			if hooks.CLIUnauthorized != nil {
				err := hooks.CLIUnauthorized(ctx)
				if hooks.UnauthorizedPolicy == PolicyBlock {
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
