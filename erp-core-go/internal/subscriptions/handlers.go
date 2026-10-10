package subscriptions

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// Handler: the merchant's own subscription endpoints.
type Handler struct {
	DB       *db.DB
	Provider Provider
}

// Entitlements: GET /me/entitlements — any signed-in user. What the
// clients need to show lock icons, limits and the expiry banner.
func (h *Handler) Entitlements(w http.ResponseWriter, r *http.Request) {
	e, ok := FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"plan": map[string]any{"code": e.Plan.Code, "name": e.Plan.Name}, "status": e.Status, "read_only": e.ReadOnly,
		"read_only_since": e.ReadOnlySince, "trial_ends_at": e.TrialEndsAt, "current_period_end": e.PeriodEnd, "grace_ends_at": e.GraceEndsAt,
		"features": e.Plan.Features, "limits": e.Plan.Limits, "retention_days": e.Plan.RetentionDays,
	})
}

// Plans: GET /plans — the plans on offer, for comparing.
func (h *Handler) Plans(w http.ResponseWriter, r *http.Request) {
	ps, err := ListPlans(r.Context(), h.DB.Pool, true)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load plans")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"plans": ps})
}

// Get: GET /subscription — subscription.manage: plan, status, usage
// against every limit, and billing history.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	var resp map[string]any
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp, err = Summary(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the subscription")
		return
	}
	plans, err := ListPlans(r.Context(), h.DB.Pool, true)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load plans")
		return
	}
	resp["plans"] = plans
	httpx.JSON(w, http.StatusOK, resp)
}

// Summary is one merchant's subscription, usage and invoices (also used
// by the platform console, inside that merchant's WithTenant).
func Summary(ctx context.Context, tx pgx.Tx) (map[string]any, error) {
	e, err := Load(ctx, tx)
	if err != nil {
		return nil, err
	}
	usage, err := Usage(ctx, tx, e.Plan)
	if err != nil {
		return nil, err
	}
	invoices, err := ListInvoices(ctx, tx, 24)
	if err != nil {
		return nil, err
	}
	pos, err := billablePOS(ctx, tx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"plan": e.Plan, "status": e.Status, "stored_status": e.Stored.Status, "read_only": e.ReadOnly, "read_only_since": e.ReadOnlySince,
		"trial_ends_at": e.TrialEndsAt, "trial_used": e.Stored.TrialUsed, "current_period_end": e.PeriodEnd, "grace_ends_at": e.GraceEndsAt,
		"notes": e.Stored.Notes, "usage": usage, "billable_pos": pos, "invoices": invoices,
	}, nil
}

// Change: POST /subscription/change {plan_code} — subscription.manage.
func (h *Handler) Change(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	var req struct {
		PlanCode string `json:"plan_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PlanCode == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "plan_code is required")
		return
	}
	target, err := LoadPlan(r.Context(), h.DB.Pool, req.PlanCode)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no such plan")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the plan")
		return
	}
	var res ChangeResult
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		e, err := Load(ctx, tx)
		if err != nil {
			return err
		}
		res, err = ChangePlan(ctx, tx, e, target, claims.UserID)
		if err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "subscriptions", EntityID: claims.TenantID, Action: "update",
			PerformedBy: claims.UserID, Before: map[string]any{"plan": e.Plan.Code, "status": e.Status},
			After: map[string]any{"plan": target.Code, "outcome": res.Outcome}, Reason: "plan change requested"})
	})
	Invalidate(claims.TenantID)
	switch {
	case errors.Is(err, ErrSamePlan):
		httpx.Error(w, http.StatusConflict, "SAME_PLAN", "you're already on this plan")
	case errors.Is(err, ErrPlanInactive):
		httpx.Error(w, http.StatusConflict, "PLAN_NOT_OFFERED", "that plan isn't offered any more")
	case err != nil:
		log.Printf("subscription change: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not change the plan")
	default:
		httpx.JSON(w, http.StatusOK, res)
	}
}

// Pay: POST /subscription/invoices/{id}/pay — subscription.manage. Charges
// through the provider (the stub today) and applies the payment. Paying a
// paid invoice replays the result, so a retry never charges twice.
func (h *Handler) Pay(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	id := chi.URLParam(r, "id")
	var paid Invoice
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		inv, err := GetInvoice(ctx, tx, id)
		if err != nil {
			return err
		}
		if inv.Status == "paid" {
			paid = inv
			return nil
		}
		if inv.Status != "due" {
			return ErrInvoiceNotDue
		}
		e, err := Load(ctx, tx)
		if err != nil {
			return err
		}
		ref, err := h.Provider.Charge(ctx, inv)
		if err != nil {
			return err
		}
		paid, err = MarkPaid(ctx, tx, e, inv, h.Provider.Name(), ref, claims.UserID)
		if err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "billing_invoices", EntityID: inv.ID, Action: "update",
			PerformedBy: claims.UserID, Before: map[string]any{"status": "due"},
			After: map[string]any{"status": "paid", "amount": inv.Amount, "provider": h.Provider.Name(), "provider_ref": ref}})
	})
	Invalidate(claims.TenantID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "invoice not found")
	case errors.Is(err, ErrInvoiceNotDue):
		httpx.Error(w, http.StatusConflict, "INVOICE_NOT_DUE", "this invoice was voided")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not take the payment")
	default:
		httpx.JSON(w, http.StatusOK, paid)
	}
}
