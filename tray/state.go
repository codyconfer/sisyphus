// Package tray models the coarse run-state a long-running process surfaces
// to the user — State and its icon Assets — and, in non-nodaemon builds,
// shows it as a system tray icon (Tray). The state/icon half is untagged so
// nodaemon consumers (status strips, desktop notifications) can still map
// states to icons.
package tray

import "sync"

// State is the coarse daemon condition surfaced to the user, typically as a
// tray icon.
type State int

// The states, in escalating order of attention required.
const (
	StateInactive State = iota
	StateRunning
	StateNotify
	StateWarn
	StateError
)

// States returns every defined State in declaration order.
func States() []State {
	return []State{StateInactive, StateRunning, StateNotify, StateWarn, StateError}
}

func (s State) String() string {
	switch s {
	case StateInactive:
		return "inactive"
	case StateRunning:
		return "running"
	case StateNotify:
		return "notify"
	case StateWarn:
		return "warn"
	case StateError:
		return "error"
	default:
		return "unknown"
	}
}

// Asset is one named binary blob (an icon, say) with its MIME type. Bytes
// are held by value so registering packages need not import their consumers.
type Asset struct {
	Name  string
	MIME  string
	Bytes []byte
}

// Icons is a concurrency-safe mapping from State to the icon Asset that
// represents it.
type Icons struct {
	mu sync.RWMutex
	m  map[State]Asset
}

// NewIcons returns an empty state-icon set.
func NewIcons() *Icons {
	return &Icons{m: make(map[State]Asset)}
}

// Set assigns asset as the icon for state, replacing any previous one.
func (si *Icons) Set(state State, asset Asset) {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.m[state] = asset
}

// Get returns the icon assigned to state.
func (si *Icons) Get(state State) (Asset, bool) {
	si.mu.RLock()
	defer si.mu.RUnlock()
	a, ok := si.m[state]
	return a, ok
}

// Missing returns the states that have no icon assigned yet, in state order.
func (si *Icons) Missing() []State {
	si.mu.RLock()
	defer si.mu.RUnlock()
	var out []State
	for _, s := range States() {
		if _, ok := si.m[s]; !ok {
			out = append(out, s)
		}
	}
	return out
}

// Complete reports whether every defined state has an icon.
func (si *Icons) Complete() bool {
	return len(si.Missing()) == 0
}

var defaultIcons = NewIcons()

// DefaultIcons returns the process-wide default state-icon set.
func DefaultIcons() *Icons { return defaultIcons }

// SetIcon assigns asset as state's icon in the process-wide default set.
func SetIcon(state State, asset Asset) { defaultIcons.Set(state, asset) }

// IconFor returns state's icon from the process-wide default set.
func IconFor(state State) (Asset, bool) { return defaultIcons.Get(state) }
