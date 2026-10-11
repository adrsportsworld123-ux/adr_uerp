package purchase

import (
	"errors"
	"time"
)

// Purchases are often entered after the fact (a supplier's invoice keyed in
// days later), so a receipt, a bill and a payment can each carry the date
// it really happened. Ledger entries follow that date, which keeps the
// month's books and GST input credit right; the period lock still applies.

var ist = time.FixedZone("IST", 5*3600+1800)

var errPaidBeforeBill = errors.New("a payment can't be dated before its bill")

// pastDate parses an optional YYYY-MM-DD that isn't in the future (India
// time). "" gives nil: today.
func pastDate(s string) (*string, error) {
	if s == "" {
		return nil, nil
	}
	d, err := time.ParseInLocation("2006-01-02", s, ist)
	if err != nil {
		return nil, errors.New("use YYYY-MM-DD")
	}
	now := time.Now().In(ist)
	if d.After(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, ist)) {
		return nil, errors.New("can't be in the future")
	}
	if d.Year() < 2000 {
		return nil, errors.New("is too far in the past")
	}
	return &s, nil
}

// dateOrToday gives the ledger date for an optional date.
func dateOrToday(d *string) string {
	if d != nil {
		return *d
	}
	return time.Now().In(ist).Format("2006-01-02")
}
