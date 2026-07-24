//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListenRefusesToReplaceRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-socket")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if ln, err := Listen("test", path); err == nil {
		_ = ln.Close()
		t.Fatal("Listen should reject a regular file")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("regular file was removed: %v", err)
	}
	if string(got) != "keep" {
		t.Fatalf("regular file changed: %q", got)
	}
}
