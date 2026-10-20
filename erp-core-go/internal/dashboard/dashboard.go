// Package dashboard is the sales dashboard (phase0_1_design.md §3.62;
// migrations/056): daily, weekly and monthly sales, sales tracking against
// a monthly target, tenders, top products and categories, branches and the
// hour of day, filtered by date range, branch and channel.
//
// Who sees it is a permission (dashboard.view, Merchant Admin by default);
// which widgets each role sees is the merchant's choice
// (dashboard_role_widgets, dashboard.configure): a role with no row sees
// every widget, and someone with several roles sees what any of them may.
// The branch scope (Phase 15) applies as everywhere, so a Branch Manager's
// dashboard shows their own branches only.
//
// Sales are completed sales (finalized, or partly or fully refunded since;
// voids and open carts left out) at what was paid — the grand total less
// any checkout round-off waiver (migrations/059) — counted on the day
// money changed hands (device_created_at for an offline sale, else
// finalized_at) in India Standard Time.
package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/locale"
)

type Handler struct {
	DB *db.DB
}

// Widget is one card on the dashboard.
type Widget struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Widgets is the catalogue, in its default order.
var Widgets = []Widget{
	{"kpis", "Headline figures", "Today, this week and this month, with the average bill, each against the same days of the period before."},
	{"sales_tracking", "Sales tracking", "This month against its target and against last month, day by day, with the month's projection and the financial year so far."},
	{"daily_sales", "Daily sales", "Sales and bills per day over the chosen dates."},
	{"weekly_sales", "Weekly sales", "The last 12 weeks (Monday to Sunday) up to the end date."},
	{"monthly_sales", "Monthly sales", "The last 12 months up to the end date, beside the same month a year before."},
	{"payment_mix", "Payment mix", "How customers paid over the chosen dates."},
	{"top_products", "Top products", "The ten best-selling products by value over the chosen dates."},
	{"top_categories", "Top categories", "The ten best-selling categories by value over the chosen dates."},
	{"branch_sales", "Sales by branch", "Each branch's sales over the chosen dates."},
	{"hourly_sales", "Sales by hour", "When in the day sales happen, over the chosen dates."},
}

func widgetKeys() []string {
	out := make([]string, len(Widgets))
	for i, w := range Widgets {
		out[i] = w.Key
	}
	return out
}

func isWidget(k string) bool {
	for _, w := range Widgets {
		if w.Key == k {
			return true
		}
	}
	return false
}

const (
	maxSpanDays = 366
	moment      = `COALESCE(so.device_created_at, so.finalized_at)`
	saleDay     = `((` + moment + `) AT TIME ZONE app_merchant_tz())::date` // the merchant's business day (migrations/064)
	// $1 branch_id ('' = all), $2 channel ('' = all)
	completed = `so.status IN ('finalized', 'partially_refunded', 'refunded')
		AND ($1 = '' OR so.branch_id::text = $1) AND ($2 = '' OR so.channel = $2)`
)

type filters struct {
	From, To time.Time // inclusive days, IST
	BranchID string
	Channel  string
}

func (f filters) fromS() string { return f.From.Format("2006-01-02") }
func (f filters) toS() string   { return f.To.Format("2006-01-02") }

// parseFilters reads the date range on loc's clock — the merchant's
// business time zone (migrations/064). Every later date sum starts from
// f.From / f.To, so it inherits that location.
func parseFilters(r *http.Request, loc *time.Location) (filters, error) {
	q := r.URL.Query()
	today := time.Now().In(loc)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	f := filters{From: today.AddDate(0, 0, -29), To: today, BranchID: q.Get("branch_id"), Channel: q.Get("channel")}
	if v := q.Get("from"); v != "" {
		d, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			return f, errors.New("from must be YYYY-MM-DD")
		}
		f.From = d
	}
	if v := q.Get("to"); v != "" {
		d, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			return f, errors.New("to must be YYYY-MM-DD")
		}
		f.To = d
	}
	if f.To.Before(f.From) {
		return f, errors.New("to is before from")
	}
	if f.To.Sub(f.From) > maxSpanDays*24*time.Hour {
		return f, fmt.Errorf("the dashboard covers at most %d days", maxSpanDays)
	}
	if f.Channel != "" && f.Channel != "pos" && f.Channel != "backoffice" && f.Channel != "online" {
		return f, errors.New("channel must be pos, backoffice or online")
	}
	return f, nil
}

