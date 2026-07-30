package journal

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func checkpoint(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	if err := s.h.Do(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `CHECKPOINT`)
		return err
	}); err != nil {
		t.Fatalf("CHECKPOINT: %v", err)
	}
}

func fileSize(t *testing.T, s *Store) int64 {
	t.Helper()
	checkpoint(t, s)
	fi, err := os.Stat(s.h.Path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	return fi.Size()
}

func usedBlocks(t *testing.T, s *Store) int64 {
	t.Helper()
	var used int64
	ctx := context.Background()
	if err := s.h.Do(ctx, func(db *sql.DB) error {
		return db.QueryRowContext(ctx, `SELECT used_blocks FROM pragma_database_size()`).Scan(&used)
	}); err != nil {
		t.Fatalf("pragma_database_size: %v", err)
	}
	return used
}

func peakOf(vals []int64) int64 {
	var high int64
	for _, v := range vals {
		high = max(high, v)
	}
	return high
}

func tableCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	ctx := context.Background()
	if err := s.h.Do(ctx, func(db *sql.DB) error {
		return db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n)
	}); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func addRun(t *testing.T, s *Store, name string, started time.Time, records int) int64 {
	t.Helper()
	id, err := s.Add(context.Background(), Run{Kind: "query", Name: name, Started: started,
		Finished: started.Add(time.Second), Count: records}, recordFixtures(records))
	if err != nil {
		t.Fatalf("Add %s: %v", name, err)
	}
	return id
}

func addChild(t *testing.T, s *Store, parent int64, name string, started time.Time, records int) int64 {
	t.Helper()
	id, err := s.Add(context.Background(), Run{ParentID: parent, Kind: "signal", Name: name, Started: started,
		Finished: started.Add(time.Second), Count: records}, recordFixtures(records))
	if err != nil {
		t.Fatalf("Add child %s: %v", name, err)
	}
	return id
}

func TestPruneRemovesOldRunsChildrenAndRecords(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	cutoff := fixtureBase.Add(24 * time.Hour)

	old := addRun(t, s, "old", fixtureBase, 3)
	oldChild := addChild(t, s, old, "old-child", fixtureBase.Add(40*time.Hour), 5)
	fresh := addRun(t, s, "fresh", fixtureBase.Add(48*time.Hour), 4)
	freshChild := addChild(t, s, fresh, "fresh-child", fixtureBase.Add(49*time.Hour), 2)

	removed, err := s.Prune(ctx, cutoff)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 1 {
		t.Errorf("Prune removed %d runs, want 1", removed)
	}
	if _, ok, err := s.Get(ctx, old); ok || err != nil {
		t.Errorf("Get(old) = ok %v, err %v, want gone", ok, err)
	}
	if _, ok, err := s.Get(ctx, oldChild); ok || err != nil {
		t.Errorf("Get(oldChild) = ok %v, err %v, want gone even though it started after the cutoff", ok, err)
	}
	if recs, err := s.Records(ctx, old); err != nil || len(recs) != 0 {
		t.Errorf("Records(old) = %d, %v, want none", len(recs), err)
	}
	if recs, err := s.Records(ctx, oldChild); err != nil || len(recs) != 0 {
		t.Errorf("Records(oldChild) = %d, %v, want none", len(recs), err)
	}

	if got, ok, err := s.Get(ctx, fresh); !ok || err != nil || got.Name != "fresh" {
		t.Errorf("Get(fresh) = %+v ok %v err %v, want intact", got, ok, err)
	}
	if kids, err := s.Children(ctx, fresh); err != nil || len(kids) != 1 || kids[0].ID != freshChild {
		t.Errorf("Children(fresh) = %v, %v, want the surviving child", kids, err)
	}
	want := recordFixtures(4)
	got, err := s.Records(ctx, fresh)
	if err != nil || len(got) != 4 {
		t.Fatalf("Records(fresh) = %d, %v, want 4", len(got), err)
	}
	for i, g := range got {
		w := want[len(want)-1-i]
		if !g.Ts.Equal(w.Ts) || !maps.Equal(g.Attrs, w.Attrs) {
			t.Errorf("surviving record %d = %+v, want %+v", i, g, w)
		}
	}
	if n := tableCount(t, s, "records"); n != 6 {
		t.Errorf("records left = %d, want 6 (fresh 4 + fresh child 2)", n)
	}
	if n := tableCount(t, s, "runs"); n != 2 {
		t.Errorf("runs left = %d, want 2", n)
	}
}

