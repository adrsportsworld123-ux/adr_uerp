package approvals

// One place to see approvals (§3.77; migrations/073):
//
//   - GET /approvals/waiting: what is waiting for someone right now —
//     branch transfers to approve, journal vouchers submitted for approval,
//     oversold offline sales to review, and employees' leave, permission
//     and attendance regularization requests (§3.78). Each row says whether the caller
//     may act on it (can_act); acting goes through the item's own endpoint
//     and permission, unchanged.
//   - GET /approvals/given?from=&to=&kind=&branch_id=: what was approved,
//     mostly by a manager's PIN at the till: manual discounts, returns,
//     open prices, cash and stock-count variances, cash paid in/out,
//     payment corrections, stock adjustments, approved transfers and
//     vouchers, and employee loans — who asked, who approved, when, how
//     much and why.
//
// Both need approvals.view and read in the caller's branch scope (RLS).

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/locale"
)

type waitingItem struct {
	Kind        string  `json:"kind"` // transfer | voucher | stock_review | leave | permission | regularization
	ID          string  `json:"id"`
	Reference   string  `json:"reference"`
	Summary     string  `json:"summary"`
	Detail      string  `json:"detail"`
	BranchID    *string `json:"branch_id"`
	Branch      *string `json:"branch"`
	RequestedBy *string `json:"requested_by"`
	RequestedAt string  `json:"requested_at"`
	Amount      *string `json:"amount"` // null for an HR request
	// CanAct: the caller holds the permission the item's own approve /
	// reject (or resolve) needs; Why says what stops them when not.
	CanAct bool   `json:"can_act"`
	Why    string `json:"why,omitempty"`
	selfID string
}

const waitingSQL = `
	SELECT 'transfer', bt.id::text, bt.transfer_number, fb.name || ' → ' || tb.name,
	       (SELECT count(*) FROM branch_transfer_lines l WHERE l.branch_transfer_id = bt.id)::text || ' item(s)' ||
	         COALESCE(' · ' || NULLIF(bt.notes, ''), ''),
	       bt.from_branch_id::text, fb.name, u.name, bt.created_at,
	       (SELECT COALESCE(sum(l.requested_quantity * v.cost_price), 0) FROM branch_transfer_lines l
	          JOIN product_variants v ON v.id = l.variant_id WHERE l.branch_transfer_id = bt.id)::numeric(14,2)::text,
	       COALESCE(bt.requested_by::text, '')
	FROM branch_transfers bt
	JOIN branches fb ON fb.id = bt.from_branch_id
	JOIN branches tb ON tb.id = bt.to_branch_id
	LEFT JOIN users u ON u.id = bt.requested_by
	WHERE bt.status = 'pending_approval'
	UNION ALL
	SELECT 'voucher', jv.id::text, jv.voucher_number, jv.description, 'Dated ' || to_char(jv.entry_date, 'DD Mon YYYY'),
	       jv.branch_id::text, b.name, u.name, COALESCE(jv.submitted_at, jv.created_at),
	       (SELECT COALESCE(sum(l.debit), 0) FROM journal_voucher_lines l WHERE l.voucher_id = jv.id)::numeric(14,2)::text,
	       jv.created_by::text
	FROM journal_vouchers jv
	LEFT JOIN branches b ON b.id = jv.branch_id
	LEFT JOIN users u ON u.id = jv.submitted_by
	WHERE jv.status = 'submitted'
	UNION ALL
	SELECT 'stock_review', s.id::text, so.order_number, p.name || ' (' || v.sku || ')',
	       'Sold ' || trim_scale(s.quantity)::text || ' offline; stock now ' || trim_scale(s.on_hand_after)::text,
	       s.branch_id::text, b.name, cu.name, s.created_at,
	       round(s.quantity * v.cost_price, 2)::text, ''
	FROM stock_review_items s
	JOIN sales_orders so ON so.id = s.sales_order_id
	JOIN product_variants v ON v.id = s.variant_id
	JOIN products p ON p.id = v.product_id
	JOIN branches b ON b.id = s.branch_id
	LEFT JOIN users cu ON cu.id = so.cashier_id
	WHERE s.status = 'open'
	UNION ALL
	SELECT 'leave', r.id::text, r.request_number, u.name || ' — ' || t.name,
	       to_char(r.from_date, 'DD Mon YYYY') || CASE WHEN r.to_date > r.from_date THEN ' to ' || to_char(r.to_date, 'DD Mon YYYY') ELSE '' END ||
	         ' (' || CASE WHEN r.half_day THEN 'half day' ELSE trim_scale(r.days)::text || ' day(s)' END || ') · ' || r.reason,
	       r.branch_id::text, b.name, rb.name, r.requested_at, NULL, r.user_id::text
	FROM leave_requests r JOIN users u ON u.id = r.user_id JOIN leave_types t ON t.id = r.leave_type_id
	LEFT JOIN branches b ON b.id = r.branch_id LEFT JOIN users rb ON rb.id = r.requested_by
	WHERE r.status = 'pending'
	UNION ALL
	SELECT 'permission', r.id::text, r.request_number, u.name || ' — permission',
	       to_char(r.work_date, 'DD Mon YYYY') || ', ' || to_char(r.from_time, 'HH24:MI') || '–' || to_char(r.to_time, 'HH24:MI') ||
	         ' (' || r.minutes || ' min) · ' || r.reason,
	       r.branch_id::text, b.name, rb.name, r.requested_at, NULL, r.user_id::text
	FROM permission_requests r JOIN users u ON u.id = r.user_id
	LEFT JOIN branches b ON b.id = r.branch_id LEFT JOIN users rb ON rb.id = r.requested_by
	WHERE r.status = 'pending'
	UNION ALL
	SELECT 'regularization', r.id::text, r.request_number,
	       u.name || CASE WHEN r.session_id IS NULL THEN ' — add a missed check-in' ELSE ' — correct a check-in' END,
	       to_char(r.work_date, 'DD Mon YYYY') ||
	         COALESCE(', in ' || to_char(r.clock_in AT TIME ZONE app_merchant_tz(), 'HH24:MI'), '') ||
	         COALESCE(', out ' || to_char(r.clock_out AT TIME ZONE app_merchant_tz(), 'HH24:MI'), '') || ' · ' || r.reason,
	       r.branch_id::text, b.name, rb.name, r.requested_at, NULL, r.user_id::text
	FROM attendance_regularizations r JOIN users u ON u.id = r.user_id
	LEFT JOIN branches b ON b.id = r.branch_id LEFT JOIN users rb ON rb.id = r.requested_by
	WHERE r.status = 'pending'
	ORDER BY 9`

