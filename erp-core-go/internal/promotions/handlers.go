// Package promotions implements Phase 3's Promotions & Loyalty sub-area's
// promotional-discount and coupon halves (phased_roadmap.md; pos_frd_complete.md
// §5) — hierarchy items 2 ("Promotional discounts: BOGO, bundles, volume"),
// 3 ("Coupon codes"), and 4 ("Customer-level discounts"), folded into
// promotions via target_segment. See migrations/012_promotions_loyalty.sql's
// header comment for what's deliberately out of scope (bundles — already a
// Phase 1 composite-product concept — and coupon payment-method
// restriction). Loyalty (hierarchy item 5) is a separate package,
// internal/loyalty, since it has its own config/ledger and no promotions
// dependency in either direction.
//
// Every discount this package produces goes through sales.ApplyDiscountLayer
// (internal/sales/discount_layer.go) — the one place sales_order_lines'
// discount_amount is ever touched, so a promotion, a coupon, and a manual
// discount can stack on the same order without one overwriting another.
package promotions

import (
	"encoding/json"
	"errors"
	"math"

	"erp-core-go/internal/db"
)

type Handler struct {
	DB *db.DB
}

// promoConfig unifies every promo_type's parameters into one struct rather
// than five separate ones — a solo-builder simplification matching this
// codebase's general preference (see tech_stack_decision.md §3.1's sqlc
// note) for one flexible shape over premature type-specific plumbing.
// Which fields matter depends on PromoType; validateConfig below is the
// one place that maps type -> required fields.
type promoConfig struct {
	ValuePercent      float64      `json:"value_percent,omitempty"`
	ValueAmount       float64      `json:"value_amount,omitempty"`
	BuyQty            float64      `json:"buy_qty,omitempty"`
	GetQty            float64      `json:"get_qty,omitempty"`
	GetDiscountPct    float64      `json:"get_discount_pct,omitempty"`
	Tiers             []volumeTier `json:"tiers,omitempty"`
	MinPurchaseAmount float64      `json:"min_purchase_amount,omitempty"`
	DiscountAmount    float64      `json:"discount_amount,omitempty"`
	DiscountPct       float64      `json:"discount_pct,omitempty"`
	// buy_x_get_y (migrations/070): buy BuyQty of any BuyProductIDs, get
	// GetQty of the GetProductIDs at GetDiscountPct off (100 = free), the
	// cheapest first; MaxSets caps how often (0 = every complete set).
	BuyProductIDs []string `json:"buy_product_ids,omitempty"`
	GetProductIDs []string `json:"get_product_ids,omitempty"`
	MaxSets       float64  `json:"max_sets,omitempty"`
}

type volumeTier struct {
	MinQty      float64 `json:"min_qty"`
	MaxQty      float64 `json:"max_qty"` // 0 = unbounded ("11+")
	DiscountPct float64 `json:"discount_pct"`
	// DiscountType (migrations/070): "percent" (or empty, every tier made
	// before it) takes DiscountPct; "amount_per_unit" is DiscountAmount off
	// each piece, "amount" DiscountAmount off the lot.
	DiscountType   string  `json:"discount_type,omitempty"`
	DiscountAmount float64 `json:"discount_amount,omitempty"`
}

// tierAmount: what one volume tier takes off qty pieces worth remaining.
func (t volumeTier) amount(qty, remaining float64) float64 {
	var amt float64
	switch t.DiscountType {
	case "amount_per_unit":
		amt = round2(t.DiscountAmount * qty)
	case "amount":
		amt = t.DiscountAmount
	default:
		amt = round2(remaining * t.DiscountPct / 100)
	}
	if amt > remaining {
		amt = remaining
	}
	return amt
}

var errInvalidPromoConfig = errors.New("config does not match promo_type's required fields")

// validateConfig checks the type-specific invariants CHECK constraints
// can't express (see migrations/012's config column comment for the exact
// shape each promo_type expects). application_level is validated
// alongside it since bogo/volume are inherently product/category-scoped —
// "buy 2 get 1" needs to know which product.
func validateConfig(promoType, applicationLevel string, cfg promoConfig) error {
	switch promoType {
	case "percent":
		if cfg.ValuePercent <= 0 || cfg.ValuePercent > 100 {
			return errInvalidPromoConfig
		}
	case "fixed":
		if cfg.ValueAmount <= 0 {
			return errInvalidPromoConfig
		}
	case "bogo":
		if applicationLevel == "order" {
			return errInvalidPromoConfig
		}
		if cfg.BuyQty < 1 || cfg.GetQty < 1 || cfg.GetDiscountPct <= 0 || cfg.GetDiscountPct > 100 {
			return errInvalidPromoConfig
		}
	case "volume":
		if applicationLevel == "order" {
			return errInvalidPromoConfig
		}
		if len(cfg.Tiers) == 0 {
			return errInvalidPromoConfig
		}
		for _, t := range cfg.Tiers {
			if t.MinQty <= 0 || (t.MaxQty != 0 && t.MaxQty < t.MinQty) {
				return errInvalidPromoConfig
			}
			switch t.DiscountType {
			case "", "percent":
				if t.DiscountPct <= 0 || t.DiscountPct > 100 {
					return errInvalidPromoConfig
				}
			case "amount_per_unit", "amount":
				if t.DiscountAmount <= 0 {
					return errInvalidPromoConfig
				}
			default:
				return errInvalidPromoConfig
			}
		}
	case "buy_x_get_y":
		if len(cfg.BuyProductIDs) == 0 || len(cfg.GetProductIDs) == 0 || cfg.BuyQty < 1 || cfg.GetQty < 1 ||
			cfg.GetDiscountPct <= 0 || cfg.GetDiscountPct > 100 || cfg.MaxSets < 0 {
			return errInvalidPromoConfig
		}
		buy := map[string]bool{}
		for _, id := range cfg.BuyProductIDs {
			buy[id] = true
		}
		for _, id := range cfg.GetProductIDs {
			if buy[id] {
				return errInvalidPromoConfig // the same product on both sides is a bogo
			}
		}
	case "min_value":
		if cfg.MinPurchaseAmount <= 0 {
			return errInvalidPromoConfig
		}
		if (cfg.DiscountAmount <= 0) == (cfg.DiscountPct <= 0) {
			return errInvalidPromoConfig // exactly one of the two must be set
		}
		if cfg.DiscountPct < 0 || cfg.DiscountPct > 100 {
			return errInvalidPromoConfig
		}
	default:
		return errInvalidPromoConfig
	}
	return nil
}

func marshalConfig(cfg promoConfig) []byte {
	b, _ := json.Marshal(cfg)
	return b
}

// round2 matches internal/sales' unexported helper of the same name
// (rounding rupee amounts to paise) — small enough, and used in different
// enough contexts (evaluating candidate promotions vs. distributing an
// already-resolved amount across lines), that importing sales just for
// this one function isn't worth it.
// round2 rounds half away from zero. (int64(v*100+0.5) truncated
// negatives toward zero: -10.00 became -9.99.)
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
