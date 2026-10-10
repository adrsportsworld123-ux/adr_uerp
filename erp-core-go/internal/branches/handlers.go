// Package branches implements Phase 2's Multi-Branch sub-area
// (phased_roadmap.md): branch management (previously only possible via
// raw SQL — see this file's own gap) and the inter-branch transfer
// workflow (transfers.go).
package branches

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

type branchResponse struct {
	BranchID string `json:"branch_id"`
	Name     string `json:"name"`
	Code     string `json:"code"`
	Timezone string `json:"timezone"`
	GSTIN    string `json:"gstin"`
	Status   string `json:"status"`
}

type branchRequest struct {
	Name     string `json:"name"`
	Code     string `json:"code"`
	Timezone string `json:"timezone"`
	GSTIN    string `json:"gstin"`
}

// updateBranchRequest (Phase 11): omitted = keep. Name/code/timezone
// can't be blanked; gstin "" clears it (a branch may have none).
type updateBranchRequest struct {
	Name     *string `json:"name"`
	Code     *string `json:"code"`
	Timezone *string `json:"timezone"`
	GSTIN    *string `json:"gstin"`
	Status   *string `json:"status"`
}

// CreateBranch: POST /branches
func (h *Handler) CreateBranch(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req branchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Name == "" || req.Code == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name and code are required")
		return
	}
	timezone := req.Timezone
	if timezone == "" {
		timezone = "Asia/Kolkata"
	}

	var resp branchResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO branches (id, merchant_id, name, code, timezone, gstin)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4)
			RETURNING id, name, code, timezone, COALESCE(gstin,''), status`,
			req.Name, req.Code, timezone, req.GSTIN,
		).Scan(&resp.BranchID, &resp.Name, &resp.Code, &resp.Timezone, &resp.GSTIN, &resp.Status); err != nil {
			return err
		}
		// Every branch needs a real pos_terminal_id an "online" channel
		// order can reference (sales_orders.pos_terminal_id is NOT NULL,
		// same as any in-person order) — see
		// migrations/026_wholesale_b2b.sql's header for why this is a
		// virtual terminal rather than relaxing that constraint.
		// Phase 14: and one for sales rung up in the admin app
		// (migrations/042_backoffice_sales.sql).
		_, err := tx.Exec(ctx, `
			INSERT INTO pos_terminals (id, merchant_id, branch_id, name, channel, status)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, 'Online Storefront', 'online', 'active'),
			       (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, 'Back Office', 'backoffice', 'active')`,
			resp.BranchID)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create branch — code may already be in use")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ListBranches: GET /branches
func (h *Handler) ListBranches(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	// Phase 11 list convention (httpx.ListParams): q matches name, code or
	// GSTIN; unpaged unless page/limit is given, so every branch picker
	// (transfers, POS, reports) still gets the full list, in name order.
	p := httpx.ParseListParams(r, map[string]string{"name": "name", "code": "code", "created": "created_at"}, "name")
	status := p.Status
	if status == "all" {
		status = ""
	}
	if status != "" && status != "active" && status != "inactive" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be all, active, or inactive")
		return
	}
	limit, offset := p.SQLLimit()

	branches := []branchResponse{}
	total := 0
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		where := ` WHERE ($1 = '' OR name ILIKE '%' || $1 || '%' OR code ILIKE '%' || $1 || '%' OR gstin ILIKE '%' || $1 || '%')
			AND ($2 = '' OR status = $2)`
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM branches`+where, p.Q, status).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, name, code, timezone, COALESCE(gstin,''), status FROM branches`+where+
			` ORDER BY `+p.OrderBy+`, id LIMIT $3 OFFSET $4`, p.Q, status, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b branchResponse
			if err := rows.Scan(&b.BranchID, &b.Name, &b.Code, &b.Timezone, &b.GSTIN, &b.Status); err != nil {
				return err
			}
			branches = append(branches, b)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list branches")
		return
	}
	httpx.JSON(w, http.StatusOK, p.Envelope("branches", branches, total))
}

// UpdateBranch: PATCH /branches/{id}
func (h *Handler) UpdateBranch(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := chi.URLParam(r, "id")

	var req updateBranchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	for _, f := range []*string{req.Name, req.Code, req.Timezone} {
		if f != nil && strings.TrimSpace(*f) == "" {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name, code and timezone can't be empty")
			return
		}
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "inactive" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be active or inactive")
		return
	}

	var resp branchResponse
	badTimezone := false
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// Validated against Postgres's own zone list — the same one price
		// rules use when they evaluate a branch's local time — so a zone
		// accepted here always works there.
		if req.Timezone != nil {
			var known bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_timezone_names WHERE name = $1)`, *req.Timezone).Scan(&known); err != nil {
				return err
			}
			if !known {
				badTimezone = true
				return nil
			}
		}
		return tx.QueryRow(ctx, `
			UPDATE branches SET
			  name = COALESCE($1, name),
			  code = COALESCE($2, code),
			  timezone = COALESCE($3, timezone),
			  gstin = CASE WHEN $4::text IS NULL THEN gstin ELSE NULLIF($4, '') END,
			  status = COALESCE($6, status),
			  updated_at = now()
			WHERE id = $5
			RETURNING id, name, code, timezone, COALESCE(gstin,''), status`,
			req.Name, req.Code, req.Timezone, req.GSTIN, branchID, req.Status,
		).Scan(&resp.BranchID, &resp.Name, &resp.Code, &resp.Timezone, &resp.GSTIN, &resp.Status)
	})
	if badTimezone {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "unknown timezone "+*req.Timezone+" — use an IANA name such as Asia/Kolkata")
		return
	}
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update branch — code may already be in use")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
