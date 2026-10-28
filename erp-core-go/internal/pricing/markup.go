package pricing

// Markup rules (migrations/071): a price worked out from the cost price,
// separate from price rules (which adjust the selling price). How a line
// uses it depends on the branch's price benefit (ResolveLinePriceAt).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/softdelete"
)

var errMarkupRuleInUse = errors.New("markup rule has priced real sales")

type markupRuleRequest struct {
	Name       string  `json:"name"`
	VariantID  string  `json:"variant_id"`  // one product variant, or
	CategoryID string  `json:"category_id"` // a category and its sub-categories; neither = every product
	BranchID   string  `json:"branch_id"`   // "" = every branch
	MarkupType string  `json:"markup_type"` // "percent" | "fixed"
	Value      float64 `json:"value"`       // % or ₹ over the cost price
	Priority   int     `json:"priority"`
	StartsAt   string  `json:"starts_at"`
	EndsAt     string  `json:"ends_at"`
	DaysOfWeek []int   `json:"days_of_week"`
	TimeStart  string  `json:"time_start"`
	TimeEnd    string  `json:"time_end"`
}

type markupRule struct {
	MarkupRuleID string  `json:"markup_rule_id"`
	Name         string  `json:"name"`
	VariantID    *string `json:"variant_id"`
	SKU          *string `json:"sku"`
	ProductName  *string `json:"product_name"`
	CategoryID   *string `json:"category_id"`
	CategoryName *string `json:"category_name"`
	BranchID     *string `json:"branch_id"`
	BranchName   *string `json:"branch_name"`
	MarkupType   string  `json:"markup_type"`
	Value        string  `json:"value"`
	Priority     int     `json:"priority"`
	StartsAt     *string `json:"starts_at"`
	EndsAt       *string `json:"ends_at"`
	DaysOfWeek   []int16 `json:"days_of_week"`
	TimeStart    *string `json:"time_start"`
	TimeEnd      *string `json:"time_end"`
	Active       bool    `json:"active"`
	CreatedAt    string  `json:"created_at"`
	// TimesUsed: sale lines this rule priced. A used rule can be switched
	// off but not edited or deleted.
	TimesUsed int `json:"times_used"`
}

const markupRuleSelect = `
	SELECT r.id, r.name, r.variant_id::text, pv.sku, p.name, r.category_id::text, c.name, r.branch_id::text, b.name,
	       r.markup_type, r.value::text, r.priority, r.starts_at::text, r.ends_at::text, r.days_of_week,
	       to_char(r.time_start, 'HH24:MI'), to_char(r.time_end, 'HH24:MI'), r.active, r.created_at::text,
	       (SELECT count(*) FROM sales_order_lines l WHERE l.markup_rule_id = r.id)::int
	FROM markup_rules r
	LEFT JOIN product_variants pv ON pv.id = r.variant_id
	LEFT JOIN products p ON p.id = pv.product_id
	LEFT JOIN categories c ON c.id = r.category_id
	LEFT JOIN branches b ON b.id = r.branch_id`

func scanMarkupRule(row pgx.Row, m *markupRule) error {
	return row.Scan(&m.MarkupRuleID, &m.Name, &m.VariantID, &m.SKU, &m.ProductName, &m.CategoryID, &m.CategoryName,
		&m.BranchID, &m.BranchName, &m.MarkupType, &m.Value, &m.Priority, &m.StartsAt, &m.EndsAt, &m.DaysOfWeek,
		&m.TimeStart, &m.TimeEnd, &m.Active, &m.CreatedAt, &m.TimesUsed)
}

func validateMarkupRule(req *markupRuleRequest) string {
	req.Name = strings.TrimSpace(req.Name)
	switch {
	case req.Name == "":
		return "name is required"
	case req.VariantID != "" && req.CategoryID != "":
		return "choose one product or one category (or neither, for every product)"
	case req.MarkupType != "percent" && req.MarkupType != "fixed":
		return "markup_type must be 'percent' or 'fixed'"
	case req.Value <= 0:
		return "the markup must be above 0"
	case req.MarkupType == "percent" && req.Value > 1000:
		return "a percentage markup can be at most 1000%"
	}
	// The schedule fields have the same rules as a price rule's.
	return validatePriceRule(priceRuleRequest{Name: req.Name, VariantID: "x", RuleType: "percent", Value: 1,
		StartsAt: req.StartsAt, EndsAt: req.EndsAt, DaysOfWeek: req.DaysOfWeek, TimeStart: req.TimeStart, TimeEnd: req.TimeEnd})
}