func m2(v float64) string { return fmt.Sprintf("%.2f", math.Round(v*100)/100) }

// change is the percentage change from prev to cur; nil when prev is zero.
func change(cur, prev float64) *float64 {
	if prev == 0 {
		return nil
	}
	c := math.Round((cur-prev)/prev*1000) / 10
	return &c
}

// --------------------------------------------------------------- visibility

// visibleWidgets: what userID may see, or nil when they lack dashboard.view.
func visibleWidgets(ctx context.Context, tx pgx.Tx, userID string) ([]string, error) {
	codes, err := authn.FetchPermissions(ctx, tx, userID)
	if err != nil || !authn.HasPermission(codes, "dashboard.view") {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT drw.widgets
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		JOIN role_permissions rp ON rp.role_id = r.id
		JOIN permissions p ON p.id = rp.permission_id AND p.code = 'dashboard.view'
		LEFT JOIN dashboard_role_widgets drw ON drw.role_id = r.id
		WHERE ur.user_id::text = $1
		ORDER BY r.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	out := []string{}
	add := func(ks []string) {
		for _, k := range ks {
			if isWidget(k) && !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	found := false
	for rows.Next() {
		var ws []string
		if err := rows.Scan(&ws); err != nil {
			return nil, err
		}
		found = true
		if ws == nil {
			ws = widgetKeys() // no row: the default, everything
		}
		add(ws)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !found { // the permission came some other way: the default
		add(widgetKeys())
	}
	return out, nil
}

// ------------------------------------------------------------------ the data

type point struct {
	Period string `json:"period"`
	Label  string `json:"label"`
	Sales  string `json:"sales"`
	Orders int    `json:"orders"`
	Prior  string `json:"prior,omitempty"` // monthly: the same month a year before
}

func series(ctx context.Context, tx pgx.Tx, q string, args ...any) ([]point, error) {
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []point{}
	for rows.Next() {
		var p point
		var sales float64
		if err := rows.Scan(&p.Period, &p.Label, &sales, &p.Orders); err != nil {
			return nil, err
		}
		p.Sales = m2(sales)
		out = append(out, p)
	}
	return out, rows.Err()
}

type kpi struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	Value     string   `json:"value"`
	Previous  string   `json:"previous"`
	ChangePct *float64 `json:"change_pct"`
	Orders    int      `json:"orders"`
	Hint      string   `json:"hint"`
}

func kpis(ctx context.Context, tx pgx.Tx, f filters) ([]kpi, error) {
	ref := f.To
	weekStart := ref.AddDate(0, 0, -((int(ref.Weekday()) + 6) % 7)) // Monday
	monthStart := time.Date(ref.Year(), ref.Month(), 1, 0, 0, 0, 0, ref.Location())
	prevMonthStart := monthStart.AddDate(0, -1, 0)
	// The same days of last month (capped at its length).
	prevMonthEnd := prevMonthStart.AddDate(0, 0, ref.Day()-1)
	if last := monthStart.AddDate(0, 0, -1); prevMonthEnd.After(last) {
		prevMonthEnd = last
	}
	d := func(t time.Time) string { return t.Format("2006-01-02") }
	var v [12]float64
	var n [8]int
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(so.grand_total + so.round_off) FILTER (WHERE `+saleDay+` = $3::date), 0),
		       count(*) FILTER (WHERE `+saleDay+` = $3::date),
		       COALESCE(sum(so.grand_total + so.round_off) FILTER (WHERE `+saleDay+` = $3::date - 1), 0),
		       count(*) FILTER (WHERE `+saleDay+` = $3::date - 1),
		       COALESCE(sum(so.grand_total + so.round_off) FILTER (WHERE `+saleDay+` BETWEEN $4::date AND $3::date), 0),
		       count(*) FILTER (WHERE `+saleDay+` BETWEEN $4::date AND $3::date),
		       COALESCE(sum(so.grand_total + so.round_off) FILTER (WHERE `+saleDay+` BETWEEN $4::date - 7 AND $3::date - 7), 0),
		       count(*) FILTER (WHERE `+saleDay+` BETWEEN $4::date - 7 AND $3::date - 7),
		       COALESCE(sum(so.grand_total + so.round_off) FILTER (WHERE `+saleDay+` BETWEEN $5::date AND $3::date), 0),
		       count(*) FILTER (WHERE `+saleDay+` BETWEEN $5::date AND $3::date),
		       COALESCE(sum(so.grand_total + so.round_off) FILTER (WHERE `+saleDay+` BETWEEN $6::date AND $7::date), 0),
		       count(*) FILTER (WHERE `+saleDay+` BETWEEN $6::date AND $7::date)
		FROM sales_orders so
		WHERE `+completed+` AND `+saleDay+` BETWEEN LEAST($6::date, $4::date - 7) AND $3::date`,
		f.BranchID, f.Channel, d(ref), d(weekStart), d(monthStart), d(prevMonthStart), d(prevMonthEnd)).
		Scan(&v[0], &n[0], &v[1], &n[1], &v[2], &n[2], &v[3], &n[3], &v[4], &n[4], &v[5], &n[5])
	if err != nil {
		return nil, err
	}
	avg := func(s float64, c int) float64 {
		if c == 0 {
			return 0
		}
		return s / float64(c)
	}
	dayLabel := "Today"
	if today := time.Now().In(ref.Location()).Format("2006-01-02"); d(ref) != today {
		dayLabel = ref.Format("2 Jan")
	}
	return []kpi{
		{"day", dayLabel, m2(v[0]), m2(v[1]), change(v[0], v[1]), n[0], "against the day before"},
		{"week", "This week", m2(v[2]), m2(v[3]), change(v[2], v[3]), n[2], "Monday to " + ref.Format("Mon") + ", against the same days last week"},
		{"month", "This month", m2(v[4]), m2(v[5]), change(v[4], v[5]), n[4], "1–" + fmt.Sprint(ref.Day()) + " " + ref.Format("Jan") + ", against the same days last month"},
		{"avg_bill", "Average bill (month)", m2(avg(v[4], n[4])), m2(avg(v[5], n[5])), change(avg(v[4], n[4]), avg(v[5], n[5])), n[4], "this month against last month's same days"},
	}, nil
}

type tracking struct {
	Month          string           `json:"month"`
	Target         *string          `json:"target"`
	MonthToDate    string           `json:"month_to_date"`
	LastMonthSame  string           `json:"last_month_same_days"`
	Projected      string           `json:"projected"`
	AchievedPct    *float64         `json:"achieved_pct"`
	ProjectedPct   *float64         `json:"projected_pct"`
	RequiredPerDay *string          `json:"required_per_day"`
	DaysLeft       int              `json:"days_left"`
	FYLabel        string           `json:"fy_label"`
	FYToDate       string           `json:"fy_to_date"`
	FYPrevSame     string           `json:"fy_prev_same_period"`
	FYChangePct    *float64         `json:"fy_change_pct"`
	Cumulative     []cumulativeItem `json:"cumulative"`
}

type cumulativeItem struct {
	Day       int     `json:"day"`
	ThisMonth *string `json:"this_month"`
	LastMonth *string `json:"last_month"`
	Target    *string `json:"target"`
}

func salesTracking(ctx context.Context, tx pgx.Tx, f filters) (tracking, error) {
	ref := f.To
	monthStart := time.Date(ref.Year(), ref.Month(), 1, 0, 0, 0, 0, ref.Location())
	daysInMonth := monthStart.AddDate(0, 1, -1).Day()
	prevStart := monthStart.AddDate(0, -1, 0)
	prevDays := monthStart.AddDate(0, 0, -1).Day()
	t := tracking{Month: ref.Format("January 2006"), DaysLeft: daysInMonth - ref.Day(), Cumulative: []cumulativeItem{}}

	// The target: the branch's when a branch is chosen, else the whole
	// business's, else the branches' targets added up.
	var target *float64
	if err := tx.QueryRow(ctx, `
		SELECT CASE WHEN $1 <> '' THEN (SELECT monthly_target FROM sales_targets WHERE branch_id::text = $1)
		       ELSE COALESCE((SELECT monthly_target FROM sales_targets WHERE branch_id IS NULL),
		                     (SELECT sum(monthly_target) FROM sales_targets WHERE branch_id IS NOT NULL)) END::float8`,
		f.BranchID).Scan(&target); err != nil {
		return t, err
	}

	byDay := map[string]float64{}
	rows, err := tx.Query(ctx, `
		SELECT `+saleDay+`::text, sum(so.grand_total + so.round_off)::float8 FROM sales_orders so
		WHERE `+completed+` AND `+saleDay+` BETWEEN $3::date AND $4::date GROUP BY 1`,
		f.BranchID, f.Channel, prevStart.Format("2006-01-02"), ref.Format("2006-01-02"))
	if err != nil {
		return t, err
	}
	for rows.Next() {
		var d string
		var v float64
		if err := rows.Scan(&d, &v); err != nil {
			rows.Close()
			return t, err
		}
		byDay[d] = v
	}
	rows.Close()

	var cur, last, lastSame float64
	for day := 1; day <= max(daysInMonth, prevDays); day++ {
		item := cumulativeItem{Day: day}
		if day <= daysInMonth && day <= ref.Day() {
			cur += byDay[monthStart.AddDate(0, 0, day-1).Format("2006-01-02")]
			s := m2(cur)
			item.ThisMonth = &s
		}
		if day <= prevDays {
			last += byDay[prevStart.AddDate(0, 0, day-1).Format("2006-01-02")]
			s := m2(last)
			item.LastMonth = &s
			if day <= ref.Day() {
				lastSame = last
			}
		}
		if target != nil && day <= daysInMonth {
			s := m2(*target * float64(day) / float64(daysInMonth))
			item.Target = &s
		}
		t.Cumulative = append(t.Cumulative, item)
	}
	t.MonthToDate, t.LastMonthSame = m2(cur), m2(lastSame)
	projected := cur / float64(ref.Day()) * float64(daysInMonth)
	t.Projected = m2(projected)
	if target != nil {
		s := m2(*target)
		t.Target = &s
		a := math.Round(cur / *target * 1000) / 10
		p := math.Round(projected / *target * 1000) / 10
		t.AchievedPct, t.ProjectedPct = &a, &p
		if t.DaysLeft > 0 {
			r := m2(math.Max(*target-cur, 0) / float64(t.DaysLeft))
			t.RequiredPerDay = &r
		}
	}

	// The Indian financial year (1 April to 31 March) so far, against the
	// same stretch of the year before.
	fyStart := time.Date(ref.Year(), time.April, 1, 0, 0, 0, 0, ref.Location())
	if ref.Before(fyStart) {
		fyStart = fyStart.AddDate(-1, 0, 0)
	}
	t.FYLabel = fmt.Sprintf("FY %d–%02d", fyStart.Year(), (fyStart.Year()+1)%100)
	var fy, fyPrev float64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(so.grand_total + so.round_off) FILTER (WHERE `+saleDay+` BETWEEN $3::date AND $4::date), 0)::float8,
		       COALESCE(sum(so.grand_total + so.round_off) FILTER (WHERE `+saleDay+` BETWEEN ($3::date - interval '1 year')::date AND ($4::date - interval '1 year')::date), 0)::float8
		FROM sales_orders so
		WHERE `+completed+` AND `+saleDay+` BETWEEN ($3::date - interval '1 year')::date AND $4::date`,
		f.BranchID, f.Channel, fyStart.Format("2006-01-02"), ref.Format("2006-01-02")).Scan(&fy, &fyPrev); err != nil {
		return t, err
	}
	t.FYToDate, t.FYPrevSame, t.FYChangePct = m2(fy), m2(fyPrev), change(fy, fyPrev)
	return t, nil
}

type share struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Value    string `json:"value"`
	Count    int    `json:"count"`
	Quantity string `json:"quantity,omitempty"`
}

