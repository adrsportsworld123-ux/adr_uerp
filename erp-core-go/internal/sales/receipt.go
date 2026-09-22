package sales

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/printing"
)

type receiptResponse struct {
	OrderNumber   string           `json:"order_number"`
	Status        string           `json:"status"`
	FinalizedAt   *string          `json:"finalized_at"`
	MerchantName  string           `json:"merchant_name"`
	MerchantGSTIN string           `json:"merchant_gstin"`
	BranchName    string           `json:"branch_name"`
	BranchGSTIN   string           `json:"branch_gstin"`
	CashierName   string           `json:"cashier_name"`
	CustomerName  *string          `json:"customer_name"`
	Lines         []orderLine      `json:"lines"`
	Subtotal      string           `json:"subtotal"`
	DiscountTotal string           `json:"discount_total"`
	TaxTotal      string           `json:"tax_total"`
	GrandTotal    string           `json:"grand_total"`
	Payments      []receiptPayment `json:"payments"`

	// Not part of the public JSON contract (GetReceipt already predates
	// notifications and other clients may depend on its exact shape) —
	// only used internally by notify_receipt.go to know where to send a
	// receipt notification.
	CustomerEmail *string `json:"-"`
	CustomerPhone *string `json:"-"`
}

type receiptPayment struct {
	Method string `json:"method"`
	Amount string `json:"amount"`
}

// GetReceipt: GET /sales/orders/{id}/receipt — the print-ready payload the
// FRD's "Barcode & Label Generation, printer integration" item needs a
// source of truth to feed. Building the actual ESC/POS driver is separate,
// hardware-dependent work (still open) — this is the structured data that
// driver (or any print pipeline) would render, assembled once here instead
// of scattered across whatever client ends up printing it.
func (h *Handler) GetReceipt(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	resp, err := loadReceiptData(r.Context(), h.DB, claims.TenantID, orderID)
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build receipt")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// PrintReceipt: GET /sales/orders/{id}/receipt/print — the actual
// printer-ready rendering of GetReceipt's payload (see
// internal/printing.BuildReceipt), closing the "printer integration"
// half of Phase 1's Barcode & Label Generation item. Deliberately shares
// loadReceiptData with GetReceipt rather than re-querying, so the JSON
// contract and the printed receipt can never drift apart.
func (h *Handler) PrintReceipt(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	resp, err := loadReceiptData(r.Context(), h.DB, claims.TenantID, orderID)
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build receipt")
		return
	}

	data := printing.ReceiptData{
		MerchantName:  resp.MerchantName,
		BranchName:    resp.BranchName,
		OrderNumber:   resp.OrderNumber,
		CashierName:   resp.CashierName,
		Subtotal:      resp.Subtotal,
		DiscountTotal: resp.DiscountTotal,
		TaxTotal:      resp.TaxTotal,
		GrandTotal:    resp.GrandTotal,
	}
	for _, l := range resp.Lines {
		data.Lines = append(data.Lines, printing.ReceiptLine{
			ProductName: l.ProductName, SKU: l.SKU, Quantity: l.Quantity, UnitPrice: l.UnitPrice, LineTotal: l.LineTotal,
		})
	}
	for _, p := range resp.Payments {
		data.Payments = append(data.Payments, printing.ReceiptPayment{Method: p.Method, Amount: p.Amount})
	}

	httpx.Binary(w, http.StatusOK, "application/vnd.escpos-raw", printing.BuildReceipt(data))
}

func loadReceiptData(ctx context.Context, database *db.DB, tenantID, orderID string) (receiptResponse, error) {
	var resp receiptResponse
	err := database.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var loadErr error
		resp, loadErr = loadReceiptDataTx(ctx, tx, orderID)
		return loadErr
	})
	return resp, err
}

// loadReceiptDataTx is loadReceiptData's tx-scoped core — split out so
// notify_receipt.go can call it from inside Checkout's own transaction
// (already inside a db.WithTenant block) instead of nesting a second one.
func loadReceiptDataTx(ctx context.Context, tx pgx.Tx, orderID string) (receiptResponse, error) {
	var resp receiptResponse
	var finalizedAt *string
	var customerName *string
	if err := tx.QueryRow(ctx, `
		SELECT so.order_number, so.status, so.finalized_at::text,
		       m.legal_name, COALESCE(m.gstin, ''), b.name, COALESCE(b.gstin, ''),
		       u.name, c.name, c.email, c.phone,
		       so.subtotal::text, so.discount_total::text, so.tax_total::text, so.grand_total::text
		FROM sales_orders so
		JOIN merchants m ON m.id = so.merchant_id
		JOIN branches b ON b.id = so.branch_id
		JOIN users u ON u.id = so.cashier_id
		LEFT JOIN customers c ON c.id = so.customer_id
		WHERE so.id = $1`, orderID,
	).Scan(&resp.OrderNumber, &resp.Status, &finalizedAt,
		&resp.MerchantName, &resp.MerchantGSTIN, &resp.BranchName, &resp.BranchGSTIN,
		&resp.CashierName, &customerName, &resp.CustomerEmail, &resp.CustomerPhone,
		&resp.Subtotal, &resp.DiscountTotal, &resp.TaxTotal, &resp.GrandTotal); err != nil {
		return resp, err
	}
	resp.FinalizedAt = finalizedAt
	resp.CustomerName = customerName

	lines, err := loadOrderLines(ctx, tx, orderID)
	if err != nil {
		return resp, err
	}
	resp.Lines = lines

	rows, err := tx.Query(ctx, `SELECT method, amount::text FROM payments WHERE sales_order_id = $1 AND status = 'captured'`, orderID)
	if err != nil {
		return resp, err
	}
	defer rows.Close()
	resp.Payments = []receiptPayment{}
	for rows.Next() {
		var p receiptPayment
		if err := rows.Scan(&p.Method, &p.Amount); err != nil {
			return resp, err
		}
		resp.Payments = append(resp.Payments, p)
	}
	return resp, rows.Err()
}