func decodeMarkupRule(w http.ResponseWriter, r *http.Request) (markupRuleRequest, bool) {
	var req markupRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return req, false
	}
	if msg := validateMarkupRule(&req); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg)
		return req, false
	}
	return req, true
}

func int16Days(days []int) []int16 {
	var out []int16
	for _, d := range days {
		out = append(out, int16(d))
	}
	return out
}

// CreateMarkupRule: POST /pricing/markup-rules (pricing.manage).
func (h *Handler) CreateMarkupRule(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	req, ok := decodeMarkupRule(w, r)
	if !ok {
		return
	}
	var resp markupRule
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO markup_rules (merchant_id, name, variant_id, category_id, branch_id, markup_type, value, priority,
			                          starts_at, ends_at, days_of_week, time_start, time_end, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, NULLIF($4,'')::uuid,
			        $5, $6, $7, NULLIF($8,'')::timestamptz, NULLIF($9,'')::timestamptz, $10, NULLIF($11,'')::time, NULLIF($12,'')::time, $13)
			RETURNING id`,
			req.Name, req.VariantID, req.CategoryID, req.BranchID, req.MarkupType, req.Value, req.Priority,
			req.StartsAt, req.EndsAt, int16Days(req.DaysOfWeek), req.TimeStart, req.TimeEnd, claims.UserID,
		).Scan(&id); err != nil {
			return err
		}
		return scanMarkupRule(tx.QueryRow(ctx, markupRuleSelect+` WHERE r.id = $1`, id), &resp)
	})
	switch {
	case isFKViolation(err):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "the product, category or branch was not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create the markup rule")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// ListMarkupRules: GET /pricing/markup-rules?active=true|false
func (h *Handler) ListMarkupRules(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	rules := []markupRule{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, markupRuleSelect+`
			WHERE ($1 = '' OR r.active = ($1 = 'true'))
			ORDER BY r.active DESC, r.created_at DESC`, r.URL.Query().Get("active"))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m markupRule
			if err := scanMarkupRule(rows, &m); err != nil {
				return err
			}
			rules = append(rules, m)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list markup rules")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"rules": rules})
}

// UpdateMarkupRule: PUT /pricing/markup-rules/{id} — replaces its terms;
// refused once it has priced a sale (switch it off and make a new one).
func (h *Handler) UpdateMarkupRule(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	req, ok := decodeMarkupRule(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	var resp markupRule
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var used bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM sales_order_lines WHERE markup_rule_id = r.id)
			FROM markup_rules r WHERE r.id::text = $1 FOR UPDATE`, id).Scan(&used); err != nil {
			return err
		}
		if used {
			return errMarkupRuleInUse
		}
		if _, err := tx.Exec(ctx, `
			UPDATE markup_rules SET name = $1, variant_id = NULLIF($2,'')::uuid, category_id = NULLIF($3,'')::uuid,
			       branch_id = NULLIF($4,'')::uuid, markup_type = $5, value = $6, priority = $7,
			       starts_at = NULLIF($8,'')::timestamptz, ends_at = NULLIF($9,'')::timestamptz, days_of_week = $10,
			       time_start = NULLIF($11,'')::time, time_end = NULLIF($12,'')::time
			WHERE id::text = $13`,
			req.Name, req.VariantID, req.CategoryID, req.BranchID, req.MarkupType, req.Value, req.Priority,
			req.StartsAt, req.EndsAt, int16Days(req.DaysOfWeek), req.TimeStart, req.TimeEnd, id); err != nil {
			return err
		}
		return scanMarkupRule(tx.QueryRow(ctx, markupRuleSelect+` WHERE r.id::text = $1`, id), &resp)
	})
	switch {
	case errors.Is(err, errMarkupRuleInUse):
		httpx.Error(w, http.StatusConflict, "MARKUP_RULE_IN_USE", "this rule has priced real sales — switch it off and create a new rule instead")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "markup rule not found")
	case isFKViolation(err):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "the product, category or branch was not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update the markup rule")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// SetMarkupRuleActive: PATCH /pricing/markup-rules/{id} {"active": bool}
