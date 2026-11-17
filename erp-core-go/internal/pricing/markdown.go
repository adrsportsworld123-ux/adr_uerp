package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/taxcalc"
)

// Markdown plans (migrations/084, §3.87): apparel's season-end clearance,
// a collection's prices stepped down on dates — 20% off from the 1st, 40%
// from the 15th, until the 31st. Each step is an ordinary percent price
// rule scoped to the collection and tagged with its plan, so it is priced
// by ResolveLinePriceAt like any rule (a variant rule still beats it), and
// it lapses on its own date. Steps change with their plan: cancel it and
// make another.

var (
	errMarkdownExists   = errors.New("this collection already has a live markdown plan for that branch — cancel it first")
	errMarkdownBelow    = errors.New("markdown sells below cost")
	errPlanNotActive    = errors.New("this markdown plan is already cancelled")
	errNoSuchCollection = errors.New("collection not found")
)

const maxMarkdownSteps = 10

type markdownStepRequest struct {
	StartsOn   string  `json:"starts_on"` // YYYY-MM-DD, the branch's (or merchant's) day
	PercentOff float64 `json:"percent_off"`
}

type markdownPlanRequest struct {
	Name     string                `json:"name"`
	BranchID string                `json:"branch_id"`
	Steps    []markdownStepRequest `json:"steps"`
	EndsOn   string                `json:"ends_on"` // the day it stops; "" = the last step runs on
	Override bool                  `json:"override"`
}

type markdownStep struct {
	PriceRuleID string  `json:"price_rule_id"`
	PercentOff  string  `json:"percent_off"`
	StartsAt    string  `json:"starts_at"`
	EndsAt      *string `json:"ends_at"`
	// State: upcoming | live | ended (live only while the plan is active
	// and the step isn't switched off).
	State     string `json:"state"`
	TimesUsed int    `json:"times_used"`
}

type markdownPlan struct {
	PlanID         string         `json:"markdown_plan_id"`
	CollectionID   string         `json:"collection_id"`
	CollectionName string         `json:"collection_name"`
	BranchID       *string        `json:"branch_id"`
	BranchName     *string        `json:"branch_name"`
	Name           string         `json:"name"`
	Status         string         `json:"status"`
	CreatedAt      string         `json:"created_at"`
	CancelledAt    *string        `json:"cancelled_at"`
	Steps          []markdownStep `json:"steps"`
	// Products: how many products the collection holds now.
	Products int `json:"products"`
}

// validateMarkdown returns a client-facing message, or "".
func validateMarkdown(req *markdownPlanRequest) string {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "Markdown"
	}
	if len(req.Steps) == 0 || len(req.Steps) > maxMarkdownSteps {
		return fmt.Sprintf("a markdown plan has 1 to %d steps", maxMarkdownSteps)
	}
	var prev time.Time
	for i, st := range req.Steps {
		d, err := time.Parse("2006-01-02", st.StartsOn)
		if err != nil {
			return fmt.Sprintf("step %d: starts_on must be YYYY-MM-DD", i+1)
		}
		if i > 0 && !d.After(prev) {
			return fmt.Sprintf("step %d starts on or before the step before it", i+1)
		}
		if st.PercentOff <= 0 || st.PercentOff > 90 {
			return fmt.Sprintf("step %d: percent_off is above 0 and at most 90", i+1)
		}
		prev = d
	}
	if req.EndsOn != "" {
		d, err := time.Parse("2006-01-02", req.EndsOn)
		if err != nil {
			return "ends_on must be YYYY-MM-DD"
		}
		if !d.After(prev) {
			return "ends_on must be after the last step starts"
		}
	}
	return ""
}

// zoneSQL: the day boundary a plan's dates mean — the branch's zone, or the merchant's.
const zoneSQL = `COALESCE((SELECT timezone FROM branches WHERE id = NULLIF($1,'')::uuid), app_merchant_tz())`

