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

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// LoyaltyEarner lets Checkout award loyalty points on a finalized sale
// without internal/sales importing internal/loyalty directly — loyalty
// already needs to import sales (for ApplyDiscountLayer, its redemption
// layer), so a direct sales -> loyalty import would cycle. Same
// field-injection shape router.go already uses for pricing.Handler.Search
// (internal/search), wired to a *loyalty.Handler there.
type LoyaltyEarner interface {
	EarnForOrder(ctx context.Context, tx pgx.Tx, customerID, orderID string, grandTotal float64) (bool, error)
}

type Handler struct {
	DB *db.DB
	// Loyalty is optional (nil is fine — Checkout just skips earning);
	// every real deployment wires it, same as pricing.Handler.Search.
	Loyalty LoyaltyEarner
	// Notify is optional (nil is fine — Checkout/NotifyReceipt just skip
	// sending); see notify_receipt.go's Notifier doc comment.
	Notify Notifier
}

type orderResponse struct {
	OrderID       string      `json:"order_id"`
	OrderNumber   string      `json:"order_number"`
	Status        string      `json:"status"`
	Subtotal      string      `json:"subtotal"`
	DiscountTotal string      `json:"discount_total"`
	TaxTotal      string      `json:"tax_total"`
	GrandTotal    string      `json:"grand_total"`
	Lines         []orderLine `json:"lines"`
}

