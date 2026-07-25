# sisyphus

[![CI](https://github.com/codyconfer/sisyphus/actions/workflows/ci.yml/badge.svg)](https://github.com/codyconfer/sisyphus/actions/workflows/ci.yml)

`sisyphus` is a small, **app-agnostic** Go toolkit for the plumbing every CLI or
service ends up rewriting. It carries no application-specific types or names —
the caller owns its config struct, namespaces, filenames, and identifiers and
passes them in. It was extracted from [munin](https://github.com/codyconfer/munin)
but depends on nothing munin-specific.

```sh
go get github.com/codyconfer/sisyphus
```

The module is available through the standard Go module proxy and checksum
database. DuckDB-backed packages require CGO (via
`github.com/marcboeker/go-duckdb/v2`).

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
| `sisyphus/store` | Ad-hoc DuckDB file queries (read-only at the app layer). |
| `sisyphus/sealed` | Encrypted credential store (AES-GCM over `kv`; key in OS keyring). |
| `sisyphus/auth` | OAuth loopback · device flow · `RunTool` CLI helper. |
| `sisyphus/mode` | Operating modes + injectable auth gate hooks. |
| `sisyphus/lifecycle` | Home-dir install / clean / nuke primitives. |
| `sisyphus/daemon` | Streaming core: poll/fan-in/dedupe, sockets (pipe-prefix param on Windows), cursors. |
| `sisyphus/daemon/service` | OS service install/start/stop wrapper. |
| `sisyphus/daemon/ui` | System tray + desktop notifications. |

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
ctx := context.Background()
m, err := sisyphus.Open(ctx, home, sisyphus.Options{}) // ModeBoth; ConfigDBName defaults to config.duckdb
if err != nil { /* ... */ }
defer m.Close()

raw, format, _ := config.ReadFile(home)
content, format, err := m.Reconcile(ctx, "config", raw, format, len(raw) > 0, myResolver)
// then: config.ParseInto(&myCfg, content, format, "MYAPP_")
```

`Reconcile` returns the DB content when file and DB match, and otherwise calls
`Resolver.Resolve` with an `Action` (`ActionImport` / `ActionUseFile` /
`ActionUseDB`). `Manager.Current/Import/History` cover the common config-DB
operations; `DB()` exposes the underlying `*configdb.Store` for anything more.

### Authorization gates

The `mode` package runs authorization policy supplied by your application; it
does not decide who is authorized. Your `GateHooks.Classify` callback maps the
current account state to:

- `AuthUnauthenticated` — no valid identity or login.
- `AuthUnauthorized` — authenticated, but missing a required approval,
  membership, scope, or onboarding step.
- `AuthAuthorized` — fully allowed.

`AllOrNothingAuth` is not a global "require authentication" switch. It affects
only an unauthorized CLI user: when `CLIUnauthorized` returns an error,
`AllOrNothingAuth: true` propagates that error and blocks the command;
`AllOrNothingAuth: false` discards it and allows the command to continue.

| Mode and state | `Gate` behavior |
|---|---|
| CLI, unauthenticated | Runs `CLIUnauthenticated`; any error blocks. |
| CLI, unauthorized, default policy | Runs `CLIUnauthorized`, discards its error, and continues. |
| CLI, unauthorized, all-or-nothing auth | Runs `CLIUnauthorized`; any error blocks. |
| CLI, authorized | Continues without calling an auth hook. |
| Serve or daemon, not authorized | Runs the corresponding hook; return `nil` to warn and continue, or an error to block. |
| Deck, any state | Always runs `DeckRequire` when that hook is provided. |

```go
err := mode.Gate(ctx, mode.ModeCLI, mode.GateHooks{
    Classify: func(ctx context.Context) mode.AuthState {
        return classifyMyAccount(ctx) // application-specific policy
    },
    CLIUnauthenticated: loginAndOnboard,
    CLIUnauthorized: func(context.Context) error {
        return errors.New("account is not approved")
    },
    AllOrNothingAuth: true,
})
if err != nil {
    return err // stop before running the command
}
```

The gate allows execution when `Classify` is nil, when the applicable hook is
nil, or when a blocking hook returns `nil`. Applications requiring strict
authorization should supply every relevant hook, return explicit denial errors,
and stop whenever `Gate` returns an error. OAuth flows in `auth` establish
credentials; the application still decides whether those credentials are
authorized.

### Encrypted, key-escrowed backups

```go
ctx := context.Background()
sealed, store, err := sisyphus.Backup(ctx, sisyphus.BackupSpec{
    Files:         []string{cfgDB, dataDB},
    SecretBackend: "auto",       // bw → op → OS keyring
    SecretService: "myapp",      // keyring service name
    SecretName:    "backup-key", // key entry name
})
// ... write `sealed` somewhere ...

names, _, err := sisyphus.Restore(ctx, sisyphus.RestoreSpec{
    Sealed: sealed, SecretBackend: "auto", SecretService: "myapp",
    SecretName: "backup-key", DestDir: home,
})
```

The AES key is generated on first backup and escrowed in the secret manager; it
never travels with the archive, and `Backup`/`Restore` are package functions
independent of `Manager` so restore works even when the config DB is corrupt.

### KV and journal

```go
ctx := context.Background()
store, _ := kv.Open(ctx, filepath.Join(home, "tokens.duckdb"))
_ = store.Put(ctx, "tokens", "github", jsonBlob, time.Time{}) // zero time = no expiry
entry, ok, _ := store.Get(ctx, "tokens", "github")

log, _ := journal.Open(ctx, filepath.Join(home, "audit.duckdb"))
parent, _ := log.Begin(ctx, "job", "nightly", map[string]string{"env": "prod"})
_, _ = log.Add(ctx, journal.Run{ParentID: parent, Kind: "step", Name: "sync", Count: 3}, records)
_ = log.RollUp(ctx, parent) // roll child counts up into the parent
```

## Development

```sh
make build          # go build ./...
make check          # build + fmt-check + lint + govulncheck + test (CI gate is `make ci`)
make test           # go test ./...
```

Linters live in the nested `tools/` module (`go tool -modfile=tools/go.mod`) so
they stay out of the consumer dependency graph.

Tests run offline. The secret backends shell out to `bw`/`op` only when present;
their availability probes are stubbable, and the keyring path is tested with
go-keyring's mock.

### Local multi-repo development (`go.work`)

When editing sisyphus alongside munin/viewkit, use an **uncommitted** `go.work`
in the consumer (typically munin) that `use`s the sibling checkouts. Do not
commit `go.work` / `go.work.sum` (gitignored here) and do not add committed
`replace` directives — CI and published consumers build against tagged pins.

## License

Released under the [MIT License](LICENSE).
