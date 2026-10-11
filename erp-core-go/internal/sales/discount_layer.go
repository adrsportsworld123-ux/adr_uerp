package sales

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/taxcalc"
)

// DiscountLayer is one application of ApplyDiscountLayer — one "layer" in
// pos_frd_complete.md §5's discount hierarchy (promotional, coupon,
// customer-level, loyalty redemption, manual, in that order). Type
// identifies which layer this is for the sales_order_discounts audit
// trail; PromotionID/CouponID back-reference the source row when there is
// one (both nil for manual and loyalty).
type DiscountLayer struct {
	OrderID      string
	Type         string // "manual" | "coupon" | "promotion" | "loyalty"
	Amount       float64
	ValuePercent *float64 // only meaningful for manual/percent-style entries — see migrations/012's column comment
	LineIDs      []string // nil/empty = whole order; non-nil = only these lines (a product/category-scoped promotion)
	PromotionID  *string
	CouponID     *string
	AuthorizedBy *string
	AppliedBy    string
	Reason       string
}

var ErrDiscountExceedsAvailable = errors.New("discount amount exceeds the order's remaining discountable value")

// ApplyDiscountLayer is the one place any discount hierarchy layer
// (internal/promotions' promotional/coupon layers, internal/loyalty's
// redemption layer, and this package's own manual layer — see
// discounts.go) touches sales_order_lines. It distributes layer.Amount
// proportionally across the target lines' CURRENT remaining taxable value
// (lineSubtotal - that line's existing discount_amount, not its original
// price) and ADDS the resulting share to discount_amount, so calling this
// more than once on the same order stacks layers instead of one silently
// overwriting another — the property the discount hierarchy needs to mean
// anything. Recomputes tax_amount/line_total from each line's new taxable
// value (tax is owed on the discounted price, matching ApplyDiscount's
// existing GST treatment) and records an audit row in
// sales_order_discounts before returning the order's fresh totals.
//
// Must be called with orderID already confirmed to be status='cart' by the
// caller — mirrors ApplyDiscount's own precondition, not re-checked here
// since every caller already loads the order for its own purposes first.
func ApplyDiscountLayer(ctx context.Context, tx pgx.Tx, layer DiscountLayer) (orderResponse, error) {
	var resp orderResponse
	if layer.Amount <= 0 {
		return resp, errDiscountOutOfRange
	}

	query := `
		SELECT sol.id, sol.unit_price, sol.quantity, sol.discount_amount, sol.price_includes_tax,
		       COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) + COALESCE(ts.cess_rate,0)
		FROM sales_order_lines sol
		JOIN product_variants pv ON pv.id = sol.variant_id
		JOIN products p ON p.id = pv.product_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		WHERE sol.sales_order_id = $1`
	args := []any{layer.OrderID}
	if len(layer.LineIDs) > 0 {
		query += ` AND sol.id = ANY($2)`
		args = append(args, layer.LineIDs)
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return resp, err
	}
	type line struct {
		lineID                           string
		unitPrice, qty, existingDiscount float64
		taxRatePct                       float64
		priceIncludesTax                 bool
	}
	var lines []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.lineID, &l.unitPrice, &l.qty, &l.existingDiscount, &l.priceIncludesTax, &l.taxRatePct); err != nil {
			rows.Close()
			return resp, err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return resp, err
	}

	var remainingBase float64
	for _, l := range lines {
		remainingBase += l.unitPrice*l.qty - l.existingDiscount
	}
	if remainingBase <= 0 || layer.Amount > remainingBase {
		return resp, ErrDiscountExceedsAvailable
	}

	for _, l := range lines {
		lineRemaining := l.unitPrice*l.qty - l.existingDiscount
		share := round2(layer.Amount * (lineRemaining / remainingBase))
		newDiscount := l.existingDiscount + share
		// For a tax-inclusive line the discount reduces the inclusive
		// price the customer pays, and tax is re-extracted from what's
		// left; for an exclusive line tax is re-added on the discounted
		// taxable value. taxcalc handles both.
		calc := taxcalc.ComputeLine(l.unitPrice, l.qty, newDiscount, l.taxRatePct, l.priceIncludesTax)
		if _, err := tx.Exec(ctx, `
			UPDATE sales_order_lines SET discount_amount = $1, tax_amount = $2, line_total = $3 WHERE id = $4`,
			newDiscount, calc.Tax, calc.Total, l.lineID); err != nil {
			return resp, err
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO sales_order_discounts
			(id, merchant_id, sales_order_id, type, value_percent, discount_amount, promotion_id, coupon_id, authorized_by, applied_by, reason)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		layer.OrderID, layer.Type, layer.ValuePercent, layer.Amount, layer.PromotionID, layer.CouponID, layer.AuthorizedBy, layer.AppliedBy, layer.Reason); err != nil {
		return resp, err
	}

	err = recalcOrderTotals(ctx, tx, layer.OrderID, &resp)
	return resp, err
}

// clearDiscountLayers undoes every discount layer on a cart, side effects
// included: each line back to its undiscounted tax and total, the coupon's
// redemption and use count, and the loyalty points redeemed against it.
// Reports whether there was anything to clear.
//
// Changing a line's quantity or removing a line calls this first. Each
// layer was sized against the cart as it was (a percent of that subtotal, a
// BOGO over those quantities), so after the edit the cashier re-applies
// them. Before, the edited line's discount was zeroed while the order kept
// the layer's audit row, so the sale was charged in full while its history
// still listed the discount.
func clearDiscountLayers(ctx context.Context, tx pgx.Tx, orderID string) (bool, error) {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM sales_order_discounts WHERE sales_order_id = $1`, orderID).Scan(&n); err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}

	rows, err := tx.Query(ctx, `
		SELECT sol.id, sol.unit_price, sol.quantity, sol.price_includes_tax,
		       COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) + COALESCE(ts.cess_rate,0)
		FROM sales_order_lines sol
		JOIN product_variants pv ON pv.id = sol.variant_id
		JOIN products p ON p.id = pv.product_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		WHERE sol.sales_order_id = $1 AND sol.discount_amount <> 0`, orderID)
	if err != nil {
		return false, err
	}
	type line struct {
		id                   string
		unitPrice, qty, rate float64
		inclusive            bool
	}
	var lines []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.id, &l.unitPrice, &l.qty, &l.inclusive, &l.rate); err != nil {
			rows.Close()
			return false, err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	for _, l := range lines {
		calc := taxcalc.ComputeLine(l.unitPrice, l.qty, 0, l.rate, l.inclusive)
		if _, err := tx.Exec(ctx, `UPDATE sales_order_lines SET discount_amount = 0, tax_amount = $1, line_total = $2 WHERE id = $3`,
			calc.Tax, calc.Total, l.id); err != nil {
			return false, err
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE coupons c SET usage_count = GREATEST(c.usage_count - r.n, 0)
		FROM (SELECT coupon_id, count(*) AS n FROM coupon_redemptions WHERE sales_order_id = $1 GROUP BY coupon_id) r
		WHERE c.id = r.coupon_id`, orderID); err != nil {
		return false, err
	}
	for _, q := range []string{
		`DELETE FROM coupon_redemptions WHERE sales_order_id = $1`,
		`DELETE FROM loyalty_ledger WHERE sales_order_id = $1 AND entry_type = 'redeem'`,
		`DELETE FROM sales_order_discounts WHERE sales_order_id = $1`,
	} {
		if _, err := tx.Exec(ctx, q, orderID); err != nil {
			return false, err
		}
	}
	return true, nil
}
