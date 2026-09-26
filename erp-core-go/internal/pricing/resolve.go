package pricing

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// ResolvePrice returns the effective unit price for variantID, snapshotted
// at the moment of the call — matches sales_order_lines.unit_price and
// quotation_lines.unit_price's existing "price snapshot at time of sale"
// convention, never recomputed retroactively for a line that already
// exists. customerID may be empty (a walk-in/anonymous sale, the common
// case) — that always falls through to the plain retail price below.
//
// Resolution order, and the one and only place this logic lives (called
// from both internal/sales' AddLine and internal/quotations' line
// creation, so a wholesale customer's price is never computed two
// different ways in two different code paths):
//  1. If the customer has a price_list_id assigned AND that list has an
//     explicit price for this variant, use it — the wholesale/B2B path.
//  2. Otherwise, product_variants.selling_price — the plain retail price
//     every customer already gets today, including one with a price list
//     that simply doesn't cover this specific variant.
//
// Never a third, silent option: an item not in the list falls back to
// retail, it doesn't error and it doesn't default to zero.
func ResolvePrice(ctx context.Context, tx pgx.Tx, customerID, variantID string) (string, error) {
	var price string
	err := tx.QueryRow(ctx, `
		WITH params AS (
			SELECT NULLIF($1,'')::uuid AS customer_id, $2::uuid AS variant_id
		)
		SELECT COALESCE(
			(SELECT pli.price::text
			 FROM customers c
			 JOIN price_list_items pli ON pli.price_list_id = c.price_list_id
			 CROSS JOIN params
			 WHERE c.id = params.customer_id AND pli.variant_id = params.variant_id),
			(SELECT pv.selling_price::text FROM product_variants pv CROSS JOIN params WHERE pv.id = params.variant_id)
		)`, customerID, variantID).Scan(&price)
	return price, err
}
