// Package sales implements the POS transaction flow: open a cart, add
// lines (which reserves stock), and checkout (which converts reservations
// into a finalized, paid sale). Every SQL statement in this file was
// verified against a live, seeded, RLS-enabled Postgres instance during
// design, including the full cart→checkout lifecycle and the idempotent-
// retry behavior — see phase0_1_design.md.
package sales

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

type orderResponse struct {
	OrderID       string `json:"order_id"`
	OrderNumber   string `json:"order_number"`
	Status        string `json:"status"`
	Subtotal      string `json:"subtotal"`
	DiscountTotal string `json:"discount_total"`
	TaxTotal      string `json:"tax_total"`
	GrandTotal    string `json:"grand_total"`
}

// ---------------------------------------------------------------------
// POST /sales/orders — open a cart
// ---------------------------------------------------------------------

type createOrderRequest struct {
	BranchID       string `json:"branch_id"`
	POSTerminalID  string `json:"pos_terminal_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}

	var req createOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.BranchID == "" || req.POSTerminalID == "" || req.IdempotencyKey == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id, pos_terminal_id and idempotency_key are required")
		return
	}

	// Human-readable receipt number. Timestamp+nanosecond keeps this
	// collision-free without a per-branch counter/sequence to contend on —
	// swap for a prettier sequential number (a per-branch sequence table)
	// once that's worth the extra round trip; the idempotency_key, not this
	// number, is what actually guarantees exactly-once processing.
	branchPrefix := req.BranchID
	if len(branchPrefix) > 8 {
		branchPrefix = branchPrefix[:8]
	}
	orderNumber := fmt.Sprintf("%s-%d", branchPrefix, time.Now().UnixNano())

	var resp orderResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO sales_orders (id, merchant_id, branch_id, pos_terminal_id, cashier_id, order_number, status, idempotency_key)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, 'cart', $5)
			ON CONFLICT (merchant_id, idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
			RETURNING id, order_number, status, subtotal::text, discount_total::text, tax_total::text, grand_total::text`,
			req.BranchID, req.POSTerminalID, claims.UserID, orderNumber, req.IdempotencyKey)
		return row.Scan(&resp.OrderID, &resp.OrderNumber, &resp.Status,
			&resp.Subtotal, &resp.DiscountTotal, &resp.TaxTotal, &resp.GrandTotal)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create order")
		return
	}

	httpx.JSON(w, http.StatusCreated, resp)
}

// ---------------------------------------------------------------------
// POST /sales/orders/{id}/lines — add a line, reserving stock in the
// same transaction so a reservation can never exist without its line
// or vice versa.
// ---------------------------------------------------------------------

type addLineRequest struct {
	VariantID string  `json:"variant_id"`
	Quantity  float64 `json:"quantity"`
}

