package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/codyconfer/sisyphus/internal/duckdb"
)

const KeyBytes = 32

const (
	walSuffix  = ".wal"
	tempSuffix = ".tmp"
	dbExt      = ".duckdb"
)

const lockTimeout = 15 * time.Second

func NewKey() ([]byte, error) {
	k := make([]byte, KeyBytes)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, nil
}

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
	if !looksLikeDB(path) {
		return readPlain(path)
	}
	return snapshotDB(ctx, path)
}

func snapshotDB(ctx context.Context, path string) ([]file, error) {
	var out []file
	h := duckdb.NewHandle(path, "", duckdb.Options{Timeout: lockTimeout})
	defer func() { _ = h.Close() }()
	err := h.Do(ctx, func(db *sql.DB) error {
		if _, err := db.ExecContext(ctx, "CHECKPOINT"); err != nil {
			return fmt.Errorf("checkpointing: %w", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = []file{{name: filepath.Base(path), data: data}}
		wal, err := os.ReadFile(path + walSuffix)
		switch {
		case err == nil:
			if len(wal) > 0 {
				out = append(out, file{name: filepath.Base(path) + walSuffix, data: wal})
			}
		case !errors.Is(err, os.ErrNotExist):
			return err
		}
		return nil
	})
	if err == nil {
		return out, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if held(err) || hasWAL(path) {
		return nil, fmt.Errorf("refusing to back up %s: cannot snapshot it safely: %w "+
			"(stop other munin processes holding it and retry)", filepath.Base(path), err)
	}
	return readPlain(path)
}

func readPlain(path string) ([]file, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return []file{{name: filepath.Base(path), data: data}}, nil
}

func looksLikeDB(path string) bool {
	if strings.EqualFold(filepath.Ext(path), dbExt) {
		return true
	}
	return hasWAL(path)
}

func hasWAL(path string) bool {
	st, err := os.Stat(path + walSuffix)
	return err == nil && st.Mode().IsRegular()
}

func held(err error) bool {
	if errors.Is(err, duckdb.ErrClosed) {
		return true
	}
	s := err.Error()
	for _, marker := range []string{
		"locked by another process",
		"timed out queueing",
		"Conflicting lock is held",
		"Could not set lock on file",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func Encrypt(plaintext, key []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func Decrypt(sealed, key []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(sealed) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return gcm.Open(nil, sealed[:ns], sealed[ns:], nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeyBytes {
		return nil, fmt.Errorf("backup key must be %d bytes, got %d", KeyBytes, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func Restore(ctx context.Context, sealed, key []byte, destDir string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plain, err := Decrypt(sealed, key)
	if err != nil {
		return nil, fmt.Errorf("decrypting backup (wrong key?): %w", err)
	}
	entries, err := Extract(plain)
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

	if err := checkUnheld(ctx, destDir, names); err != nil {
		return nil, err
	}

	stage, err := os.MkdirTemp(destDir, ".restore-")
	if err != nil {
		return nil, fmt.Errorf("staging restore in %s: %w", destDir, err)
	}
	defer func() { _ = os.RemoveAll(stage) }()

	for _, base := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(stage, base), entries[base], 0o600); err != nil {
			return nil, fmt.Errorf("staging %s: %w", base, err)
		}
	}
	for _, base := range names {
		if err := clearSidecars(filepath.Join(destDir, base)); err != nil {
			return nil, err
		}
	}
	for _, base := range names {
		if err := os.Rename(filepath.Join(stage, base), filepath.Join(destDir, base)); err != nil {
			return nil, fmt.Errorf("writing %s: %w", base, err)
		}
	}
	return names, nil
}

func clearSidecars(path string) error {
	if strings.HasSuffix(path, walSuffix) {
		return nil
	}
	if err := os.Remove(path + walSuffix); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing stale %s: %w", filepath.Base(path)+walSuffix, err)
	}
	if err := os.RemoveAll(path + tempSuffix); err != nil {
		return fmt.Errorf("removing stale %s: %w", filepath.Base(path)+tempSuffix, err)
	}
	return nil
}

func checkUnheld(ctx context.Context, destDir string, names []string) error {
	for _, base := range names {
		if !strings.EqualFold(filepath.Ext(base), dbExt) {
			continue
		}
		path := filepath.Join(destDir, base)
		if _, err := os.Stat(path); err != nil && !hasWAL(path) {
			continue
		}
		h := duckdb.NewHandle(path, "", duckdb.Options{Timeout: lockTimeout})
		err := h.Ensure(ctx)
		_ = h.Close()
		if err != nil && held(err) {
			return fmt.Errorf("refusing to restore over %s: %w "+
				"(stop the munin daemon and any other munin process, then retry)", base, err)
		}
	}
	return nil
}

const maxEntryBytes = 256 << 20

func Extract(archive []byte) (map[string][]byte, error) {
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
