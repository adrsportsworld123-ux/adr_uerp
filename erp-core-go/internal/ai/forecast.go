package ai

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type forecastDay struct {
	Date            string `json:"date"`
	ForecastedUnits string `json:"forecasted_units"`
}

type demandForecastResponse struct {
	VariantID          string        `json:"variant_id"`
	HistoryDays        int           `json:"history_days"`
	HorizonDays        int           `json:"horizon_days"`
	HistoricalDailyAvg string        `json:"historical_daily_avg"`
	TrendPerDay        string        `json:"trend_per_day"` // slope of the fitted line — positive = growing, negative = declining
	TotalForecastUnits string        `json:"total_forecast_units"`
	Confidence         string        `json:"confidence"` // "low" | "moderate" | "adequate" — see doc comment
	Forecast           []forecastDay `json:"forecast"`
}

// GetDemandForecast: GET /ai/demand-forecast/{variant_id}?branch_id=&history_days=&horizon_days=
//
// A real linear-trend forecast — ordinary least squares over this
// variant's own daily sold-quantity history (finalized orders only,
// zero-filled for days with no sales so the regression's spacing is
// correct), never a trained model. This is exactly the honest,
// non-ML-needed approach the roadmap already commits to for reorder
// suggestions and recommendations, extended to forecasting.
//
// confidence is a heuristic based on how many of the historical days
// actually had a sale, not a real statistical confidence interval —
// deliberately conservative wording ("low"/"moderate"/"adequate", never
// "high"): the roadmap's own note that demand forecasting was "deferred
// this long" because it "needs real transaction history" is still true
// here. A brand-new product with a week of data gets a real number back,
// correctly computed, but honestly labeled low-confidence rather than
// silently presented as authoritative.
func (h *Handler) GetDemandForecast(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	variantID := chi.URLParam(r, "variant_id")
	branchID := r.URL.Query().Get("branch_id")
	historyDays := 60
	if v, err := strconv.Atoi(r.URL.Query().Get("history_days")); err == nil && v > 0 {
		historyDays = v
	}
	horizonDays := 14
	if v, err := strconv.Atoi(r.URL.Query().Get("horizon_days")); err == nil && v > 0 && v <= 90 {
		horizonDays = v
	}

	var resp demandForecastResponse
	resp.VariantID = variantID
	resp.HistoryDays = historyDays
	resp.HorizonDays = horizonDays

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			WITH params AS (
				SELECT $1::int AS history_days, NULLIF($3,'')::uuid AS branch_filter
			),
			days AS (
				SELECT generate_series(
					current_date - (SELECT history_days FROM params) + 1,
					current_date, interval '1 day'
				)::date AS day
			),
			sales AS (
				SELECT so.finalized_at::date AS day, SUM(sol.quantity) AS qty
				FROM sales_order_lines sol
				JOIN sales_orders so ON so.id = sol.sales_order_id
				CROSS JOIN params
				WHERE sol.variant_id = $2 AND so.status = 'finalized'
				  AND so.finalized_at >= now() - make_interval(days => params.history_days)
				  AND (params.branch_filter IS NULL OR so.branch_id = params.branch_filter)
				GROUP BY so.finalized_at::date
			)
			SELECT d.day, COALESCE(s.qty, 0)
			FROM days d
			LEFT JOIN sales s ON s.day = d.day
			ORDER BY d.day`, historyDays, variantID, branchID)
		if err != nil {
			return err
		}
		defer rows.Close()

		var xs []float64
		var ys []float64
		var lastDate time.Time
		nonZeroDays := 0
		i := 0.0
		for rows.Next() {
			var day time.Time
			var qty float64
			if err := rows.Scan(&day, &qty); err != nil {
				return err
			}
			xs = append(xs, i)
			ys = append(ys, qty)
			if qty > 0 {
				nonZeroDays++
			}
			lastDate = day
			i++
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(xs) == 0 {
			resp.Confidence = "low"
			resp.Forecast = []forecastDay{}
			return nil
		}

		slope, intercept := leastSquares(xs, ys)
		var sum float64
		for _, y := range ys {
			sum += y
		}
		resp.HistoricalDailyAvg = strconv.FormatFloat(sum/float64(len(ys)), 'f', 3, 64)
		resp.TrendPerDay = strconv.FormatFloat(slope, 'f', 4, 64)

		resp.Forecast = make([]forecastDay, 0, horizonDays)
		var total float64
		n := float64(len(xs))
		for d := 1; d <= horizonDays; d++ {
			predicted := intercept + slope*(n-1+float64(d))
			if predicted < 0 {
				predicted = 0
			}
			total += predicted
			resp.Forecast = append(resp.Forecast, forecastDay{
				Date:            lastDate.AddDate(0, 0, d).Format("2006-01-02"),
				ForecastedUnits: strconv.FormatFloat(predicted, 'f', 2, 64),
			})
		}
		resp.TotalForecastUnits = strconv.FormatFloat(total, 'f', 2, 64)

		switch {
		case nonZeroDays < 5:
			resp.Confidence = "low"
		case nonZeroDays < 15:
			resp.Confidence = "moderate"
		default:
			resp.Confidence = "adequate"
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not compute demand forecast")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// leastSquares fits y = intercept + slope*x by ordinary least squares.
// Returns slope=0, intercept=mean(y) if there's no variance in x (a
// single data point) — a flat forecast is the only honest answer with
// one day of history, not a divide-by-zero.
func leastSquares(xs, ys []float64) (slope, intercept float64) {
	n := float64(len(xs))
	if n == 0 {
		return 0, 0
	}
	var sumX, sumY, sumXY, sumXX float64
	for i := range xs {
		sumX += xs[i]
		sumY += ys[i]
		sumXY += xs[i] * ys[i]
		sumXX += xs[i] * xs[i]
	}
	meanY := sumY / n
	denominator := n*sumXX - sumX*sumX
	if denominator == 0 {
		return 0, meanY
	}
	slope = (n*sumXY - sumX*sumY) / denominator
	intercept = (sumY - slope*sumX) / n
	return slope, intercept
}
