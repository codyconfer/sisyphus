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

var ErrRevisionMarker = errors.New("change committed but revision marker not updated")

func (s *Store) Revision() (string, bool) {
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
	path := s.h.Path() + revSuffix
	rev := strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + Hash("rev", []byte(change))[:12]
	dir, file := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	if _, err := sconfig.WriteItem(dir, file, []byte(rev+"\n")); err != nil {
		return fmt.Errorf("%w: %w", ErrRevisionMarker, err)
	}
	return nil
}