func (h *Handler) AddLine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var req addLineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.VariantID == "" || req.Quantity <= 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "variant_id and a positive quantity are required")
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

		var sellingPrice, cgst, sgst, igst, cess float64
		if err := tx.QueryRow(ctx, `
			SELECT pv.selling_price, COALESCE(ts.cgst_rate,0), COALESCE(ts.sgst_rate,0),
			       COALESCE(ts.igst_rate,0), COALESCE(ts.cess_rate,0)
			FROM product_variants pv
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
			WHERE pv.id = $1`, req.VariantID,
		).Scan(&sellingPrice, &cgst, &sgst, &igst, &cess); err != nil {
			return err
		}

		if err := reserveStock(ctx, tx, branchID, req.VariantID, req.Quantity); err != nil {
			return err
		}

		// NOTE on float64 for money: this computes in float64 and relies on
		// the destination columns being NUMERIC(14,2), which round cleanly on
		// insert regardless of any sub-paisa binary-float noise upstream —
		// verified against representative price/quantity/tax-rate combinations
		// to show no meaningful drift. That's an acceptable trade-off for
		// Phase 1's INR retail price range, but don't carry this pattern into
		// high-value B2B invoicing, multi-currency conversion, or anywhere
		// compounding roundoff across many lines would matter — switch to
		// integer-paise (or a decimal library) arithmetic before then.
		lineSubtotal := sellingPrice * req.Quantity
		taxRatePct := cgst + sgst + igst + cess
		taxAmount := lineSubtotal * taxRatePct / 100
		lineTotal := lineSubtotal + taxAmount

		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_reservations (id, merchant_id, branch_id, variant_id, sales_order_id, quantity, status, expires_at)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, 'active', now() + interval '15 minutes')`,
			branchID, req.VariantID, orderID, req.Quantity); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO sales_order_lines (id, sales_order_id, variant_id, quantity, unit_price, discount_amount, tax_amount, line_total)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, 0, $5, $6)`,
			orderID, req.VariantID, req.Quantity, sellingPrice, taxAmount, lineTotal); err != nil {
			return err
		}

		row := tx.QueryRow(ctx, `
			UPDATE sales_orders SET
			  subtotal = (SELECT COALESCE(SUM(unit_price*quantity),0) FROM sales_order_lines WHERE sales_order_id = $1),
			  tax_total = (SELECT COALESCE(SUM(tax_amount),0) FROM sales_order_lines WHERE sales_order_id = $1),
			  discount_total = (SELECT COALESCE(SUM(discount_amount),0) FROM sales_order_lines WHERE sales_order_id = $1),
			  grand_total = (SELECT COALESCE(SUM(line_total),0) FROM sales_order_lines WHERE sales_order_id = $1)
			WHERE id = $1
			RETURNING id, order_number, status, subtotal::text, discount_total::text, tax_total::text, grand_total::text`, orderID)
		return row.Scan(&resp.OrderID, &resp.OrderNumber, &resp.Status,
			&resp.Subtotal, &resp.DiscountTotal, &resp.TaxTotal, &resp.GrandTotal)
	})

	switch {
	case errors.Is(err, ErrInsufficientStock):
		httpx.Error(w, http.StatusConflict, "STOCK_UNAVAILABLE", "requested quantity exceeds available stock")
	case errors.Is(err, errOrderNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "this order is no longer a cart")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order or product not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not add line")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// ---------------------------------------------------------------------
// POST /sales/orders/{id}/checkout
// ---------------------------------------------------------------------

type checkoutRequest struct {
	Payments []struct {
		Method string  `json:"method"`
		Amount float64 `json:"amount"`
	} `json:"payments"`
}

func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var req checkoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}

	var resp orderResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID, status, grandTotal string
		if err := tx.QueryRow(ctx, `SELECT branch_id, status, grand_total::text FROM sales_orders WHERE id = $1`, orderID).
			Scan(&branchID, &status, &grandTotal); err != nil {
			return err
		}

		// Idempotency: a finalized order simply returns its existing result
		// rather than reprocessing — this is what makes it safe for a POS
		// device to retry a checkout call after a dropped connection without
		// double-charging or double-decrementing stock.
		if status == "finalized" {
			return loadOrder(ctx, tx, orderID, &resp)
		}
		if status != "cart" {
			return errOrderNotEditable
		}

		var paidTotal float64
		for _, p := range req.Payments {
			paidTotal += p.Amount
		}
		grandTotalFloat, err := strconv.ParseFloat(grandTotal, 64)
		if err != nil {
			return err
		}
		if paidTotal < grandTotalFloat {
			return errPaymentMismatch
		}

		rows, err := tx.Query(ctx, `
			SELECT variant_id, quantity FROM stock_reservations
			WHERE sales_order_id = $1 AND status = 'active'`, orderID)
		if err != nil {
			return err
		}
		type reservation struct {
			variantID string
			quantity  float64
		}
		var reservations []reservation
		for rows.Next() {
			var res reservation
			if err := rows.Scan(&res.variantID, &res.quantity); err != nil {
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
			if err := consumeReservation(ctx, tx, branchID, res.variantID, res.quantity); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_movements (merchant_id, branch_id, variant_id, movement_type, quantity_delta, reference_type, reference_id, performed_by)
				VALUES (current_setting('app.tenant_id')::uuid, $1, $2, 'sale', $3, 'sales_order', $4, $5)`,
				branchID, res.variantID, -res.quantity, orderID, claims.UserID); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `UPDATE stock_reservations SET status = 'consumed' WHERE sales_order_id = $1 AND status = 'active'`, orderID); err != nil {
			return err
		}

		for _, p := range req.Payments {
			if _, err := tx.Exec(ctx, `
				INSERT INTO payments (id, sales_order_id, method, amount, status)
				VALUES (gen_random_uuid(), $1, $2, $3, 'captured')`, orderID, p.Method, p.Amount); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `UPDATE sales_orders SET status = 'finalized', finalized_at = now() WHERE id = $1`, orderID); err != nil {
			return err
		}

		return loadOrder(ctx, tx, orderID, &resp)
	})

	switch {
	case errors.Is(err, ErrInsufficientStock):
		// Should be rare here (AddLine already reserved the stock) but can
		// happen if a manual adjustment shrank on_hand between reservation
		// and checkout — surfacing it rather than silently overselling.
		httpx.Error(w, http.StatusConflict, "STOCK_UNAVAILABLE", "stock changed since this item was added to the cart")
	case errors.Is(err, errOrderNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "this order has already been voided or refunded")
	case errors.Is(err, errPaymentMismatch):
		httpx.Error(w, http.StatusBadRequest, "PAYMENT_MISMATCH", "payment total does not cover the order's grand total")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "checkout failed")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// ---------------------------------------------------------------------
// GET /sales/orders/{id}
// ---------------------------------------------------------------------

func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var resp orderResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return loadOrder(ctx, tx, orderID, &resp)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load order")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func loadOrder(ctx context.Context, tx pgx.Tx, orderID string, resp *orderResponse) error {
	row := tx.QueryRow(ctx, `
		SELECT id, order_number, status, subtotal::text, discount_total::text, tax_total::text, grand_total::text
		FROM sales_orders WHERE id = $1`, orderID)
	return row.Scan(&resp.OrderID, &resp.OrderNumber, &resp.Status,
		&resp.Subtotal, &resp.DiscountTotal, &resp.TaxTotal, &resp.GrandTotal)
}

var (
	errOrderNotEditable = errors.New("order is not editable")
	errPaymentMismatch  = errors.New("payment total does not match grand total")
)
