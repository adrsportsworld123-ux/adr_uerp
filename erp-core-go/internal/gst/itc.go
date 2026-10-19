package gst

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// itcByHead splits the period's input-tax credit (the GST Input ledger
// balance, ledgerITC) by head, in the proportions of the purchase bills
// dated in the period (purchase_bills.cgst/sgst/igst/cess_amount,
// migrations/060). The largest head takes any rounding residue so the
// heads always add back up to the ledger figure. With no bill tax to go by, the
// credit is reported as intra-state (CGST/SGST 50/50).
func itcByHead(ctx context.Context, tx pgx.Tx, start, end time.Time, ledgerITC float64) (itcAvlEntry, error) {
	var cgst, sgst, igst, cess, total float64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(cgst_amount), 0), COALESCE(SUM(sgst_amount), 0), COALESCE(SUM(igst_amount), 0),
		       COALESCE(SUM(cess_amount), 0), COALESCE(SUM(tax_total), 0)
		FROM purchase_bills WHERE bill_date >= $1::date AND bill_date < $2::date`, start, end,
	).Scan(&cgst, &sgst, &igst, &cess, &total); err != nil {
		return itcAvlEntry{}, err
	}
	r2 := func(v float64) float64 { return math.Round(v*100) / 100 }
	if total <= 0 {
		half := r2(ledgerITC / 2)
		return itcAvlEntry{Type: "OTH", IGST: "0.00", CGST: formatMoney(half), SGST: formatMoney(r2(ledgerITC - half)), Cess: "0.00"}, nil
	}
	f := ledgerITC / total
	cgst, sgst, igst, cess = r2(cgst*f), r2(sgst*f), r2(igst*f), r2(cess*f)
	residue := r2(ledgerITC - cgst - sgst - igst - cess)
	switch {
	case igst >= cgst && igst >= sgst:
		igst = r2(igst + residue)
	case cgst >= sgst:
		cgst = r2(cgst + residue)
	default:
		sgst = r2(sgst + residue)
	}
	return itcAvlEntry{Type: "OTH", IGST: formatMoney(igst), CGST: formatMoney(cgst), SGST: formatMoney(sgst), Cess: formatMoney(cess)}, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
