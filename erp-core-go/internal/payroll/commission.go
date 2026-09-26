// Package payroll implements Phase 5's commission engine and payroll run
// (phased_roadmap.md; pos_frd_complete.md's HR section: "Commission
// engine (net-sales-after-returns model, category-weighted, tiered)",
// "Payroll run (earnings/deductions, statutory: PF/ESI/TDS/PT/LWF,
// challan generation)"). Reads employees/salary structures
// (internal/hr's tables) and sales data (internal/sales' tables)
// directly by SQL rather than importing those packages — the same
// cross-domain-read pattern internal/gst and internal/einvoice already
// use, so this package never needs those packages' Go types, just their
// already-committed rows.
package payroll

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

type tier struct {
	MinNetSales float64 `json:"min_net_sales"`
	RatePercent float64 `json:"rate_percent"`
}

type commissionRuleResponse struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	CategoryID *string `json:"category_id,omitempty"`
	Tiers      []tier  `json:"tiers"`
	Status     string  `json:"status"`
}

type createCommissionRuleRequest struct {
	Name       string  `json:"name"`
	CategoryID *string `json:"category_id"`
	Tiers      []tier  `json:"tiers"`
}

// CreateCommissionRule: POST /payroll/commission-rules
func (h *Handler) CreateCommissionRule(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createCommissionRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Name == "" || len(req.Tiers) == 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name and at least one tier are required")
		return
	}
	tiersJSON, err := json.Marshal(req.Tiers)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid tiers")
		return
	}

	var resp commissionRuleResponse
	var tiersOut []byte
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO commission_rules (id, merchant_id, name, category_id, tiers, status)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3::jsonb, 'active')
			RETURNING id, name, category_id, tiers, status`,
			req.Name, req.CategoryID, tiersJSON,
		).Scan(&resp.ID, &resp.Name, &resp.CategoryID, &tiersOut, &resp.Status)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create commission rule")
		return
	}
	_ = json.Unmarshal(tiersOut, &resp.Tiers)
	httpx.JSON(w, http.StatusCreated, resp)
}

// ListCommissionRules: GET /payroll/commission-rules
func (h *Handler) ListCommissionRules(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	rules := []commissionRuleResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, category_id, tiers, status FROM commission_rules ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var rule commissionRuleResponse
			var tiersRaw []byte
			if err := rows.Scan(&rule.ID, &rule.Name, &rule.CategoryID, &tiersRaw, &rule.Status); err != nil {
				return err
			}
			_ = json.Unmarshal(tiersRaw, &rule.Tiers)
			rules = append(rules, rule)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list commission rules")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"commission_rules": rules})
}

type updateCommissionRuleRequest struct {
	Name   *string `json:"name"`
	Tiers  []tier  `json:"tiers"`
	Status *string `json:"status"`
}

// UpdateCommissionRule: PATCH /payroll/commission-rules/{id}
func (h *Handler) UpdateCommissionRule(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req updateCommissionRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var tiersArg any
	if req.Tiers != nil {
		tj, err := json.Marshal(req.Tiers)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid tiers")
			return
		}
		tiersArg = tj
	}

	var resp commissionRuleResponse
	var tiersOut []byte
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			UPDATE commission_rules SET
			  name = COALESCE($2, name),
			  tiers = COALESCE($3::jsonb, tiers),
			  status = COALESCE($4, status)
			WHERE id = $1
			RETURNING id, name, category_id, tiers, status`,
			id, req.Name, tiersArg, req.Status,
		).Scan(&resp.ID, &resp.Name, &resp.CategoryID, &tiersOut, &resp.Status)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no commission rule with this id")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update commission rule")
		return
	}
	_ = json.Unmarshal(tiersOut, &resp.Tiers)
	httpx.JSON(w, http.StatusOK, resp)
}

type computeCommissionRequest struct {
	PeriodMonth int `json:"period_month"`
	PeriodYear  int `json:"period_year"`
}

type commissionEarningResponse struct {
	UserID           string `json:"user_id"`
	RuleID           string `json:"rule_id"`
	RuleName         string `json:"rule_name"`
	NetSales         string `json:"net_sales"`
	CommissionAmount string `json:"commission_amount"`
}

