package pricing

// Selling price suggestions (§3.83; migrations/080): a suggested selling
// price from an item's cost and MRP by the Merchant Admin's rules. It only
// pre-fills a price someone then saves (the Pricing dialog, New product)
// or applies in bulk after a preview; the POS never sees a suggestion.
// Plan feature price_suggestion (the router's path gate covers
// /pricing/suggest… and /pricing/suggestion-…); off until switched on.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/softdelete"
	"erp-core-go/internal/taxcalc"
)

// SuggestionRule is one price_suggestion_rules row.
type SuggestionRule struct {
	ID             string   `json:"rule_id"`
	Name           string   `json:"name"`
	CategoryID     *string  `json:"category_id"`
	Category       *string  `json:"category"`
	BrandID        *string  `json:"brand_id"`
	Brand          *string  `json:"brand"`
	Method         string   `json:"method"`    // cost | mrp | lower | higher
	CostType       string   `json:"cost_type"` // margin | markup
	CostPct        *float64 `json:"cost_pct"`
	MRPDiscountPct *float64 `json:"mrp_discount_pct"`
	MinMarginPct   float64  `json:"min_margin_pct"`
	Rounding       string   `json:"rounding"`
	Priority       int      `json:"priority"`
	Active         bool     `json:"active"`
	CreatedAt      string   `json:"created_at"`
}

const ruleSelect = `
	SELECT r.id::text, r.name, r.category_id::text, c.name, r.brand_id::text, br.name, r.method, r.cost_type,
	       r.cost_pct::float8, r.mrp_discount_pct::float8, r.min_margin_pct::float8, r.rounding, r.priority, r.active, r.created_at::text
	FROM price_suggestion_rules r
	LEFT JOIN categories c ON c.id = r.category_id
	LEFT JOIN brands br ON br.id = r.brand_id`

func scanRule(row pgx.Row, x *SuggestionRule) error {
	return row.Scan(&x.ID, &x.Name, &x.CategoryID, &x.Category, &x.BrandID, &x.Brand, &x.Method, &x.CostType,
		&x.CostPct, &x.MRPDiscountPct, &x.MinMarginPct, &x.Rounding, &x.Priority, &x.Active, &x.CreatedAt)
}

var roundings = map[string]bool{"none": true, "up_1": true, "up_5": true, "up_10": true, "end_9": true, "end_99": true, "charm": true}

// roundUp rounds a price up (never down) the rule's way. charm is the ADR
// Sports playbook: the next ₹5 up to ₹100, then the next price ending in 9.
func roundUp(p float64, how string) float64 {
	p = round2(p)
	const eps = 1e-9
	switch how {
	case "up_1":
		return math.Ceil(p - eps)
	case "up_5":
		return math.Ceil(p/5-eps) * 5
	case "up_10":
		return math.Ceil(p/10-eps) * 10
	case "end_9":
		if p <= 9 {
			return 9
		}
		return math.Ceil((p-9)/10-eps)*10 + 9
	case "end_99":
		return round2(math.Ceil(p-0.99-eps) + 0.99)
	case "charm":
		if p <= 100 {
			return roundUp(p, "up_5")
		}
		return roundUp(p, "end_9")
	}
	return p
}

// suggestIn is what a suggestion is worked out from.
type suggestIn struct {
	Cost, MRP       float64
	TaxRatePct      float64
	Inclusive       bool // the product's selling price includes GST
	CostIncludesTax bool // the merchant's setting
}

// Suggestion is the result for one item.
type Suggestion struct {
	Price     *float64 `json:"suggested_price"`
	Status    string   `json:"status"` // ok | at_mrp | check | hold | no_cost | no_rule
	MarginPct *float64 `json:"margin_pct"`
	RuleID    *string  `json:"rule_id"`
	Rule      *string  `json:"rule"`
	Note      string   `json:"note"`
}

