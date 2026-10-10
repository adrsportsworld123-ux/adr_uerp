package accounting

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Phase 13: what the cash drawer should hold, computed ONE way for both
// the daily whole-branch count (CreateCashReconciliation) and a shift's
// Z close (internal/sales/shifts.go), so a shift close reconciles to the
// same figure the cash reconciliation would compute for that till and
// window. See migrations/040_pos_shifts.sql.

// CashScope picks the sales and movements that went through one drawer:
// a whole branch on a calendar day (Date), or one till over a shift's
// window (TerminalID + From/To).
type CashScope struct {
	BranchID   string
	TerminalID string     // "" = every till in the branch
	Date       string     // YYYY-MM-DD; used when From is nil
	From, To   *time.Time // a shift's window; To nil = up to now
}

// CashFlow is the drawer's movement within a scope. The time a sale counts
// at is when the money changed hands: device_created_at for a sale made
// offline (it syncs later), else finalized_at.
type CashFlow struct {
	CashSales   float64 // cash captured on sales that still stand (finalized, or partly/fully refunded since)
	CashRefunds float64 // cash paid back by returns
	FloatIn     float64
	Drops       float64
	PaidIn      float64
	PaidOut     float64
}

// Net is the change in drawer cash over the scope, before the opening float.
func (c CashFlow) Net() float64 {
	return round2(c.CashSales - c.CashRefunds + c.FloatIn - c.Drops + c.PaidIn - c.PaidOut)
}

const saleMoment = `COALESCE(so.device_created_at, so.finalized_at)`

// ComputeCashFlow sums the scope's cash. Voided sales are excluded (a void
// reverses the whole sale); refunded ones are included, with the refund
// itself subtracted when it was paid in cash.
func ComputeCashFlow(ctx context.Context, tx pgx.Tx, s CashScope) (CashFlow, error) {
	var f CashFlow
	var date *string
	if s.From == nil && s.Date != "" {
		date = &s.Date
	}
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(p.amount), 0)
		FROM payments p
		JOIN sales_orders so ON so.id = p.sales_order_id
		WHERE so.branch_id = $1 AND ($2 = '' OR so.pos_terminal_id::text = $2)
		  AND so.status IN ('finalized', 'partially_refunded', 'refunded')
		  AND p.method = 'cash' AND p.status = 'captured'
		  AND ($3::date IS NULL OR (`+saleMoment+`)::date = $3::date)
		  AND ($4::timestamptz IS NULL OR `+saleMoment+` >= $4)
		  AND ($5::timestamptz IS NULL OR `+saleMoment+` < $5)`,
		s.BranchID, s.TerminalID, date, s.From, s.To).Scan(&f.CashSales); err != nil {
		return f, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(r.refund_amount), 0)
		FROM sales_returns r
		WHERE r.branch_id = $1 AND ($2 = '' OR r.pos_terminal_id::text = $2) AND r.refund_method = 'cash'
		  AND ($3::date IS NULL OR r.created_at::date = $3::date)
		  AND ($4::timestamptz IS NULL OR r.created_at >= $4)
		  AND ($5::timestamptz IS NULL OR r.created_at < $5)`,
		s.BranchID, s.TerminalID, date, s.From, s.To).Scan(&f.CashRefunds); err != nil {
		return f, err
	}
	rows, err := tx.Query(ctx, `
		SELECT m.kind, COALESCE(SUM(m.amount), 0)
		FROM pos_cash_movements m
		JOIN pos_shifts sh ON sh.id = m.shift_id
		WHERE m.branch_id = $1 AND ($2 = '' OR sh.pos_terminal_id::text = $2)
		  AND ($3::date IS NULL OR m.created_at::date = $3::date)
		  AND ($4::timestamptz IS NULL OR m.created_at >= $4)
		  AND ($5::timestamptz IS NULL OR m.created_at < $5)
		GROUP BY m.kind`, s.BranchID, s.TerminalID, date, s.From, s.To)
	if err != nil {
		return f, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var amount float64
		if err := rows.Scan(&kind, &amount); err != nil {
			return f, err
		}
		switch kind {
		case "float_in":
			f.FloatIn = amount
		case "drop":
			f.Drops = amount
		case "paid_in":
			f.PaidIn = amount
		case "paid_out":
			f.PaidOut = amount
		}
	}
	return f, rows.Err()
}

