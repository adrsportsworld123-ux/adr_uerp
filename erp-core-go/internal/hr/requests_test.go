package hr

import (
	"testing"
	"time"
)

func TestLeaveYearOf(t *testing.T) {
	d := func(s string) time.Time { v, _ := time.Parse("2006-01-02", s); return v }
	cases := []struct {
		day   string
		start int
		want  int
	}{
		{"2026-01-01", 1, 2026}, {"2026-12-31", 1, 2026},
		{"2026-03-31", 4, 2025}, {"2026-04-01", 4, 2026}, {"2027-01-10", 4, 2026},
	}
	for _, c := range cases {
		if got := leaveYearOf(d(c.day), c.start); got != c.want {
			t.Errorf("leaveYearOf(%s, %d) = %d, want %d", c.day, c.start, got, c.want)
		}
	}
	from, to := leaveYearRange(2026, 4)
	if from.Format("2006-01-02") != "2026-04-01" || to.Format("2006-01-02") != "2027-03-31" {
		t.Errorf("leaveYearRange(2026, 4) = %s … %s", from, to)
	}
}

func TestFreeMinutes(t *testing.T) {
	cases := []struct{ minutes, usedCount, usedFree, freeCount, freeMins, want int }{
		{120, 0, 0, 2, 240, 120},   // first: all free
		{150, 1, 120, 2, 240, 120}, // second: what is left
		{60, 2, 240, 2, 240, 0},    // over the count
		{60, 1, 240, 2, 240, 0},    // minutes used up
		{60, 0, 0, 0, 240, 0},      // no free permissions at all
	}
	for _, c := range cases {
		if got := freeMinutes(c.minutes, c.usedCount, c.usedFree, c.freeCount, c.freeMins); got != c.want {
			t.Errorf("freeMinutes%v = %d, want %d", c, got, c.want)
		}
	}
}

func TestHoursText(t *testing.T) {
	for m, want := range map[int]string{240: "4 h", 90: "1 h 30 min", 45: "45 min"} {
		if got := hoursText(m); got != want {
			t.Errorf("hoursText(%d) = %q, want %q", m, got, want)
		}
	}
}