// suggest works a price out in the terms the product stores its selling
// price (with GST for a GST-inclusive product). Cost: with GST added for
// an inclusive product, unless the merchant's costs already include it.
// MRP always includes GST.
func suggest(in suggestIn, r SuggestionRule) Suggestion {
	out := Suggestion{RuleID: &r.ID, Rule: &r.Name}
	if in.Cost <= 0 {
		out.Status, out.Note = "no_cost", "No cost price to work from"
		return out
	}
	t := in.TaxRatePct / 100
	cost := in.Cost
	if in.Inclusive && !in.CostIncludesTax {
		cost = in.Cost * (1 + t)
	}
	mrp := in.MRP
	if mrp > 0 && !in.Inclusive {
		mrp = in.MRP / (1 + t)
	}
	fromCost := func(pct float64) float64 {
		if r.CostType == "markup" {
			return cost * (1 + pct/100)
		}
		return cost / (1 - pct/100)
	}
	byCost, byMRP := 0.0, 0.0
	if r.CostPct != nil {
		byCost = fromCost(*r.CostPct)
	}
	if r.MRPDiscountPct != nil && mrp > 0 {
		byMRP = mrp * (1 - *r.MRPDiscountPct/100)
	}
	var p float64
	switch r.Method {
	case "cost":
		p = byCost
	case "mrp":
		if mrp <= 0 {
			out.Status, out.Note = "check", "No MRP: this rule works from MRP"
			return out
		}
		p = byMRP
	case "lower", "higher":
		switch {
		case mrp <= 0:
			p = byCost
		case r.Method == "lower":
			p = math.Min(byCost, byMRP)
		default:
			p = math.Max(byCost, byMRP)
		}
	}
	floor := cost / (1 - r.MinMarginPct/100)
	if p < floor {
		p = floor
	}
	p = roundUp(p, r.Rounding)
	switch {
	case mrp <= 0:
		out.Status, out.Note = "check", "No MRP: check the price against the label"
	case round2(mrp) <= round2(floor) || round2(mrp) <= round2(cost):
		out.Status, out.Note = "hold", "Cost is at or above MRP (or leaves less than the minimum margin): hold the item, renegotiate cost or correct the MRP"
		return out
	case p > round2(mrp):
		p = round2(mrp)
		out.Status, out.Note = "at_mrp", "The target margin isn't reachable below MRP: sell at MRP"
	default:
		out.Status = "ok"
	}
	p = round2(p)
	out.Price = &p
	m := round2((p - cost) / p * 100)
	out.MarginPct = &m
	return out
}

// mrpInPriceTerms: MRP (always with GST) as the product stores its price.
func mrpInPriceTerms(mrp, taxRatePct float64, inclusive bool) float64 {
	if inclusive {
		return mrp
	}
	return mrp / (1 + taxRatePct/100)
}

