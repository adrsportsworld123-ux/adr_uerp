package purchase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

var errGRNNotCompleted = errors.New("grn must be completed before billing")
var errBillOverpaid = errors.New("payment would exceed the bill's grand total")

type billResponse struct {
	BillID                string `json:"bill_id"`
	BillNumber            string `json:"bill_number"`
	SupplierInvoiceNumber string `json:"supplier_invoice_number"`
	SupplierID            string `json:"supplier_id"`
	GRNID                 string `json:"grn_id"`
	Subtotal              string `json:"subtotal"`
	TaxTotal              string `json:"tax_total"`
	FreightAmount         string `json:"freight_amount"`
	OtherCharges          string `json:"other_charges"`
	GrandTotal            string `json:"grand_total"`
	AmountPaid            string `json:"amount_paid"`
	Status                string `json:"status"`
}

// ---------------------------------------------------------------------
// POST /purchase/bills — bills a completed GRN. The GRN's cost/freight
// figures become the bill's subtotal/freight/other_charges; tax_total is
// the supplier invoice's GST, which the GRN itself doesn't model (a GRN
// is about physical receipt and costing, not the supplier's tax invoice).
// ---------------------------------------------------------------------

type createBillRequest struct {
	GRNID                 string  `json:"grn_id"`
	SupplierInvoiceNumber string  `json:"supplier_invoice_number"`
	TaxTotal              float64 `json:"tax_total"`
	DueDate               string  `json:"due_date"` // YYYY-MM-DD, optional
}

func (h *Handler) CreateBill(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createBillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.GRNID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "grn_id is required")
		return
	}

	var resp billResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var supplierID, branchID, grnStatus string
		var subtotal, freight, otherCharges float64
		if err := tx.QueryRow(ctx, `
			SELECT supplier_id, branch_id, status, subtotal, freight_amount, other_charges
			FROM goods_receipt_notes WHERE id = $1`, req.GRNID,
		).Scan(&supplierID, &branchID, &grnStatus, &subtotal, &freight, &otherCharges); err != nil {
			return err
		}
		if grnStatus != "completed" {
			return errGRNNotCompleted
		}

		billNumber := fmt.Sprintf("BILL-%d", time.Now().UnixNano())
		grandTotal := subtotal + freight + otherCharges + req.TaxTotal

		var dueDate *string
		if req.DueDate != "" {
			dueDate = &req.DueDate
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO purchase_bills (id, merchant_id, branch_id, supplier_id, grn_id, bill_number, supplier_invoice_number,
			                             due_date, subtotal, tax_total, freight_amount, other_charges, grand_total)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id, bill_number, COALESCE(supplier_invoice_number,''), supplier_id, grn_id,
			          subtotal::text, tax_total::text, freight_amount::text, other_charges::text, grand_total::text, amount_paid::text, status`,
			branchID, supplierID, req.GRNID, billNumber, req.SupplierInvoiceNumber, dueDate,
			subtotal, req.TaxTotal, freight, otherCharges, grandTotal,
		).Scan(&resp.BillID, &resp.BillNumber, &resp.SupplierInvoiceNumber, &resp.SupplierID, &resp.GRNID,
			&resp.Subtotal, &resp.TaxTotal, &resp.FreightAmount, &resp.OtherCharges, &resp.GrandTotal, &resp.AmountPaid, &resp.Status); err != nil {
			return err
		}

		// Liability recognized at billing, not at GRN receipt — a GRN is a
		// physical event with no financial obligation yet; the bill is what
		// creates the payable. Landed cost (subtotal+freight+other_charges)
		// capitalizes into Inventory Asset, matching the same landed-cost
		// figure GRN completion already used for WAC.
		var lines []accounting.JournalLine
		if landedCost := subtotal + freight + otherCharges; landedCost > 0 {
			lines = append(lines, accounting.JournalLine{AccountCode: accounting.AccountInventory, Debit: landedCost})
		}
		if req.TaxTotal > 0 {
			lines = append(lines, accounting.JournalLine{AccountCode: accounting.AccountGSTInput, Debit: req.TaxTotal})
		}
		lines = append(lines, accounting.JournalLine{
			AccountCode: accounting.AccountPayable, Credit: grandTotal,
			PartyType: "supplier", PartyID: supplierID,
		})
		_, err := accounting.PostJournalEntry(ctx, tx, branchID, "purchase_bill", resp.BillID, "Purchase bill "+billNumber, claims.UserID, lines)
		return err
	})

	switch {
	case errors.Is(err, errGRNNotCompleted):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "the GRN must be completed before it can be billed")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "GRN not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create bill")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// ---------------------------------------------------------------------
// POST /purchase/bills/{id}/payments
// ---------------------------------------------------------------------

type billPaymentRequest struct {
	Amount      float64 `json:"amount"`
	Method      string  `json:"method"`
	ReferenceNo string  `json:"reference_no"`
}

func (h *Handler) RecordBillPayment(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	billID := chi.URLParam(r, "id")

	var req billPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Amount <= 0 || req.Method == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a positive amount and method are required")
		return
	}

	var resp billResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID, supplierID string
		var grandTotal, amountPaid float64
		if err := tx.QueryRow(ctx, `SELECT branch_id, supplier_id, grand_total, amount_paid FROM purchase_bills WHERE id = $1`, billID).
			Scan(&branchID, &supplierID, &grandTotal, &amountPaid); err != nil {
			return err
		}
		if amountPaid+req.Amount > grandTotal+0.01 { // small epsilon for float rounding
			return errBillOverpaid
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO purchase_bill_payments (id, purchase_bill_id, amount, method, reference_no, performed_by)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)`,
			billID, req.Amount, req.Method, req.ReferenceNo, claims.UserID); err != nil {
			return err
		}

		if _, err := accounting.PostJournalEntry(ctx, tx, branchID, "bill_payment", billID, "Payment against bill", claims.UserID, []accounting.JournalLine{
			{AccountCode: accounting.AccountPayable, Debit: req.Amount, PartyType: "supplier", PartyID: supplierID},
			{AccountCode: accounting.AccountCodeForPaymentMethod(req.Method), Credit: req.Amount},
		}); err != nil {
			return err
		}

		newPaid := amountPaid + req.Amount
		status := "partially_paid"
		if newPaid >= grandTotal-0.01 {
			status = "paid"
		}

		return tx.QueryRow(ctx, `
			UPDATE purchase_bills SET amount_paid = $1, status = $2 WHERE id = $3
			RETURNING id, bill_number, COALESCE(supplier_invoice_number,''), supplier_id, grn_id,
			          subtotal::text, tax_total::text, freight_amount::text, other_charges::text, grand_total::text, amount_paid::text, status`,
			newPaid, status, billID,
		).Scan(&resp.BillID, &resp.BillNumber, &resp.SupplierInvoiceNumber, &resp.SupplierID, &resp.GRNID,
			&resp.Subtotal, &resp.TaxTotal, &resp.FreightAmount, &resp.OtherCharges, &resp.GrandTotal, &resp.AmountPaid, &resp.Status)
	})

	switch {
	case errors.Is(err, errBillOverpaid):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "payment would exceed the bill's grand total")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "bill not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not record payment")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

