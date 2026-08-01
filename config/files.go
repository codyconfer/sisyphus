package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/codyconfer/sisyphus/internal/fsutil"
)

// WriteConfigFile writes content as home's root config file, named for its
// format (config.yaml or config.json), and returns the written path.
func WriteConfigFile(home string, content []byte, format Format) (string, error) {
	name := "config.yaml"
	if format == FormatJSON {
		name = "config.json"
	}
	return WriteItem(home, name, content)
}

// WriteItem atomically writes content to dir/filename (mode 0600), creating
// dir as needed, and returns the written path.
func WriteItem(dir, filename string, content []byte) (string, error) {
	if err := EnsureDir(dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, filename)
	if err := fsutil.WriteAtomic(path, content); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

// OpenAppend opens path for appending, creating it (and its directory) with
// owner-only permissions.
func OpenAppend(path string) (*os.File, error) {
	return fsutil.OpenAppend(path)
}

// ReadFileAt reads one config file at an explicit path, reporting its format
// from the extension.
func ReadFileAt(path string) (raw []byte, format Format, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", path, err)
	}
	return data, formatOf(path), nil
}

// ReadRaw reads path, reporting a missing file as ok=false rather than an
// error.
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

// RemoveFiles removes dir/base+ext for each ext (the collection extensions
// when exts is empty), ignoring files that do not exist.
func RemoveFiles(dir, base string, exts []string) (removed []string, err error) {
	if len(exts) == 0 {
		exts = fsutil.CollectionExts
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

// RemoveItem removes path, ignoring a file that does not exist.
func RemoveItem(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}

// EnsureDir creates path (and parents) with owner-only permissions.
func EnsureDir(path string) error {
	return fsutil.EnsureDir(path)
}

// Exists reports whether path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsFile reports whether path exists and is not a directory.
func IsFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// IsDir reports whether path exists and is a directory.
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

// Archive moves the named home-relative entries into a timestamped directory
// under home/.archive, returning the archive directory and what moved.
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

// UserConfigPath returns name under the OS user config directory for app.
func UserConfigPath(app, name string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving user config dir: %w", err)
	}
	return filepath.Join(dir, app, name), nil
}