// Waiting: GET /approvals/waiting (approvals.view).
func (h *Handler) Waiting(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	items := []waitingItem{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		codes, err := authn.FetchPermissions(ctx, tx, claims.UserID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, waitingSQL)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var it waitingItem
			var at time.Time
			if err := rows.Scan(&it.Kind, &it.ID, &it.Reference, &it.Summary, &it.Detail, &it.BranchID, &it.Branch,
				&it.RequestedBy, &at, &it.Amount, &it.selfID); err != nil {
				return err
			}
			it.RequestedAt = at.UTC().Format(time.RFC3339)
			need := map[string]string{"transfer": "branch_transfer.approve", "voucher": "accounting.jv_approve", "stock_review": "inventory.adjust",
				"leave": "hr.manage", "permission": "hr.manage", "regularization": "hr.manage"}[it.Kind]
			it.CanAct = authn.HasPermission(codes, need)
			switch {
			case !it.CanAct:
				it.Why = "needs the " + need + " permission"
			case it.Kind == "voucher" && it.selfID == claims.UserID:
				it.CanAct, it.Why = false, "you made this voucher; someone else approves it"
			case (it.Kind == "leave" || it.Kind == "permission" || it.Kind == "regularization") && it.selfID == claims.UserID:
				it.CanAct, it.Why = false, "this is your own request; someone else approves it"
			}
			items = append(items, it)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the approvals waiting")
		return
	}
	counts := map[string]int{"transfer": 0, "voucher": 0, "stock_review": 0, "leave": 0, "permission": 0, "regularization": 0}
	for _, it := range items {
		counts[it.Kind]++
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items, "counts": counts, "total": len(items)})
}

type givenItem struct {
	Kind       string  `json:"kind"`
	At         string  `json:"at"`
	Reference  string  `json:"reference"`
	BranchID   *string `json:"branch_id"`
	Branch     *string `json:"branch"`
	DoneBy     *string `json:"done_by"`
	ApprovedBy *string `json:"approved_by"`
	Amount     *string `json:"amount"`
	Reason     *string `json:"reason"`
}

// GivenKinds is every kind GET /approvals/given lists, in the order the
// screen offers them.
var GivenKinds = []string{"manual_discount", "return", "open_price", "cash_variance", "stock_count", "stock_adjustment",
	"cash_movement", "payment_correction", "transfer", "voucher", "loan", "leave", "permission", "regularization"}

