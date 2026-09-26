package hr

import (
	"context"
	"encoding/json"
	"net/http"

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
			RETURNING id, branch_id, name, start_time::text, end_time::text`,
			req.BranchID, req.Name, req.StartTime, req.EndTime,
		).Scan(&resp.ID, &resp.BranchID, &resp.Name, &resp.StartTime, &resp.EndTime)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create shift")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ListShifts: GET /hr/shifts
func (h *Handler) ListShifts(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	shifts := []shiftResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, branch_id, name, start_time::text, end_time::text FROM shifts ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s shiftResponse
			if err := rows.Scan(&s.ID, &s.BranchID, &s.Name, &s.StartTime, &s.EndTime); err != nil {
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
