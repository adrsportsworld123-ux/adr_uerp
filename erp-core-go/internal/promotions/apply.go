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
	"erp-core-go/internal/db"
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
// promotion against the cart and applies the best price per line: each
// line gets whichever promotion takes the most off it (planPromotions),
// so a Cricket Pad covered by both "Cricket 30%" and "Cricket Pad 40%"
// gets 40% while a bat in the same cart still gets 30%. An "exclusive"
// promotion never combines with another on the same line; "stackable"
// ones combine with each other, and that combined discount competes with
// the best exclusive one, line by line.
//
// It runs after every cart change (Reapply, wired as sales.Handler.
// OnCartChange) and again when the POS payment sheet opens or its "Apply
// promotions" button is tapped, so it is idempotent — a re-evaluation, not
// another layer:
//   - promotions already the cart's only discounts: re-planned from the
//     undiscounted cart; nothing is written when the plan is unchanged,
//     otherwise they are cleared and the new plan applied (a customer
//     attached since, or a promotion that started/ended);
//   - a coupon, manual or loyalty discount on top of applied promotions:
//     left exactly as it is (each was sized against the cart as it stood;
//     a line edit clears them all and the next call starts over);
//   - no promotion applied yet: planned against what's left after any
//     other discount, as before.
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
		res, err := reapply(ctx, tx, orderID, claims.UserID)
		if err != nil {
			return err
		}
		out := map[string]any{"applied": res.applied, "order": res.order}
		if res.unchanged {
			out["unchanged"] = true
		}
		resp = out
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

// Reapply re-plans the cart's promotions inside the caller's transaction:
// the hook internal/sales runs after every cart change (a line added,
// changed or removed, a customer attached — sales.Handler.OnCartChange),
// so a promotion shows on the cart the moment an item goes in rather
// than only at payment. Same rules as ApplyPromotions; an order that is
// no longer a cart is left alone.
func Reapply(ctx context.Context, tx pgx.Tx, orderID, userID string) error {
	_, err := reapply(ctx, tx, orderID, userID)
	if errors.Is(err, errOrderNotEditable) {
		return nil
	}
	return err
}

type reapplyResult struct {
	applied   []string
	unchanged bool
	order     any // sales' (unexported-typed) order snapshot; nil for an empty cart
}

func reapply(ctx context.Context, tx pgx.Tx, orderID, userID string) (reapplyResult, error) {
	var status string
	var customerID *string
	if err := tx.QueryRow(ctx, `SELECT status, customer_id FROM sales_orders WHERE id = $1`, orderID).
		Scan(&status, &customerID); err != nil {
		return reapplyResult{}, err
	}
	if status != "cart" {
		return reapplyResult{}, errOrderNotEditable
	}

	lines, err := loadCartLines(ctx, tx, orderID)
	if err != nil {
		return reapplyResult{}, err
	}
	if len(lines) == 0 {
		return reapplyResult{applied: []string{}}, nil
	}

	existing, otherLayers, err := loadPromotionLayers(ctx, tx, orderID)
	if err != nil {
		return reapplyResult{}, err
	}
	unchanged := func() (reapplyResult, error) {
		order, err := sales.LoadOrder(ctx, tx, orderID)
		return reapplyResult{applied: promotionIDs(existing), unchanged: true, order: order}, err
	}
	if len(existing) > 0 && otherLayers {
		return unchanged()
	}
	if len(existing) > 0 {
		// Only promotions on the cart: plan from the undiscounted lines.
		for i := range lines {
			lines[i].existingDiscount = 0
		}
	}

	var subtotal float64
	for _, l := range lines {
		subtotal += l.qty * l.unitPrice
	}
	segment := ""
	if customerID != nil {
		segment, err = customers.FetchSegment(ctx, tx, *customerID)
		if err != nil && err != pgx.ErrNoRows {
			return reapplyResult{}, err
		}
	}
	defs, err := loadEligiblePromotionDefs(ctx, tx)
	if err != nil {
		return reapplyResult{}, err
	}
	var promos []promotionDef
	for _, p := range defs {
		if p.targetSegment == "" || p.targetSegment == segment {
			promos = append(promos, p)
		}
	}
	plan := planPromotions(promos, lines, subtotal)

	if samePlan(existing, plan) {
		return unchanged()
	}
	if len(existing) > 0 {
		if err := sales.ClearDiscountLayers(ctx, tx, orderID); err != nil {
			return reapplyResult{}, err
		}
	}

	res := reapplyResult{applied: []string{}}
	for _, e := range plan {
		promotionID := e.id
		state, err := sales.ApplyDiscountLayer(ctx, tx, sales.DiscountLayer{
			OrderID:     orderID,
			Type:        "promotion",
			Amount:      e.amount,
			LineIDs:     e.lineIDs,
			PromotionID: &promotionID,
			AppliedBy:   userID,
			Reason:      "auto-applied promotion",
		})
		if errors.Is(err, sales.ErrDiscountExceedsAvailable) {
			continue // stackable layers on the same lines already consumed the room; not an error
		}
		if err != nil {
			return reapplyResult{}, err
		}
		res.order = state
		res.applied = append(res.applied, e.id)
	}
	if res.order == nil && len(existing) > 0 {
		// Every promotion that was on the cart has lapsed: the cleared
		// cart is the answer.
		if res.order, err = sales.LoadOrder(ctx, tx, orderID); err != nil {
			return reapplyResult{}, err
		}
	}
	return res, nil
}

