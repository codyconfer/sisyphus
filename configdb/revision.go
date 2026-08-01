package configdb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sconfig "github.com/codyconfer/sisyphus/config"
)

const revSuffix = ".gen"

// ErrGenerationMarker wraps a failure to update the sidecar generation file
// after a write already committed. The store's data is correct; only pollers
// watching Generation may miss the change. Match it with errors.Is.
var ErrGenerationMarker = errors.New("change committed but generation marker not updated")

// Generation reports an opaque marker that changes with every committed
// write, so pollers can detect change without opening the database.
func (s *Store) Generation() (string, bool) {
	if s == nil || s.h == nil {
		return "", false
	}
	b, err := os.ReadFile(s.h.Path() + revSuffix)
	if err != nil {
		return "", false
	}
	rev := strings.TrimSpace(string(b))
	if rev == "" {
		return "", false
	}
	return rev, true
}

func (s *Store) bump(change string) error {
	s.revMu.Lock()
	defer s.revMu.Unlock()

	path := s.h.Path() + revSuffix
	rev := strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + Hash("gen", []byte(change))[:12]
	dir, file := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	if _, err := sconfig.WriteItem(dir, file, []byte(rev+"\n")); err != nil {
		return fmt.Errorf("%w: %w", ErrGenerationMarker, err)
	}
	return nil
}
