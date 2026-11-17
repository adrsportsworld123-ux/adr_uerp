package catalog

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// One original barcode, several things on the shelf (migrations/083,
// §3.86). A scanned code can be:
//   - shared by variants of one product (every colour carries the same
//     printed code): the cashier always picks, the system can't see colour;
//   - a variant with MRP lots (old stock at the old MRP, new at the new):
//     the one lot in stock is taken, two or more in stock ask "Which MRP
//     is on the pack?".
// scanOptions works out the choices; the barcode endpoint answers with the
// one to use and, when a person has to decide, every option.

// ScanOption is one variant a scanned code may mean.
type ScanOption struct {
	VariantID string `json:"variant_id"`
	SKU       string `json:"sku"`
	// Label: the variant's attribute values ("Red · L"); "" for a
	// product with one variant.
	Label        string `json:"label"`
	MRP          string `json:"mrp"`
	SellingPrice string `json:"selling_price"`
	// MRPLotOf: the variant this is an MRP lot of; nil for the variant itself.
	MRPLotOf *string `json:"mrp_lot_of"`
	// Available: what the asked branch can sell now; nil when no branch
	// was asked.
	Available *string `json:"available"`

	base      string
	available float64
}

// scanChoice is what a scan resolves to.
type scanChoice struct {
	Options []ScanOption
	// Chosen: the option to use when ChoiceRequired is false (the only
	// one, or the only MRP lot in stock); else the first, for an older
	// client that can't ask.
	Chosen         ScanOption
	ChoiceRequired bool
}

// ComboLabel: an attribute_combo's values in key order, "Red · L".
func ComboLabel(raw []byte) string {
	var combo map[string]any
	if json.Unmarshal(raw, &combo) != nil || len(combo) == 0 {
		return ""
	}
	keys := make([]string, 0, len(combo))
	for k := range combo {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make([]string, 0, len(keys))
	for _, k := range keys {
		if v := strings.TrimSpace(strings.Trim(strings.TrimSpace(toString(combo[k])), `"`)); v != "" {
			vals = append(vals, v)
		}
	}
	return strings.Join(vals, " · ")
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// scanOptions lists the sellable variants code can mean, in shelf order:
// the code's variants (primary first), each followed by its MRP lots
// oldest first. receive = a goods receipt: only the variants themselves,
// since a receipt names its MRP on the line (migrations/083), never a lot.
// branchID "" leaves Available nil. firstVariant is the variant the
// code's first row names (the response's own fields).
func scanOptions(ctx context.Context, tx pgx.Tx, code, branchID string, receive bool) (scanChoice, error) {
	var ch scanChoice
	rows, err := tx.Query(ctx, `
		WITH coded AS (
			SELECT COALESCE(v.mrp_parent_id, v.id) AS base, min(b.created_at) AS first_at, bool_or(b.is_primary) AS prim
			FROM barcodes b JOIN product_variants v ON v.id = b.variant_id
			WHERE b.code = $1
			GROUP BY 1
		)
		SELECT pv.id::text, pv.sku, pv.attribute_combo::text, pv.mrp::text, pv.selling_price::text, pv.mrp_parent_id::text,
		       c.base::text, sl.on_hand - sl.reserved
		FROM coded c
		JOIN product_variants pv ON pv.id = c.base OR (pv.mrp_parent_id = c.base AND NOT $3)
		JOIN products p ON p.id = pv.product_id
		LEFT JOIN stock_levels sl ON sl.variant_id = pv.id AND sl.branch_id::text = NULLIF($2, '')
		WHERE `+SellablePredicate+`
		ORDER BY c.prim DESC, c.first_at, c.base, pv.mrp_parent_id IS NOT NULL, pv.created_at, pv.id`, code, branchID, receive)
	if err != nil {
		return ch, err
	}
	defer rows.Close()
	for rows.Next() {
		var o ScanOption
		var combo string
		var avail *float64
		if err := rows.Scan(&o.VariantID, &o.SKU, &combo, &o.MRP, &o.SellingPrice, &o.MRPLotOf, &o.base, &avail); err != nil {
			return ch, err
		}
		o.Label = ComboLabel([]byte(combo))
		if branchID != "" {
			a := 0.0
			if avail != nil {
				a = *avail
			}
			o.available = a
			s := strconv.FormatFloat(a, 'f', 3, 64)
			o.Available = &s
		}
		ch.Options = append(ch.Options, o)
	}
	if err := rows.Err(); err != nil || len(ch.Options) == 0 {
		return ch, err
	}
	ch.Chosen = ch.Options[0]
	bases := map[string]bool{}
	for _, o := range ch.Options {
		bases[o.base] = true
	}
	switch {
	case len(ch.Options) == 1:
	case len(bases) > 1:
		// Different colours/sizes on one code: only the cashier knows.
		ch.ChoiceRequired = true
	case branchID == "":
		ch.ChoiceRequired = true
	default:
		// One variant in several MRP lots: the lots in stock decide.
		var inStock []ScanOption
		for _, o := range ch.Options {
			if o.available > 0 {
				inStock = append(inStock, o)
			}
		}
		switch len(inStock) {
		case 0: // nothing in stock: the oldest, and the sale says so
		case 1:
			ch.Chosen = inStock[0]
		default:
			ch.Options, ch.Chosen, ch.ChoiceRequired = inStock, inStock[0], true
		}
	}
	return ch, nil
}