// ComputeCommission: POST /payroll/commission/compute — for every active
// commission rule and every cashier with finalized sales in the period
// scoped to that rule (category_id NULL = the cashier's total net sales
// across all categories; set = only that category's share), computes net
// sales "after returns" — status = 'finalized' only, so a voided or
// refunded order (sales_orders.status moves OFF 'finalized' the moment
// either happens — internal/sales/void.go) is already excluded, never
// netted against as a separate step. Re-running for the same period
// recomputes and overwrites (ON CONFLICT DO UPDATE) rather than
// double-counting, so correcting a rule and recomputing is safe.
//
// Operational recommendation, not an enforced rule (pos_frd_complete.md
// §7's own "Payment: After return period (15-30 days)"): calling this
// too early in a period — before customers' return window on those
// sales has closed — can pay commission on a sale that's later returned
// or voided, since "net sales after returns" only reflects returns that
// have *already happened* by the moment this runs, not ones still
// possible. Re-running later naturally corrects the figure (this
// overwrites, not double-counts), but a merchant who's already
// finalized a payroll run using an earlier compute has already paid out
// the stale number — recompute after the return window closes, or after
// period-end plus a grace period, not the instant the period ends.
func (h *Handler) ComputeCommission(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req computeCommissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.PeriodMonth < 1 || req.PeriodMonth > 12 || req.PeriodYear < 2000 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "valid period_month (1-12) and period_year are required")
		return
	}

	var results []commissionEarningResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, category_id, tiers FROM commission_rules WHERE status = 'active'`)
		if err != nil {
			return err
		}
		type ruleRow struct {
			id, name string
			catID    *string
			tiers    []tier
		}
		var rules []ruleRow
		for rows.Next() {
			var rr ruleRow
			var tiersRaw []byte
			if err := rows.Scan(&rr.id, &rr.name, &rr.catID, &tiersRaw); err != nil {
				rows.Close()
				return err
			}
			_ = json.Unmarshal(tiersRaw, &rr.tiers)
			rules = append(rules, rr)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		results = []commissionEarningResponse{}
		for _, rule := range rules {
			salesRows, err := tx.Query(ctx, `
				SELECT so.cashier_id, SUM(sol.unit_price * sol.quantity - sol.discount_amount) AS net_sales
				FROM sales_orders so
				JOIN sales_order_lines sol ON sol.sales_order_id = so.id
				JOIN product_variants pv ON pv.id = sol.variant_id
				JOIN products p ON p.id = pv.product_id
				WHERE so.status = 'finalized'
				  AND EXTRACT(MONTH FROM so.finalized_at) = $1 AND EXTRACT(YEAR FROM so.finalized_at) = $2
				  AND ($3::uuid IS NULL OR p.category_id = $3::uuid)
				GROUP BY so.cashier_id`,
				req.PeriodMonth, req.PeriodYear, rule.catID)
			if err != nil {
				return err
			}
			type cashierSales struct {
				userID   string
				netSales float64
			}
			var cashiers []cashierSales
			for salesRows.Next() {
				var cs cashierSales
				if err := salesRows.Scan(&cs.userID, &cs.netSales); err != nil {
					salesRows.Close()
					return err
				}
				cashiers = append(cashiers, cs)
			}
			salesRows.Close()
			if err := salesRows.Err(); err != nil {
				return err
			}

			for _, cs := range cashiers {
				rate := tierRate(rule.tiers, cs.netSales)
				amount := cs.netSales * rate / 100
				var earning commissionEarningResponse
				if err := tx.QueryRow(ctx, `
					INSERT INTO commission_earnings (id, merchant_id, user_id, rule_id, period_month, period_year, net_sales, commission_amount)
					VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6)
					ON CONFLICT (user_id, rule_id, period_month, period_year)
					DO UPDATE SET net_sales = EXCLUDED.net_sales, commission_amount = EXCLUDED.commission_amount, computed_at = now()
					RETURNING user_id, rule_id, net_sales::text, commission_amount::text`,
					cs.userID, rule.id, req.PeriodMonth, req.PeriodYear, cs.netSales, amount,
				).Scan(&earning.UserID, &earning.RuleID, &earning.NetSales, &earning.CommissionAmount); err != nil {
					return err
				}
				earning.RuleName = rule.name
				results = append(results, earning)
			}
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not compute commission")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"commission_earnings": results})
}

// tierRate picks the rate of the highest tier whose min_net_sales the
// given net sales figure clears — a tier applies to the WHOLE period's
// net sales once cleared, not as a marginal bracket (matching
// migrations/023_hr_payroll.sql's column comment). tiers need not be
// pre-sorted; this finds the maximum-qualifying tier regardless of
// input order.
func tierRate(tiers []tier, netSales float64) float64 {
	var rate float64
	var bestMin = -1.0
	for _, t := range tiers {
		if netSales >= t.MinNetSales && t.MinNetSales > bestMin {
			bestMin = t.MinNetSales
			rate = t.RatePercent
		}
	}
	return rate
}
