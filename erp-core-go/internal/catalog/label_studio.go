package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/printing"
)

// Phase 14 label studio (pos_frd_complete.md §12): POST /labels/preview and
// POST /labels/print. Any size and set of fields (printing.LabelSpec), the
// FRD's bulk printing (many products, or a whole GRN, with copies each),
// and the three direct-thermal languages: ZPL, TSPL and ESC/POS. The
// browser's own print paths (system dialog at the exact size, an A4
// sticker sheet) draw from the preview's data, so every path prints the
// same names, prices, barcodes and batches.

type studioItem struct {
	VariantID  string `json:"variant_id"`
	Copies     int    `json:"copies"`
	BatchNo    string `json:"batch_no"`    // e.g. from a GRN line
	ExpiryDate string `json:"expiry_date"` // with batch_no, when the caller knows it
}

type studioRequest struct {
	Preset   string             `json:"preset"` // standard | compact | detailed | qr | custom
	Spec     printing.LabelSpec `json:"spec"`   // the size and fields for custom; dpi overrides a preset's
	Language string             `json:"language"`
	BranchID string             `json:"branch_id"` // for the batch that sells next, when batch_no isn't given
	Items    []studioItem       `json:"items"`

	resolved printing.LabelSpec
	data     []printing.LabelData
}

const (
	maxStudioItems  = 200
	maxStudioCopies = 2000
)

var errStudioVariant = errors.New("variant not found")

func (req *studioRequest) validate() error {
	if req.Preset == "" || req.Preset == "custom" {
		req.resolved = req.Spec
	} else {
		p, ok := printing.LabelPresets[req.Preset]
		if !ok {
			return errors.New("preset must be standard, compact, detailed, qr or custom")
		}
		req.resolved = p
		if req.Spec.DPI != 0 {
			req.resolved.DPI = req.Spec.DPI
		}
	}
	if req.resolved.DPI == 0 {
		req.resolved.DPI = 203
	}
	if err := req.resolved.Validate(); err != nil {
		return err
	}
	if len(req.Items) == 0 || len(req.Items) > maxStudioItems {
		return fmt.Errorf("choose 1 to %d items", maxStudioItems)
	}
	total := 0
	for _, it := range req.Items {
		if it.VariantID == "" || it.Copies < 1 || it.Copies > 500 {
			return errors.New("each item needs a variant_id and 1 to 500 copies")
		}
		total += it.Copies
	}
	if total > maxStudioCopies {
		return fmt.Errorf("at most %d labels per job", maxStudioCopies)
	}
	return nil
}

// load fills each item's label data: the variant's name, prices and
// primary barcode, and the batch (given, or the one that sells next).
func (req *studioRequest) load(ctx context.Context, tx pgx.Tx) error {
	req.data = make([]printing.LabelData, len(req.Items))
	for i, it := range req.Items {
		d := &req.data[i]
		if err := tx.QueryRow(ctx, `
			SELECT p.name, pv.sku, pv.mrp::text, pv.selling_price::text, COALESCE(b.code, ''), COALESCE(b.symbology, '')
			FROM product_variants pv
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN barcodes b ON b.variant_id = pv.id AND b.is_primary = true
			WHERE pv.id::text = $1`, it.VariantID).
			Scan(&d.ProductName, &d.SKU, &d.MRP, &d.SellingPrice, &d.BarcodeCode, &d.Symbology); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %s", errStudioVariant, it.VariantID)
			}
			return err
		}
		if !req.resolved.Fields.Batch {
			continue
		}
		var exp *string
		var err error
		switch {
		case it.BatchNo != "":
			d.BatchNo, d.ExpiryDate = it.BatchNo, it.ExpiryDate
			if d.ExpiryDate == "" && req.BranchID != "" {
				err = tx.QueryRow(ctx, `SELECT expiry_date::text FROM stock_batches WHERE variant_id::text = $1 AND branch_id::text = $2 AND batch_no = $3`,
					it.VariantID, req.BranchID, it.BatchNo).Scan(&exp)
			}
		case req.BranchID != "":
			err = tx.QueryRow(ctx, `
				SELECT batch_no, expiry_date::text FROM stock_batches
				WHERE variant_id::text = $1 AND branch_id::text = $2 AND quantity_remaining > 0
				  AND (expiry_date IS NULL OR expiry_date >= current_date)
				ORDER BY expiry_date NULLS LAST, received_at LIMIT 1`, it.VariantID, req.BranchID).Scan(&d.BatchNo, &exp)
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if exp != nil {
			d.ExpiryDate = *exp
		}
	}
	return nil
}

