package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

type Params struct {
	TenantID   string // always applied as a filter, never client-controlled
	Query      string
	CategoryID string
	BrandID    string
	MinPrice   string
	MaxPrice   string
	From, Size int
}

type Hit struct {
	Document
	Score float64 `json:"_score"`
}

type Result struct {
	Total int64
	Hits  []Hit
}

// Search always scopes to Params.TenantID as a hard filter — this is the
// one query this package builds, and the merchant_id term below is the
// entire tenant-isolation boundary for this data source (OpenSearch has
// no RLS equivalent). Never build a query here from caller-supplied
// merchant_id; it must come from authn claims.
func (c *Client) Search(ctx context.Context, p Params) (Result, error) {
	if p.TenantID == "" {
		return Result{}, fmt.Errorf("search: TenantID is required")
	}
	filter := []map[string]any{
		{"term": map[string]any{"merchant_id": p.TenantID}},
		{"term": map[string]any{"status": "active"}},
	}
	if p.CategoryID != "" {
		filter = append(filter, map[string]any{"term": map[string]any{"category_id": p.CategoryID}})
	}
	if p.BrandID != "" {
		filter = append(filter, map[string]any{"term": map[string]any{"brand_id": p.BrandID}})
	}
	if p.MinPrice != "" || p.MaxPrice != "" {
		rng := map[string]any{}
		if v, err := strconv.ParseFloat(p.MinPrice, 64); err == nil {
			rng["gte"] = v
		}
		if v, err := strconv.ParseFloat(p.MaxPrice, 64); err == nil {
			rng["lte"] = v
		}
		if len(rng) > 0 {
			filter = append(filter, map[string]any{"range": map[string]any{"selling_price": rng}})
		}
	}

	boolQuery := map[string]any{"filter": filter}
	if p.Query != "" {
		boolQuery["must"] = []map[string]any{
			{
				"multi_match": map[string]any{
					"query":     p.Query,
					"fields":    []string{"name^3", "sku^3", "hsn_code", "category_name", "brand_name"},
					"fuzziness": "AUTO",
				},
			},
		}
	} else {
		boolQuery["must"] = []map[string]any{{"match_all": map[string]any{}}}
	}

	size := p.Size
	if size <= 0 || size > 200 {
		size = 50
	}
	body := map[string]any{
		"from":  p.From,
		"size":  size,
		"query": map[string]any{"bool": boolQuery},
		"sort":  []any{"_score", map[string]any{"name.keyword": map[string]any{"order": "asc", "unmapped_type": "keyword"}}},
	}

	resp, err := c.request(ctx, http.MethodPost, "/"+IndexName+"/_search", body)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("search: query: %s: %s", resp.Status, truncate(string(respBody), 500))
	}

	var parsed struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				Score  float64  `json:"_score"`
				Source Document `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return Result{}, err
	}

	result := Result{Total: parsed.Hits.Total.Value}
	for _, h := range parsed.Hits.Hits {
		result.Hits = append(result.Hits, Hit{Document: h.Source, Score: h.Score})
	}
	return result, nil
}
