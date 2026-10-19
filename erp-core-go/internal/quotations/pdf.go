package quotations

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/documents"
	"erp-core-go/internal/httpx"
)

// DownloadQuotation: GET /quotations/{id}/pdf — the quote as the A4 PDF
// the customer receives, under the branch's letterhead (internal/
// documents), in any status: a draft prints with a DRAFT stamp so it can
// be reviewed before it is sent. Read-only; branch-scope RLS hides another
// branch's quote here exactly as it does from the list.
func (h *Handler) DownloadQuotation(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")

	var (
		in documents.QuotationInput
		lh documents.Letterhead
	)
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID string
		c := &in.Customer
		if err := tx.QueryRow(ctx, `
			SELECT q.branch_id::text, q.quote_number, q.status,
			       to_char(q.created_at AT TIME ZONE b.timezone, 'DD Mon YYYY'), to_char(q.valid_until, 'DD Mon YYYY'),
			       COALESCE(u.name, ''), q.subtotal::text, q.tax_total::text, q.grand_total::text, COALESCE(q.notes, ''),
			       COALESCE(c.name, ''), COALESCE(c.company_name, ''), COALESCE(c.gstin, ''), COALESCE(c.address, ''),
			       COALESCE(c.phone, ''), COALESCE(c.email, '')
			FROM quotations q
			JOIN branches b ON b.id = q.branch_id
			JOIN customers c ON c.id = q.customer_id
			LEFT JOIN users u ON u.id = q.created_by
			WHERE q.id::text = $1`, id,
		).Scan(&branchID, &in.Number, &in.Status, &in.Date, &in.ValidUntil, &in.PreparedBy,
			&in.Subtotal, &in.Tax, &in.Grand, &in.Notes,
			&c.Name, &c.CompanyName, &c.GSTIN, &c.Address, &c.Phone, &c.Email); err != nil {
			return err
		}

		// Lines and the tax split from the same loader as the JSON, so the
		// screen and the PDF can't disagree.
		q, err := loadQuotation(ctx, tx, id)
		if err != nil {
			return err
		}
		for _, l := range q.Lines {
			in.Lines = append(in.Lines, documents.QuotationLine{Name: l.ProductName, SKU: l.SKU, Quantity: l.Quantity,
				UnitPrice: l.UnitPrice, Tax: l.TaxAmount, Total: l.LineTotal,
				LineTax: documents.LineTax{HSN: l.HSNCode, Rate: l.TaxRate, Taxable: l.TaxableValue,
					CGST: l.CGSTAmount, SGST: l.SGSTAmount, IGST: l.IGSTAmount}})
		}
		in.Breakup = q.TaxBreakup
		lh, _, err = documents.LoadLetterhead(ctx, tx, branchID, documents.KindReceipt)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "quotation not found")
		return
	} else if err != nil {
		log.Printf("quotations: build pdf %s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build document")
		return
	}
	pdf, err := documents.Render(lh, documents.Quotation(in), documents.FormatA4)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not render document")
		return
	}
	documents.WritePDF(w, "quotation-"+in.Number+".pdf", pdf)
}
