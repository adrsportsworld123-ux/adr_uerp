package promotions

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/customers"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/sales"
)

type cartLine struct {
	lineID           string
	productID        string
	categoryID       string // "" if the product has no category
	qty, unitPrice   float64
	existingDiscount float64
}

type eligiblePromo struct {
	id       string
	stacking string
	amount   float64
	lineIDs  []string
}

// ---------------------------------------------------------------------
// POST /sales/orders/{id}/promotions/apply — evaluates every active
// promotion against the cart's current state and applies them per the
// FRD's stacking rule: if any eligible promotion is "exclusive", only the
// single best (largest discount) one applies and every other eligible
// promotion is skipped for this call; otherwise every eligible
// "stackable" promotion applies, each computed against what's left after
// the previous one (sales.ApplyDiscountLayer's additive behavior).
//
// Deliberately a separate, explicitly-called endpoint rather than
// something AddLine triggers automatically — matches this codebase's
// existing pattern of an explicit POST /sales/orders/{id}/discounts call
// for manual discounts rather than implicit magic, and lets a POS client
// call it once right before checkout so the customer sees a stable total.
// Calling it more than once on the same cart is safe: an already-matched
// promotion whose lines have no taxable value left simply stops being
// eligible (see evaluatePromotion's remainingBase <= 0 check), it doesn't
// re-apply.
// ---------------------------------------------------------------------

