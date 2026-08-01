package fsutil

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

func TestAppendItemAppends(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "logs")
	path, err := AppendItem(dir, "app.log", []byte("one\n"))
	if err != nil {
		t.Fatalf("AppendItem: %v", err)
	}
	if _, err := AppendItem(dir, "app.log", []byte("two\n")); err != nil {
		t.Fatalf("AppendItem: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "one\ntwo\n" {
		t.Errorf("content = %q, want %q", got, "one\ntwo\n")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestAppendItemCreateThenAppend(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh")
	path, err := AppendItem(dir, "new.log", []byte("first\n"))
	if err != nil {
		t.Fatalf("AppendItem creating the file: %v", err)
	}
	if _, err := AppendItem(dir, "new.log", []byte("second\n")); err != nil {
		t.Fatalf("AppendItem after create: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first\nsecond\n" {
		t.Errorf("content = %q, want %q", got, "first\nsecond\n")
	}
}

func TestClearDir(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.yaml", "b.json", "keep.txt"} {
		if err := WriteAtomic(filepath.Join(dir, n), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := ClearDir(dir, nil)
	if err != nil {
		t.Fatalf("ClearDir: %v", err)
	}
	if len(removed) != 2 {
		t.Errorf("removed = %v, want 2 entries", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Error("keep.txt should survive")
	}
}
