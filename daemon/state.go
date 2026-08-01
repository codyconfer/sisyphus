package daemon

import "sync"

// State is the coarse daemon condition surfaced to the user, typically as a
// tray icon (see daemon/ui).
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

// StateIcons is a concurrency-safe mapping from State to the icon Asset that
// represents it.
type StateIcons struct {
	mu sync.RWMutex
	m  map[State]Asset
}

// NewStateIcons returns an empty state-icon set.
func NewStateIcons() *StateIcons {
	return &StateIcons{m: make(map[State]Asset)}
}

// Set assigns asset as the icon for state, replacing any previous one.
func (si *StateIcons) Set(state State, asset Asset) {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.m[state] = asset
}

// Register builds an Asset named "state:<state>" from mime and data and
// assigns it as the icon for state.
func (si *StateIcons) Register(state State, mime string, data []byte) {
	si.Set(state, Asset{Name: "state:" + state.String(), MIME: mime, Bytes: data})
}

// Get returns the icon assigned to state.
func (si *StateIcons) Get(state State) (Asset, bool) {
	si.mu.RLock()
	defer si.mu.RUnlock()
	a, ok := si.m[state]
	return a, ok
}

// Missing returns the states that have no icon assigned yet, in state order.
func (si *StateIcons) Missing() []State {
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
func (si *StateIcons) Complete() bool {
	return len(si.Missing()) == 0
}

var defaultStateIcons = NewStateIcons()

// DefaultStateIcons returns the process-wide default state-icon set.
func DefaultStateIcons() *StateIcons { return defaultStateIcons }

// RegisterStateIcon builds and assigns an icon for state in the process-wide
// default set.
func RegisterStateIcon(state State, mime string, data []byte) {
	defaultStateIcons.Register(state, mime, data)
}

// SetStateIcon assigns asset as state's icon in the process-wide default set.
func SetStateIcon(state State, asset Asset) { defaultStateIcons.Set(state, asset) }

// StateIcon returns state's icon from the process-wide default set.
func StateIcon(state State) (Asset, bool) { return defaultStateIcons.Get(state) }

// MissingStateIcons returns the states missing icons in the process-wide
// default set.
func MissingStateIcons() []State { return defaultStateIcons.Missing() }
