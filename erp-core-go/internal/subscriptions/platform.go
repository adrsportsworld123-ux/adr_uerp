package subscriptions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// The SaaS console's backend (phased_roadmap.md Phase 16: "SaaS (platform
// staff) console for merchants, plans and billing ... behind a separate
// platform-staff role"). Platform staff sign in with their own accounts
// (platform_staff), and their tokens carry no tenant: every tenant route
// refuses them (Middleware), and every /platform route refuses a tenant
// token (RequirePlatform). Reading a merchant goes through that merchant's
// own WithTenant transaction, so RLS still applies to every tenant table.

const PlatformRole = "platform_staff"

const (
	platformMaxAttempts = 5
	platformLockout     = 15 * time.Minute
	platformSessionTTL  = time.Hour
)

type PlatformHandler struct {
	DB     *db.DB
	Issuer *authn.TokenIssuer
}

// Login: POST /platform/auth/login {email, password}.
func (h *PlatformHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" || req.Password == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "email and password are required")
		return
	}
	ctx := r.Context()
	var id, name, hash, status string
	var attempts int
	var lockedUntil *time.Time
	err := h.DB.Pool.QueryRow(ctx, `SELECT id, name, password_hash, status, failed_login_attempts, locked_until
		FROM platform_staff WHERE lower(email) = lower($1)`, strings.TrimSpace(req.Email)).
		Scan(&id, &name, &hash, &status, &attempts, &lockedUntil)
	invalid := func() {
		httpx.Error(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		invalid()
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not sign in")
		return
	}
	if lockedUntil != nil && time.Now().Before(*lockedUntil) {
		httpx.Error(w, http.StatusLocked, "ACCOUNT_LOCKED", "too many failed attempts; try again later")
		return
	}
	if status != "active" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		if status == "active" {
			_, _ = h.DB.Pool.Exec(ctx, `UPDATE platform_staff SET failed_login_attempts = failed_login_attempts + 1,
				locked_until = CASE WHEN failed_login_attempts + 1 >= $2 THEN now() + $3::interval ELSE locked_until END WHERE id = $1`,
				id, platformMaxAttempts, platformLockout.String())
		}
		invalid()
		return
	}
	_, _ = h.DB.Pool.Exec(ctx, `UPDATE platform_staff SET failed_login_attempts = 0, locked_until = NULL, last_login_at = now() WHERE id = $1`, id)
	token, exp, err := h.Issuer.IssueAccessToken("", id, "", []string{PlatformRole}, platformSessionTTL)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not sign in")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"access_token": token, "expires_at": exp, "staff": map[string]any{"id": id, "name": name}})
}

// RequirePlatform: only a platform-staff token passes.
func RequirePlatform(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := authn.FromContext(r.Context())
		if !ok || c.TenantID != "" || !authn.HasRole(c.Roles, PlatformRole) {
			httpx.Error(w, http.StatusForbidden, "FORBIDDEN", "platform staff only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *PlatformHandler) logPlatform(ctx context.Context, staffID, action, targetType, targetID string, before, after any, reason string) error {
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	_, err := h.DB.Pool.Exec(ctx, `INSERT INTO platform_audit_log (staff_id, action, target_type, target_id, before_value, after_value, reason)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))`, staffID, action, targetType, targetID, b, a, reason)
	return err
}

type merchantRow struct {
	MerchantID string    `json:"merchant_id"`
	Code       string    `json:"code"`
	LegalName  string    `json:"legal_name"`
	TradeName  *string   `json:"trade_name"`
	Status     string    `json:"merchant_status"`
	CreatedAt  time.Time `json:"created_at"`
}

func (h *PlatformHandler) merchants(ctx context.Context, id string) ([]merchantRow, error) {
	rows, err := h.DB.Pool.Query(ctx, `SELECT id, code, legal_name, trade_name, status, created_at FROM merchants
		WHERE $1 = '' OR id::text = $1 ORDER BY created_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []merchantRow{}
	for rows.Next() {
		var m merchantRow
		if err := rows.Scan(&m.MerchantID, &m.Code, &m.LegalName, &m.TradeName, &m.Status, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListMerchants: GET /platform/merchants — every merchant with its plan,
// status, usage and open invoices. One WithTenant per merchant, the same
// loop the sweepers use.
func (h *PlatformHandler) ListMerchants(w http.ResponseWriter, r *http.Request) {
	ms, err := h.merchants(r.Context(), "")
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list merchants")
		return
	}
	out := make([]map[string]any, 0, len(ms))
	for _, m := range ms {
		var row map[string]any
		err := h.DB.WithTenant(r.Context(), m.MerchantID, func(ctx context.Context, tx pgx.Tx) error {
			e, err := Load(ctx, tx)
			if err != nil {
				return err
			}
			usage, err := Usage(ctx, tx, e.Plan)
			if err != nil {
				return err
			}
			var due int
			var dueAmount string
			if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(sum(amount), 0)::text FROM billing_invoices WHERE status = 'due'`).
				Scan(&due, &dueAmount); err != nil {
				return err
			}
			var lastSale *time.Time
			if err := db.Unrestricted(ctx, tx, func() error {
				return tx.QueryRow(ctx, `SELECT max(created_at) FROM sales_orders WHERE status = 'finalized'`).Scan(&lastSale)
			}); err != nil {
				return err
			}
			row = map[string]any{"merchant": m, "plan": map[string]any{"code": e.Plan.Code, "name": e.Plan.Name}, "status": e.Status,
				"read_only": e.ReadOnly, "current_period_end": e.PeriodEnd, "trial_ends_at": e.TrialEndsAt, "grace_ends_at": e.GraceEndsAt,
				"usage": usage, "invoices_due": due, "amount_due": dueAmount, "last_sale_at": lastSale, "no_subscription_row": e.Missing}
			return nil
		})
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load merchant "+m.Code)
			return
		}
		out = append(out, row)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"merchants": out})
}

