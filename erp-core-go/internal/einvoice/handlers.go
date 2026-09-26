package einvoice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// cancelWindow matches the real GST rule for both documents this package
// generates: an e-invoice's IRN and an e-way bill can each only be
// cancelled within 24 hours of generation (Rule 138(9) for e-way bills;
// the equivalent NIC e-invoice API restriction for IRNs) — after that,
// the real systems simply refuse the cancel call, so this codebase
// enforces the same window rather than silently accepting a cancel a
// real GSP would reject.
const cancelWindow = 24 * time.Hour

var (
	errOrderNotFinalized     = errors.New("order is not finalized")
	errCustomerRequired      = errors.New("a customer must be attached to generate an e-invoice")
	errCustomerGSTINRequired = errors.New("the attached customer has no GSTIN on file — e-invoicing applies to B2B sales only")
	errSupplierGSTINMissing  = errors.New("no GSTIN configured for the selling branch or merchant")
	errAlreadyCancelled      = errors.New("already cancelled")
	errCancelWindowExpired   = errors.New("cancel window has expired")
)

type Handler struct {
	DB *db.DB
	// GSP is the vendor seam (gsp.go's doc comment) — every real GSP call
	// goes through this interface. Provider records which implementation
	// is currently wired, stored on every row this handler writes so a
	// later vendor switch is visible in the data.
	GSP      GSPClient
	Provider ProviderName
}

// stateCode is a GSTIN's first two characters — the GST state code.
// Duplicated from internal/gst's identical unexported helper rather than
// exported and imported: it's a three-line pure function, and this
// package has no other reason to depend on internal/gst.
func stateCode(gstin string) string {
	if len(gstin) < 2 {
		return ""
	}
	return gstin[:2]
}

type eInvoiceResponse struct {
	SalesOrderID string `json:"sales_order_id"`
	GSPProvider  string `json:"gsp_provider"`
	IRN          string `json:"irn"`
	AckNo        string `json:"ack_no"`
	AckDate      string `json:"ack_date"`
	SignedQR     string `json:"signed_qr_code"`
	Status       string `json:"status"`
}

