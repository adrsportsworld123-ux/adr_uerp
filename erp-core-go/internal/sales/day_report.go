package sales

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/documents"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/printing"
)

// The day-wise Z report (migrations/056, phase0_1_design.md §3.60). The X
// report is one till's shift (shifts.go: live while it's open, frozen when
// it closes); the Z report is one branch's whole business day: every till,
// every shift, sales by tender, tax rate, channel and cashier, returns,
// voids and the drawer cash. Until the day is closed it's computed live;
// closing it (reports.day_close) freezes it with the branch's next Z
// number, so every reprint is the same document. A sale that syncs late
// into a closed day doesn't change the frozen report: the live figures are
// shown beside it as a difference instead.
//
// A business day is the calendar day in India Standard Time, whatever the
// database server's time zone. A sale counts at the moment money changed
// hands: device_created_at for one made offline, else finalized_at (the
// same moment the shift X report and the cash count use).

var (
	errDayAlreadyClosed = errors.New("day already closed")
	errShiftsStillOpen  = errors.New("shifts still open")
	errFutureDay        = errors.New("that day hasn't ended")
	ist                 = time.FixedZone("IST", 5*3600+30*60)
)

type dayTaxRate struct {
	Rate    string `json:"rate"`
	Taxable string `json:"taxable_value"`
	Tax     string `json:"tax"`
}

type dayCashier struct {
	Cashier  string `json:"cashier"`
	Orders   int    `json:"orders"`
	NetSales string `json:"net_sales"`
}

type dayShift struct {
	ShiftID     string  `json:"shift_id"`
	ShiftNumber int     `json:"shift_number"`
	Terminal    string  `json:"terminal_name"`
	Status      string  `json:"status"`
	OpenedBy    string  `json:"opened_by"`
	OpenedAt    string  `json:"opened_at"`
	ClosedAt    *string `json:"closed_at"`
	OrderCount  int     `json:"order_count"`
	NetSales    string  `json:"net_sales"`
	Expected    string  `json:"expected_cash"`
	Counted     *string `json:"counted_cash"`
	Variance    *string `json:"variance"`
}

type dayCash struct {
	CashSales   string `json:"cash_sales"`
	CashRefunds string `json:"cash_refunds"`
	FloatIn     string `json:"float_in"`
	Drops       string `json:"drops"`
	PaidIn      string `json:"paid_in"`
	PaidOut     string `json:"paid_out"`
	NetCash     string `json:"net_cash"`
	Counted     string `json:"counted"`  // sum of the closed shifts' counts
	Variance    string `json:"variance"` // sum of the closed shifts' variances
}

type dayReport struct {
	Kind          string            `json:"kind"` // "Z"
	BranchID      string            `json:"branch_id"`
	BranchName    string            `json:"branch_name"`
	Date          string            `json:"date"`
	Status        string            `json:"status"` // "open" (live) | "closed" (frozen)
	ZNumber       *int              `json:"z_number"`
	ClosedBy      *string           `json:"closed_by"`
	ClosedAt      *string           `json:"closed_at"`
	GeneratedAt   string            `json:"generated_at"`
	FirstOrder    *string           `json:"first_order_number"`
	LastOrder     *string           `json:"last_order_number"`
	OrderCount    int               `json:"order_count"`
	Subtotal      string            `json:"subtotal"`
	DiscountTotal string            `json:"discount_total"`
	TaxTotal      string            `json:"tax_total"`
	NetSales      string            `json:"net_sales"`
	CreditSales   string            `json:"credit_sales"`
	AverageBill   string            `json:"average_bill"`
	VoidedCount   int               `json:"voided_count"`
	VoidedTotal   string            `json:"voided_total"`
	ReleasedCount int               `json:"released_count"`
	ByMethod      map[string]string `json:"by_payment_method"`
	ByChannel     map[string]string `json:"by_channel"`
	ReturnCount   int               `json:"return_count"`
	RefundTotal   string            `json:"refund_total"`
	RefundsMethod map[string]string `json:"refunds_by_method"`
	TaxRates      []dayTaxRate      `json:"tax_rates"`
	Cashiers      []dayCashier      `json:"cashiers"`
	Cash          dayCash           `json:"cash"`
	Shifts        []dayShift        `json:"shifts"`
	OpenShifts    int               `json:"open_shifts"`
	// Only on a closed day whose live figures no longer match the frozen
	// ones (a sale synced late, a later void): what changed since.
	Since *daySince `json:"changed_since_close,omitempty"`
}

