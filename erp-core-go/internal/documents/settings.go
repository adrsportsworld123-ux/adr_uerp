package documents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/merchants"
	"erp-core-go/internal/taxes"
)

// Kind picks which of the branch's footers a document carries.
type Kind int

const (
	KindReceipt  Kind = iota // customer-facing: sale receipts
	KindPurchase             // supplier-facing: GRNs, purchase bills, purchase returns
	KindInternal             // for staff (payslips): no footer
)

const defaultReceiptFooter = "Thank you, visit again!"

// LoadLetterhead resolves a branch's letterhead inside the caller's
// tenant transaction, falling back to defaults for a branch that was
// never configured. It also returns the branch's default receipt paper.
func LoadLetterhead(ctx context.Context, tx pgx.Tx, branchID string, kind Kind) (Letterhead, Format, error) {
	var (
		lh                                       Letterhead
		legal, trade, merchantGSTIN, branchGSTIN string
		merchantAddress                          []byte
		display, address, phone, email, header   *string
		receiptFooter, purchaseFooter, logoMime  *string
		showLogo, showGSTIN, showSKU             bool
		format                                   string
		logo                                     []byte
	)
	err := tx.QueryRow(ctx, `
		SELECT m.legal_name, COALESCE(m.trade_name, ''), COALESCE(m.gstin, ''), b.name, COALESCE(b.gstin, ''),
		       ps.display_name, ps.address, ps.phone, ps.email, ps.header_text, ps.receipt_footer, ps.purchase_footer,
		       COALESCE(ps.show_logo, true), COALESCE(ps.show_gstin, true), COALESCE(ps.receipt_format, 'a4'), COALESCE(ps.show_sku, true),
		       ps.logo, ps.logo_mime, m.address
		FROM branches b
		JOIN merchants m ON m.id = b.merchant_id
		LEFT JOIN branch_print_settings ps ON ps.branch_id = b.id
		WHERE b.id = $1`, branchID,
	).Scan(&legal, &trade, &merchantGSTIN, &lh.BranchName, &branchGSTIN,
		&display, &address, &phone, &email, &header, &receiptFooter, &purchaseFooter,
		&showLogo, &showGSTIN, &format, &showSKU, &logo, &logoMime, &merchantAddress)
	if err != nil {
		return lh, FormatA4, err
	}

	lh.HideSKU = !showSKU
	lh.Name = firstNonEmpty(deref(display), trade, legal)
	lh.Address = Lines(deref(address))
	if len(lh.Address) == 0 {
		// The company profile's registered address (migrations/065) when
		// the branch's print settings give none.
		lh.Address = merchants.AddressLines(merchantAddress)
	}
	lh.Phone = strings.TrimSpace(deref(phone))
	lh.Email = strings.TrimSpace(deref(email))
	lh.Header = Lines(deref(header))
	if showGSTIN {
		lh.GSTIN = firstNonEmpty(branchGSTIN, merchantGSTIN)
	}
	if showLogo && len(logo) > 0 {
		lh.Logo, lh.LogoMime = logo, deref(logoMime)
	}
	switch kind {
	case KindReceipt:
		if receiptFooter == nil {
			lh.Footer = []string{defaultReceiptFooter}
		} else if lh.Footer = Lines(*receiptFooter); lh.Footer == nil {
			lh.Footer = []string{} // deliberately blank, not "use the default"
		}
	case KindPurchase:
		lh.Footer = Lines(deref(purchaseFooter))
	}
	if ts, err := taxes.Load(ctx, tx, branchID); err == nil {
		lh.Currency = ts.Currency()
	} else {
		return lh, FormatA4, err
	}
	return lh, ParseFormat(format, FormatA4), nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// WritePDF sends a rendered document as a download.
func WritePDF(w http.ResponseWriter, filename string, pdf []byte) {
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safeFilename(filename)))
	w.Header().Set("Cache-Control", "no-store")
	httpx.Binary(w, http.StatusOK, "application/pdf", pdf)
}

// safeFilename keeps a document number usable as a file name and a
// header value ("INV/24-25/001" → "INV-24-25-001").
func safeFilename(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '-'
	}, s)
}

// ---------------------------------------------------------------------
// Print settings API
// ---------------------------------------------------------------------

type Handler struct {
	DB *db.DB
}

