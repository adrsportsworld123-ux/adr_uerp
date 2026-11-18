package purchase

// The endpoints of the invoice reader (§3.90): each supplier's template
// (how their invoices are read; migrations/086) and the read itself,
// which turns a document into lines for the Import Invoice screen. The
// rules are in invoice_reader.go, the AI path in invoice_ai.go.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type invoiceTemplate struct {
	SupplierID string         `json:"supplier_id"`
	Supplier   string         `json:"supplier"`
	Saved      bool           `json:"saved"` // false = the built-in defaults
	Columns    invoiceColumns `json:"columns"`
	Options    invoiceOptions `json:"options"`
	Uses       int            `json:"uses"`
	LastUsedAt *time.Time     `json:"last_used_at"`
	UpdatedAt  *time.Time     `json:"updated_at"`
}

// loadInvoiceTemplate: the supplier's saved template, or the defaults.
// Columns and options come back filled in, ready to use.
func loadInvoiceTemplate(ctx context.Context, tx pgx.Tx, supplierID string) (invoiceTemplate, error) {
	t := invoiceTemplate{SupplierID: supplierID}
	if supplierID != "" {
		if err := tx.QueryRow(ctx, `SELECT name FROM suppliers WHERE id::text = $1`, supplierID).Scan(&t.Supplier); err != nil {
			return t, err
		}
		var cols, opts []byte
		err := tx.QueryRow(ctx, `
			SELECT column_map, options, uses, last_used_at, updated_at
			FROM supplier_invoice_templates WHERE supplier_id::text = $1`, supplierID).
			Scan(&cols, &opts, &t.Uses, &t.LastUsedAt, &t.UpdatedAt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return t, err
		default:
			t.Saved = true
			// A saved template that no longer reads (an option dropped in a
			// later version) falls back to the defaults for that part.
			_ = json.Unmarshal(cols, &t.Columns)
			if json.Unmarshal(opts, &t.Options) != nil {
				t.Options = invoiceOptions{}
			}
		}
	}
	t.Columns = t.Columns.withDefaults()
	if err := t.Options.normalize(); err != nil {
		t.Options = invoiceOptions{}
		_ = t.Options.normalize()
	}
	return t, nil
}

// GetInvoiceTemplate: GET /purchase/import-templates/{supplier_id}
func (h *Handler) GetInvoiceTemplate(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var t invoiceTemplate
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		t, err = loadInvoiceTemplate(ctx, tx, chi.URLParam(r, "supplier_id"))
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "supplier not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the template")
	default:
		httpx.JSON(w, http.StatusOK, t)
	}
}

type templateRequest struct {
	Columns invoiceColumns `json:"columns"`
	Options invoiceOptions `json:"options"`
}

// SaveInvoiceTemplate: PUT /purchase/import-templates/{supplier_id} — how
// this supplier's invoices are read from now on. Headings left empty mean
// the defaults. Like the supplier itself, anyone who imports can set it.
func (h *Handler) SaveInvoiceTemplate(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req templateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := req.Options.normalize(); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	cols, _ := json.Marshal(req.Columns)
	opts, _ := json.Marshal(req.Options)
	supplierID := chi.URLParam(r, "supplier_id")
	var t invoiceTemplate
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := loadInvoiceTemplate(ctx, tx, supplierID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO supplier_invoice_templates (merchant_id, supplier_id, column_map, options, updated_by, updated_at)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2, $3, $4, now())
			ON CONFLICT (merchant_id, supplier_id) DO UPDATE
			SET column_map = EXCLUDED.column_map, options = EXCLUDED.options, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			supplierID, cols, opts, claims.UserID); err != nil {
			return err
		}
		action := "update"
		var prior any = map[string]any{"columns": before.Columns, "options": before.Options}
		if !before.Saved {
			action, prior = "create", nil
		}
		if err := audit.Log(ctx, tx, audit.Entry{EntityType: "supplier", EntityID: supplierID, Action: action, PerformedBy: claims.UserID,
			Before: prior, After: map[string]any{"columns": req.Columns, "options": req.Options}, Reason: "invoice reading template"}); err != nil {
			return err
		}
		t, err = loadInvoiceTemplate(ctx, tx, supplierID)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "supplier not found")
	case err != nil:
		log.Printf("purchase: save invoice template: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the template")
	default:
		httpx.JSON(w, http.StatusOK, t)
	}
}

