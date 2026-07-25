package daemon

import "github.com/codyconfer/sisyphus/mode"

// Attached reports whether a live service socket is listening at prefix/name.
//
// It is the capability-aware form of IsListening: when mode.DaemonSupported is
// false (nodaemon builds) it always reports false, because such a binary ships
// no service to attach to. Gate optional UI and features on Attached, and reach
// for IsListening only when a raw probe is what you want regardless of build.
func Attached(prefix, name string) bool {
	if !mode.DaemonSupported {
		return false
	}
	return IsListening(prefix, name)
}