type settingsResponse struct {
	BranchID       string  `json:"branch_id"`
	DisplayName    string  `json:"display_name"`
	Address        string  `json:"address"`
	Phone          string  `json:"phone"`
	Email          string  `json:"email"`
	HeaderText     string  `json:"header_text"`
	ReceiptFooter  *string `json:"receipt_footer"` // null = the default "Thank you, visit again!"
	PurchaseFooter string  `json:"purchase_footer"`
	ShowLogo       bool    `json:"show_logo"`
	ShowGSTIN      bool    `json:"show_gstin"`
	ShowSKU        bool    `json:"show_sku"` // migrations/062: the SKU under each item on receipts and invoices
	ReceiptFormat  string  `json:"receipt_format"`
	HasLogo        bool    `json:"has_logo"`
	LogoUpdatedAt  *string `json:"logo_updated_at"`
	UpdatedAt      *string `json:"updated_at"`
}

type settingsRequest struct {
	DisplayName    string  `json:"display_name"`
	Address        string  `json:"address"`
	Phone          string  `json:"phone"`
	Email          string  `json:"email"`
	HeaderText     string  `json:"header_text"`
	ReceiptFooter  *string `json:"receipt_footer"`
	PurchaseFooter string  `json:"purchase_footer"`
	ShowLogo       *bool   `json:"show_logo"`
	ShowGSTIN      *bool   `json:"show_gstin"`
	ShowSKU        *bool   `json:"show_sku"` // omitted = keep what is saved
	ReceiptFormat  string  `json:"receipt_format"`
}

var errBranchNotFound = errors.New("branch not found")

// visibleBranch: the branch exists and this session may act on it
// (migrations/044's scope). Branches are merchant-wide master data, so the
// scope check is explicit here; the settings row's own RESTRICTIVE policy
// would refuse the write anyway, but with an opaque error.
func visibleBranch(ctx context.Context, tx pgx.Tx, branchID string) error {
	var visible bool
	err := tx.QueryRow(ctx, `SELECT app_branch_visible(id) FROM branches WHERE id::text = $1`, branchID).Scan(&visible)
	if err == pgx.ErrNoRows || (err == nil && !visible) {
		return errBranchNotFound
	}
	return err
}

func loadSettings(ctx context.Context, tx pgx.Tx, branchID string) (settingsResponse, error) {
	s := settingsResponse{BranchID: branchID, ShowLogo: true, ShowGSTIN: true, ShowSKU: true, ReceiptFormat: string(FormatA4)}
	var display, address, phone, email, header, purchaseFooter *string
	err := tx.QueryRow(ctx, `
		SELECT display_name, address, phone, email, header_text, receipt_footer, purchase_footer,
		       show_logo, show_gstin, show_sku, receipt_format, logo IS NOT NULL, logo_updated_at::text, updated_at::text
		FROM branch_print_settings WHERE branch_id = $1`, branchID,
	).Scan(&display, &address, &phone, &email, &header, &s.ReceiptFooter, &purchaseFooter,
		&s.ShowLogo, &s.ShowGSTIN, &s.ShowSKU, &s.ReceiptFormat, &s.HasLogo, &s.LogoUpdatedAt, &s.UpdatedAt)
	if err == pgx.ErrNoRows {
		return s, nil // never configured: the defaults
	}
	s.DisplayName, s.Address, s.Phone, s.Email = deref(display), deref(address), deref(phone), deref(email)
	s.HeaderText, s.PurchaseFooter = deref(header), deref(purchaseFooter)
	return s, err
}

// GetSettings: GET /branches/{id}/print-settings
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := chi.URLParam(r, "id")
	var resp settingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := visibleBranch(ctx, tx, branchID); err != nil {
			return err
		}
		var err error
		resp, err = loadSettings(ctx, tx, branchID)
		return err
	})
	if errors.Is(err, errBranchNotFound) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load print settings")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// fieldLimits: generous for a letterhead, small enough that a pasted
// essay can't push the document's own content off the page.
var fieldLimits = []struct {
	name string
	max  int
	get  func(*settingsRequest) string
}{
	{"display_name", 120, func(r *settingsRequest) string { return r.DisplayName }},
	{"address", 400, func(r *settingsRequest) string { return r.Address }},
	{"phone", 60, func(r *settingsRequest) string { return r.Phone }},
	{"email", 120, func(r *settingsRequest) string { return r.Email }},
	{"header_text", 400, func(r *settingsRequest) string { return r.HeaderText }},
	{"receipt_footer", 600, func(r *settingsRequest) string { return deref(r.ReceiptFooter) }},
	{"purchase_footer", 600, func(r *settingsRequest) string { return r.PurchaseFooter }},
}

const maxTextLines = 6

