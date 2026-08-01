// Package sisyphus is a small, app-agnostic toolkit for the plumbing every
// CLI or service ends up rewriting: config reconciliation, DuckDB-backed
// stores, secret escrow, encrypted backups, and daemon primitives.
//
// The root package carries the two application-facing facades:
//
//   - ConfigStore reconciles named config documents between files on disk and
//     a versioned DuckDB store (see Open, Plan, Apply, Effective).
//   - Backup and Restore produce and consume encrypted tar archives whose AES
//     key is escrowed in a secret manager (see BackupSpec, RestoreSpec).
//
// Everything application-specific — home directory, file names, namespaces,
// key names — is passed in by the caller; nothing is baked in. Each
// sub-package (config, configdb, kv, journal, secret, sealed, backup, daemon,
// mode, lifecycle, redact, duckfile, ...) is usable on its own.
package sisyphus
