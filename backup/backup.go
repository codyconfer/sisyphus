// Package backup snapshots a set of files into a tar archive and restores
// such archives atomically. DuckDB databases get special handling on both
// sides: they are checkpointed and lock-guarded before being read, and a
// restore refuses to replace a database another process still holds.
// Encryption (AES-256-GCM) is applied by the caller; the root sisyphus
// package's Backup/Restore pair this with key escrow.
package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/codyconfer/sisyphus/internal/crypt"
	"github.com/codyconfer/sisyphus/internal/duckdb"
)

const (
	walSuffix  = ".wal"
	tempSuffix = ".tmp"
	lockSuffix = ".lock"
	wantSuffix = ".wait"
)

const (
	stagePrefix = ".restore-stage-"
	asidePrefix = ".restore-aside-"
)

const (
	duckMagic       = "DUCK"
	duckMagicOffset = 8
)

var lockTimeout = 15 * time.Second

var renameFile = os.Rename

// Archive snapshots paths into an uncompressed tar, storing each file under
// its basename (two paths with the same basename are an error). A DuckDB
// database is checkpointed under this package's cross-process lock before it
// is read, so the snapshot is consistent — which also means backing up a
// database needs write access to it. A path that does not exist is skipped,
// unless an orphaned .wal sits beside it, which is an error. An archive that
// would contain no files at all is also an error.
func Archive(ctx context.Context, paths []string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	n := 0
	seen := map[string]bool{}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		files, err := snapshot(ctx, p)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if seen[f.name] {
				return nil, fmt.Errorf("duplicate backup entry %q: paths with the same basename collide", f.name)
			}
			seen[f.name] = true
			hdr := &tar.Header{
				Name:    f.name,
				Mode:    0o600,
				Size:    int64(len(f.data)),
				ModTime: time.Now(),
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return nil, err
			}
			if _, err := tw.Write(f.data); err != nil {
				return nil, err
			}
			n++
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("nothing to back up (no files found)")
	}
	return buf.Bytes(), nil
}

type file struct {
	name string
	data []byte
}

func snapshot(ctx context.Context, path string) ([]file, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if hasWAL(path) {
				return nil, fmt.Errorf("refusing to back up %s: %s exists without its database file",
					filepath.Base(path), filepath.Base(path)+walSuffix)
			}
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	db, err := isDatabase(path)
	if err != nil {
		return nil, fmt.Errorf("examining %s: %w", path, err)
	}
	if !db {
		return readPlain(path)
	}
	return snapshotDB(ctx, path)
}