func shares(ctx context.Context, tx pgx.Tx, withQty bool, q string, args ...any) ([]share, error) {
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []share{}
	for rows.Next() {
		var s share
		var v float64
		var dest = []any{&s.Key, &s.Label, &v, &s.Count}
		if withQty {
			dest = append(dest, &s.Quantity)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		s.Value = m2(v)
		out = append(out, s)
	}
	return out, rows.Err()
}

func buildWidget(ctx context.Context, tx pgx.Tx, key string, f filters) (any, error) {
	inRange := saleDay + ` BETWEEN $3::date AND $4::date`
	args := []any{f.BranchID, f.Channel, f.fromS(), f.toS()}
	switch key {
	case "kpis":
		return kpis(ctx, tx, f)
	case "sales_tracking":
		return salesTracking(ctx, tx, f)
	case "daily_sales":
		return series(ctx, tx, `
			SELECT d::date::text, to_char(d, 'DD Mon'), COALESCE(s.sales, 0)::float8, COALESCE(s.orders, 0)
			FROM generate_series($3::date, $4::date, interval '1 day') d
			LEFT JOIN (SELECT `+saleDay+` AS day, sum(so.grand_total + so.round_off) sales, count(*) orders FROM sales_orders so
			           WHERE `+completed+` AND `+inRange+` GROUP BY 1) s ON s.day = d::date
			ORDER BY d`, args...)
	case "weekly_sales":
		return series(ctx, tx, `
			SELECT w::date::text, to_char(w, 'DD Mon'), COALESCE(s.sales, 0)::float8, COALESCE(s.orders, 0)
			FROM generate_series(date_trunc('week', $4::date) - interval '11 weeks', date_trunc('week', $4::date), interval '1 week') w
			LEFT JOIN (SELECT date_trunc('week', `+saleDay+`) AS wk, sum(so.grand_total + so.round_off) sales, count(*) orders FROM sales_orders so
			           WHERE `+completed+` AND `+saleDay+` BETWEEN (date_trunc('week', $4::date) - interval '11 weeks')::date AND $4::date
			           GROUP BY 1) s ON s.wk = w
			WHERE $3::date IS NOT NULL
			ORDER BY w`, args...)
	case "monthly_sales":
		pts, err := series(ctx, tx, `
			SELECT to_char(m, 'YYYY-MM'), to_char(m, 'Mon YY'), COALESCE(s.sales, 0)::float8, COALESCE(s.orders, 0)
			FROM generate_series(date_trunc('month', $4::date) - interval '11 months', date_trunc('month', $4::date), interval '1 month') m
			LEFT JOIN (SELECT date_trunc('month', `+saleDay+`) AS mo, sum(so.grand_total + so.round_off) sales, count(*) orders FROM sales_orders so
			           WHERE `+completed+` AND `+saleDay+` BETWEEN (date_trunc('month', $4::date) - interval '23 months')::date AND $4::date
			           GROUP BY 1) s ON s.mo = m
			WHERE $3::date IS NOT NULL
			ORDER BY m`, args...)
		if err != nil {
			return nil, err
		}
		prior, err := series(ctx, tx, `
			SELECT to_char(m + interval '1 year', 'YYYY-MM'), '', COALESCE(s.sales, 0)::float8, COALESCE(s.orders, 0)
			FROM generate_series(date_trunc('month', $4::date) - interval '23 months', date_trunc('month', $4::date) - interval '12 months', interval '1 month') m
			LEFT JOIN (SELECT date_trunc('month', `+saleDay+`) AS mo, sum(so.grand_total + so.round_off) sales, count(*) orders FROM sales_orders so
			           WHERE `+completed+` AND `+saleDay+` BETWEEN (date_trunc('month', $4::date) - interval '23 months')::date AND $4::date
			           GROUP BY 1) s ON s.mo = m
			WHERE $3::date IS NOT NULL
			ORDER BY m`, args...)
		if err != nil {
			return nil, err
		}
		for i := range pts {
			if i < len(prior) {
				pts[i].Prior = prior[i].Sales
			}
		}
		return pts, nil
	case "payment_mix":
		return shares(ctx, tx, false, `
			SELECT method, method, sum(amount)::float8, count(*) FROM (
			    SELECT p.method, p.amount FROM payments p JOIN sales_orders so ON so.id = p.sales_order_id
			    WHERE `+completed+` AND `+inRange+` AND p.status = 'captured'
			    UNION ALL
			    SELECT 'credit', so.credit_amount FROM sales_orders so WHERE `+completed+` AND `+inRange+` AND so.credit_amount > 0
			) x GROUP BY 1 ORDER BY 3 DESC`, args...)
	case "top_products":
		return shares(ctx, tx, true, `
			SELECT p.id::text, p.name, sum(l.line_total)::float8, count(DISTINCT so.id)::int, trim_scale(sum(l.quantity))::text
			FROM sales_order_lines l JOIN sales_orders so ON so.id = l.sales_order_id
			JOIN product_variants v ON v.id = l.variant_id JOIN products p ON p.id = v.product_id
			WHERE `+completed+` AND `+inRange+` GROUP BY 1, 2 ORDER BY 3 DESC LIMIT 10`, args...)
	case "top_categories":
		return shares(ctx, tx, true, `
			SELECT COALESCE(c.id::text, ''), COALESCE(c.name, 'Uncategorised'), sum(l.line_total)::float8, count(DISTINCT so.id)::int,
			       trim_scale(sum(l.quantity))::text
			FROM sales_order_lines l JOIN sales_orders so ON so.id = l.sales_order_id
			JOIN product_variants v ON v.id = l.variant_id JOIN products p ON p.id = v.product_id
			LEFT JOIN categories c ON c.id = p.category_id
			WHERE `+completed+` AND `+inRange+` GROUP BY 1, 2 ORDER BY 3 DESC LIMIT 10`, args...)
	case "branch_sales":
		return shares(ctx, tx, false, `
			SELECT b.id::text, b.name, sum(so.grand_total + so.round_off)::float8, count(*)::int
			FROM sales_orders so JOIN branches b ON b.id = so.branch_id
			WHERE `+completed+` AND `+inRange+` GROUP BY 1, 2 ORDER BY 3 DESC`, args...)
	case "hourly_sales":
		return series(ctx, tx, `
			SELECT h::text, lpad(h::text, 2, '0') || ':00', COALESCE(s.sales, 0)::float8, COALESCE(s.orders, 0)
			FROM generate_series(0, 23) h
			LEFT JOIN (SELECT extract(hour FROM (`+moment+`) AT TIME ZONE app_merchant_tz())::int AS hr, sum(so.grand_total + so.round_off) sales, count(*) orders
			           FROM sales_orders so WHERE `+completed+` AND `+inRange+` GROUP BY 1) s ON s.hr = h
			ORDER BY h`, args...)
	}
	return nil, fmt.Errorf("unknown widget %q", key)
}

// ------------------------------------------------------------------ handlers

func unauthorized(w http.ResponseWriter) {
	httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
}

// Dashboard: GET /dashboard?from=&to=&branch_id=&channel= — the caller's
// widgets and their data. Without dashboard.view: 403 with code
// DASHBOARD_NOT_ALLOWED (the screen then shows its shortcuts only).
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		unauthorized(w)
		return
	}
	f, err := parseFilters(r, locale.MerchantLocation(r.Context(), h.DB, claims.TenantID))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	var keys []string
	data := map[string]any{}
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if keys, err = visibleWidgets(ctx, tx, claims.UserID); err != nil || keys == nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = 20000`); err != nil {
			return err
		}
		for _, k := range keys {
			v, err := buildWidget(ctx, tx, k, f)
			if err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
			data[k] = v
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build the dashboard")
		return
	}
	if keys == nil {
		httpx.Error(w, http.StatusForbidden, "DASHBOARD_NOT_ALLOWED", "your role doesn't include the sales dashboard (dashboard.view)")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"widgets": keys, "data": data,
		"filters": map[string]any{"from": f.fromS(), "to": f.toS(), "branch_id": f.BranchID, "channel": f.Channel},
	})
}

// Catalog: GET /dashboard/widgets — every widget there is.
func (h *Handler) Catalog(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"widgets": Widgets})
}

type roleConfig struct {
	RoleID     string   `json:"role_id"`
	RoleName   string   `json:"role_name"`
	CanView    bool     `json:"can_view"` // the role has dashboard.view
	Widgets    []string `json:"widgets"`
	Customized bool     `json:"customized"` // false = the default (every widget)
	UpdatedBy  *string  `json:"updated_by"`
	UpdatedAt  *string  `json:"updated_at"`
}

// ListRoles: GET /dashboard/roles — dashboard.configure: every role, whether
// it may see the dashboard, and its widgets.
func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		unauthorized(w)
		return
	}
	out := []roleConfig{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT r.id, r.name,
			       EXISTS (SELECT 1 FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id
			               WHERE rp.role_id = r.id AND p.code = 'dashboard.view'),
			       drw.widgets, u.name, to_char(drw.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
			FROM roles r
			LEFT JOIN dashboard_role_widgets drw ON drw.role_id = r.id
			LEFT JOIN users u ON u.id = drw.updated_by
			ORDER BY r.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c roleConfig
			var ws []string
			if err := rows.Scan(&c.RoleID, &c.RoleName, &c.CanView, &ws, &c.UpdatedBy, &c.UpdatedAt); err != nil {
				return err
			}
			c.Customized = ws != nil
			c.Widgets = ws
			if ws == nil {
				c.Widgets = widgetKeys()
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the dashboard roles")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"roles": out, "widgets": Widgets})
}

// SetRole: PUT /dashboard/roles/{id} {widgets: [...] | null} —
// dashboard.configure. The widgets in display order; null puts the role
// back on the default (every widget). Seeing the dashboard at all is the
// role's dashboard.view permission (Roles & Permissions).
func (h *Handler) SetRole(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		unauthorized(w)
		return
	}
	var req struct {
		Widgets *[]string `json:"widgets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var clean []string
	if req.Widgets != nil {
		clean = []string{}
		seen := map[string]bool{}
		for _, k := range *req.Widgets {
			if !isWidget(k) {
				httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "unknown widget "+k)
				return
			}
			if !seen[k] {
				seen[k] = true
				clean = append(clean, k)
			}
		}
	}
	roleID := chi.URLParam(r, "id")
	var name string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM roles WHERE id::text = $1`, roleID).Scan(&name); err != nil {
			return err
		}
		var before []string
		_ = tx.QueryRow(ctx, `SELECT widgets FROM dashboard_role_widgets WHERE role_id::text = $1`, roleID).Scan(&before)
		if clean == nil {
			if _, err := tx.Exec(ctx, `DELETE FROM dashboard_role_widgets WHERE role_id::text = $1`, roleID); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `
			INSERT INTO dashboard_role_widgets (merchant_id, role_id, widgets, updated_by, updated_at)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2, $3, now())
			ON CONFLICT (merchant_id, role_id) DO UPDATE SET widgets = $2, updated_by = $3, updated_at = now()`,
			roleID, clean, claims.UserID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "dashboard_role_widgets", EntityID: roleID, Action: "update", PerformedBy: claims.UserID,
			Before: map[string]any{"role": name, "widgets": before}, After: map[string]any{"role": name, "widgets": clean}})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "role not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the role's dashboard")
		return
	}
	widgets := clean
	if widgets == nil {
		widgets = widgetKeys()
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"role_id": roleID, "role_name": name, "widgets": widgets, "customized": clean != nil})
}

type target struct {
	BranchID      *string `json:"branch_id"`
	BranchName    *string `json:"branch_name"`
	MonthlyTarget string  `json:"monthly_target"`
}

func loadTargets(ctx context.Context, tx pgx.Tx) ([]target, error) {
	rows, err := tx.Query(ctx, `
		SELECT t.branch_id::text, b.name, t.monthly_target::text FROM sales_targets t LEFT JOIN branches b ON b.id = t.branch_id
		ORDER BY t.branch_id IS NOT NULL, b.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []target{}
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.BranchID, &t.BranchName, &t.MonthlyTarget); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Targets: GET /dashboard/targets — dashboard.view.
func (h *Handler) Targets(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		unauthorized(w)
		return
	}
	var out []target
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = loadTargets(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the sales targets")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"targets": out})
}

