// Phase 4, sub-area 4: Reconciliation & Audit — Cash reconciliation
// (phased_roadmap.md; pos_frd_complete.md §16). See
// migrations/016_cash_reconciliation.sql's header comment for scope
// (reconciliation type #1 of 4 — #4, Inter-branch Transfer, already
// closed in Phase 2; #2 Payment Gateway and #3 Inventory are this same
// sub-area's next items, not yet built).
package accounting

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// ---------------------------------------------------------------------
// POST /cash-reconciliation — one EOD (or any-time) count per branch per
// day. Open to any authenticated user for the zero-variance case (a
// cashier counting their own drawer needs no one's permission to record
// that it matched) — only a non-zero variance needs a
// Branch Manager/Merchant Admin's PIN, checked inline exactly the way
// internal/sales/discounts.go's ApplyDiscount already authorizes a
// manual discount, not the router-level RequirePermission middleware.
// ---------------------------------------------------------------------

type denominationInput struct {
	Denomination float64 `json:"denomination"`
	Count        int     `json:"count"`
}

type createCashReconciliationRequest struct {
	BranchID      string              `json:"branch_id"`
	ReconDate     string              `json:"recon_date"` // YYYY-MM-DD
	OpeningFloat  float64             `json:"opening_float"`
	Denominations []denominationInput `json:"denominations"`
	Reason        string              `json:"reason"`         // required whenever variance != 0
	AuthorizedBy  string              `json:"authorized_by"`  // required whenever variance != 0
	AuthorizedPIN string              `json:"authorized_pin"` // required whenever variance != 0
}

type denominationLine struct {
	Denomination string `json:"denomination"`
	Count        int    `json:"count"`
	Subtotal     string `json:"subtotal"`
}

type cashReconciliationResponse struct {
	ReconciliationID string             `json:"reconciliation_id"`
	BranchID         string             `json:"branch_id"`
	ReconDate        string             `json:"recon_date"`
	OpeningFloat     string             `json:"opening_float"`
	SystemExpected   string             `json:"system_expected"`
	CountedTotal     string             `json:"counted_total"`
	Variance         string             `json:"variance"`
	Reason           string             `json:"reason"`
	Authorized       bool               `json:"authorized"`
	Denominations    []denominationLine `json:"denominations"`
	CreatedAt        string             `json:"created_at"`
}

var (
	errAlreadyReconciled     = errors.New("this branch already has a cash reconciliation for this date")
	errVarianceNeedsReason   = errors.New("a non-zero variance requires a reason")
	errVarianceNotAuthorized = errors.New("variance exceeds what the authorizer's role/PIN permits")
)

