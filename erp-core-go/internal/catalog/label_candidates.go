package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// Bulk label jobs (§3.88): the variants a Label Studio job can start from —
// every sellable variant, those whose labels carry no original (printed)
// barcode, or the ones picked — each with what the branch has available,
// which is the job's default number of copies (the label count can be
// changed per item before printing). Read-only; printing still goes
// through POST /labels/preview and /labels/print.

type labelCandidate struct {
	VariantID   string `json:"variant_id"`
	ProductName string `json:"product_name"`
	SKU         string `json:"sku"`
	// Label: the variant's attribute values ("Red · L").
	Label string `json:"label"`
	MRP   string `json:"mrp"`
	// Available: on hand less reserved at the branch; DefaultCopies is that,
	// whole, between 1 and maxStudioItemCopies.
	Available     string `json:"available"`
	DefaultCopies int    `json:"default_copies"`
	// Barcode: what the label will print (an MRP lot prints its parent's
	// code); "" = none yet. HasOriginal: it is the printed (supplier) code.
	Barcode     string  `json:"barcode"`
	HasOriginal bool    `json:"has_original"`
	MRPLotOf    *string `json:"mrp_lot_of"`
}

const maxLabelCandidates = 1000

// defaultCopies: the label count a variant starts with — its available
// stock, whole, at least 1 and at most one item's limit.
func defaultCopies(available float64) int {
	n := int(math.Floor(available))
	if n < 1 {
		n = 1
	}
	if n > maxStudioItemCopies {
		n = maxStudioItemCopies
	}
	return n
}

// LabelCandidates: GET /labels/candidates?branch_id=&scope=all|no_original
// &variant_ids=a,b&in_stock=true|false&category_id=&q= — scope all (the
// default) is every sellable variant; no_original those whose label has no
// printed (supplier) barcode; variant_ids picks given ones (scope ignored).
// in_stock (default true for a scope, false for picked ones) leaves out
// what the branch has none of. At most 1000, by product name; `total` says
// how many matched, `no_barcode` how many have no barcode at all.
func (h *LabelHandler) LabelCandidates(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	branchID, scope := q.Get("branch_id"), q.Get("scope")
	if branchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id is required")
		return
	}
	if scope == "" {
		scope = "all"
	}
	if scope != "all" && scope != "no_original" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "scope must be all or no_original")
		return
	}
	var ids []string
	for _, id := range strings.Split(q.Get("variant_ids"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) > maxLabelCandidates {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "at most 1000 variant_ids")
		return
	}
	inStock := len(ids) == 0
	if v := q.Get("in_stock"); v != "" {
		inStock = v == "true"
	}
	if len(ids) > 0 {
		scope = "picked"
	}

	out := []labelCandidate{}
	var total, noBarcode int
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		const from = `
			FROM product_variants pv
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN stock_levels sl ON sl.variant_id = pv.id AND sl.branch_id::text = $1
			LEFT JOIN LATERAL (
				SELECT b.code, b.source FROM barcodes b WHERE b.variant_id = COALESCE(pv.mrp_parent_id, pv.id)
				ORDER BY b.is_primary DESC, b.created_at LIMIT 1) bc ON true
			WHERE ` + SellablePredicate + `
			  AND ($2 <> 'picked' OR pv.id::text = ANY($3))
			  AND ($2 <> 'no_original' OR NOT EXISTS (
			        SELECT 1 FROM barcodes o WHERE o.variant_id = COALESCE(pv.mrp_parent_id, pv.id) AND o.source = 'original'))
			  AND (NOT $4 OR COALESCE(sl.on_hand - sl.reserved, 0) > 0)
			  AND ($5 = '' OR p.category_id::text = $5)
			  AND ($6 = '' OR p.name ILIKE '%' || $6 || '%' OR pv.sku ILIKE '%' || $6 || '%')`
		args := []any{branchID, scope, ids, inStock, q.Get("category_id"), strings.TrimSpace(q.Get("q"))}
		if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE bc.code IS NULL)`+from, args...).Scan(&total, &noBarcode); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT pv.id::text, p.name, pv.sku, pv.attribute_combo::text, pv.mrp::text,
			       COALESCE(sl.on_hand - sl.reserved, 0)::float8, COALESCE(bc.code, ''), COALESCE(bc.source = 'original', false),
			       pv.mrp_parent_id::text`+from+`
			ORDER BY p.name, pv.sku
			LIMIT $7`, append(args, maxLabelCandidates)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c labelCandidate
			var combo string
			var avail float64
			if err := rows.Scan(&c.VariantID, &c.ProductName, &c.SKU, &combo, &c.MRP, &avail, &c.Barcode, &c.HasOriginal, &c.MRPLotOf); err != nil {
				return err
			}
			c.Label = ComboLabel([]byte(combo))
			c.Available = strconv.FormatFloat(avail, 'f', -1, 64)
			c.DefaultCopies = defaultCopies(avail)
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list the variants")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"variants": out, "total": total, "no_barcode": noBarcode, "limit": maxLabelCandidates})
}

// GenerateMissingBarcodes: POST /labels/generate-barcodes {variant_ids} —
// a system barcode for each listed variant that has none (an MRP lot's goes
// on the variant it is a lot of, whose code it prints), so the job can
// print them. Variants that already have a code are left alone. Not gated,
// as POST /products/variants/{id}/barcodes isn't. The variants are touched
// so tills pull the new codes.
func (h *LabelHandler) GenerateMissingBarcodes(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		VariantIDs []string `json:"variant_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.VariantIDs) == 0 || len(req.VariantIDs) > maxLabelCandidates {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "variant_ids: 1 to 1000")
		return
	}
	codes := map[string]string{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		done := map[string]string{} // base variant → its new code
		for _, id := range req.VariantIDs {
			var base string
			var has bool
			err := tx.QueryRow(ctx, `
				SELECT COALESCE(pv.mrp_parent_id, pv.id)::text,
				       EXISTS (SELECT 1 FROM barcodes b WHERE b.variant_id = COALESCE(pv.mrp_parent_id, pv.id))
				FROM product_variants pv WHERE pv.id::text = $1`, id).Scan(&base, &has)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			} else if err != nil {
				return err
			}
			if code, ok := done[base]; ok {
				codes[id] = code
				continue
			}
			if has {
				continue
			}
			code, err := assignGeneratedCode(ctx, tx, base, true)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE product_variants SET updated_at = now() WHERE id::text = $1 OR mrp_parent_id::text = $1`, base); err != nil {
				return err
			}
			done[base], codes[id] = code, code
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not generate the barcodes")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"generated": len(codes), "codes": codes})
}