type daySince struct {
	OrderCount int    `json:"order_count"`
	NetSales   string `json:"net_sales"`
	Refunds    string `json:"refund_total"`
}

// dayWindow is the business day's [from, to) in IST.
func dayWindow(date string) (time.Time, time.Time, error) {
	d, err := time.ParseInLocation("2006-01-02", date, ist)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return d, d.Add(24 * time.Hour), nil
}

func todayIST() string { return time.Now().In(ist).Format("2006-01-02") }

const dayMoment = `COALESCE(so.device_created_at, so.finalized_at)`

// buildDayReport computes branchID's business day live.
func buildDayReport(ctx context.Context, tx pgx.Tx, merchantID, branchID, date string) (dayReport, error) {
	rep := dayReport{Kind: "Z", BranchID: branchID, Date: date, Status: "open", ByMethod: map[string]string{}, ByChannel: map[string]string{},
		RefundsMethod: map[string]string{}, TaxRates: []dayTaxRate{}, Cashiers: []dayCashier{}, Shifts: []dayShift{}}
	from, to, err := dayWindow(date)
	if err != nil {
		return rep, err
	}
	if err := tx.QueryRow(ctx, `SELECT name FROM branches WHERE id::text = $1`, branchID).Scan(&rep.BranchName); err != nil {
		return rep, err
	}
	const sold = `so.branch_id::text = $1 AND so.status IN ('finalized', 'partially_refunded', 'refunded') AND ` + dayMoment + ` >= $2 AND ` + dayMoment + ` < $3`
	var sub, disc, tax, net, credit float64
	if err := tx.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(so.subtotal), 0), COALESCE(sum(so.discount_total), 0), COALESCE(sum(so.tax_total), 0),
		       COALESCE(sum(so.grand_total), 0), COALESCE(sum(so.credit_amount), 0),
		       (array_agg(so.order_number ORDER BY `+dayMoment+`, so.order_number))[1],
		       (array_agg(so.order_number ORDER BY `+dayMoment+` DESC, so.order_number DESC))[1]
		FROM sales_orders so WHERE `+sold, branchID, from, to).
		Scan(&rep.OrderCount, &sub, &disc, &tax, &net, &credit, &rep.FirstOrder, &rep.LastOrder); err != nil {
		return rep, err
	}
	rep.Subtotal, rep.DiscountTotal, rep.TaxTotal, rep.NetSales, rep.CreditSales = money(sub), money(disc), money(tax), money(net), money(credit)
	rep.AverageBill = money(0)
	if rep.OrderCount > 0 {
		rep.AverageBill = money(round2(net / float64(rep.OrderCount)))
	}

	var voided float64
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE so.status = 'voided'), COALESCE(sum(so.grand_total) FILTER (WHERE so.status = 'voided'), 0),
		       count(*) FILTER (WHERE so.status = 'held_voided')
		FROM sales_orders so
		WHERE so.branch_id::text = $1 AND so.status IN ('voided', 'held_voided')
		  AND COALESCE(so.voided_at, so.finalized_at, so.created_at) >= $2 AND COALESCE(so.voided_at, so.finalized_at, so.created_at) < $3`,
		branchID, from, to).Scan(&rep.VoidedCount, &voided, &rep.ReleasedCount); err != nil {
		return rep, err
	}
	rep.VoidedTotal = money(voided)

	collect := func(q string, into map[string]string, args ...any) error {
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var v float64
			if err := rows.Scan(&k, &v); err != nil {
				return err
			}
			into[k] = money(v)
		}
		return rows.Err()
	}
	if err := collect(`
		SELECT p.method, sum(p.amount) FROM payments p JOIN sales_orders so ON so.id = p.sales_order_id
		WHERE `+sold+` AND p.status = 'captured' GROUP BY p.method`, rep.ByMethod, branchID, from, to); err != nil {
		return rep, err
	}
	if credit > 0 {
		rep.ByMethod["credit"] = money(credit)
	}
	if err := collect(`SELECT so.channel, sum(so.grand_total) FROM sales_orders so WHERE `+sold+` GROUP BY so.channel`, rep.ByChannel, branchID, from, to); err != nil {
		return rep, err
	}

	var refunds float64
	rows, err := tx.Query(ctx, `
		SELECT refund_method, count(*), sum(refund_amount) FROM sales_returns
		WHERE branch_id::text = $1 AND created_at >= $2 AND created_at < $3 GROUP BY refund_method`, branchID, from, to)
	if err != nil {
		return rep, err
	}
	for rows.Next() {
		var m string
		var n int
		var a float64
		if err := rows.Scan(&m, &n, &a); err != nil {
			rows.Close()
			return rep, err
		}
		rep.ReturnCount += n
		refunds += a
		rep.RefundsMethod[m] = money(a)
	}
	rows.Close()
	rep.RefundTotal = money(refunds)

	// GST by rate: the rate on each line's product slab.
	rows, err = tx.Query(ctx, `
		SELECT COALESCE(ts.cgst_rate + ts.sgst_rate + ts.igst_rate, 0)::text,
		       sum(COALESCE(l.taxable_value, l.line_total - l.tax_amount))::text, sum(l.tax_amount)::text
		FROM sales_order_lines l
		JOIN sales_orders so ON so.id = l.sales_order_id
		JOIN product_variants v ON v.id = l.variant_id
		JOIN products p ON p.id = v.product_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		WHERE `+sold+` GROUP BY 1 ORDER BY 1::numeric`, branchID, from, to)
	if err != nil {
		return rep, err
	}
	for rows.Next() {
		var t dayTaxRate
		if err := rows.Scan(&t.Rate, &t.Taxable, &t.Tax); err != nil {
			rows.Close()
			return rep, err
		}
		t.Rate = strings.TrimRight(strings.TrimRight(t.Rate, "0"), ".")
		if t.Rate == "" {
			t.Rate = "0"
		}
		rep.TaxRates = append(rep.TaxRates, t)
	}
	rows.Close()

	rows, err = tx.Query(ctx, `
		SELECT COALESCE(u.name, '—'), count(*), sum(so.grand_total)
		FROM sales_orders so LEFT JOIN users u ON u.id = so.cashier_id
		WHERE `+sold+` GROUP BY 1 ORDER BY 3 DESC`, branchID, from, to)
	if err != nil {
		return rep, err
	}
	for rows.Next() {
		var c dayCashier
		var v float64
		if err := rows.Scan(&c.Cashier, &c.Orders, &v); err != nil {
			rows.Close()
			return rep, err
		}
		c.NetSales = money(v)
		rep.Cashiers = append(rep.Cashiers, c)
	}
	rows.Close()

	flow, err := accounting.ComputeCashFlow(ctx, tx, accounting.CashScope{BranchID: branchID, From: &from, To: &to})
	if err != nil {
		return rep, err
	}
	rep.Cash = dayCash{CashSales: money(flow.CashSales), CashRefunds: money(flow.CashRefunds), FloatIn: money(flow.FloatIn),
		Drops: money(flow.Drops), PaidIn: money(flow.PaidIn), PaidOut: money(flow.PaidOut), NetCash: money(flow.Net())}

	// Every shift that was open at some point of the day, with its own X.
	var ids []string
	rows, err = tx.Query(ctx, `
		SELECT id FROM pos_shifts WHERE branch_id::text = $1 AND opened_at < $3 AND (closed_at IS NULL OR closed_at >= $2)
		ORDER BY opened_at`, branchID, from, to)
	if err != nil {
		return rep, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return rep, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var counted, variance float64
	for _, id := range ids {
		s, err := loadShift(ctx, tx, merchantID, id, false)
		if err != nil {
			return rep, err
		}
		if err := withReport(ctx, tx, &s); err != nil {
			return rep, err
		}
		ds := dayShift{ShiftID: s.ShiftID, ShiftNumber: s.ShiftNumber, Terminal: s.TerminalName, Status: s.Status, OpenedBy: s.OpenedBy,
			OpenedAt: s.OpenedAt, ClosedAt: s.ClosedAt, OrderCount: s.Report.OrderCount, NetSales: s.Report.NetSales, Expected: s.Report.Cash.Expected}
		if c := s.Report.Close; c != nil {
			ds.Counted, ds.Variance = &c.CountedTotal, &c.Variance
			var a, b float64
			fmt.Sscan(c.CountedTotal, &a)
			fmt.Sscan(c.Variance, &b)
			counted += a
			variance += b
		}
		if s.Status == "open" {
			rep.OpenShifts++
		}
		rep.Shifts = append(rep.Shifts, ds)
	}
	rep.Cash.Counted, rep.Cash.Variance = money(round2(counted)), money(round2(variance))
	rep.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	return rep, nil
}

