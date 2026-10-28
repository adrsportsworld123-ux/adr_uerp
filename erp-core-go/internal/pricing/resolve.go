package pricing

import (
	"context"
	"errors"
	"time"

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

// LinePrice is ResolveLinePriceAt's result: the unit price, and which
// price rule (if any) produced it — persisted on sales_order_lines.
type LinePrice struct {
	UnitPrice   float64
	Source      string  // "price_list" | "price_rule" | "markup_rule" | "mrp" | "base"
	PriceRuleID *string // set when a price rule priced the line
	RuleName    string
	// migrations/071: the markup rule the line's price came from or was
	// worked on (MarkupRuleID), and which price it is (Basis:
	// price_list | price_rule | markup | selling_price | mrp).
	MarkupRuleID   *string
	MarkupRuleName string
	Basis          string
	// Benefit: the branch's price benefit (standard | customer | shop).
	Benefit string
}

// ResolveLinePriceAt is ResolvePrice plus Phase 10's dynamic pricing, for
// a POS cart line (not a quotation — a 30-day quote must not snapshot a
// 3-hour happy-hour price). Precedence:
//  1. The customer's price-list price for this variant — a negotiated
//     contract price is never overridden by a time-based rule.
//  2. Otherwise the best live price rule (migrations/035) applied to the
//     base selling_price: variant rule over category rule, then higher
//     priority, then newest. Time windows are evaluated in the branch's
//     own timezone at `at`.
//  3. Otherwise the base selling_price.
//
// Markup rules and the branch's price benefit (migrations/071) come in at
// 2–3. A live markup rule gives a markup price (cost + markup).
//   - standard (the default): as above, with the markup price, when there
//     is one, standing in for the selling price.
//   - customer / shop: the lowest / highest of MRP, the selling price, the
//     markup price and the price rule's price (worked on the selling
//     price). A tie keeps the earlier of selling price, price rule,
//     markup, MRP.
//
// at is normally time.Now(); GET /pricing/variants/{id}/effective-price
// passes a caller-chosen instant so a rule can be previewed ("what will
// this cost Saturday at 3 PM?").
func ResolveLinePriceAt(ctx context.Context, tx pgx.Tx, customerID, variantID, branchID string, at time.Time) (LinePrice, error) {
	var listPrice *float64
	var base, mrp, cost, taxRate float64
	var categoryID *string
	var inclusive bool
	var benefit string
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT pli.price FROM customers c
		        JOIN price_list_items pli ON pli.price_list_id = c.price_list_id
		        WHERE c.id = NULLIF($1,'')::uuid AND pli.variant_id = pv.id),
		       pv.selling_price, pv.mrp, p.category_id::text, pv.cost_price, p.price_includes_tax,
		       COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) + COALESCE(ts.cess_rate,0),
		       app_price_benefit(NULLIF($3,'')::uuid)
		FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		WHERE pv.id = $2`, customerID, variantID, branchID).Scan(&listPrice, &base, &mrp, &categoryID, &cost, &inclusive, &taxRate, &benefit); err != nil {
		return LinePrice{}, err
	}
	if listPrice != nil {
		return LinePrice{UnitPrice: *listPrice, Source: "price_list", Basis: "price_list", Benefit: benefit}, nil
	}

	mID, mName, mType, mValue, mFound, err := liveMarkup(ctx, tx, variantID, deref(categoryID), branchID, at)
	if err != nil {
		return LinePrice{}, err
	}
	var markup *candidate
	if mFound {
		if p, ok := markupPrice(cost, mrp, taxRate, inclusive, mType, mValue); ok {
			markup = &candidate{price: p, basis: "markup"}
		}
	}

	var ruleID, ruleName, ruleType string
	var value float64
	err = tx.QueryRow(ctx, `
		WITH local AS (
			SELECT $4::timestamptz AT TIME ZONE COALESCE(
				(SELECT timezone FROM branches WHERE id = NULLIF($3,'')::uuid), app_merchant_tz()) AS ts
		)
		SELECT r.id, r.name, r.rule_type, r.value
		FROM price_rules r CROSS JOIN local
		WHERE r.active
		  AND (r.variant_id = $1::uuid OR r.category_id = NULLIF($2,'')::uuid)
		  AND (r.branch_id IS NULL OR r.branch_id = NULLIF($3,'')::uuid)
		  AND (r.starts_at IS NULL OR r.starts_at <= $4::timestamptz)
		  AND (r.ends_at IS NULL OR $4::timestamptz < r.ends_at)
		  AND (r.days_of_week IS NULL OR EXTRACT(DOW FROM local.ts)::smallint = ANY(r.days_of_week))
		  AND (r.time_start IS NULL OR CASE
		         WHEN r.time_start <= r.time_end THEN local.ts::time >= r.time_start AND local.ts::time < r.time_end
		         ELSE local.ts::time >= r.time_start OR local.ts::time < r.time_end  -- window crosses midnight
		       END)
		ORDER BY (r.variant_id IS NOT NULL) DESC, r.priority DESC, r.created_at DESC
		LIMIT 1`, variantID, deref(categoryID), branchID, at).Scan(&ruleID, &ruleName, &ruleType, &value)
	ruleFound := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return LinePrice{}, err
	}
	withMarkup := func(lp LinePrice) LinePrice {
		lp.MarkupRuleID, lp.MarkupRuleName, lp.Benefit = &mID, mName, benefit
		return lp
	}

	if benefit != "customer" && benefit != "shop" {
		// standard: the markup price stands in for the selling price.
		start := base
		if markup != nil {
			start = markup.price
		}
		switch {
		case ruleFound && markup != nil:
			return withMarkup(LinePrice{UnitPrice: applyRule(start, mrp, ruleType, value), Source: "price_rule", PriceRuleID: &ruleID, RuleName: ruleName, Basis: "price_rule"}), nil
		case ruleFound:
			return LinePrice{UnitPrice: applyRule(start, mrp, ruleType, value), Source: "price_rule", PriceRuleID: &ruleID, RuleName: ruleName, Basis: "price_rule", Benefit: benefit}, nil
		case markup != nil:
			return withMarkup(LinePrice{UnitPrice: markup.price, Source: "markup_rule", Basis: "markup"}), nil
		}
		return LinePrice{UnitPrice: base, Source: "base", Basis: "selling_price", Benefit: benefit}, nil
	}

	// customer / shop: compare the prices this line could be sold at.
	cands := []candidate{{price: base, basis: "selling_price"}}
	if ruleFound {
		cands = append(cands, candidate{price: applyRule(base, mrp, ruleType, value), basis: "price_rule"})
	}
	if markup != nil {
		cands = append(cands, *markup)
	}
	if mrp > 0 {
		cands = append(cands, candidate{price: mrp, basis: "mrp"})
	}
	best := pickPrice(cands, benefit == "shop")
	lp := LinePrice{UnitPrice: best.price, Basis: best.basis, Benefit: benefit}
	switch best.basis {
	case "price_rule":
		lp.Source, lp.PriceRuleID, lp.RuleName = "price_rule", &ruleID, ruleName
	case "markup":
		lp = withMarkup(lp)
		lp.Source = "markup_rule"
	case "mrp":
		lp.Source = "mrp"
	default:
		lp.Source = "base"
	}
	return lp, nil
}

type candidate struct {
	price float64
	basis string
}

// pickPrice: the lowest (customer benefit) or highest (shop benefit)
// price; on a tie the earlier candidate.
func pickPrice(cands []candidate, highest bool) candidate {
	best := cands[0]
	for _, c := range cands[1:] {
		if (highest && c.price > best.price+0.0001) || (!highest && c.price < best.price-0.0001) {
			best = c
		}
	}
	return best
}

// applyRule computes a rule's price from the base selling price: a fixed
// price, or a signed percentage of the base. Never below 0, rounded to
// the paisa, and never above MRP when one is set — selling above the
// printed MRP is illegal in India, so a surge rule tops out there.
func applyRule(base, mrp float64, ruleType string, value float64) float64 {
	price := value
	if ruleType == "percent" {
		price = base * (1 + value/100)
	}
	if price < 0 {
		price = 0
	}
	price = round2(price)
	if mrp > 0 && price > mrp {
		price = mrp
	}
	return price
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
