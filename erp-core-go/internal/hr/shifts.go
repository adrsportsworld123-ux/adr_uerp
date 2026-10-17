package hr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type shiftResponse struct {
	ID        string  `json:"id"`
	BranchID  *string `json:"branch_id,omitempty"`
	Name      string  `json:"name"`
	StartTime string  `json:"start_time"`
	EndTime   string  `json:"end_time"`
	Status    string  `json:"status"`
}

type createShiftRequest struct {
	BranchID  *string `json:"branch_id"`
	Name      string  `json:"name"`
	StartTime string  `json:"start_time"` // "HH:MM"
	EndTime   string  `json:"end_time"`
}

// CreateShift: POST /hr/shifts
func (h *Handler) CreateShift(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createShiftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Name == "" || req.StartTime == "" || req.EndTime == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name, start_time and end_time are required")
		return
	}

	var resp shiftResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO shifts (id, merchant_id, branch_id, name, start_time, end_time)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3::time, $4::time)
			RETURNING id, branch_id, name, start_time::text, end_time::text, status`,
			req.BranchID, req.Name, req.StartTime, req.EndTime,
		).Scan(&resp.ID, &resp.BranchID, &resp.Name, &resp.StartTime, &resp.EndTime, &resp.Status)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create shift")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ListShifts: GET /hr/shifts[?status=active|inactive] — every shift unless
// a status is named (the employee form asks for active ones).
func (h *Handler) ListShifts(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	shifts := []shiftResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, branch_id, name, start_time::text, end_time::text, status FROM shifts
			WHERE ($1 = '' OR $1 = 'all' OR status = $1) ORDER BY name`, r.URL.Query().Get("status"))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s shiftResponse
			if err := rows.Scan(&s.ID, &s.BranchID, &s.Name, &s.StartTime, &s.EndTime, &s.Status); err != nil {
				return err
			}
			shifts = append(shifts, s)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list shifts")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"shifts": shifts})
}

type updateShiftRequest struct {
	Name      *string `json:"name"`
	BranchID  *string `json:"branch_id"` // "" = all branches
	StartTime *string `json:"start_time"`
	EndTime   *string `json:"end_time"`
	Status    *string `json:"status"` // active | inactive
}

// UpdateShift: PATCH /hr/shifts/{id} (hr.manage) — omitted = keep. An
// inactive shift stays on the employees already assigned to it (their
// late/early minutes still compute) but isn't offered for new assignments.
func (h *Handler) UpdateShift(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req updateShiftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "inactive" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be active or inactive")
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name can't be empty")
		return
	}
	for _, t := range []*string{req.StartTime, req.EndTime} {
		if t != nil {
			if _, err := time.Parse("15:04", strings.TrimSpace(*t)); err != nil {
				if _, err := time.Parse("15:04:05", strings.TrimSpace(*t)); err != nil {
					httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "start_time and end_time are HH:MM")
					return
				}
			}
		}
	}
	var resp shiftResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			UPDATE shifts SET
			  name = COALESCE(NULLIF(trim($2::text), ''), name),
			  branch_id = CASE WHEN $3::text IS NULL THEN branch_id ELSE NULLIF($3::text, '')::uuid END,
			  start_time = COALESCE($4::time, start_time),
			  end_time = COALESCE($5::time, end_time),
			  status = COALESCE($6::text, status)
			WHERE id::text = $1
			RETURNING id, branch_id, name, start_time::text, end_time::text, status`,
			chi.URLParam(r, "id"), req.Name, req.BranchID, req.StartTime, req.EndTime, req.Status,
		).Scan(&resp.ID, &resp.BranchID, &resp.Name, &resp.StartTime, &resp.EndTime, &resp.Status)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "shift not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update the shift")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