// markdownBelowCost: the collection's SKUs the deepest step sells below cost (net of tax).
func markdownBelowCost(ctx context.Context, tx pgx.Tx, collectionID string, deepest float64) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT pv.sku, pv.selling_price, pv.mrp, pv.cost_price, p.price_includes_tax,
		       COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) + COALESCE(ts.cess_rate,0)
		FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		WHERE p.collection_id::text = $1 AND pv.status <> 'discontinued'`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var below []string
	for rows.Next() {
		var sku string
		var selling, mrp, cost, taxRate float64
		var inclusive bool
		if err := rows.Scan(&sku, &selling, &mrp, &cost, &inclusive, &taxRate); err != nil {
			return nil, err
		}
		if cost > 0 && taxcalc.NetPrice(applyRule(selling, mrp, "percent", -deepest), taxRate, inclusive) < cost {
			below = append(below, sku)
		}
	}
	return below, rows.Err()
}

func loadMarkdownPlans(ctx context.Context, tx pgx.Tx, where string, arg string) ([]markdownPlan, error) {
	rows, err := tx.Query(ctx, `
		SELECT mp.id::text, mp.collection_id::text, c.name, mp.branch_id::text, b.name, mp.name, mp.status,
		       mp.created_at, mp.cancelled_at,
		       (SELECT count(*) FROM products p WHERE p.collection_id = mp.collection_id)::int
		FROM markdown_plans mp
		JOIN collections c ON c.id = mp.collection_id
		LEFT JOIN branches b ON b.id = mp.branch_id
		WHERE `+where+`
		ORDER BY mp.created_at DESC`, arg)
	if err != nil {
		return nil, err
	}
	plans := []markdownPlan{}
	for rows.Next() {
		var p markdownPlan
		var created time.Time
		var cancelled *time.Time
		if err := rows.Scan(&p.PlanID, &p.CollectionID, &p.CollectionName, &p.BranchID, &p.BranchName, &p.Name, &p.Status,
			&created, &cancelled, &p.Products); err != nil {
			rows.Close()
			return nil, err
		}
		p.CreatedAt = created.UTC().Format(time.RFC3339)
		if cancelled != nil {
			s := cancelled.UTC().Format(time.RFC3339)
			p.CancelledAt = &s
		}
		plans = append(plans, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range plans {
		srows, err := tx.Query(ctx, `
			SELECT r.id::text, (-r.value)::text, r.starts_at, r.ends_at,
			       CASE WHEN NOT r.active OR $2 <> 'active' THEN CASE WHEN r.starts_at > now() THEN 'upcoming' ELSE 'ended' END
			            WHEN r.starts_at > now() THEN 'upcoming'
			            WHEN r.ends_at IS NOT NULL AND r.ends_at <= now() THEN 'ended'
			            ELSE 'live' END,
			       (SELECT count(*) FROM sales_order_lines l WHERE l.price_rule_id = r.id)::int
			FROM price_rules r WHERE r.markdown_plan_id::text = $1 ORDER BY r.starts_at`, plans[i].PlanID, plans[i].Status)
		if err != nil {
			return nil, err
		}
		plans[i].Steps = []markdownStep{}
		for srows.Next() {
			var st markdownStep
			var starts time.Time
			var ends *time.Time
			if err := srows.Scan(&st.PriceRuleID, &st.PercentOff, &starts, &ends, &st.State, &st.TimesUsed); err != nil {
				srows.Close()
				return nil, err
			}
			st.StartsAt = starts.UTC().Format(time.RFC3339)
			if ends != nil {
				e := ends.UTC().Format(time.RFC3339)
				st.EndsAt = &e
			}
			plans[i].Steps = append(plans[i].Steps, st)
		}
		srows.Close()
		if err := srows.Err(); err != nil {
			return nil, err
		}
	}
	return plans, nil
}

// ListMarkdownPlans: GET /collections/{id}/markdown-plans — newest first, each with its steps.
func (h *Handler) ListMarkdownPlans(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var plans []markdownPlan
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		plans, err = loadMarkdownPlans(ctx, tx, `mp.collection_id::text = $1`, chi.URLParam(r, "id"))
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list markdown plans")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"plans": plans})
}

// CreateMarkdownPlan: POST /collections/{id}/markdown-plans (pricing.manage)
// {name, branch_id?, steps: [{starts_on, percent_off}], ends_on?, override?}.
// A step selling the collection below cost needs override (409
// NEGATIVE_MARGIN with the SKUs), as a price rule does.
func (h *Handler) CreateMarkdownPlan(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	collectionID := chi.URLParam(r, "id")
	var req markdownPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if msg := validateMarkdown(&req); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg)
		return
	}
	var plan markdownPlan
	var below []string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM collections WHERE id::text = $1)`, collectionID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errNoSuchCollection
		}
		if req.BranchID != "" {
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM branches WHERE id::text = $1)`, req.BranchID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return errNoSuchCollection
			}
		}
		// One live plan per collection and branch scope: two would fight
		// over the same items.
		var live bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM markdown_plans mp WHERE mp.collection_id::text = $1 AND mp.status = 'active'
			               AND mp.branch_id IS NOT DISTINCT FROM NULLIF($2,'')::uuid
			               AND EXISTS (SELECT 1 FROM price_rules r WHERE r.markdown_plan_id = mp.id AND r.active
			                           AND (r.ends_at IS NULL OR r.ends_at > now())))`, collectionID, req.BranchID).Scan(&live); err != nil {
			return err
		}
		if live {
			return errMarkdownExists
		}
		deepest := 0.0
		for _, st := range req.Steps {
			deepest = max(deepest, st.PercentOff)
		}
		var err error
		if below, err = markdownBelowCost(ctx, tx, collectionID, deepest); err != nil {
			return err
		}
		if len(below) > 0 && !req.Override {
			return errMarkdownBelow
		}
		var planID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO markdown_plans (merchant_id, collection_id, branch_id, name, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, NULLIF($2,'')::uuid, $3, $4) RETURNING id::text`,
			collectionID, req.BranchID, req.Name, claims.UserID).Scan(&planID); err != nil {
			return err
		}
		for i, st := range req.Steps {
			end := req.EndsOn
			if i+1 < len(req.Steps) {
				end = req.Steps[i+1].StartsOn
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO price_rules (merchant_id, name, collection_id, branch_id, rule_type, value, priority, starts_at, ends_at,
				                         created_by, markdown_plan_id)
				VALUES (current_setting('app.tenant_id')::uuid, $2, $3::uuid, NULLIF($1,'')::uuid, 'percent', $4, 0,
				        ($5::date)::timestamp AT TIME ZONE `+zoneSQL+`,
				        CASE WHEN $6 = '' THEN NULL ELSE ($6::date)::timestamp AT TIME ZONE `+zoneSQL+` END,
				        $7, $8::uuid)`,
				req.BranchID, fmt.Sprintf("%s · %g%% off", req.Name, st.PercentOff), collectionID, -st.PercentOff,
				st.StartsOn, end, claims.UserID, planID); err != nil {
				return err
			}
		}
		if err := audit.Log(ctx, tx, audit.Entry{EntityType: "markdown_plans", EntityID: planID, Action: "create", PerformedBy: claims.UserID,
			After: map[string]any{"collection_id": collectionID, "branch_id": req.BranchID, "name": req.Name, "steps": req.Steps, "ends_on": req.EndsOn,
				"below_cost_skus": below}}); err != nil {
			return err
		}
		plans, err := loadMarkdownPlans(ctx, tx, `mp.id::text = $1`, planID)
		if err == nil && len(plans) == 1 {
			plan = plans[0]
		}
		return err
	})
	switch {
	case errors.Is(err, errNoSuchCollection):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "collection or branch not found")
	case errors.Is(err, errMarkdownExists):
		httpx.Error(w, http.StatusConflict, "MARKDOWN_EXISTS", errMarkdownExists.Error())
	case errors.Is(err, errMarkdownBelow):
		httpx.ErrorWithDetails(w, http.StatusConflict, "NEGATIVE_MARGIN",
			fmt.Sprintf("the deepest step sells %d variant(s) below cost — pass override:true to allow it anyway", len(below)),
			map[string]any{"below_cost_skus": below})
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create the markdown plan")
	default:
		httpx.JSON(w, http.StatusCreated, plan)
	}
}