func TestPruneReclaimsDiskSpace(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	for i := range 80 {
		addRun(t, s, "run", fixtureBase.Add(time.Duration(i)*time.Hour), 40)
	}
	before := fileSize(t, s)
	if before < 512*1024 {
		t.Fatalf("fixture journal is only %d bytes; too small to measure reclamation", before)
	}

	removed, err := s.Prune(ctx, fixtureBase.Add(1000*time.Hour))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 80 {
		t.Fatalf("Prune removed %d runs, want 80", removed)
	}
	if n := tableCount(t, s, "runs"); n != 0 {
		t.Errorf("runs after full prune = %d, want 0", n)
	}
	if n := tableCount(t, s, "records"); n != 0 {
		t.Errorf("records after full prune = %d, want 0", n)
	}

	after := fileSize(t, s)
	if after >= before/2 {
		t.Errorf("journal file is %d bytes after pruning every run, was %d: pruning must return the space, not just the rows", after, before)
	}
	if used := usedBlocks(t, s); used > 1 {
		t.Errorf("used_blocks after full prune = %d, want at most 1", used)
	}
	if _, err := s.Add(ctx, Run{Kind: "query", Name: "after", Started: fixtureBase, Finished: fixtureBase, Count: 1},
		recordFixtures(1)); err != nil {
		t.Errorf("Add after prune: %v", err)
	}
	if runs, err := s.Recent(ctx, 10); err != nil || len(runs) != 1 {
		t.Errorf("Recent after prune = %d runs, %v, want 1", len(runs), err)
	}
}

func TestRetainBoundsJournalGrowth(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	control := openAt(t, filepath.Join(dir, "control.duckdb"))
	bounded := openAt(t, filepath.Join(dir, "bounded.duckdb"))

	const gens = 5
	const perGen = 20
	const perRun = 200
	var controlUsed, boundedUsed, controlSize, boundedSize []int64
	for gen := range gens {
		for i := range perGen {
			seq := gen*perGen + i
			started := fixtureBase.Add(time.Duration(seq) * time.Hour)
			run := Run{Kind: "query", Name: "run", Started: started, Finished: started.Add(time.Second), Count: perRun}
			recs := bulkyRecords(seq, perRun)
			if _, err := control.Add(ctx, run, recs); err != nil {
				t.Fatalf("control Add: %v", err)
			}
			if _, err := bounded.Add(ctx, run, recs); err != nil {
				t.Fatalf("bounded Add: %v", err)
			}
		}
		removed, err := bounded.Retain(ctx, perGen)
		if err != nil {
			t.Fatalf("Retain gen %d: %v", gen, err)
		}
		if gen == 0 && removed != 0 {
			t.Errorf("Retain removed %d on the first generation, want 0", removed)
		}
		if gen > 0 && removed != perGen {
			t.Errorf("Retain gen %d removed %d, want %d", gen, removed, perGen)
		}
		controlSize = append(controlSize, fileSize(t, control))
		boundedSize = append(boundedSize, fileSize(t, bounded))
		controlUsed = append(controlUsed, usedBlocks(t, control))
		boundedUsed = append(boundedUsed, usedBlocks(t, bounded))
	}
	t.Logf("used blocks: control %v, retained %v", controlUsed, boundedUsed)
	t.Logf("file bytes:  control %v, retained %v", controlSize, boundedSize)

	if n := tableCount(t, bounded, "runs"); n != perGen {
		t.Errorf("retained runs = %d, want %d", n, perGen)
	}
	if n := tableCount(t, bounded, "records"); n != perGen*perRun {
		t.Errorf("retained records = %d, want %d", n, perGen*perRun)
	}
	if controlSize[gens-1] <= 2*controlSize[0] || controlUsed[gens-1] <= controlUsed[0] {
		t.Fatalf("unpruned journal grew only %d -> %d bytes (%d -> %d blocks); fixture is too small to measure retention",
			controlSize[0], controlSize[gens-1], controlUsed[0], controlUsed[gens-1])
	}
	if drift := boundedUsed[gens-1] - boundedUsed[0]; drift > 2 {
		t.Errorf("retained journal drifted %d used blocks across %d generations (%v): retention must reach a steady state",
			drift, gens, boundedUsed)
	}
	if peak := peakOf(boundedUsed); peak >= controlUsed[gens-1] {
		t.Errorf("retained journal peaked at %d used blocks while the unpruned one reached %d: retention is not bounding growth",
			peak, controlUsed[gens-1])
	}
	if peak := peakOf(boundedSize); peak >= controlSize[gens-1] {
		t.Errorf("retained journal peaked at %d bytes while the unpruned one reached %d bytes",
			peak, controlSize[gens-1])
	}
}

func TestRetainKeepsNewestRuns(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	var ids []int64
	for i := range 10 {
		id := addRun(t, s, "run", fixtureBase.Add(time.Duration(i)*time.Hour), 2)
		addChild(t, s, id, "child", fixtureBase.Add(time.Duration(i)*time.Hour), 3)
		ids = append(ids, id)
	}

	removed, err := s.Retain(ctx, 3)
	if err != nil {
		t.Fatalf("Retain: %v", err)
	}
	if removed != 7 {
		t.Errorf("Retain(3) removed %d, want 7", removed)
	}
	runs, err := s.Recent(ctx, 20)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("Recent after Retain(3) = %d runs, want 3", len(runs))
	}
	for i, r := range runs {
		if want := ids[9-i]; r.ID != want {
			t.Errorf("survivor %d = run %d, want %d (newest first)", i, r.ID, want)
		}
	}
	if n := tableCount(t, s, "runs"); n != 6 {
		t.Errorf("runs left = %d, want 6 (3 kept plus their children)", n)
	}
	if n := tableCount(t, s, "records"); n != 15 {
		t.Errorf("records left = %d, want 15", n)
	}
	for _, id := range ids[:7] {
		if _, ok, err := s.Get(ctx, id); ok || err != nil {
			t.Errorf("Get(%d) = ok %v err %v, want pruned", id, ok, err)
		}
	}
	for _, id := range ids[7:] {
		if kids, err := s.Children(ctx, id); err != nil || len(kids) != 1 {
			t.Errorf("Children(%d) = %v, %v, want 1", id, kids, err)
		}
		if recs, err := s.Records(ctx, id); err != nil || len(recs) != 2 {
			t.Errorf("Records(%d) = %d, %v, want 2", id, len(recs), err)
		}
	}
}

