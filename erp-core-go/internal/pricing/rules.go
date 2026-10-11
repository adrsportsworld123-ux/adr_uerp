package pricing

// Phase 10: dynamic pricing (pos_frd_complete.md §11 "Dynamic Pricing":
// time-based happy hours, day-specific weekend specials, seasonal with
// auto-activation). A price rule changes a POS line's unit price itself —
// see migrations/035's header for why that's not just a promotion — and
// is resolved by ResolveLinePriceAt (resolve.go), the one place that
// decides a cart line's price.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/taxcalc"
)

var errPriceRuleInUse = errors.New("price rule has priced real sales")

type priceRuleRequest struct {
	Name       string `json:"name"`
	VariantID  string `json:"variant_id"`
	CategoryID string `json:"category_id"`
	BranchID   string `json:"branch_id"`
	RuleType   string `json:"rule_type"` // "fixed_price" | "percent"
	// fixed_price: the unit price. percent: signed adjustment to the base
	// selling price (-20 = 20% off, +10 = 10% up).
	Value      float64 `json:"value"`
	Priority   int     `json:"priority"`
	StartsAt   string  `json:"starts_at"` // RFC3339, optional
	EndsAt     string  `json:"ends_at"`   // RFC3339, optional
	DaysOfWeek []int   `json:"days_of_week"`
	TimeStart  string  `json:"time_start"` // "HH:MM", optional; with time_end
	TimeEnd    string  `json:"time_end"`
	// Override allows a rule that prices some covered variant below its
	// cost — same opt-in as PATCH /pricing/variants/{id}.
	Override bool `json:"override"`
}

type priceRule struct {
	PriceRuleID  string  `json:"price_rule_id"`
	Name         string  `json:"name"`
	VariantID    *string `json:"variant_id"`
	SKU          *string `json:"sku"`
	ProductName  *string `json:"product_name"`
	CategoryID   *string `json:"category_id"`
	CategoryName *string `json:"category_name"`
	BranchID     *string `json:"branch_id"`
	RuleType     string  `json:"rule_type"`
	Value        string  `json:"value"`
	Priority     int     `json:"priority"`
	StartsAt     *string `json:"starts_at"`
	EndsAt       *string `json:"ends_at"`
	DaysOfWeek   []int16 `json:"days_of_week"`
	TimeStart    *string `json:"time_start"`
	TimeEnd      *string `json:"time_end"`
	Active       bool    `json:"active"`
	CreatedAt    string  `json:"created_at"`
	// TimesUsed counts the sale lines this rule priced. A used rule can be
	// deactivated but not edited or deleted: its terms explain those sales.
	TimesUsed int `json:"times_used"`
}

const priceRuleSelect = `
	SELECT r.id, r.name, r.variant_id::text, pv.sku, p.name, r.category_id::text, c.name, r.branch_id::text,
	       r.rule_type, r.value::text, r.priority, r.starts_at::text, r.ends_at::text, r.days_of_week,
	       to_char(r.time_start, 'HH24:MI'), to_char(r.time_end, 'HH24:MI'), r.active, r.created_at::text,
	       (SELECT count(*) FROM sales_order_lines l WHERE l.price_rule_id = r.id)::int
	FROM price_rules r
	LEFT JOIN product_variants pv ON pv.id = r.variant_id
	LEFT JOIN products p ON p.id = pv.product_id
	LEFT JOIN categories c ON c.id = r.category_id`

func scanPriceRule(row pgx.Row, pr *priceRule) error {
	return row.Scan(&pr.PriceRuleID, &pr.Name, &pr.VariantID, &pr.SKU, &pr.ProductName, &pr.CategoryID, &pr.CategoryName,
		&pr.BranchID, &pr.RuleType, &pr.Value, &pr.Priority, &pr.StartsAt, &pr.EndsAt, &pr.DaysOfWeek,
		&pr.TimeStart, &pr.TimeEnd, &pr.Active, &pr.CreatedAt, &pr.TimesUsed)
}

