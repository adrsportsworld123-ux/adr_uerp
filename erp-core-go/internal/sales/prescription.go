package sales

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type attachPrescriptionRequest struct {
	PrescriptionID string `json:"prescription_id"`
}

type attachPrescriptionResponse struct {
	OrderID        string `json:"order_id"`
	PrescriptionID string `json:"prescription_id"`
}

// AttachPrescription: POST /sales/orders/{id}/prescription — same
// not-restricted-to-cart-status shape as AttachCustomer (customer.go):
// attaching evidence after the fact (e.g. a prescription collected right
// before Checkout, or added afterward for the record) doesn't touch
// totals or stock, so there's no reason to forbid it either way. Checkout
// is where a missing-but-required prescription actually blocks anything.
func (h *Handler) AttachPrescription(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var req attachPrescriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PrescriptionID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "prescription_id is required")
		return
	}

	var resp attachPrescriptionResponse
	resp.OrderID = orderID
	resp.PrescriptionID = req.PrescriptionID
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var orderExists, prescriptionExists string
		if err := tx.QueryRow(ctx, `SELECT id FROM sales_orders WHERE id = $1`, orderID).Scan(&orderExists); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM prescriptions WHERE id = $1`, req.PrescriptionID).Scan(&prescriptionExists); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE sales_orders SET prescription_id = $1 WHERE id = $2`, req.PrescriptionID, orderID)
		return err
	})

	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order or prescription not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not attach prescription")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
