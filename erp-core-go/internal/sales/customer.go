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

type attachCustomerRequest struct {
	CustomerID string `json:"customer_id"` // if set, the other fields are ignored
	Name       string `json:"name"`
	Phone      string `json:"phone"`
	Email      string `json:"email"`
}

type attachCustomerResponse struct {
	OrderID    string `json:"order_id"`
	CustomerID string `json:"customer_id"`
}

// AttachCustomer: POST /sales/orders/{id}/customer — attaches an existing
// customer by id, or creates a walk-in customer record from inline
// name/phone/email and attaches that. Not restricted to cart-status orders
// — attaching a customer after checkout (e.g. for a loyalty/receipt lookup
// added as an afterthought) doesn't touch totals or stock, so there's no
// reason to forbid it.
func (h *Handler) AttachCustomer(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var req attachCustomerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.CustomerID == "" && req.Name == "" && req.Phone == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "customer_id, or at least a name/phone for a walk-in customer, is required")
		return
	}

	var resp attachCustomerResponse
	resp.OrderID = orderID
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var orderExists string
		if err := tx.QueryRow(ctx, `SELECT id FROM sales_orders WHERE id = $1`, orderID).Scan(&orderExists); err != nil {
			return err
		}

		customerID := req.CustomerID
		if customerID == "" {
			if err := tx.QueryRow(ctx, `
				INSERT INTO customers (id, merchant_id, name, phone, email)
				VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3)
				RETURNING id`, req.Name, req.Phone, req.Email,
			).Scan(&customerID); err != nil {
				return err
			}
		} else {
			var customerExists string
			if err := tx.QueryRow(ctx, `SELECT id FROM customers WHERE id = $1`, customerID).Scan(&customerExists); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `UPDATE sales_orders SET customer_id = $1 WHERE id = $2`, customerID, orderID); err != nil {
			return err
		}
		resp.CustomerID = customerID
		return nil
	})

	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order or customer not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not attach customer")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
