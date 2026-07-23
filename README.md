# sisyphus

> Sisyphus was condemned to roll a boulder uphill forever. This package bundles
> the boulder-chores an application repeats forever: loading configuration,
> persisting state, and making encrypted, key-escrowed backups.

`sisyphus` is a small, **app-agnostic** Go toolkit for the plumbing every CLI or
service ends up rewriting. It carries no application-specific types or names —
the caller owns its config struct, namespaces, filenames, and identifiers and
passes them in. It was extracted from [munin](https://github.com/codyconfer/munin)
but depends on nothing munin-specific.

```sh
# private module: configure Go to fetch it directly (not via the public proxy)
go env -w 'GOPRIVATE=github.com/codyconfer/*'
go get github.com/codyconfer/sisyphus
```

Building also needs git credentials with read access to the repo. DuckDB-backed
packages require CGO (via `github.com/marcboeker/go-duckdb/v2`).

## Packages

| Package | Responsibility |
|---|---|
| `sisyphus` (root) | `Manager` facade for config reconciliation + package-level `Backup`/`Restore`. |
| `sisyphus/config` | Home-dir resolution and parsing a YAML/JSON file into your struct (with env overrides). |
| `sisyphus/configdb` | Versioned, name-keyed blob store in DuckDB (`store_current` + `store_history`) — the source of truth for file-backed state. |
| `sisyphus/kv` | Generic namespaced key/value store in DuckDB, with an optional TTL column. |
| `sisyphus/journal` | Generic activity log in DuckDB: nested parent/child runs + records, each with a free-form string attribute map. |
| `sisyphus/secret` | Key escrow via the Bitwarden (`bw`) or 1Password (`op`) CLI, or the OS keyring. |
| `sisyphus/backup` | tar archive + AES-256-GCM encrypt/decrypt/restore. |

Each sub-package is usable on its own. A nil `*Store` is a valid no-op across the
DuckDB packages, so "disabled" and "open failed" behave uniformly.

## App-agnostic by design

Everything application-specific is injected, never baked in:

- **Home dir** — `config.Home(override, envVar, dirName)` takes the env var and
  directory name.
- **Config file names** — `config.ReadFile(home, basenames...)` (defaults to
  `config.{yaml,yml,json}` when none given).
- **Env prefix** — `config.ParseInto(target, raw, format, envPrefix)`.
- **KV namespace** — a parameter on every `kv` call.
- **Config DB filename** — `Options.ConfigDBName` (defaults to `config.duckdb`).
- **Keyring service name** — `secret.Resolve(backend, service)` (defaults to
  `"sisyphus"`); backup threads it via `BackupSpec.SecretService`.
- **Backup file list + secret name** — supplied on `BackupSpec` / `RestoreSpec`.

## Usage

### Config reconciliation

`Manager` makes DuckDB the source of truth for file-backed config, and never
auto-imports — you decide via a `Resolver` when file and DB disagree.

```go
m, err := sisyphus.Open(home, sisyphus.Options{}) // ModeBoth; ConfigDBName defaults to config.duckdb
if err != nil { /* ... */ }
defer m.Close()

raw, format, _ := config.ReadFile(home)
content, format, err := m.Reconcile("config", raw, format, len(raw) > 0, myResolver)
// then: config.ParseInto(&myCfg, content, format, "MYAPP_")
```

`Reconcile` returns the DB content when file and DB match, and otherwise calls
`Resolver.Resolve` with an `Action` (`ActionImport` / `ActionUseFile` /
`ActionUseDB`). `Manager.Current/Import/History` cover the common config-DB
operations; `DB()` exposes the underlying `*configdb.Store` for anything more.

### Encrypted, key-escrowed backups

```go
sealed, store, err := sisyphus.Backup(sisyphus.BackupSpec{
    Files:         []string{cfgDB, dataDB},
    SecretBackend: "auto",       // bw → op → OS keyring
    SecretService: "myapp",      // keyring service name
    SecretName:    "backup-key", // key entry name
})
// ... write `sealed` somewhere ...

names, _, err := sisyphus.Restore(sisyphus.RestoreSpec{
    Sealed: sealed, SecretBackend: "auto", SecretService: "myapp",
    SecretName: "backup-key", DestDir: home,
})
```

The AES key is generated on first backup and escrowed in the secret manager; it
never travels with the archive, and `Backup`/`Restore` are package functions
independent of `Manager` so restore works even when the config DB is corrupt.

### KV and journal

```go
store, _ := kv.Open(filepath.Join(home, "tokens.duckdb"))
_ = store.Put("tokens", "github", jsonBlob, time.Time{}) // zero time = no expiry
entry, ok, _ := store.Get("tokens", "github")

log, _ := journal.Open(filepath.Join(home, "audit.duckdb"))
parent, _ := log.Begin("job", "nightly", map[string]string{"env": "prod"})
_, _ = log.Add(journal.Run{ParentID: parent, Kind: "step", Name: "sync", Count: 3}, records)
_ = log.RollUp(parent) // roll child counts up into the parent
```

## Development

```sh
go build ./...
go vet ./...
go test ./...
```

Tests run offline. The secret backends shell out to `bw`/`op` only when present;
their availability probes are stubbable, and the keyring path is tested with
go-keyring's mock.

## License

Released under the [MIT License](LICENSE).