// ResetInvoiceTemplate: DELETE /purchase/import-templates/{supplier_id} —
// back to the defaults.
func (h *Handler) ResetInvoiceTemplate(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	supplierID := chi.URLParam(r, "supplier_id")
	var t invoiceTemplate
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := loadInvoiceTemplate(ctx, tx, supplierID)
		if err != nil {
			return err
		}
		if before.Saved {
			if _, err := tx.Exec(ctx, `DELETE FROM supplier_invoice_templates WHERE supplier_id::text = $1`, supplierID); err != nil {
				return err
			}
			if err := audit.Log(ctx, tx, audit.Entry{EntityType: "supplier", EntityID: supplierID, Action: "delete", PerformedBy: claims.UserID,
				Before: map[string]any{"columns": before.Columns, "options": before.Options}, Reason: "invoice reading template reset"}); err != nil {
				return err
			}
		}
		t, err = loadInvoiceTemplate(ctx, tx, supplierID)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "supplier not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not reset the template")
	default:
		httpx.JSON(w, http.StatusOK, t)
	}
}

// ---------------------------------------------------------------------
// POST /purchase/imports/read
// ---------------------------------------------------------------------

const (
	maxReadBody  = 24 << 20 // the pages' text, or up to a few photos (base64)
	maxReadFiles = 6
	maxFileBytes = 8 << 20
	maxDocItems  = 60000
)

type readFile struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"` // base64
}

type readRequest struct {
	SupplierID string     `json:"supplier_id"`
	Pages      []docPage  `json:"pages"` // a typed PDF's text, from the browser
	Files      []readFile `json:"files"` // photos / a scanned PDF, for the AI reader
	// Columns / Options: try these instead of the saved template, without
	// saving (the screen's "Reading rules" while they're being changed).
	Columns *invoiceColumns `json:"columns"`
	Options *invoiceOptions `json:"options"`
}

type readChecks struct {
	Lines         int    `json:"lines"`
	Quantity      string `json:"quantity"`
	LinesTotal    string `json:"lines_total"`
	PrintedQty    string `json:"printed_quantity,omitempty"`
	PrintedTotal  string `json:"printed_subtotal,omitempty"`
	InvoiceTotal  string `json:"invoice_total,omitempty"`
	Difference    string `json:"difference,omitempty"` // invoice total − lines total
	AddsUp        bool   `json:"adds_up"`              // within ₹1 of the invoice total (the rest is round-off)
	NeedAttention int    `json:"need_attention"`       // lines with a note
}

type readResponse struct {
	Source string `json:"source"` // "pdf_text" | "ai"
	// The invoice's own details, as read.
	InvoiceNumber string `json:"invoice_number"`
	InvoiceDate   string `json:"invoice_date"`
	InvoiceTotal  string `json:"invoice_total"`
	SupplierGSTIN string `json:"supplier_gstin"`
	SupplierName  string `json:"supplier_name,omitempty"`
	// SupplierID: the supplier the lines were read for — the one chosen,
	// or the one whose GSTIN is on the invoice when none was chosen.
	SupplierID   string            `json:"supplier_id"`
	SupplierNote string            `json:"supplier_note,omitempty"`
	Columns      map[string]string `json:"columns_found"`
	Lines        []readLine        `json:"lines"`
	Checks       readChecks        `json:"checks"`
	Warnings     []string          `json:"warnings"`
	CSV          string            `json:"csv"`
	Template     invoiceTemplate   `json:"template"`
	AIAvailable  bool              `json:"ai_available"`
}

type supplierRef struct{ id, name, gstin string }

