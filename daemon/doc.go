// Package daemon holds what is genuinely daemon-flavored: SignalContext for
// shutdown-signal handling and Attached, the capability-aware probe for a
// running service. The service manager lives in daemon/service.
//
// Everything else that used to live here has moved to semantic packages —
// event pipelines to sisyphus/stream, socket/pipe transport to sisyphus/ipc,
// periodic jobs to sisyphus/schedule, and run-state icons plus the systray to
// sisyphus/tray.
package daemon
