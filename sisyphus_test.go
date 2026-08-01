package sisyphus

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func openMgr(t *testing.T) *ConfigStore {
	t.Helper()
	m, err := Open(context.Background(), t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func planOne(t *testing.T, m *ConfigStore, it Item) Reconciliation {
	t.Helper()
	plan, err := m.Plan(context.Background(), it)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan) != 1 {
		t.Fatalf("Plan returned %d reconciliations, want 1", len(plan))
	}
	return plan[0]
}

func TestPlanAbsentBothSidesOmitted(t *testing.T) {
	m := openMgr(t)

	plan, err := m.Plan(context.Background(), Item{Name: "config", FileFormat: "yaml"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan) != 0 {
		t.Errorf("plan = %v, want empty (absent on both sides)", plan)
	}
}

func TestPlanInSyncOmitted(t *testing.T) {
	m := openMgr(t)
	file := []byte("output: terminal\n")
	if err := m.Import(context.Background(), "config", file, "yaml"); err != nil {
		t.Fatalf("seed Import: %v", err)
	}

	plan, err := m.Plan(context.Background(), Item{Name: "config", FileContent: file, FileFormat: "yaml"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan) != 0 {
		t.Errorf("plan = %v, want empty (in sync)", plan)
	}
}

func TestPlanFileOnly(t *testing.T) {
	m := openMgr(t)
	file := []byte("output: terminal\n")

	rec := planOne(t, m, Item{Name: "config", FileContent: file, FileFormat: "yaml"})
	if rec.Name != "config" || !bytes.Equal(rec.FileContent, file) || rec.FileFormat != "yaml" {
		t.Errorf("rec = %+v, want file side populated", rec)
	}
	if rec.HasDB() {
		t.Error("HasDB() = true, want false (empty DB)")
	}
	if !rec.HasFile() {
		t.Error("HasFile() = false, want true")
	}
}

func TestPlanDBOnly(t *testing.T) {
	m := openMgr(t)
	stored := []byte("output: json\n")
	if err := m.Import(context.Background(), "config", stored, "yaml"); err != nil {
		t.Fatalf("seed Import: %v", err)
	}

	rec := planOne(t, m, Item{Name: "config", FileFormat: "yaml"})
	if !rec.HasDB() {
		t.Error("HasDB() = false, want true")
	}
	if rec.HasFile() {
		t.Error("HasFile() = true, want false")
	}
	if rec.DB.Content != string(stored) {
		t.Errorf("DB.Content = %q, want %q", rec.DB.Content, stored)
	}
}

func TestPlanDrifted(t *testing.T) {
	m := openMgr(t)
	old := []byte("output: json\n")
	if err := m.Import(context.Background(), "config", old, "yaml"); err != nil {
		t.Fatalf("seed Import: %v", err)
	}
	file := []byte("output: terminal\n")

	rec := planOne(t, m, Item{Name: "config", FileContent: file, FileFormat: "yaml"})
	if !rec.HasDB() || !rec.HasFile() {
		t.Errorf("HasDB()=%v HasFile()=%v, want both true", rec.HasDB(), rec.HasFile())
	}
	if rec.DB.Content != string(old) {
		t.Errorf("DB.Content = %q, want %q", rec.DB.Content, old)
	}
	if !bytes.Equal(rec.FileContent, file) {
		t.Errorf("FileContent = %q, want %q", rec.FileContent, file)
	}
}

func TestPlanMixedBatchOmitsInSync(t *testing.T) {
	m := openMgr(t)
	same := []byte("a: 1\n")
	if err := m.Import(context.Background(), "same", same, "yaml"); err != nil {
		t.Fatalf("seed Import: %v", err)
	}
	if err := m.Import(context.Background(), "drift", []byte("b: 1\n"), "yaml"); err != nil {
		t.Fatalf("seed Import: %v", err)
	}

	plan, err := m.Plan(context.Background(),
		Item{Name: "same", FileContent: same, FileFormat: "yaml"},
		Item{Name: "drift", FileContent: []byte("b: 2\n"), FileFormat: "yaml"},
		Item{Name: "new", FileContent: []byte("c: 3\n"), FileFormat: "yaml"},
	)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan) != 2 {
		t.Fatalf("plan has %d entries, want 2: %+v", len(plan), plan)
	}
	if plan[0].Name != "drift" || plan[1].Name != "new" {
		t.Errorf("plan names = %s, %s; want drift, new", plan[0].Name, plan[1].Name)
	}
}

func TestApplyImport(t *testing.T) {
	m := openMgr(t)
	file := []byte("output: terminal\n")
	rec := planOne(t, m, Item{Name: "config", FileContent: file, FileFormat: "yaml"})

	content, format, err := m.Apply(context.Background(), rec, ActionImport)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !bytes.Equal(content, file) || format != "yaml" {
		t.Errorf("got %q/%q, want %q/yaml", content, format, file)
	}
	cur, ok, err := m.Current(context.Background(), "config")
	if err != nil || !ok {
		t.Fatalf("Current after import: ok=%v err=%v", ok, err)
	}
	if cur.Content != string(file) {
		t.Errorf("stored content = %q, want %q", cur.Content, file)
	}

	plan, err := m.Plan(context.Background(), Item{Name: "config", FileContent: file, FileFormat: "yaml"})
	if err != nil {
		t.Fatalf("Plan after import: %v", err)
	}
	if len(plan) != 0 {
		t.Errorf("plan = %+v, want empty after import", plan)
	}
}

func TestApplyUseFile(t *testing.T) {
	m := openMgr(t)
	file := []byte("output: terminal\n")
	rec := planOne(t, m, Item{Name: "config", FileContent: file, FileFormat: "yaml"})

	content, format, err := m.Apply(context.Background(), rec, ActionUseFile)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !bytes.Equal(content, file) || format != "yaml" {
		t.Errorf("got %q/%q, want %q/yaml", content, format, file)
	}
	if _, ok, err := m.Current(context.Background(), "config"); ok || err != nil {
		t.Errorf("DB should stay empty after UseFile: ok=%v err=%v", ok, err)
	}
}

func TestApplyUseDB(t *testing.T) {
	m := openMgr(t)
	old := []byte("output: json\n")
	if err := m.Import(context.Background(), "config", old, "yaml"); err != nil {
		t.Fatalf("seed Import: %v", err)
	}
	rec := planOne(t, m, Item{Name: "config", FileContent: []byte("output: terminal\n"), FileFormat: "yaml"})

	content, format, err := m.Apply(context.Background(), rec, ActionUseDB)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !bytes.Equal(content, old) || format != "yaml" {
		t.Errorf("got %q/%q, want %q/yaml", content, format, old)
	}
}

func TestApplyUseDBEmptyDB(t *testing.T) {
	m := openMgr(t)
	rec := planOne(t, m, Item{Name: "config", FileContent: []byte("output: terminal\n"), FileFormat: "yaml"})

	content, format, err := m.Apply(context.Background(), rec, ActionUseDB)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(content) != 0 {
		t.Errorf("content = %q, want empty (UseDB with empty DB)", content)
	}
	if format != "yaml" {
		t.Errorf("format = %q, want yaml", format)
	}
}

func TestEffective(t *testing.T) {
	m := openMgr(t)
	stored := []byte("output: json\n")
	if err := m.Import(context.Background(), "config", stored, "yaml"); err != nil {
		t.Fatalf("seed Import: %v", err)
	}

	content, format, err := m.Effective(context.Background(), Item{Name: "config", FileContent: stored, FileFormat: "yaml"})
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if !bytes.Equal(content, stored) || format != "yaml" {
		t.Errorf("got %q/%q, want %q/yaml", content, format, stored)
	}

	content, format, err = m.Effective(context.Background(), Item{Name: "config", FileFormat: "yaml"})
	if err != nil {
		t.Fatalf("Effective (no file): %v", err)
	}
	if !bytes.Equal(content, stored) || format != "yaml" {
		t.Errorf("no file: got %q/%q, want stored %q/yaml", content, format, stored)
	}

	content, format, err = m.Effective(context.Background(), Item{Name: "missing", FileFormat: "yaml"})
	if err != nil {
		t.Fatalf("Effective (absent): %v", err)
	}
	if len(content) != 0 || format != "yaml" {
		t.Errorf("absent: got %q/%q, want empty/yaml", content, format)
	}
}

func TestBackendFiles(t *testing.T) {
	m, err := Open(context.Background(), t.TempDir(), Options{Backend: BackendFiles})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	file := []byte("x: 1\n")

	plan, err := m.Plan(context.Background(), Item{Name: "config", FileContent: file, FileFormat: "yaml"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan != nil {
		t.Errorf("plan = %+v, want nil in BackendFiles", plan)
	}

	content, format, err := m.Effective(context.Background(), Item{Name: "config", FileContent: file, FileFormat: "yaml"})
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if !bytes.Equal(content, file) || format != "yaml" {
		t.Errorf("filestore should pass file through untouched: %q/%q", content, format)
	}
}

func TestBackendDB(t *testing.T) {
	m, err := Open(context.Background(), t.TempDir(), Options{Backend: BackendDB})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	stored := []byte("from: db\n")
	if err := m.Import(context.Background(), "config", stored, "yaml"); err != nil {
		t.Fatalf("Import: %v", err)
	}

	plan, err := m.Plan(context.Background(), Item{Name: "config", FileContent: []byte("from: file\n"), FileFormat: "yaml"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan != nil {
		t.Errorf("plan = %+v, want nil in BackendDB", plan)
	}

	content, _, err := m.Effective(context.Background(), Item{Name: "config", FileContent: []byte("from: file\n"), FileFormat: "yaml"})
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if !bytes.Equal(content, stored) {
		t.Errorf("duckdb mode should ignore file: content=%q", content)
	}
}

func TestConfigDBNameOption(t *testing.T) {
	home := t.TempDir()
	m, err := Open(context.Background(), home, Options{ConfigDBName: "state.duckdb"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	if err := m.Import(context.Background(), "config", []byte("a: 1\n"), "yaml"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "state.duckdb")); err != nil {
		t.Errorf("expected state.duckdb: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "config.duckdb")); !os.IsNotExist(err) {
		t.Errorf("config.duckdb should not exist: %v", err)
	}
}
