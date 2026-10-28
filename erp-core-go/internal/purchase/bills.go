package purchase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/banking"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/locale"
	"erp-core-go/internal/partners"
	"erp-core-go/internal/taxes"
)

var errGRNNotCompleted = errors.New("grn must be completed before billing")
var errBillOverpaid = errors.New("payment would exceed the bill's grand total")

var errPartnerRequired = errors.New("a payment by a partner names the partner")
var errGRNAlreadyBilled = errors.New("grn already has a purchase bill")

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
	// migrations/060: the supplier's tax by head, and how the branch
	// prints it. Additive.
	CGSTAmount string      `json:"cgst_amount"`
	SGSTAmount string      `json:"sgst_amount"`
	IGSTAmount string      `json:"igst_amount"`
	CessAmount string      `json:"cess_amount"`
	InterState bool        `json:"inter_state"`
	TaxRows    []taxes.Row `json:"tax_rows"`
	// The partner funding the bill's purchase order, if any (migrations/074):
	// the payment form offers "paid by" them first.
	FundedByPartnerID *string `json:"funded_by_partner_id"`
	FundedByPartner   *string `json:"funded_by_partner"`
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
	// BillDate is the supplier invoice's date (YYYY-MM-DD, optional, not in
	// the future): the bill and its ledger entry are dated to it, so a bill
	// entered late still lands in the right month's books and GST input.
	BillDate string `json:"bill_date"`
	// RoundOff is the supplier's rounding of the invoice total (under ₹1
	// either way), so the bill equals the printed total to the paisa
	// without misstating GST. Posted to Round Off (5005).
	RoundOff float64 `json:"round_off"`
	// The supplier invoice's tax by head (migrations/060), all optional:
	// when given they must add up to tax_total (or set it, when tax_total
	// is 0); when not, tax_total is split by the supplier's GSTIN state
	// against the branch's — CGST + SGST, or IGST.
	CGSTAmount *float64 `json:"cgst_amount"`
	SGSTAmount *float64 `json:"sgst_amount"`
	IGSTAmount *float64 `json:"igst_amount"`
	CessAmount *float64 `json:"cess_amount"`
	// InterState, with no amounts given, says how tax_total splits
	// instead of the GSTIN states: true = IGST, false = CGST + SGST.
	InterState *bool `json:"inter_state"`
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
	if req.TaxTotal < 0 || math.Abs(req.RoundOff) >= 1 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "tax_total can't be negative and round_off must be under ₹1 either way")
		return
	}
	for _, v := range []*float64{req.CGSTAmount, req.SGSTAmount, req.IGSTAmount, req.CessAmount} {
		if v != nil && *v < 0 {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "cgst_amount, sgst_amount, igst_amount and cess_amount can't be negative")
			return
		}
	}
	billDate, err := pastDate(req.BillDate, locale.MerchantLocation(r.Context(), h.DB, claims.TenantID))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "bill_date: "+err.Error())
		return
	}

	var resp billResponse
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp, err = createBillTx(ctx, tx, req, claims.UserID, billDate)
		return err
	})

	switch {
	case errors.Is(err, errTaxSplitMismatch):
		httpx.Error(w, http.StatusBadRequest, "TAX_SPLIT_MISMATCH", "CGST + SGST + IGST + cess must add up to the bill's tax total")
	case errors.Is(err, accounting.ErrPeriodLocked):
		httpx.Error(w, http.StatusConflict, "PERIOD_LOCKED", "that date is in a locked accounting period")
	case errors.Is(err, errGRNNotCompleted):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "the GRN must be completed before it can be billed")
	case errors.Is(err, errGRNAlreadyBilled):
		httpx.Error(w, http.StatusConflict, "GRN_ALREADY_BILLED", "this GRN already has a purchase bill")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "GRN not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create bill")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// createBillTx bills a completed GRN inside the caller's transaction: the