// validatePriceRule returns a client-facing message, or "" when valid —
// the same rules migrations/035's CHECKs enforce, as friendly 400s.
func validatePriceRule(req priceRuleRequest) string {
	switch {
	case req.Name == "":
		return "name is required"
	case (req.VariantID == "") == (req.CategoryID == ""):
		return "exactly one of variant_id or category_id is required"
	case req.RuleType != "fixed_price" && req.RuleType != "percent":
		return "rule_type must be 'fixed_price' or 'percent'"
	case req.RuleType == "fixed_price" && req.VariantID == "":
		return "a fixed_price rule must target one variant (use a percent rule for a category)"
	case req.RuleType == "fixed_price" && req.Value < 0:
		return "a fixed price cannot be negative"
	case req.RuleType == "percent" && (req.Value < -100 || req.Value == 0):
		return "a percent rule's value must be non-zero and at least -100"
	case (req.TimeStart == "") != (req.TimeEnd == ""):
		return "time_start and time_end go together"
	}
	if req.TimeStart != "" {
		start, err1 := time.Parse("15:04", req.TimeStart)
		end, err2 := time.Parse("15:04", req.TimeEnd)
		if err1 != nil || err2 != nil {
			return "time_start/time_end must be HH:MM (24-hour)"
		}
		if start.Equal(end) {
			return "time_start and time_end can't be equal"
		}
	}
	for _, d := range req.DaysOfWeek {
		if d < 0 || d > 6 {
			return "days_of_week values are 0 (Sunday) to 6 (Saturday)"
		}
	}
	var starts, ends time.Time
	var err error
	if req.StartsAt != "" {
		if starts, err = time.Parse(time.RFC3339, req.StartsAt); err != nil {
			return "starts_at must be an RFC3339 timestamp"
		}
	}
	if req.EndsAt != "" {
		if ends, err = time.Parse(time.RFC3339, req.EndsAt); err != nil {
			return "ends_at must be an RFC3339 timestamp"
		}
	}
	if req.StartsAt != "" && req.EndsAt != "" && !starts.Before(ends) {
		return "starts_at must be before ends_at"
	}
	return ""
}

