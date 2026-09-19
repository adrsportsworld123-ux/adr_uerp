package sync

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type catalogEntry struct {
	Barcode      string  `json:"barcode"`
	ProductID    string  `json:"product_id"`
	ProductName  string  `json:"product_name"`
	HSNCode      string  `json:"hsn_code"`
	VariantID    string  `json:"variant_id"`
	SKU          string  `json:"sku"`
	SellingPrice string  `json:"selling_price"`
	MRP          string  `json:"mrp"`
	CGSTRate     float64 `json:"cgst_rate"`
	SGSTRate     float64 `json:"sgst_rate"`
	IGSTRate     float64 `json:"igst_rate"`
	CessRate     float64 `json:"cess_rate"`
}

type stockEntry struct {
	VariantID string `json:"variant_id"`
	OnHand    string `json:"on_hand"`
	Reserved  string `json:"reserved"`
	Available string `json:"available"`
}

type pullResponse struct {
	ServerTime string         `json:"server_time"` // pass this back as `since` on the next pull
	Catalog    []catalogEntry `json:"catalog"`
	Stock      []stockEntry   `json:"stock"`
}

// Pull: GET /sync/pull?since=<RFC3339>&branch_id= — incremental catalog and
// stock-level changes for a device's local cache, so a POS that's been
// offline for hours can catch up without re-downloading the whole catalog.
// `since` omitted (or unparseable) means "everything" — a fresh device's
// first pull.
func (h *Handler) Pull(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	if branchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id is required")
		return
	}

	since, sinceErr := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
	hasSince := sinceErr == nil

	resp := pullResponse{ServerTime: time.Now().UTC().Format(time.RFC3339)}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		catalogRows, err := tx.Query(ctx, `
			SELECT b.code, p.id, p.name, COALESCE(p.hsn_code, ''),
			       pv.id, pv.sku, pv.selling_price::text, pv.mrp::text,
			       COALESCE(ts.cgst_rate,0), COALESCE(ts.sgst_rate,0),
			       COALESCE(ts.igst_rate,0), COALESCE(ts.cess_rate,0)
			FROM barcodes b
			JOIN product_variants pv ON pv.id = b.variant_id
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
			WHERE ($1::boolean = false) OR (p.updated_at > $2) OR (pv.updated_at > $2)`,
			hasSince, since)
		if err != nil {
			return err
		}
		defer catalogRows.Close()
		resp.Catalog = []catalogEntry{}
		for catalogRows.Next() {
			var c catalogEntry
			if err := catalogRows.Scan(&c.Barcode, &c.ProductID, &c.ProductName, &c.HSNCode,
				&c.VariantID, &c.SKU, &c.SellingPrice, &c.MRP,
				&c.CGSTRate, &c.SGSTRate, &c.IGSTRate, &c.CessRate); err != nil {
				return err
			}
			resp.Catalog = append(resp.Catalog, c)
		}
		if err := catalogRows.Err(); err != nil {
			return err
		}

		stockRows, err := tx.Query(ctx, `
			SELECT variant_id, on_hand::text, reserved::text, (on_hand - reserved)::text
			FROM stock_levels
			WHERE branch_id = $1 AND (($2::boolean = false) OR (updated_at > $3))`,
			branchID, hasSince, since)
		if err != nil {
			return err
		}
		defer stockRows.Close()
		resp.Stock = []stockEntry{}
		for stockRows.Next() {
			var s stockEntry
			if err := stockRows.Scan(&s.VariantID, &s.OnHand, &s.Reserved, &s.Available); err != nil {
				return err
			}
			resp.Stock = append(resp.Stock, s)
		}
		return stockRows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not pull sync data")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
