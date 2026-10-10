package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

// Get: GET /settings/approval-policies — any signed-in user (the POS shows
// the discount tiers it will be held to).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var p Policy
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		p, err = Load(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load approval policies")
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

// Put: PUT /settings/approval-policies — approvals.manage; the whole policy,
// validated, audited with before/after.
func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var p Policy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := p.Validate(); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	raw, _ := json.Marshal(p)
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := Load(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO approval_policies (merchant_id, policy, updated_by, updated_at)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, now())
			ON CONFLICT (merchant_id) DO UPDATE SET policy = $1, updated_by = $2, updated_at = now()`, raw, claims.UserID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "approval_policies", EntityID: claims.TenantID, Action: "update",
			PerformedBy: claims.UserID, Before: before, After: p})
	})
	if errors.Is(err, ErrInvalid) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save approval policies")
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}
