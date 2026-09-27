package sales

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/inventory"
)

var errOrderNotHeld = errors.New("order is not currently held")

type holdRequest struct {
	Note string `json:"note"`
}

// Hold: POST /sales/orders/{id}/hold — parks an in-progress cart so the
// cashier can serve someone else and come back to it. Requires status =
// 'cart' and at least one line (nothing to come back to otherwise), and
// extends every active reservation this order holds out to
// HoldDurationMinutes from now, so the ordinary 15-minute sweeper doesn't
// reclaim stock out from under a hold that's still well within its own,
// longer, deliberately-explicit-action window — see config.go's doc
// comment on HoldDurationMinutes for why this differs from the FRD's
// 15-minute abandoned-cart rule.
func (h *Handler) Hold(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var req holdRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
			return
		}
	}

	holdMinutes := h.HoldDurationMinutes
	if holdMinutes <= 0 {
		holdMinutes = 120
	}

	var resp orderResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM sales_orders WHERE id = $1`, orderID).Scan(&status); err != nil {
			return err
		}
		if status != "cart" {
			return errOrderNotEditable
		}

		var lineCount int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM sales_order_lines WHERE sales_order_id = $1`, orderID).Scan(&lineCount); err != nil {
			return err
		}
		if lineCount == 0 {
			return errEmptyCart
		}

		if _, err := tx.Exec(ctx, `
			UPDATE stock_reservations SET expires_at = now() + make_interval(mins => $2::int)
			WHERE sales_order_id = $1 AND status = 'active'`, orderID, holdMinutes); err != nil {
			return err
		}

		var note *string
		if req.Note != "" {
			note = &req.Note
		}
		if _, err := tx.Exec(ctx, `
			UPDATE sales_orders SET status = 'held', held_at = now(), hold_note = $2 WHERE id = $1`,
			orderID, note); err != nil {
			return err
		}

		return loadOrder(ctx, tx, orderID, &resp)
	})

	switch {
	case errors.Is(err, errOrderNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "only an open cart can be held")
	case errors.Is(err, errEmptyCart):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "cannot hold an order with no lines")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not hold order")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

type unavailableLine struct {
	LineID    string `json:"line_id"`
	VariantID string `json:"variant_id"`
	Reason    string `json:"reason"`
}

type recallResponse struct {
	orderResponse
	UnavailableLines []unavailableLine `json:"unavailable_lines,omitempty"`
}

