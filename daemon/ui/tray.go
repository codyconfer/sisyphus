//go:build !nodaemon

package ui

import "github.com/codyconfer/sisyphus/tray"

// Deprecated forwarders: the systray moved to sisyphus/tray. Delete this
// file once consumers import the new package.

// Tray forwards to its new home.
//
// Deprecated: moved to sisyphus/tray.
type Tray = tray.Tray

// TrayConfig forwards to its new home.
//
// Deprecated: moved to sisyphus/tray.
type TrayConfig = tray.Config

// NewTray forwards to its new home.
//
// Deprecated: moved to sisyphus/tray.
func NewTray(cfg tray.Config) *tray.Tray { return tray.NewTray(cfg) }