// ReadInvoice: POST /purchase/imports/read — the supplier's document in,
// import lines out (nothing is saved but the template's use count). A
// typed PDF comes as its pages' text; photos and scanned PDFs as files,
// for the AI reader. The answer's csv is the import template, for the
// existing preview and commit.
func (h *Handler) ReadInvoice(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req readRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxReadBody)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not read the request (a document is limited to 16 MB)")
		return
	}
	items := 0
	for _, p := range req.Pages {
		items += len(p.Items)
	}
	if items > maxDocItems {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "the document is too long — import it in parts")
		return
	}
	var files []attachment
	if items == 0 {
		if len(req.Files) == 0 {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "send the PDF's pages, or the photos / scanned PDF")
			return
		}
		if len(req.Files) > maxReadFiles {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("up to %d photos or files at a time", maxReadFiles))
			return
		}
		for _, f := range req.Files {
			b, err := base64.StdEncoding.DecodeString(f.Data)
			if err != nil || len(b) == 0 {
				httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a file could not be read")
				return
			}
			if len(b) > maxFileBytes {
				httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("%s is larger than 8 MB", f.Name))
				return
			}
			if !aiMediaType(f.MediaType) {
				httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "photos must be JPEG, PNG or WebP, documents PDF")
				return
			}
			files = append(files, attachment{mediaType: f.MediaType, data: b})
		}
		if h.Reader == nil {
			httpx.Error(w, http.StatusServiceUnavailable, "AI_READER_UNAVAILABLE",
				"reading photos and scanned PDFs needs the AI reader, which isn't set up on this server — use a typed PDF or the CSV template")
			return
		}
	}
	if req.Options != nil {
		if err := req.Options.normalize(); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
	}

	apply := func(t invoiceTemplate) invoiceTemplate {
		if req.Columns != nil {
			t.Columns = req.Columns.withDefaults()
		}
		if req.Options != nil {
			t.Options = *req.Options
		}
		return t
	}

	// 1. What the read needs: the shop's own GSTINs and brands, the chosen
	// supplier and their template.
	var resp readResponse
	resp.AIAvailable = h.Reader != nil
	var own, brands []string
	var chosen supplierRef
	var tpl invoiceTemplate
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if own, brands, err = readContext(ctx, tx); err != nil {
			return err
		}
		if req.SupplierID != "" {
			if err := tx.QueryRow(ctx, `SELECT id::text, name, COALESCE(gstin, '') FROM suppliers WHERE id::text = $1`, req.SupplierID).
				Scan(&chosen.id, &chosen.name, &chosen.gstin); err != nil {
				return err
			}
		}
		tpl, err = loadInvoiceTemplate(ctx, tx, chosen.id)
		tpl = apply(tpl)
		return err
	})
	if err != nil {
		h.readFailed(w, err, resp)
		return
	}

	// 2. Read the document: the PDF's text here, or the AI reader (outside
	// any database transaction — it takes seconds).
	var doc rawDoc
	var readErr error
	if items > 0 {
		resp.Source = "pdf_text"
		doc, readErr = parseDocPages(req.Pages, tpl.Columns, tpl.Options)
	} else {
		resp.Source = "ai"
		if doc, readErr = h.readWithAI(r.Context(), files, tpl); readErr != nil {
			log.Printf("purchase: AI invoice read: %v", readErr)
			httpx.Error(w, http.StatusBadGateway, "AI_READ_FAILED", "the AI reader couldn't read this document — try a clearer photo, or the CSV template")
			return
		}
	}

	// 3. The supplier (found by the invoice's GSTIN when none was chosen,
	// and read again with their own template), the lines and the checks.
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		found, err := resolveReadSupplier(ctx, tx, &resp, doc, chosen, own)
		if err != nil {
			return err
		}
		if chosen.id == "" && found != "" {
			t, err := loadInvoiceTemplate(ctx, tx, found)
			if err != nil {
				return err
			}
			if t.Saved {
				tpl = apply(t)
				if items > 0 {
					doc, readErr = parseDocPages(req.Pages, tpl.Columns, tpl.Options)
				}
			} else {
				tpl.SupplierID, tpl.Supplier = t.SupplierID, t.Supplier
			}
		}
		resp.Template = tpl
		if readErr != nil {
			return readErr
		}
		return finishRead(ctx, tx, &resp, doc, tpl, brands)
	})
	if err != nil {
		h.readFailed(w, err, resp)
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (h *Handler) readFailed(w http.ResponseWriter, err error, resp readResponse) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "supplier not found")
	case errors.Is(err, errNoText):
		httpx.ErrorWithDetails(w, http.StatusUnprocessableEntity, "NO_TEXT",
			"this PDF has no text (a scan) — read it with AI, or use the CSV template", map[string]any{"ai_available": resp.AIAvailable})
	case errors.Is(err, errNoTable):
		httpx.ErrorWithDetails(w, http.StatusUnprocessableEntity, "NO_TABLE_FOUND",
			"no item table was found — set this supplier's column headings in the reading rules, or read it with AI",
			map[string]any{"ai_available": resp.AIAvailable, "template": resp.Template, "supplier_id": resp.SupplierID})
	default:
		log.Printf("purchase: read invoice: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not read the invoice")
	}
}

