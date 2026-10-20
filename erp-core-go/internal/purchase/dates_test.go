package purchase

import (
	"testing"
	"time"

	"erp-core-go/internal/locale"
)

func TestPastDate(t *testing.T) {
	ist := locale.IST
	today := time.Now().In(ist).Format("2006-01-02")
	tomorrow := time.Now().In(ist).AddDate(0, 0, 1).Format("2006-01-02")
	if d, err := pastDate("", ist); d != nil || err != nil {
		t.Errorf("empty: %v %v", d, err)
	}
	for _, ok := range []string{"2026-09-14", today} {
		if d, err := pastDate(ok, ist); err != nil || d == nil || *d != ok {
			t.Errorf("%s: %v %v", ok, d, err)
		}
	}
	for _, bad := range []string{tomorrow, "14/09/2026", "2026-13-01", "1999-12-31"} {
		if _, err := pastDate(bad, ist); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
	if dateOrToday(nil, ist) != today {
		t.Error("nil date is today")
	}
}