func (h *Handler) ApplyPromotions(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var resp any
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var customerID *string
		if err := tx.QueryRow(ctx, `SELECT status, customer_id FROM sales_orders WHERE id = $1`, orderID).
			Scan(&status, &customerID); err != nil {
			return err
		}
		if status != "cart" {
			return errOrderNotEditable
		}

		lines, err := loadCartLines(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if len(lines) == 0 {
			resp = map[string]any{"applied": []string{}}
			return nil
		}
		var subtotal float64
		for _, l := range lines {
			subtotal += l.qty * l.unitPrice
		}

		segment := ""
		if customerID != nil {
			segment, err = customers.FetchSegment(ctx, tx, *customerID)
			if err != nil && err != pgx.ErrNoRows {
				return err
			}
		}

		promos, err := loadEligiblePromotionDefs(ctx, tx)
		if err != nil {
			return err
		}

		var eligible []eligiblePromo
		for _, p := range promos {
			if p.targetSegment != "" && p.targetSegment != segment {
				continue
			}
			matched := matchLines(p, lines)
			if len(matched) == 0 {
				continue
			}
			amount, ok := evaluatePromotion(p, matched, subtotal)
			if !ok {
				continue
			}
			lineIDs := make([]string, len(matched))
			for i, l := range matched {
				lineIDs[i] = l.lineID
			}
			eligible = append(eligible, eligiblePromo{id: p.id, stacking: p.stacking, amount: amount, lineIDs: lineIDs})
		}

		var toApply []eligiblePromo
		hasExclusive := false
		for _, e := range eligible {
			if e.stacking == "exclusive" {
				hasExclusive = true
			}
		}
		if hasExclusive {
			best := eligible[0]
			for _, e := range eligible {
				if e.stacking == "exclusive" && e.amount > best.amount {
					best = e
				}
			}
			toApply = []eligiblePromo{best}
		} else {
			toApply = eligible
		}
		// Deterministic order (largest first) so a chain of stackable
		// promotions applied in the same call always resolves to the
		// same final totals regardless of the map/slice iteration order
		// promotions were loaded in.
		sort.Slice(toApply, func(i, j int) bool { return toApply[i].amount > toApply[j].amount })

		applied := []string{}
		var lastState any // sales.ApplyDiscountLayer's (unexported-typed) order snapshot; held via type inference only, never named explicitly
		for _, e := range toApply {
			promotionID := e.id
			state, err := sales.ApplyDiscountLayer(ctx, tx, sales.DiscountLayer{
				OrderID:     orderID,
				Type:        "promotion",
				Amount:      e.amount,
				LineIDs:     e.lineIDs,
				PromotionID: &promotionID,
				AppliedBy:   claims.UserID,
				Reason:      "auto-applied promotion",
			})
			if errors.Is(err, sales.ErrDiscountExceedsAvailable) {
				continue // another layer in this same call already consumed the room; not an error
			}
			if err != nil {
				return err
			}
			lastState = state
			applied = append(applied, e.id)
		}
		resp = map[string]any{"applied": applied, "order": lastState}
		return nil
	})

	switch {
	case errors.Is(err, errOrderNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "this order is no longer a cart")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not apply promotions")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

var errOrderNotEditable = errors.New("order is not editable")

func loadCartLines(ctx context.Context, tx pgx.Tx, orderID string) ([]cartLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT sol.id, p.id, COALESCE(p.category_id::text,''), sol.quantity, sol.unit_price, sol.discount_amount
		FROM sales_order_lines sol
		JOIN product_variants pv ON pv.id = sol.variant_id
		JOIN products p ON p.id = pv.product_id
		WHERE sol.sales_order_id = $1`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var lines []cartLine
	for rows.Next() {
		var l cartLine
		if err := rows.Scan(&l.lineID, &l.productID, &l.categoryID, &l.qty, &l.unitPrice, &l.existingDiscount); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

type promotionDef struct {
	id, promoType, applicationLevel, productID, categoryID, targetSegment, stacking string
	config                                                                          promoConfig
}

func loadEligiblePromotionDefs(ctx context.Context, tx pgx.Tx) ([]promotionDef, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, promo_type, application_level, COALESCE(product_id::text,''), COALESCE(category_id::text,''),
		       COALESCE(target_segment,''), config, stacking
		FROM promotions
		WHERE active
		  AND (starts_at IS NULL OR starts_at <= now())
		  AND (ends_at IS NULL OR ends_at >= now())
		  AND (days_of_week IS NULL OR EXTRACT(DOW FROM now())::smallint = ANY(days_of_week))
		  AND (time_start IS NULL OR time_end IS NULL OR now()::time BETWEEN time_start AND time_end)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var defs []promotionDef
	for rows.Next() {
		var d promotionDef
		var rawConfig []byte
		if err := rows.Scan(&d.id, &d.promoType, &d.applicationLevel, &d.productID, &d.categoryID,
			&d.targetSegment, &rawConfig, &d.stacking); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(rawConfig, &d.config)
		defs = append(defs, d)
	}
	return defs, rows.Err()
}

func matchLines(p promotionDef, lines []cartLine) []cartLine {
	switch p.applicationLevel {
	case "order":
		return lines
	case "product":
		var out []cartLine
		for _, l := range lines {
			if l.productID == p.productID {
				out = append(out, l)
			}
		}
		return out
	case "category":
		var out []cartLine
		for _, l := range lines {
			if l.categoryID == p.categoryID {
				out = append(out, l)
			}
		}
		return out
	}
	return nil
}

// evaluatePromotion computes the rupee discount a promotion resolves to
// against its matched lines, or ok=false if it doesn't actually apply
// (threshold not met, no room left after prior layers, etc.).
func evaluatePromotion(p promotionDef, matched []cartLine, orderSubtotal float64) (amount float64, ok bool) {
	var matchedRaw, matchedRemaining, matchedQty float64
	for _, l := range matched {
		matchedRaw += l.qty * l.unitPrice
		matchedRemaining += l.qty*l.unitPrice - l.existingDiscount
		matchedQty += l.qty
	}
	if matchedRemaining <= 0 {
		return 0, false
	}

	switch p.promoType {
	case "percent":
		amt := round2(matchedRemaining * p.config.ValuePercent / 100)
		return amt, amt > 0

	case "fixed":
		amt := p.config.ValueAmount
		if amt > matchedRemaining {
			amt = matchedRemaining
		}
		return amt, amt > 0

	case "min_value":
		if orderSubtotal < p.config.MinPurchaseAmount {
			return 0, false
		}
		amt := p.config.DiscountAmount
		if p.config.DiscountPct > 0 {
			amt = round2(orderSubtotal * p.config.DiscountPct / 100)
		}
		if amt > matchedRemaining {
			amt = matchedRemaining
		}
		return amt, amt > 0

	case "bogo":
		setSize := p.config.BuyQty + p.config.GetQty
		if setSize <= 0 {
			return 0, false
		}
		numSets := math.Floor(matchedQty / setSize)
		if numSets <= 0 {
			return 0, false
		}
		discountedQty := numSets * p.config.GetQty
		avgUnitPrice := matchedRaw / matchedQty
		amt := round2(discountedQty * avgUnitPrice * p.config.GetDiscountPct / 100)
		if amt > matchedRemaining {
			amt = matchedRemaining
		}
		return amt, amt > 0

	case "volume":
		var best *volumeTier
		for i := range p.config.Tiers {
			t := &p.config.Tiers[i]
			if matchedQty >= t.MinQty && (t.MaxQty == 0 || matchedQty <= t.MaxQty) {
				if best == nil || t.DiscountPct > best.DiscountPct {
					best = t
				}
			}
		}
		if best == nil {
			return 0, false
		}
		amt := round2(matchedRemaining * best.DiscountPct / 100)
		return amt, amt > 0
	}
	return 0, false
}