func validateSettings(req *settingsRequest) string {
	for _, f := range fieldLimits {
		v := f.get(req)
		if utf8.RuneCountInString(v) > f.max {
			return fmt.Sprintf("%s must be at most %d characters", f.name, f.max)
		}
	}
	for name, v := range map[string]string{"address": req.Address, "header_text": req.HeaderText,
		"receipt_footer": deref(req.ReceiptFooter), "purchase_footer": req.PurchaseFooter} {
		if len(Lines(v)) > maxTextLines {
			return fmt.Sprintf("%s can have at most %d lines", name, maxTextLines)
		}
	}
	if req.ReceiptFormat == "" {
		req.ReceiptFormat = string(FormatA4)
	}
	if req.ReceiptFormat != string(FormatA4) && req.ReceiptFormat != string(FormatThermal) {
		return "receipt_format must be a4 or thermal"
	}
	if req.Email != "" && !strings.Contains(req.Email, "@") {
		return "email is not a valid address"
	}
	return ""
}

// UpdateSettings: PUT /branches/{id}/print-settings (branch.print_settings).
// Replaces the whole letterhead — it's one settings form — except the
// logo, which has its own endpoints. Idempotent by nature (a PUT), so a
// retried request needs no key.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := chi.URLParam(r, "id")
	var req settingsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if msg := validateSettings(&req); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg)
		return
	}
	showLogo, showGSTIN := true, true
	if req.ShowLogo != nil {
		showLogo = *req.ShowLogo
	}
	if req.ShowGSTIN != nil {
		showGSTIN = *req.ShowGSTIN
	}
	if req.ReceiptFooter != nil && strings.TrimSpace(*req.ReceiptFooter) == "" {
		// An empty receipt footer is a choice ("print nothing"), stored as
		// '' — distinct from NULL, which means the default thank-you line.
		empty := ""
		req.ReceiptFooter = &empty
	}

	var saved settingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := visibleBranch(ctx, tx, branchID); err != nil {
			return err
		}
		before, err := loadSettings(ctx, tx, branchID)
		if err != nil {
			return err
		}
		showSKU := before.ShowSKU
		if req.ShowSKU != nil {
			showSKU = *req.ShowSKU
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO branch_print_settings (branch_id, merchant_id, display_name, address, phone, email, header_text,
			    receipt_footer, purchase_footer, show_logo, show_gstin, receipt_format, updated_by, updated_at, show_sku)
			VALUES ($1, current_setting('app.tenant_id')::uuid, NULLIF($2,''), NULLIF($3,''), NULLIF($4,''), NULLIF($5,''), NULLIF($6,''),
			    $7, NULLIF($8,''), $9, $10, $11, $12, now(), $13)
			ON CONFLICT (branch_id) DO UPDATE SET
			    display_name = EXCLUDED.display_name, address = EXCLUDED.address, phone = EXCLUDED.phone,
			    email = EXCLUDED.email, header_text = EXCLUDED.header_text, receipt_footer = EXCLUDED.receipt_footer,
			    purchase_footer = EXCLUDED.purchase_footer, show_logo = EXCLUDED.show_logo, show_gstin = EXCLUDED.show_gstin,
			    receipt_format = EXCLUDED.receipt_format, updated_by = EXCLUDED.updated_by, updated_at = now(),
			    show_sku = EXCLUDED.show_sku`,
			branchID, strings.TrimSpace(req.DisplayName), strings.TrimSpace(req.Address), strings.TrimSpace(req.Phone),
			strings.TrimSpace(req.Email), strings.TrimSpace(req.HeaderText), req.ReceiptFooter,
			strings.TrimSpace(req.PurchaseFooter), showLogo, showGSTIN, req.ReceiptFormat, claims.UserID, showSKU); err != nil {
			return err
		}
		if saved, err = loadSettings(ctx, tx, branchID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "branch_print_settings", EntityID: branchID, Action: "update",
			PerformedBy: claims.UserID, Before: before, After: saved})
	})
	if errors.Is(err, errBranchNotFound) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save print settings")
		return
	}
	httpx.JSON(w, http.StatusOK, saved)
}

const (
	maxLogoUpload = 2 << 20 // what we accept over the wire
	maxLogoStored = 512 << 10
	maxLogoPixels = 2000
)

// normalizeLogo decodes the upload (PNG or JPEG only, sniffed from the
// bytes, not the client's Content-Type), bounds its dimensions, and
// re-encodes it. Re-encoding strips metadata and anything the PDF writer
// can't embed (interlaced PNGs, CMYK oddities), so a logo that saved will
// also render.
func normalizeLogo(raw []byte) ([]byte, string, string) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg") {
		return nil, "", "logo must be a PNG or JPEG image"
	}
	if cfg.Width > maxLogoPixels || cfg.Height > maxLogoPixels || cfg.Width < 16 || cfg.Height < 16 {
		return nil, "", fmt.Sprintf("logo must be between 16 and %d pixels on each side", maxLogoPixels)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", "logo could not be read"
	}
	var out bytes.Buffer
	mime := "image/png"
	if format == "jpeg" {
		mime = "image/jpeg"
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 90})
	} else {
		err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, img)
	}
	if err != nil {
		return nil, "", "logo could not be processed"
	}
	if out.Len() > maxLogoStored {
		return nil, "", "logo is too large — keep it under 512 KB (a 600×600 PNG is plenty)"
	}
	return out.Bytes(), mime, ""
}

// UploadLogo: PUT /branches/{id}/print-settings/logo (branch.print_settings).
// The body is the image itself (Content-Type image/png or image/jpeg).
func (h *Handler) UploadLogo(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := chi.URLParam(r, "id")
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxLogoUpload+1))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not read the upload")
		return
	}
	if len(raw) > maxLogoUpload {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "LOGO_TOO_LARGE", "logo upload must be under 2 MB")
		return
	}
	logo, mime, msg := normalizeLogo(raw)
	if msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_LOGO", msg)
		return
	}

	var saved settingsResponse
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := visibleBranch(ctx, tx, branchID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO branch_print_settings (branch_id, merchant_id, logo, logo_mime, logo_updated_at, updated_by, updated_at)
			VALUES ($1, current_setting('app.tenant_id')::uuid, $2, $3, now(), $4, now())
			ON CONFLICT (branch_id) DO UPDATE SET logo = EXCLUDED.logo, logo_mime = EXCLUDED.logo_mime,
			    logo_updated_at = now(), updated_by = EXCLUDED.updated_by, updated_at = now()`,
			branchID, logo, mime, claims.UserID); err != nil {
			return err
		}
		var err error
		if saved, err = loadSettings(ctx, tx, branchID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "branch_print_settings", EntityID: branchID, Action: "update",
			PerformedBy: claims.UserID, After: map[string]any{"logo": "uploaded", "logo_mime": mime, "logo_bytes": len(logo)}})
	})
	if errors.Is(err, errBranchNotFound) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save logo")
		return
	}
	httpx.JSON(w, http.StatusOK, saved)
}

