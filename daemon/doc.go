// Package daemon holds the streaming core a long-running service is built
// from: polling sources (Poll, PollAdaptive) that emit Emission batches,
// channel plumbing (FanIn, Subject, Run), deduplication with optional
// kv-backed persistence (Deduper, Cursor, Watermark), a lightweight job
// scheduler (Schedule, RunAt), and the local IPC surface — a Unix socket on
// Unix, a named pipe on Windows — used to broadcast events to attached
// clients (Listen, Broadcast, Dial).
//
// Everything here is untagged and available in `nodaemon` builds too; only
// the daemon/service and daemon/ui sub-packages are compiled out. Use
// Attached (capability-aware) rather than IsListening to gate features on a
// running service.
package daemon
