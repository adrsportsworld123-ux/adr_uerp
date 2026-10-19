package reportbuilder

import (
	"testing"
	"time"
)

// Every standard report compiles with its default period, and every quick
// filter names a field of its dataset.
func TestLibraryCompiles(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range Library {
		if seen[l.Key] {
			t.Errorf("duplicate key %s", l.Key)
		}
		seen[l.Key] = true
		ds, ok := FindDataset(l.Spec.Dataset)
		if !ok {
			t.Errorf("%s: unknown dataset %s", l.Key, l.Spec.Dataset)
			continue
		}
		if ds.HasDateRange && l.DefaultPeriod == "" {
			t.Errorf("%s: a dated report needs a default period", l.Key)
		}
		if _, err := Compile(resolved(l.specFor(libraryRequest{})), time.Time{}, PreviewRows); err != nil {
			t.Errorf("%s: %v", l.Key, err)
		}
		for _, k := range l.QuickFilters {
			if _, ok := ds.Field(k); !ok {
				t.Errorf("%s: quick filter %s isn't a field of %s", l.Key, k, ds.Key)
			}
		}
	}
}

func TestResolvePeriod(t *testing.T) {
	// Sunday 18 Oct 2026, 23:30 UTC = Monday 19 Oct, 05:00 IST.
	now := time.Date(2026, 10, 18, 23, 30, 0, 0, time.UTC)
	cases := map[string][2]string{
		"today":        {"2026-10-19", "2026-10-19"},
		"yesterday":    {"2026-10-18", "2026-10-18"},
		"this_week":    {"2026-10-19", "2026-10-19"},
		"last_week":    {"2026-10-12", "2026-10-18"},
		"this_month":   {"2026-10-01", "2026-10-19"},
		"last_month":   {"2026-09-01", "2026-09-30"},
		"this_quarter": {"2026-10-01", "2026-10-19"},
		"last_quarter": {"2026-07-01", "2026-09-30"},
		"this_fy":      {"2026-04-01", "2026-10-19"},
		"last_fy":      {"2025-04-01", "2026-03-31"},
	}
	for p, want := range cases {
		f, to, ok := ResolvePeriod(p, now)
		if !ok || f != want[0] || to != want[1] {
			t.Errorf("%s: got %s..%s (%v), want %s..%s", p, f, to, ok, want[0], want[1])
		}
	}
	if _, _, ok := ResolvePeriod("next_year", now); ok {
		t.Error("unknown period accepted")
	}
	// Explicit dates win over a period.
	l, _ := FindLibrary("sales_daily")
	s := l.specFor(libraryRequest{Period: "today", DateFrom: "2026-01-01", DateTo: "2026-01-31"})
	if s.DateFrom != "2026-01-01" || s.Period != "" {
		t.Errorf("dates: %+v", s)
	}
}
