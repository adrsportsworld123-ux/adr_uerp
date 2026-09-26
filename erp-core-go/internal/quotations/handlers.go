// Package quotations implements Phase 7's "formal B2B quotations" item
// (phased_roadmap.md Phase 7). A quotation is a pricing proposal for a
// named customer — it never touches stock (no reservation, unlike a
// cart), and it's built whole in one request rather than incrementally
// line-by-line like a POS cart, since a quote is a document you draft and
// send, not a live checkout session. The only place this ever becomes a
// real, stock-reserving transaction is ConvertQuotation, which opens a
// cart and adds lines through internal/sales' exact same
// CreateCart/AddLineToCart functions a walk-in POS sale uses — so a
// converted quote is, by construction, indistinguishable from any other
// sales_order to every downstream system (reports, checkout, accounting,
// stock). That's what makes "the same catalog and stock serve both a
// walk-in retail customer and a B2B wholesale order without double-entry"
// (this phase's own Definition of Done) true here, not just asserted.
package quotations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/inventory"
	"erp-core-go/internal/pricing"
	"erp-core-go/internal/sales"
)

type Handler struct {
	DB *db.DB
}

type quotationLine struct {
	LineID      string `json:"line_id"`
	VariantID   string `json:"variant_id"`
	SKU         string `json:"sku"`
	ProductName string `json:"product_name"`
	Quantity    string `json:"quantity"`
	UnitPrice   string `json:"unit_price"`
	TaxAmount   string `json:"tax_amount"`
	LineTotal   string `json:"line_total"`
}

type quotationResponse struct {
	QuotationID           string          `json:"quotation_id"`
	QuoteNumber           string          `json:"quote_number"`
	BranchID              string          `json:"branch_id"`
	CustomerID            string          `json:"customer_id"`
	Status                string          `json:"status"`
	ValidUntil            string          `json:"valid_until"`
	Subtotal              string          `json:"subtotal"`
	TaxTotal              string          `json:"tax_total"`
	GrandTotal            string          `json:"grand_total"`
	Notes                 string          `json:"notes"`
	ConvertedSalesOrderID *string         `json:"converted_sales_order_id"`
	CreatedAt             string          `json:"created_at"`
	Lines                 []quotationLine `json:"lines"`
}

type quotationSummary struct {
	QuotationID string `json:"quotation_id"`
	QuoteNumber string `json:"quote_number"`
	CustomerID  string `json:"customer_id"`
	Status      string `json:"status"`
	ValidUntil  string `json:"valid_until"`
	GrandTotal  string `json:"grand_total"`
	CreatedAt   string `json:"created_at"`
}

var (
	errQuotationNotDraft    = errors.New("quotation is not a draft")
	errQuotationNotSent     = errors.New("quotation has not been sent")
	errQuotationNotAccepted = errors.New("quotation has not been accepted")
)

// ---------------------------------------------------------------------
// POST /quotations — create a whole quote in one request (see package
// doc for why this isn't an incremental cart-style add-line flow).
// ---------------------------------------------------------------------

type createLineRequest struct {
	VariantID string   `json:"variant_id"`
	Quantity  float64  `json:"quantity"`
	UnitPrice *float64 `json:"unit_price"` // optional override; omitted = resolve via pricing.ResolvePrice
}

type createQuotationRequest struct {
	BranchID   string              `json:"branch_id"`
	CustomerID string              `json:"customer_id"`
	ValidUntil string              `json:"valid_until"` // YYYY-MM-DD, optional (defaults to +30 days)
	Notes      string              `json:"notes"`
	Lines      []createLineRequest `json:"lines"`
}

