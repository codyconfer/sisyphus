// Package mode names an application's operating surfaces (CLI, serve,
// daemon, deck, or app-defined) and runs the application's own authorization
// policy at the right moment through Gate.
//
// The package never decides who is authorized: the GateHooks.Classify
// callback maps the current account state to AuthUnauthenticated,
// AuthUnauthorized, or AuthAuthorized, and Gate dispatches on mode and state:
//
//   - CLI, unauthenticated: runs CLIUnauthenticated; any error blocks.
//   - CLI, unauthorized: runs CLIUnauthorized; with PolicyBlock its error
//     blocks, with PolicyWarn (the zero value) the error is discarded and the
//     command continues.
//   - CLI, authorized: continues without calling an auth hook.
//   - Serve or daemon, not authorized: runs ServeUnauthorized or
//     DaemonUnauthorized; return nil to warn and continue, an error to block.
//   - Deck, any state: always runs DeckRequire when provided.
//   - Serve or daemon in a nodaemon build: fails with ErrUnsupportedMode
//     without calling any hook.
//
// Gate allows execution when Classify is nil, when the applicable hook is
// nil, or when a blocking hook returns nil — strict applications must supply
// every relevant hook and return explicit denial errors.
//
// The package is also the single build-tag source for daemon capability: the
// DaemonSupported constant is true by default and false under the `nodaemon`
// tag, letting the compiler drop the daemon half of a program.
package mode
