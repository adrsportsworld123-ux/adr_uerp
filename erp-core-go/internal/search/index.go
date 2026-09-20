package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
)

// Document is one product_variant, denormalized with its parent product's
// display fields and its category/brand names — see the package comment
// for why (no joins in OpenSearch). variant_id is also the OpenSearch
// document _id, so re-indexing the same variant is a plain overwrite, not
// an upsert-with-merge.
type Document struct {
	VariantID    string  `json:"variant_id"`
	ProductID    string  `json:"product_id"`
	MerchantID   string  `json:"merchant_id"`
	Name         string  `json:"name"`
	SKU          string  `json:"sku"`
	HSNCode      string  `json:"hsn_code"`
	CategoryID   string  `json:"category_id,omitempty"`
	CategoryName string  `json:"category_name,omitempty"`
	BrandID      string  `json:"brand_id,omitempty"`
	BrandName    string  `json:"brand_name,omitempty"`
	CostPrice    float64 `json:"cost_price"`
	MRP          float64 `json:"mrp"`
	SellingPrice float64 `json:"selling_price"`
	Status       string  `json:"status"`
}

// IndexDocument upserts a single document (PUT with an explicit _id is an
// overwrite in OpenSearch, so this doubles as create-or-update).
func (c *Client) IndexDocument(ctx context.Context, doc Document) error {
	resp, err := c.request(ctx, http.MethodPut, "/"+IndexName+"/_doc/"+doc.VariantID, doc)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("search: index document: %s: %s", resp.Status, string(body))
	}
	return nil
}

// DeleteDocument removes a variant from the index. A 404 (already gone,
// or never indexed) is not an error — the caller's intent ("this variant
// shouldn't be searchable") is already satisfied.
func (c *Client) DeleteDocument(ctx context.Context, variantID string) error {
	resp, err := c.request(ctx, http.MethodDelete, "/"+IndexName+"/_doc/"+variantID, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("search: delete document: %s: %s", resp.Status, string(body))
	}
	return nil
}

// BulkIndex writes many documents in one request via OpenSearch's
// newline-delimited _bulk API — the only realistic way to reindex a whole
// catalog without one HTTP round trip per variant.
func (c *Client) BulkIndex(ctx context.Context, docs []Document) error {
	if len(docs) == 0 {
		return nil
	}
	if !c.Enabled() {
		return errDisabled
	}
	var buf bytes.Buffer
	for _, d := range docs {
		action := map[string]any{"index": map[string]any{"_index": IndexName, "_id": d.VariantID}}
		actionLine, err := json.Marshal(action)
		if err != nil {
			return err
		}
		docLine, err := json.Marshal(d)
		if err != nil {
			return err
		}
		buf.Write(actionLine)
		buf.WriteByte('\n')
		buf.Write(docLine)
		buf.WriteByte('\n')
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/_bulk", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("search: bulk index: %s: %s", resp.Status, string(body))
	}
	var result struct {
		Errors bool `json:"errors"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}
	if result.Errors {
		return fmt.Errorf("search: bulk index: one or more items failed: %s", truncate(string(body), 500))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// FetchDocument reads one variant's current, fully denormalized row
// straight from Postgres (the system of record) — used both by the bulk
// reindex and by callers that just changed one variant (e.g. pricing)
// and want to push that single, authoritative row into the index rather
// than reconstructing a Document from whatever fields they happen to have
// in hand.
func FetchDocument(ctx context.Context, database *db.DB, tenantID, variantID string) (Document, error) {
	var doc Document
	err := database.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT v.id, v.product_id, v.merchant_id, p.name, v.sku, COALESCE(p.hsn_code, ''),
			       COALESCE(p.category_id::text, ''), COALESCE(c.name, ''),
			       COALESCE(p.brand_id::text, ''), COALESCE(b.name, ''),
			       v.cost_price, v.mrp, v.selling_price, v.status
			FROM product_variants v
			JOIN products p ON p.id = v.product_id
			LEFT JOIN categories c ON c.id = p.category_id
			LEFT JOIN brands b ON b.id = p.brand_id
			WHERE v.id = $1`, variantID).
			Scan(&doc.VariantID, &doc.ProductID, &doc.MerchantID, &doc.Name, &doc.SKU, &doc.HSNCode,
				&doc.CategoryID, &doc.CategoryName, &doc.BrandID, &doc.BrandName,
				&doc.CostPrice, &doc.MRP, &doc.SellingPrice, &doc.Status)
	})
	return doc, err
}

// BackfillAllTenants rebuilds the index for every merchant in the system —
// called once at startup when the index comes up empty (a lost volume, a
// disaster-recovery restore, or a brand-new environment), so search
// doesn't silently return nothing until someone remembers to call
// POST /search/reindex by hand. That endpoint stays the per-tenant,
// on-demand path; this is the same operation run unattended, across every
// tenant, before any request has come in to trigger it.
//
// Querying `merchants` directly via database.Pool rather than WithTenant
// is deliberate, not an oversight: merchants is the table that DEFINES
// tenants, so there is no tenant context to scope this query to — the
// same reasoning `internal/authn`'s login handler already documents for
// its own direct `merchants` lookup (see its header comment). Every
// per-merchant document fetch below still goes through
// FetchAllDocuments -> WithTenant as normal.
func BackfillAllTenants(ctx context.Context, database *db.DB, client *Client) (int, error) {
	rows, err := database.Pool.Query(ctx, `SELECT id FROM merchants WHERE status = 'active'`)
	if err != nil {
		return 0, err
	}
	var merchantIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		merchantIDs = append(merchantIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	if err := client.EnsureIndex(ctx); err != nil {
		return 0, err
	}

	total := 0
	for _, tenantID := range merchantIDs {
		docs, err := FetchAllDocuments(ctx, database, tenantID)
		if err != nil {
			return total, fmt.Errorf("search: backfill tenant %s: fetch: %w", tenantID, err)
		}
		if err := client.BulkIndex(ctx, docs); err != nil {
			return total, fmt.Errorf("search: backfill tenant %s: index: %w", tenantID, err)
		}
		total += len(docs)
	}
	return total, nil
}

// FetchAllDocuments is FetchDocument's bulk-reindex counterpart: every
// variant belonging to the tenant, in one pass.
func FetchAllDocuments(ctx context.Context, database *db.DB, tenantID string) ([]Document, error) {
	var docs []Document
	err := database.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT v.id, v.product_id, v.merchant_id, p.name, v.sku, COALESCE(p.hsn_code, ''),
			       COALESCE(p.category_id::text, ''), COALESCE(c.name, ''),
			       COALESCE(p.brand_id::text, ''), COALESCE(b.name, ''),
			       v.cost_price, v.mrp, v.selling_price, v.status
			FROM product_variants v
			JOIN products p ON p.id = v.product_id
			LEFT JOIN categories c ON c.id = p.category_id
			LEFT JOIN brands b ON b.id = p.brand_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d Document
			if err := rows.Scan(&d.VariantID, &d.ProductID, &d.MerchantID, &d.Name, &d.SKU, &d.HSNCode,
				&d.CategoryID, &d.CategoryName, &d.BrandID, &d.BrandName,
				&d.CostPrice, &d.MRP, &d.SellingPrice, &d.Status); err != nil {
				return err
			}
			docs = append(docs, d)
		}
		return rows.Err()
	})
	return docs, err
}
