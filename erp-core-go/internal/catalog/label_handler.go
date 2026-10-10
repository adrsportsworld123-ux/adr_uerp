package catalog

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/printing"
)

type LabelHandler struct {
	DB *db.DB
}

const maxLabelCopies = 100

// PrintLabel: GET /products/variants/{id}/label?template=standard|compact|detailed|qr
// &copies=1..100&branch_id=&batch_id= — renders the variant's primary
// barcode as an ESC/POS print job (see internal/printing.BuildLabels).
// Requires a barcode already assigned via POST /products/variants/{id}/barcodes;
// there's deliberately no "generate one on the fly" fallback here — a
// label always reflects a real, already-committed barcode, never one
// invented just to print.
//
// Phase 13: the "detailed" template prints a batch and expiry: batch_id
// names one, otherwise branch_id picks the batch that sells next there
// (earliest expiry with stock, the same FIFO order sales use). A variant
// that isn't batch-tracked simply prints none. An unknown template is now
// a 400 instead of silently printing "standard"; an empty one still means
// "standard".
func (h *LabelHandler) PrintLabel(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	variantID := chi.URLParam(r, "id")
	q := r.URL.Query()
	template := q.Get("template")
	if template == "" {
		template = "standard"
	}
	if !printing.IsLabelTemplate(template) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "template must be one of "+strings.Join(printing.LabelTemplates, ", "))
		return
	}
	copies := 1
	if c := q.Get("copies"); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil || n < 1 || n > maxLabelCopies {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "copies must be 1 to 100")
			return
		}
		copies = n
	}
	branchID, batchID := q.Get("branch_id"), q.Get("batch_id")

	var data printing.LabelData
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT p.name, pv.sku, pv.mrp::text, pv.selling_price::text, COALESCE(b.code, ''), COALESCE(b.symbology, '')
			FROM product_variants pv
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN barcodes b ON b.variant_id = pv.id AND b.is_primary = true
			WHERE pv.id = $1`, variantID).
			Scan(&data.ProductName, &data.SKU, &data.MRP, &data.SellingPrice, &data.BarcodeCode, &data.Symbology); err != nil {
			return err
		}
		if template != "detailed" || (batchID == "" && branchID == "") {
			return nil
		}
		var expiry *string
		err := tx.QueryRow(ctx, `
			SELECT batch_no, expiry_date::text FROM stock_batches
			WHERE variant_id = $1
			  AND (($2 <> '' AND id::text = $2) OR ($2 = '' AND branch_id::text = $3 AND quantity_remaining > 0
			       AND (expiry_date IS NULL OR expiry_date >= current_date)))
			ORDER BY expiry_date NULLS LAST, received_at
			LIMIT 1`, variantID, batchID, branchID).Scan(&data.BatchNo, &expiry)
		if errors.Is(err, pgx.ErrNoRows) {
			if batchID != "" {
				return errBatchNotFound
			}
			return nil // not batch-tracked, or nothing in stock: no batch line
		}
		if expiry != nil {
			data.ExpiryDate = *expiry
		}
		return err
	})

	switch {
	case errors.Is(err, errBatchNotFound):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "that batch isn't one of this variant's")
		return
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "variant not found")
		return
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load variant")
		return
	}
	if data.BarcodeCode == "" {
		httpx.Error(w, http.StatusConflict, "NO_BARCODE", "this variant has no barcode assigned yet — POST /products/variants/{id}/barcodes first")
		return
	}

	httpx.Binary(w, http.StatusOK, "application/vnd.escpos-raw", printing.BuildLabels(template, data, copies))
}

var errBatchNotFound = errors.New("batch not found")
