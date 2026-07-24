package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/codyconfer/sisyphus/kv"
)

func TestQuery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "q.duckdb")
	s, err := kv.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "ns", "k", "v", time.Time{}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	res, err := Query(ctx, path, `SELECT key, value FROM kv WHERE namespace = ?`, "ns")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Columns) != 2 || len(res.Rows) != 1 {
		t.Fatalf("Query = %+v", res)
	}
	if res.Rows[0][0] != "k" || res.Rows[0][1] != "v" {
		t.Fatalf("row = %v", res.Rows[0])
	}
}

func TestQueryCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Query(ctx, filepath.Join(t.TempDir(), "x.duckdb"), "SELECT 1"); err == nil {
		t.Fatal("expected canceled context error")
	}
}
