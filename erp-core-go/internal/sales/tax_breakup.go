package sales

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/taxes"
)

// loadLinesAndTax fills an order response's lines and its tax breakup
// (migrations/060): the split every client prints under the subtotal.
func loadLinesAndTax(ctx context.Context, tx pgx.Tx, resp *orderResponse) error {
	lines, err := loadOrderLines(ctx, tx, resp.OrderID)
	if err != nil {
		return err
	}
	resp.Lines = lines
	if err := tx.QueryRow(ctx, `SELECT ship_to_state FROM sales_orders WHERE id::text = $1`, resp.OrderID).Scan(&resp.ShipToState); err != nil {
		return err
	}
	resp.TaxBreakup, err = orderTaxBreakup(ctx, tx, resp.OrderID, lines)
	return err
}

// orderTaxBreakup splits an order's lines under its branch's tax regime
// and the order's own place of supply.
func orderTaxBreakup(ctx context.Context, tx pgx.Tx, orderID string, lines []orderLine) (*taxes.Breakup, error) {
	var branchID, pos string
	var inter bool
	if err := tx.QueryRow(ctx, `SELECT branch_id::text, COALESCE(place_of_supply, ''), inter_state FROM sales_orders WHERE id::text = $1`, orderID).
		Scan(&branchID, &pos, &inter); err != nil {
		return nil, err
	}
	settings, err := taxes.Load(ctx, tx, branchID)
	if err != nil {
		return nil, err
	}
	in := make([]taxes.Line, 0, len(lines))
	for _, l := range lines {
		in = append(in, taxes.Line{Rate: num(l.TaxRate), Taxable: num(l.TaxableValue), Tax: num(l.TaxAmount), Cess: num(l.CessAmount)})
	}
	b := taxes.Build(settings, inter, pos, in)
	return &b, nil
}

func num(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// receiptSummaryLine is one rate of a mixed-rate bill on the 32-column
// slip: "@5% 24.29: CGST 0.61 SGST 0.60".
func receiptSummaryLine(b *taxes.Breakup, s taxes.Bucket) string {
	head := "@" + taxes.FormatRate(num(s.Rate)) + "% " + s.Taxable + ": "
	switch {
	case b.Regime == taxes.RegimeSingle:
		return head + b.TaxLabel + " " + s.Tax
	case b.InterState:
		return head + "IGST " + s.IGST
	}
	return head + "CGST " + s.CGST + " SGST " + s.SGST
}
