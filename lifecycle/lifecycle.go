package lifecycle

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/codyconfer/sisyphus/config"
)

// FileSeed is a file to materialize during Install.
type FileSeed struct {
	RelPath string
	Content []byte
}

// InstallSpec describes a home-directory bootstrap.
type InstallSpec struct {
	Home  string
	Force bool
	// Dirs are created under Home (relative paths).
	Dirs []string
	// Files are written under Home when missing (or always when Force).
	Files []FileSeed
	// ConfigBasenames, if set, are checked for "already installed" (default
	// config.yaml/yml/json). When any exists and Force is false, Install errors.
	ConfigBasenames []string
	// After runs after files are written (e.g. open app DBs). Errors are ignored
	// by callers that treat DB creation as best-effort; return them to fail.
	After func(home string, created *[]string) error
}

// Install creates dirs and seed files. Returns paths created.
func Install(spec InstallSpec) ([]string, error) {
	home := spec.Home
	if home == "" {
		return nil, fmt.Errorf("lifecycle: empty home")
	}
	basenames := spec.ConfigBasenames
	if len(basenames) == 0 {
		basenames = []string{"config.yaml", "config.yml", "config.json"}
	}
	if !spec.Force {
		for _, n := range basenames {
			if config.IsFile(filepath.Join(home, n)) {
				return nil, fmt.Errorf("%s already has a config file", home)
			}
		}
	}

	for _, d := range append([]string{""}, spec.Dirs...) {
		path := home
		if d != "" {
			path = filepath.Join(home, d)
		}
		if err := config.EnsureDir(path); err != nil {
			return nil, err
		}
	}

	var created []string
	for _, f := range spec.Files {
		path := filepath.Join(home, f.RelPath)
		if !spec.Force && config.IsFile(path) {
			continue
		}
		if err := config.EnsureDir(filepath.Dir(path)); err != nil {
			return nil, err
		}
		if _, err := config.WriteItem(filepath.Dir(path), filepath.Base(path), f.Content); err != nil {
			return nil, err
		}
		created = append(created, path)
	}
	if spec.After != nil {
		if err := spec.After(home, &created); err != nil {
			return created, err
		}
	}
	return created, nil
}

// Clean archives the named entries under home into .archive/<timestamp>/.
func Clean(home string, entries []string) (dest string, moved []string, err error) {
	return config.Archive(home, entries)
}

// Nuke removes home entirely, then reinstalls via spec (Force is forced true).
func Nuke(home string, reinstall InstallSpec) ([]string, error) {
	if err := config.RemoveAll(home); err != nil {
		return nil, fmt.Errorf("removing %s: %w", home, err)
	}
	reinstall.Home = home
	reinstall.Force = true
	return Install(reinstall)
}

// Exists reports whether path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
