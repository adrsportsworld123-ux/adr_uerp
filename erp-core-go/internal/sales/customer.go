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
	"erp-core-go/internal/customers"
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
	// DiscountsCleared: the cart had discounts tied to the customer it
	// had before (loyalty points, a coupon counted against them), so its
	// discounts were undone to be applied again (see dropCustomerDiscounts).
	DiscountsCleared bool `json:"discounts_cleared,omitempty"`
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
	req.Name, req.Phone, req.Email = strings.TrimSpace(req.Name), strings.TrimSpace(req.Phone), strings.TrimSpace(req.Email)
	if req.CustomerID == "" && !customers.ValidEmail(req.Email) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "email is not a valid address")
		return
	}
	if req.CustomerID == "" && req.Name == "" && req.Phone == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "customer_id, or at least a name/phone for a walk-in customer, is required")
		return
	}

	var resp attachCustomerResponse
	resp.OrderID = orderID
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var orderStatus, before string
		if err := tx.QueryRow(ctx, `SELECT status, COALESCE(customer_id::text, '') FROM sales_orders WHERE id = $1 FOR UPDATE`, orderID).
			Scan(&orderStatus, &before); err != nil {
			return err
		}

		customerID := req.CustomerID
		if customerID == "" {
			if err := tx.QueryRow(ctx, `
				INSERT INTO customers (id, merchant_id, name, phone, email)
				VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, NULLIF($3,''))
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

		// Another customer on an open cart: their points and coupon uses
		// mustn't stay with the sale.
		if before != "" && before != customerID && (orderStatus == "cart" || orderStatus == "held") {
			cleared, err := h.dropCustomerDiscounts(ctx, tx, orderID)
			if err != nil {
				return err
			}
			resp.DiscountsCleared = cleared
		}
		if _, err := tx.Exec(ctx, `UPDATE sales_orders SET customer_id = $1 WHERE id = $2`, customerID, orderID); err != nil {
			return err
		}
		resp.CustomerID = customerID
		// A customer's segment can bring a promotion in (target_segment).
		if orderStatus == "cart" {
			return h.runCartHook(ctx, tx, orderID, claims.UserID)
		}
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

// DetachCustomer: DELETE /sales/orders/{id}/customer — the sale goes back
// to a walk-in (no customer). Only before checkout (a cart or a held
// sale): a completed sale's invoice keeps its customer. Loyalty points
// redeemed and coupons counted against the customer are undone with the
// cart's other discounts, to be applied again (discounts_cleared), the
// place of supply goes back to the branch's state (the supply trigger),
// and promotions for the customer's segment drop out. A sale with no
// customer answers as it is, so a retry is harmless. Answers with the
// order, like the other cart endpoints.
func (h *Handler) DetachCustomer(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	var resp orderResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var before *string
		if err := tx.QueryRow(ctx, `SELECT status, customer_id::text FROM sales_orders WHERE id::text = $1 FOR UPDATE`, orderID).
			Scan(&status, &before); err != nil {
			return err
		}
		if status != "cart" && status != "held" {
			return errOrderNotEditable
		}
		if before == nil {
			return loadOrder(ctx, tx, orderID, &resp)
		}
		cleared, err := h.dropCustomerDiscounts(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE sales_orders SET customer_id = NULL WHERE id::text = $1`, orderID); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, audit.Entry{EntityType: "sales_order", EntityID: orderID, Action: "update", PerformedBy: claims.UserID,
			Before: map[string]any{"customer_id": *before}, After: map[string]any{"customer_id": nil}, Reason: "walk-in customer"}); err != nil {
			return err
		}
		if err := recalcOrderTotals(ctx, tx, orderID, &resp); err != nil {
			return err
		}
		resp.DiscountsCleared = cleared
		if status == "cart" {
			return h.refreshPromotions(ctx, tx, orderID, claims.UserID, &resp)
		}
		return loadOrder(ctx, tx, orderID, &resp)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case errors.Is(err, errOrderNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "a completed sale keeps its customer — only a sale not yet paid can go back to walk-in")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not clear the customer")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// dropCustomerDiscounts undoes the cart's discounts when one of them
// belongs to its customer — loyalty points redeemed, or a coupon whose use
// was counted against them — as a line edit does (clearDiscountLayers),
// since the layers were sized together. Reports whether the cashier has
// discounts to apply again. A cart with only promotions or a manual
// discount keeps them.
func (h *Handler) dropCustomerDiscounts(ctx context.Context, tx pgx.Tx, orderID string) (bool, error) {
	var tied bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM sales_order_discounts WHERE sales_order_id::text = $1 AND type = 'loyalty')
		    OR EXISTS (SELECT 1 FROM coupon_redemptions WHERE sales_order_id::text = $1 AND customer_id IS NOT NULL)`, orderID).Scan(&tied); err != nil {
		return false, err
	}
	if !tied {
		return false, nil
	}
	byHand, err := h.discountsNeedReapplying(ctx, tx, orderID)
	if err != nil {
		return false, err
	}
	cleared, err := clearDiscountLayers(ctx, tx, orderID)
	if err != nil {
		return false, err
	}
	var resp orderResponse
	if err := recalcOrderTotals(ctx, tx, orderID, &resp); err != nil {
		return false, err
	}
	return cleared && byHand, nil
}
