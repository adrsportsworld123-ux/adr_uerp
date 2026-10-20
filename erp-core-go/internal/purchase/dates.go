package purchase

import (
	"errors"
	"time"
)

// Purchases are often entered after the fact (a supplier's invoice keyed in
// days later), so a receipt, a bill and a payment can each carry the date
// it really happened. Ledger entries follow that date, which keeps the
// month's books and GST input credit right; the period lock still applies.
// "Today" and "the future" are on the merchant's clock (locale.Location,
// migrations/064; IST unless the merchant set another time zone).

var errPaidBeforeBill = errors.New("a payment can't be dated before its bill")

// pastDate parses an optional YYYY-MM-DD that isn't in the future on loc's
// clock. "" gives nil: today.
func pastDate(s string, loc *time.Location) (*string, error) {
	if s == "" {
		return nil, nil
	}
	d, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return nil, errors.New("use YYYY-MM-DD")
	}
	now := time.Now().In(loc)
	if d.After(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)) {
		return nil, errors.New("can't be in the future")
	}
	if d.Year() < 2000 {
		return nil, errors.New("is too far in the past")
	}
	return &s, nil
}

// dateOrToday gives the ledger date for an optional date.
func dateOrToday(d *string, loc *time.Location) string {
	if d != nil {
		return *d
	}
	return time.Now().In(loc).Format("2006-01-02")
}
