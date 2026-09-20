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
}

type volumeTier struct {
	MinQty      float64 `json:"min_qty"`
	MaxQty      float64 `json:"max_qty"` // 0 = unbounded ("11+")
	DiscountPct float64 `json:"discount_pct"`
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
			if t.MinQty <= 0 || t.DiscountPct <= 0 || t.DiscountPct > 100 || (t.MaxQty != 0 && t.MaxQty < t.MinQty) {
				return errInvalidPromoConfig
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
func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