func snapshotDB(ctx context.Context, path string) ([]file, error) {
	var data []byte
	lock, err := duckdb.AcquireLock(ctx, path, lockTimeout)
	if err == nil {
		defer lock.Release()
		db, openErr := duckdb.Open(ctx, path, "")
		if openErr != nil {
			err = openErr
		} else {
			data, err = checkpointCloseAndRead(ctx, db, path)
		}
	}
	if err == nil {
		return []file{{name: filepath.Base(path), data: data}}, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	base := filepath.Base(path)
	switch {
	case held(err):
		return nil, fmt.Errorf("refusing to back up %s: another process is holding it: %w "+
			"(stop other munin processes holding it and retry)", base, err)
	case errors.Is(err, fs.ErrPermission):
		return nil, fmt.Errorf("refusing to back up %s: backing up a database rewrites it — it is "+
			"checkpointed before it is read — so it needs write access to the file and its directory, "+
			"which read-only media cannot give: %w", base, err)
	}
	return nil, fmt.Errorf("refusing to back up %s: cannot snapshot it safely: %w", base, err)
}

func checkpointCloseAndRead(ctx context.Context, db *sql.DB, path string) ([]byte, error) {
	closed := false
	defer func() {
		if !closed {
			_ = db.Close()
		}
	}()
	if _, err := db.ExecContext(ctx, "CHECKPOINT"); err != nil {
		return nil, fmt.Errorf("checkpointing: %w", err)
	}
	if err := db.Close(); err != nil {
		return nil, fmt.Errorf("closing after checkpoint: %w", err)
	}
	closed = true
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func readPlain(path string) ([]file, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return []file{{name: filepath.Base(path), data: data}}, nil
}

func isDatabase(path string) (bool, error) {
	if hasWAL(path) {
		return true, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, duckMagicOffset+len(duckMagic))
	switch _, err := io.ReadFull(f, head); {
	case err == nil:
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return false, nil
	default:
		return false, err
	}
	return string(head[duckMagicOffset:]) == duckMagic, nil
}

func hasWAL(path string) bool {
	st, err := os.Stat(path + walSuffix)
	return err == nil && st.Mode().IsRegular()
}

func held(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, duckdb.ErrLocked) || errors.Is(err, duckdb.ErrClosed)
}

// Restore decrypts sealed with key, extracts the archive, and swaps the
// contained files into destDir (created if needed), returning the sorted
// basenames it wrote. Entries are flattened to basenames, staged to disk
// first, then swapped in with the previous files set aside; on failure the
// swap is rolled back, and the error says exactly which files (if any) could
// not be put back. Databases still held by another process are refused.
func Restore(ctx context.Context, sealed, key []byte, destDir string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plain, err := crypt.Decrypt(sealed, key)
	if err != nil {
		return nil, fmt.Errorf("decrypting backup (wrong key?): %w", err)
	}
	entries, err := extract(plain)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", destDir, err)
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, filepath.Base(name))
	}
	sort.Strings(names)

	if err := checkReplaceable(destDir, names); err != nil {
		return nil, err
	}
	locks, err := acquireDestLocks(ctx, destDir, names)
	if err != nil {
		return nil, err
	}
	defer releaseLocks(locks)
	sweepStageDirs(destDir)

	stage, err := os.MkdirTemp(destDir, stagePrefix)
	if err != nil {
		return nil, fmt.Errorf("staging restore in %s: %w", destDir, err)
	}
	defer func() { _ = os.RemoveAll(stage) }()

	for _, base := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := writeSynced(filepath.Join(stage, base), entries[base]); err != nil {
			return nil, fmt.Errorf("staging %s: %w", base, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	aside, err := os.MkdirTemp(destDir, asidePrefix)
	if err != nil {
		return nil, fmt.Errorf("preparing the rollback area in %s: %w", destDir, err)
	}
	sw := &swap{dest: destDir, stage: stage, aside: aside}
	if err := sw.run(names); err != nil {
		return nil, err
	}
	syncDir(destDir)
	_ = os.RemoveAll(aside)
	return names, nil
}

func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func syncDir(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = f.Sync()
	_ = f.Close()
}

type swap struct {
	dest   string
	stage  string
	aside  string
	moved  []string
	placed []string
}

func (s *swap) run(names []string) error {
	for _, base := range names {
		for _, name := range []string{base, base + walSuffix, base + tempSuffix} {
			if err := s.setAside(name); err != nil {
				if renameHeld(err) {
					return s.abort(fmt.Errorf("refusing to restore over %s: %w: %w",
						base, duckdb.ErrLocked, err))
				}
				return s.abort(fmt.Errorf("setting %s aside: %w", name, err))
			}
		}
	}
	for _, base := range names {
		if err := renameFile(filepath.Join(s.stage, base), filepath.Join(s.dest, base)); err != nil {
			return s.abort(fmt.Errorf("writing %s: %w", base, err))
		}
		s.placed = append(s.placed, base)
	}
	return nil
}

func (s *swap) setAside(name string) error {
	from := filepath.Join(s.dest, name)
	if _, err := os.Lstat(from); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := renameFile(from, filepath.Join(s.aside, name)); err != nil {
		return err
	}
	s.moved = append(s.moved, name)
	return nil
}

func (s *swap) abort(cause error) error {
	set := make(map[string]bool, len(s.moved))
	for _, name := range s.moved {
		set[name] = true
	}
	var stuck []string
	for _, name := range s.placed {
		if set[name] {
			continue
		}
		if err := os.Remove(filepath.Join(s.dest, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			stuck = append(stuck, name)
		}
	}
	for i := len(s.moved) - 1; i >= 0; i-- {
		name := s.moved[i]
		if err := renameFile(filepath.Join(s.aside, name), filepath.Join(s.dest, name)); err != nil {
			stuck = append(stuck, name)
		}
	}
	syncDir(s.dest)
	if len(stuck) == 0 {
		_ = os.RemoveAll(s.aside)
		return fmt.Errorf("%w (nothing was replaced: every previous file is back in place)", cause)
	}
	sort.Strings(stuck)
	return fmt.Errorf("%w; rolling the restore back failed for %s, so %s is left half-replaced: "+
		"the previous files are intact in %s — stop every munin process, move them back into %s by hand, "+
		"then retry", cause, strings.Join(stuck, ", "), s.dest, s.aside, s.dest)
}

func checkReplaceable(destDir string, names []string) error {
	for _, base := range names {
		path := filepath.Join(destDir, base)
		if err := regularOrAbsent(path); err != nil {
			return fmt.Errorf("refusing to restore over %s: %w", base, err)
		}
		for _, suffix := range []string{lockSuffix, wantSuffix} {
			if err := regularOrAbsent(path + suffix); err != nil {
				return fmt.Errorf("refusing to restore over %s: %w (that sidecar is how a munin "+
					"process still holding the database is detected, so restoring now could "+
					"overwrite a database in use)", base, err)
			}
		}
	}
	return nil
}

func regularOrAbsent(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file (%s)", filepath.Base(path), st.Mode().Type())
	}
	return nil
}

func acquireDestLocks(ctx context.Context, destDir string, names []string) ([]*duckdb.Lock, error) {
	var locks []*duckdb.Lock
	for _, base := range names {
		path := filepath.Join(destDir, base)
		db, err := isDatabase(path)
		if err != nil || !db {
			continue
		}
		l, err := duckdb.AcquireLock(ctx, path, lockTimeout)
		if err != nil {
			releaseLocks(locks)
			if held(err) {
				return nil, fmt.Errorf("refusing to restore over %s: %w "+
					"(stop the munin daemon and any other munin process, then retry)", base, err)
			}
			return nil, fmt.Errorf("refusing to restore over %s: %w", base, err)
		}
		locks = append(locks, l)
	}
	return locks, nil
}

func releaseLocks(locks []*duckdb.Lock) {
	for i := len(locks) - 1; i >= 0; i-- {
		locks[i].Release()
	}
}

const stageSweepAge = time.Hour

func sweepStageDirs(destDir string) {
	ents, err := os.ReadDir(destDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-stageSweepAge)
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), stagePrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(destDir, e.Name()))
	}
}

const maxEntryBytes = 256 << 20

func extract(archive []byte) (map[string][]byte, error) {
	out := map[string][]byte{}
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(filepath.Clean(hdr.Name))
		if name == "." || name == ".." || name == "" || name == string(filepath.Separator) {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxEntryBytes+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > maxEntryBytes {
			return nil, fmt.Errorf("backup entry %q exceeds %d bytes", name, maxEntryBytes)
		}
		out[name] = data
	}
	return out, nil
}