// CashCount is one drawer count to record as a cash reconciliation.
type CashCount struct {
	BranchID       string
	ReconDate      string // YYYY-MM-DD
	ShiftID        string // "" for a whole-branch daily count
	OpeningFloat   float64
	SystemExpected float64
	Denominations  []DenominationCount
	Reason         string
	CountedBy      string
	// Authorize is called only when the count doesn't match; it returns
	// the approving user's id, or "" when the approval is missing/wrong.
	Authorize func(ctx context.Context, tx pgx.Tx) (string, error)
}

type DenominationCount struct {
	Denomination float64 `json:"denomination"`
	Count        int     `json:"count"`
}

// CashCountResult is what was stored.
type CashCountResult struct {
	ReconciliationID string
	CountedTotal     float64
	Variance         float64
	AuthorizedBy     *string
	Lines            []denominationLine
	CreatedAt        string
}

var (
	// ErrVarianceNeedsReason / ErrVarianceNotAuthorized: a count that
	// doesn't match needs a reason and a manager's approval.
	ErrVarianceNeedsReason   = errVarianceNeedsReason
	ErrVarianceNotAuthorized = errVarianceNotAuthorized
	errNoDenominations       = errors.New("at least one denomination line is required")
)

// RecordCashCount stores a count: the reconciliation row, its denomination
// lines, and, when it doesn't match, the Cash Over/Short posting. The
// single writer of cash_reconciliations, for daily counts and shift closes.
func RecordCashCount(ctx context.Context, tx pgx.Tx, c CashCount) (CashCountResult, error) {
	var res CashCountResult
	if len(c.Denominations) == 0 {
		return res, errNoDenominations
	}
	for _, d := range c.Denominations {
		res.CountedTotal += d.Denomination * float64(d.Count)
	}
	res.CountedTotal = round2(res.CountedTotal)
	res.Variance = round2(res.CountedTotal - c.SystemExpected)

	if res.Variance != 0 {
		if c.Reason == "" {
			return res, errVarianceNeedsReason
		}
		id, err := c.Authorize(ctx, tx)
		if err != nil {
			return res, err
		}
		if id == "" {
			return res, errVarianceNotAuthorized
		}
		res.AuthorizedBy = &id
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO cash_reconciliations
			(id, merchant_id, branch_id, recon_date, opening_float, system_expected, counted_total, variance, reason, counted_by, authorized_by, pos_shift_id)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2::date, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, '')::uuid)
		RETURNING id, created_at::text`,
		c.BranchID, c.ReconDate, c.OpeningFloat, c.SystemExpected, res.CountedTotal, res.Variance, c.Reason, c.CountedBy, res.AuthorizedBy, c.ShiftID,
	).Scan(&res.ReconciliationID, &res.CreatedAt); err != nil {
		return res, err
	}

	res.Lines = make([]denominationLine, 0, len(c.Denominations))
	for _, d := range c.Denominations {
		subtotal := round2(d.Denomination * float64(d.Count))
		if _, err := tx.Exec(ctx, `
			INSERT INTO cash_reconciliation_denominations (id, cash_reconciliation_id, denomination, count, subtotal)
			VALUES (gen_random_uuid(), $1, $2, $3, $4)`,
			res.ReconciliationID, d.Denomination, d.Count, subtotal); err != nil {
			return res, err
		}
		res.Lines = append(res.Lines, denominationLine{Denomination: formatMoney(d.Denomination), Count: d.Count, Subtotal: formatMoney(subtotal)})
	}

	if res.Variance != 0 {
		var lines []JournalLine
		if res.Variance > 0 {
			lines = []JournalLine{{AccountCode: AccountCash, Debit: res.Variance}, {AccountCode: AccountCashOverShort, Credit: res.Variance}}
		} else {
			lines = []JournalLine{{AccountCode: AccountCashOverShort, Debit: -res.Variance}, {AccountCode: AccountCash, Credit: -res.Variance}}
		}
		if _, err := PostJournalEntryOnDate(ctx, tx, c.BranchID, "cash_reconciliation", res.ReconciliationID,
			"Cash drawer variance: "+c.Reason, c.CountedBy, c.ReconDate, lines); err != nil {
			return res, err
		}
	}
	return res, nil
}