// GetMerchant: GET /platform/merchants/{id} — the full Summary.
func (h *PlatformHandler) GetMerchant(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ms, err := h.merchants(r.Context(), id)
	if err != nil || len(ms) == 0 {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "merchant not found")
		return
	}
	var resp map[string]any
	err = h.DB.WithTenant(r.Context(), id, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp, err = Summary(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the merchant")
		return
	}
	resp["merchant"] = ms[0]
	httpx.JSON(w, http.StatusOK, resp)
}

// UpdateSubscription: PATCH /platform/merchants/{id}/subscription
// {plan_code?, status?, trial_ends_at?, current_period_end?, grace_ends_at?,
// clear_period_end?, notes?, reason} — set any part of the lifecycle
// directly (a sales-led deal, an extension, a cancellation). Reason
// required; audited in the platform log and the merchant's own log.
func (h *PlatformHandler) UpdateSubscription(w http.ResponseWriter, r *http.Request) {
	staff, _ := authn.FromContext(r.Context())
	id := chi.URLParam(r, "id")
	var req struct {
		PlanCode         *string    `json:"plan_code"`
		Status           *string    `json:"status"`
		TrialEndsAt      *time.Time `json:"trial_ends_at"`
		CurrentPeriodEnd *time.Time `json:"current_period_end"`
		ClearPeriodEnd   bool       `json:"clear_period_end"`
		GraceEndsAt      *time.Time `json:"grace_ends_at"`
		Notes            *string    `json:"notes"`
		Reason           string     `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason is required")
		return
	}
	if req.Status != nil {
		switch *req.Status {
		case StatusTrial, StatusActive, StatusGrace, StatusExpired, StatusCancelled:
		default:
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be trial, active, grace, expired or cancelled")
			return
		}
		if *req.Status == StatusTrial && req.TrialEndsAt == nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a trial needs trial_ends_at")
			return
		}
	}
	if req.PlanCode != nil {
		if _, err := LoadPlan(r.Context(), h.DB.Pool, *req.PlanCode); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "unknown plan_code")
			return
		}
	}
	if ms, err := h.merchants(r.Context(), id); err != nil || len(ms) == 0 {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "merchant not found")
		return
	}
	var before, after Stored
	err := h.DB.WithTenant(r.Context(), id, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if before, _, err = loadStored(ctx, tx); err != nil {
			return err
		}
		after = before
		if req.PlanCode != nil {
			after.PlanCode = *req.PlanCode
		}
		if req.Status != nil {
			after.Status = *req.Status
			// Moving out of a lapsed state starts clean.
			if *req.Status == StatusActive || *req.Status == StatusTrial {
				after.GraceEndsAt, after.ReadOnlySince = nil, nil
			}
			if *req.Status == StatusExpired || *req.Status == StatusCancelled {
				now := time.Now()
				after.ReadOnlySince = &now
			}
		}
		if req.TrialEndsAt != nil {
			after.TrialEndsAt = req.TrialEndsAt
			after.TrialUsed = true
		}
		if req.CurrentPeriodEnd != nil {
			after.CurrentPeriodEnd = req.CurrentPeriodEnd
		}
		if req.ClearPeriodEnd {
			after.CurrentPeriodEnd = nil
		}
		// New dates restart the lapse clock: a grace end or read-only time
		// left over from an earlier lapse no longer applies.
		if (req.TrialEndsAt != nil || req.CurrentPeriodEnd != nil || req.ClearPeriodEnd) && req.Status == nil {
			after.GraceEndsAt, after.ReadOnlySince = nil, nil
		}
		if req.GraceEndsAt != nil {
			after.GraceEndsAt = req.GraceEndsAt
		}
		if req.Notes != nil {
			after.Notes = req.Notes
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO subscriptions (merchant_id, plan_code, status, trial_ends_at, trial_used, current_period_end, grace_ends_at,
				read_only_since, notes, updated_by, updated_at)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, $8, $9, now())
			ON CONFLICT (merchant_id) DO UPDATE SET plan_code = $1, status = $2, trial_ends_at = $3, trial_used = $4,
				current_period_end = $5, grace_ends_at = $6, read_only_since = $7, notes = $8, updated_by = $9, updated_at = now()`,
			after.PlanCode, after.Status, after.TrialEndsAt, after.TrialUsed, after.CurrentPeriodEnd, after.GraceEndsAt,
			after.ReadOnlySince, after.Notes, staff.UserID); err != nil {
			return err
		}
		if err := syncTier(ctx, tx, after.PlanCode, nil); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "subscriptions", EntityID: id, Action: "update",
			PerformedBy: staff.UserID, Before: before, After: after, Reason: "platform staff: " + req.Reason})
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update the subscription")
		return
	}
	_ = h.logPlatform(r.Context(), staff.UserID, "subscription.update", "merchant", id, before, after, req.Reason)
	Invalidate(id)
	h.GetMerchant(w, r)
}

