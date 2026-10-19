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
	"erp-core-go/internal/taxes"
)

// SetPlaceOfSupply: PUT /sales/orders/{id}/place-of-supply — the state the
// goods are delivered to (migrations/061), for a sale that leaves the
// branch's state: a walk-in delivery to another state is then inter-state
// (IGST), and a B2B customer's goods follow the delivery, not the GSTIN.
// {"state_code": null} goes back to the usual rule (the customer's GSTIN
// state, else the branch's). Only while the order is a cart or held: a
// finalized invoice's place of supply never changes.
func (h *Handler) SetPlaceOfSupply(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	var req struct {
		StateCode *string `json:"state_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.StateCode != nil {
		if v := strings.TrimSpace(*req.StateCode); v == "" {
			req.StateCode = nil
		} else if !taxes.ValidState(v) {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "state_code must be a GST state code (e.g. 33 for Tamil Nadu)")
			return
		} else {
			req.StateCode = &v
		}
	}
	var resp orderResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var before *string
		if err := tx.QueryRow(ctx, `SELECT status, ship_to_state FROM sales_orders WHERE id::text = $1 FOR UPDATE`, orderID).Scan(&status, &before); err != nil {
			return err
		}
		if status != "cart" && status != "held" {
			return errOrderNotEditable
		}
		if _, err := tx.Exec(ctx, `UPDATE sales_orders SET ship_to_state = $2 WHERE id::text = $1`, orderID, req.StateCode); err != nil {
			return err
		}
		if err := loadOrder(ctx, tx, orderID, &resp); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "sales_order", EntityID: orderID, Action: "update", PerformedBy: claims.UserID,
			Before: map[string]any{"ship_to_state": before}, After: map[string]any{"ship_to_state": req.StateCode}, Reason: "place of supply"})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case errors.Is(err, errOrderNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "the place of supply can only change before checkout")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not set the place of supply")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}