// liveSuggestionRule: the rule for a product of categoryID and brandID —
// category and brand, then the nearest category above it, then brand,
// then every product; then priority, newest.
func liveSuggestionRule(ctx context.Context, tx pgx.Tx, categoryID, brandID string) (SuggestionRule, bool, error) {
	var r SuggestionRule
	err := scanRule(tx.QueryRow(ctx, `
		WITH RECURSIVE up AS (
			SELECT id, parent_id, 0 AS depth FROM categories WHERE id = NULLIF($1, '')::uuid
			UNION ALL
			SELECT c.id, c.parent_id, up.depth + 1 FROM categories c JOIN up ON c.id = up.parent_id
		)`+strings.Replace(ruleSelect, "FROM price_suggestion_rules r", "FROM price_suggestion_rules r LEFT JOIN up ON up.id = r.category_id", 1)+`
		WHERE r.active AND (r.category_id IS NULL OR up.id IS NOT NULL) AND (r.brand_id IS NULL OR r.brand_id = NULLIF($2, '')::uuid)
		ORDER BY (r.category_id IS NOT NULL AND r.brand_id IS NOT NULL) DESC, (r.category_id IS NOT NULL) DESC, up.depth ASC NULLS LAST,
		         (r.brand_id IS NOT NULL) DESC, r.priority DESC, r.created_at DESC
		LIMIT 1`, categoryID, brandID), &r)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

type suggestionSettings struct {
	Enabled         bool `json:"enabled"`
	CostIncludesTax bool `json:"cost_includes_tax"`
	CanManage       bool `json:"can_manage"`
	RuleCount       int  `json:"rule_count"`
}

func loadSuggestionSettings(ctx context.Context, tx pgx.Tx) (suggestionSettings, error) {
	var s suggestionSettings
	err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT suggestion_enabled FROM pricing_settings), false),
		COALESCE((SELECT suggestion_cost_includes_tax FROM pricing_settings), false),
		(SELECT count(*) FROM price_suggestion_rules WHERE active)::int`).Scan(&s.Enabled, &s.CostIncludesTax, &s.RuleCount)
	return s, err
}

func canManageSuggestions(ctx context.Context, tx pgx.Tx, userID string) (bool, error) {
	codes, err := authn.FetchPermissions(ctx, tx, userID)
	return authn.HasPermission(codes, "pricing.suggestion.manage"), err
}

// GetSuggestionSettings: GET /pricing/suggestion-settings
func (h *Handler) GetSuggestionSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var s suggestionSettings
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if s, err = loadSuggestionSettings(ctx, tx); err != nil {
			return err
		}
		s.CanManage, err = canManageSuggestions(ctx, tx, claims.UserID)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load suggestion settings")
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// UpdateSuggestionSettings: PUT /pricing/suggestion-settings {enabled?,
// cost_includes_tax?} (pricing.suggestion.manage). Switching on with no
// rule adds one for every product: a 15% margin, rounded the charm way.
func (h *Handler) UpdateSuggestionSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var in struct {
		Enabled         *bool `json:"enabled"`
		CostIncludesTax *bool `json:"cost_includes_tax"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil || (in.Enabled == nil && in.CostIncludesTax == nil) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "send enabled and/or cost_includes_tax")
		return
	}
	var s suggestionSettings
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := loadSuggestionSettings(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO pricing_settings (merchant_id, suggestion_enabled, suggestion_cost_includes_tax, updated_by, updated_at)
			VALUES (current_setting('app.tenant_id')::uuid, COALESCE($1, false), COALESCE($2, false), $3, now())
			ON CONFLICT (merchant_id) DO UPDATE SET
			  suggestion_enabled = COALESCE($1, pricing_settings.suggestion_enabled),
			  suggestion_cost_includes_tax = COALESCE($2, pricing_settings.suggestion_cost_includes_tax),
			  updated_by = $3, updated_at = now()`, in.Enabled, in.CostIncludesTax, claims.UserID); err != nil {
			return err
		}
		if in.Enabled != nil && *in.Enabled {
			var any bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM price_suggestion_rules)`).Scan(&any); err != nil {
				return err
			}
			if !any {
				if _, err := tx.Exec(ctx, `INSERT INTO price_suggestion_rules (merchant_id, name, method, cost_type, cost_pct, rounding, created_by)
					VALUES (current_setting('app.tenant_id')::uuid, 'All products', 'cost', 'margin', 15, 'charm', $1)`, claims.UserID); err != nil {
					return err
				}
			}
		}
		if s, err = loadSuggestionSettings(ctx, tx); err != nil {
			return err
		}
		s.CanManage = true
		return audit.Log(ctx, tx, audit.Entry{EntityType: "pricing_settings", EntityID: claims.TenantID, Action: "update",
			PerformedBy: claims.UserID, Before: before, After: s, Reason: "selling price suggestions"})
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save suggestion settings")
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// ListSuggestionRules: GET /pricing/suggestion-rules?status=active|inactive
func (h *Handler) ListSuggestionRules(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	status := r.URL.Query().Get("status")
	out := []SuggestionRule{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, ruleSelect+` WHERE ($1 = '' OR r.active = ($1 = 'active'))
			ORDER BY (r.category_id IS NOT NULL AND r.brand_id IS NOT NULL) DESC, (r.category_id IS NOT NULL) DESC, (r.brand_id IS NOT NULL) DESC,
			         r.priority DESC, r.name`, status)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x SuggestionRule
			if err := scanRule(rows, &x); err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the rules")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"rules": out})
}

type ruleInput struct {
	Name           string   `json:"name"`
	CategoryID     string   `json:"category_id"`
	BrandID        string   `json:"brand_id"`
	Method         string   `json:"method"`
	CostType       string   `json:"cost_type"`
	CostPct        *float64 `json:"cost_pct"`
	MRPDiscountPct *float64 `json:"mrp_discount_pct"`
	MinMarginPct   float64  `json:"min_margin_pct"`
	Rounding       string   `json:"rounding"`
	Priority       int      `json:"priority"`
}

