//go:build !nodaemon

// Package ui puts a daemon in the system tray via fyne.io/systray: an icon
// per daemon.State, a tooltip, and a Quit menu item. Desktop notifications
// live in the untagged sisyphus/desktop package instead.
//
// Under the `nodaemon` build tag the package is empty (see doc_nodaemon.go).
package ui
