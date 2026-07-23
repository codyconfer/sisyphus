package configdb

import (
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "store.duckdb"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCurrentMissOnFreshStore(t *testing.T) {
	s := openTemp(t)
	v, ok, err := s.Current("config")
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if ok {
		t.Fatalf("expected no current on fresh store, got %+v", v)
	}
}

func TestImportThenCurrent(t *testing.T) {
	s := openTemp(t)

	content := []byte("output: terminal\n")
	if err := s.Import("config", content, "yaml"); err != nil {
		t.Fatalf("Import: %v", err)
	}

	cur, ok, err := s.Current("config")
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if !ok {
		t.Fatal("expected current after import")
	}
	if cur.Content != string(content) {
		t.Errorf("content = %q, want %q", cur.Content, content)
	}
	if cur.Format != "yaml" {
		t.Errorf("format = %q, want yaml", cur.Format)
	}
	if cur.Hash != Hash("yaml", content) {
		t.Errorf("hash = %q, want %q", cur.Hash, Hash("yaml", content))
	}
	if cur.Name != "config" {
		t.Errorf("name = %q, want config", cur.Name)
	}

	hist, err := s.History("config", 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 0 {
		t.Fatalf("expected empty history after first import, got %d", len(hist))
	}
}

func TestReImportArchivesPrior(t *testing.T) {
	s := openTemp(t)

	v1 := []byte("output: terminal\n")
	v2 := []byte("output: json\n")
	if err := s.Import("config", v1, "yaml"); err != nil {
		t.Fatalf("Import v1: %v", err)
	}
	if err := s.Import("config", v2, "yaml"); err != nil {
		t.Fatalf("Import v2: %v", err)
	}

	cur, ok, err := s.Current("config")
	if err != nil || !ok {
		t.Fatalf("Current: ok=%v err=%v", ok, err)
	}
	if cur.Content != string(v2) {
		t.Errorf("current content = %q, want %q", cur.Content, v2)
	}

	hist, err := s.History("config", 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("expected 1 archived version, got %d", len(hist))
	}
	if hist[0].Content != string(v1) {
		t.Errorf("archived content = %q, want %q", hist[0].Content, v1)
	}
}

func TestNamesAreIndependent(t *testing.T) {
	s := openTemp(t)

	cfg := []byte("output: terminal\n")
	qry := []byte("SELECT 1\n")
	if err := s.Import("config", cfg, "yaml"); err != nil {
		t.Fatalf("Import config: %v", err)
	}
	if err := s.Import("queries", qry, "sql"); err != nil {
		t.Fatalf("Import queries: %v", err)
	}

	c, ok, err := s.Current("config")
	if err != nil || !ok || c.Content != string(cfg) || c.Format != "yaml" {
		t.Fatalf("config current = %+v ok=%v err=%v", c, ok, err)
	}
	q, ok, err := s.Current("queries")
	if err != nil || !ok || q.Content != string(qry) || q.Format != "sql" {
		t.Fatalf("queries current = %+v ok=%v err=%v", q, ok, err)
	}

	if err := s.Import("config", []byte("output: json\n"), "yaml"); err != nil {
		t.Fatalf("re-import config: %v", err)
	}
	ch, _ := s.History("config", 10)
	if len(ch) != 1 {
		t.Errorf("config history = %d, want 1", len(ch))
	}
	qh, _ := s.History("queries", 10)
	if len(qh) != 0 {
		t.Errorf("queries history = %d, want 0", len(qh))
	}
}

func TestHistoryNewestFirst(t *testing.T) {
	s := openTemp(t)

	versions := [][]byte{
		[]byte("v1\n"),
		[]byte("v2\n"),
		[]byte("v3\n"),
	}
	for _, v := range versions {
		if err := s.Import("config", v, "yaml"); err != nil {
			t.Fatalf("Import %q: %v", v, err)
		}
	}

	hist, err := s.History("config", 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("history len = %d, want 2", len(hist))
	}
	if hist[0].Content != "v2\n" {
		t.Errorf("hist[0] = %q, want v2", hist[0].Content)
	}
	if hist[1].Content != "v1\n" {
		t.Errorf("hist[1] = %q, want v1", hist[1].Content)
	}
	if hist[0].At.Before(hist[1].At) {
		t.Errorf("history not newest-first: %v before %v", hist[0].At, hist[1].At)
	}
}

func TestNilStore(t *testing.T) {
	var s *Store

	v, ok, err := s.Current("config")
	if ok || err != nil {
		t.Errorf("nil Current = %+v ok=%v err=%v", v, ok, err)
	}
	if err := s.Import("config", []byte("x"), "yaml"); err == nil {
		t.Error("nil Import should error")
	}
	if err := s.Close(); err != nil {
		t.Errorf("nil Close = %v", err)
	}
}
