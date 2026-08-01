package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var collectionExts = []string{".yaml", ".yml", ".json"}

func SerializeDir(dir string) (blob []byte, has bool, err error) {
	files, err := collectionFiles(dir)
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
		if err := writeAtomic(path, []byte(files[name])); err != nil {
			return nil, fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return names, nil
}

func collectionFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !hasExt(e.Name(), collectionExts) {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	sort.Strings(files)
	return files, nil
}

func WriteConfigFile(home string, content []byte, format string) (string, error) {
	name := "config.yaml"
	if format == "json" {
		name = "config.json"
	}
	return WriteItem(home, name, content)
}

func WriteItem(dir, filename string, content []byte) (string, error) {
	if err := EnsureDir(dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, filename)
	if err := writeAtomic(path, content); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

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
		syncDir(filepath.Dir(path))
	}
	return path, nil
}

func writeAtomic(path string, content []byte) error {
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
	syncDir(filepath.Dir(path))
	return nil
}

// syncDir flushes a directory entry so a completed rename survives a crash:
// without it the file contents are durable but the name may still be the old one.
// Directory fsync is not portable, so this is best effort by design.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}

func ReadFileAt(path string) (raw []byte, format string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", path, err)
	}
	return data, formatOf(path), nil
}

func ReadRaw(path string) (raw []byte, ok bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}
	return data, true, nil
}

func RemoveFiles(dir, base string, exts []string) (removed []string, err error) {
	if len(exts) == 0 {
		exts = collectionExts
	}
	for _, ext := range exts {
		p := filepath.Join(dir, base+ext)
		rerr := os.Remove(p)
		if rerr == nil {
			removed = append(removed, p)
			continue
		}
		if !os.IsNotExist(rerr) {
			return removed, fmt.Errorf("removing %s: %w", p, rerr)
		}
	}
	return removed, nil
}

func RemoveItem(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}

func ClearDir(dir string, exts []string) (removed []string, err error) {
	if len(exts) == 0 {
		exts = collectionExts
	}
	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", dir, rerr)
	}
	for _, e := range entries {
		if e.IsDir() || !hasExt(e.Name(), exts) {
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

func EnsureDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	return nil
}

func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func IsFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func IsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// JoinUnder joins a non-empty relative path to base, rejecting paths that
// would escape base.
func JoinUnder(base, relative string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return "", fmt.Errorf("empty base path")
	}
	if strings.TrimSpace(relative) == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("path %q must be non-empty and relative", relative)
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes base directory", relative)
	}
	return filepath.Join(base, clean), nil
}

// RemoveAll removes path after rejecting empty paths and filesystem roots.
// Callers remain responsible for confirming that the non-root path is the
// intended application directory.
func RemoveAll(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("refusing to remove empty path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", path, err)
	}
	absolute = filepath.Clean(absolute)
	root := filepath.Clean(filepath.VolumeName(absolute) + string(filepath.Separator))
	if absolute == root {
		return fmt.Errorf("refusing to remove filesystem root %s", absolute)
	}
	if err := os.RemoveAll(absolute); err != nil {
		return fmt.Errorf("removing %s: %w", absolute, err)
	}
	return nil
}

// newArchiveDir claims a fresh directory under root, suffixing the second-
// resolution stamp when it is already taken. Mkdir rather than MkdirAll is what
// makes the claim exclusive, so two archives in the same second cannot merge.
func newArchiveDir(root, stamp string) (string, error) {
	for i := 1; i <= 100; i++ {
		dest := filepath.Join(root, stamp)
		if i > 1 {
			dest = filepath.Join(root, stamp+"-"+strconv.Itoa(i))
		}
		err := os.Mkdir(dest, 0o700)
		if err == nil {
			return dest, nil
		}
		if !os.IsExist(err) {
			return "", fmt.Errorf("creating %s: %w", dest, err)
		}
	}
	return "", fmt.Errorf("too many archives under %s for %s", root, stamp)
}

func Archive(home string, entries []string) (dest string, moved []string, err error) {
	cleaned := make([]string, 0, len(entries))
	for _, name := range entries {
		path, pathErr := JoinUnder(home, name)
		if pathErr != nil {
			return "", nil, fmt.Errorf("archiving %q: %w", name, pathErr)
		}
		relative, pathErr := filepath.Rel(home, path)
		if pathErr != nil {
			return "", nil, fmt.Errorf("archiving %q: %w", name, pathErr)
		}
		cleaned = append(cleaned, relative)
	}
	root := filepath.Join(home, ".archive")
	if err = EnsureDir(root); err != nil {
		return "", nil, err
	}
	dest, err = newArchiveDir(root, time.Now().Format("20060102150405"))
	if err != nil {
		return "", nil, err
	}
	for _, n := range cleaned {
		src, _ := JoinUnder(home, n)
		if !Exists(src) {
			continue
		}
		target, _ := JoinUnder(dest, n)
		if err = EnsureDir(filepath.Dir(target)); err != nil {
			return dest, moved, err
		}
		if err = os.Rename(src, target); err != nil {
			return dest, moved, fmt.Errorf("archiving %s: %w", n, err)
		}
		moved = append(moved, n)
	}
	if len(moved) == 0 {
		_ = os.Remove(dest)
	}
	return dest, moved, nil
}

func UserConfigPath(app, name string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving user config dir: %w", err)
	}
	return filepath.Join(dir, app, name), nil
}

func hasExt(name string, exts []string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return false
}