// bill row and its ledger entry (Inventory, GST input and Round Off
// against the supplier payable), dated billDate (nil = today). Shared by
// CreateBill and the invoice importer (import.go).
func createBillTx(ctx context.Context, tx pgx.Tx, req createBillRequest, userID string, billDate *string) (billResponse, error) {
	var resp billResponse
	var supplierID, branchID, grnStatus string
	var subtotal, freight, otherCharges float64
	if err := tx.QueryRow(ctx, `
		SELECT supplier_id, branch_id, status, subtotal, freight_amount, other_charges
		FROM goods_receipt_notes WHERE id = $1 FOR UPDATE`, req.GRNID,
	).Scan(&supplierID, &branchID, &grnStatus, &subtotal, &freight, &otherCharges); err != nil {
		return billResponse{}, err
	}
	if grnStatus != "completed" {
		return billResponse{}, errGRNNotCompleted
	}
	// One bill per GRN: a second would book the same goods' payable twice.
	// The row lock above makes two simultaneous attempts queue here.
	var billed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM purchase_bills WHERE grn_id = $1)`, req.GRNID).Scan(&billed); err != nil {
		return billResponse{}, err
	}
	if billed {
		return billResponse{}, errGRNAlreadyBilled
	}

	split, inter, err := resolveBillTax(ctx, tx, &req, branchID, supplierID)
	if err != nil {
		return billResponse{}, err
	}

	billNumber := fmt.Sprintf("BILL-%d", time.Now().UnixNano())
	grandTotal := math.Round((subtotal+freight+otherCharges+req.TaxTotal+req.RoundOff)*100) / 100

	var dueDate *string
	if req.DueDate != "" {
		dueDate = &req.DueDate
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO purchase_bills (id, merchant_id, branch_id, supplier_id, grn_id, bill_number, supplier_invoice_number,
		                             due_date, subtotal, tax_total, freight_amount, other_charges, grand_total, bill_date,
		                             cgst_amount, sgst_amount, igst_amount, cess_amount, inter_state)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
		        COALESCE($12::date, CURRENT_DATE), $13, $14, $15, $16, $17)
		RETURNING id, bill_number, COALESCE(supplier_invoice_number,''), supplier_id, grn_id,
		          subtotal::text, tax_total::text, freight_amount::text, other_charges::text, grand_total::text, amount_paid::text, status`,
		branchID, supplierID, req.GRNID, billNumber, req.SupplierInvoiceNumber, dueDate,
		subtotal, req.TaxTotal, freight, otherCharges, grandTotal, billDate,
		split.CGST, split.SGST, split.IGST, split.Cess, inter,
	).Scan(&resp.BillID, &resp.BillNumber, &resp.SupplierInvoiceNumber, &resp.SupplierID, &resp.GRNID,
		&resp.Subtotal, &resp.TaxTotal, &resp.FreightAmount, &resp.OtherCharges, &resp.GrandTotal, &resp.AmountPaid, &resp.Status); err != nil {
		return billResponse{}, err
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
	// The payable is the printed total; whatever rounding separates it
	// from cost + tax lands in Round Off.
	if diff := math.Round((grandTotal-(subtotal+freight+otherCharges+req.TaxTotal))*100) / 100; diff > 0 {
		lines = append(lines, accounting.JournalLine{AccountCode: accounting.AccountRoundOff, Debit: diff})
	} else if diff < 0 {
		lines = append(lines, accounting.JournalLine{AccountCode: accounting.AccountRoundOff, Credit: -diff})
	}
	lines = append(lines, accounting.JournalLine{
		AccountCode: accounting.AccountPayable, Credit: grandTotal,
		PartyType: "supplier", PartyID: supplierID,
	})
	desc := "Purchase bill " + billNumber
	if req.SupplierInvoiceNumber != "" {
		desc += " (supplier invoice " + req.SupplierInvoiceNumber + ")"
	}
	if _, err := accounting.PostJournalEntryOnDate(ctx, tx, branchID, "purchase_bill", resp.BillID, desc, userID, dateOrToday(billDate, locale.Location(ctx, tx)), lines); err != nil {
		return resp, err
	}
	return resp, fillBillTax(ctx, tx, &resp)
}

// ---------------------------------------------------------------------
// POST /purchase/bills/{id}/payments
// ---------------------------------------------------------------------

