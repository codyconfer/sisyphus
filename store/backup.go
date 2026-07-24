package store

import "sync"

var (
	backupMu    sync.Mutex
	backupPaths []string
)

// RegisterBackupPath records a plugin DB path for inclusion in encrypted
// backups. Apps should union these with core paths when calling Backup.
func RegisterBackupPath(path string) {
	if path == "" {
		return
	}
	backupMu.Lock()
	defer backupMu.Unlock()
	for _, p := range backupPaths {
		if p == path {
			return
		}
	}
	backupPaths = append(backupPaths, path)
}

// BackupPaths returns registered plugin store paths.
func BackupPaths() []string {
	backupMu.Lock()
	defer backupMu.Unlock()
	out := make([]string, len(backupPaths))
	copy(out, backupPaths)
	return out
}