// CancelMarkdownPlan: POST /markdown-plans/{id}/cancel (pricing.manage) —
// every step stops at once; the collection sells at its own prices again.
// Sales already made keep the price they were made at.
func (h *Handler) CancelMarkdownPlan(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var plan markdownPlan
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM markdown_plans WHERE id::text = $1 FOR UPDATE`, id).Scan(&status); err != nil {
			return err
		}
		if status != "active" {
			return errPlanNotActive
		}
		if _, err := tx.Exec(ctx, `UPDATE markdown_plans SET status = 'cancelled', cancelled_at = now(), cancelled_by = $2 WHERE id::text = $1`,
			id, claims.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE price_rules SET active = false WHERE markdown_plan_id::text = $1`, id); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, audit.Entry{EntityType: "markdown_plans", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Before: map[string]any{"status": "active"}, After: map[string]any{"status": "cancelled"}}); err != nil {
			return err
		}
		plans, err := loadMarkdownPlans(ctx, tx, `mp.id::text = $1`, id)
		if err == nil && len(plans) == 1 {
			plan = plans[0]
		}
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "markdown plan not found")
	case errors.Is(err, errPlanNotActive):
		httpx.Error(w, http.StatusConflict, "INVALID_STATE", errPlanNotActive.Error())
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not cancel the markdown plan")
	default:
		httpx.JSON(w, http.StatusOK, plan)
	}
}
