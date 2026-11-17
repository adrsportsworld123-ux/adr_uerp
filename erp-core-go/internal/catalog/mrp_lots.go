package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// MRP lots (migrations/083, §3.86): the same item at a new MRP while old
// stock is on the shelf. A lot is its own variant (stock, MRP, selling
// price, cost) whose mrp_parent_id names the variant it is a lot of; it
// answers to its parent's barcodes, and everything else (stock moves,
// sales, returns, reports) treats it as the variant it is.

var errBadLotPrice = errors.New("an MRP lot needs a positive MRP, and a selling price above 0 and not above the MRP")

// lotSKUSuffix: "-M1099", "-M1099.5".
func lotSKUSuffix(mrp float64) string {
	return "-M" + strconv.FormatFloat(mrp, 'f', -1, 64)
}

// EnsureMRPLot returns the variant that holds variantID's stock at mrp: the
// variant itself (or its parent) when the MRP is the parent's, an existing
// lot with that MRP, else a new lot priced sellingPrice (0 = the parent's
// price, capped at the new MRP) and costed costPrice (0 = the parent's
// cost). created reports a new lot. Inside the caller's transaction.
func EnsureMRPLot(ctx context.Context, tx pgx.Tx, variantID string, mrp, sellingPrice, costPrice float64, userID string) (lotID string, created bool, err error) {
	mrp = math.Round(mrp*100) / 100
	sellingPrice = math.Round(sellingPrice*100) / 100
	if mrp <= 0 || sellingPrice < 0 || sellingPrice > mrp {
		return "", false, errBadLotPrice
	}
	var base, sku, productID string
	var baseMRP, basePrice, baseCost float64
	if err := tx.QueryRow(ctx, `
		SELECT b.id::text, b.sku, b.product_id::text, b.mrp::float8, b.selling_price::float8, b.cost_price::float8
		FROM product_variants v JOIN product_variants b ON b.id = COALESCE(v.mrp_parent_id, v.id)
		WHERE v.id::text = $1
		FOR UPDATE OF b`, variantID).Scan(&base, &sku, &productID, &baseMRP, &basePrice, &baseCost); err != nil {
		return "", false, err
	}
	if math.Abs(baseMRP-mrp) < 0.005 {
		return base, false, nil
	}
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM product_variants WHERE mrp_parent_id::text = $1 AND abs(mrp - $2) < 0.005
		ORDER BY status = 'active' DESC, created_at LIMIT 1`, base, mrp).Scan(&lotID)
	if err == nil {
		return lotID, false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	if sellingPrice == 0 {
		sellingPrice = math.Min(basePrice, mrp)
	}
	if costPrice <= 0 {
		costPrice = baseCost
	}
	// A SKU nobody has: SKU-M1099, then SKU-M1099-2, ...
	newSKU := sku + lotSKUSuffix(mrp)
	for i := 2; ; i++ {
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM product_variants WHERE sku = $1)`, newSKU).Scan(&taken); err != nil {
			return "", false, err
		}
		if !taken {
			break
		}
		newSKU = sku + lotSKUSuffix(mrp) + "-" + strconv.Itoa(i)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO product_variants (merchant_id, product_id, sku, attribute_combo, cost_price, mrp, selling_price, track_serial, track_batch,
		                              open_price, open_price_min, open_price_max, open_price_approval_above, mrp_parent_id)
		SELECT merchant_id, product_id, $2, attribute_combo, $3, $4, $5, track_serial, track_batch,
		       open_price, open_price_min, open_price_max, open_price_approval_above, id
		FROM product_variants WHERE id::text = $1
		RETURNING id::text`, base, newSKU, costPrice, mrp, sellingPrice).Scan(&lotID); err != nil {
		return "", false, err
	}
	// Zero stock at every branch, as insertVariant does for any new variant.
	if err := db.Unrestricted(ctx, tx, func() error {
		_, err := tx.Exec(ctx, `
			INSERT INTO stock_levels (merchant_id, branch_id, variant_id, on_hand, reserved, version)
			SELECT current_setting('app.tenant_id')::uuid, b.id, $1::uuid, 0, 0, 0
			FROM branches b WHERE b.merchant_id = current_setting('app.tenant_id')::uuid
			ON CONFLICT (branch_id, variant_id) DO NOTHING`, lotID)
		return err
	}); err != nil {
		return "", false, err
	}
	// Tills pull changed variants by their product's and variant's time.
	if _, err := tx.Exec(ctx, `UPDATE products SET updated_at = now() WHERE id::text = $1`, productID); err != nil {
		return "", false, err
	}
	if err := audit.Log(ctx, tx, audit.Entry{
		EntityType: "product_variants", EntityID: lotID, Action: "create", PerformedBy: userID, Reason: "MRP lot",
		After: map[string]any{"mrp_lot_of": base, "sku": newSKU, "mrp": mrp, "selling_price": sellingPrice, "cost_price": costPrice},
	}); err != nil {
		return "", false, err
	}
	return lotID, true, nil
}

// CreateMRPLot: POST /products/variants/{id}/mrp-lots {mrp, selling_price,
// cost_price?} (pricing.manage) — a lot for the new MRP before any goods
// arrive (a GRN line with the new MRP makes one on its own). An MRP that
// already has a lot answers with it (200); a new lot is 201. Responds with
// the product, as the other variant edits do.
func (h *ListHandler) CreateMRPLot(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var in struct {
		MRP          float64 `json:"mrp"`
		SellingPrice float64 `json:"selling_price"`
		CostPrice    float64 `json:"cost_price"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if in.SellingPrice <= 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", errBadLotPrice.Error())
		return
	}
	var d productDetail
	var created bool
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if _, created, err = EnsureMRPLot(ctx, tx, chi.URLParam(r, "id"), in.MRP, in.SellingPrice, in.CostPrice, claims.UserID); err != nil {
			return err
		}
		var productID string
		if err := tx.QueryRow(ctx, `SELECT product_id::text FROM product_variants WHERE id::text = $1`, chi.URLParam(r, "id")).Scan(&productID); err != nil {
			return err
		}
		d, err = loadProductDetail(ctx, tx, productID)
		return err
	})
	if errors.Is(err, errBadLotPrice) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if respondProductEditError(w, err, "could not add the MRP lot") {
		return
	}
	h.reindexProduct(r.Context(), claims.TenantID, d)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, d)
}