// InvoiceAction: POST /platform/merchants/{id}/invoices/{invoice_id}/{action}
// where action is mark-paid {reference, reason} (an offline payment, e.g. a
// bank transfer) or void {reason}.
func (h *PlatformHandler) InvoiceAction(w http.ResponseWriter, r *http.Request) {
	staff, _ := authn.FromContext(r.Context())
	id, invID, action := chi.URLParam(r, "id"), chi.URLParam(r, "invoice_id"), chi.URLParam(r, "action")
	var req struct {
		Reference string `json:"reference"`
		Reason    string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Reason) == "" || (action == "mark-paid" && strings.TrimSpace(req.Reference) == "") {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason (and, to mark paid, the payment reference) is required")
		return
	}
	var out Invoice
	err := h.DB.WithTenant(r.Context(), id, func(ctx context.Context, tx pgx.Tx) error {
		inv, err := GetInvoice(ctx, tx, invID)
		if err != nil {
			return err
		}
		switch action {
		case "mark-paid":
			e, err := Load(ctx, tx)
			if err != nil {
				return err
			}
			if out, err = MarkPaid(ctx, tx, e, inv, "manual", req.Reference, ""); err != nil {
				return err
			}
		case "void":
			if inv.Status != "due" {
				return ErrInvoiceNotDue
			}
			if out, err = scanInvoice(tx.QueryRow(ctx, `UPDATE billing_invoices SET status = 'void' WHERE id = $1 RETURNING `+invoiceColumns, inv.ID)); err != nil {
				return err
			}
		default:
			return errUnknownAction
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "billing_invoices", EntityID: inv.ID, Action: "update", PerformedBy: staff.UserID,
			Before: map[string]any{"status": inv.Status}, After: map[string]any{"status": out.Status, "reference": req.Reference},
			Reason: "platform staff: " + req.Reason})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "invoice not found")
		return
	case errors.Is(err, errUnknownAction):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "unknown action")
		return
	case errors.Is(err, ErrInvoiceNotDue):
		httpx.Error(w, http.StatusConflict, "INVOICE_NOT_DUE", "only a due invoice can be voided or paid")
		return
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update the invoice")
		return
	}
	_ = h.logPlatform(r.Context(), staff.UserID, "invoice."+action, "billing_invoice", invID, nil, out, req.Reason)
	Invalidate(id)
	httpx.JSON(w, http.StatusOK, out)
}

var errUnknownAction = errors.New("unknown action")

// ListPlans: GET /platform/plans — every plan, offered or not.
func (h *PlatformHandler) ListPlans(w http.ResponseWriter, r *http.Request) {
	ps, err := ListPlans(r.Context(), h.DB.Pool, false)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load plans")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"plans": ps, "features": AllFeatures, "limit_kinds": AllLimits})
}

