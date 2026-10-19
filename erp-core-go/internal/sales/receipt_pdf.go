package sales

import (
	"context"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/documents"
	"erp-core-go/internal/httpx"
)

// DownloadReceipt: GET /sales/orders/{id}/receipt/pdf?format=a4|thermal —
// the sale as a downloadable PDF under its branch's letterhead (logo,
// header, receipt footer; internal/documents). Built from the same
// loadReceiptData as GetReceipt and PrintReceipt, so all three agree.
// Without ?format the branch's default receipt paper is used.
func (h *Handler) DownloadReceipt(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var (
		resp receiptResponse
		lh   documents.Letterhead
		def  documents.Format
	)
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if resp, err = loadReceiptDataTx(ctx, tx, orderID); err != nil {
			return err
		}
		lh, def, err = documents.LoadLetterhead(ctx, tx, resp.BranchID, documents.KindReceipt)
		return err
	})
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
		return
	} else if err != nil {
		log.Printf("sales: build receipt pdf %s: %v", orderID, err)
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build receipt")
		return
	}

	in := documents.SaleInput{
		OrderNumber: resp.OrderNumber, Status: resp.Status, Date: resp.DateText, Cashier: resp.CashierName,
		Subtotal: resp.Subtotal, Discount: resp.DiscountTotal, Tax: resp.TaxTotal, Grand: resp.GrandTotal,
		RoundOff: resp.RoundOff, Payable: resp.PayableTotal, Breakup: resp.TaxBreakup,
	}
	if resp.CustomerName != nil {
		in.CustomerName = *resp.CustomerName
		if resp.CustomerPhone != nil {
			in.CustomerPhone = *resp.CustomerPhone
		}
		if resp.CustomerGSTIN != nil {
			in.CustomerGSTIN = *resp.CustomerGSTIN
		}
	}
	for _, l := range resp.Lines {
		in.Lines = append(in.Lines, documents.SaleLine{Name: l.ProductName, SKU: l.SKU, Quantity: l.Quantity,
			UnitPrice: l.UnitPrice, Discount: l.DiscountAmount, Tax: l.TaxAmount, Total: l.LineTotal,
			LineTax: documents.LineTax{HSN: l.HSNCode, Rate: l.TaxRate, Taxable: l.TaxableValue,
				CGST: l.CGSTAmount, SGST: l.SGSTAmount, IGST: l.IGSTAmount}})
	}
	for _, p := range resp.Payments {
		in.Payments = append(in.Payments, [2]string{p.Method, p.Amount})
	}

	format := documents.ParseFormat(r.URL.Query().Get("format"), def)
	pdf, err := documents.Render(lh, documents.SaleReceipt(lh, in), format)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not render receipt")
		return
	}
	documents.WritePDF(w, "receipt-"+resp.OrderNumber+".pdf", pdf)
}