type billSummary struct {
	BillID     string `json:"bill_id"`
	BillNumber string `json:"bill_number"`
	SupplierID string `json:"supplier_id"`
	GrandTotal string `json:"grand_total"`
	AmountPaid string `json:"amount_paid"`
	Status     string `json:"status"`
	BillDate   string `json:"bill_date"`
}

// ListBills: GET /purchase/bills — optionally filtered by status/supplier
func (h *Handler) ListBills(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	status := r.URL.Query().Get("status")
	supplierID := r.URL.Query().Get("supplier_id")

	bills := []billSummary{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, bill_number, supplier_id, grand_total::text, amount_paid::text, status, bill_date::text
			FROM purchase_bills
			WHERE ($1 = '' OR status = $1) AND ($2 = '' OR supplier_id::text = $2)
			ORDER BY created_at DESC`, status, supplierID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b billSummary
			if err := rows.Scan(&b.BillID, &b.BillNumber, &b.SupplierID, &b.GrandTotal, &b.AmountPaid, &b.Status, &b.BillDate); err != nil {
				return err
			}
			bills = append(bills, b)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list bills")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"bills": bills})
}

// GetBill: GET /purchase/bills/{id}
func (h *Handler) GetBill(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	billID := chi.URLParam(r, "id")

	var resp billResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, bill_number, COALESCE(supplier_invoice_number,''), supplier_id, grn_id,
			       subtotal::text, tax_total::text, freight_amount::text, other_charges::text, grand_total::text, amount_paid::text, status
			FROM purchase_bills WHERE id = $1`, billID,
		).Scan(&resp.BillID, &resp.BillNumber, &resp.SupplierInvoiceNumber, &resp.SupplierID, &resp.GRNID,
			&resp.Subtotal, &resp.TaxTotal, &resp.FreightAmount, &resp.OtherCharges, &resp.GrandTotal, &resp.AmountPaid, &resp.Status)
	})
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "bill not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load bill")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
