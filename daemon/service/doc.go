//go:build !nodaemon

// Package service wraps kardianos/service to install, start, stop and run a
// program as an OS service (systemd, launchd, Windows SCM, ...), system-wide
// or per-user. Unlike the raw library, a work function that fails takes the
// service down with it: Run unwinds and returns the error so the supervisor
// can restart the process.
//
// Under the `nodaemon` build tag the package is empty (see doc_nodaemon.go).
package service
