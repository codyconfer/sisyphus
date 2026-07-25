//go:build nodaemon

package mode

// DaemonSupported is false under the `nodaemon` build tag: ModeServe and
// ModeDaemon are compiled out. See capability_daemon.go for the default build.
const DaemonSupported = false