// GenerateEInvoice: POST /sales/orders/{id}/e-invoice — idempotent: a
// second call against an order that already has a 'generated' row just
// returns it rather than calling the GSP again, since (unlike a client
// retrying an idempotency-keyed write) a real GSP charges per call and a
// cancelled IRN can never be reused (migrations/022_einvoice.sql).
func (h *Handler) GenerateEInvoice(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var resp eInvoiceResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if existing, err := loadEInvoice(ctx, tx, orderID); err == nil {
			if existing.Status == "cancelled" {
				return errAlreadyCancelled
			}
			resp = existing
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var status, orderNumber, branchGSTIN, merchantGSTIN, grandTotal string
		var customerID *string
		var finalizedAt time.Time
		if err := tx.QueryRow(ctx, `
			SELECT so.status, so.order_number, so.grand_total::text, so.customer_id, so.finalized_at,
			       COALESCE(b.gstin, ''), COALESCE(m.gstin, '')
			FROM sales_orders so
			JOIN branches b ON b.id = so.branch_id
			JOIN merchants m ON m.id = so.merchant_id
			WHERE so.id = $1`, orderID,
		).Scan(&status, &orderNumber, &grandTotal, &customerID, &finalizedAt, &branchGSTIN, &merchantGSTIN); err != nil {
			return err
		}
		if status != "finalized" {
			return errOrderNotFinalized
		}
		supplierGSTIN := branchGSTIN
		if supplierGSTIN == "" {
			supplierGSTIN = merchantGSTIN
		}
		if supplierGSTIN == "" {
			return errSupplierGSTINMissing
		}
		if customerID == nil {
			return errCustomerRequired
		}
		var buyerGSTIN, buyerName string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(gstin,''), COALESCE(name,'') FROM customers WHERE id = $1`, *customerID).
			Scan(&buyerGSTIN, &buyerName); err != nil {
			return err
		}
		if buyerGSTIN == "" {
			return errCustomerGSTINRequired
		}

		items, err := loadIRNLineItems(ctx, tx, orderID)
		if err != nil {
			return err
		}
		grandTotalFloat, err := parseMoney(grandTotal)
		if err != nil {
			return err
		}

		irnResp, err := h.GSP.GenerateIRN(ctx, IRNRequest{
			SalesOrderID:    orderID,
			SupplierGSTIN:   supplierGSTIN,
			BuyerGSTIN:      buyerGSTIN,
			BuyerLegalName:  buyerName,
			InvoiceNumber:   orderNumber,
			InvoiceDate:     finalizedAt,
			TotalInvoiceVal: grandTotalFloat,
			Items:           items,
		})
		if err != nil {
			return err
		}

		return tx.QueryRow(ctx, `
			INSERT INTO e_invoices (id, merchant_id, sales_order_id, gsp_provider, irn, ack_no, ack_date, signed_invoice, signed_qr_code, status)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, 'generated')
			RETURNING sales_order_id::text, gsp_provider, irn, ack_no, ack_date::text, signed_qr_code, status`,
			orderID, string(h.Provider), irnResp.IRN, irnResp.AckNo, irnResp.AckDate, irnResp.SignedInvoice, irnResp.SignedQRCode,
		).Scan(&resp.SalesOrderID, &resp.GSPProvider, &resp.IRN, &resp.AckNo, &resp.AckDate, &resp.SignedQR, &resp.Status)
	})

	switch {
	case errors.Is(err, errOrderNotFinalized):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "order must be finalized before generating an e-invoice")
	case errors.Is(err, errCustomerRequired):
		httpx.Error(w, http.StatusBadRequest, "EINVOICE_CUSTOMER_REQUIRED", err.Error())
	case errors.Is(err, errCustomerGSTINRequired):
		httpx.Error(w, http.StatusBadRequest, "EINVOICE_GSTIN_REQUIRED", err.Error())
	case errors.Is(err, errSupplierGSTINMissing):
		httpx.Error(w, http.StatusConflict, "EINVOICE_SUPPLIER_GSTIN_MISSING", err.Error())
	case errors.Is(err, errAlreadyCancelled):
		httpx.Error(w, http.StatusConflict, "EINVOICE_ALREADY_CANCELLED", "this order's e-invoice was already cancelled and cannot be regenerated (a cancelled IRN can never be reused)")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not generate e-invoice")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// GetEInvoice: GET /sales/orders/{id}/e-invoice
func (h *Handler) GetEInvoice(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var resp eInvoiceResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp, err = loadEInvoice(ctx, tx, orderID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no e-invoice for this order")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load e-invoice")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type cancelRequest struct {
	Reason string `json:"reason"`
}

// CancelEInvoice: POST /sales/orders/{id}/e-invoice/cancel
func (h *Handler) CancelEInvoice(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	var req cancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}

	var resp eInvoiceResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var irn, status string
		var createdAt time.Time
		if err := tx.QueryRow(ctx, `SELECT irn, status, created_at FROM e_invoices WHERE sales_order_id = $1`, orderID).
			Scan(&irn, &status, &createdAt); err != nil {
			return err
		}
		if status == "cancelled" {
			return errAlreadyCancelled
		}
		if time.Since(createdAt) > cancelWindow {
			return errCancelWindowExpired
		}
		if err := h.GSP.CancelIRN(ctx, irn, req.Reason); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			UPDATE e_invoices SET status = 'cancelled', cancel_reason = $2, cancelled_at = now()
			WHERE sales_order_id = $1
			RETURNING sales_order_id::text, gsp_provider, irn, ack_no, ack_date::text, signed_qr_code, status`,
			orderID, req.Reason,
		).Scan(&resp.SalesOrderID, &resp.GSPProvider, &resp.IRN, &resp.AckNo, &resp.AckDate, &resp.SignedQR, &resp.Status)
	})

	switch {
	case errors.Is(err, errAlreadyCancelled):
		httpx.Error(w, http.StatusConflict, "EINVOICE_ALREADY_CANCELLED", "this e-invoice was already cancelled")
	case errors.Is(err, errCancelWindowExpired):
		httpx.Error(w, http.StatusConflict, "EINVOICE_CANCEL_WINDOW_EXPIRED", "an e-invoice can only be cancelled within 24 hours of generation")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no e-invoice for this order")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not cancel e-invoice")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

func loadEInvoice(ctx context.Context, tx pgx.Tx, orderID string) (eInvoiceResponse, error) {
	var resp eInvoiceResponse
	err := tx.QueryRow(ctx, `
		SELECT sales_order_id::text, gsp_provider, COALESCE(irn,''), COALESCE(ack_no,''), COALESCE(ack_date::text,''), COALESCE(signed_qr_code,''), status
		FROM e_invoices WHERE sales_order_id = $1`, orderID,
	).Scan(&resp.SalesOrderID, &resp.GSPProvider, &resp.IRN, &resp.AckNo, &resp.AckDate, &resp.SignedQR, &resp.Status)
	return resp, err
}

func loadIRNLineItems(ctx context.Context, tx pgx.Tx, orderID string) ([]IRNLineItem, error) {
	rows, err := tx.Query(ctx, `
		SELECT COALESCE(p.hsn_code,''), p.name, sol.quantity, (sol.unit_price*sol.quantity - sol.discount_amount),
		       COALESCE(ts.cgst_rate,0), COALESCE(ts.sgst_rate,0), COALESCE(ts.igst_rate,0)
		FROM sales_order_lines sol
		JOIN product_variants pv ON pv.id = sol.variant_id
		JOIN products p ON p.id = pv.product_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		WHERE sol.sales_order_id = $1`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []IRNLineItem
	for rows.Next() {
		var it IRNLineItem
		if err := rows.Scan(&it.HSNCode, &it.Description, &it.Quantity, &it.TaxableValue, &it.CGSTRate, &it.SGSTRate, &it.IGSTRate); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func parseMoney(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}
