package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteConfigFileExtension(t *testing.T) {
	cases := []struct {
		format Format
		want   string
	}{
		{"yaml", "config.yaml"},
		{"", "config.yaml"},
		{"json", "config.json"},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		path, err := WriteConfigFile(dir, []byte("content"), tc.format)
		if err != nil {
			t.Fatalf("WriteConfigFile(%q): %v", tc.format, err)
		}
		if filepath.Base(path) != tc.want {
			t.Errorf("format %q -> %s, want %s", tc.format, filepath.Base(path), tc.want)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != "content" {
			t.Errorf("content = %q, want %q", got, "content")
		}
	}
}

func TestWriteConfigFileCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "home")
	if _, err := WriteConfigFile(dir, []byte("x"), "yaml"); err != nil {
		t.Fatalf("WriteConfigFile: %v", err)
	}
	if !IsFile(filepath.Join(dir, "config.yaml")) {
		t.Fatal("expected config.yaml created")
	}
}

func TestOpenAppendAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "app.log")
	for _, chunk := range []string{"one\n", "two\n"} {
		f, err := OpenAppend(path)
		if err != nil {
			t.Fatalf("OpenAppend: %v", err)
		}
		if _, err := f.WriteString(chunk); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "one\ntwo\n" {
		t.Errorf("content = %q, want %q", got, "one\ntwo\n")
	}
}

func TestRemoveItem(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteItem(dir, "gone.txt", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteItem(dir, "keep.txt", []byte("y")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveItem(path); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	if Exists(path) {
		t.Error("file should be removed")
	}
	if err := RemoveItem(filepath.Join(dir, "missing.txt")); err != nil {
		t.Fatalf("RemoveItem missing path: %v", err)
	}
	if !IsFile(filepath.Join(dir, "keep.txt")) {
		t.Error("keep.txt should survive")
	}
}

func TestRemoveFilesAndClearDir(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"config.yaml", "config.json", "keep.txt"} {
		if _, err := WriteItem(dir, n, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := RemoveFiles(dir, "config", nil)
	if err != nil {
		t.Fatalf("RemoveFiles: %v", err)
	}
	if len(removed) != 2 {
		t.Errorf("removed = %v, want 2", removed)
	}
	if !IsFile(filepath.Join(dir, "keep.txt")) {
		t.Error("keep.txt should survive")
	}
}

func TestArchive(t *testing.T) {
	home := t.TempDir()
	if _, err := WriteItem(home, "config.yaml", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteItem(filepath.Join(home, "queries"), "q.yaml", []byte("y")); err != nil {
		t.Fatal(err)
	}
	dest, moved, err := Archive(home, []string{"config.yaml", "queries", "missing"})
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if !reflect.DeepEqual(moved, []string{"config.yaml", "queries"}) {
		t.Errorf("moved = %v", moved)
	}
	if !IsFile(filepath.Join(dest, "config.yaml")) || !IsDir(filepath.Join(dest, "queries")) {
		t.Error("entries not archived")
	}
	if Exists(filepath.Join(home, "config.yaml")) {
		t.Error("source should be moved, not copied")
	}
}

func TestJoinUnderRejectsEscape(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"", ".", "..", filepath.Join("..", "outside")} {
		if _, err := JoinUnder(home, name); err == nil {
			t.Errorf("JoinUnder(%q) should reject escape or empty path", name)
		}
	}
	if _, err := JoinUnder(home, filepath.Join("queries", "q.yaml")); err != nil {
		t.Fatalf("JoinUnder valid nested path: %v", err)
	}
}

func TestRemoveAllRejectsDangerousPaths(t *testing.T) {
	for _, path := range []string{"", string(filepath.Separator)} {
		if err := RemoveAll(path); err == nil {
			t.Errorf("RemoveAll(%q) should reject dangerous path", path)
		}
	}
}

func TestArchiveRejectsEscapeWithoutMovingFiles(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, "home")
	if err := EnsureDir(home); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.yaml")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Archive(home, []string{filepath.Join("..", "outside.yaml")}); err == nil {
		t.Fatal("Archive should reject a path outside home")
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("outside file was moved or removed: %v", err)
	}
	if string(got) != "keep" {
		t.Fatalf("outside file changed: %q", got)
	}
}