// loadDayReport: the frozen Z report when the day is closed (with what has
// changed since, if anything), else the live one.
func loadDayReport(ctx context.Context, tx pgx.Tx, merchantID, branchID, date string) (dayReport, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT report FROM z_reports WHERE branch_id::text = $1 AND business_date = $2::date`, branchID, date).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return buildDayReport(ctx, tx, merchantID, branchID, date)
	} else if err != nil {
		return dayReport{}, err
	}
	var frozen dayReport
	if err := json.Unmarshal(raw, &frozen); err != nil {
		return frozen, err
	}
	live, err := buildDayReport(ctx, tx, merchantID, branchID, date)
	if err != nil {
		return frozen, err
	}
	if live.OrderCount != frozen.OrderCount || live.NetSales != frozen.NetSales || live.RefundTotal != frozen.RefundTotal {
		var ln, fn, lr, fr float64
		fmt.Sscan(live.NetSales, &ln)
		fmt.Sscan(frozen.NetSales, &fn)
		fmt.Sscan(live.RefundTotal, &lr)
		fmt.Sscan(frozen.RefundTotal, &fr)
		frozen.Since = &daySince{OrderCount: live.OrderCount - frozen.OrderCount, NetSales: money(round2(ln - fn)), Refunds: money(round2(lr - fr))}
	}
	return frozen, nil
}

func dayParams(w http.ResponseWriter, r *http.Request) (branchID, date string, ok bool) {
	q := r.URL.Query()
	branchID, date = q.Get("branch_id"), q.Get("date")
	if date == "" {
		date = todayIST()
	}
	if branchID == "" || !isValidDate(date) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id and a date (YYYY-MM-DD) are required")
		return "", "", false
	}
	return branchID, date, true
}

// ZReport: GET /reports/z-report?branch_id=&date= — the branch's day.
func (h *Handler) ZReport(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID, date, ok := dayParams(w, r)
	if !ok {
		return
	}
	var rep dayReport
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		rep, err = loadDayReport(ctx, tx, claims.TenantID, branchID, date)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build the Z report")
		return
	}
	httpx.JSON(w, http.StatusOK, rep)
}

// CloseDay: POST /reports/z-report/close {branch_id, date} —
// reports.day_close. Freezes the day's Z report with the branch's next Z
// number. Refused while a shift that was open during the day is still open
// (its sales and count would be missing), and for today or a later day,
// which hasn't ended. Idempotent on (branch, date): closing a closed day
// returns its Z report with "replayed": true.
func (h *Handler) CloseDay(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		BranchID string `json:"branch_id"`
		Date     string `json:"date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BranchID == "" || !isValidDate(req.Date) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id and date (YYYY-MM-DD) are required")
		return
	}
	var rep dayReport
	replayed := false
	var openTills []string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// One close at a time per branch: the Z number is the next one.
		if err := tx.QueryRow(ctx, `SELECT id FROM branches WHERE id::text = $1 FOR UPDATE`, req.BranchID).Scan(new(string)); err != nil {
			return err
		}
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT report FROM z_reports WHERE branch_id::text = $1 AND business_date = $2::date`, req.BranchID, req.Date).Scan(&raw)
		if err == nil {
			replayed = true
			return json.Unmarshal(raw, &rep)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if req.Date >= todayIST() {
			return errFutureDay
		}
		var err2 error
		if rep, err2 = buildDayReport(ctx, tx, claims.TenantID, req.BranchID, req.Date); err2 != nil {
			return err2
		}
		if rep.OpenShifts > 0 {
			for _, s := range rep.Shifts {
				if s.Status == "open" {
					openTills = append(openTills, fmt.Sprintf("%s (shift %d)", s.Terminal, s.ShiftNumber))
				}
			}
			return errShiftsStillOpen
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(z_number), 0) + 1 FROM z_reports WHERE branch_id::text = $1`, req.BranchID).Scan(&n); err != nil {
			return err
		}
		var by string
		if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id::text = $1`, claims.UserID).Scan(&by); err != nil {
			return err
		}
		at := time.Now().UTC().Format(time.RFC3339)
		rep.Status, rep.ZNumber, rep.ClosedBy, rep.ClosedAt = "closed", &n, &by, &at
		raw, _ = json.Marshal(rep)
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO z_reports (merchant_id, branch_id, business_date, z_number, report, closed_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::date, $3, $4, $5) RETURNING id`,
			req.BranchID, req.Date, n, raw, claims.UserID).Scan(&id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "z_reports", EntityID: id, Action: "create", PerformedBy: claims.UserID,
			After: map[string]any{"branch": rep.BranchName, "date": rep.Date, "z_number": n, "order_count": rep.OrderCount, "net_sales": rep.NetSales}})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
	case errors.Is(err, errFutureDay):
		httpx.Error(w, http.StatusConflict, "DAY_NOT_OVER", "only a day that has ended can be closed: today's Z report stays live until tomorrow")
	case errors.Is(err, errShiftsStillOpen):
		httpx.ErrorWithDetails(w, http.StatusConflict, "SHIFT_STILL_OPEN",
			"close these shifts first, so the day's Z report has every sale and drawer count: "+strings.Join(openTills, ", "),
			map[string]any{"open_shifts": openTills})
	case isUniqueViolation(err):
		httpx.Error(w, http.StatusConflict, "DAY_ALREADY_CLOSED", "this day was just closed by someone else; reload it")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not close the day")
	default:
		status := http.StatusCreated
		if replayed {
			status = http.StatusOK
		}
		httpx.JSON(w, status, map[string]any{"report": rep, "replayed": replayed})
	}
}