const givenSQL = `
	SELECT * FROM (
	  SELECT 'manual_discount' AS kind, d.created_at AS at, so.order_number AS ref, so.branch_id, pu.name AS done_by, au.name AS approved_by,
	         d.discount_amount AS amount, d.reason
	    FROM sales_order_discounts d JOIN sales_orders so ON so.id = d.sales_order_id
	    LEFT JOIN users pu ON pu.id = d.applied_by JOIN users au ON au.id = d.authorized_by
	    WHERE d.type = 'manual' AND so.status IN ('finalized', 'partially_refunded', 'refunded', 'voided')
	  UNION ALL
	  SELECT 'return', r.created_at, so.order_number, r.branch_id, pu.name, au.name, r.refund_amount, r.reason
	    FROM sales_returns r JOIN sales_orders so ON so.id = r.sales_order_id
	    LEFT JOIN users pu ON pu.id = r.processed_by JOIN users au ON au.id = r.approved_by
	  UNION ALL
	  SELECT 'open_price', COALESCE(so.finalized_at, so.created_at), so.order_number, so.branch_id, cu.name, au.name,
	         round(l.unit_price * l.quantity, 2), 'Open price: ' || v.sku
	    FROM sales_order_lines l JOIN sales_orders so ON so.id = l.sales_order_id JOIN product_variants v ON v.id = l.variant_id
	    LEFT JOIN users cu ON cu.id = so.cashier_id JOIN users au ON au.id = l.price_approved_by
	    WHERE so.status IN ('finalized', 'partially_refunded', 'refunded', 'voided')
	  UNION ALL
	  SELECT 'cash_variance', c.created_at, to_char(c.recon_date, 'DD Mon YYYY'), c.branch_id, cb.name, au.name, c.variance, c.reason
	    FROM cash_reconciliations c LEFT JOIN users cb ON cb.id = c.counted_by JOIN users au ON au.id = c.authorized_by
	  UNION ALL
	  SELECT 'stock_count', i.created_at, initcap(i.recon_type) || ' count ' || to_char(i.recon_date, 'DD Mon YYYY'), i.branch_id, cb.name, au.name,
	         i.variance_value, i.reason
	    FROM inventory_reconciliations i LEFT JOIN users cb ON cb.id = i.counted_by JOIN users au ON au.id = i.authorized_by
	  UNION ALL
	  SELECT 'stock_adjustment', m.created_at, v.sku, m.branch_id, pu.name, au.name, round(m.quantity_delta * v.cost_price, 2), m.reason
	    FROM stock_movements m JOIN product_variants v ON v.id = m.variant_id
	    LEFT JOIN users pu ON pu.id = m.performed_by JOIN users au ON au.id = m.approved_by
	    WHERE m.movement_type = 'adjustment'
	  UNION ALL
	  SELECT 'cash_movement', cm.created_at, replace(initcap(cm.kind), '_', ' '), cm.branch_id, pu.name, au.name, cm.amount, cm.reason
	    FROM pos_cash_movements cm LEFT JOIN users pu ON pu.id = cm.performed_by JOIN users au ON au.id = cm.approved_by
	  UNION ALL
	  SELECT 'payment_correction', pc.created_at, so.order_number, so.branch_id, pu.name, au.name, pc.amount,
	         pc.from_method || ' → ' || pc.to_method || ': ' || pc.reason
	    FROM payment_corrections pc JOIN sales_orders so ON so.id = pc.sales_order_id
	    LEFT JOIN users pu ON pu.id = pc.performed_by JOIN users au ON au.id = pc.approved_by
	  UNION ALL
	  SELECT 'transfer', bt.approved_at, bt.transfer_number, bt.from_branch_id, ru.name, au.name,
	         (SELECT COALESCE(sum(l.requested_quantity * v.cost_price), 0) FROM branch_transfer_lines l
	            JOIN product_variants v ON v.id = l.variant_id WHERE l.branch_transfer_id = bt.id)::numeric(14,2), bt.notes
	    FROM branch_transfers bt LEFT JOIN users ru ON ru.id = bt.requested_by JOIN users au ON au.id = bt.approved_by
	    WHERE bt.approved_at IS NOT NULL
	  UNION ALL
	  SELECT 'voucher', jv.approved_at, jv.voucher_number, jv.branch_id, cu.name, au.name,
	         (SELECT COALESCE(sum(l.debit), 0) FROM journal_voucher_lines l WHERE l.voucher_id = jv.id), jv.description
	    FROM journal_vouchers jv LEFT JOIN users cu ON cu.id = jv.created_by JOIN users au ON au.id = jv.approved_by
	    WHERE jv.approved_at IS NOT NULL
	  UNION ALL
	  SELECT 'loan', el.created_at, el.loan_number, el.branch_id, eu.name, au.name, el.principal, el.purpose
	    FROM employee_loans el LEFT JOIN users eu ON eu.id = el.user_id JOIN users au ON au.id = el.approved_by
	  UNION ALL
	  SELECT 'leave', lr.decided_at, lr.request_number, lr.branch_id, eu.name, au.name, NULL::numeric,
	         t.name || ', ' || to_char(lr.from_date, 'DD Mon') || CASE WHEN lr.to_date > lr.from_date THEN '–' || to_char(lr.to_date, 'DD Mon') ELSE '' END ||
	         ' (' || trim_scale(lr.days)::text || ' day(s)' || CASE WHEN lr.unpaid_days > 0 THEN ', ' || trim_scale(lr.unpaid_days)::text || ' unpaid' ELSE '' END ||
	         '): ' || lr.reason
	    FROM leave_requests lr JOIN leave_types t ON t.id = lr.leave_type_id
	    LEFT JOIN users eu ON eu.id = lr.user_id JOIN users au ON au.id = lr.decided_by
	    WHERE lr.status IN ('approved', 'cancelled') AND lr.paid_days IS NOT NULL
	  UNION ALL
	  SELECT 'permission', pr.decided_at, pr.request_number, pr.branch_id, eu.name, au.name, NULL::numeric,
	         to_char(pr.work_date, 'DD Mon') || ' ' || to_char(pr.from_time, 'HH24:MI') || '–' || to_char(pr.to_time, 'HH24:MI') ||
	         CASE WHEN pr.unpaid_minutes > 0 THEN ' (' || pr.unpaid_minutes || ' min unpaid)' ELSE '' END || ': ' || pr.reason
	    FROM permission_requests pr LEFT JOIN users eu ON eu.id = pr.user_id JOIN users au ON au.id = pr.decided_by
	    WHERE pr.status IN ('approved', 'cancelled') AND pr.paid_minutes IS NOT NULL
	  UNION ALL
	  SELECT 'regularization', ar.decided_at, ar.request_number, ar.branch_id, eu.name, au.name, NULL::numeric,
	         to_char(ar.work_date, 'DD Mon') || ': ' || ar.reason
	    FROM attendance_regularizations ar LEFT JOIN users eu ON eu.id = ar.user_id JOIN users au ON au.id = ar.decided_by
	    WHERE ar.status = 'approved'
	) g
	WHERE (g.at AT TIME ZONE app_merchant_tz())::date BETWEEN $1::date AND $2::date
	  AND ($3 = '' OR g.kind = $3)
	  AND ($4 = '' OR g.branch_id::text = $4)
	ORDER BY g.at DESC
	LIMIT 1000`

