package journal

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "journal.duckdb"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestBeginRollUpChildren(t *testing.T) {
	s := openTemp(t)
	now := time.Now()

	parent, err := s.Begin(context.Background(), "job", "nightly", map[string]string{"env": "prod"})
	if err != nil || parent == 0 {
		t.Fatalf("Begin = %d, %v", parent, err)
	}
	if _, err := s.Add(context.Background(), Run{ParentID: parent, Kind: "query", Name: "a", Started: now, Finished: now, Count: 2},
		[]Record{{Ts: now, Attrs: map[string]string{"title": "x"}}, {Attrs: map[string]string{"title": "y"}}}); err != nil {
		t.Fatalf("Add a: %v", err)
	}
	if _, err := s.Add(context.Background(), Run{ParentID: parent, Kind: "query", Name: "b", Started: now, Finished: now, Count: 1},
		[]Record{{Attrs: map[string]string{"title": "z"}}}); err != nil {
		t.Fatalf("Add b: %v", err)
	}
	if err := s.RollUp(context.Background(), parent); err != nil {
		t.Fatalf("RollUp: %v", err)
	}

	top, err := s.Recent(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || top[0].Kind != "job" || top[0].Name != "nightly" {
		t.Fatalf("Recent = %+v", top)
	}
	if top[0].Count != 3 {
		t.Errorf("rolled-up count = %d, want 3", top[0].Count)
	}
	if top[0].Attrs["env"] != "prod" {
		t.Errorf("attrs = %v", top[0].Attrs)
	}
	if top[0].Finished.IsZero() {
		t.Error("RollUp should set finished")
	}

	children, err := s.Children(context.Background(), parent)
	if err != nil || len(children) != 2 {
		t.Fatalf("Children = %v, %v", children, err)
	}
	recs, err := s.Records(context.Background(), children[0].ID)
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
	id, err := s.Add(context.Background(), Run{Kind: "write", Name: "tasks add", Started: now, Finished: now, Count: 1, Error: "boom"}, nil)
	if err != nil || id == 0 {
		t.Fatalf("Add = %d, %v", id, err)
	}
	got, ok, err := s.Get(context.Background(), id)
	if err != nil || !ok || got.Error != "boom" || got.Kind != "write" {
		t.Fatalf("Get = %+v ok=%v err=%v", got, ok, err)
	}
	if top, _ := s.Recent(context.Background(), 10); len(top) != 1 {
		t.Fatalf("Recent = %d, want 1", len(top))
	}
}

func TestNilStore(t *testing.T) {
	var s *Store
	if id, err := s.Begin(context.Background(), "k", "n", nil); id != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Begin = %d, %v", id, err)
	}
	if id, err := s.Add(context.Background(), Run{}, nil); id != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Add = %d, %v", id, err)
	}
	if err := s.RollUp(context.Background(), 1); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil RollUp = %v, want ErrUnavailable", err)
	}
	if err := s.RollUp(context.Background(), 0); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil RollUp(0) = %v, want ErrUnavailable", err)
	}
	if runs, err := s.Recent(context.Background(), 5); runs != nil || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Recent = %v %v, want ErrUnavailable", runs, err)
	}
	if runs, err := s.Children(context.Background(), 1); runs != nil || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Children = %v %v, want ErrUnavailable", runs, err)
	}
	if run, ok, err := s.Get(context.Background(), 1); ok || run.ID != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Get = %+v %v %v, want ErrUnavailable", run, ok, err)
	}
	if recs, err := s.Records(context.Background(), 1); recs != nil || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Records = %v %v, want ErrUnavailable", recs, err)
	}
	if _, err := s.Query(context.Background(), "SELECT 1"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Query = %v, want ErrUnavailable", err)
	}
	if err := s.Delete(context.Background(), 1); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Delete = %v, want ErrUnavailable", err)
	}
	if n, err := s.Prune(context.Background(), time.Now()); n != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Prune = %d, %v, want ErrUnavailable", n, err)
	}
	if n, err := s.Retain(context.Background(), 5); n != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Retain = %d, %v, want ErrUnavailable", n, err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("nil Close = %v", err)
	}
}

