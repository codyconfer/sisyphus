//go:build !nodaemon

package mode

// DaemonSupported reports whether this build compiles in daemon support. It is
// a constant so the compiler eliminates the branches it guards, and so callers
// can drop the daemon-only half of their program from the binary.
const DaemonSupported = true
