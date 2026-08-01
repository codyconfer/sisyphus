// Package stream provides event pipelines with resumable position state:
// polling sources (Poll, PollAdaptive, Source), fan-in and pub/sub plumbing
// (FanIn, Subject), duplicate suppression across restarts (Deduper), and the
// small KV-backed persistence primitives that make resumption work (Cursor,
// Watermark, ScopedKV over the KV interface).
//
// The package is untagged: it is present in nodaemon builds, matching the
// sisyphus rule that only daemon/service and the systray compile out.
package stream
