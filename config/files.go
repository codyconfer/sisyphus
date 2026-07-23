package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
		if err := os.WriteFile(path, []byte(files[name]), 0o600); err != nil {
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
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
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

func RemoveAll(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}

func Archive(home string, entries []string) (dest string, moved []string, err error) {
	ts := time.Now().Format("20060102150405")
	dest = filepath.Join(home, ".archive", ts)
	if err = EnsureDir(dest); err != nil {
		return dest, nil, err
	}
	for _, n := range entries {
		src := filepath.Join(home, n)
		if !Exists(src) {
			continue
		}
		if err = os.Rename(src, filepath.Join(dest, n)); err != nil {
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