type orderLine struct {
	LineID         string `json:"line_id"`
	VariantID      string `json:"variant_id"`
	SKU            string `json:"sku"`
	ProductName    string `json:"product_name"`
	Quantity       string `json:"quantity"`
	UnitPrice      string `json:"unit_price"`
	DiscountAmount string `json:"discount_amount"`
	TaxAmount      string `json:"tax_amount"`
	LineTotal      string `json:"line_total"`
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
		if err := row.Scan(&resp.OrderID, &resp.OrderNumber, &resp.Status,
			&resp.Subtotal, &resp.DiscountTotal, &resp.TaxTotal, &resp.GrandTotal); err != nil {
			return err
		}
		// A replayed idempotency_key returns the pre-existing cart, which may
		// already have lines from the original call — not necessarily empty.
		lines, err := loadOrderLines(ctx, tx, resp.OrderID)
		if err != nil {
			return err
		}
		resp.Lines = lines
		return nil
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

		return recalcOrderTotals(ctx, tx, orderID, &resp)
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
// DELETE /sales/orders/{id}/lines/{line_id} — remove a line and release
// its stock reservation. The schema has no FK from sales_order_lines to
// stock_reservations (AddLine inserts both independently in the same
// transaction), so the matching reservation is found by (order, variant,
// quantity) rather than a direct join. If a variant was added to the same
// cart more than once as separate lines, this releases one matching
// active reservation, not necessarily the exact one tied to this specific
// line — a known simplification worth a real join key if that scenario
// turns out to matter in practice.
// ---------------------------------------------------------------------

func (h *Handler) DeleteLine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	lineID := chi.URLParam(r, "line_id")

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
		var quantity float64
		if err := tx.QueryRow(ctx, `
			SELECT variant_id, quantity FROM sales_order_lines
			WHERE id = $1 AND sales_order_id = $2`, lineID, orderID,
		).Scan(&variantID, &quantity); err != nil {
			return err
		}

		var reservationID string
		err := tx.QueryRow(ctx, `
			SELECT id FROM stock_reservations
			WHERE sales_order_id = $1 AND variant_id = $2 AND quantity = $3 AND status = 'active'
			ORDER BY created_at DESC LIMIT 1`, orderID, variantID, quantity,
		).Scan(&reservationID)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		if err == nil {
			if err := releaseReservation(ctx, tx, branchID, variantID, quantity); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE stock_reservations SET status = 'released' WHERE id = $1`, reservationID); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `DELETE FROM sales_order_lines WHERE id = $1 AND sales_order_id = $2`, lineID, orderID); err != nil {
			return err
		}

		return recalcOrderTotals(ctx, tx, orderID, &resp)
	})

	switch {
	case errors.Is(err, errOrderNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "this order is no longer a cart")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order or line not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not remove line")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// ---------------------------------------------------------------------
// POST /sales/orders/{id}/checkout
// ---------------------------------------------------------------------

type checkoutRequest struct {
	Payments []struct {
		Method    string  `json:"method"`
		Amount    float64 `json:"amount"`
		Reference string  `json:"reference"` // card/UPI gateway transaction id (UTR), if the terminal/gateway integration provides one — matched later by accounting.MatchPaymentGatewaySettlementLine
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
		var customerID *string
		var subtotal, discountTotal, taxTotal float64
		if err := tx.QueryRow(ctx, `
			SELECT branch_id, status, grand_total::text, subtotal, discount_total, tax_total, customer_id
			FROM sales_orders WHERE id = $1`, orderID,
		).Scan(&branchID, &status, &grandTotal, &subtotal, &discountTotal, &taxTotal, &customerID); err != nil {
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

		// Found live while verifying the new POST /products endpoint: a
		// cart with zero lines (subtotal/grand_total both 0) passed the
		// paidTotal < grandTotalFloat check below trivially for *any*
		// paidTotal >= 0, letting checkout finalize an empty order and
		// still record whatever payment amount the client sent — a real
		// gap, not specific to how this cart ended up empty (AddLine
		// failing partway through is one way, but not the only one).
		var lineCount int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM sales_order_lines WHERE sales_order_id = $1`, orderID).Scan(&lineCount); err != nil {
			return err
		}
		if lineCount == 0 {
			return errEmptyCart
		}

		var paidTotal, creditPortion float64
		for _, p := range req.Payments {
			paidTotal += p.Amount
			if p.Method == "credit" {
				creditPortion += p.Amount
			}
		}
		grandTotalFloat, err := strconv.ParseFloat(grandTotal, 64)
		if err != nil {
			return err
		}
		if paidTotal < grandTotalFloat {
			return errPaymentMismatch
		}

		var dueDate *time.Time
		if creditPortion > 0 {
			var err error
			dueDate, err = checkCreditAndComputeDueDate(ctx, tx, customerID, creditPortion)
			if err != nil {
				return err
			}
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
			var reference *string
			if p.Reference != "" {
				reference = &p.Reference
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO payments (id, sales_order_id, method, amount, reference_no, status)
				VALUES (gen_random_uuid(), $1, $2, $3, $4, 'captured')`, orderID, p.Method, p.Amount, reference); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `UPDATE sales_orders SET status = 'finalized', finalized_at = now() WHERE id = $1`, orderID); err != nil {
			return err
		}

		// Journal Dr lines are scaled to sum to exactly grand_total, not the
		// raw submitted amounts — this system doesn't model "change given"
		// (an over-tendered cash payment), so without this an over-tender
		// would Dr more than the Cr side and fail PostJournalEntry's balance
		// check, breaking a checkout that would otherwise have succeeded. In
		// every real path through this codebase today (the Flutter client
		// always sends payments summing to exactly grand_total), scale is 1
		// and this is a no-op.
		scale := 1.0
		if paidTotal > grandTotalFloat && paidTotal > 0 {
			scale = grandTotalFloat / paidTotal
		}
		debitLines := make([]accounting.JournalLine, 0, len(req.Payments))
		for _, p := range req.Payments {
			line := accounting.JournalLine{AccountCode: accounting.AccountCodeForPaymentMethod(p.Method), Debit: p.Amount * scale}
			if p.Method == "credit" {
				// Books to Receivable, not a cash/clearing account —
				// party-tagged so it shows on this customer's
				// GET /accounting/party-ledger. checkCreditAndComputeDueDate
				// above already confirmed customerID is set whenever
				// creditPortion > 0.
				line = accounting.JournalLine{AccountCode: accounting.AccountReceivable, Debit: p.Amount * scale, PartyType: "customer", PartyID: *customerID}
			}
			debitLines = append(debitLines, line)
		}
		if err := postSaleJournal(ctx, tx, branchID, orderID, claims.UserID, debitLines, subtotal, discountTotal, taxTotal); err != nil {
			return err
		}

		if creditPortion > 0 {
			if _, err := tx.Exec(ctx, `UPDATE sales_orders SET credit_amount = $1, due_date = $2 WHERE id = $3`,
				creditPortion, dueDate, orderID); err != nil {
				return err
			}
		}

		// Loyalty earn (discount hierarchy item 5's counterpart — see
		// internal/loyalty.Handler.EarnForOrder for why a redemption on
		// this same order suppresses this rather than the other way
		// around). Only walk-in orders with no customer attached skip
		// this; h.Loyalty is nil only in tests/tools that never wire one.
		if h.Loyalty != nil && customerID != nil {
			if _, err := h.Loyalty.EarnForOrder(ctx, tx, *customerID, orderID, grandTotalFloat); err != nil {
				return err
			}
		}

		// Receipt notification (best-effort — see notify_receipt.go's doc
		// comment for why this can never fail the checkout it's part of).
		notifyReceipt(ctx, tx, h.Notify, orderID)

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
	case errors.Is(err, errEmptyCart):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "cannot check out an order with no lines")
	case errors.Is(err, errPaymentMismatch):
		httpx.Error(w, http.StatusBadRequest, "PAYMENT_MISMATCH", "payment total does not cover the order's grand total")
	case errors.Is(err, errNoCustomerForCredit):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a credit payment requires a customer already attached to the order")
	case errors.Is(err, errCreditHold):
		httpx.Error(w, http.StatusForbidden, "CREDIT_HOLD", "this customer is on credit hold")
	case errors.Is(err, errCreditLimitExceeded):
		httpx.Error(w, http.StatusConflict, "CREDIT_LIMIT_EXCEEDED", "this sale would put the customer over their credit limit")
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
	if err := row.Scan(&resp.OrderID, &resp.OrderNumber, &resp.Status,
		&resp.Subtotal, &resp.DiscountTotal, &resp.TaxTotal, &resp.GrandTotal); err != nil {
		return err
	}
	lines, err := loadOrderLines(ctx, tx, orderID)
	if err != nil {
		return err
	}
	resp.Lines = lines
	return nil
}

// recalcOrderTotals re-derives sales_orders' cached aggregate columns from
// sales_order_lines — the source of truth is always the lines, never a
// running total maintained incrementally, so this is safe to call after any
// line-mutating operation (add, discount, delete) without needing to track
// deltas. grand_total is computed explicitly as subtotal - discount + tax
// rather than SUM(line_total) alone, so it's correct even if a caller ever
// leaves line_total stale — belt-and-suspenders given how easy it is for a
// per-line total to drift from the columns it's derived from.
func recalcOrderTotals(ctx context.Context, tx pgx.Tx, orderID string, resp *orderResponse) error {
	row := tx.QueryRow(ctx, `
		UPDATE sales_orders SET
		  subtotal = (SELECT COALESCE(SUM(unit_price*quantity),0) FROM sales_order_lines WHERE sales_order_id = $1),
		  tax_total = (SELECT COALESCE(SUM(tax_amount),0) FROM sales_order_lines WHERE sales_order_id = $1),
		  discount_total = (SELECT COALESCE(SUM(discount_amount),0) FROM sales_order_lines WHERE sales_order_id = $1),
		  grand_total = (SELECT COALESCE(SUM(unit_price*quantity - discount_amount + tax_amount),0) FROM sales_order_lines WHERE sales_order_id = $1)
		WHERE id = $1
		RETURNING id, order_number, status, subtotal::text, discount_total::text, tax_total::text, grand_total::text`, orderID)
	if err := row.Scan(&resp.OrderID, &resp.OrderNumber, &resp.Status,
		&resp.Subtotal, &resp.DiscountTotal, &resp.TaxTotal, &resp.GrandTotal); err != nil {
		return err
	}
	lines, err := loadOrderLines(ctx, tx, orderID)
	if err != nil {
		return err
	}
	resp.Lines = lines
	return nil
}

// loadOrderLines joins sales_order_lines with product_variants for
// name/sku — closes the gap flagged in phase0_1_design.md §3.4: GET
// /sales/orders/{id} previously returned order-level aggregates only.
func loadOrderLines(ctx context.Context, tx pgx.Tx, orderID string) ([]orderLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT sol.id, sol.variant_id, pv.sku, p.name, sol.quantity::text,
		       sol.unit_price::text, sol.discount_amount::text, sol.tax_amount::text, sol.line_total::text
		FROM sales_order_lines sol
		JOIN product_variants pv ON pv.id = sol.variant_id
		JOIN products p ON p.id = pv.product_id
		WHERE sol.sales_order_id = $1
		ORDER BY sol.created_at`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	lines := []orderLine{}
	for rows.Next() {
		var l orderLine
		if err := rows.Scan(&l.LineID, &l.VariantID, &l.SKU, &l.ProductName, &l.Quantity,
			&l.UnitPrice, &l.DiscountAmount, &l.TaxAmount, &l.LineTotal); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

var (
	errOrderNotEditable    = errors.New("order is not editable")
	errPaymentMismatch     = errors.New("payment total does not match grand total")
	errEmptyCart           = errors.New("cannot check out an order with no lines")
	errNoCustomerForCredit = errors.New("a credit payment requires a customer already attached to the order")
	errCreditHold          = errors.New("customer is on credit hold")
	errCreditLimitExceeded = errors.New("sale would exceed customer's credit limit")
)

// paymentTermsDays maps customers.payment_terms to a due-date offset —
// see migrations/014_credit_facility.sql's CHECK constraint for the exact
// set of valid values.
var paymentTermsDays = map[string]int{
	"due_on_receipt": 0, "net_7": 7, "net_15": 15, "net_30": 30, "net_60": 60, "net_90": 90,
}

// checkCreditAndComputeDueDate is Phase 4's B2B Credit Facility auto-block
// (pos_frd_complete.md §6: "Auto-block on limit breach"), enforced at the
// one point a credit sale is actually created — Checkout. Outstanding is
// computed fresh from sales_orders (credit_amount - credit_paid summed
// across every other finalized order for this customer), not cached, the
// same "compute on read" choice used throughout this codebase (segment,
// margin, loyalty balance) rather than a running balance that could drift.
func checkCreditAndComputeDueDate(ctx context.Context, tx pgx.Tx, customerID *string, creditPortion float64) (*time.Time, error) {
	if customerID == nil {
		return nil, errNoCustomerForCredit
	}
	var creditLimit float64
	var paymentTerms string
	var creditHold bool
	if err := tx.QueryRow(ctx, `SELECT credit_limit, payment_terms, credit_hold FROM customers WHERE id = $1`, *customerID).
		Scan(&creditLimit, &paymentTerms, &creditHold); err != nil {
		return nil, err
	}
	if creditHold {
		return nil, errCreditHold
	}
	var currentOutstanding float64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(credit_amount - credit_paid), 0) FROM sales_orders
		WHERE customer_id = $1 AND status = 'finalized' AND credit_amount > credit_paid`, *customerID,
	).Scan(&currentOutstanding); err != nil {
		return nil, err
	}
	if currentOutstanding+creditPortion > creditLimit+0.01 { // epsilon for float rounding, same as RecordBillPayment
		return nil, errCreditLimitExceeded
	}
	due := time.Now().AddDate(0, 0, paymentTermsDays[paymentTerms]).Truncate(24 * time.Hour)
	return &due, nil
}
