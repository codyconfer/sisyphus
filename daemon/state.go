package daemon

import "sync"

type State int

const (
	StateInactive State = iota
	StateRunning
	StateNotify
	StateWarn
	StateError
)

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

type StateIcons struct {
	mu sync.RWMutex
	m  map[State]Asset
}

func NewStateIcons() *StateIcons {
	return &StateIcons{m: make(map[State]Asset)}
}

func (si *StateIcons) Set(state State, asset Asset) {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.m[state] = asset
}

func (si *StateIcons) Register(state State, mime string, data []byte) {
	si.Set(state, Asset{Name: "state:" + state.String(), MIME: mime, Bytes: data})
}

func (si *StateIcons) Get(state State) (Asset, bool) {
	si.mu.RLock()
	defer si.mu.RUnlock()
	a, ok := si.m[state]
	return a, ok
}

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

func (si *StateIcons) Complete() bool {
	return len(si.Missing()) == 0
}

var defaultStateIcons = NewStateIcons()

func DefaultStateIcons() *StateIcons { return defaultStateIcons }

func RegisterStateIcon(state State, mime string, data []byte) {
	defaultStateIcons.Register(state, mime, data)
}

func SetStateIcon(state State, asset Asset) { defaultStateIcons.Set(state, asset) }

func StateIcon(state State) (Asset, bool) { return defaultStateIcons.Get(state) }

func MissingStateIcons() []State { return defaultStateIcons.Missing() }
