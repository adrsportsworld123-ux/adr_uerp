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

// Standalone stock holds outside the cart flow — e.g. a phone/counter order
// promised to a customer before any sales_order exists. AddLine/DeleteLine
// (handlers.go) already cover the cart-driven case; stock_reservations.
// sales_order_id has always been nullable (migrations/001_schema.sql)
// specifically so a hold can exist with no cart behind it — this was the
// one path that column supported but had no API for. Gated by
// inventory.adjust (router.go), the same permission stock adjustments use:
// this is an inventory-management action, not something every POS user
// should be able to do for an arbitrary quantity/duration.

const defaultManualReservationMinutes = 15

var errCartOwnedReservation = errors.New("reservation belongs to a cart")

type createReservationRequest struct {
	BranchID         string  `json:"branch_id"`
	VariantID        string  `json:"variant_id"`
	Quantity         float64 `json:"quantity"`
	ExpiresInMinutes int     `json:"expires_in_minutes"`
}

type reservationResponse struct {
	ReservationID string `json:"reservation_id"`
	BranchID      string `json:"branch_id"`
	VariantID     string `json:"variant_id"`
	Quantity      string `json:"quantity"`
	Status        string `json:"status"`
	ExpiresAt     string `json:"expires_at"`
}

// CreateReservation: POST /inventory/reservations
func (h *Handler) CreateReservation(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}

	var req createReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.BranchID == "" || req.VariantID == "" || req.Quantity <= 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id, variant_id and a positive quantity are required")
		return
	}
	minutes := req.ExpiresInMinutes
	if minutes <= 0 {
		minutes = defaultManualReservationMinutes
	}

	var resp reservationResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := reserveStock(ctx, tx, req.BranchID, req.VariantID, req.Quantity); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO stock_reservations (id, merchant_id, branch_id, variant_id, sales_order_id, quantity, status, expires_at)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, NULL, $3, 'active', now() + make_interval(mins => $4))
			RETURNING id, branch_id, variant_id, quantity::text, status, expires_at::text`,
			req.BranchID, req.VariantID, req.Quantity, minutes,
		).Scan(&resp.ReservationID, &resp.BranchID, &resp.VariantID, &resp.Quantity, &resp.Status, &resp.ExpiresAt)
	})

	switch {
	case errors.Is(err, ErrInsufficientStock):
		httpx.Error(w, http.StatusConflict, "STOCK_UNAVAILABLE", "requested quantity exceeds available stock")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no stock record for this branch/variant")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create reservation")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// ReleaseManualReservation: DELETE /inventory/reservations/{id} — releases
// an active, non-cart reservation early (before its expiry sweeper would
// otherwise reclaim it). Refuses to touch a cart-owned reservation
// (sales_order_id IS NOT NULL) — that one's lifecycle belongs to
// DELETE /sales/orders/{id}/lines/{line_id} instead, which also deletes the
// order line the reservation backs; releasing it here would leave a
// dangling line with no stock behind it.
func (h *Handler) ReleaseManualReservation(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	reservationID := chi.URLParam(r, "id")

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID, variantID string
		var quantity float64
		var salesOrderID *string
		if err := tx.QueryRow(ctx, `
			SELECT branch_id, variant_id, quantity, sales_order_id FROM stock_reservations
			WHERE id = $1 AND status = 'active'`, reservationID,
		).Scan(&branchID, &variantID, &quantity, &salesOrderID); err != nil {
			return err
		}
		if salesOrderID != nil {
			return errCartOwnedReservation
		}
		if err := releaseReservation(ctx, tx, branchID, variantID, quantity); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE stock_reservations SET status = 'released' WHERE id = $1`, reservationID)
		return err
	})

	switch {
	case errors.Is(err, errCartOwnedReservation):
		httpx.Error(w, http.StatusConflict, "RESERVATION_CART_OWNED", "this reservation belongs to a cart — remove it via DELETE /sales/orders/{id}/lines/{line_id} instead")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no active reservation with this id")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not release reservation")
	default:
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "released"})
	}
}