type billPaymentRequest struct {
	Amount      float64 `json:"amount"`
	Method      string  `json:"method"`
	ReferenceNo string  `json:"reference_no"`
	// PaidOn: when the payment was made (YYYY-MM-DD, optional, not in the
	// future, not before the bill); the payment and its ledger entry are
	// dated to it.
	PaidOn string `json:"paid_on"`
	// BankAccountID (migrations/066): which of our bank accounts a
	// bank_transfer or cheque went from; "" = Bank (1002), as before.
	BankAccountID string `json:"bank_account_id"`
	// A cheque from a named bank account is also written on the cheque
	// register (and can be printed): the leaf (cheque_number "" = the
	// book's next one), its date (default paid_on / today) and whether
	// it is crossed "A/c Payee" (default yes).
	ChequeBookID string `json:"cheque_book_id"`
	ChequeNumber string `json:"cheque_number"`
	ChequeDate   string `json:"cheque_date"`
	AccountPayee *bool  `json:"account_payee"`
	// PartnerID (migrations/074): with method "partner", the partner who
	// paid the supplier from their own money; it adds to their capital.
	PartnerID string `json:"partner_id"`
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
	paidOn, err := pastDate(req.PaidOn, locale.MerchantLocation(r.Context(), h.DB, claims.TenantID))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "paid_on: "+err.Error())
		return
	}

	var resp billResponse
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID, supplierID string
		var grandTotal, amountPaid float64
		var billDate string
		if err := tx.QueryRow(ctx, `SELECT branch_id, supplier_id, grand_total, amount_paid, bill_date::text FROM purchase_bills WHERE id = $1`, billID).
			Scan(&branchID, &supplierID, &grandTotal, &amountPaid, &billDate); err != nil {
			return err
		}
		if paidOn != nil && *paidOn < billDate {
			return errPaidBeforeBill
		}
		if amountPaid+req.Amount > grandTotal+0.01 { // small epsilon for float rounding
			return errBillOverpaid
		}

		var ledger string
		var err error
		if req.Method == "partner" {
			// Paid by a partner from their own money (migrations/074): Cr
			// their capital instead of cash or bank.
			if req.PartnerID == "" {
				return errPartnerRequired
			}
			req.BankAccountID = ""
			if ledger, _, err = partners.Capital(ctx, tx, req.PartnerID); err != nil {
				return err
			}
		} else {
			req.PartnerID = ""
			if ledger, err = accounting.PaymentLedgerCode(ctx, tx, req.Method, req.BankAccountID); err != nil {
				return err
			}
		}
		var paymentID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO purchase_bill_payments (id, purchase_bill_id, amount, method, reference_no, performed_by, paid_at, bank_account_id, partner_id)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5,
			        COALESCE(($6::date + time '12:00') AT TIME ZONE app_merchant_tz(), now()), NULLIF($7, '')::uuid, NULLIF($8, '')::uuid)
			RETURNING id`,
			billID, req.Amount, req.Method, req.ReferenceNo, claims.UserID, paidOn, req.BankAccountID, req.PartnerID).Scan(&paymentID); err != nil {
			return err
		}
		paidDate := dateOrToday(paidOn, locale.Location(ctx, tx))
		credit := accounting.JournalLine{AccountCode: ledger, Credit: req.Amount}
		if req.BankAccountID != "" && (req.Method == "bank_transfer" || req.Method == "cheque") {
			credit.PartyType, credit.PartyID = "bank_account", req.BankAccountID
		}
		if req.Method == "partner" {
			credit.PartyType, credit.PartyID = "partner", req.PartnerID
		}
		if req.Method == "cheque" && req.BankAccountID != "" {
			chequeDate := req.ChequeDate
			if chequeDate == "" {
				chequeDate = paidDate
			}
			var payee string
			if err := tx.QueryRow(ctx, `SELECT name FROM suppliers WHERE id::text = $1`, supplierID).Scan(&payee); err != nil {
				return err
			}
			if _, err := banking.Issue(ctx, tx, banking.IssueRequest{BankAccountID: req.BankAccountID, ChequeBookID: req.ChequeBookID,
				ChequeNumber: req.ChequeNumber, ChequeDate: chequeDate, PayeeName: payee, Amount: req.Amount, AccountPayee: req.AccountPayee,
				PartyType: "supplier", PartyID: supplierID, PurchaseBillID: billID, BillPaymentID: paymentID, BranchID: branchID,
				Memo: "Bill payment"}, claims.UserID); err != nil {
				return err
			}
		}

		desc := "Payment against bill"
		if req.Method == "partner" {
			desc = "Bill paid by a partner"
		}
		entryNo, err := accounting.PostJournalEntryOnDate(ctx, tx, branchID, "bill_payment", billID, desc, claims.UserID, paidDate, []accounting.JournalLine{
			{AccountCode: accounting.AccountPayable, Debit: req.Amount, PartyType: "supplier", PartyID: supplierID},
			credit,
		})
		if err != nil {
			return err
		}
		if req.Method == "partner" {
			if err := partners.Record(ctx, tx, partners.Txn{PartnerID: req.PartnerID, Type: "paid_bill", Date: paidDate, Amount: req.Amount, Method: "own_funds",
				BranchID: branchID, Reference: req.ReferenceNo, BillPaymentID: paymentID, JournalEntryID: partners.EntryID(ctx, tx, entryNo),
				CreatedBy: claims.UserID}); err != nil {
				return err
			}
		}

		newPaid := amountPaid + req.Amount
		status := "partially_paid"
		if newPaid >= grandTotal-0.01 {
			status = "paid"
		}

		if err := tx.QueryRow(ctx, `
			UPDATE purchase_bills SET amount_paid = $1, status = $2 WHERE id = $3
			RETURNING id, bill_number, COALESCE(supplier_invoice_number,''), supplier_id, grn_id,
			          subtotal::text, tax_total::text, freight_amount::text, other_charges::text, grand_total::text, amount_paid::text, status`,
			newPaid, status, billID,
		).Scan(&resp.BillID, &resp.BillNumber, &resp.SupplierInvoiceNumber, &resp.SupplierID, &resp.GRNID,
			&resp.Subtotal, &resp.TaxTotal, &resp.FreightAmount, &resp.OtherCharges, &resp.GrandTotal, &resp.AmountPaid, &resp.Status); err != nil {
			return err
		}
		return fillBillTax(ctx, tx, &resp)
	})

	switch {
	case errors.Is(err, errPartnerRequired):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "choose the partner who paid (partner_id)")
	case errors.Is(err, partners.ErrNoPartner):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "partner not found")
	case errors.Is(err, partners.ErrPartnerRetired):
		httpx.Error(w, http.StatusConflict, "PARTNER_RETIRED", "that partner has retired")
	case errors.Is(err, errBillOverpaid):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "payment would exceed the bill's grand total")
	case errors.Is(err, errPaidBeforeBill):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "paid_on is before the bill's date")
	case errors.Is(err, accounting.ErrPeriodLocked):
		httpx.Error(w, http.StatusConflict, "PERIOD_LOCKED", "that date is in a locked accounting period")
	case errors.Is(err, accounting.ErrNoBankAccount):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "bank account not found")
	case errors.Is(err, accounting.ErrBankAccountInactive):
		httpx.Error(w, http.StatusConflict, "BANK_ACCOUNT_INACTIVE", "that bank account is inactive")
	case banking.IsClientError(err):
		banking.WriteClientError(w, err)
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
		if err := tx.QueryRow(ctx, `
			SELECT id, bill_number, COALESCE(supplier_invoice_number,''), supplier_id, grn_id,
			       subtotal::text, tax_total::text, freight_amount::text, other_charges::text, grand_total::text, amount_paid::text, status
			FROM purchase_bills WHERE id = $1`, billID,
		).Scan(&resp.BillID, &resp.BillNumber, &resp.SupplierInvoiceNumber, &resp.SupplierID, &resp.GRNID,
			&resp.Subtotal, &resp.TaxTotal, &resp.FreightAmount, &resp.OtherCharges, &resp.GrandTotal, &resp.AmountPaid, &resp.Status); err != nil {
			return err
		}
		return fillBillTax(ctx, tx, &resp)
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
