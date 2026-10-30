package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// SetOriginalBarcode: PUT /products/variants/{id}/original-barcode {code}
// (pricing.manage; §3.83) — the Pricing dialog's "Original barcode": the
// code printed on the product. It replaces the variant's original barcode
// (the first one, as the product list shows it), keeping its pack, or adds
// one when it has none. The system's generated code is never touched.
// The variant is touched so a till's next pull picks the new code up.
func (h *BarcodeAssignHandler) SetOriginalBarcode(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	variantID := chi.URLParam(r, "id")
	var in struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	code := strings.TrimSpace(in.Code)
	if code == "" || len(code) > 64 || strings.ContainsAny(code, " \t\r\n") {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "enter the barcode (no spaces)")
		return
	}
	symbology := "CODE128"
	if len(code) == 13 {
		if err := validateEAN13(code); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		symbology = "EAN13"
	}
	type result struct {
		Code     string  `json:"code"`
		Previous *string `json:"previous"`
	}
	var out result
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM product_variants WHERE id::text = $1)`, variantID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		var oldID, oldCode string
		err := tx.QueryRow(ctx, `SELECT id::text, code FROM barcodes WHERE variant_id::text = $1 AND source = 'original' ORDER BY created_at LIMIT 1 FOR UPDATE`,
			variantID).Scan(&oldID, &oldCode)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM barcodes WHERE variant_id::text = $1`, variantID).Scan(&count); err != nil {
				return err
			}
			if err := insertBarcode(ctx, tx, variantID, code, symbology, "original", count == 0); err != nil {
				return err
			}
		case err != nil:
			return err
		case oldCode == code:
			out = result{Code: code, Previous: &oldCode}
			return nil
		default:
			out.Previous = &oldCode
			if _, err := tx.Exec(ctx, `UPDATE barcodes SET code = $2, symbology = $3 WHERE id::text = $1`, oldID, code, symbology); err != nil {
				return err
			}
		}
		out.Code = code
		if _, err := tx.Exec(ctx, `UPDATE product_variants SET updated_at = now() WHERE id::text = $1`, variantID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "barcodes", EntityID: variantID, Action: "update", PerformedBy: claims.UserID,
			Before: map[string]any{"original_barcode": out.Previous}, After: map[string]any{"original_barcode": code}, Reason: "original barcode"})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "variant not found")
	case isUniqueViolation(err):
		httpx.Error(w, http.StatusConflict, "BARCODE_EXISTS", "this barcode is already assigned to another item")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the barcode")
	default:
		httpx.JSON(w, http.StatusOK, out)
	}
}
