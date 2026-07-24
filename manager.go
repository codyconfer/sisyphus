package sisyphus

import (
	"context"
	"path/filepath"

	"github.com/codyconfer/sisyphus/configdb"
)

type Mode int

const (
	ModeBoth Mode = iota
	ModeFileStore
	ModeDuckDB
)

const defaultConfigDBName = "config.duckdb"

type Options struct {
	Mode         Mode
	ConfigDBName string
}

type Manager struct {
	home  string
	mode  Mode
	cfgdb *configdb.Store
}

func Open(ctx context.Context, home string, opts Options) (*Manager, error) {
	m := &Manager{home: home, mode: opts.Mode}
	if opts.Mode == ModeFileStore {
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

func (m *Manager) Mode() Mode { return m.mode }

func (m *Manager) Home() string { return m.home }

func (m *Manager) DB() *configdb.Store { return m.cfgdb }

func (m *Manager) Current(ctx context.Context, name string) (Version, bool, error) {
	return m.cfgdb.Current(ctx, name)
}

func (m *Manager) Import(ctx context.Context, name string, content []byte, format string) error {
	return m.cfgdb.Import(ctx, name, content, format)
}

func (m *Manager) History(ctx context.Context, name string, limit int) ([]Version, error) {
	return m.cfgdb.History(ctx, name, limit)
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	return m.cfgdb.Close()
}

type Action int

const (
	ActionImport Action = iota
	ActionUseFile
	ActionUseDB
)

type Version = configdb.Version

type Reconciliation struct {
	Name        string
	FileContent []byte
	FileFormat  string
	DB          Version
	HasDB       bool
}

type Resolver interface {
	Resolve(Reconciliation) (Action, error)
}

func (m *Manager) Reconcile(ctx context.Context, name string, fileContent []byte, format string, hasFile bool, r Resolver) (content []byte, effFormat string, err error) {
	if m.mode == ModeFileStore {
		return fileContent, format, nil
	}
	cur, hasCur, err := m.cfgdb.Current(ctx, name)
	if err != nil {
		return nil, "", err
	}
	if m.mode == ModeDuckDB {
		return dbContentOr(cur, hasCur, format)
	}
	if !hasFile {
		return dbContentOr(cur, hasCur, format)
	}
	if hasCur && cur.Hash == configdb.Hash(format, fileContent) {
		return fileContent, format, nil
	}

	act, err := r.Resolve(Reconciliation{Name: name, FileContent: fileContent, FileFormat: format, DB: cur, HasDB: hasCur})
	if err != nil {
		return nil, "", err
	}
	switch act {
	case ActionImport:
		if err := m.cfgdb.Import(ctx, name, fileContent, format); err != nil {
			return nil, "", err
		}
		return fileContent, format, nil
	case ActionUseDB:
		return dbContentOr(cur, hasCur, format)
	default:
		return fileContent, format, nil
	}
}

func dbContentOr(cur Version, hasCur bool, fallbackFormat string) ([]byte, string, error) {
	if hasCur {
		return []byte(cur.Content), cur.Format, nil
	}
	return nil, fallbackFormat, nil
}
