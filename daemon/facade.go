package daemon

// Deprecated forwarders for the identifiers that moved out of this package
// in the stream/ipc/schedule/tray split. Every declaration here forwards
// unchanged; delete this file once consumers import the new packages.

import (
	"context"
	"net"
	"time"

	"github.com/codyconfer/sisyphus/ipc"
	"github.com/codyconfer/sisyphus/schedule"
	"github.com/codyconfer/sisyphus/stream"
	"github.com/codyconfer/sisyphus/tray"
)

// --- moved to sisyphus/stream ---

// Emission forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
type Emission[T any] = stream.Emission[T]

// Deduper forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
type Deduper[T any] = stream.Deduper[T]

// Subject forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
type Subject[T any] = stream.Subject[T]

// Source forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
type Source[T any] = stream.Source[T]

// KV forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
type KV = stream.KV

// Cursor forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
type Cursor = stream.Cursor

// Watermark forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
type Watermark = stream.Watermark

// FanIn forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
func FanIn[T any](ctx context.Context, chans ...<-chan T) <-chan T {
	return stream.FanIn(ctx, chans...)
}

// NewDeduper forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
func NewDeduper[T any](key func(T) string) *stream.Deduper[T] {
	return stream.NewDeduper(key)
}

// NewPersistentDeduper forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
func NewPersistentDeduper[T any](ctx context.Context, key func(T) string, store stream.KV, namespace string) *stream.Deduper[T] {
	return stream.NewPersistentDeduper(ctx, key, store, namespace)
}

// NewSubject forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
func NewSubject[T any]() *stream.Subject[T] { return stream.NewSubject[T]() }

// NewCursor forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
func NewCursor(store stream.KV, namespace, key string) *stream.Cursor {
	return stream.NewCursor(store, namespace, key)
}

// NewWatermark forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
func NewWatermark(store stream.KV, namespace, key string) *stream.Watermark {
	return stream.NewWatermark(store, namespace, key)
}

// ScopedKV forwards to its new home.
//
// Deprecated: moved to sisyphus/stream.
func ScopedKV(store stream.KV, prefix string) stream.KV {
	return stream.ScopedKV(store, prefix)
}

// --- moved to sisyphus/ipc ---

// ErrInUse forwards to its new home.
//
// Deprecated: moved to sisyphus/ipc.
var ErrInUse = ipc.ErrInUse

// Listen forwards to its new home.
//
// Deprecated: moved to sisyphus/ipc.
func Listen(prefix, name string) (net.Listener, error) { return ipc.Listen(prefix, name) }

// IsListening forwards to its new home.
//
// Deprecated: moved to sisyphus/ipc.
func IsListening(prefix, name string) bool { return ipc.IsListening(prefix, name) }

// Broadcast forwards to its new home.
//
// Deprecated: moved to sisyphus/ipc.
func Broadcast[T any](ctx context.Context, ln net.Listener, subj *stream.Subject[T], buffer int, encode func(T) ([]byte, error)) {
	ipc.Broadcast(ctx, ln, subj, buffer, encode)
}

// Dial forwards to its new home.
//
// Deprecated: moved to sisyphus/ipc.
func Dial[T any](ctx context.Context, prefix, name string, decode func([]byte) (T, error), opts ...ipc.DialOption) (<-chan T, error) {
	return ipc.Dial(ctx, prefix, name, decode, opts...)
}

// --- moved to sisyphus/schedule ---

// ScheduleJob forwards to its new home.
//
// Deprecated: moved to sisyphus/schedule (schedule.Job).
type ScheduleJob = schedule.Job

// Due forwards to its new home.
//
// Deprecated: moved to sisyphus/schedule.
type Due = schedule.Due

// Schedule forwards to its new home.
//
// Deprecated: moved to sisyphus/schedule (schedule.Run).
func Schedule(ctx context.Context, interval time.Duration, jobs ...schedule.Job) error {
	return schedule.Run(ctx, interval, jobs...)
}

// --- moved to sisyphus/tray ---

// State forwards to its new home.
//
// Deprecated: moved to sisyphus/tray.
type State = tray.State

// Asset forwards to its new home.
//
// Deprecated: moved to sisyphus/tray.
type Asset = tray.Asset

// StateIcons forwards to its new home.
//
// Deprecated: moved to sisyphus/tray (tray.Icons).
type StateIcons = tray.Icons

// StateInactive and friends forward to their new home.
//
// Deprecated: moved to sisyphus/tray.
const (
	StateInactive = tray.StateInactive
	StateRunning  = tray.StateRunning
	StateNotify   = tray.StateNotify
	StateWarn     = tray.StateWarn
	StateError    = tray.StateError
)

// States forwards to its new home.
//
// Deprecated: moved to sisyphus/tray.
func States() []tray.State { return tray.States() }

// DefaultStateIcons forwards to its new home.
//
// Deprecated: moved to sisyphus/tray (tray.DefaultIcons).
func DefaultStateIcons() *tray.Icons { return tray.DefaultIcons() }

// SetStateIcon forwards to its new home.
//
// Deprecated: moved to sisyphus/tray (tray.SetIcon).
func SetStateIcon(state tray.State, asset tray.Asset) { tray.SetIcon(state, asset) }

// StateIcon forwards to its new home.
//
// Deprecated: moved to sisyphus/tray (tray.IconFor).
func StateIcon(state tray.State) (tray.Asset, bool) { return tray.IconFor(state) }
