package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Stock is one variant's quantities, summed over the branches the caller
// may see (stock_levels is branch-scoped, migrations/044) or for one branch.
// Available is what can still be sold: on hand less what open carts hold.
type Stock struct {
	OnHand    string `json:"on_hand"`
	Reserved  string `json:"reserved"`
	Available string `json:"available"`
}

// StockByVariant reads live quantities for a page of variants — the
// product search and the pricing list show them beside each item. Live
// from Postgres on purpose: a quantity copied into the search index would
// be stale after the next sale. branchID "" = every visible branch. A
// variant with no stock row at all answers zeros.
func StockByVariant(ctx context.Context, tx pgx.Tx, variantIDs []string, branchID string) (map[string]Stock, error) {
	out := make(map[string]Stock, len(variantIDs))
	if len(variantIDs) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT v.id, COALESCE(sum(s.on_hand), 0)::text, COALESCE(sum(s.reserved), 0)::text,
		       COALESCE(sum(s.on_hand - s.reserved), 0)::text
		FROM unnest($1::text[]) AS v(id)
		LEFT JOIN stock_levels s ON s.variant_id::text = v.id AND ($2 = '' OR s.branch_id::text = $2)
		GROUP BY v.id`, variantIDs, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var st Stock
		if err := rows.Scan(&id, &st.OnHand, &st.Reserved, &st.Available); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}