// SetTargets: PUT /dashboard/targets {targets: [{branch_id|null, monthly_target}]}
// — dashboard.configure. Replaces them all; a branch_id of null is the
// whole business's target.
func (h *Handler) SetTargets(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		unauthorized(w)
		return
	}
	var req struct {
		Targets []struct {
			BranchID      *string `json:"branch_id"`
			MonthlyTarget float64 `json:"monthly_target"`
		} `json:"targets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	seen := map[string]bool{}
	for _, t := range req.Targets {
		k := ""
		if t.BranchID != nil {
			k = strings.TrimSpace(*t.BranchID)
		}
		if t.MonthlyTarget <= 0 || seen[k] {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "each target needs an amount above zero, and one per branch (and one for the whole business)")
			return
		}
		seen[k] = true
	}
	var out []target
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := loadTargets(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sales_targets`); err != nil {
			return err
		}
		for _, t := range req.Targets {
			var b *string
			if t.BranchID != nil && strings.TrimSpace(*t.BranchID) != "" {
				b = t.BranchID
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO sales_targets (merchant_id, branch_id, monthly_target, updated_by)
				VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, round($2::numeric, 2), $3)`, b, t.MonthlyTarget, claims.UserID); err != nil {
				return err
			}
		}
		if out, err = loadTargets(ctx, tx); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "sales_targets", EntityID: claims.TenantID, Action: "update", PerformedBy: claims.UserID,
			Before: before, After: out})
	})
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not save the targets: check the branches")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"targets": out})
}
