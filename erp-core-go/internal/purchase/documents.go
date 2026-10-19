package purchase

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

// Downloadable purchase documents — purchase order, GRN, purchase bill,
// purchase return — as A4 PDFs under the receiving branch's letterhead
// with its purchase footer (internal/documents). Read-only, so the same branch-scope RLS
// that hides another branch's GRN from its list hides its PDF too.

const supplierCols = `s.name, COALESCE(s.legal_name, ''), COALESCE(s.gstin, ''), COALESCE(s.phone, ''), COALESCE(s.email, '')`

func scanSupplier(s *documents.Supplier) []any {
	return []any{&s.Name, &s.LegalName, &s.GSTIN, &s.Phone, &s.Email}
}

// purchaseLines reads document lines from goods_receipt_lines or
// purchase_return_lines (the query differs only in table and key).
func purchaseLines(ctx context.Context, tx pgx.Tx, query, id string) ([]documents.PurchaseLine, error) {
	rows, err := tx.Query(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []documents.PurchaseLine
	for rows.Next() {
		var l documents.PurchaseLine
		if err := rows.Scan(&l.Name, &l.SKU, &l.BatchNo, &l.Expiry, &l.Quantity, &l.UnitCost, &l.Total); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

const grnLinesSQL = `
	SELECT p.name, pv.sku, COALESCE(gl.batch_no, ''), COALESCE(to_char(gl.expiry_date, 'Mon YYYY'), ''),
	       gl.quantity::text, gl.unit_cost::text, gl.line_total::text
	FROM goods_receipt_lines gl
	JOIN product_variants pv ON pv.id = gl.variant_id
	JOIN products p ON p.id = pv.product_id
	WHERE gl.grn_id = $1
	ORDER BY gl.created_at`

// renderPurchaseDoc is the shared tail of the three handlers: resolve the
// letterhead in the same transaction as the load, render, send.
func (h *Handler) renderPurchaseDoc(w http.ResponseWriter, r *http.Request, notFound string,
	load func(ctx context.Context, tx pgx.Tx, id string) (branchID, filename string, doc documents.Document, err error)) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var (
		doc      documents.Document
		lh       documents.Letterhead
		filename string
	)
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		branchID, name, d, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		doc, filename = d, name
		lh, _, err = documents.LoadLetterhead(ctx, tx, branchID, documents.KindPurchase)
		return err
	})
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", notFound)
		return
	} else if err != nil {
		log.Printf("purchase: build document %s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build document")
		return
	}
	pdf, err := documents.Render(lh, doc, documents.FormatA4)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not render document")
		return
	}
	documents.WritePDF(w, filename, pdf)
}

// DownloadGRN: GET /purchase/grn/{id}/pdf
func (h *Handler) DownloadGRN(w http.ResponseWriter, r *http.Request) {
	h.renderPurchaseDoc(w, r, "grn not found", func(ctx context.Context, tx pgx.Tx, id string) (string, string, documents.Document, error) {
		var in documents.GRNInput
		var branchID string
		if err := tx.QueryRow(ctx, `
			SELECT g.branch_id, g.grn_number, g.status,
			       to_char(COALESCE(g.completed_at, g.created_at) AT TIME ZONE b.timezone, 'DD Mon YYYY'),
			       COALESCE(u.name, ''), COALESCE(g.notes, ''),
			       g.subtotal::text, g.freight_amount::text, g.other_charges::text, g.grand_total::text,
			       COALESCE(po.po_number, ''), `+supplierCols+`
			FROM goods_receipt_notes g
			JOIN branches b ON b.id = g.branch_id
			JOIN suppliers s ON s.id = g.supplier_id
			LEFT JOIN users u ON u.id = g.received_by
			LEFT JOIN purchase_orders po ON po.id = g.purchase_order_id
			WHERE g.id::text = $1`, id,
		).Scan(append([]any{&branchID, &in.Number, &in.Status, &in.Date, &in.ReceivedBy, &in.Notes,
			&in.Subtotal, &in.Freight, &in.Other, &in.Grand, &in.PONumber}, scanSupplier(&in.Supplier)...)...); err != nil {
			return "", "", documents.Document{}, err
		}
		lines, err := purchaseLines(ctx, tx, grnLinesSQL, id)
		if err != nil {
			return "", "", documents.Document{}, err
		}
		in.Lines = lines
		return branchID, "grn-" + in.Number + ".pdf", documents.GRN(in), nil
	})
}

