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

// DiscountableLineSQL is the SQL test (sol = the line, so = its order) for
// a line any discount layer may touch: an open-price line only where its
// branch lets open-price items take discounts and promotions
// (migrations/062, app_open_price_discountable). Every discount path —
// this layer, the manual discount's subtotal, a coupon's minimum spend,
// a promotion's cart lines — uses it, so none can size a discount on a
// line it then can't apply to.
const DiscountableLineSQL = `(NOT sol.open_price OR app_open_price_discountable(so.branch_id))`

// ErrOpenPriceNotDiscountable: the cart has nothing to discount because
// its only lines are open-price items, which this branch keeps out of
// discounts. It is also an ErrDiscountExceedsAvailable, so a caller that
// only knows that error (a promotion pass skipping a full layer) treats it
// the same way.
var ErrOpenPriceNotDiscountable error = openPriceErr{}

type openPriceErr struct{}

func (openPriceErr) Error() string {
	return "open-price items don't take discounts or promotions at this branch"
}
func (openPriceErr) Is(target error) bool { return target == ErrDiscountExceedsAvailable }

// IsOpenPriceNotDiscountable reports whether err is ErrOpenPriceNotDiscountable.
func IsOpenPriceNotDiscountable(err error) bool {
	var e openPriceErr
	return errors.As(err, &e)
}

// onlyOpenPriceLeft reports whether a cart's discountable lines are all
// excluded open-price lines (and there is at least one of those).
func onlyOpenPriceLeft(ctx context.Context, tx pgx.Tx, orderID string) (bool, error) {
	var excluded, eligible int
	err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE NOT `+DiscountableLineSQL+`), count(*) FILTER (WHERE `+DiscountableLineSQL+`)
		FROM sales_order_lines sol JOIN sales_orders so ON so.id = sol.sales_order_id
		WHERE sol.sales_order_id = $1`, orderID).Scan(&excluded, &eligible)
	return excluded > 0 && eligible == 0, err
}

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
		JOIN sales_orders so ON so.id = sol.sales_order_id
		JOIN product_variants pv ON pv.id = sol.variant_id
		JOIN products p ON p.id = pv.product_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		WHERE sol.sales_order_id = $1 AND ` + DiscountableLineSQL
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

	if len(lines) == 0 {
		if only, err := onlyOpenPriceLeft(ctx, tx, layer.OrderID); err != nil {
			return resp, err
		} else if only {
			return resp, ErrOpenPriceNotDiscountable
		}
	}
	var remainingBase float64
	for _, l := range lines {
		remainingBase += l.unitPrice*l.qty - l.existingDiscount
	}
	if remainingBase <= 0 || layer.Amount > remainingBase {
		return resp, ErrDiscountExceedsAvailable
	}

	var allocated float64
	for i, l := range lines {
		lineRemaining := l.unitPrice*l.qty - l.existingDiscount
		share := round2(layer.Amount * (lineRemaining / remainingBase))
		// The last line takes whatever rounding the others left, so the
		// shares add up to exactly layer.Amount: a fixed ₹100 off is ₹100,
		// not ₹99.99 across three lines.
		if i == len(lines)-1 {
			share = round2(layer.Amount - allocated)
		}
		allocated += share
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

// ClearDiscountLayers is clearDiscountLayers for internal/promotions,
// which re-plans a cart's promotions from scratch when they are its only
// layers: every line back to undiscounted, every layer row removed.
func ClearDiscountLayers(ctx context.Context, tx pgx.Tx, orderID string) error {
	cleared, err := clearDiscountLayers(ctx, tx, orderID)
	if err != nil || !cleared {
		return err
	}
	// The order's cached totals follow its lines: a cart whose promotions
	// have all lapsed (edited, switched off, out of their hours) and get
	// no new layer would otherwise keep showing the old discount.
	var resp orderResponse
	return recalcOrderTotals(ctx, tx, orderID, &resp)
}

// LoadOrder returns the order's current snapshot, the shape every cart
// endpoint responds with.
func LoadOrder(ctx context.Context, tx pgx.Tx, orderID string) (orderResponse, error) {
	var resp orderResponse
	err := loadOrder(ctx, tx, orderID, &resp)
	return resp, err
}