func (h *Handler) CreateQuotation(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createQuotationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.BranchID == "" || req.CustomerID == "" || len(req.Lines) == 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id, customer_id, and at least one line are required")
		return
	}
	for _, l := range req.Lines {
		if l.VariantID == "" || l.Quantity <= 0 {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "each line needs a variant_id and a positive quantity")
			return
		}
	}
	validUntil := req.ValidUntil
	if validUntil == "" {
		validUntil = time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", validUntil); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "valid_until must be YYYY-MM-DD")
		return
	}

	branchPrefix := req.BranchID
	if len(branchPrefix) > 8 {
		branchPrefix = branchPrefix[:8]
	}
	quoteNumber := fmt.Sprintf("Q-%s-%d", branchPrefix, time.Now().UnixNano())

	var resp quotationResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var customerExists string
		if err := tx.QueryRow(ctx, `SELECT id FROM customers WHERE id = $1`, req.CustomerID).Scan(&customerExists); err != nil {
			return err
		}

		var quotationID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO quotations (id, merchant_id, branch_id, customer_id, quote_number, status, valid_until, notes, created_by)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, 'draft', $4, $5, $6)
			RETURNING id`,
			req.BranchID, req.CustomerID, quoteNumber, validUntil, req.Notes, claims.UserID,
		).Scan(&quotationID); err != nil {
			return err
		}

		var subtotal, taxTotal, grandTotal float64
		for _, l := range req.Lines {
			var cgst, sgst, igst, cess float64
			var sku, productName string
			if err := tx.QueryRow(ctx, `
				SELECT pv.sku, p.name, COALESCE(ts.cgst_rate,0), COALESCE(ts.sgst_rate,0),
				       COALESCE(ts.igst_rate,0), COALESCE(ts.cess_rate,0)
				FROM product_variants pv
				JOIN products p ON p.id = pv.product_id
				LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
				WHERE pv.id = $1`, l.VariantID,
			).Scan(&sku, &productName, &cgst, &sgst, &igst, &cess); err != nil {
				return err
			}

			var unitPrice float64
			if l.UnitPrice != nil {
				unitPrice = *l.UnitPrice
			} else {
				priceStr, err := pricing.ResolvePrice(ctx, tx, req.CustomerID, l.VariantID)
				if err != nil {
					return err
				}
				unitPrice, err = strconv.ParseFloat(priceStr, 64)
				if err != nil {
					return err
				}
			}

			lineSubtotal := unitPrice * l.Quantity
			taxAmount := lineSubtotal * (cgst + sgst + igst + cess) / 100
			lineTotal := lineSubtotal + taxAmount
			subtotal += lineSubtotal
			taxTotal += taxAmount
			grandTotal += lineTotal

			if _, err := tx.Exec(ctx, `
				INSERT INTO quotation_lines (id, quotation_id, variant_id, quantity, unit_price, tax_amount, line_total)
				VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6)`,
				quotationID, l.VariantID, l.Quantity, unitPrice, taxAmount, lineTotal); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE quotations SET subtotal = $1, tax_total = $2, grand_total = $3 WHERE id = $4`,
			subtotal, taxTotal, grandTotal, quotationID); err != nil {
			return err
		}

		var err error
		resp, err = loadQuotation(ctx, tx, quotationID)
		return err
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "customer or a variant referenced by a line was not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create quotation")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// ---------------------------------------------------------------------
// GET /quotations?customer_id=&status=&branch_id=
// ---------------------------------------------------------------------

