// Package schedule drives periodic jobs: Run polls each Job's Next for its
// Due time on a fixed tick and executes it with per-job failure backoff;
// RunAt runs a single function at an absolute time. It carries no I/O or
// storage dependencies — persistence of "last ran" state belongs to the
// caller (see stream.Watermark).
package schedule
