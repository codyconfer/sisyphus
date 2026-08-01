// Package fsutil holds the generic filesystem helpers behind the config
// package. Helpers that applications need stay exported from config; the rest
// live only here.
package fsutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CollectionExts are the file extensions a collection directory serializes.
var CollectionExts = []string{".yaml", ".yml", ".json"}

// SerializeDir reads every collection file directly under dir into one JSON
// blob of basename -> content.
func SerializeDir(dir string) (blob []byte, has bool, err error) {
	files, err := CollectionFiles(dir)
	if err != nil {
		return nil, false, err
	}
	if len(files) == 0 {
		return nil, false, nil
	}
	c := make(map[string]string, len(files))
	for _, p := range files {
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil, false, fmt.Errorf("reading %s: %w", p, rerr)
		}
		c[filepath.Base(p)] = string(data)
	}
	blob, err = json.Marshal(c)
	return blob, true, err
}

// WriteCollection materializes a SerializeDir blob back into dir.
func WriteCollection(dir string, blob []byte) (names []string, err error) {
	files := map[string]string{}
	if err := json.Unmarshal(blob, &files); err != nil {
		return nil, fmt.Errorf("decoding collection blob: %w", err)
	}
	if err := EnsureDir(dir); err != nil {
		return nil, err
	}
	names = make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "." || name == ".." || name != filepath.Base(name) {
			return nil, fmt.Errorf("invalid collection entry name %q", name)
		}
		path := filepath.Join(dir, name)
		if err := WriteAtomic(path, []byte(files[name])); err != nil {
			return nil, fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return names, nil
}

// CollectionFiles lists the collection files directly under dir, sorted.
func CollectionFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !HasExt(e.Name(), CollectionExts) {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	sort.Strings(files)
	return files, nil
}

// OpenAppend opens path for appending, creating it (and its directory) 0600.
func OpenAppend(path string) (*os.File, error) {
	if err := EnsureDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return f, nil
}

// AppendItem appends content to dir/filename, syncing the file (and, when the
// file is new, its directory).
func AppendItem(dir, filename string, content []byte) (string, error) {
	path := filepath.Join(dir, filename)
	_, statErr := os.Lstat(path)
	created := os.IsNotExist(statErr)
	f, err := OpenAppend(path)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return "", fmt.Errorf("appending %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", fmt.Errorf("appending %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("appending %s: %w", path, err)
	}
	if created {
		SyncDir(filepath.Dir(path))
	}
	return path, nil
}

// WriteAtomic writes content to path via a same-directory temp file and
// rename, mode 0600.
func WriteAtomic(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		tmp.Close()
		// Best-effort: after a successful rename there is nothing left to remove.
		_ = os.Remove(name)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	SyncDir(filepath.Dir(path))
	return nil
}

// SyncDir flushes a directory entry so a completed rename survives a crash:
// without it the file contents are durable but the name may still be the old
// one. Directory fsync is not portable, so this is best effort by design.
func SyncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}

// ClearDir removes the files directly under dir whose extension is in exts
// (CollectionExts when empty).
func ClearDir(dir string, exts []string) (removed []string, err error) {
	if len(exts) == 0 {
		exts = CollectionExts
	}
	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", dir, rerr)
	}
	for _, e := range entries {
		if e.IsDir() || !HasExt(e.Name(), exts) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if err := os.Remove(p); err != nil {
			return removed, fmt.Errorf("removing %s: %w", p, err)
		}
		removed = append(removed, p)
	}
	return removed, nil
}

// EnsureDir creates path (and parents) 0700.
func EnsureDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	return nil
}

// HasExt reports whether name's extension is one of exts (case-insensitive).
func HasExt(name string, exts []string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return false
}