func TestRetainMoreThanPresentIsNoop(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	for i := range 3 {
		addRun(t, s, "run", fixtureBase.Add(time.Duration(i)*time.Hour), 5)
	}
	before := fileSize(t, s)
	removed, err := s.Retain(ctx, 50)
	if err != nil || removed != 0 {
		t.Fatalf("Retain(50) = %d, %v, want 0, nil", removed, err)
	}
	if after := fileSize(t, s); after != before {
		t.Errorf("Retain with nothing to remove changed the file: %d -> %d", before, after)
	}
	if n := tableCount(t, s, "records"); n != 15 {
		t.Errorf("records = %d, want 15", n)
	}
}

func TestPruneNothingOlderIsNoop(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	for i := range 3 {
		addRun(t, s, "run", fixtureBase.Add(time.Duration(i)*time.Hour), 5)
	}
	before := fileSize(t, s)
	removed, err := s.Prune(ctx, fixtureBase.Add(-time.Hour))
	if err != nil || removed != 0 {
		t.Fatalf("Prune(before everything) = %d, %v, want 0, nil", removed, err)
	}
	if after := fileSize(t, s); after != before {
		t.Errorf("Prune with nothing to remove changed the file: %d -> %d", before, after)
	}
	if n := tableCount(t, s, "runs"); n != 3 {
		t.Errorf("runs = %d, want 3", n)
	}
}

func TestPruneZeroTimeAndRetainNonPositiveAreNoops(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	addRun(t, s, "run", fixtureBase, 4)
	for _, tc := range []struct {
		name string
		call func() (int, error)
	}{
		{"Prune(zero)", func() (int, error) { return s.Prune(ctx, time.Time{}) }},
		{"Retain(0)", func() (int, error) { return s.Retain(ctx, 0) }},
		{"Retain(-1)", func() (int, error) { return s.Retain(ctx, -1) }},
	} {
		removed, err := tc.call()
		if err != nil || removed != 0 {
			t.Errorf("%s = %d, %v, want 0, nil", tc.name, removed, err)
		}
	}
	if n := tableCount(t, s, "runs"); n != 1 {
		t.Errorf("runs = %d, want 1: an unset retention must not empty the journal", n)
	}
	if n := tableCount(t, s, "records"); n != 4 {
		t.Errorf("records = %d, want 4", n)
	}
}

func TestPruneRollsBackWhenReclaimFails(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	old := addRun(t, s, "old", fixtureBase, 5)
	addChild(t, s, old, "old-child", fixtureBase, 3)
	fresh := addRun(t, s, "fresh", fixtureBase.Add(48*time.Hour), 4)
	if err := s.h.Do(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `CREATE VIEW runs_compact AS SELECT 1 AS x`)
		return err
	}); err != nil {
		t.Fatalf("inject blocking view: %v", err)
	}

	removed, err := s.Prune(ctx, fixtureBase.Add(24*time.Hour))
	if err == nil {
		t.Fatal("Prune succeeded with reclamation blocked, want failure")
	}
	if removed != 0 {
		t.Errorf("failed Prune reported %d removed, want 0", removed)
	}
	if n := tableCount(t, s, "runs"); n != 3 {
		t.Errorf("runs after failed Prune = %d, want 3: the whole prune must roll back", n)
	}
	if n := tableCount(t, s, "records"); n != 12 {
		t.Errorf("records after failed Prune = %d, want 12", n)
	}
	if _, ok, err := s.Get(ctx, old); !ok || err != nil {
		t.Errorf("Get(old) = ok %v err %v, want still present", ok, err)
	}
	if recs, err := s.Records(ctx, fresh); err != nil || len(recs) != 4 {
		t.Errorf("Records(fresh) = %d, %v, want 4", len(recs), err)
	}
}

func TestPruneRetainUnavailable(t *testing.T) {
	var nilStore *Store
	if n, err := nilStore.Prune(context.Background(), time.Now()); n != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Prune = %d, %v, want ErrUnavailable", n, err)
	}
	if n, err := nilStore.Retain(context.Background(), 5); n != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Retain = %d, %v, want ErrUnavailable", n, err)
	}
	s := openTemp(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Prune(context.Background(), time.Now()); n != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Prune = %d, %v, want ErrUnavailable", n, err)
	}
	if n, err := s.Retain(context.Background(), 5); n != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed Retain = %d, %v, want ErrUnavailable", n, err)
	}
}
