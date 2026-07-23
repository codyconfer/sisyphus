package sisyphus

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

type fakeResolver struct {
	action Action
	err    error
	calls  int
	last   Reconciliation
}

func (f *fakeResolver) Resolve(r Reconciliation) (Action, error) {
	f.calls++
	f.last = r
	return f.action, f.err
}

func openMgr(t *testing.T) *Manager {
	t.Helper()
	m, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func TestReconcileNoFileEmptyDB(t *testing.T) {
	m := openMgr(t)
	r := &fakeResolver{action: ActionUseFile}

	content, format, err := m.Reconcile("config", nil, "yaml", false, r)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(content) != 0 {
		t.Errorf("content = %q, want empty", content)
	}
	if format != "yaml" {
		t.Errorf("format = %q, want yaml", format)
	}
	if r.calls != 0 {
		t.Errorf("resolver called %d times, want 0", r.calls)
	}
}

func TestReconcileNoFileWithDB(t *testing.T) {
	m := openMgr(t)
	stored := []byte("output: json\n")
	if err := m.Import("config", stored, "yaml"); err != nil {
		t.Fatalf("seed Import: %v", err)
	}
	r := &fakeResolver{action: ActionUseFile}

	content, format, err := m.Reconcile("config", nil, "yaml", false, r)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !bytes.Equal(content, stored) {
		t.Errorf("content = %q, want %q", content, stored)
	}
	if format != "yaml" {
		t.Errorf("format = %q, want yaml", format)
	}
	if r.calls != 0 {
		t.Errorf("resolver called %d times, want 0", r.calls)
	}
}

func TestReconcileFileEmptyDBImport(t *testing.T) {
	m := openMgr(t)
	file := []byte("output: terminal\n")
	r := &fakeResolver{action: ActionImport}

	content, format, err := m.Reconcile("config", file, "yaml", true, r)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !bytes.Equal(content, file) || format != "yaml" {
		t.Errorf("got %q/%q, want %q/yaml", content, format, file)
	}
	if r.calls != 1 {
		t.Fatalf("resolver called %d times, want 1", r.calls)
	}
	if r.last.HasDB {
		t.Errorf("HasDB = true, want false (empty DB)")
	}

	content, _, err = m.Reconcile("config", file, "yaml", true, r)
	if err != nil {
		t.Fatalf("Reconcile #2: %v", err)
	}
	if !bytes.Equal(content, file) {
		t.Errorf("content = %q, want %q", content, file)
	}
	if r.calls != 1 {
		t.Errorf("resolver called %d times total, want 1 (matched)", r.calls)
	}
}

func TestReconcileFileEmptyDBUseFile(t *testing.T) {
	m := openMgr(t)
	file := []byte("output: terminal\n")
	r := &fakeResolver{action: ActionUseFile}

	content, format, err := m.Reconcile("config", file, "yaml", true, r)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !bytes.Equal(content, file) || format != "yaml" {
		t.Errorf("got %q/%q, want %q/yaml", content, format, file)
	}
	if r.calls != 1 {
		t.Errorf("resolver called %d times, want 1", r.calls)
	}

	if _, ok, err := m.Current("config"); ok || err != nil {
		t.Errorf("DB should be empty after UseFile: ok=%v err=%v", ok, err)
	}
}

func TestReconcileFileEmptyDBUseDB(t *testing.T) {
	m := openMgr(t)
	file := []byte("output: terminal\n")
	r := &fakeResolver{action: ActionUseDB}

	content, format, err := m.Reconcile("config", file, "yaml", true, r)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(content) != 0 {
		t.Errorf("content = %q, want empty (UseDB with empty DB)", content)
	}
	if format != "yaml" {
		t.Errorf("format = %q, want yaml", format)
	}
	if r.calls != 1 {
		t.Errorf("resolver called %d times, want 1", r.calls)
	}
}

func TestReconcileFileIdenticalToDB(t *testing.T) {
	m := openMgr(t)
	file := []byte("output: terminal\n")
	if err := m.Import("config", file, "yaml"); err != nil {
		t.Fatalf("seed Import: %v", err)
	}
	r := &fakeResolver{action: ActionImport}

	content, format, err := m.Reconcile("config", file, "yaml", true, r)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !bytes.Equal(content, file) || format != "yaml" {
		t.Errorf("got %q/%q, want %q/yaml", content, format, file)
	}
	if r.calls != 0 {
		t.Errorf("resolver called %d times, want 0 (identical)", r.calls)
	}
}

func TestReconcileFileDiffersFromDB(t *testing.T) {
	m := openMgr(t)
	old := []byte("output: json\n")
	if err := m.Import("config", old, "yaml"); err != nil {
		t.Fatalf("seed Import: %v", err)
	}
	file := []byte("output: terminal\n")
	r := &fakeResolver{action: ActionUseDB}

	content, format, err := m.Reconcile("config", file, "yaml", true, r)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if r.calls != 1 {
		t.Fatalf("resolver called %d times, want 1", r.calls)
	}
	if !r.last.HasDB {
		t.Error("HasDB = false, want true")
	}
	if r.last.DB.Content != string(old) {
		t.Errorf("DB.Content = %q, want %q", r.last.DB.Content, old)
	}
	if !bytes.Equal(content, old) || format != "yaml" {
		t.Errorf("got %q/%q, want %q/yaml", content, format, old)
	}
}

func TestReconcileModeFileStore(t *testing.T) {
	m, err := Open(t.TempDir(), Options{Mode: ModeFileStore})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	r := &fakeResolver{action: ActionImport}
	file := []byte("x: 1\n")
	content, format, err := m.Reconcile("config", file, "yaml", true, r)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !bytes.Equal(content, file) || format != "yaml" || r.calls != 0 {
		t.Errorf("filestore should pass file through untouched: %q/%q calls=%d", content, format, r.calls)
	}
}

func TestReconcileModeDuckDB(t *testing.T) {
	m, err := Open(t.TempDir(), Options{Mode: ModeDuckDB})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	stored := []byte("from: db\n")
	if err := m.Import("config", stored, "yaml"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	r := &fakeResolver{action: ActionUseFile}
	content, _, err := m.Reconcile("config", []byte("from: file\n"), "yaml", true, r)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !bytes.Equal(content, stored) || r.calls != 0 {
		t.Errorf("duckdb mode should ignore file: content=%q calls=%d", content, r.calls)
	}
}

func TestConfigDBNameOption(t *testing.T) {
	home := t.TempDir()
	m, err := Open(home, Options{ConfigDBName: "state.duckdb"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	if err := m.Import("config", []byte("a: 1\n"), "yaml"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "state.duckdb")); err != nil {
		t.Errorf("expected state.duckdb: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "config.duckdb")); !os.IsNotExist(err) {
		t.Errorf("config.duckdb should not exist: %v", err)
	}
}