func (h *LabelHandler) studio(w http.ResponseWriter, r *http.Request) (*studioRequest, bool) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return nil, false
	}
	var req studioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return nil, false
	}
	if err := req.validate(); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return nil, false
	}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, req.load)
	if errors.Is(err, errStudioVariant) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return nil, false
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the labels")
		return nil, false
	}
	return &req, true
}

type previewLabel struct {
	VariantID    string        `json:"variant_id"`
	Copies       int           `json:"copies"`
	ProductName  string        `json:"product_name"`
	SKU          string        `json:"sku"`
	MRP          string        `json:"mrp"`
	SellingPrice string        `json:"selling_price"`
	BarcodeCode  string        `json:"barcode"`
	Symbology    string        `json:"symbology"`
	BatchNo      string        `json:"batch_no"`
	ExpiryDate   string        `json:"expiry_date"`
	Scan         printing.Scan `json:"scan"`
}

// PreviewLabels: POST /labels/preview — the resolved size and fields, and
// for each item its label data and a scannability verdict at that size and
// dpi. Any signed-in user.
func (h *LabelHandler) PreviewLabels(w http.ResponseWriter, r *http.Request) {
	req, ok := h.studio(w, r)
	if !ok {
		return
	}
	labels := make([]previewLabel, len(req.Items))
	worst := "ok"
	rank := map[string]int{"ok": 0, "none": 0, "small": 1, "fail": 2}
	for i, it := range req.Items {
		d := req.data[i]
		l := printing.Layout(req.resolved, d)
		labels[i] = previewLabel{
			VariantID: it.VariantID, Copies: it.Copies, ProductName: d.ProductName, SKU: d.SKU, MRP: d.MRP,
			SellingPrice: d.SellingPrice, BarcodeCode: d.BarcodeCode, Symbology: d.Symbology,
			BatchNo: d.BatchNo, ExpiryDate: d.ExpiryDate, Scan: l.Scan,
		}
		if rank[l.Scan.Level] > rank[worst] {
			worst = l.Scan.Level
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"spec": req.resolved, "labels": labels, "worst_scan": worst})
}

// PrintLabels: POST /labels/print — one print file for the whole job in the
// printer's own language: zpl (Zebra), tspl (TSC and most generic thermal
// label printers) or escpos. Returned as a download for the printer's raw
// port or driver; the server never connects to a printer itself. When the
// label shows a code, an item without a barcode refuses the whole job,
// naming the SKUs.
func (h *LabelHandler) PrintLabels(w http.ResponseWriter, r *http.Request) {
	req, ok := h.studio(w, r)
	if !ok {
		return
	}
	var build func(printing.LabelSpec, printing.LabelData, int) []byte
	contentType, ext := "", ""
	switch req.Language {
	case "zpl":
		build, contentType, ext = printing.BuildZPL, "application/vnd.zebra-zpl", "zpl"
	case "tspl":
		build, contentType, ext = printing.BuildTSPL, "text/plain; charset=utf-8", "prn"
	case "escpos":
		build, contentType, ext = printing.BuildCustomESCPOS, "application/vnd.escpos-raw", "bin"
	default:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "language must be zpl, tspl or escpos")
		return
	}
	if req.resolved.Fields.Code != "none" {
		var missing []string
		for _, d := range req.data {
			if d.BarcodeCode == "" {
				missing = append(missing, d.SKU)
			}
		}
		if len(missing) > 0 {
			httpx.ErrorWithDetails(w, http.StatusConflict, "NO_BARCODE", "these items have no barcode yet: "+strings.Join(missing, ", "), map[string]any{"skus": missing})
			return
		}
	}
	var out []byte
	for i, it := range req.Items {
		out = append(out, build(req.resolved, req.data[i], it.Copies)...)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="labels-%s.%s"`, time.Now().Format("20060102-150405"), ext))
	httpx.Binary(w, http.StatusOK, contentType, out)
}