// ListZReports: GET /reports/z-reports?branch_id=&from=&to= — closed days,
// newest first (at most 400).
func (h *Handler) ListZReports(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	for _, d := range []string{from, to} {
		if d != "" && !isValidDate(d) {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "from/to must be YYYY-MM-DD")
			return
		}
	}
	type row struct {
		ZNumber    int    `json:"z_number"`
		BranchID   string `json:"branch_id"`
		BranchName string `json:"branch_name"`
		Date       string `json:"date"`
		OrderCount int    `json:"order_count"`
		NetSales   string `json:"net_sales"`
		Refunds    string `json:"refund_total"`
		Variance   string `json:"cash_variance"`
		ClosedBy   string `json:"closed_by"`
		ClosedAt   string `json:"closed_at"`
	}
	list := []row{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT z.z_number, z.branch_id, b.name, z.business_date::text, (z.report->>'order_count')::int, z.report->>'net_sales',
			       z.report->>'refund_total', COALESCE(z.report->'cash'->>'variance', '0.00'), u.name,
			       to_char(z.closed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
			FROM z_reports z JOIN branches b ON b.id = z.branch_id JOIN users u ON u.id = z.closed_by
			WHERE ($1 = '' OR z.branch_id::text = $1) AND ($2 = '' OR z.business_date >= $2::date) AND ($3 = '' OR z.business_date <= $3::date)
			ORDER BY z.business_date DESC, b.name LIMIT 400`, q.Get("branch_id"), from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x row
			if err := rows.Scan(&x.ZNumber, &x.BranchID, &x.BranchName, &x.Date, &x.OrderCount, &x.NetSales, &x.Refunds, &x.Variance, &x.ClosedBy, &x.ClosedAt); err != nil {
				return err
			}
			list = append(list, x)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list Z reports")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"z_reports": list})
}

// ---------------------------------------------------------------- printing

var methodLabel = map[string]string{"cash": "Cash", "card": "Card", "upi": "UPI", "credit": "On credit", "bank_transfer": "Bank transfer",
	"cheque": "Cheque", "wallet": "Wallet", "gateway": "Online", "store_credit": "Store credit"}

func label(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return strings.ToUpper(k[:1]) + strings.ReplaceAll(k[1:], "_", " ")
}

func sortedRows(m map[string]string, labels map[string]string) []printing.ReportRow {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]printing.ReportRow, 0, len(keys))
	for _, k := range keys {
		out = append(out, printing.ReportRow{Label: label(labels, k), Value: m[k]})
	}
	return out
}

func printedAt() string { return "Printed " + time.Now().In(ist).Format("02 Jan 2006 15:04") }

func istTime(rfc string) string {
	t, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		return rfc
	}
	return t.In(ist).Format("02 Jan 15:04")
}

func dayReportPrint(lh documents.Letterhead, d dayReport) printing.ReportPrint {
	sub := []string{d.Date}
	if d.Status == "closed" && d.ZNumber != nil {
		sub = append(sub, fmt.Sprintf("Z no. %d · closed by %s", *d.ZNumber, *d.ClosedBy))
	} else {
		sub = append(sub, "DAY NOT CLOSED - figures so far")
	}
	sub = append(sub, printedAt())
	sales := []printing.ReportRow{
		{Label: "Sales", Value: fmt.Sprint(d.OrderCount)},
		{Label: "Subtotal", Value: d.Subtotal},
		{Label: "Discounts", Value: "-" + d.DiscountTotal},
		{Label: "Tax", Value: d.TaxTotal},
		{Label: "Net sales", Value: d.NetSales, Strong: true},
		{Label: "Average bill", Value: d.AverageBill},
	}
	if d.FirstOrder != nil {
		sales = append(sales, printing.ReportRow{Label: "First: " + *d.FirstOrder}, printing.ReportRow{Label: "Last:  " + *d.LastOrder})
	}
	tax := []printing.ReportRow{}
	for _, t := range d.TaxRates {
		tax = append(tax, printing.ReportRow{Label: "GST " + t.Rate + "% on " + t.Taxable, Value: t.Tax})
	}
	returns := []printing.ReportRow{{Label: "Returns", Value: fmt.Sprint(d.ReturnCount)}, {Label: "Refunded", Value: d.RefundTotal}}
	returns = append(returns, sortedRows(d.RefundsMethod, methodLabel)...)
	returns = append(returns, printing.ReportRow{Label: "Voided sales", Value: fmt.Sprintf("%d (%s)", d.VoidedCount, d.VoidedTotal)},
		printing.ReportRow{Label: "Released carts", Value: fmt.Sprint(d.ReleasedCount)})
	cash := []printing.ReportRow{
		{Label: "Cash sales", Value: d.Cash.CashSales}, {Label: "Cash refunds", Value: "-" + d.Cash.CashRefunds},
		{Label: "Float in", Value: d.Cash.FloatIn}, {Label: "Drops", Value: "-" + d.Cash.Drops},
		{Label: "Paid in", Value: d.Cash.PaidIn}, {Label: "Paid out", Value: "-" + d.Cash.PaidOut},
		{Label: "Net cash", Value: d.Cash.NetCash, Strong: true},
		{Label: "Counted (closed shifts)", Value: d.Cash.Counted}, {Label: "Variance", Value: d.Cash.Variance},
	}
	shifts := []printing.ReportRow{}
	for _, s := range d.Shifts {
		v := s.NetSales
		if s.Variance != nil {
			v += " / " + *s.Variance
		} else if s.Status == "open" {
			v += " OPEN"
		}
		shifts = append(shifts, printing.ReportRow{Label: fmt.Sprintf("%s #%d", s.Terminal, s.ShiftNumber), Value: v})
	}
	cashiers := []printing.ReportRow{}
	for _, c := range d.Cashiers {
		cashiers = append(cashiers, printing.ReportRow{Label: fmt.Sprintf("%s (%d)", c.Cashier, c.Orders), Value: c.NetSales})
	}
	sections := []printing.ReportSection{
		{Heading: "Sales", Rows: sales},
		{Heading: "Tenders", Rows: sortedRows(d.ByMethod, methodLabel)},
		{Heading: "Channels", Rows: sortedRows(d.ByChannel, map[string]string{"pos": "POS", "backoffice": "Back office", "online": "Online"})},
		{Heading: "Tax", Rows: tax},
		{Heading: "Returns & voids", Rows: returns},
		{Heading: "Cash", Rows: cash},
		{Heading: "Shifts (net / variance)", Rows: shifts},
		{Heading: "Cashiers", Rows: cashiers},
	}
	footer := []string{}
	if d.Since != nil {
		footer = append(footer, fmt.Sprintf("Changed since close: %+d sales, %s", d.Since.OrderCount, d.Since.NetSales))
	}
	return printing.ReportPrint{MerchantName: lh.Name, BranchName: d.BranchName, Header: lh.Header, Title: "Z REPORT", Subtitle: sub,
		Sections: sections, Footer: footer}
}

func shiftReportPrint(lh documents.Letterhead, branchName string, s shiftResponse) printing.ReportPrint {
	rep := s.Report
	sub := []string{fmt.Sprintf("%s · shift %d", s.TerminalName, s.ShiftNumber), "Opened " + istTime(s.OpenedAt) + " by " + s.OpenedBy}
	if s.ClosedAt != nil {
		sub = append(sub, "Closed "+istTime(*s.ClosedAt)+" by "+*s.ClosedBy)
	} else {
		sub = append(sub, "SHIFT OPEN - figures so far")
	}
	sub = append(sub, printedAt())
	sales := []printing.ReportRow{{Label: "Sales", Value: fmt.Sprint(rep.OrderCount)}, {Label: "Net sales", Value: rep.NetSales, Strong: true}}
	if rep.VoidedCount > 0 {
		sales = append(sales, printing.ReportRow{Label: "Voided", Value: fmt.Sprint(rep.VoidedCount)})
	}
	returns := []printing.ReportRow{{Label: "Returns", Value: fmt.Sprint(rep.ReturnCount)}, {Label: "Refunded", Value: rep.RefundTotal}}
	returns = append(returns, sortedRows(rep.RefundsMethod, methodLabel)...)
	c := rep.Cash
	cash := []printing.ReportRow{
		{Label: "Opening float", Value: c.OpeningFloat}, {Label: "Cash sales", Value: c.CashSales}, {Label: "Cash refunds", Value: "-" + c.CashRefunds},
		{Label: "Float in", Value: c.FloatIn}, {Label: "Drops", Value: "-" + c.Drops}, {Label: "Paid in", Value: c.PaidIn},
		{Label: "Paid out", Value: "-" + c.PaidOut}, {Label: "Expected in drawer", Value: c.Expected, Strong: true},
	}
	if cl := rep.Close; cl != nil {
		cash = append(cash, printing.ReportRow{Label: "Counted", Value: cl.CountedTotal}, printing.ReportRow{Label: "Variance", Value: cl.Variance, Strong: true})
		if cl.Reason != "" {
			cash = append(cash, printing.ReportRow{Label: "Reason: " + cl.Reason})
		}
	}
	moves := []printing.ReportRow{}
	for _, m := range rep.Movements {
		moves = append(moves, printing.ReportRow{Label: label(map[string]string{"float_in": "Float in", "drop": "Drop", "paid_in": "Paid in", "paid_out": "Paid out"}, m.Kind) + " " + istTime(m.CreatedAt), Value: m.Amount})
	}
	sections := []printing.ReportSection{
		{Heading: "Sales", Rows: sales},
		{Heading: "Tenders", Rows: sortedRows(rep.ByMethod, methodLabel)},
		{Heading: "Returns", Rows: returns},
		{Heading: "Drawer cash", Rows: cash},
	}
	if len(moves) > 0 {
		sections = append(sections, printing.ReportSection{Heading: "Cash movements", Rows: moves})
	}
	return printing.ReportPrint{MerchantName: lh.Name, BranchName: branchName, Header: lh.Header, Title: "X REPORT", Subtitle: sub, Sections: sections}
}

// PrintZReport: GET /reports/z-report/print?branch_id=&date= — ESC/POS.
func (h *Handler) PrintZReport(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID, date, ok := dayParams(w, r)
	if !ok {
		return
	}
	var out []byte
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rep, err := loadDayReport(ctx, tx, claims.TenantID, branchID, date)
		if err != nil {
			return err
		}
		lh, _, err := documents.LoadLetterhead(ctx, tx, branchID, documents.KindReceipt)
		if err != nil {
			return err
		}
		out = printing.BuildReport(dayReportPrint(lh, rep))
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build the Z report")
		return
	}
	httpx.Binary(w, http.StatusOK, "application/octet-stream", out)
}

// PrintShiftReport: GET /pos-shifts/{id}/report/print — the shift's X
// report (live while open, as closed after) as ESC/POS.
func (h *Handler) PrintShiftReport(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var out []byte
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		s, err := loadShift(ctx, tx, claims.TenantID, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		if err := withReport(ctx, tx, &s); err != nil {
			return err
		}
		lh, _, err := documents.LoadLetterhead(ctx, tx, s.BranchID, documents.KindReceipt)
		if err != nil {
			return err
		}
		out = printing.BuildReport(shiftReportPrint(lh, s.BranchName, s))
		return nil
	})
	if err != nil {
		writeShiftError(w, err, "could not build the X report")
		return
	}
	httpx.Binary(w, http.StatusOK, "application/octet-stream", out)
}
