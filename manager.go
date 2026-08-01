package sisyphus

import (
	"context"
	"path/filepath"

	"github.com/codyconfer/sisyphus/config"
	"github.com/codyconfer/sisyphus/configdb"
)

// Backend selects where a ConfigStore keeps config documents.
type Backend int

const (
	// BackendBoth keeps files and the DB reconciled against each other.
	BackendBoth Backend = iota
	// BackendFiles reads files only; no database is opened.
	BackendFiles
	// BackendDB reads the database only.
	BackendDB
)

const defaultConfigDBName = "config.duckdb"

// Options configures Open.
type Options struct {
	// Backend selects files, DB, or both (the zero value, BackendBoth).
	Backend Backend
	// ConfigDBName is the config database filename inside home. Empty means
	// "config.duckdb".
	ConfigDBName string
}

// ConfigStore is the facade over an application's config home: named config
// documents on disk, versioned snapshots in a DuckDB store, or both,
// reconciled through Plan/Apply/Effective.
//
// A ConfigStore opened with BackendFiles carries no database: reads report
// absent with a nil error, Import and Forget return configdb.ErrUnavailable,
// and Close returns nil (as it does on a nil *ConfigStore).
type ConfigStore struct {
	home    string
	backend Backend
	cfgdb   *configdb.Store
}

// Open returns a ConfigStore rooted at home. Unless opts.Backend is
// BackendFiles it opens (creating if needed) the config database inside home,
// named by opts.ConfigDBName ("config.duckdb" when empty).
func Open(ctx context.Context, home string, opts Options) (*ConfigStore, error) {
	m := &ConfigStore{home: home, backend: opts.Backend}
	if opts.Backend == BackendFiles {
		return m, nil
	}
	name := opts.ConfigDBName
	if name == "" {
		name = defaultConfigDBName
	}
	cdb, err := configdb.Open(ctx, filepath.Join(home, name))
	if err != nil {
		return nil, err
	}
	m.cfgdb = cdb
	return m, nil
}

// Backend reports which backend this store was opened with.
func (m *ConfigStore) Backend() Backend { return m.backend }

// Home returns the home directory the store was opened at.
func (m *ConfigStore) Home() string { return m.home }

// Current returns the stored current snapshot of the named document. Without
// a database (BackendFiles) it reports absent with a nil error.
func (m *ConfigStore) Current(ctx context.Context, name string) (Snapshot, bool, error) {
	return m.cfgdb.Current(ctx, name)
}

// Import stores content as the named document's new current snapshot,
// archiving the previous one into its history. Without a database
// (BackendFiles) it returns configdb.ErrUnavailable.
func (m *ConfigStore) Import(ctx context.Context, name string, content []byte, format config.Format) error {
	return m.cfgdb.Import(ctx, name, content, format)
}

// History returns up to limit archived snapshots of the named document,
// newest first (limit <= 0 means 50). Without a database (BackendFiles) it
// returns nil with a nil error.
func (m *ConfigStore) History(ctx context.Context, name string, limit int) ([]Snapshot, error) {
	return m.cfgdb.History(ctx, name, limit)
}

// Forget drops a document's current snapshot and history from the store.
func (m *ConfigStore) Forget(ctx context.Context, name string) error {
	return m.cfgdb.Forget(ctx, name)
}

// Generation reports the store's change marker: an opaque value that changes
// with every committed write, so pollers can detect change without opening
// the database.
func (m *ConfigStore) Generation() (string, bool) {
	return m.cfgdb.Generation()
}

// Close releases the underlying config database. It is safe on a nil
// *ConfigStore and on one opened with BackendFiles; both return nil.
func (m *ConfigStore) Close() error {
	if m == nil {
		return nil
	}
	return m.cfgdb.Close()
}

// Action is the caller's decision for one drifted document, passed to Apply.
type Action int

