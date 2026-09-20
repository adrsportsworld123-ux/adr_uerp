package sales

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type voidRequest struct {
	Reason string `json:"reason"`
}

var errOrderNotVoidable = errors.New("order must be finalized to void")

// Void: POST /sales/orders/{id}/void — reverses a finalized order.
// Authorization is enforced by authn.RequirePermission("sales.void") at
// the router level, same mechanism as inventory adjustments.
//
// Reverses stock the same way checkout consumed it, in reverse: on_hand
// goes back up by each line's quantity (reserved is untouched — checkout
// already zeroed it when the reservation was consumed, so there's nothing
// to "un-reserve"). Recorded as an 'adjustment' movement (the movement_type
// CHECK constraint has no dedicated 'void' value) tagged
// reference_type='void' so it's still distinguishable in the audit trail
// from a manual stock correction.
func (h *Handler) Void(w http.ResponseWriter, r *http.Request) {
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
	if req.Reason == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "reason is required")
		return
	}

	var resp orderResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID, status string
		var subtotal, discountTotal, taxTotal float64
		if err := tx.QueryRow(ctx, `
			SELECT branch_id, status, subtotal, discount_total, tax_total FROM sales_orders WHERE id = $1`, orderID,
		).Scan(&branchID, &status, &subtotal, &discountTotal, &taxTotal); err != nil {
			return err
		}
		if status != "finalized" {
			return errOrderNotVoidable
		}

		rows, err := tx.Query(ctx, `SELECT variant_id, quantity FROM sales_order_lines WHERE sales_order_id = $1`, orderID)
		if err != nil {
			return err
		}
		type line struct {
			variantID string
			quantity  float64
		}
		var lines []line
		for rows.Next() {
			var l line
			if err := rows.Scan(&l.variantID, &l.quantity); err != nil {
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
			if _, err := tx.Exec(ctx, `
				UPDATE stock_levels SET on_hand = on_hand + $1, version = version + 1, updated_at = now()
				WHERE branch_id = $2 AND variant_id = $3`, l.quantity, branchID, l.variantID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_movements (merchant_id, branch_id, variant_id, movement_type, quantity_delta, reference_type, reference_id, reason, performed_by)
				VALUES (current_setting('app.tenant_id')::uuid, $1, $2, 'adjustment', $3, 'void', $4, $5, $6)`,
				branchID, l.variantID, l.quantity, orderID, req.Reason, claims.UserID); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE sales_orders SET status = 'voided', void_reason = $1 WHERE id = $2`, req.Reason, orderID); err != nil {
			return err
		}

		payRows, err := tx.Query(ctx, `SELECT method, amount FROM payments WHERE sales_order_id = $1 AND status = 'captured'`, orderID)
		if err != nil {
			return err
		}
		var creditLines []accounting.JournalLine
		for payRows.Next() {
			var method string
			var amount float64
			if err := payRows.Scan(&method, &amount); err != nil {
				payRows.Close()
				return err
			}
			creditLines = append(creditLines, accounting.JournalLine{AccountCode: accounting.AccountCodeForPaymentMethod(method), Credit: amount})
		}
		payRows.Close()
		if err := payRows.Err(); err != nil {
			return err
		}
		if err := postSaleVoidJournal(ctx, tx, branchID, orderID, claims.UserID, creditLines, subtotal, discountTotal, taxTotal); err != nil {
			return err
		}

		return loadOrder(ctx, tx, orderID, &resp)
	})

	switch {
	case errors.Is(err, errOrderNotVoidable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "only a finalized order can be voided")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not void order")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}
