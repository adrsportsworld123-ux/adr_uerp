package sales

import (
	"context"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
)

// Round-off waiver (migrations/059): a sale settled for slightly less
// than its total, ₹105.75 paid as ₹105. The invoice keeps its total
// (GST unchanged); sales_orders.round_off holds the signed adjustment
// (-0.75), the tenders cover grand_total + round_off, and the waiver is
// debited to Round Off (5005). The merchant switches it on and caps it
// with sales_settings.round_off_max, never above MaxRoundOff.

// MaxRoundOff is the most any merchant may let a cashier waive on one
// sale (the database CHECKs carry the same figure).
const MaxRoundOff = 5.0

var (
	errRoundOffDisabled = errors.New("round-off is not enabled")
	errRoundOffTooLarge = errors.New("round-off exceeds the allowed waiver")
)

// checkRoundOff validates a requested round-off (0 or negative: the
// amount waived, signed) against the merchant's setting and the bill, and
// returns it rounded to the paisa.
func checkRoundOff(settings salesSettings, grandTotal, roundOff float64) (float64, error) {
	roundOff = round2(roundOff)
	if roundOff == 0 {
		return 0, nil
	}
	if roundOff > 0 {
		return 0, errRoundOffTooLarge // only a waiver, never a surcharge
	}
	if settings.roundOffMax <= 0 {
		return 0, errRoundOffDisabled
	}
	waived := -roundOff
	if waived > settings.roundOffMax+0.004 || waived > MaxRoundOff+0.004 || waived >= grandTotal {
		return 0, errRoundOffTooLarge
	}
	return roundOff, nil
}

// roundOffJournalLine is the sale entry's debit for the waiver (none when
// nothing was waived). roundOff is the order's signed round_off.
func roundOffJournalLine(roundOff float64) []accounting.JournalLine {
	if roundOff >= 0 {
		return nil
	}
	return []accounting.JournalLine{{AccountCode: accounting.AccountRoundOff, Debit: round2(-roundOff)}}
}

// returnRoundOff is how much of the sale's waiver a return gives back, so
// the refunds never add up to more than was paid: after every return,
// the waiver given back so far is max(0, waiver - value still unreturned).
// The value of a return is its goods (taxable + tax) before this
// adjustment; the refund is that value less what this returns.
func returnRoundOff(ctx context.Context, tx pgx.Tx, orderID string, thisValue float64) (float64, error) {
	var grand, roundOff, returnedValue, givenBack float64
	if err := tx.QueryRow(ctx, `
		SELECT so.grand_total::float8, so.round_off::float8,
		       COALESCE((SELECT sum(r.subtotal + r.tax_amount) FROM sales_returns r WHERE r.sales_order_id = so.id), 0)::float8,
		       COALESCE((SELECT sum(r.round_off) FROM sales_returns r WHERE r.sales_order_id = so.id), 0)::float8
		FROM sales_orders so WHERE so.id::text = $1`, orderID).Scan(&grand, &roundOff, &returnedValue, &givenBack); err != nil {
		return 0, err
	}
	waiver := -roundOff
	if waiver <= 0 {
		return 0, nil
	}
	remainingAfter := round2(grand - returnedValue - thisValue)
	due := math.Max(0, waiver-math.Max(0, remainingAfter)) - givenBack
	return round2(math.Min(math.Max(0, due), thisValue)), nil
}
