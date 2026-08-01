package tabular

import (
	"testing"
	"time"
)

func TestRowStrBoundsSafe(t *testing.T) {
	r := Row{"a", "b"}
	if got := r.Str(1); got != "b" {
		t.Fatalf("Str(1) = %q, want b", got)
	}
	if got := r.Str(-1); got != "" {
		t.Fatalf("Str(-1) = %q, want empty", got)
	}
	if got := r.Str(2); got != "" {
		t.Fatalf("Str(2) = %q, want empty", got)
	}
	if got := Row(nil).Str(0); got != "" {
		t.Fatalf("nil row Str(0) = %q, want empty", got)
	}
}

func TestResultRowOutOfRange(t *testing.T) {
	res := Result{Columns: []string{"id"}, Rows: [][]string{{"42"}}}
	if res.Row(1) != nil || res.Row(-1) != nil {
		t.Fatal("out-of-range Row should be nil")
	}
	if _, err := res.Row(1).Int64(0); err == nil {
		t.Fatal("nil row Int64 should error, not panic")
	}
}

func TestRowInt64(t *testing.T) {
	r := Row{"42", " 7 ", "not-a-number"}
	if id, err := r.Int64(0); err != nil || id != 42 {
		t.Fatalf("Int64(0) = (%d, %v), want (42, nil)", id, err)
	}
	if id, err := r.Int64(1); err != nil || id != 7 {
		t.Fatalf("Int64(1) with padding = (%d, %v), want (7, nil)", id, err)
	}
	if _, err := r.Int64(2); err == nil {
		t.Fatal("Int64 on a non-number should error")
	}
	if _, err := r.Int64(3); err == nil {
		t.Fatal("Int64 out of range should error")
	}
}

func TestRowBool(t *testing.T) {
	cases := map[string]bool{
		"true": true, "TRUE": true, "t": true, "1": true,
		"false": false, "0": false, "yes": false, "NULL": false, "": false,
	}
	for in, want := range cases {
		if got := (Row{in}).Bool(0); got != want {
			t.Errorf("Bool(%q) = %v, want %v", in, got, want)
		}
	}
	if (Row{}).Bool(0) {
		t.Error("out-of-range Bool should be false")
	}
}

func TestRowTimeLayouts(t *testing.T) {
	want := time.Date(2026, 7, 29, 2, 10, 54, 875310000, time.UTC)
	whole := time.Date(2026, 7, 29, 2, 10, 54, 0, time.UTC)

	cases := []struct {
		name string
		in   string
		want time.Time
	}{
		{"duckdb go string", "2026-07-29 02:10:54.87531 +0000 UTC", want},
		{"duckdb go string whole second", "2026-07-29 02:10:54 +0000 UTC", whole},
		{"go string offset only", "2026-07-29 02:10:54.87531 +0000", want},
		{"go string non utc zone", "2026-07-28 22:10:54.87531 -0400 EDT", want},
		{"rfc3339nano", "2026-07-29T02:10:54.87531Z", want},
		{"rfc3339", "2026-07-29T02:10:54Z", whole},
		{"rfc3339 with offset", "2026-07-28T22:10:54-04:00", whole},
		{"naive", "2026-07-29 02:10:54", whole},
		{"date only", "2026-07-29", time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)},
		{"monotonic suffix", "2026-07-29 02:10:54.87531 +0000 UTC m=+7200.492691676", want},
		{"padded", "  2026-07-29 02:10:54.87531 +0000 UTC  ", want},
	}
	for _, c := range cases {
		got, ok := (Row{c.in}).Time(0)
		if !ok {
			t.Errorf("%s: Time(%q) not parsed", c.name, c.in)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("%s: Time(%q) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestRowTimeAbsent(t *testing.T) {
	for _, in := range []string{"", "NULL", "null", "not a time"} {
		if _, ok := (Row{in}).Time(0); ok {
			t.Errorf("Time(%q) parsed, want absent", in)
		}
	}
	if _, ok := (Row{}).Time(0); ok {
		t.Error("out-of-range Time should be absent")
	}
}