func (h *Handler) SetMarkupRuleActive(w http.ResponseWriter, r *http.Request) {
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
	var resp markupRule
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE markup_rules SET active = $1 WHERE id::text = $2`, *req.Active, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return scanMarkupRule(tx.QueryRow(ctx, markupRuleSelect+` WHERE r.id::text = $1`, id), &resp)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "markup rule not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update the markup rule")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// DeleteMarkupRule: DELETE /pricing/markup-rules/{id} — soft delete,
// refused (409 MARKUP_RULE_IN_USE) once it has priced a sale.
func (h *Handler) DeleteMarkupRule(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var used bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sales_order_lines WHERE markup_rule_id::text = $1)`, id).Scan(&used); err != nil {
			return err
		}
		if used {
			return errMarkupRuleInUse
		}
		return softdelete.Delete(ctx, tx, "markup_rules", id, claims.UserID, "")
	})
	switch {
	case errors.Is(err, errMarkupRuleInUse):
		httpx.Error(w, http.StatusConflict, "MARKUP_RULE_IN_USE", "this rule has priced real sales — switch it off instead of deleting it")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "markup rule not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not delete the markup rule")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// liveMarkup: the markup rule that prices variantID at branchID at `at`,
// if any: a variant rule, then one on the product's category or a
// category above it, then an all-products rule; then priority, newest.
func liveMarkup(ctx context.Context, tx pgx.Tx, variantID, categoryID, branchID string, at time.Time) (id, name, markupType string, value float64, found bool, err error) {
	err = tx.QueryRow(ctx, `
		WITH RECURSIVE up AS (
			SELECT id, parent_id FROM categories WHERE id = NULLIF($2,'')::uuid
			UNION ALL
			SELECT c.id, c.parent_id FROM categories c JOIN up ON c.id = up.parent_id
		), local AS (
			SELECT $4::timestamptz AT TIME ZONE COALESCE(
				(SELECT timezone FROM branches WHERE id = NULLIF($3,'')::uuid), app_merchant_tz()) AS ts
		)
		SELECT r.id::text, r.name, r.markup_type, r.value::float8
		FROM markup_rules r CROSS JOIN local
		WHERE r.active
		  AND (r.variant_id = $1::uuid OR r.category_id IN (SELECT id FROM up) OR (r.variant_id IS NULL AND r.category_id IS NULL))
		  AND (r.branch_id IS NULL OR r.branch_id = NULLIF($3,'')::uuid)
		  AND (r.starts_at IS NULL OR r.starts_at <= $4::timestamptz)
		  AND (r.ends_at IS NULL OR $4::timestamptz < r.ends_at)
		  AND (r.days_of_week IS NULL OR EXTRACT(DOW FROM local.ts)::smallint = ANY(r.days_of_week))
		  AND (r.time_start IS NULL OR CASE
		         WHEN r.time_start <= r.time_end THEN local.ts::time >= r.time_start AND local.ts::time < r.time_end
		         ELSE local.ts::time >= r.time_start OR local.ts::time < r.time_end
		       END)
		ORDER BY (r.variant_id IS NOT NULL) DESC, (r.category_id IS NOT NULL) DESC, r.priority DESC, r.created_at DESC
		LIMIT 1`, variantID, categoryID, branchID, at).Scan(&id, &name, &markupType, &value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", 0, false, nil
	}
	return id, name, markupType, value, err == nil, err
}

// markupPrice: cost plus the markup, with GST added for a product whose
// prices include it, never above MRP. ok=false when there is no cost.
func markupPrice(cost, mrp, taxRate float64, inclusive bool, markupType string, value float64) (float64, bool) {
	if cost <= 0 {
		return 0, false
	}
	price := cost + value
	if markupType == "percent" {
		price = cost * (1 + value/100)
	}
	if inclusive {
		price *= 1 + taxRate/100
	}
	price = round2(price)
	if mrp > 0 && price > mrp {
		price = mrp
	}
	return price, true
}