// CreatePriceRule: POST /pricing/rules
func (h *Handler) CreatePriceRule(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req priceRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if msg := validatePriceRule(req); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg)
		return
	}

	var resp priceRule
	var belowCost []string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if belowCost, err = ruleBelowCost(ctx, tx, req); err != nil {
			return err
		}
		if len(belowCost) > 0 && !req.Override {
			return errNegativeMargin
		}

		var days []int16
		for _, d := range req.DaysOfWeek {
			days = append(days, int16(d))
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO price_rules (merchant_id, name, variant_id, category_id, branch_id, rule_type, value, priority,
			                         starts_at, ends_at, days_of_week, time_start, time_end, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, NULLIF($4,'')::uuid,
			        $5, $6, $7, NULLIF($8,'')::timestamptz, NULLIF($9,'')::timestamptz, $10, NULLIF($11,'')::time, NULLIF($12,'')::time, $13)
			RETURNING id`,
			req.Name, req.VariantID, req.CategoryID, req.BranchID, req.RuleType, req.Value, req.Priority,
			req.StartsAt, req.EndsAt, days, req.TimeStart, req.TimeEnd, claims.UserID,
		).Scan(&id); err != nil {
			return err
		}
		return scanPriceRule(tx.QueryRow(ctx, priceRuleSelect+` WHERE r.id = $1`, id), &resp)
	})

	switch {
	case errors.Is(err, errNegativeMargin):
		httpx.ErrorWithDetails(w, http.StatusConflict, "NEGATIVE_MARGIN",
			fmt.Sprintf("this rule would sell %d variant(s) below cost — pass override:true to allow it anyway", len(belowCost)),
			map[string]any{"below_cost_skus": belowCost})
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "variant not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create price rule")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// ListPriceRules: GET /pricing/rules?active=true|false&variant_id=&category_id=
func (h *Handler) ListPriceRules(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	rules := []priceRule{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, priceRuleSelect+`
			WHERE ($1 = '' OR r.active = ($1 = 'true'))
			  AND ($2 = '' OR r.variant_id::text = $2)
			  AND ($3 = '' OR r.category_id::text = $3)
			ORDER BY r.active DESC, r.created_at DESC`, q.Get("active"), q.Get("variant_id"), q.Get("category_id"))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var pr priceRule
			if err := scanPriceRule(rows, &pr); err != nil {
				return err
			}
			rules = append(rules, pr)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list price rules")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"rules": rules})
}

// SetPriceRuleActive: PATCH /pricing/rules/{id} {"active": bool} — the
// way to retire a rule that has already priced sales (see DeletePriceRule).
// Other fields aren't editable: a rule's terms are what a past sale was
// priced under, so changing them means a new rule.
func (h *Handler) SetPriceRuleActive(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		Active *bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Active == nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "active (true/false) is required")
		return
	}
	id := chi.URLParam(r, "id")
	var resp priceRule
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE price_rules SET active = $1 WHERE id = $2`, *req.Active, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return scanPriceRule(tx.QueryRow(ctx, priceRuleSelect+` WHERE r.id = $1`, id), &resp)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "price rule not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update price rule")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// DeletePriceRule: DELETE /pricing/rules/{id} — refuses (409
// PRICE_RULE_IN_USE) once the rule has priced a real sale line, the same
// shape as DELETE /pricing/price-lists/{id}'s PRICE_LIST_IN_USE; deactivate
// it instead.
func (h *Handler) DeletePriceRule(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var used bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sales_order_lines WHERE price_rule_id = $1)`, id).Scan(&used); err != nil {
			return err
		}
		if used {
			return errPriceRuleInUse
		}
		tag, err := tx.Exec(ctx, `DELETE FROM price_rules WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
	switch {
	case errors.Is(err, errPriceRuleInUse):
		httpx.Error(w, http.StatusConflict, "PRICE_RULE_IN_USE", "this rule has priced real sales — deactivate it instead of deleting it")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "price rule not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not delete price rule")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// GetEffectivePrice: GET /pricing/variants/{id}/effective-price
