package sales

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/inventory"
)

var (
	errNotYourHold = errors.New("someone else's held order")
	errNotUnsold   = errors.New("order is not an open cart or held order")
	errCartInUse   = errors.New("cart is still being rung up")
)

// cartIdleAfter is how long another cashier's open cart must sit untouched
// before someone else may release it: the stock reservation a line add
// makes runs this long (reservation.go), so an active one means the cart
// was worked on within it.
const cartIdleAfter = "15 minutes"

// VoidHeld: POST /sales/orders/{id}/void-held {reason} — drops a parked
// cart for good (migrations/055). Nothing was sold, so nothing is reversed:
// the order's reservations and batch allocations are released (the stock
// is sellable again at once, without waiting for the hold to expire), any
// coupon use or loyalty points it claimed are given back, and the order
// becomes held_voided with who, when and why — its lines and totals stay
// as they were, as the record. Distinct from Void, which reverses a
// finalized sale (stock back, ledger reversal) and counts as a void in
// reports; a dropped hold doesn't.
//
// The cashier who held the order may void it (they could have emptied
// the cart line by line anyway); anyone else's needs sales.void. Either
// way a reason is required and the void is written to audit_logs.
func (h *Handler) VoidHeld(w http.ResponseWriter, r *http.Request) {
	h.dropUnsold(w, r, false)
}

// Release: POST /sales/orders/{id}/release {reason} — VoidHeld for any
// unpaid order, an open cart as well as a held one: a back-office sale
// left unfinished, or a till's cart abandoned mid-sale, otherwise sits
// holding its stock and shows nowhere. Same steps, same held_voided
// result ("Released" on screen), same permission rule. One more guard for
// a cart: someone else's cart that is still being worked on (a stock
// reservation younger than cartIdleAfter, or an empty cart opened within
// it) is refused with 409 CART_IN_USE, so a sale in progress at a till
// can't be pulled out from under its cashier.
func (h *Handler) Release(w http.ResponseWriter, r *http.Request) {
	h.dropUnsold(w, r, true)
}

func (h *Handler) dropUnsold(w http.ResponseWriter, r *http.Request, allowCart bool) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	var req voidRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "reason is required")
		return
	}

	var resp orderResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID, status, cashierID, orderNumber, grandTotal string
		if err := tx.QueryRow(ctx, `
			SELECT branch_id::text, status, COALESCE(cashier_id::text, ''), order_number, grand_total::text
			FROM sales_orders WHERE id::text = $1 FOR UPDATE`, orderID,
		).Scan(&branchID, &status, &cashierID, &orderNumber, &grandTotal); err != nil {
			return err
		}
		switch {
		case status == "held":
		case status == "cart" && allowCart:
		case allowCart:
			return errNotUnsold
		default:
			return errOrderNotHeld
		}
		if status == "cart" && cashierID != claims.UserID {
			var inUse bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM stock_reservations WHERE sales_order_id::text = $1 AND status = 'active' AND expires_at > now())
				    OR (NOT EXISTS (SELECT 1 FROM sales_order_lines WHERE sales_order_id::text = $1)
				        AND (SELECT created_at FROM sales_orders WHERE id::text = $1) > now() - $2::interval)`,
				orderID, cartIdleAfter).Scan(&inUse); err != nil {
				return err
			}
			if inUse {
				return errCartInUse
			}
		}
		if cashierID != claims.UserID {
			codes, err := authn.FetchPermissions(ctx, tx, claims.UserID)
			if err != nil {
				return err
			}
			if !authn.HasPermission(codes, "sales.void") {
				return errNotYourHold
			}
		}

		// Stock: every reservation still active goes back to sellable.
		rows, err := tx.Query(ctx, `
			SELECT id::text, variant_id::text, quantity FROM stock_reservations
			WHERE sales_order_id::text = $1 AND status = 'active'`, orderID)
		if err != nil {
			return err
		}
		type reservation struct {
			id, variantID string
			quantity      float64
		}
		var reservations []reservation
		for rows.Next() {
			var res reservation
			if err := rows.Scan(&res.id, &res.variantID, &res.quantity); err != nil {
				rows.Close()
				return err
			}
			reservations = append(reservations, res)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, res := range reservations {
			if err := releaseReservation(ctx, tx, branchID, res.variantID, res.quantity); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE stock_reservations SET status = 'released' WHERE id::text = $1`, res.id); err != nil {
				return err
			}
		}

		// Batch allocations (a no-op for a line without any, or one the
		// expiry sweeper already released).
		lineRows, err := tx.Query(ctx, `SELECT id::text FROM sales_order_lines WHERE sales_order_id::text = $1`, orderID)
		if err != nil {
			return err
		}
		var lineIDs []string
		for lineRows.Next() {
			var id string
			if err := lineRows.Scan(&id); err != nil {
				lineRows.Close()
				return err
			}
			lineIDs = append(lineIDs, id)
		}
		lineRows.Close()
		if err := lineRows.Err(); err != nil {
			return err
		}
		for _, id := range lineIDs {
			if err := inventory.ReleaseLineBatchAllocations(ctx, tx, id); err != nil {
				return err
			}
		}

		// Give back what the hold claimed: a coupon's use and redeemed
		// loyalty points (the same statements clearDiscountLayers uses).
		// The lines and totals are left as the record of the hold.
		if _, err := tx.Exec(ctx, `
			UPDATE coupons c SET usage_count = GREATEST(c.usage_count - r.n, 0)
			FROM (SELECT coupon_id, count(*) AS n FROM coupon_redemptions WHERE sales_order_id::text = $1 GROUP BY coupon_id) r
			WHERE c.id = r.coupon_id`, orderID); err != nil {
			return err
		}
		for _, q := range []string{
			`DELETE FROM coupon_redemptions WHERE sales_order_id::text = $1`,
			`DELETE FROM loyalty_ledger WHERE sales_order_id::text = $1 AND entry_type = 'redeem'`,
		} {
			if _, err := tx.Exec(ctx, q, orderID); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE sales_orders SET status = 'held_voided', void_reason = $2, voided_at = now(), voided_by = $3::uuid
			WHERE id::text = $1`, orderID, req.Reason, claims.UserID); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, audit.Entry{EntityType: "sales_orders", EntityID: orderID, Action: "update", PerformedBy: claims.UserID,
			Before: map[string]any{"status": status, "order_number": orderNumber, "grand_total": grandTotal, "cashier_id": cashierID},
			After:  map[string]any{"status": "held_voided", "reservations_released": len(reservations)},
			Reason: req.Reason}); err != nil {
			return err
		}
		return loadOrder(ctx, tx, orderID, &resp)
	})

	switch {
	case errors.Is(err, errOrderNotHeld):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_HELD", "only a held order can be voided this way — a completed sale is voided with POST /sales/orders/{id}/void")
	case errors.Is(err, errNotUnsold):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_RELEASABLE", "only an open cart or a held order can be released — a completed sale is voided instead")
	case errors.Is(err, errCartInUse):
		httpx.Error(w, http.StatusConflict, "CART_IN_USE", "this cart was worked on in the last 15 minutes by another cashier — ask them to finish or discard it, or try again once it's idle")
	case errors.Is(err, errNotYourHold):
		httpx.Error(w, http.StatusForbidden, "FORBIDDEN", "another cashier opened this order — releasing it needs the sales.void permission")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not release the order")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}
