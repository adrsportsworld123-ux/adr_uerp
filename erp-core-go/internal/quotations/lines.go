package quotations

// Phase 10: editing a draft quotation (the gap Phase 7 named: "editing a
// draft quotation's lines after creation"). Before this, a typo in one
// line meant deleting the whole quote and re-entering it. Every edit is
// draft-only — once sent, the customer may already have the numbers, so
// the honest next step is reject + a new quote, not a silent change —
// and re-totals the quote from its lines in the same transaction.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/catalog"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/pricing"
	"erp-core-go/internal/sales"
	"erp-core-go/internal/softdelete"
	"erp-core-go/internal/taxcalc"
)

var (
	errQuotationEmpty     = errors.New("a quotation needs at least one line — delete the quotation instead")
	errValidUntilInPast   = errors.New("valid_until can't be in the past")
	errInvalidQuoteChange = errors.New("invalid change")
)

// insertQuotationLine prices and inserts one line: unitPrice when the
// caller set one, else pricing.ResolvePrice (price list or base price —
// deliberately NOT ResolveLinePriceAt: a quote valid for weeks must not
// snapshot a happy-hour price). unit_price is read in the product's own
// tax basis, exactly as sales.AddLineToCart does, so conversion bills
// the same total that was quoted.
func insertQuotationLine(ctx context.Context, tx pgx.Tx, quotationID, customerID, variantID string, quantity float64, unitPrice *float64) error {
	var priceIncludesTax, sellable bool
	var taxRate float64
	if err := tx.QueryRow(ctx, `
		SELECT p.price_includes_tax,
		       COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) + COALESCE(ts.cess_rate,0),
		       `+catalog.SellablePredicate+`
		FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		WHERE pv.id = $1`, variantID).Scan(&priceIncludesTax, &taxRate, &sellable); err != nil {
		return err
	}
	if !sellable {
		return sales.ErrProductInactive
	}

	var price float64
	if unitPrice != nil {
		price = *unitPrice
	} else {
		priceStr, err := pricing.ResolvePrice(ctx, tx, customerID, variantID)
		if err != nil {
			return err
		}
		if price, err = strconv.ParseFloat(priceStr, 64); err != nil {
			return err
		}
	}

	if err := catalog.CheckQuantity(ctx, tx, variantID, quantity); err != nil {
		return err
	}
	calc := taxcalc.ComputeLine(price, quantity, 0, taxRate, priceIncludesTax)
	_, err := tx.Exec(ctx, `
		INSERT INTO quotation_lines (id, quotation_id, variant_id, quantity, unit_price, tax_amount, line_total, price_includes_tax)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7)`,
		quotationID, variantID, quantity, price, calc.Tax, calc.Total, priceIncludesTax)
	return err
}

