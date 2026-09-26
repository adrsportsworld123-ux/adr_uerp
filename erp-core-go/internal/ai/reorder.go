// Package ai implements Phase 6's AI Platform: reorder suggestions, a
// cross-sell recommendation engine, demand forecasting, and fraud/anomaly
// detection are all real math/SQL over this merchant's own already-posted
// transaction data — no trained model, no new tables, matching the
// roadmap's own "doesn't need deep ML to start" framing.
//
// NLP-BI (nlpbi.go) and the AI Copilot (copilot.go) are the two remaining
// Phase 6 items and the only ones that genuinely need a real LLM: both go
// through the internal/ai/llm.Client interface, which the user chose to
// back with either a self-hosted Ollama server or the real Anthropic API,
// selected per-deployment via LLM_PROVIDER (see internal/config) — the
// same "build the interface, don't lock in a vendor" precedent as
// e-invoicing's GSPClient/StubGSPClient.
package ai

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/ai/llm"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB  *db.DB
	LLM llm.Client // nil if LLM_PROVIDER isn't configured — see requireLLM in nlpbi.go
}

type reorderSuggestion struct {
	BranchID             string  `json:"branch_id"`
	VariantID            string  `json:"variant_id"`
	ProductName          string  `json:"product_name"`
	SKU                  string  `json:"sku"`
	OnHand               string  `json:"on_hand"`
	Reserved             string  `json:"reserved"`
	Available            string  `json:"available"`
	ReorderPoint         string  `json:"reorder_point"`
	DailyVelocity        string  `json:"daily_velocity"`
	DaysOfStockRemaining *string `json:"days_of_stock_remaining"` // null = velocity is 0, can't estimate a runway
	SuggestedReorderQty  string  `json:"suggested_reorder_qty"`
	Reason               string  `json:"reason"` // "below reorder point" | "will run out before lead time" | "both"
}

// GetReorderSuggestions: GET /ai/reorder-suggestions?branch_id=&velocity_window_days=&lead_time_days=
//
// Sales velocity = units sold per day, averaged over the trailing
// velocity_window_days (default 30) of *finalized* orders — voided/
// refunded orders never reach 'finalized' (internal/sales/void.go), so
// they're excluded structurally, not netted out as a separate step, same
// reasoning internal/payroll's commission engine already documents for
// "net sales after returns."
//
// lead_time_days (default 7) is a caller-supplied parameter, not stored
// master data: this codebase has no per-product/per-supplier lead-time
// field today (Purchase Management, migrations/006_purchase.sql, never
// linked a product to a preferred supplier — a real, separate gap this
// pass doesn't invent an answer for), so rather than pretend to know a
// number it doesn't, this endpoint asks for it explicitly. A merchant who
// knows "my cricket-gear supplier takes about 10 days" passes
// lead_time_days=10.
//
// A variant surfaces as a suggestion when it's already at or below its
// reorder_point, OR its current available stock would run out before
// lead_time_days elapses at the recent velocity — not every stocked item,
// only ones genuinely at risk. suggested_reorder_qty covers demand for
// the lead time itself plus enough to land back at the reorder_point
// (not just zero) once the new stock arrives.
func (h *Handler) GetReorderSuggestions(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	windowDays := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("velocity_window_days")); err == nil && v > 0 {
		windowDays = v
	}
	leadTimeDays := 7.0
	if v, err := strconv.ParseFloat(r.URL.Query().Get("lead_time_days"), 64); err == nil && v > 0 {
		leadTimeDays = v
	}

	suggestions := []reorderSuggestion{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			WITH params AS (
				SELECT $1::int AS window_days, NULLIF($2,'')::uuid AS branch_filter
			),
			velocity AS (
				SELECT so.branch_id, sol.variant_id, SUM(sol.quantity) AS total_qty
				FROM sales_order_lines sol
				JOIN sales_orders so ON so.id = sol.sales_order_id
				CROSS JOIN params
				WHERE so.status = 'finalized'
				  AND so.finalized_at >= now() - make_interval(days => params.window_days)
				  AND (params.branch_filter IS NULL OR so.branch_id = params.branch_filter)
				GROUP BY so.branch_id, sol.variant_id
			)
			SELECT sl.branch_id::text, sl.variant_id::text, p.name, pv.sku,
			       sl.on_hand::text, sl.reserved::text, (sl.on_hand - sl.reserved)::text, sl.reorder_point::text,
			       COALESCE(v.total_qty, 0)::float8 / params.window_days::float8
			FROM stock_levels sl
			CROSS JOIN params
			JOIN product_variants pv ON pv.id = sl.variant_id
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN velocity v ON v.branch_id = sl.branch_id AND v.variant_id = sl.variant_id
			WHERE (params.branch_filter IS NULL OR sl.branch_id = params.branch_filter)
			ORDER BY p.name`, windowDays, branchID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var s reorderSuggestion
			var available, reorderPoint float64
			var velocity float64
			var onHandStr, reservedStr, availableStr, reorderPointStr string
			if err := rows.Scan(&s.BranchID, &s.VariantID, &s.ProductName, &s.SKU,
				&onHandStr, &reservedStr, &availableStr, &reorderPointStr, &velocity); err != nil {
				return err
			}
			available, _ = strconv.ParseFloat(availableStr, 64)
			reorderPoint, _ = strconv.ParseFloat(reorderPointStr, 64)
			s.OnHand, s.Reserved, s.Available, s.ReorderPoint = onHandStr, reservedStr, availableStr, reorderPointStr
			s.DailyVelocity = strconv.FormatFloat(velocity, 'f', 3, 64)

			// reorder_point = 0 means "never configured," not "reorder at
			// zero" — same convention internal/inventory/sweeper.go's
			// RunLowStockSweeper already uses (its query filters
			// `reorder_point > 0`), found live: without this, a brand-new
			// product's zero-stock row at every branch (POST /products
			// seeds one per branch) trivially satisfies available(0) <=
			// reorder_point(0) and gets flagged everywhere, before anyone
			// has actually set a real threshold.
			belowReorderPoint := reorderPoint > 0 && available <= reorderPoint
			var runsOutBeforeLeadTime bool
			if velocity > 0 {
				daysRemaining := available / velocity
				str := strconv.FormatFloat(daysRemaining, 'f', 1, 64)
				s.DaysOfStockRemaining = &str
				runsOutBeforeLeadTime = daysRemaining <= leadTimeDays
			}

			if !belowReorderPoint && !runsOutBeforeLeadTime {
				continue // not at risk — don't surface every stocked item, only the ones that need attention
			}
			switch {
			case belowReorderPoint && runsOutBeforeLeadTime:
				s.Reason = "below reorder point and will run out before lead time"
			case belowReorderPoint:
				s.Reason = "below reorder point"
			default:
				s.Reason = "will run out before lead time"
			}

			// Cover demand for the lead time itself, then land back at the
			// reorder_point (not zero) once the new stock arrives — the
			// standard reorder-point/lead-time formula, not just "top up
			// to zero."
			suggestedQty := velocity*leadTimeDays + reorderPoint - available
			if suggestedQty < 0 {
				suggestedQty = 0
			}
			s.SuggestedReorderQty = strconv.FormatFloat(suggestedQty, 'f', 2, 64)

			suggestions = append(suggestions, s)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not compute reorder suggestions")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"velocity_window_days": windowDays,
		"lead_time_days":       leadTimeDays,
		"suggestions":          suggestions,
	})
}