func (in *ruleInput) check() string {
	in.Name = strings.TrimSpace(in.Name)
	if in.CostType == "" {
		in.CostType = "margin"
	}
	if in.Rounding == "" {
		in.Rounding = "none"
	}
	switch {
	case in.Name == "":
		return "name is required"
	case in.Method != "cost" && in.Method != "mrp" && in.Method != "lower" && in.Method != "higher":
		return "method must be cost, mrp, lower or higher"
	case in.CostType != "margin" && in.CostType != "markup":
		return "cost_type must be margin or markup"
	case !roundings[in.Rounding]:
		return "rounding must be none, up_1, up_5, up_10, end_9, end_99 or charm"
	case in.Method != "mrp" && (in.CostPct == nil || *in.CostPct < 0 || (in.CostType == "margin" && *in.CostPct >= 100) || *in.CostPct >= 1000):
		return "cost_pct: a margin from 0 to under 100%, or a markup from 0 to under 1000%"
	case in.Method != "cost" && (in.MRPDiscountPct == nil || *in.MRPDiscountPct < 0 || *in.MRPDiscountPct >= 100):
		return "mrp_discount_pct: from 0 to under 100%"
	case in.MinMarginPct < 0 || in.MinMarginPct >= 100:
		return "min_margin_pct: from 0 to under 100%"
	}
	if in.Method == "mrp" {
		in.CostPct = nil
	}
	if in.Method == "cost" {
		in.MRPDiscountPct = nil
	}
	return ""
}

// SaveSuggestionRule: POST /pricing/suggestion-rules and PUT
// /pricing/suggestion-rules/{id} (pricing.suggestion.manage).
func (h *Handler) SaveSuggestionRule(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in ruleInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if msg := in.check(); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg)
		return
	}
	var out SuggestionRule
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var before *SuggestionRule
		if id != "" {
			var b SuggestionRule
			if err := scanRule(tx.QueryRow(ctx, ruleSelect+` WHERE r.id::text = $1`, id), &b); err != nil {
				return err
			}
			before = &b
			if _, err := tx.Exec(ctx, `UPDATE price_suggestion_rules SET name = $2, category_id = NULLIF($3, '')::uuid, brand_id = NULLIF($4, '')::uuid,
				method = $5, cost_type = $6, cost_pct = $7, mrp_discount_pct = $8, min_margin_pct = $9, rounding = $10, priority = $11, updated_at = now()
				WHERE id::text = $1`, id, in.Name, in.CategoryID, in.BrandID, in.Method, in.CostType, in.CostPct, in.MRPDiscountPct,
				in.MinMarginPct, in.Rounding, in.Priority); err != nil {
				return err
			}
		} else if err := tx.QueryRow(ctx, `INSERT INTO price_suggestion_rules (merchant_id, name, category_id, brand_id, method, cost_type, cost_pct,
				mrp_discount_pct, min_margin_pct, rounding, priority, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id::text`, in.Name, in.CategoryID, in.BrandID, in.Method, in.CostType, in.CostPct, in.MRPDiscountPct,
			in.MinMarginPct, in.Rounding, in.Priority, claims.UserID).Scan(&id); err != nil {
			return err
		}
		if err := scanRule(tx.QueryRow(ctx, ruleSelect+` WHERE r.id::text = $1`, id), &out); err != nil {
			return err
		}
		action := "create"
		var b any
		if before != nil {
			action, b = "update", before
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "price_suggestion_rules", EntityID: id, Action: action, PerformedBy: claims.UserID, Before: b, After: out})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "rule not found")
	case isFKViolation(err):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "choose a category and brand of this business")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the rule")
	default:
		status := http.StatusOK
		if chi.URLParam(r, "id") == "" {
			status = http.StatusCreated
		}
		httpx.JSON(w, status, out)
	}
}

// SetSuggestionRule: PATCH /pricing/suggestion-rules/{id} {active} and
// DELETE /pricing/suggestion-rules/{id} (pricing.suggestion.manage; a
// deleted rule goes to the Recycle Bin like a markup rule).
func (h *Handler) SetSuggestionRule(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	del := r.Method == http.MethodDelete
	var in struct {
		Active *bool `json:"active"`
	}
	if !del {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&in); err != nil || in.Active == nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "active is required")
			return
		}
	}
	var out SuggestionRule
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var before SuggestionRule
		if err := scanRule(tx.QueryRow(ctx, ruleSelect+` WHERE r.id::text = $1`, id), &before); err != nil {
			return err
		}
		if del {
			return softdelete.Delete(ctx, tx, "price_suggestion_rules", id, claims.UserID, "")
		}
		if _, err := tx.Exec(ctx, `UPDATE price_suggestion_rules SET active = $2, updated_at = now() WHERE id::text = $1`, id, *in.Active); err != nil {
			return err
		}
		if err := scanRule(tx.QueryRow(ctx, ruleSelect+` WHERE r.id::text = $1`, id), &out); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "price_suggestion_rules", EntityID: id, Action: "update", PerformedBy: claims.UserID, Before: before, After: out})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "rule not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the rule")
	case del:
		w.WriteHeader(http.StatusNoContent)
	default:
		httpx.JSON(w, http.StatusOK, out)
	}
}

