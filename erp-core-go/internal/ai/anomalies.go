package ai

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type discountAnomaly struct {
	DiscountID     string `json:"discount_id"`
	SalesOrderID   string `json:"sales_order_id"`
	AppliedBy      string `json:"applied_by"`
	AppliedByName  string `json:"applied_by_name"`
	ValuePercent   string `json:"value_percent"`
	DiscountAmount string `json:"discount_amount"`
	Reason         string `json:"reason"`
	CreatedAt      string `json:"created_at"`
	ZScore         string `json:"z_score"`
}

type voidRateAnomaly struct {
	CashierID    string `json:"cashier_id"`
	CashierName  string `json:"cashier_name"`
	TotalOrders  int    `json:"total_orders"`
	VoidedOrders int    `json:"voided_orders"`
	VoidRate     string `json:"void_rate"`
	ZScore       string `json:"z_score"`
}

// GetAnomalies: GET /ai/anomalies?branch_id=&days=
//
// Statistical outlier detection, not a trained fraud-detection model —
// exactly the same "real math over your own data, no ML needed"
// framing already used for the other three internal/ai endpoints.
// Two checks, both z-score-based against this merchant's own population
// in the window (never a hardcoded universal threshold — what's
// "normal" varies merchant to merchant):
//
//  1. Individual manual discounts (sales_order_discounts, type='manual')
//     whose value_percent is a statistical outlier vs. every other manual
//     discount applied in the window — a single unusually large discount,
//     not a per-cashier aggregate.
//  2. Cashiers whose void rate (voided orders / their total orders) is a
//     statistical outlier vs. every other cashier's void rate in the
//     window — flags a person, not a transaction.
//
// Both need a minimum sample size (5 discounts / 3 orders-per-cashier)
// before computing a z-score at all — a "population" of one or two data
// points can't meaningfully have an outlier, and would otherwise produce
// nonsense (a lone discount is always exactly at the mean, z=0; a
// population with too few cashiers has an unstable standard deviation).
// z > 2 is a conventional "unusual" threshold (roughly the top ~2.5% of
// a normal distribution), not a claim of actual fraud — every result
// here is a "look at this," not a verdict.
func (h *Handler) GetAnomalies(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	days := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 {
		days = v
	}

	var discountAnomalies []discountAnomaly
	var voidAnomalies []voidRateAnomaly
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		discountAnomalies, err = findDiscountAnomalies(ctx, tx, days, branchID)
		if err != nil {
			return err
		}
		voidAnomalies, err = findVoidRateAnomalies(ctx, tx, days, branchID)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not compute anomalies")
		return
	}
	if discountAnomalies == nil {
		discountAnomalies = []discountAnomaly{}
	}
	if voidAnomalies == nil {
		voidAnomalies = []voidRateAnomaly{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"window_days":         days,
		"discount_anomalies":  discountAnomalies,
		"void_rate_anomalies": voidAnomalies,
	})
}

func findDiscountAnomalies(ctx context.Context, tx pgx.Tx, days int, branchID string) ([]discountAnomaly, error) {
	rows, err := tx.Query(ctx, `
		WITH params AS (
			SELECT $1::int AS window_days, NULLIF($2,'')::uuid AS branch_filter
		),
		discounts AS (
			SELECT sod.id, sod.sales_order_id, sod.applied_by, u.name AS applied_by_name,
			       sod.value_percent, sod.discount_amount, COALESCE(sod.reason,'') AS reason, sod.created_at
			FROM sales_order_discounts sod
			JOIN sales_orders so ON so.id = sod.sales_order_id
			JOIN users u ON u.id = sod.applied_by
			CROSS JOIN params
			WHERE sod.type = 'manual'
			  AND sod.created_at >= now() - make_interval(days => params.window_days)
			  AND (params.branch_filter IS NULL OR so.branch_id = params.branch_filter)
		),
		stats AS (
			SELECT AVG(value_percent) AS mean_pct, STDDEV(value_percent) AS stddev_pct, COUNT(*) AS n FROM discounts
		)
		SELECT d.id::text, d.sales_order_id::text, d.applied_by::text, d.applied_by_name,
		       d.value_percent::text, d.discount_amount::text, d.reason, d.created_at::text,
		       (d.value_percent - s.mean_pct) / s.stddev_pct AS z
		FROM discounts d CROSS JOIN stats s
		WHERE s.n >= 5 AND s.stddev_pct > 0 AND (d.value_percent - s.mean_pct) / s.stddev_pct > 2
		ORDER BY z DESC`, days, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []discountAnomaly
	for rows.Next() {
		var a discountAnomaly
		var z float64
		if err := rows.Scan(&a.DiscountID, &a.SalesOrderID, &a.AppliedBy, &a.AppliedByName,
			&a.ValuePercent, &a.DiscountAmount, &a.Reason, &a.CreatedAt, &z); err != nil {
			return nil, err
		}
		a.ZScore = strconv.FormatFloat(z, 'f', 2, 64)
		results = append(results, a)
	}
	return results, rows.Err()
}

func findVoidRateAnomalies(ctx context.Context, tx pgx.Tx, days int, branchID string) ([]voidRateAnomaly, error) {
	rows, err := tx.Query(ctx, `
		WITH params AS (
			SELECT $1::int AS window_days, NULLIF($2,'')::uuid AS branch_filter
		),
		cashier_orders AS (
			SELECT cashier_id, COUNT(*) AS total,
			       COUNT(*) FILTER (WHERE status = 'voided') AS voided
			FROM sales_orders so
			CROSS JOIN params
			WHERE so.created_at >= now() - make_interval(days => params.window_days)
			  AND so.status IN ('finalized', 'voided')
			  AND (params.branch_filter IS NULL OR so.branch_id = params.branch_filter)
			GROUP BY cashier_id
			HAVING COUNT(*) >= 3
		),
		rates AS (
			SELECT cashier_id, total, voided, voided::float8 / total AS void_rate FROM cashier_orders
		),
		pop AS (
			SELECT AVG(void_rate) AS mean_rate, STDDEV(void_rate) AS stddev_rate FROM rates
		)
		SELECT r.cashier_id::text, u.name, r.total, r.voided, r.void_rate,
		       (r.void_rate - p.mean_rate) / p.stddev_rate AS z
		FROM rates r
		JOIN users u ON u.id = r.cashier_id
		CROSS JOIN pop p
		WHERE p.stddev_rate > 0 AND (r.void_rate - p.mean_rate) / p.stddev_rate > 2
		ORDER BY z DESC`, days, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []voidRateAnomaly
	for rows.Next() {
		var a voidRateAnomaly
		var rate, z float64
		if err := rows.Scan(&a.CashierID, &a.CashierName, &a.TotalOrders, &a.VoidedOrders, &rate, &z); err != nil {
			return nil, err
		}
		a.VoidRate = strconv.FormatFloat(rate, 'f', 3, 64)
		a.ZScore = strconv.FormatFloat(z, 'f', 2, 64)
		results = append(results, a)
	}
	return results, rows.Err()
}
