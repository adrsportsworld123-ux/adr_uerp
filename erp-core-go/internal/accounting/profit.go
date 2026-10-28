package accounting

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// NetProfit is the business's profit from from to to (YYYY-MM-DD, the
// merchant's calendar), worked out exactly as the report builder's
// "Profit and loss" dataset does (reportbuilder datasets.go,
// profit_loss): income less expenses from the posted ledger, less the
// cost of the items sold at their current cost price, plus the cost of
// items returned to stock. A loss is negative. Partners' profit shares
// (internal/partners, migrations/074) are this times each one's ratio.
func NetProfit(ctx context.Context, tx pgx.Tx, from, to string) (float64, error) {
	var p float64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE((
		    SELECT sum(jl.credit - jl.debit)
		    FROM journal_lines jl
		    JOIN journal_entries je ON je.id = jl.journal_entry_id
		    JOIN chart_of_accounts a ON a.id = jl.account_id
		    WHERE je.status = 'posted' AND a.account_type IN ('income', 'expense')
		      AND je.entry_date BETWEEN $1::date AND $2::date), 0)
		 - COALESCE((
		    SELECT sum(round(l.quantity * v.cost_price, 2))
		    FROM sales_order_lines l
		    JOIN sales_orders so ON so.id = l.sales_order_id
		    JOIN product_variants v ON v.id = l.variant_id
		    WHERE so.status IN ('finalized', 'partially_refunded', 'refunded')
		      AND (COALESCE(so.finalized_at, so.created_at) AT TIME ZONE app_merchant_tz())::date BETWEEN $1::date AND $2::date), 0)
		 + COALESCE((
		    SELECT sum(round(rl.quantity * v.cost_price, 2))
		    FROM sales_return_lines rl
		    JOIN sales_returns r ON r.id = rl.sales_return_id
		    JOIN product_variants v ON v.id = rl.variant_id
		    WHERE r.restocked AND (r.created_at AT TIME ZONE app_merchant_tz())::date BETWEEN $1::date AND $2::date), 0)`,
		from, to).Scan(&p)
	return p, err
}
