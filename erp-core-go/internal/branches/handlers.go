// Package branches implements Phase 2's Multi-Branch sub-area
// (phased_roadmap.md): branch management (previously only possible via
// raw SQL — see this file's own gap) and the inter-branch transfer
// workflow (transfers.go).
package branches

import (
	"context"
	"encoding/json"
	"net/http"

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
		return tx.QueryRow(ctx, `
			INSERT INTO branches (id, merchant_id, name, code, timezone, gstin)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4)
			RETURNING id, name, code, timezone, COALESCE(gstin,''), status`,
			req.Name, req.Code, timezone, req.GSTIN,
		).Scan(&resp.BranchID, &resp.Name, &resp.Code, &resp.Timezone, &resp.GSTIN, &resp.Status)
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
	branches := []branchResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, code, timezone, COALESCE(gstin,''), status FROM branches ORDER BY name`)
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
	httpx.JSON(w, http.StatusOK, map[string]any{"branches": branches})
}

// UpdateBranch: PATCH /branches/{id}
func (h *Handler) UpdateBranch(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := chi.URLParam(r, "id")

	var req branchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}

	var resp branchResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			UPDATE branches SET
			  name = COALESCE(NULLIF($1, ''), name),
			  code = COALESCE(NULLIF($2, ''), code),
			  timezone = COALESCE(NULLIF($3, ''), timezone),
			  gstin = COALESCE(NULLIF($4, ''), gstin),
			  updated_at = now()
			WHERE id = $5
			RETURNING id, name, code, timezone, COALESCE(gstin,''), status`,
			req.Name, req.Code, req.Timezone, req.GSTIN, branchID,
		).Scan(&resp.BranchID, &resp.Name, &resp.Code, &resp.Timezone, &resp.GSTIN, &resp.Status)
	})
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update branch")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