func (h *Handler) ListQuotations(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	customerID := r.URL.Query().Get("customer_id")
	status := r.URL.Query().Get("status")
	branchID := r.URL.Query().Get("branch_id")

	quotes := []quotationSummary{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			WITH params AS (
				SELECT NULLIF($1,'')::uuid AS customer_id, NULLIF($2,'') AS status, NULLIF($3,'')::uuid AS branch_id
			)
			SELECT q.id::text, q.quote_number, q.customer_id::text, q.status, q.valid_until::text,
			       q.grand_total::text, q.created_at::text
			FROM quotations q
			CROSS JOIN params
			WHERE (params.customer_id IS NULL OR q.customer_id = params.customer_id)
			  AND (params.status IS NULL OR q.status = params.status)
			  AND (params.branch_id IS NULL OR q.branch_id = params.branch_id)
			ORDER BY q.created_at DESC`, customerID, status, branchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var q quotationSummary
			if err := rows.Scan(&q.QuotationID, &q.QuoteNumber, &q.CustomerID, &q.Status, &q.ValidUntil,
				&q.GrandTotal, &q.CreatedAt); err != nil {
				return err
			}
			quotes = append(quotes, q)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list quotations")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"quotations": quotes})
}

// GetQuotation: GET /quotations/{id}
func (h *Handler) GetQuotation(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	quotationID := chi.URLParam(r, "id")

	var resp quotationResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp, err = loadQuotation(ctx, tx, quotationID)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "quotation not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load quotation")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

func loadQuotation(ctx context.Context, tx pgx.Tx, quotationID string) (quotationResponse, error) {
	var resp quotationResponse
	if err := tx.QueryRow(ctx, `
		SELECT id::text, quote_number, branch_id::text, customer_id::text, status, valid_until::text,
		       subtotal::text, tax_total::text, grand_total::text, COALESCE(notes,''),
		       converted_sales_order_id::text, created_at::text
		FROM quotations WHERE id = $1`, quotationID,
	).Scan(&resp.QuotationID, &resp.QuoteNumber, &resp.BranchID, &resp.CustomerID, &resp.Status, &resp.ValidUntil,
		&resp.Subtotal, &resp.TaxTotal, &resp.GrandTotal, &resp.Notes, &resp.ConvertedSalesOrderID, &resp.CreatedAt); err != nil {
		return resp, err
	}

	rows, err := tx.Query(ctx, `
		SELECT ql.id::text, ql.variant_id::text, pv.sku, p.name, ql.quantity::text, ql.unit_price::text,
		       ql.tax_amount::text, ql.line_total::text
		FROM quotation_lines ql
		JOIN product_variants pv ON pv.id = ql.variant_id
		JOIN products p ON p.id = pv.product_id
		WHERE ql.quotation_id = $1
		ORDER BY ql.created_at`, quotationID)
	if err != nil {
		return resp, err
	}
	defer rows.Close()
	resp.Lines = []quotationLine{}
	for rows.Next() {
		var l quotationLine
		if err := rows.Scan(&l.LineID, &l.VariantID, &l.SKU, &l.ProductName, &l.Quantity, &l.UnitPrice,
			&l.TaxAmount, &l.LineTotal); err != nil {
			return resp, err
		}
		resp.Lines = append(resp.Lines, l)
	}
	return resp, rows.Err()
}

// ---------------------------------------------------------------------
// Status transitions — draft -> sent -> accepted|rejected -> converted.
// Each is a plain conditional UPDATE; the sweeper (sweeper.go) is what
// moves an unconverted quote to 'expired' once valid_until passes.
// ---------------------------------------------------------------------

func (h *Handler) SendQuotation(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, "draft", "sent", errQuotationNotDraft, "QUOTATION_NOT_DRAFT")
}

func (h *Handler) AcceptQuotation(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, "sent", "accepted", errQuotationNotSent, "QUOTATION_NOT_SENT")
}

func (h *Handler) RejectQuotation(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, "sent", "rejected", errQuotationNotSent, "QUOTATION_NOT_SENT")
}

// DeleteQuotation: DELETE /quotations/{id} — only while still a draft.
// Found missing during a completeness audit: without this, a quote
// created by mistake had no way to be removed for up to 30 days (the
// default valid_until), since reject only applies from 'sent' and the
// expiry sweeper ignores anything not yet past its validity date. Once a
// quote has been sent, the customer may already know about it — the
// correct next step from there is `reject`, a real recorded outcome, not
// silent deletion.
func (h *Handler) DeleteQuotation(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	quotationID := chi.URLParam(r, "id")

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM quotations WHERE id = $1 AND status = 'draft'`, quotationID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists string
			if err := tx.QueryRow(ctx, `SELECT id FROM quotations WHERE id = $1`, quotationID).Scan(&exists); err != nil {
				return err
			}
			return errQuotationNotDraft
		}
		return nil
	})

	switch {
	case errors.Is(err, errQuotationNotDraft):
		httpx.Error(w, http.StatusConflict, "QUOTATION_NOT_DRAFT", "only a draft quotation can be deleted — reject it instead")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "quotation not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not delete quotation")
	default:
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func (h *Handler) transition(w http.ResponseWriter, r *http.Request, fromStatus, toStatus string, transitionErr error, errCode string) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	quotationID := chi.URLParam(r, "id")

	var resp quotationResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE quotations SET status = $1, updated_at = now() WHERE id = $2 AND status = $3`,
			toStatus, quotationID, fromStatus)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists string
			if err := tx.QueryRow(ctx, `SELECT id FROM quotations WHERE id = $1`, quotationID).Scan(&exists); err != nil {
				return err
			}
			return transitionErr
		}
		resp, err = loadQuotation(ctx, tx, quotationID)
		return err
	})

	switch {
	case errors.Is(err, transitionErr):
		httpx.Error(w, http.StatusConflict, errCode, transitionErr.Error())
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "quotation not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update quotation")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// ---------------------------------------------------------------------
// POST /quotations/{id}/convert — the one place a quotation becomes a
// real, stock-reserving sales_order. Opens a cart and adds every line
// through internal/sales' exact CreateCart/AddLineToCart functions — see
// package doc for why that's the whole point of this design.
// ---------------------------------------------------------------------

type convertRequest struct {
	POSTerminalID string `json:"pos_terminal_id"` // optional — defaults to the branch's "online" virtual terminal
}

func (h *Handler) ConvertQuotation(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	quotationID := chi.URLParam(r, "id")
	var req convertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}

	var orderID string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID, status, customerID string
		if err := tx.QueryRow(ctx, `SELECT branch_id, status, customer_id::text FROM quotations WHERE id = $1`, quotationID).
			Scan(&branchID, &status, &customerID); err != nil {
			return err
		}
		if status != "accepted" {
			return errQuotationNotAccepted
		}

		posTerminalID := req.POSTerminalID
		if posTerminalID == "" {
			if err := tx.QueryRow(ctx, `
				SELECT id FROM pos_terminals WHERE branch_id = $1 AND channel = 'online' LIMIT 1`, branchID,
			).Scan(&posTerminalID); err != nil {
				return err
			}
		}

		rows, err := tx.Query(ctx, `SELECT variant_id, quantity FROM quotation_lines WHERE quotation_id = $1`, quotationID)
		if err != nil {
			return err
		}
		type lineToAdd struct {
			variantID string
			quantity  float64
		}
		var lines []lineToAdd
		for rows.Next() {
			var l lineToAdd
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

		// Idempotent by construction, same mechanism as any other cart:
		// re-calling convert on an already-converted quote (status check
		// above already refuses it, this is a second, independent layer)
		// would return the same pre-existing cart rather than a duplicate.
		cart, err := sales.CreateCart(ctx, tx, branchID, posTerminalID, claims.UserID, customerID,
			"quotation:"+quotationID, "Q-CONV-"+quotationID)
		if err != nil {
			return err
		}
		orderID = cart.OrderID

		for _, l := range lines {
			if _, err := sales.AddLineToCart(ctx, tx, orderID, l.variantID, l.quantity); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE quotations SET status = 'converted', converted_sales_order_id = $1, updated_at = now() WHERE id = $2`,
			orderID, quotationID); err != nil {
			return err
		}
		return nil
	})

	switch {
	case errors.Is(err, errQuotationNotAccepted):
		httpx.Error(w, http.StatusConflict, "QUOTATION_NOT_ACCEPTED", "only an accepted quotation can be converted")
	case errors.Is(err, sales.ErrInsufficientStock):
		httpx.Error(w, http.StatusConflict, "STOCK_UNAVAILABLE", "one or more lines exceed available stock")
	case errors.Is(err, inventory.ErrExpiredOrUntrackedStock):
		httpx.Error(w, http.StatusConflict, "STOCK_EXPIRED", "one or more lines have no unexpired, batch-tracked stock available")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "quotation not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not convert quotation")
	default:
		httpx.JSON(w, http.StatusOK, map[string]string{"order_id": orderID, "status": "converted"})
	}
}