// Recall: POST /sales/orders/{id}/recall — brings a held cart back into an
// editable 'cart', re-validating (and if needed re-reserving) stock for
// any line whose reservation didn't survive the hold — a hold that
// outlived HoldDurationMinutes, or (Phase 8) a batch-tracked line whose
// allocation was reclaimed along with it. Never silently drops or
// force-completes an unavailable line: it comes back in
// unavailable_lines so the cashier decides whether to remove/adjust it
// before checkout, the same "fail closed, surface it" convention
// STOCK_EXPIRED already uses elsewhere in this package.
func (h *Handler) Recall(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var resp recallResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID, status string
		if err := tx.QueryRow(ctx, `SELECT branch_id, status FROM sales_orders WHERE id = $1`, orderID).
			Scan(&branchID, &status); err != nil {
			return err
		}
		if status != "held" {
			return errOrderNotHeld
		}

		rows, err := tx.Query(ctx, `
			SELECT sol.id, sol.variant_id, sol.quantity, pv.track_batch, COALESCE(c.min_shelf_life_days, 0)
			FROM sales_order_lines sol
			JOIN product_variants pv ON pv.id = sol.variant_id
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN categories c ON c.id = p.category_id
			WHERE sol.sales_order_id = $1`, orderID)
		if err != nil {
			return err
		}
		type line struct {
			id, variantID    string
			quantity         float64
			trackBatch       bool
			minShelfLifeDays int
		}
		var lines []line
		for rows.Next() {
			var l line
			if err := rows.Scan(&l.id, &l.variantID, &l.quantity, &l.trackBatch, &l.minShelfLifeDays); err != nil {
				rows.Close()
				return err
			}
			lines = append(lines, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, l := range lines {
			var activeCount int
			if err := tx.QueryRow(ctx, `
				SELECT COUNT(*) FROM stock_reservations
				WHERE sales_order_id = $1 AND variant_id = $2 AND quantity = $3 AND status = 'active'`,
				orderID, l.variantID, l.quantity).Scan(&activeCount); err != nil {
				return err
			}
			if activeCount > 0 {
				continue // this line's hold survived intact
			}

			if err := reacquireLineStock(ctx, tx, branchID, orderID, l.id, l.variantID, l.quantity, l.trackBatch, l.minShelfLifeDays); err != nil {
				reason := "insufficient stock"
				switch {
				case errors.Is(err, inventory.ErrExpiredOrUntrackedStock):
					reason = "no unexpired batch stock available"
				case errors.Is(err, ErrInsufficientStock):
					// keep default reason
				default:
					return err // a real infrastructure error, not a "stock's just not there" outcome
				}
				resp.UnavailableLines = append(resp.UnavailableLines, unavailableLine{LineID: l.id, VariantID: l.variantID, Reason: reason})
			}
		}

		if _, err := tx.Exec(ctx, `UPDATE sales_orders SET status = 'cart', held_at = NULL WHERE id = $1`, orderID); err != nil {
			return err
		}

		return recalcOrderTotals(ctx, tx, orderID, &resp.orderResponse)
	})

	switch {
	case errors.Is(err, errOrderNotHeld):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_HELD", "this order is not currently held")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not recall order")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// reacquireLineStock re-reserves stock for an existing line whose hold
// didn't survive. Mirrors AddLineToCart's reserve-then-batch-allocate
// sequence exactly, but against an already-existing line/quantity rather
// than a brand new one, so Recall can use it without touching
// AddLineToCart's own, already-verified path at all.
func reacquireLineStock(ctx context.Context, tx pgx.Tx, branchID, orderID, lineID, variantID string, quantity float64, trackBatch bool, minShelfLifeDays int) error {
	if err := reserveStock(ctx, tx, branchID, variantID, quantity); err != nil {
		return err
	}
	if trackBatch {
		allocations, err := inventory.AllocateBatchesFIFO(ctx, tx, branchID, variantID, quantity, minShelfLifeDays)
		if err != nil {
			if releaseErr := releaseReservation(ctx, tx, branchID, variantID, quantity); releaseErr != nil {
				return releaseErr
			}
			return err
		}
		if err := inventory.RecordLineBatchAllocations(ctx, tx, lineID, allocations); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO stock_reservations (id, merchant_id, branch_id, variant_id, sales_order_id, quantity, status, expires_at)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, 'active', now() + interval '15 minutes')`,
		branchID, variantID, orderID, quantity)
	return err
}

type heldOrderSummary struct {
	OrderID     string `json:"order_id"`
	OrderNumber string `json:"order_number"`
	HeldAt      string `json:"held_at"`
	HoldNote    string `json:"hold_note"`
	LineCount   int    `json:"line_count"`
	GrandTotal  string `json:"grand_total"`
}

// ListHeld: GET /sales/orders/held?branch_id= — the recall screen's list
// of every cart currently on hold at a branch.
func (h *Handler) ListHeld(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")

	held := []heldOrderSummary{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT so.id, so.order_number, so.held_at, COALESCE(so.hold_note, ''), so.grand_total::text,
			       (SELECT COUNT(*) FROM sales_order_lines WHERE sales_order_id = so.id)
			FROM sales_orders so
			WHERE so.status = 'held' AND ($1 = '' OR so.branch_id::text = $1)
			ORDER BY so.held_at DESC`, branchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s heldOrderSummary
			var heldAt time.Time
			if err := rows.Scan(&s.OrderID, &s.OrderNumber, &heldAt, &s.HoldNote, &s.GrandTotal, &s.LineCount); err != nil {
				return err
			}
			s.HeldAt = heldAt.Format(time.RFC3339)
			held = append(held, s)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list held orders")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"held_orders": held})
}