// Given: GET /approvals/given?from=&to=&kind=&branch_id= (approvals.view).
// Dates are the merchant's calendar; the default is the last 30 days.
func (h *Handler) Given(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind != "" && !contains(GivenKinds, kind) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "unknown kind")
		return
	}
	today := time.Now().In(locale.MerchantLocation(r.Context(), h.DB, claims.TenantID)).Format("2006-01-02")
	from, to := q.Get("from"), q.Get("to")
	if to == "" {
		to = today
	}
	if from == "" {
		t, _ := time.Parse("2006-01-02", to)
		from = t.AddDate(0, 0, -29).Format("2006-01-02")
	}
	f, err1 := time.Parse("2006-01-02", from)
	t, err2 := time.Parse("2006-01-02", to)
	if err1 != nil || err2 != nil || t.Before(f) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "from and to are YYYY-MM-DD, from not after to")
		return
	}
	if t.Sub(f) > 366*24*time.Hour {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "choose at most a year")
		return
	}
	items := []givenItem{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT g.kind, g.at, g.ref, g.branch_id::text, b.name, g.done_by, g.approved_by, g.amount::text, g.reason
			FROM (`+givenSQL+`) g LEFT JOIN branches b ON b.id = g.branch_id
			ORDER BY g.at DESC`, from, to, kind, q.Get("branch_id"))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var it givenItem
			var at time.Time
			if err := rows.Scan(&it.Kind, &at, &it.Reference, &it.BranchID, &it.Branch, &it.DoneBy, &it.ApprovedBy, &it.Amount, &it.Reason); err != nil {
				return err
			}
			it.At = at.UTC().Format(time.RFC3339)
			items = append(items, it)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the approvals given")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items, "from": from, "to": to, "kinds": GivenKinds, "truncated": len(items) >= 1000})
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
