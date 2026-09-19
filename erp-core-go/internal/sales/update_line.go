package sales

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type updateLineRequest struct {
	Quantity float64 `json:"quantity"`
}

// UpdateLine: PATCH /sales/orders/{id}/lines/{line_id} — change a line's
// quantity pre-finalization. Adjusts the stock reservation by the delta
// (reserveStock for an increase, releaseReservation for a decrease) rather
// than releasing and re-reserving the full quantity, so a concurrent
// reservation on the same variant only ever sees the actual net change.
//
// Known simplification: any discount already applied to this line
// (discount_amount) is reset to 0 — it was computed against the
// pre-edit subtotal in POST .../discounts, and re-proportioning it here
// would need the whole order's discount context, not just this line.
// Re-apply the discount after editing quantity if needed; this mirrors the
// same reservation-matching simplification DeleteLine already documents
// (no direct FK from a line to its reservation).
func (h *Handler) UpdateLine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	lineID := chi.URLParam(r, "line_id")

	var req updateLineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Quantity <= 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "quantity must be positive")
		return
	}

	var resp orderResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID, status string
		if err := tx.QueryRow(ctx, `SELECT branch_id, status FROM sales_orders WHERE id = $1`, orderID).
			Scan(&branchID, &status); err != nil {
			return err
		}
		if status != "cart" {
			return errOrderNotEditable
		}

		var variantID string
		var oldQuantity float64
		if err := tx.QueryRow(ctx, `
			SELECT variant_id, quantity FROM sales_order_lines
			WHERE id = $1 AND sales_order_id = $2`, lineID, orderID,
		).Scan(&variantID, &oldQuantity); err != nil {
			return err
		}

		delta := req.Quantity - oldQuantity
		switch {
		case delta > 0:
			if err := reserveStock(ctx, tx, branchID, variantID, delta); err != nil {
				return err
			}
		case delta < 0:
			if err := releaseReservation(ctx, tx, branchID, variantID, -delta); err != nil {
				return err
			}
		}

		// Keep the matching reservation row's own quantity in step, same
		// best-effort matching DeleteLine uses (no direct FK to disambiguate
		// which reservation belongs to which line if a variant was added
		// more than once).
		if _, err := tx.Exec(ctx, `
			UPDATE stock_reservations SET quantity = $1
			WHERE id = (
				SELECT id FROM stock_reservations
				WHERE sales_order_id = $2 AND variant_id = $3 AND quantity = $4 AND status = 'active'
				ORDER BY created_at DESC LIMIT 1
			)`, req.Quantity, orderID, variantID, oldQuantity); err != nil {
			return err
		}

		var sellingPrice, cgst, sgst, igst, cess float64
		if err := tx.QueryRow(ctx, `
			SELECT pv.selling_price, COALESCE(ts.cgst_rate,0), COALESCE(ts.sgst_rate,0),
			       COALESCE(ts.igst_rate,0), COALESCE(ts.cess_rate,0)
			FROM product_variants pv
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
			WHERE pv.id = $1`, variantID,
		).Scan(&sellingPrice, &cgst, &sgst, &igst, &cess); err != nil {
			return err
		}
		lineSubtotal := sellingPrice * req.Quantity
		taxAmount := lineSubtotal * (cgst + sgst + igst + cess) / 100
		lineTotal := lineSubtotal + taxAmount

		if _, err := tx.Exec(ctx, `
			UPDATE sales_order_lines
			SET quantity = $1, discount_amount = 0, tax_amount = $2, line_total = $3
			WHERE id = $4`, req.Quantity, taxAmount, lineTotal, lineID); err != nil {
			return err
		}

		return recalcOrderTotals(ctx, tx, orderID, &resp)
	})

	switch {
	case errors.Is(err, ErrInsufficientStock):
		httpx.Error(w, http.StatusConflict, "STOCK_UNAVAILABLE", "requested quantity exceeds available stock")
	case errors.Is(err, errOrderNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "this order is no longer a cart")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order or line not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update line")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}
