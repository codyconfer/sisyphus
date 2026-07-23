package journal

import (
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "journal.duckdb"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestBeginRollUpChildren(t *testing.T) {
	s := openTemp(t)
	now := time.Now()

	parent, err := s.Begin("flight", "morning", map[string]string{"role": "oncall"})
	if err != nil || parent == 0 {
		t.Fatalf("Begin = %d, %v", parent, err)
	}
	if _, err := s.Add(Run{ParentID: parent, Kind: "query", Name: "a", Started: now, Finished: now, Count: 2},
		[]Record{{Ts: now, Attrs: map[string]string{"title": "x"}}, {Attrs: map[string]string{"title": "y"}}}); err != nil {
		t.Fatalf("Add a: %v", err)
	}
	if _, err := s.Add(Run{ParentID: parent, Kind: "query", Name: "b", Started: now, Finished: now, Count: 1},
		[]Record{{Attrs: map[string]string{"title": "z"}}}); err != nil {
		t.Fatalf("Add b: %v", err)
	}
	if err := s.RollUp(parent); err != nil {
		t.Fatalf("RollUp: %v", err)
	}

	top, err := s.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || top[0].Kind != "flight" || top[0].Name != "morning" {
		t.Fatalf("Recent = %+v", top)
	}
	if top[0].Count != 3 {
		t.Errorf("rolled-up count = %d, want 3", top[0].Count)
	}
	if top[0].Attrs["role"] != "oncall" {
		t.Errorf("attrs = %v", top[0].Attrs)
	}
	if top[0].Finished.IsZero() {
		t.Error("RollUp should set finished")
	}

	children, err := s.Children(parent)
	if err != nil || len(children) != 2 {
		t.Fatalf("Children = %v, %v", children, err)
	}
	recs, err := s.Records(children[0].ID)
	if err != nil || len(recs) != 2 {
		t.Fatalf("Records = %v, %v", recs, err)
	}
	if recs[0].Attrs["title"] == "" {
		t.Errorf("record attrs lost: %+v", recs[0])
	}
}

func TestAddStandaloneWithError(t *testing.T) {
	s := openTemp(t)
	now := time.Now()
	id, err := s.Add(Run{Kind: "write", Name: "tasks add", Started: now, Finished: now, Count: 1, Error: "boom"}, nil)
	if err != nil || id == 0 {
		t.Fatalf("Add = %d, %v", id, err)
	}
	got, ok, err := s.Get(id)
	if err != nil || !ok || got.Error != "boom" || got.Kind != "write" {
		t.Fatalf("Get = %+v ok=%v err=%v", got, ok, err)
	}
	if top, _ := s.Recent(10); len(top) != 1 {
		t.Fatalf("Recent = %d, want 1", len(top))
	}
}

func TestNilStore(t *testing.T) {
	var s *Store
	if id, err := s.Begin("k", "n", nil); id != 0 || err == nil {
		t.Errorf("nil Begin = %d, %v", id, err)
	}
	if id, err := s.Add(Run{}, nil); id != 0 || err == nil {
		t.Errorf("nil Add = %d, %v", id, err)
	}
	if err := s.RollUp(1); err != nil {
		t.Errorf("nil RollUp = %v", err)
	}
	if runs, err := s.Recent(5); err != nil || runs != nil {
		t.Errorf("nil Recent = %v %v", runs, err)
	}
	if runs, err := s.Children(1); err != nil || runs != nil {
		t.Errorf("nil Children = %v %v", runs, err)
	}
	if run, ok, err := s.Get(1); err != nil || ok || run.ID != 0 {
		t.Errorf("nil Get = %+v %v %v", run, ok, err)
	}
	if recs, err := s.Records(1); err != nil || recs != nil {
		t.Errorf("nil Records = %v %v", recs, err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("nil Close = %v", err)
	}
}
