package catalog

// Units of measure (§3.79; migrations/076). A product keeps its stock in
// one base unit; a barcode can be a pack of it (a tray of 30 eggs, a 25 kg
// bag, a 500 g packet). Stock, cost and price stay per base unit
// everywhere: a pack only decides how much one scan adds. A unit with no
// decimals (Pcs) is counted in whole numbers.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type UnitHandler struct {
	DB *db.DB
}

// Unit is one unit of measure.
type Unit struct {
	ID        string   `json:"unit_id"`
	Code      string   `json:"code"`
	Name      string   `json:"name"`
	Symbol    string   `json:"symbol"`
	Kind      string   `json:"kind"`   // count | weight | volume | length | area | pack
	Factor    *float64 `json:"factor"` // in the kind's reference unit (pcs, kg, L, m, sq m); null for a pack unit
	Decimals  int      `json:"decimals"`
	IsSystem  bool     `json:"is_system"`
	Status    string   `json:"status"`
	SortOrder int      `json:"sort_order"`
	Products  int      `json:"products"` // how many products are kept in it
}

// UnitRef is the unit a product is kept in, as other responses show it.
type UnitRef struct {
	ID       string `json:"unit_id"`
	Code     string `json:"code"`
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Decimals int    `json:"decimals"`
}

const unitSelect = `SELECT u.id::text, u.code, u.name, u.symbol, u.kind, u.factor::float8, u.decimals, u.is_system, u.status, u.sort_order,
	(SELECT count(*) FROM products p WHERE p.unit_id = u.id)::int FROM units u`

func scanUnit(row pgx.Row, u *Unit) error {
	return row.Scan(&u.ID, &u.Code, &u.Name, &u.Symbol, &u.Kind, &u.Factor, &u.Decimals, &u.IsSystem, &u.Status, &u.SortOrder, &u.Products)
}

var validKinds = map[string]bool{"count": true, "weight": true, "volume": true, "length": true, "area": true, "pack": true}

// ListUnits: GET /units — every user (a product form, the POS).
func (h *UnitHandler) ListUnits(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	out := []Unit{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, unitSelect+` ORDER BY u.status, u.sort_order, u.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var u Unit
			if err := scanUnit(rows, &u); err != nil {
				return err
			}
			out = append(out, u)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list units")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"units": out})
}

type unitInput struct {
	Code      *string  `json:"code"`
	Name      *string  `json:"name"`
	Symbol    *string  `json:"symbol"`
	Kind      *string  `json:"kind"`
	Factor    *float64 `json:"factor"`
	Decimals  *int     `json:"decimals"`
	Status    *string  `json:"status"`
	SortOrder *int     `json:"sort_order"`
}

// unitError is a refusal with its status and code (respondProductEditError answers it).
type unitError struct {
	status    int
	code, msg string
}

func (e *unitError) Error() string { return e.msg }

func unitInvalid(msg string) error {
	return &unitError{status: http.StatusBadRequest, code: "INVALID_REQUEST", msg: msg}
}

func (in unitInput) check() error {
	switch {
	case in.Code != nil && (strings.TrimSpace(*in.Code) == "" || len(*in.Code) > 10):
		return unitInvalid("code is 1 to 10 characters")
	case in.Name != nil && strings.TrimSpace(*in.Name) == "":
		return unitInvalid("name is required")
	case in.Symbol != nil && (strings.TrimSpace(*in.Symbol) == "" || len(*in.Symbol) > 10):
		return unitInvalid("symbol is 1 to 10 characters")
	case in.Kind != nil && !validKinds[*in.Kind]:
		return unitInvalid("kind must be count, weight, volume, length, area or pack")
	case in.Factor != nil && *in.Factor <= 0:
		return unitInvalid("factor must be more than 0")
	case in.Decimals != nil && (*in.Decimals < 0 || *in.Decimals > 3):
		return unitInvalid("decimals is 0 to 3")
	case in.Status != nil && *in.Status != "active" && *in.Status != "inactive":
		return unitInvalid("status must be active or inactive")
	}
	return nil
}

// CreateUnit: POST /units (units.manage) {code, name, symbol, kind, factor
// (not for a pack), decimals}.
func (h *UnitHandler) CreateUnit(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var in unitInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if in.Code == nil || in.Name == nil || in.Symbol == nil || in.Kind == nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "code, name, symbol and kind are required")
		return
	}
	if err := in.check(); err != nil {
		respondProductEditError(w, err, "")
		return
	}
	if (*in.Kind == "pack") != (in.Factor == nil) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a pack unit has no factor; every other unit needs one (how many kg, L, m … it is)")
		return
	}
	decimals, order := 0, 100
	if in.Decimals != nil {
		decimals = *in.Decimals
	}
	if in.SortOrder != nil {
		order = *in.SortOrder
	}
	var u Unit
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO units (merchant_id, code, name, symbol, kind, factor, decimals, sort_order)
			VALUES (current_setting('app.tenant_id')::uuid, upper(trim($1)), trim($2), trim($3), $4, $5, $6, $7) RETURNING id::text`,
			*in.Code, *in.Name, *in.Symbol, *in.Kind, in.Factor, decimals, order).Scan(&id); err != nil {
			if isUniqueViolation(err) {
				return &unitError{status: http.StatusConflict, code: "UNIT_EXISTS", msg: "a unit with that code already exists"}
			}
			return err
		}
		if err := scanUnit(tx.QueryRow(ctx, unitSelect+` WHERE u.id::text = $1`, id), &u); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "units", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: u})
	})
	if respondProductEditError(w, err, "could not save the unit") {
		return
	}
	httpx.JSON(w, http.StatusCreated, u)
}