type appliedLayer struct {
	promotionID string
	amount      float64
}

// loadPromotionLayers returns the promotion layers already on the order,
// and whether any other kind of discount layer is there too.
func loadPromotionLayers(ctx context.Context, tx pgx.Tx, orderID string) ([]appliedLayer, bool, error) {
	rows, err := tx.Query(ctx, `
		SELECT type, COALESCE(promotion_id::text, ''), discount_amount FROM sales_order_discounts
		WHERE sales_order_id = $1`, orderID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var layers []appliedLayer
	other := false
	for rows.Next() {
		var typ string
		var l appliedLayer
		if err := rows.Scan(&typ, &l.promotionID, &l.amount); err != nil {
			return nil, false, err
		}
		if typ == "promotion" {
			layers = append(layers, l)
		} else {
			other = true
		}
	}
	return layers, other, rows.Err()
}

func promotionIDs(layers []appliedLayer) []string {
	ids := make([]string, len(layers))
	for i, l := range layers {
		ids[i] = l.promotionID
	}
	return ids
}

// samePlan: the plan would apply exactly the promotions already on the
// cart, for the same amounts (an empty plan on a cart with none counts).
func samePlan(existing []appliedLayer, plan []eligiblePromo) bool {
	if len(existing) != len(plan) {
		return false
	}
	have := map[string]float64{}
	for _, l := range existing {
		have[l.promotionID] += l.amount
	}
	for _, e := range plan {
		amt, ok := have[e.id]
		if !ok || math.Abs(amt-e.amount) > 0.005 {
			return false
		}
	}
	return true
}

// planPromotions decides which promotion applies to which line, for the
// best price per line:
//
//  1. Every promotion is evaluated on all the lines it matches, and each
//     line's share of its discount worked out (pro rata to the line's
//     value, the way ApplyDiscountLayer spreads it).
//  2. Each line goes to the exclusive promotion with the largest share for
//     it, or to the stackable promotions together when their combined
//     share is larger. A tie goes to the more specific promotion (product,
//     then the narrower category, then order-wide).
//  3. Each promotion is re-evaluated on just the lines it won. One that no
//     longer qualifies there (a volume tier or BOGO set now short) is
//     dropped and the plan is made again without it, so its lines fall to
//     their next-best promotion.
//
// Largest discount first in the result, the order they're applied in.
func planPromotions(promos []promotionDef, lines []cartLine, subtotal float64) []eligiblePromo {
	dropped := map[string]bool{}
	for {
		type candidate struct {
			p     promotionDef
			share map[string]float64
		}
		var cands []candidate
		for _, p := range promos {
			if dropped[p.id] {
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
			cands = append(cands, candidate{p: p, share: lineShares(matched, amount)})
		}

		won := map[string][]string{}
		for _, l := range lines {
			var best *candidate
			var bestShare, stackShare float64
			var stack []string
			for i := range cands {
				c := &cands[i]
				s, ok := c.share[l.lineID]
				if !ok {
					continue
				}
				if c.p.stacking == "stackable" {
					stack = append(stack, c.p.id)
					stackShare += s
					continue
				}
				if best == nil || s > bestShare+0.005 || (math.Abs(s-bestShare) <= 0.005 && moreSpecific(c.p, best.p)) {
					best, bestShare = c, s
				}
			}
			switch {
			case len(stack) > 0 && (best == nil || stackShare > bestShare+0.005):
				for _, id := range stack {
					won[id] = append(won[id], l.lineID)
				}
			case best != nil:
				won[best.p.id] = append(won[best.p.id], l.lineID)
			}
		}

		var plan []eligiblePromo
		redo := false
		for _, c := range cands {
			ids := won[c.p.id]
			if len(ids) == 0 {
				continue
			}
			mine := map[string]bool{}
			for _, id := range ids {
				mine[id] = true
			}
			var sub []cartLine
			for _, l := range lines {
				if mine[l.lineID] {
					sub = append(sub, l)
				}
			}
			amount, ok := evaluatePromotion(c.p, sub, subtotal)
			if !ok {
				dropped[c.p.id] = true
				redo = true
				continue
			}
			plan = append(plan, eligiblePromo{id: c.p.id, stacking: c.p.stacking, amount: amount, lineIDs: ids})
		}
		if redo {
			continue
		}
		sort.SliceStable(plan, func(i, j int) bool {
			if plan[i].amount != plan[j].amount {
				return plan[i].amount > plan[j].amount
			}
			return plan[i].id < plan[j].id
		})
		return plan
	}
}

// lineShares spreads a promotion's amount over its matched lines pro rata
// to each line's remaining value.
func lineShares(matched []cartLine, amount float64) map[string]float64 {
	var total float64
	for _, l := range matched {
		total += l.qty*l.unitPrice - l.existingDiscount
	}
	shares := make(map[string]float64, len(matched))
	for _, l := range matched {
		if total > 0 {
			shares[l.lineID] = amount * (l.qty*l.unitPrice - l.existingDiscount) / total
		}
	}
	return shares
}

// moreSpecific: a product promotion beats a category one, a narrower
// category (fewer categories in its subtree) a wider one, and either
// beats an order-wide one.
func moreSpecific(a, b promotionDef) bool {
	rank := func(p promotionDef) int {
		switch p.applicationLevel {
		case "product":
			return 3
		case "category":
			return 2
		}
		return 1
	}
	if rank(a) != rank(b) {
		return rank(a) > rank(b)
	}
	if a.applicationLevel == "category" && len(a.categoryTree) != len(b.categoryTree) {
		return len(a.categoryTree) < len(b.categoryTree)
	}
	return a.id < b.id
}

var errOrderNotEditable = errors.New("order is not editable")

func loadCartLines(ctx context.Context, tx pgx.Tx, orderID string) ([]cartLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT sol.id, p.id, COALESCE(p.category_id::text,''), sol.quantity, sol.unit_price, sol.discount_amount
		FROM sales_order_lines sol
		JOIN sales_orders so ON so.id = sol.sales_order_id
		JOIN product_variants pv ON pv.id = sol.variant_id
		JOIN products p ON p.id = pv.product_id
		WHERE sol.sales_order_id = $1 AND `+sales.DiscountableLineSQL, orderID) // open-price lines a branch excludes never count
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
	// categoryTree is categoryID plus every category beneath it, filled
	// for category-level promotions: products are filed under a leaf
	// (Cricket > Cricket Bats), so a promotion on "Cricket" must reach
	// them — the same "this category and its subcategories" meaning the
	// product search and pricing filters use (db.CategorySubtree).
	categoryTree map[string]bool
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range defs {
		if defs[i].applicationLevel != "category" || defs[i].categoryID == "" {
			continue
		}
		ids, err := db.CategorySubtree(ctx, tx, defs[i].categoryID)
		if err != nil {
			return nil, err
		}
		defs[i].categoryTree = make(map[string]bool, len(ids))
		for _, id := range ids {
			defs[i].categoryTree[id] = true
		}
	}
	return defs, nil
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
			if l.categoryID != "" && (l.categoryID == p.categoryID || p.categoryTree[l.categoryID]) {
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