// ?branch_id=&customer_id=&at= — what a POS line would be priced at, and
// why (price list, which rule, or base). at (RFC3339, default now) lets
// an admin preview a rule: "what does this cost Saturday at 3 PM?".
func (h *Handler) GetEffectivePrice(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	variantID := chi.URLParam(r, "id")
	q := r.URL.Query()
	at := time.Now()
	if v := q.Get("at"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "at must be an RFC3339 timestamp")
			return
		}
		at = parsed
	}

	var lp LinePrice
	var base string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if lp, err = ResolveLinePriceAt(ctx, tx, q.Get("customer_id"), variantID, q.Get("branch_id"), at); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT selling_price::text FROM product_variants WHERE id = $1`, variantID).Scan(&base)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "variant not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not resolve price")
	default:
		resp := map[string]any{
			"variant_id": variantID, "at": at.Format(time.RFC3339),
			"unit_price": strconv.FormatFloat(lp.UnitPrice, 'f', 2, 64), "base_selling_price": base,
			"source": lp.Source, "price_rule_id": lp.PriceRuleID,
		}
		if lp.RuleName != "" {
			resp["price_rule_name"] = lp.RuleName
		}
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// ruleBelowCost is the margin guard over every variant a rule covers, on
// the NET price: the same rule PATCH /pricing/variants/{id} applies, just
// across a category when the rule targets one. Returns the SKUs the rule
// would sell below cost, or pgx.ErrNoRows when a variant rule's variant
// doesn't exist.
func ruleBelowCost(ctx context.Context, tx pgx.Tx, req priceRuleRequest) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT pv.sku, pv.selling_price, pv.mrp, pv.cost_price, p.price_includes_tax,
		       COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) + COALESCE(ts.cess_rate,0)
		FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		WHERE pv.id = NULLIF($1,'')::uuid OR p.category_id = NULLIF($2,'')::uuid`, req.VariantID, req.CategoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var belowCost []string
	covered := 0
	for rows.Next() {
		var sku string
		var selling, mrp, cost, taxRate float64
		var inclusive bool
		if err := rows.Scan(&sku, &selling, &mrp, &cost, &inclusive, &taxRate); err != nil {
			return nil, err
		}
		covered++
		price := applyRule(selling, mrp, req.RuleType, req.Value)
		if taxcalc.NetPrice(price, taxRate, inclusive) < cost {
			belowCost = append(belowCost, sku)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if req.VariantID != "" && covered == 0 {
		return nil, pgx.ErrNoRows
	}
	return belowCost, nil
}

// UpdatePriceRule: PUT /pricing/rules/{id} — replaces a rule's terms, with
// create's validation and margin guard. Refused (409 PRICE_RULE_IN_USE)
// once the rule has priced a sale: those sales were priced under its
// current terms, so deactivate it and create a new rule instead. The
// active flag is left as it is (PATCH sets it).
func (h *Handler) UpdatePriceRule(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req priceRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if msg := validatePriceRule(req); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg)
		return
	}
	id := chi.URLParam(r, "id")

	var resp priceRule
	var belowCost []string
	var ruleMissing bool
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var used bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM sales_order_lines WHERE price_rule_id = r.id)
			FROM price_rules r WHERE r.id = $1 FOR UPDATE`, id).Scan(&used); err != nil {
			ruleMissing = errors.Is(err, pgx.ErrNoRows)
			return err
		}
		if used {
			return errPriceRuleInUse
		}
		var err error
		if belowCost, err = ruleBelowCost(ctx, tx, req); err != nil {
			return err
		}
		if len(belowCost) > 0 && !req.Override {
			return errNegativeMargin
		}
		var days []int16
		for _, d := range req.DaysOfWeek {
			days = append(days, int16(d))
		}
		if _, err := tx.Exec(ctx, `
			UPDATE price_rules SET name = $1, variant_id = NULLIF($2,'')::uuid, category_id = NULLIF($3,'')::uuid,
			       branch_id = NULLIF($4,'')::uuid, rule_type = $5, value = $6, priority = $7,
			       starts_at = NULLIF($8,'')::timestamptz, ends_at = NULLIF($9,'')::timestamptz, days_of_week = $10,
			       time_start = NULLIF($11,'')::time, time_end = NULLIF($12,'')::time
			WHERE id = $13`,
			req.Name, req.VariantID, req.CategoryID, req.BranchID, req.RuleType, req.Value, req.Priority,
			req.StartsAt, req.EndsAt, days, req.TimeStart, req.TimeEnd, id); err != nil {
			return err
		}
		return scanPriceRule(tx.QueryRow(ctx, priceRuleSelect+` WHERE r.id = $1`, id), &resp)
	})

	switch {
	case errors.Is(err, errPriceRuleInUse):
		httpx.Error(w, http.StatusConflict, "PRICE_RULE_IN_USE", "this rule has priced real sales — deactivate it and create a new rule instead")
	case errors.Is(err, errNegativeMargin):
		httpx.ErrorWithDetails(w, http.StatusConflict, "NEGATIVE_MARGIN",
			fmt.Sprintf("this rule would sell %d variant(s) below cost — pass override:true to allow it anyway", len(belowCost)),
			map[string]any{"below_cost_skus": belowCost})
	case errors.Is(err, pgx.ErrNoRows) && ruleMissing:
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "price rule not found")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "variant not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update price rule")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}
