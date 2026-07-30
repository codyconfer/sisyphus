package journal

import (
	"context"
	"database/sql"
	"maps"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

var fixtureBase = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func mix(v uint64) uint64 {
	v += 0x9e3779b97f4a7c15
	v = (v ^ (v >> 30)) * 0xbf58476d1ce4e5b9
	v = (v ^ (v >> 27)) * 0x94d049bb133111eb
	return v ^ (v >> 31)
}

func bulkyRecords(seed, n int) []Record {
	recs := make([]Record, n)
	for i := range recs {
		v := uint64(seed)*1_000_003 + uint64(i)
		attrs := make(map[string]string, 4)
		for f := range 4 {
			var b strings.Builder
			for w := range 4 {
				b.WriteString(strconv.FormatUint(mix(v*97+uint64(f)*13+uint64(w)), 16))
			}
			attrs["field"+strconv.Itoa(f)] = b.String()
		}
		recs[i] = Record{Ts: fixtureBase.Add(time.Duration(v) * time.Millisecond), Attrs: attrs}
	}
	return recs
}

func recordFixtures(n int) []Record {
	recs := make([]Record, n)
	for i := range recs {
		id := strconv.Itoa(i)
		recs[i] = Record{
			Ts: fixtureBase.Add(time.Duration(i) * time.Second),
			Attrs: map[string]string{
				"signal":   "github",
				"index":    id,
				"kind":     "pull_request",
				"title":    "Fix the flaky integration test in the audit path #" + id,
				"subtitle": "codyconfer/munin - opened by codyconfer",
				"url":      "https://github.com/codyconfer/munin/pull/" + id,
			},
		}
	}
	return recs
}

func recordSignature(r Record) string {
	keys := make([]string, 0, len(r.Attrs))
	for k := range r.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(r.Ts.UTC().Format(time.RFC3339Nano))
	if r.Attrs == nil {
		b.WriteString("|nil")
	}
	for _, k := range keys {
		b.WriteString("|" + k + "=" + r.Attrs[k])
	}
	return b.String()
}

func multiset(recs []Record) map[string]int {
	m := make(map[string]int, len(recs))
	for _, r := range recs {
		m[recordSignature(r)]++
	}
	return m
}

func TestAddRecordsRoundTripAcrossChunkSizes(t *testing.T) {
	for _, n := range []int{0, 1, 2, recordChunk - 1, recordChunk, recordChunk + 1, 2 * recordChunk, 1000} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			s := openTemp(t)
			ctx := context.Background()
			want := recordFixtures(n)
			id, err := s.Add(ctx, Run{Kind: "query", Name: "review-queue", Started: fixtureBase,
				Finished: fixtureBase.Add(time.Second), Count: n}, want)
			if err != nil || id == 0 {
				t.Fatalf("Add = %d, %v", id, err)
			}
			got, err := s.Records(ctx, id)
			if err != nil {
				t.Fatalf("Records: %v", err)
			}
			if len(got) != len(want) {
				t.Fatalf("Records returned %d records, want %d", len(got), len(want))
			}
			for i, g := range got {
				w := want[len(want)-1-i]
				if !g.Ts.Equal(w.Ts) {
					t.Fatalf("record %d ts = %s, want %s", i, g.Ts, w.Ts)
				}
				if !maps.Equal(g.Attrs, w.Attrs) {
					t.Fatalf("record %d attrs = %v, want %v", i, g.Attrs, w.Attrs)
				}
			}
			var stored int
			if err := s.h.Do(ctx, func(db *sql.DB) error {
				return db.QueryRowContext(ctx, `SELECT count(*) FROM records WHERE run_id = ?`, id).Scan(&stored)
			}); err != nil {
				t.Fatal(err)
			}
			if stored != n {
				t.Fatalf("stored rows = %d, want %d", stored, n)
			}
		})
	}
}

func TestAddSpanningChunksPreservesNullFields(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	want := recordFixtures(2*recordChunk + 50)
	for i := range want {
		if i%50 == 0 {
			want[i].Ts = time.Time{}
		}
		if i%77 == 0 {
			want[i].Attrs = nil
		}
	}
	id, err := s.Add(ctx, Run{Kind: "query", Name: "nulls", Started: fixtureBase, Finished: fixtureBase, Count: len(want)}, want)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := s.Records(ctx, id)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Records returned %d, want %d", len(got), len(want))
	}
	if gotSet, wantSet := multiset(got), multiset(want); !maps.Equal(gotSet, wantSet) {
		for sig, n := range wantSet {
			if gotSet[sig] != n {
				t.Errorf("signature %q: got %d, want %d", sig, gotSet[sig], n)
			}
		}
		t.Fatal("record contents changed across chunk boundaries")
	}
}

func TestAddRollsBackEntireBatchOnMidBatchFailure(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	survivor, err := s.Add(ctx, Run{Kind: "query", Name: "before", Started: fixtureBase, Finished: fixtureBase, Count: 2}, recordFixtures(2))
	if err != nil {
		t.Fatalf("Add survivor: %v", err)
	}
	if err := s.h.Do(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `ALTER TABLE records ALTER COLUMN attrs SET NOT NULL`)
		return err
	}); err != nil {
		t.Fatalf("inject NOT NULL: %v", err)
	}

	doomed := recordFixtures(recordChunk + 50)
	doomed[recordChunk+40].Attrs = nil
	id, err := s.Add(ctx, Run{Kind: "query", Name: "doomed", Started: fixtureBase, Finished: fixtureBase, Count: len(doomed)}, doomed)
	if err == nil {
		t.Fatal("Add with an unwritable record succeeded, want failure")
	}
	if id != 0 {
		t.Errorf("failed Add returned id %d, want 0", id)
	}

	var runs, records int
	if err := s.h.Do(ctx, func(db *sql.DB) error {
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM runs`).Scan(&runs); err != nil {
			return err
		}
		return db.QueryRowContext(ctx, `SELECT count(*) FROM records`).Scan(&records)
	}); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Errorf("runs after rolled-back Add = %d, want 1", runs)
	}
	if records != 2 {
		t.Errorf("records after rolled-back Add = %d, want 2 (the first chunk must not survive)", records)
	}
	if recs, err := s.Records(ctx, survivor); err != nil || len(recs) != 2 {
		t.Errorf("Records(survivor) = %d records, %v, want 2", len(recs), err)
	}
}

func TestRecordInsertPlaceholders(t *testing.T) {
	for _, n := range []int{1, 2, 7, recordChunk} {
		q := recordInsert(n)
		if got := strings.Count(q, "(?, ?, ?)"); got != n {
			t.Errorf("recordInsert(%d) has %d tuples, want %d", n, got, n)
		}
		if got := strings.Count(q, "?"); got != 3*n {
			t.Errorf("recordInsert(%d) has %d placeholders, want %d", n, got, 3*n)
		}
		if strings.HasSuffix(q, ",") || strings.Contains(q, ",,") {
			t.Errorf("recordInsert(%d) malformed: %s", n, q)
		}
	}
	if fullRecordInsert != recordInsert(recordChunk) {
		t.Error("fullRecordInsert does not match recordInsert(recordChunk)")
	}
}