// UpdatePlan: PATCH /platform/plans/{code} — change a plan without a
// deploy. Applies to every merchant on it within the cache window.
func (h *PlatformHandler) UpdatePlan(w http.ResponseWriter, r *http.Request) {
	staff, _ := authn.FromContext(r.Context())
	code := chi.URLParam(r, "code")
	before, err := LoadPlan(r.Context(), h.DB.Pool, code)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no such plan")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the plan")
		return
	}
	var req struct {
		Name             *string           `json:"name"`
		PricePerPOSMonth *float64          `json:"price_per_pos_month"`
		Limits           map[string]*int64 `json:"limits"`
		Features         []string          `json:"features"`
		RetentionDays    *int              `json:"retention_days"`
		Lifetime         bool              `json:"lifetime_retention"`
		TrialDays        *int              `json:"trial_days"`
		GraceDays        *int              `json:"grace_days"`
		Active           *bool             `json:"active"`
		Reason           string            `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	after := before
	bad := func(msg string) { httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg) }
	if strings.TrimSpace(req.Reason) == "" {
		bad("a reason is required")
		return
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			bad("name can't be empty")
			return
		}
		after.Name = strings.TrimSpace(*req.Name)
	}
	price := before.PricePerPOSMonth
	if req.PricePerPOSMonth != nil {
		if *req.PricePerPOSMonth < 0 {
			bad("price can't be negative")
			return
		}
		price = jsonNumber(*req.PricePerPOSMonth)
	}
	if req.Limits != nil {
		for k, v := range req.Limits {
			if !contains(AllLimits, k) || (v != nil && *v < 0) {
				bad("limits: unknown kind or negative value: " + k)
				return
			}
		}
		after.Limits = req.Limits
	}
	if req.Features != nil {
		for _, f := range req.Features {
			if !contains(AllFeatures, f) {
				bad("unknown feature: " + f)
				return
			}
		}
		after.Features = req.Features
	}
	if req.Lifetime {
		after.RetentionDays = nil
	} else if req.RetentionDays != nil {
		if *req.RetentionDays < 1 {
			bad("retention_days must be at least 1 (or lifetime_retention)")
			return
		}
		after.RetentionDays = req.RetentionDays
	}
	for _, p := range []*int{req.TrialDays, req.GraceDays} {
		if p != nil && *p < 0 {
			bad("trial and grace days can't be negative")
			return
		}
	}
	if req.TrialDays != nil {
		after.TrialDays = *req.TrialDays
	}
	if req.GraceDays != nil {
		after.GraceDays = *req.GraceDays
	}
	if req.Active != nil {
		after.Active = *req.Active
	}
	limits, _ := json.Marshal(after.Limits)
	saved, err := scanPlan(h.DB.Pool.QueryRow(r.Context(), `
		UPDATE plans SET name = $2, price_per_pos_month = $3::numeric, limits = $4, features = $5, retention_days = $6, trial_days = $7,
			grace_days = $8, active = $9, updated_at = now() WHERE code = $1 RETURNING `+planColumns,
		code, after.Name, price, limits, after.Features, after.RetentionDays, after.TrialDays, after.GraceDays, after.Active))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the plan")
		return
	}
	_ = h.logPlatform(r.Context(), staff.UserID, "plan.update", "plan", code, before, saved, req.Reason)
	Invalidate("")
	httpx.JSON(w, http.StatusOK, saved)
}

// AuditLog: GET /platform/audit-log — the latest platform actions.
func (h *PlatformHandler) AuditLog(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Pool.Query(r.Context(), `
		SELECT l.id, s.name, l.action, l.target_type, l.target_id, l.before_value, l.after_value, l.reason, l.created_at
		FROM platform_audit_log l JOIN platform_staff s ON s.id = l.staff_id ORDER BY l.id DESC LIMIT 200`)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the log")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, action, tt, tid string
		var b, a []byte
		var reason *string
		var at time.Time
		if err := rows.Scan(&id, &name, &action, &tt, &tid, &b, &a, &reason, &at); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the log")
			return
		}
		out = append(out, map[string]any{"id": id, "staff": name, "action": action, "target_type": tt, "target_id": tid,
			"before": json.RawMessage(nonNull(b)), "after": json.RawMessage(nonNull(a)), "reason": reason, "created_at": at})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": out})
}

func nonNull(b []byte) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func jsonNumber(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