func (h *Handler) CreateCashReconciliation(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createCashReconciliationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.BranchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id is required")
		return
	}
	if _, err := time.Parse("2006-01-02", req.ReconDate); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "recon_date must be YYYY-MM-DD")
		return
	}
	if len(req.Denominations) == 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "at least one denomination line is required")
		return
	}
	for _, d := range req.Denominations {
		if d.Denomination <= 0 || d.Count < 0 {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "each denomination must be positive with a non-negative count")
			return
		}
	}

	var resp cashReconciliationResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var already bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cash_reconciliations WHERE branch_id = $1 AND recon_date = $2::date)`,
			req.BranchID, req.ReconDate).Scan(&already); err != nil {
			return err
		}
		if already {
			return errAlreadyReconciled
		}

		var cashSales float64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(p.amount), 0)
			FROM payments p
			JOIN sales_orders so ON so.id = p.sales_order_id
			WHERE so.branch_id = $1 AND so.status = 'finalized' AND so.finalized_at::date = $2::date
			  AND p.method = 'cash' AND p.status = 'captured'`,
			req.BranchID, req.ReconDate,
		).Scan(&cashSales); err != nil {
			return err
		}
		systemExpected := req.OpeningFloat + cashSales

		var countedTotal float64
		for _, d := range req.Denominations {
			countedTotal += d.Denomination * float64(d.Count)
		}
		variance := round2(countedTotal - systemExpected)

		var authorizerID *string
		if variance != 0 {
			if req.Reason == "" {
				return errVarianceNeedsReason
			}
			ok, err := cashAuthorizerPermits(ctx, tx, req.AuthorizedBy, req.AuthorizedPIN)
			if err != nil {
				return err
			}
			if !ok {
				return errVarianceNotAuthorized
			}
			authorizerID = &req.AuthorizedBy
		}

		var reconID, createdAt string
		if err := tx.QueryRow(ctx, `
			INSERT INTO cash_reconciliations
				(id, merchant_id, branch_id, recon_date, opening_float, system_expected, counted_total, variance, reason, counted_by, authorized_by)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2::date, $3, $4, $5, $6, $7, $8, $9)
			RETURNING id, created_at::text`,
			req.BranchID, req.ReconDate, req.OpeningFloat, systemExpected, countedTotal, variance, req.Reason, claims.UserID, authorizerID,
		).Scan(&reconID, &createdAt); err != nil {
			return err
		}

		lines := make([]denominationLine, 0, len(req.Denominations))
		for _, d := range req.Denominations {
			subtotal := round2(d.Denomination * float64(d.Count))
			if _, err := tx.Exec(ctx, `
				INSERT INTO cash_reconciliation_denominations (id, cash_reconciliation_id, denomination, count, subtotal)
				VALUES (gen_random_uuid(), $1, $2, $3, $4)`,
				reconID, d.Denomination, d.Count, subtotal); err != nil {
				return err
			}
			lines = append(lines, denominationLine{Denomination: formatMoney(d.Denomination), Count: d.Count, Subtotal: formatMoney(subtotal)})
		}

		if variance != 0 {
			var journalLines []JournalLine
			if variance > 0 {
				journalLines = []JournalLine{
					{AccountCode: AccountCash, Debit: variance},
					{AccountCode: AccountCashOverShort, Credit: variance},
				}
			} else {
				journalLines = []JournalLine{
					{AccountCode: AccountCashOverShort, Debit: -variance},
					{AccountCode: AccountCash, Credit: -variance},
				}
			}
			if _, err := PostJournalEntryOnDate(ctx, tx, req.BranchID, "cash_reconciliation", reconID, "Cash drawer variance: "+req.Reason, claims.UserID, req.ReconDate, journalLines); err != nil {
				return err
			}
		}

		resp = cashReconciliationResponse{
			ReconciliationID: reconID, BranchID: req.BranchID, ReconDate: req.ReconDate,
			OpeningFloat: formatMoney(req.OpeningFloat), SystemExpected: formatMoney(systemExpected),
			CountedTotal: formatMoney(countedTotal), Variance: formatMoney(variance), Reason: req.Reason,
			Authorized: authorizerID != nil, Denominations: lines, CreatedAt: createdAt,
		}
		return nil
	})

	switch {
	case errors.Is(err, errAlreadyReconciled):
		httpx.Error(w, http.StatusConflict, "ALREADY_RECONCILED", "this branch already has a cash reconciliation for this date")
	case errors.Is(err, errVarianceNeedsReason):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a non-zero variance requires a reason")
	case errors.Is(err, errVarianceNotAuthorized):
		httpx.Error(w, http.StatusForbidden, "VARIANCE_NOT_AUTHORIZED", "a Branch Manager or Merchant Admin PIN is required to close a reconciliation with a variance")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create cash reconciliation")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// cashAuthorizerPermits mirrors internal/sales/discounts.go's
// authorizerPermits exactly (role-name check against Branch Manager/
// Merchant Admin, PIN verified via bcrypt) — duplicated rather than
// imported since that function is unexported in a different package and
// this check is small enough that copying it is clearer than exporting a
// cross-package dependency for one helper.
func cashAuthorizerPermits(ctx context.Context, tx pgx.Tx, authorizedBy, authorizedPIN string) (bool, error) {
	if authorizedBy == "" || authorizedPIN == "" {
		return false, nil
	}
	var pinHash *string
	if err := tx.QueryRow(ctx, `SELECT pin_hash FROM users WHERE id = $1 AND status = 'active'`, authorizedBy).Scan(&pinHash); err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if pinHash == nil || bcrypt.CompareHashAndPassword([]byte(*pinHash), []byte(authorizedPIN)) != nil {
		return false, nil
	}
	rows, err := tx.Query(ctx, `SELECT r.name FROM roles r JOIN user_roles ur ON ur.role_id = r.id WHERE ur.user_id = $1`, authorizedBy)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		roles = append(roles, name)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return authn.HasRole(roles, "branch manager") || authn.HasRole(roles, "merchant admin"), nil
}

// ---------------------------------------------------------------------
// GET /cash-reconciliation?branch_id=&date= — a single day's record.
// ---------------------------------------------------------------------

func (h *Handler) GetCashReconciliation(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	date := r.URL.Query().Get("date")
	if branchID == "" || date == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id and date are required")
		return
	}

	var resp cashReconciliationResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var authorized bool
		if err := tx.QueryRow(ctx, `
			SELECT id, branch_id, recon_date::text, opening_float::text, system_expected::text,
			       counted_total::text, variance::text, COALESCE(reason,''), (authorized_by IS NOT NULL), created_at::text
			FROM cash_reconciliations WHERE branch_id = $1 AND recon_date = $2::date`, branchID, date,
		).Scan(&resp.ReconciliationID, &resp.BranchID, &resp.ReconDate, &resp.OpeningFloat, &resp.SystemExpected,
			&resp.CountedTotal, &resp.Variance, &resp.Reason, &authorized, &resp.CreatedAt); err != nil {
			return err
		}
		resp.Authorized = authorized

		rows, err := tx.Query(ctx, `
			SELECT denomination::text, count, subtotal::text FROM cash_reconciliation_denominations
			WHERE cash_reconciliation_id = $1 ORDER BY denomination DESC`, resp.ReconciliationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		resp.Denominations = []denominationLine{}
		for rows.Next() {
			var l denominationLine
			if err := rows.Scan(&l.Denomination, &l.Count, &l.Subtotal); err != nil {
				return err
			}
			resp.Denominations = append(resp.Denominations, l)
		}
		return rows.Err()
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no cash reconciliation for this branch/date")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load cash reconciliation")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------
// GET /cash-reconciliation/history?branch_id=&start=&end= — the FRD's
// "Settlement Reports: Opening balance, All transactions, Closing
// balance" for cash, one row per reconciled day.
// ---------------------------------------------------------------------

type cashReconciliationSummary struct {
	ReconDate      string `json:"recon_date"`
	OpeningFloat   string `json:"opening_float"`
	SystemExpected string `json:"system_expected"`
	CountedTotal   string `json:"counted_total"`
	Variance       string `json:"variance"`
	Authorized     bool   `json:"authorized"`
}

func (h *Handler) CashReconciliationHistory(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")
	if branchID == "" || start == "" || end == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id, start, and end are required")
		return
	}

	history := []cashReconciliationSummary{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT recon_date::text, opening_float::text, system_expected::text, counted_total::text, variance::text, (authorized_by IS NOT NULL)
			FROM cash_reconciliations
			WHERE branch_id = $1 AND recon_date BETWEEN $2::date AND $3::date
			ORDER BY recon_date`, branchID, start, end)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s cashReconciliationSummary
			if err := rows.Scan(&s.ReconDate, &s.OpeningFloat, &s.SystemExpected, &s.CountedTotal, &s.Variance, &s.Authorized); err != nil {
				return err
			}
			history = append(history, s)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load cash reconciliation history")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"branch_id": branchID, "start": start, "end": end, "history": history})
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