const (
	// ActionImport stores the file content as the new DB snapshot and uses it.
	ActionImport Action = iota
	// ActionUseFile uses the file content without touching the DB.
	ActionUseFile
	// ActionUseDB uses the stored DB snapshot, ignoring the file.
	ActionUseDB
)

// Snapshot is one stored content snapshot of a named config document.
type Snapshot = configdb.Snapshot

// Item is one named config document to reconcile against the DB.
type Item struct {
	Name        string
	FileContent []byte // empty = no file on disk
	FileFormat  config.Format
}

func (it Item) hasFile() bool { return len(it.FileContent) > 0 }

// Reconciliation is one drifted document reported by Plan: the file-side
// content next to the stored DB snapshot (zero when only one side exists),
// awaiting a caller decision via Apply.
type Reconciliation struct {
	Name        string
	FileContent []byte
	FileFormat  config.Format
	DB          Snapshot
}

// HasDB reports whether a stored snapshot exists for this item. A zero DB
// Snapshot expresses absence; stored snapshots always carry a non-empty hash.
func (r Reconciliation) HasDB() bool { return r.DB.Hash != "" }

// HasFile reports whether a file version exists for this item.
func (r Reconciliation) HasFile() bool { return len(r.FileContent) > 0 }

// Plan returns one Reconciliation per item that has drifted: the file differs
// from the DB snapshot, or the item exists on only one side. Items in sync
// (or absent on both sides) are omitted. In BackendFiles and BackendDB it
// returns nil: there is nothing to reconcile, and Effective resolves content
// per the backend.
func (m *ConfigStore) Plan(ctx context.Context, items ...Item) ([]Reconciliation, error) {
	if m.backend != BackendBoth {
		return nil, nil
	}
	var out []Reconciliation
	for _, it := range items {
		cur, hasCur, err := m.cfgdb.Current(ctx, it.Name)
		if err != nil {
			return nil, err
		}
		if !it.hasFile() && !hasCur {
			continue
		}
		if it.hasFile() && hasCur && cur.Hash == configdb.Hash(it.FileFormat, it.FileContent) {
			continue
		}
		rec := Reconciliation{Name: it.Name, FileContent: it.FileContent, FileFormat: it.FileFormat}
		if hasCur {
			rec.DB = cur
		}
		out = append(out, rec)
	}
	return out, nil
}

// Apply resolves one planned Reconciliation with the caller's decision and
// returns the effective content and format.
func (m *ConfigStore) Apply(ctx context.Context, rec Reconciliation, act Action) (content []byte, format config.Format, err error) {
	switch act {
	case ActionImport:
		if err := m.cfgdb.Import(ctx, rec.Name, rec.FileContent, rec.FileFormat); err != nil {
			return nil, "", err
		}
		return rec.FileContent, rec.FileFormat, nil
	case ActionUseDB:
		return dbContentOr(rec.DB, rec.HasDB(), rec.FileFormat)
	default:
		return rec.FileContent, rec.FileFormat, nil
	}
}

// Effective resolves the content for an item that needs no reconciliation:
// BackendFiles uses the file, BackendDB uses the stored snapshot, and
// BackendBoth uses the file when present, falling back to the stored snapshot.
func (m *ConfigStore) Effective(ctx context.Context, it Item) (content []byte, format config.Format, err error) {
	if m.backend == BackendFiles {
		return it.FileContent, it.FileFormat, nil
	}
	cur, hasCur, err := m.cfgdb.Current(ctx, it.Name)
	if err != nil {
		return nil, "", err
	}
	if m.backend == BackendDB || !it.hasFile() {
		return dbContentOr(cur, hasCur, it.FileFormat)
	}
	return it.FileContent, it.FileFormat, nil
}

func dbContentOr(cur Snapshot, hasCur bool, fallbackFormat config.Format) ([]byte, config.Format, error) {
	if hasCur {
		return []byte(cur.Content), cur.Format, nil
	}
	return nil, fallbackFormat, nil
}
