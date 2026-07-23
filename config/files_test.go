package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteCollectionRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "queries")
	src := map[string]string{
		"b.yaml": "name: b\n",
		"a.yaml": "name: a\n",
		"c.json": `{"name":"c"}`,
	}
	blob, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	names, err := WriteCollection(dir, blob)
	if err != nil {
		t.Fatalf("WriteCollection: %v", err)
	}
	want := []string{"a.yaml", "b.yaml", "c.json"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for name, content := range src {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != content {
			t.Errorf("%s = %q, want %q", name, got, content)
		}
	}

	blob2, has, err := SerializeDir(dir)
	if err != nil || !has {
		t.Fatalf("SerializeDir: has=%v err=%v", has, err)
	}
	round := map[string]string{}
	if err := json.Unmarshal(blob2, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(round, src) {
		t.Errorf("round-trip = %v, want %v", round, src)
	}
}

func TestWriteCollectionBadBlob(t *testing.T) {
	if _, err := WriteCollection(t.TempDir(), []byte("not json")); err == nil {
		t.Fatal("expected error decoding invalid blob")
	}
}

func TestSerializeDirMissing(t *testing.T) {
	blob, has, err := SerializeDir(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if has || blob != nil {
		t.Errorf("missing dir should report has=false nil blob, got has=%v blob=%v", has, blob)
	}
}

func TestWriteConfigFileExtension(t *testing.T) {
	cases := []struct {
		format string
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
