package hr

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type salaryStructureResponse struct {
	EffectiveFrom    string `json:"effective_from"`
	Basic            string `json:"basic"`
	HRA              string `json:"hra"`
	SpecialAllowance string `json:"special_allowance"`
	OtherAllowances  string `json:"other_allowances"`
	PFApplicable     bool   `json:"pf_applicable"`
}

type setSalaryStructureRequest struct {
	EffectiveFrom    string  `json:"effective_from"`
	Basic            float64 `json:"basic"`
	HRA              float64 `json:"hra"`
	SpecialAllowance float64 `json:"special_allowance"`
	OtherAllowances  float64 `json:"other_allowances"`
	PFApplicable     *bool   `json:"pf_applicable"`
}

// SetSalaryStructure: POST /hr/employees/{id}/salary-structure — adds a
// new versioned row rather than updating in place, so a raise (or a
// benefits change) never loses what an employee was actually paid before
// it, which a past payroll run's payslip still needs to reconcile
// against.
func (h *Handler) SetSalaryStructure(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	employeeID := chi.URLParam(r, "id")
	var req setSalaryStructureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.EffectiveFrom == "" || req.Basic <= 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "effective_from and a positive basic are required")
		return
	}
	pfApplicable := true
	if req.PFApplicable != nil {
		pfApplicable = *req.PFApplicable
	}

	var resp salaryStructureResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO salary_structures (id, merchant_id, user_id, effective_from, basic, hra, special_allowance, other_allowances, pf_applicable)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2::date, $3, $4, $5, $6, $7)
			ON CONFLICT (user_id, effective_from) DO UPDATE SET
			  basic = EXCLUDED.basic, hra = EXCLUDED.hra, special_allowance = EXCLUDED.special_allowance,
			  other_allowances = EXCLUDED.other_allowances, pf_applicable = EXCLUDED.pf_applicable
			RETURNING effective_from::text, basic::text, hra::text, special_allowance::text, other_allowances::text, pf_applicable`,
			employeeID, req.EffectiveFrom, req.Basic, req.HRA, req.SpecialAllowance, req.OtherAllowances, pfApplicable,
		).Scan(&resp.EffectiveFrom, &resp.Basic, &resp.HRA, &resp.SpecialAllowance, &resp.OtherAllowances, &resp.PFApplicable)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not set salary structure")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// GetSalaryStructure: GET /hr/employees/{id}/salary-structure — full
// version history, newest first.
func (h *Handler) GetSalaryStructure(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	employeeID := chi.URLParam(r, "id")

	history := []salaryStructureResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT effective_from::text, basic::text, hra::text, special_allowance::text, other_allowances::text, pf_applicable
			FROM salary_structures WHERE user_id = $1 ORDER BY effective_from DESC`, employeeID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s salaryStructureResponse
			if err := rows.Scan(&s.EffectiveFrom, &s.Basic, &s.HRA, &s.SpecialAllowance, &s.OtherAllowances, &s.PFApplicable); err != nil {
				return err
			}
			history = append(history, s)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load salary structure")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"salary_structure_history": history})
}