// DownloadBill: GET /purchase/bills/{id}/pdf. A bill bills one GRN, so
// its lines are that GRN's; its round-off isn't stored separately, it is
// whatever the grand total carries beyond the components.
func (h *Handler) DownloadBill(w http.ResponseWriter, r *http.Request) {
	h.renderPurchaseDoc(w, r, "bill not found", func(ctx context.Context, tx pgx.Tx, id string) (string, string, documents.Document, error) {
		var in documents.BillInput
		var branchID, grnID string
		if err := tx.QueryRow(ctx, `
			SELECT pb.branch_id, pb.grn_id, pb.bill_number, COALESCE(pb.supplier_invoice_number, ''), pb.status,
			       to_char(pb.bill_date, 'DD Mon YYYY'), COALESCE(to_char(pb.due_date, 'DD Mon YYYY'), ''), g.grn_number,
			       pb.subtotal::text, pb.tax_total::text, pb.freight_amount::text, pb.other_charges::text,
			       (pb.grand_total - pb.subtotal - pb.tax_total - pb.freight_amount - pb.other_charges)::text,
			       pb.grand_total::text, pb.amount_paid::text, (pb.grand_total - pb.amount_paid)::text,
			       COALESCE(po.po_number, ''), `+supplierCols+`
			FROM purchase_bills pb
			JOIN goods_receipt_notes g ON g.id = pb.grn_id
			JOIN suppliers s ON s.id = pb.supplier_id
			LEFT JOIN purchase_orders po ON po.id = g.purchase_order_id
			WHERE pb.id::text = $1`, id,
		).Scan(append([]any{&branchID, &grnID, &in.Number, &in.SupplierInvoice, &in.Status, &in.BillDate, &in.DueDate, &in.GRNNumber,
			&in.Subtotal, &in.Tax, &in.Freight, &in.Other, &in.RoundOff, &in.Grand, &in.Paid, &in.Balance, &in.PONumber},
			scanSupplier(&in.Supplier)...)...); err != nil {
			return "", "", documents.Document{}, err
		}
		lines, err := purchaseLines(ctx, tx, grnLinesSQL, grnID)
		if err != nil {
			return "", "", documents.Document{}, err
		}
		in.Lines = lines

		rows, err := tx.Query(ctx, `
			SELECT p.method, to_char(p.paid_at AT TIME ZONE b.timezone, 'DD Mon YYYY'), COALESCE(p.reference_no, ''), p.amount::text
			FROM purchase_bill_payments p
			JOIN purchase_bills pb ON pb.id = p.purchase_bill_id
			JOIN branches b ON b.id = pb.branch_id
			WHERE p.purchase_bill_id = $1
			ORDER BY p.paid_at`, id)
		if err != nil {
			return "", "", documents.Document{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var method, date, ref, amount string
			if err := rows.Scan(&method, &date, &ref, &amount); err != nil {
				return "", "", documents.Document{}, err
			}
			label := documents.Label(method) + " · " + date
			if ref != "" {
				label += " · " + ref
			}
			in.Payments = append(in.Payments, [2]string{label, amount})
		}
		if err := rows.Err(); err != nil {
			return "", "", documents.Document{}, err
		}
		return branchID, "purchase-bill-" + in.Number + ".pdf", documents.PurchaseBill(in), nil
	})
}

// DownloadPurchaseReturn: GET /purchase/returns/{id}/pdf — the debit note
// that goes back to the supplier with the goods.
func (h *Handler) DownloadPurchaseReturn(w http.ResponseWriter, r *http.Request) {
	h.renderPurchaseDoc(w, r, "return not found", func(ctx context.Context, tx pgx.Tx, id string) (string, string, documents.Document, error) {
		var in documents.ReturnInput
		var branchID string
		if err := tx.QueryRow(ctx, `
			SELECT pr.branch_id, pr.return_number, pr.status, to_char(pr.created_at AT TIME ZONE b.timezone, 'DD Mon YYYY'),
			       pr.reason, COALESCE(g.grn_number, ''), COALESCE(u.name, ''), pr.grand_total::text, `+supplierCols+`
			FROM purchase_returns pr
			JOIN branches b ON b.id = pr.branch_id
			JOIN suppliers s ON s.id = pr.supplier_id
			LEFT JOIN goods_receipt_notes g ON g.id = pr.grn_id
			LEFT JOIN users u ON u.id = pr.created_by
			WHERE pr.id::text = $1`, id,
		).Scan(append([]any{&branchID, &in.Number, &in.Status, &in.Date, &in.Reason, &in.GRNNumber, &in.CreatedBy, &in.Grand},
			scanSupplier(&in.Supplier)...)...); err != nil {
			return "", "", documents.Document{}, err
		}
		lines, err := purchaseLines(ctx, tx, `
			SELECT p.name, pv.sku, COALESCE(rl.batch_no, ''), '', rl.quantity::text, rl.unit_cost::text, rl.line_total::text
			FROM purchase_return_lines rl
			JOIN product_variants pv ON pv.id = rl.variant_id
			JOIN products p ON p.id = pv.product_id
			WHERE rl.purchase_return_id::text = $1
			ORDER BY p.name, pv.sku`, id)
		if err != nil {
			return "", "", documents.Document{}, err
		}
		in.Lines = lines
		return branchID, "purchase-return-" + in.Number + ".pdf", documents.PurchaseReturn(in), nil
	})
}

// DownloadPurchaseOrder: GET /purchase/orders/{id}/pdf — the order that
// goes to the supplier, with what has been received against it so far.
func (h *Handler) DownloadPurchaseOrder(w http.ResponseWriter, r *http.Request) {
	h.renderPurchaseDoc(w, r, "purchase order not found", func(ctx context.Context, tx pgx.Tx, id string) (string, string, documents.Document, error) {
		var in documents.POInput
		var branchID string
		if err := tx.QueryRow(ctx, `
			SELECT po.branch_id, po.po_number, po.status, to_char(po.order_date, 'DD Mon YYYY'),
			       COALESCE(to_char(po.expected_date, 'DD Mon YYYY'), ''), COALESCE(u.name, ''),
			       po.subtotal::text, COALESCE(po.notes, ''), COALESCE(po.close_reason, ''), `+supplierCols+`
			FROM purchase_orders po
			JOIN suppliers s ON s.id = po.supplier_id
			LEFT JOIN users u ON u.id = po.created_by
			WHERE po.id::text = $1`, id,
		).Scan(append([]any{&branchID, &in.Number, &in.Status, &in.Date, &in.ExpectedDate, &in.CreatedBy,
			&in.Subtotal, &in.Notes, &in.CloseReason}, scanSupplier(&in.Supplier)...)...); err != nil {
			return "", "", documents.Document{}, err
		}
		rows, err := tx.Query(ctx, `
			SELECT p.name, pv.sku, l.quantity::text, l.received_qty::text, l.unit_cost::text, l.line_total::text
			FROM purchase_order_lines l
			JOIN product_variants pv ON pv.id = l.variant_id
			JOIN products p ON p.id = pv.product_id
			WHERE l.po_id::text = $1
			ORDER BY l.created_at, l.id`, id)
		if err != nil {
			return "", "", documents.Document{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var l documents.POLine
			if err := rows.Scan(&l.Name, &l.SKU, &l.Quantity, &l.Received, &l.UnitCost, &l.Total); err != nil {
				return "", "", documents.Document{}, err
			}
			in.Lines = append(in.Lines, l)
		}
		if err := rows.Err(); err != nil {
			return "", "", documents.Document{}, err
		}
		return branchID, "purchase-order-" + in.Number + ".pdf", documents.PurchaseOrder(in), nil
	})
}