// suggestItem: a variant (its own cost, MRP, category, brand and GST, any
// of which the request may replace — the dialog's edited cost) or, for a
// product not saved yet, the figures themselves.
type suggestItem struct {
	Key              string   `json:"key"`
	VariantID        string   `json:"variant_id"`
	CostPrice        *float64 `json:"cost_price"`
	MRP              *float64 `json:"mrp"`
	CategoryID       *string  `json:"category_id"`
	BrandID          *string  `json:"brand_id"`
	TaxSlabID        *string  `json:"tax_slab_id"`
	TaxRatePct       *float64 `json:"tax_rate_pct"`
	PriceIncludesTax *bool    `json:"price_includes_tax"`
}

type suggestResult struct {
	Key          string  `json:"key"`
	VariantID    string  `json:"variant_id,omitempty"`
	SKU          string  `json:"sku,omitempty"`
	ProductName  string  `json:"product_name,omitempty"`
	CurrentPrice float64 `json:"current_price"`
	CostPrice    float64 `json:"cost_price"`
	MRP          float64 `json:"mrp"`
	Suggestion
	taxRatePct float64
	inclusive  bool
}

func suggestFor(ctx context.Context, tx pgx.Tx, s suggestionSettings, it suggestItem) (suggestResult, error) {
	res := suggestResult{Key: it.Key, VariantID: it.VariantID}
	var in suggestIn
	var category, brand string
	if it.VariantID != "" {
		if err := tx.QueryRow(ctx, `
			SELECT pv.sku, p.name, pv.selling_price::float8, pv.cost_price::float8, pv.mrp::float8, COALESCE(p.category_id::text, ''), COALESCE(p.brand_id::text, ''),
			       (COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) + COALESCE(ts.cess_rate,0))::float8, p.price_includes_tax
			FROM product_variants pv JOIN products p ON p.id = pv.product_id LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
			WHERE pv.id::text = $1`, it.VariantID).Scan(&res.SKU, &res.ProductName, &res.CurrentPrice, &in.Cost, &in.MRP, &category, &brand,
			&in.TaxRatePct, &in.Inclusive); err != nil {
			return res, err
		}
	}
	if it.CostPrice != nil {
		in.Cost = *it.CostPrice
	}
	if it.MRP != nil {
		in.MRP = *it.MRP
	}
	if it.CategoryID != nil {
		category = *it.CategoryID
	}
	if it.BrandID != nil {
		brand = *it.BrandID
	}
	if it.TaxSlabID != nil && *it.TaxSlabID != "" {
		if err := tx.QueryRow(ctx, `SELECT (COALESCE(cgst_rate,0) + COALESCE(sgst_rate,0) + COALESCE(igst_rate,0) + COALESCE(cess_rate,0))::float8
			FROM tax_slabs WHERE id::text = $1`, *it.TaxSlabID).Scan(&in.TaxRatePct); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return res, err
		}
	} else if it.TaxRatePct != nil {
		in.TaxRatePct = *it.TaxRatePct
	}
	if it.PriceIncludesTax != nil {
		in.Inclusive = *it.PriceIncludesTax
	}
	in.CostIncludesTax = s.CostIncludesTax
	res.CostPrice, res.MRP, res.taxRatePct, res.inclusive = in.Cost, in.MRP, in.TaxRatePct, in.Inclusive
	rule, found, err := liveSuggestionRule(ctx, tx, category, brand)
	if err != nil {
		return res, err
	}
	if !found {
		res.Status, res.Note = "no_rule", "No suggestion rule covers this item"
		return res, nil
	}
	res.Suggestion = suggest(in, rule)
	return res, nil
}

