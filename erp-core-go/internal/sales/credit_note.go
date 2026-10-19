package sales

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
	"erp-core-go/internal/taxes"
)

// A sales return is a GST credit note against its invoice: the tax it
// gives back is split exactly as the invoice charged it — CGST + SGST, or
// IGST for an inter-state invoice — by the invoice's own place of supply
// (migrations/060). Each returned line's cess is its share of the invoice
// line's.

type creditNoteLine struct {
	Name, SKU, HSN             string
	Quantity, UnitPrice        string
	Taxable, Tax, Refund, Rate string
	CGST, SGST, IGST, Cess     string
	taxable, tax, cess, rate   float64
}

func loadCreditNoteLines(ctx context.Context, tx pgx.Tx, returnID string) ([]creditNoteLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT COALESCE(sol.description, p.name), pv.sku, COALESCE(p.hsn_code, ''), rl.quantity::text, sol.unit_price::text,
		       rl.taxable_amount::text, rl.tax_amount::text, rl.refund_amount::text, COALESCE(sol.tax_rate, 0)::text,
		       gst_cgst(rl.tax_amount, x.cess, so.inter_state)::text, gst_sgst(rl.tax_amount, x.cess, so.inter_state)::text,
		       gst_igst(rl.tax_amount, x.cess, so.inter_state)::text, x.cess::text,
		       rl.taxable_amount, rl.tax_amount, x.cess, COALESCE(sol.tax_rate, 0)
		FROM sales_return_lines rl
		JOIN sales_returns r ON r.id = rl.sales_return_id
		JOIN sales_orders so ON so.id = r.sales_order_id
		JOIN sales_order_lines sol ON sol.id = rl.sales_order_line_id
		JOIN product_variants pv ON pv.id = rl.variant_id
		JOIN products p ON p.id = pv.product_id
		CROSS JOIN LATERAL (SELECT COALESCE(round(rl.tax_amount * sol.cess_amount / NULLIF(sol.tax_amount, 0), 2), 0)::numeric(14,2) AS cess) x
		WHERE rl.sales_return_id::text = $1
		ORDER BY sol.created_at`, returnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []creditNoteLine
	for rows.Next() {
		var l creditNoteLine
		if err := rows.Scan(&l.Name, &l.SKU, &l.HSN, &l.Quantity, &l.UnitPrice, &l.Taxable, &l.Tax, &l.Refund, &l.Rate,
			&l.CGST, &l.SGST, &l.IGST, &l.Cess, &l.taxable, &l.tax, &l.cess, &l.rate); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// returnTaxBreakup: the credit note's tax, split as its invoice was.
func returnTaxBreakup(ctx context.Context, tx pgx.Tx, returnID string) (*taxes.Breakup, []creditNoteLine, error) {
	var branchID, pos string
	var inter bool
	if err := tx.QueryRow(ctx, `
		SELECT so.branch_id::text, COALESCE(so.place_of_supply, ''), so.inter_state
		FROM sales_returns r JOIN sales_orders so ON so.id = r.sales_order_id WHERE r.id::text = $1`, returnID).
		Scan(&branchID, &pos, &inter); err != nil {
		return nil, nil, err
	}
	lines, err := loadCreditNoteLines(ctx, tx, returnID)
	if err != nil {
		return nil, nil, err
	}
	settings, err := taxes.Load(ctx, tx, branchID)
	if err != nil {
		return nil, nil, err
	}
	in := make([]taxes.Line, 0, len(lines))
	for _, l := range lines {
		in = append(in, taxes.Line{Rate: l.rate, Taxable: l.taxable, Tax: l.tax, Cess: l.cess})
	}
	b := taxes.Build(settings, inter, pos, in)
	return &b, lines, nil
}

// DownloadCreditNote: GET /sales/returns/{id}/pdf — the return as a
// CREDIT NOTE against its invoice, under the branch's letterhead: the
// goods given back, their taxable value and tax split as the invoice
// charged it, any round-off returned and the refund.
func (h *Handler) DownloadCreditNote(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	returnID := chi.URLParam(r, "id")
	var (
		in  documents.CreditNoteInput
		lh  documents.Letterhead
		def documents.Format
	)
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID string
		var customer, phone, gstin *string
		if err := tx.QueryRow(ctx, `
			SELECT r.return_number, so.order_number, so.branch_id::text,
			       to_char(r.created_at AT TIME ZONE b.timezone, 'DD Mon YYYY, HH24:MI'),
			       to_char(COALESCE(so.finalized_at, so.created_at) AT TIME ZONE b.timezone, 'DD Mon YYYY'),
			       r.reason, r.refund_method, r.subtotal::text, r.tax_amount::text, r.round_off::text, r.refund_amount::text,
			       u.name, c.name, c.phone, NULLIF(c.gstin, '')
			FROM sales_returns r
			JOIN sales_orders so ON so.id = r.sales_order_id
			JOIN branches b ON b.id = so.branch_id
			JOIN users u ON u.id = r.processed_by
			LEFT JOIN customers c ON c.id = so.customer_id
			WHERE r.id::text = $1`, returnID).Scan(&in.Number, &in.InvoiceNumber, &branchID, &in.Date, &in.InvoiceDate,
			&in.Reason, &in.RefundMethod, &in.Taxable, &in.Tax, &in.RoundOff, &in.Refund, &in.ProcessedBy, &customer, &phone, &gstin); err != nil {
			return err
		}
		in.CustomerName, in.CustomerPhone, in.CustomerGSTIN = deref(customer), deref(phone), deref(gstin)
		b, lines, err := returnTaxBreakup(ctx, tx, returnID)
		if err != nil {
			return err
		}
		in.Breakup = b
		for _, l := range lines {
			in.Lines = append(in.Lines, documents.SaleLine{Name: l.Name, SKU: l.SKU, Quantity: l.Quantity, UnitPrice: l.UnitPrice,
				Discount: "0.00", Tax: l.Tax, Total: l.Refund,
				LineTax: documents.LineTax{HSN: l.HSN, Rate: l.Rate, Taxable: l.Taxable, CGST: l.CGST, SGST: l.SGST, IGST: l.IGST}})
		}
		lh, def, err = documents.LoadLetterhead(ctx, tx, branchID, documents.KindReceipt)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "return not found")
		return
	} else if err != nil {
		log.Printf("sales: build credit note %s: %v", returnID, err)
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build credit note")
		return
	}
	pdf, err := documents.Render(lh, documents.CreditNote(lh, in), documents.ParseFormat(r.URL.Query().Get("format"), def))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not render credit note")
		return
	}
	documents.WritePDF(w, "credit-note-"+in.Number+".pdf", pdf)
}
