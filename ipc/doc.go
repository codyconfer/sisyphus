// Package ipc carries events between local processes over unix sockets
// (named pipes on Windows): Listen/Dial establish the transport, Broadcast
// fans a stream.Subject out to every connected peer, and IsListening probes
// for a live listener. Connections are restricted to the same user via
// peer-credential checks.
//
// The package is untagged: it is present in nodaemon builds, matching the
// sisyphus rule that only daemon/service and the systray compile out.
package ipc
