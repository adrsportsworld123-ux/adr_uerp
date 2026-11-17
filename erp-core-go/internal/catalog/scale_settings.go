package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// loadScaleFormats: the merchant's saved formats, or the default when none
// is saved (configured false).
func loadScaleFormats(ctx context.Context, tx pgx.Tx) (formats []ScaleFormat, configured bool, err error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, prefix, plu_digits, value_kind, value_digits, value_decimals, active
		FROM scale_barcode_formats ORDER BY prefix`)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var f ScaleFormat
		if err := rows.Scan(&f.FormatID, &f.Prefix, &f.PLUDigits, &f.ValueKind, &f.ValueDigits, &f.ValueDecimals, &f.Active); err != nil {
			return nil, false, err
		}
		formats = append(formats, f)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(formats) == 0 {
		return []ScaleFormat{DefaultScaleFormat}, false, nil
	}
	return formats, true, nil
}

type scaleSettingsResponse struct {
	Formats []ScaleFormat `json:"formats"`
	// Configured false: none saved, so the default (Formats' one entry) is read.
	Configured bool `json:"configured"`
}

// GetScaleSettings: GET /settings/scale-barcodes — the formats a scan reads.
func (h *BarcodeHandler) GetScaleSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var resp scaleSettingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp.Formats, resp.Configured, err = loadScaleFormats(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the scale barcode formats")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// PutScaleSettings: PUT /settings/scale-barcodes {formats: [...]}
// (catalog.manage) — replaces the list; an empty list goes back to the
// default. Prefixes are unique.
func (h *BarcodeHandler) PutScaleSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		Formats []ScaleFormat `json:"formats"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if len(req.Formats) > 9 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "at most 9 formats")
		return
	}
	seen := map[string]bool{}
	for i, f := range req.Formats {
		f.Prefix = strings.TrimSpace(f.Prefix)
		req.Formats[i].Prefix = f.Prefix
		if msg := f.Validate(); msg != "" {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "format "+f.Prefix+": "+msg)
			return
		}
		if seen[f.Prefix] {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "prefix "+f.Prefix+" is listed twice")
			return
		}
		seen[f.Prefix] = true
	}
	var resp scaleSettingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, _, err := loadScaleFormats(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM scale_barcode_formats`); err != nil {
			return err
		}
		for _, f := range req.Formats {
			if _, err := tx.Exec(ctx, `
				INSERT INTO scale_barcode_formats (merchant_id, prefix, plu_digits, value_kind, value_digits, value_decimals, active, updated_by)
				VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7)`,
				f.Prefix, f.PLUDigits, f.ValueKind, f.ValueDigits, f.ValueDecimals, f.Active, claims.UserID); err != nil {
				return err
			}
		}
		if resp.Formats, resp.Configured, err = loadScaleFormats(ctx, tx); err != nil {
			return err
		}
		var merchantID string
		if err := tx.QueryRow(ctx, `SELECT current_setting('app.tenant_id')`).Scan(&merchantID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "scale_barcode_formats", EntityID: merchantID, Action: "update", PerformedBy: claims.UserID,
			Before: map[string]any{"formats": before}, After: map[string]any{"formats": resp.Formats}})
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the scale barcode formats")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
