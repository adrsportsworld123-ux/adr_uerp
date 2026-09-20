// Package search wraps the OpenSearch product index used by
// Phase 2's Product Search sub-area (phased_roadmap.md). Implemented as a
// small hand-rolled REST client over OpenSearch's plain JSON HTTP API
// rather than pulling in the opensearch-go SDK — this codebase has stayed
// deliberately dependency-light (see tech_stack_decision.md §3.1's as-built
// note on skipping an ORM), and OpenSearch's document/search/bulk API
// surface used here is a handful of JSON requests, not enough to justify
// a new module.
//
// OpenSearch is a derived, rebuildable read model, never the system of
// record — Postgres (via product_variants/products) stays authoritative.
// A search index document is a denormalized snapshot: one document per
// product_variant, carrying its parent product's name/HSN and its
// category/brand names inline, since OpenSearch has no join.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// IndexName is the single OpenSearch index this package manages. One
// index shared across all tenants — merchant_id is a filterable field on
// every document, not a separate index per tenant, since OpenSearch has
// no RLS: every query this package builds MUST include a merchant_id
// term filter, or a tenant's catalog leaks into another's search results.
const IndexName = "products"

type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient never fails or blocks — OpenSearch may not be reachable yet
// (compose startup ordering, or search simply not configured), and search
// is an optional, best-effort feature layered on top of Postgres, not a
// dependency the rest of the API should fail to boot without. A nil
// *Client (baseURL == "") makes every method below a no-op error, and
// callers treat that as "search unavailable," not a fatal condition.
func NewClient(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: 5 * time.Second}}
}

func (c *Client) Enabled() bool { return c != nil && c.baseURL != "" }

var errDisabled = fmt.Errorf("search: OPENSEARCH_URL not configured")

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	if !c.Enabled() {
		return nil, errDisabled
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.http.Do(req)
}

// EnsureIndex creates the index with an explicit mapping if it doesn't
// already exist. Idempotent and safe to call on every startup and before
// every reindex — OpenSearch returns 400
// resource_already_exists_exception on a repeat PUT, which this treats as
// success rather than an error.
func (c *Client) EnsureIndex(ctx context.Context) error {
	mapping := map[string]any{
		"mappings": map[string]any{
			"properties": map[string]any{
				"merchant_id":   map[string]any{"type": "keyword"},
				"variant_id":    map[string]any{"type": "keyword"},
				"product_id":    map[string]any{"type": "keyword"},
				"name":          map[string]any{"type": "text"},
				"sku":           map[string]any{"type": "keyword"},
				"hsn_code":      map[string]any{"type": "keyword"},
				"category_id":   map[string]any{"type": "keyword"},
				"category_name": map[string]any{"type": "text"},
				"brand_id":      map[string]any{"type": "keyword"},
				"brand_name":    map[string]any{"type": "text"},
				"cost_price":    map[string]any{"type": "double"},
				"mrp":           map[string]any{"type": "double"},
				"selling_price": map[string]any{"type": "double"},
				"status":        map[string]any{"type": "keyword"},
			},
		},
	}
	resp, err := c.request(ctx, http.MethodPut, "/"+IndexName, mapping)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusBadRequest && bytes.Contains(body, []byte("resource_already_exists_exception")) {
		return nil
	}
	return fmt.Errorf("search: ensure index: %s: %s", resp.Status, string(body))
}

// Count returns how many documents are currently in the index — used at
// startup to detect "the index exists but is empty," which is otherwise
// indistinguishable from "search is just fine, nobody's indexed anything
// yet" without this check. See BackfillAllTenants in index.go for what
// happens when this comes back 0.
func (c *Client) Count(ctx context.Context) (int64, error) {
	resp, err := c.request(ctx, http.MethodGet, "/"+IndexName+"/_count", nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("search: count: %s: %s", resp.Status, string(body))
	}
	var parsed struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, err
	}
	return parsed.Count, nil
}