// activeSlabRates: the GST rates the shop has an active tax slab for, as
// the import looks them up (loadCatalogue): "5", "18".
func activeSlabRates(ctx context.Context, tx pgx.Tx) ([]string, error) {
	var rates []string
	err := eachRow(ctx, tx, `
		SELECT DISTINCT CASE WHEN cgst_rate + sgst_rate = 0 THEN igst_rate ELSE cgst_rate + sgst_rate END::float8
		FROM tax_slabs WHERE status = 'active' ORDER BY 1`, func(r pgx.Rows) error {
		var v float64
		if err := r.Scan(&v); err != nil {
			return err
		}
		rates = append(rates, qtyText(v))
		return nil
	})
	return rates, err
}

// noteSaleGST marks the lines the import would refuse as new items: no
// sale GST (an invoice line at 0%), or a rate with no active tax slab
// (12% after the slabs changed). An item already in the catalogue keeps
// its own slab, so this is a note, fixed in Sale GST % when it matters.
func noteSaleGST(lines []readLine, slabs []string) {
	have := strings.Join(slabs, "%, ") + "%"
	if len(slabs) == 0 {
		have = "none"
	}
	for i := range lines {
		l := &lines[i]
		r, ok := parseNumber(l.SaleGSTRate)
		switch {
		case l.SaleGSTRate == "":
			l.Notes = append(l.Notes, fmt.Sprintf("no sale GST %% — if this is a new item, enter the GST it is sold at (tax slabs: %s)", have))
		case ok && !contains(slabs, qtyText(r)):
			l.Notes = append(l.Notes, fmt.Sprintf("there is no active %s%% tax slab — if this is a new item, change Sale GST %% to one you have (%s), or create the slab under Catalog › Tax Slabs", qtyText(r), have))
		}
	}
}

// readContext: the shop's own GSTINs (never the supplier's) and its brands.
func readContext(ctx context.Context, tx pgx.Tx) (own, brands []string, err error) {
	if err = eachRow(ctx, tx, `
		SELECT upper(gstin) FROM merchants WHERE id = current_setting('app.tenant_id')::uuid AND COALESCE(gstin, '') <> ''
		UNION SELECT upper(gstin) FROM branches WHERE COALESCE(gstin, '') <> ''`, func(r pgx.Rows) error {
		var g string
		if err := r.Scan(&g); err != nil {
			return err
		}
		own = append(own, g)
		return nil
	}); err != nil {
		return
	}
	err = eachRow(ctx, tx, `SELECT name FROM brands WHERE status = 'active' ORDER BY length(name) DESC`, func(r pgx.Rows) error {
		var b string
		if err := r.Scan(&b); err != nil {
			return err
		}
		brands = append(brands, b)
		return nil
	})
	return
}

// resolveReadSupplier: the supplier's GSTIN on the invoice (the first
// that isn't the shop's own), the supplier it belongs to, and a note when
// it isn't the one chosen. Returns the supplier found by the GSTIN.
func resolveReadSupplier(ctx context.Context, tx pgx.Tx, resp *readResponse, doc rawDoc, chosen supplierRef, own []string) (string, error) {
	for _, g := range doc.GSTINs {
		if !contains(own, g) {
			resp.SupplierGSTIN = g
			break
		}
	}
	resp.SupplierName = doc.SupplierName
	resp.SupplierID = chosen.id
	if resp.SupplierGSTIN == "" {
		return "", nil
	}
	var found supplierRef
	err := tx.QueryRow(ctx, `
		SELECT id::text, name, COALESCE(gstin, '') FROM suppliers
		WHERE upper(gstin) = $1 ORDER BY (status = 'active') DESC, created_at LIMIT 1`, resp.SupplierGSTIN).
		Scan(&found.id, &found.name, &found.gstin)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	switch {
	case chosen.id == "" && found.id != "":
		resp.SupplierID = found.id
		resp.SupplierNote = "Supplier " + found.name + " found by the GSTIN on the invoice."
	case chosen.id == "":
		resp.SupplierNote = "No supplier has the GSTIN " + resp.SupplierGSTIN + " — choose the supplier, or add it first."
	case chosen.gstin != "" && !strings.EqualFold(chosen.gstin, resp.SupplierGSTIN):
		if found.id != "" {
			resp.SupplierNote = fmt.Sprintf("This invoice carries %s's GSTIN (%s), not %s's.", found.name, resp.SupplierGSTIN, chosen.name)
		} else {
			resp.SupplierNote = fmt.Sprintf("The GSTIN on the invoice (%s) is not %s's (%s).", resp.SupplierGSTIN, chosen.name, chosen.gstin)
		}
	}
	return found.id, nil
}