// DeleteLogo: DELETE /branches/{id}/print-settings/logo (branch.print_settings).
func (h *Handler) DeleteLogo(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := chi.URLParam(r, "id")
	var saved settingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := visibleBranch(ctx, tx, branchID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE branch_print_settings SET logo = NULL, logo_mime = NULL, logo_updated_at = now(), updated_by = $2, updated_at = now()
			WHERE branch_id = $1 AND logo IS NOT NULL`, branchID, claims.UserID)
		if err != nil {
			return err
		}
		if saved, err = loadSettings(ctx, tx, branchID); err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil // nothing to remove: already the requested state
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "branch_print_settings", EntityID: branchID, Action: "update",
			PerformedBy: claims.UserID, After: map[string]any{"logo": "removed"}})
	})
	if errors.Is(err, errBranchNotFound) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not remove logo")
		return
	}
	httpx.JSON(w, http.StatusOK, saved)
}

// GetLogo: GET /branches/{id}/print-settings/logo — the image, for the
// settings screen's preview.
func (h *Handler) GetLogo(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := chi.URLParam(r, "id")
	var logo []byte
	var mime *string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := visibleBranch(ctx, tx, branchID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT logo, logo_mime FROM branch_print_settings WHERE branch_id = $1`, branchID).Scan(&logo, &mime)
	})
	if errors.Is(err, errBranchNotFound) || err == pgx.ErrNoRows || (err == nil && len(logo) == 0) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "this branch has no logo")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load logo")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.Binary(w, http.StatusOK, deref(mime), logo)
}

// Preview: GET /branches/{id}/print-settings/preview?format=a4|thermal&kind=receipt|purchase
// — a sample document under the branch's saved letterhead, so the
// settings screen can show exactly what will print before a real sale.
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := chi.URLParam(r, "id")
	kind := KindReceipt
	if r.URL.Query().Get("kind") == "purchase" {
		kind = KindPurchase
	}
	var lh Letterhead
	var def Format
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := visibleBranch(ctx, tx, branchID); err != nil {
			return err
		}
		var err error
		lh, def, err = LoadLetterhead(ctx, tx, branchID, kind)
		return err
	})
	if errors.Is(err, errBranchNotFound) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load print settings")
		return
	}
	format := ParseFormat(r.URL.Query().Get("format"), def)
	if kind == KindPurchase {
		format = FormatA4
	}
	pdf, err := Render(lh, SampleDocument(lh, kind), format)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not render preview")
		return
	}
	WritePDF(w, "preview.pdf", pdf)
}
