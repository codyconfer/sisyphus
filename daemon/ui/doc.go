//go:build !nodaemon

// Package ui is a deprecated facade: the system tray moved to sisyphus/tray,
// which also owns the daemon run-state and icon types the tray displays.
// Only alias forwarders remain here; new code should import sisyphus/tray.
//
// Under the `nodaemon` build tag the package is empty (see doc_nodaemon.go).
package ui