// finishRead: the lines under the template, and the checks against the
// invoice's own totals.
func finishRead(ctx context.Context, tx pgx.Tx, resp *readResponse, doc rawDoc, tpl invoiceTemplate, brands []string) error {
	resp.InvoiceNumber, resp.InvoiceDate, resp.InvoiceTotal = doc.InvoiceNumber, doc.InvoiceDate, doc.Total
	resp.Columns = doc.Columns
	resp.Lines = buildLines(doc.Rows, tpl.Options, brands)
	slabs, err := activeSlabRates(ctx, tx)
	if err != nil {
		return err
	}
	noteSaleGST(resp.Lines, slabs)
	resp.Warnings = []string{}
	resp.CSV = linesCSV(resp.Lines)

	c := readChecks{Lines: len(resp.Lines), PrintedQty: doc.SubtotalQty, PrintedTotal: doc.SubtotalTotal, InvoiceTotal: doc.Total}
	// printed: the amounts as the table prints them, for its own total
	// row; total: with GST, for the invoice's total.
	var qty, total, printed float64
	for _, l := range resp.Lines {
		q, _ := strconv.ParseFloat(l.Quantity, 64)
		t, _ := strconv.ParseFloat(l.LineTotal, 64)
		qty += q
		total += t
		if l.PrintedAmount != "" {
			t, _ = strconv.ParseFloat(l.PrintedAmount, 64)
		}
		printed += t
		if len(l.Notes) > 0 {
			c.NeedAttention++
		}
	}
	c.Quantity, c.LinesTotal = qtyText(qty), money(total)
	if len(resp.Lines) == 0 {
		resp.Warnings = append(resp.Warnings, "The item table was found but no lines could be read from it.")
	}
	if p, ok := firstNumber(doc.SubtotalQty); ok && math.Abs(p-qty) > 0.0005 {
		resp.Warnings = append(resp.Warnings, fmt.Sprintf("The lines come to %s pieces; the invoice's total row says %s.", qtyText(qty), doc.SubtotalQty))
	}
	if p, ok := firstNumber(doc.SubtotalTotal); ok && math.Abs(p-printed) > 0.005 {
		resp.Warnings = append(resp.Warnings, fmt.Sprintf("The lines add up to %s; the invoice's total row says %s.", money(printed), doc.SubtotalTotal))
	}
	if inv, ok := firstNumber(doc.Total); ok {
		d := round2(inv - total)
		c.Difference = money(d)
		c.AddsUp = math.Abs(d) < 1
		if !c.AddsUp {
			resp.Warnings = append(resp.Warnings, fmt.Sprintf("The lines add up to %s but the invoice total is %s — freight, other charges or a missed line?", money(total), doc.Total))
		}
	} else {
		resp.Warnings = append(resp.Warnings, "The invoice total wasn't found — type it in, so the import can check the lines against it.")
	}
	if doc.InvoiceNumber == "" {
		resp.Warnings = append(resp.Warnings, "The invoice number wasn't found — type it in.")
	}
	resp.Checks = c

	if tpl.Saved && resp.SupplierID != "" {
		if _, err := tx.Exec(ctx, `UPDATE supplier_invoice_templates SET uses = uses + 1, last_used_at = now() WHERE supplier_id::text = $1`,
			resp.SupplierID); err != nil {
			return err
		}
	}
	return nil
}