// recalcQuotationTotals re-totals a quote from its lines. subtotal is the
// pre-tax taxable value (line_total - tax_amount), so subtotal + tax =
// grand total for tax-inclusive and tax-exclusive lines alike.
func recalcQuotationTotals(ctx context.Context, tx pgx.Tx, quotationID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE quotations q SET
			subtotal    = COALESCE(t.subtotal, 0),
			tax_total   = COALESCE(t.tax_total, 0),
			grand_total = COALESCE(t.grand_total, 0),
			updated_at  = now()
		FROM (SELECT SUM(line_total - tax_amount) AS subtotal, SUM(tax_amount) AS tax_total, SUM(line_total) AS grand_total
		      FROM quotation_lines WHERE quotation_id = $1) t
		WHERE q.id = $1`, quotationID)
	return err
}

// lockDraft locks the quotation row for the rest of the transaction (two
// people editing one draft serialize instead of racing the totals) and
// returns its customer, or errQuotationNotDraft.
func lockDraft(ctx context.Context, tx pgx.Tx, quotationID string) (customerID string, err error) {
	var status string
	if err := tx.QueryRow(ctx, `SELECT status, customer_id::text FROM quotations WHERE id = $1 FOR UPDATE`, quotationID).
		Scan(&status, &customerID); err != nil {
		return "", err
	}
	if status != "draft" {
		return "", errQuotationNotDraft
	}
	return customerID, nil
}

// respondEdit maps every draft-edit endpoint's errors the same way.
func respondEdit(w http.ResponseWriter, err error, resp quotationResponse, notFound string) {
	if catalog.WriteQuantityError(w, err) {
		return
	}
	switch {
	case errors.Is(err, errQuotationNotDraft):
		httpx.Error(w, http.StatusConflict, "QUOTATION_NOT_DRAFT", "only a draft quotation can be edited — reject it and create a new one instead")
	case errors.Is(err, sales.ErrProductInactive):
		httpx.Error(w, http.StatusConflict, "PRODUCT_INACTIVE", "this product is inactive or discontinued and can't be quoted")
	case errors.Is(err, errQuotationEmpty):
		httpx.Error(w, http.StatusConflict, "QUOTATION_EMPTY", errQuotationEmpty.Error())
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", notFound)
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update quotation")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// AddQuotationLine: POST /quotations/{id}/lines {variant_id, quantity, unit_price?}
func (h *Handler) AddQuotationLine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createLineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.VariantID == "" || req.Quantity <= 0 || (req.UnitPrice != nil && *req.UnitPrice < 0) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "variant_id and a positive quantity are required; unit_price can't be negative")
		return
	}
	quotationID := chi.URLParam(r, "id")
	var resp quotationResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		customerID, err := lockDraft(ctx, tx, quotationID)
		if err != nil {
			return err
		}
		if err := insertQuotationLine(ctx, tx, quotationID, customerID, req.VariantID, req.Quantity, req.UnitPrice); err != nil {
			return err
		}
		if err := recalcQuotationTotals(ctx, tx, quotationID); err != nil {
			return err
		}
		resp, err = loadQuotation(ctx, tx, quotationID)
		return err
	})
	respondEdit(w, err, resp, "quotation or variant not found")
}

type updateQuotationLineRequest struct {
	Quantity  *float64 `json:"quantity"`
	UnitPrice *float64 `json:"unit_price"`
	// Reprice re-resolves the line's price now (price list or base) — for
	// a draft whose catalog price changed since the line was added.
	// Can't be combined with unit_price.
	Reprice bool `json:"reprice"`
}

// UpdateQuotationLine: PATCH /quotations/{id}/lines/{line_id}. Without
// unit_price or reprice, the line keeps its snapshotted price — same
// convention as a POS cart line's quantity change.
func (h *Handler) UpdateQuotationLine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req updateQuotationLineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	switch {
	case req.Quantity == nil && req.UnitPrice == nil && !req.Reprice:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "nothing to change: send quantity, unit_price, or reprice")
		return
	case req.Quantity != nil && *req.Quantity <= 0:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "quantity must be positive")
		return
	case req.UnitPrice != nil && *req.UnitPrice < 0:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "unit_price can't be negative")
		return
	case req.UnitPrice != nil && req.Reprice:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "send either unit_price or reprice, not both")
		return
	}

	quotationID := chi.URLParam(r, "id")
	lineID := chi.URLParam(r, "line_id")
	var resp quotationResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		customerID, err := lockDraft(ctx, tx, quotationID)
		if err != nil {
			return err
		}
		var variantID string
		var quantity, unitPrice, taxRate float64
		var inclusive bool
		if err := tx.QueryRow(ctx, `
			SELECT ql.variant_id, ql.quantity, ql.unit_price, ql.price_includes_tax,
			       COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) + COALESCE(ts.cess_rate,0)
			FROM quotation_lines ql
			JOIN product_variants pv ON pv.id = ql.variant_id
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
			WHERE ql.id = $1 AND ql.quotation_id = $2`, lineID, quotationID,
		).Scan(&variantID, &quantity, &unitPrice, &inclusive, &taxRate); err != nil {
			return err
		}
		if req.Quantity != nil {
			quantity = *req.Quantity
		}
		switch {
		case req.UnitPrice != nil:
			unitPrice = *req.UnitPrice
		case req.Reprice:
			priceStr, err := pricing.ResolvePrice(ctx, tx, customerID, variantID)
			if err != nil {
				return err
			}
			if unitPrice, err = strconv.ParseFloat(priceStr, 64); err != nil {
				return err
			}
			// A repriced line takes the product's CURRENT tax basis too,
			// since the resolved price is expressed in it.
			if err := tx.QueryRow(ctx, `SELECT p.price_includes_tax FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $1`,
				variantID).Scan(&inclusive); err != nil {
				return err
			}
		}

		calc := taxcalc.ComputeLine(unitPrice, quantity, 0, taxRate, inclusive)
		if _, err := tx.Exec(ctx, `
			UPDATE quotation_lines SET quantity = $1, unit_price = $2, tax_amount = $3, line_total = $4, price_includes_tax = $5
			WHERE id = $6`, quantity, unitPrice, calc.Tax, calc.Total, inclusive, lineID); err != nil {
			return err
		}
		if err := recalcQuotationTotals(ctx, tx, quotationID); err != nil {
			return err
		}
		resp, err = loadQuotation(ctx, tx, quotationID)
		return err
	})
	respondEdit(w, err, resp, "quotation or line not found")
}

// DeleteQuotationLine: DELETE /quotations/{id}/lines/{line_id}. Refuses
// to remove the last line (409 QUOTATION_EMPTY) — creation requires one,
// and an empty quote has nothing to send; delete the quote instead.
func (h *Handler) DeleteQuotationLine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	quotationID := chi.URLParam(r, "id")
	lineID := chi.URLParam(r, "line_id")
	var resp quotationResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := lockDraft(ctx, tx, quotationID); err != nil {
			return err
		}
		var lineCount int
		var lineExists bool
		if err := tx.QueryRow(ctx, `
			SELECT count(*), bool_or(id = $2) FROM quotation_lines WHERE quotation_id = $1`, quotationID, lineID,
		).Scan(&lineCount, &lineExists); err != nil {
			return err
		}
		if !lineExists {
			return pgx.ErrNoRows
		}
		if lineCount <= 1 {
			return errQuotationEmpty
		}
		if err := softdelete.Delete(ctx, tx, "quotation_lines", lineID, claims.UserID, ""); err != nil {
			return err
		}
		if err := recalcQuotationTotals(ctx, tx, quotationID); err != nil {
			return err
		}
		var err error
		resp, err = loadQuotation(ctx, tx, quotationID)
		return err
	})
	respondEdit(w, err, resp, "quotation or line not found")
}

// UpdateQuotation: PATCH /quotations/{id} {valid_until?, notes?} — the
// header fields of a draft.
func (h *Handler) UpdateQuotation(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		ValidUntil *string `json:"valid_until"`
		Notes      *string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.ValidUntil == nil && req.Notes == nil) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "send valid_until and/or notes")
		return
	}
	if req.ValidUntil != nil {
		if _, err := time.Parse("2006-01-02", *req.ValidUntil); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "valid_until must be YYYY-MM-DD")
			return
		}
	}

	quotationID := chi.URLParam(r, "id")
	var resp quotationResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := lockDraft(ctx, tx, quotationID); err != nil {
			return err
		}
		// "Today" is the database's date — the same current_date the
		// expiry sweeper compares valid_until against.
		var inPast bool
		if err := tx.QueryRow(ctx, `SELECT COALESCE($1::date < current_date, false)`, req.ValidUntil).Scan(&inPast); err != nil {
			return err
		}
		if inPast {
			return errInvalidQuoteChange
		}
		if _, err := tx.Exec(ctx, `
			UPDATE quotations SET valid_until = COALESCE($1::date, valid_until), notes = COALESCE($2, notes), updated_at = now()
			WHERE id = $3`, req.ValidUntil, req.Notes, quotationID); err != nil {
			return err
		}
		var err error
		resp, err = loadQuotation(ctx, tx, quotationID)
		return err
	})
	if errors.Is(err, errInvalidQuoteChange) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", errValidUntilInPast.Error())
		return
	}
	respondEdit(w, err, resp, "quotation not found")
}
