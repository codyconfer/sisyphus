package configdb

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const revSuffix = ".gen"

// Revision reports a token that changes whenever this store is written. It
// reads a small marker file rather than the database, so a watcher can poll it
// without taking the database lock away from whoever is working.
//
// Reports false when the store has never been written, or when the marker is
// missing or unreadable.
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
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(rev+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