// UpdateUnit: PATCH /units/{id} (units.manage) — name, symbol, decimals,
// status, order; a unit's own kind and size (and a standard unit's code)
// don't change, since products and packs are measured in them.
func (h *UnitHandler) UpdateUnit(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in unitInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := in.check(); err != nil {
		respondProductEditError(w, err, "")
		return
	}
	var u Unit
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var before Unit
		if err := scanUnit(tx.QueryRow(ctx, unitSelect+` WHERE u.id::text = $1 FOR UPDATE OF u`, id), &before); err != nil {
			return err
		}
		if (in.Kind != nil && *in.Kind != before.Kind) || (in.Factor != nil && (before.Factor == nil || *in.Factor != *before.Factor)) {
			return &unitError{status: http.StatusConflict, code: "UNIT_FIXED", msg: "a unit's kind and size can't change; add a new unit instead"}
		}
		if in.Code != nil && before.IsSystem && !strings.EqualFold(strings.TrimSpace(*in.Code), before.Code) {
			return &unitError{status: http.StatusConflict, code: "UNIT_FIXED", msg: "a standard unit keeps its code"}
		}
		if in.Status != nil && *in.Status == "inactive" && before.Code == "PCS" {
			return &unitError{status: http.StatusConflict, code: "UNIT_FIXED", msg: "Pieces is every new product's unit and stays active"}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE units SET code = COALESCE(upper(NULLIF(trim($2), '')), code), name = COALESCE(NULLIF(trim($3), ''), name),
			  symbol = COALESCE(NULLIF(trim($4), ''), symbol), decimals = COALESCE($5, decimals), status = COALESCE($6, status),
			  sort_order = COALESCE($7, sort_order)
			WHERE id::text = $1`, id, in.Code, in.Name, in.Symbol, in.Decimals, in.Status, in.SortOrder); err != nil {
			if isUniqueViolation(err) {
				return &unitError{status: http.StatusConflict, code: "UNIT_EXISTS", msg: "another unit has that code"}
			}
			return err
		}
		if err := scanUnit(tx.QueryRow(ctx, unitSelect+` WHERE u.id::text = $1`, id), &u); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "units", EntityID: id, Action: "update", PerformedBy: claims.UserID, Before: before, After: u})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "unit not found")
		return
	}
	if respondProductEditError(w, err, "could not save the unit") {
		return
	}
	httpx.JSON(w, http.StatusOK, u)
}

// ---------------------------------------------------------------------
// Whole numbers
// ---------------------------------------------------------------------

// QuantityError: a quantity finer than the product's unit is counted in.
type QuantityError struct {
	Product, Unit string
	Decimals      int
}

func (e *QuantityError) Error() string {
	if e.Decimals == 0 {
		return fmt.Sprintf("%s is counted in whole %s", e.Product, e.Unit)
	}
	return fmt.Sprintf("%s is counted to at most %d decimal place(s) of %s", e.Product, e.Decimals, e.Unit)
}

// CheckQuantity refuses a quantity of a variant finer than its product's
// unit (a fraction of a Pcs item). A product without a unit, or a variant
// that doesn't exist, passes: the caller's own lookup reports that.
func CheckQuantity(ctx context.Context, tx pgx.Tx, variantID string, qty float64) error {
	var name, unit string
	var decimals int
	err := tx.QueryRow(ctx, `
		SELECT p.name, u.name, u.decimals FROM product_variants v JOIN products p ON p.id = v.product_id JOIN units u ON u.id = p.unit_id
		WHERE v.id::text = $1`, variantID).Scan(&name, &unit, &decimals)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if !fitsDecimals(qty, decimals) {
		return &QuantityError{Product: name, Unit: strings.ToLower(unit), Decimals: decimals}
	}
	return nil
}

func fitsDecimals(qty float64, decimals int) bool {
	scaled := math.Abs(qty) * math.Pow(10, float64(decimals))
	return math.Abs(scaled-math.Round(scaled)) < 1e-6
}

// WriteQuantityError answers a QuantityError (400 QTY_UNIT); false for any other error.
func WriteQuantityError(w http.ResponseWriter, err error) bool {
	var qe *QuantityError
	if !errors.As(err, &qe) {
		return false
	}
	httpx.Error(w, http.StatusBadRequest, "QTY_UNIT", qe.Error())
	return true
}

// ---------------------------------------------------------------------
// Packs
// ---------------------------------------------------------------------

// Pack is what one scan of a barcode adds.
type Pack struct {
	Qty    string  `json:"pack_qty"`     // base units a scan adds ("1.000" = one)
	UnitID *string `json:"pack_unit_id"` // what the pack is called; null = the base unit
	Size   *string `json:"pack_size"`    // as entered: 30 (in a tray) or 500 (g)
	Label  string  `json:"pack_label"`   // "Tray of 30 pcs", "500 g", "" for one base unit
}

// packFor works out a barcode's base quantity from a pack unit and size:
// a unit of the product's own kind converts (500 g of a kg product is
// 0.5); a pack unit (Tray, Bag) holds `size` base units. No pack unit and
// no size is one base unit.
func packFor(ctx context.Context, tx pgx.Tx, variantID string, packUnitID *string, size *float64) (qty float64, label string, err error) {
	var base UnitRef
	var baseFactor *float64
	if err := tx.QueryRow(ctx, `
		SELECT u.id::text, u.code, u.symbol, u.name, u.kind, u.decimals, u.factor::float8
		FROM product_variants v JOIN products p ON p.id = v.product_id JOIN units u ON u.id = p.unit_id WHERE v.id::text = $1`, variantID).
		Scan(&base.ID, &base.Code, &base.Symbol, &base.Name, &base.Kind, &base.Decimals, &baseFactor); err != nil {
		return 0, "", err
	}
	if packUnitID == nil || *packUnitID == "" || *packUnitID == base.ID {
		if size == nil {
			return 1, "", nil
		}
		if *size <= 0 || !fitsDecimals(*size, base.Decimals) {
			return 0, "", unitInvalid(fmt.Sprintf("a pack holds a positive number of %s (%d decimal places at most)", base.Symbol, base.Decimals))
		}
		return *size, trimNum(*size) + " " + base.Symbol, nil
	}
	var pu UnitRef
	var puFactor *float64
	if err := tx.QueryRow(ctx, `SELECT id::text, code, symbol, name, kind, decimals, factor::float8 FROM units WHERE id::text = $1 AND status = 'active'`, *packUnitID).
		Scan(&pu.ID, &pu.Code, &pu.Symbol, &pu.Name, &pu.Kind, &pu.Decimals, &puFactor); errors.Is(err, pgx.ErrNoRows) {
		return 0, "", unitInvalid("choose an active pack unit")
	} else if err != nil {
		return 0, "", err
	}
	if size == nil || *size <= 0 {
		return 0, "", unitInvalid("say how much is in the pack (pack_size)")
	}
	switch {
	case pu.Kind == "pack":
		if !fitsDecimals(*size, base.Decimals) {
			return 0, "", unitInvalid(fmt.Sprintf("a %s holds a whole number of %s", strings.ToLower(pu.Name), base.Symbol))
		}
		return *size, pu.Name + " of " + trimNum(*size) + " " + base.Symbol, nil
	case pu.Kind == base.Kind && puFactor != nil && baseFactor != nil:
		q := math.Round(*size**puFactor / *baseFactor * 1000) / 1000
		if q <= 0 || !fitsDecimals(q, base.Decimals) {
			return 0, "", unitInvalid(fmt.Sprintf("%s %s isn't a quantity of %s this product can be sold in", trimNum(*size), pu.Symbol, base.Symbol))
		}
		return q, trimNum(*size) + " " + pu.Symbol, nil
	}
	return 0, "", &unitError{status: http.StatusBadRequest, code: "UNIT_KIND_MISMATCH",
		msg: fmt.Sprintf("this product is kept in %s: a pack is a Box, Bag, Tray … of it, or another %s unit", base.Name, base.Kind)}
}

func trimNum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// setPack stores a barcode's pack.
func setPack(ctx context.Context, tx pgx.Tx, variantID, code string, packUnitID *string, size *float64) error {
	qty, _, err := packFor(ctx, tx, variantID, packUnitID, size)
	if err != nil {
		return err
	}
	var unit any
	if packUnitID != nil && *packUnitID != "" {
		unit = *packUnitID
	}
	tag, err := tx.Exec(ctx, `UPDATE barcodes SET pack_qty = $3, pack_unit_id = $4::uuid, pack_size = $5 WHERE variant_id::text = $1 AND code = $2`,
		variantID, code, qty, unit, size)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if err != nil {
		return err
	}
	// A till's incremental catalog pull goes by the variant's updated_at.
	_, err = tx.Exec(ctx, `UPDATE product_variants SET updated_at = now() WHERE id::text = $1`, variantID)
	return err
}

// BarcodeInfo is one barcode of a variant, with its pack.
type BarcodeInfo struct {
	Code      string `json:"code"`
	Symbology string `json:"symbology"`
	Source    string `json:"source"`
	IsPrimary bool   `json:"is_primary"`
	Pack
}

// unitColumns / unitCols read a product's unit u (LEFT JOINed: all may be null).
const unitColumns = `u.id::text, u.code, u.symbol, u.name, u.kind, u.decimals`

type unitCols struct {
	id, code, symbol, name, kind *string
	decimals                     *int
}

func (c *unitCols) dest() []any {
	return []any{&c.id, &c.code, &c.symbol, &c.name, &c.kind, &c.decimals}
}

func (c *unitCols) ref() *UnitRef {
	if c.id == nil {
		return nil
	}
	return &UnitRef{ID: *c.id, Code: *c.code, Symbol: *c.symbol, Name: *c.name, Kind: *c.kind, Decimals: *c.decimals}
}

// packLabelSQL builds a barcode b's pack label in SQL (bu = its pack unit, u = the product's unit).
const packLabelSQL = `CASE WHEN b.pack_unit_id IS NULL AND b.pack_qty = 1 THEN ''
	WHEN b.pack_unit_id IS NULL THEN trim_scale(b.pack_qty)::text || ' ' || u.symbol
	WHEN bu.kind = 'pack' THEN bu.name || ' of ' || trim_scale(b.pack_qty)::text || ' ' || u.symbol
	ELSE trim_scale(COALESCE(b.pack_size, b.pack_qty))::text || ' ' || bu.symbol END`

func variantBarcodes(ctx context.Context, tx pgx.Tx, variantID string) ([]BarcodeInfo, error) {
	rows, err := tx.Query(ctx, `
		SELECT b.code, b.symbology, b.source, b.is_primary, b.pack_qty::text, b.pack_unit_id::text, b.pack_size::text, `+packLabelSQL+`
		FROM barcodes b JOIN product_variants v ON v.id = b.variant_id JOIN products p ON p.id = v.product_id
		LEFT JOIN units u ON u.id = p.unit_id LEFT JOIN units bu ON bu.id = b.pack_unit_id
		WHERE b.variant_id::text = $1 ORDER BY b.is_primary DESC, b.created_at`, variantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BarcodeInfo{}
	for rows.Next() {
		var b BarcodeInfo
		var label *string
		if err := rows.Scan(&b.Code, &b.Symbology, &b.Source, &b.IsPrimary, &b.Qty, &b.UnitID, &b.Size, &label); err != nil {
			return nil, err
		}
		if label != nil {
			b.Label = *label
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SetBarcodePack: PUT /products/variants/{id}/barcodes/{code}/pack
// {pack_unit_id, pack_size} (catalog.manage). Both empty: one base unit.
func (h *UnitHandler) SetBarcodePack(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	variantID, code := chi.URLParam(r, "id"), chi.URLParam(r, "code")
	var req struct {
		PackUnitID *string  `json:"pack_unit_id"`
		PackSize   *float64 `json:"pack_size"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var out []BarcodeInfo
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := variantBarcodes(ctx, tx, variantID)
		if err != nil {
			return err
		}
		if err := setPack(ctx, tx, variantID, code, req.PackUnitID, req.PackSize); err != nil {
			return err
		}
		if out, err = variantBarcodes(ctx, tx, variantID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "barcodes", EntityID: variantID, Action: "update", PerformedBy: claims.UserID,
			Before: before, After: out, Reason: "pack of " + code})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no such barcode on this variant")
		return
	}
	if respondProductEditError(w, err, "could not save the pack") {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"barcodes": out})
}
