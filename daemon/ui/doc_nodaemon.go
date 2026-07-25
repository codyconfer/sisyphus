//go:build nodaemon

// Package ui is empty in `nodaemon` builds: the system tray belongs to a
// running service, so it is compiled out and the systray dependency tree is
// left out of the binary. Desktop notifications live in sisyphus/desktop
// (untagged). Importing this package under the tag is a compile error at the
// first use, which is the intent. This file exists only so the directory still
// declares a buildable package.
package ui