func TestClosedStoreUnavailable(t *testing.T) {
	s := openTemp(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Begin(context.Background(), "k", "n", nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Begin = %v, want ErrUnavailable", err)
	}
	if _, err := s.Add(context.Background(), Run{}, nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Add = %v, want ErrUnavailable", err)
	}
	if err := s.RollUp(context.Background(), 1); !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed RollUp = %v, want ErrUnavailable", err)
	}
	if runs, err := s.Recent(context.Background(), 5); runs != nil || !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Recent = %v %v, want ErrUnavailable", runs, err)
	}
	if runs, err := s.Children(context.Background(), 1); runs != nil || !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Children = %v %v, want ErrUnavailable", runs, err)
	}
	if _, ok, err := s.Get(context.Background(), 1); ok || !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Get = ok %v err %v, want ErrUnavailable", ok, err)
	}
	if recs, err := s.Records(context.Background(), 1); recs != nil || !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Records = %v %v, want ErrUnavailable", recs, err)
	}
	if _, err := s.Query(context.Background(), "SELECT 1"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Query = %v, want ErrUnavailable", err)
	}
	if err := s.Delete(context.Background(), 1); !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Delete = %v, want ErrUnavailable", err)
	}
	if n, err := s.Prune(context.Background(), time.Now()); n != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Prune = %d, %v, want ErrUnavailable", n, err)
	}
	if n, err := s.Retain(context.Background(), 5); n != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Retain = %d, %v, want ErrUnavailable", n, err)
	}
}

func TestRollUpZeroIDNoop(t *testing.T) {
	s := openTemp(t)
	if err := s.RollUp(context.Background(), 0); err != nil {
		t.Errorf("RollUp(0) = %v, want nil", err)
	}
}

func TestDeleteRemovesRunChildrenAndRecords(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	now := time.Now()

	parent, err := s.Begin(ctx, "job", "nightly", nil)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := s.Add(ctx, Run{ParentID: parent, Kind: "query", Name: "a", Started: now, Finished: now, Count: 1},
		[]Record{{Ts: now, Attrs: map[string]string{"title": "x"}}}); err != nil {
		t.Fatalf("Add child: %v", err)
	}
	keep, err := s.Add(ctx, Run{Kind: "action", Name: "keep", Started: now, Finished: now, Count: 1},
		[]Record{{Ts: now, Attrs: map[string]string{"title": "survivor"}}})
	if err != nil {
		t.Fatalf("Add keep: %v", err)
	}

	if err := s.Delete(ctx, parent); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, ok, err := s.Get(ctx, parent); ok || err != nil {
		t.Errorf("Get(deleted) = ok %v, err %v, want gone", ok, err)
	}
	if children, err := s.Children(ctx, parent); err != nil || len(children) != 0 {
		t.Errorf("Children(deleted) = %v, %v, want none", children, err)
	}
	res, err := s.Query(ctx, `SELECT count(*) FROM records`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "1" {
		t.Errorf("records after delete = %v, want only the surviving run's", res.Rows)
	}
	if recs, err := s.Records(ctx, keep); err != nil || len(recs) != 1 {
		t.Errorf("Records(keep) = %v, %v, want 1", recs, err)
	}
	if runs, err := s.Recent(ctx, 10); err != nil || len(runs) != 1 || runs[0].ID != keep {
		t.Errorf("Recent = %v, %v, want only run %d", runs, err, keep)
	}
}

func TestDeleteZeroIDNoopAndUnknownID(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if err := s.Delete(ctx, 0); err != nil {
		t.Errorf("Delete(0) = %v, want nil", err)
	}
	if err := s.Delete(ctx, 4242); err != nil {
		t.Errorf("Delete(unknown) = %v, want nil", err)
	}
}

func TestQuery(t *testing.T) {
	s := openTemp(t)
	id, err := s.Begin(context.Background(), "job", "q", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Query(context.Background(), `SELECT kind, name FROM runs WHERE id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Columns) != 2 || len(res.Rows) != 1 {
		t.Fatalf("Query = %+v", res)
	}
	if res.Rows[0][0] != "job" || res.Rows[0][1] != "q" {
		t.Fatalf("row = %v", res.Rows[0])
	}
}