// Suggest: POST /pricing/suggest {items: [...]} — up to 200 suggestions.
// Off ({enabled: false, items: []}) until the Merchant Admin switches it on.
func (h *Handler) Suggest(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var in struct {
		Items []suggestItem `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 128<<10)).Decode(&in); err != nil || len(in.Items) > 200 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "send items (up to 200)")
		return
	}
	out := []suggestResult{}
	var enabled bool
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		s, err := loadSuggestionSettings(ctx, tx)
		if err != nil || !s.Enabled {
			return err
		}
		enabled = true
		for _, it := range in.Items {
			res, err := suggestFor(ctx, tx, s, it)
			if errors.Is(err, pgx.ErrNoRows) {
				res.Status, res.Note = "no_cost", "Item not found"
			} else if err != nil {
				return err
			}
			out = append(out, res)
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not work out the suggestions")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"enabled": enabled, "items": out})
}

// ApplySuggestions: POST /pricing/suggest/preview and /pricing/suggest/apply
// {filter: {category_id, brand_id}, q?, variant_ids?, prices?: {variant_id:
// price}, reason} (apply: pricing.manage) — every item's selling price to
// its suggestion, or to the price the user typed over it (`prices`). An
// unedited suggestion is applied only when "ok" or "at MRP" (hold, check,
// no cost and no rule are skipped); an edited one whatever its status. A
// price above MRP or below cost on the net price is never applied.
func (h *Handler) ApplySuggestions(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	apply := strings.HasSuffix(r.URL.Path, "/apply")
	var in struct {
		Filter     bulkFilter         `json:"filter"`
		Q          string             `json:"q"` // name, SKU or barcode
		VariantIDs []string           `json:"variant_ids"`
		Prices     map[string]float64 `json:"prices"`
		Reason     string             `json:"reason"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		in.Reason = "Suggested price"
	}
	for _, p := range in.Prices {
		if p <= 0 {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "an edited price must be more than 0")
			return
		}
	}
	type item struct {
		suggestResult
		Result string   `json:"result"` // would_update | updated | unchanged | skipped | skipped_negative_margin | skipped_above_mrp
		Price  *float64 `json:"price"`  // what is (or would be) applied: the suggestion, or the edited price
		Edited bool     `json:"edited"`
	}
	out := []item{}
	var enabled bool
	var updated []string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		s, err := loadSuggestionSettings(ctx, tx)
		if err != nil || !s.Enabled {
			return err
		}
		enabled = true
		rows, err := tx.Query(ctx, `
			SELECT pv.id::text FROM product_variants pv JOIN products p ON p.id = pv.product_id
			WHERE pv.status = 'active' AND (COALESCE(cardinality($3::text[]), 0) = 0 OR pv.id::text = ANY($3))
			  AND ($1 = '' OR p.category_id IN (`+db.CategorySubtreeSQL+`)) AND ($2 = '' OR p.brand_id::text = $2)
			  AND ($4 = '' OR p.name ILIKE '%' || $4 || '%' OR pv.sku ILIKE '%' || $4 || '%'
			       OR EXISTS (SELECT 1 FROM barcodes b WHERE b.variant_id = pv.id AND b.code = $4))
			ORDER BY p.name, pv.sku LIMIT 2000`, in.Filter.CategoryID, in.Filter.BrandID, in.VariantIDs, strings.TrimSpace(in.Q))
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, id := range ids {
			res, err := suggestFor(ctx, tx, s, suggestItem{Key: id, VariantID: id})
			if err != nil {
				return err
			}
			x := item{suggestResult: res, Price: res.Price}
			if p, ok := in.Prices[id]; ok {
				p = round2(p)
				x.Price, x.Edited = &p, true
			}
			switch {
			case x.Price == nil || (!x.Edited && res.Status != "ok" && res.Status != "at_mrp"):
				x.Result = "skipped"
			case round2(*x.Price) == round2(res.CurrentPrice):
				x.Result = "unchanged"
			case res.MRP > 0 && *x.Price > round2(mrpInPriceTerms(res.MRP, res.taxRatePct, res.inclusive)):
				x.Result = "skipped_above_mrp"
			case taxcalc.NetPrice(*x.Price, res.taxRatePct, res.inclusive) < res.CostPrice:
				x.Result = "skipped_negative_margin" // a cost that includes GST, on a GST-inclusive price
			case !apply:
				x.Result = "would_update"
			default:
				if _, err := tx.Exec(ctx, `SAVEPOINT suggest_item`); err != nil {
					return err
				}
				p := *x.Price
				reason := in.Reason
				if x.Edited {
					reason += " (edited)"
				}
				if _, err := applyVariantPricing(ctx, tx, id, nil, nil, &p, reason, false, claims.UserID); errors.Is(err, errNegativeMargin) {
					if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT suggest_item`); err != nil {
						return err
					}
					x.Result = "skipped_negative_margin"
				} else if err != nil {
					return err
				} else {
					x.Result = "updated"
					updated = append(updated, id)
				}
			}
			out = append(out, x)
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not apply the suggestions")
		return
	}
	for _, id := range updated {
		h.reindex(r.Context(), claims.TenantID, id)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"enabled": enabled, "items": out, "updated": len(updated)})
}
