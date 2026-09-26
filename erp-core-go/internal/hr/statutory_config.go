package hr

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// PTSlab is one bracket of statutory_config.pt_slabs — see
// migrations/023_hr_payroll.sql's column comment. Exported so
// internal/payroll can reuse it when computing a payslip's PT line
// without this package importing that one (payroll reads
// statutory_config directly by SQL, same cross-domain-read pattern
// internal/gst/einvoice already use).
type PTSlab struct {
	Min    float64  `json:"min"`
	Max    *float64 `json:"max"`
	Amount float64  `json:"amount"`
}

type StatutoryConfig struct {
	PFEmployeeRate  float64  `json:"pf_employee_rate"`
	PFEmployerRate  float64  `json:"pf_employer_rate"`
	PFWageCeiling   float64  `json:"pf_wage_ceiling"`
	ESIEmployeeRate float64  `json:"esi_employee_rate"`
	ESIEmployerRate float64  `json:"esi_employer_rate"`
	ESIWageCeiling  float64  `json:"esi_wage_ceiling"`
	PTSlabs         []PTSlab `json:"pt_slabs"`
	// LWFEmployee/LWFEmployer are added to EVERY payroll run that reads
	// this config — unlike PF/ESI/PT they are NOT a rate, and the real
	// LWF contribution is due annually or half-yearly, not monthly (the
	// FRD's own §7 says "Annual contribution"; Karnataka's actual rule is
	// half-yearly). Left nonzero here, a merchant running payroll monthly
	// would overpay LWF 6x-12x over. The correct operating pattern: leave
	// these at 0 (the default) except in the specific run(s) LWF is
	// actually due, set the real amount for just that run via
	// PATCH /hr/statutory-config immediately before running payroll, then
	// reset to 0 afterward. This is a real, documented manual-process
	// requirement, not a "set once" rate like everything else here.
	LWFEmployee    float64 `json:"lwf_employee_amount"`
	LWFEmployer    float64 `json:"lwf_employer_amount"`
	TDSRatePercent float64 `json:"tds_rate_percent"`
}

// LoadStatutoryConfig reads the calling tenant's one statutory_config row,
// creating a (fully zeroed/disabled) default row on first read so payroll
// computation never has to special-case "not configured yet" — an
// unconfigured merchant simply deducts nothing beyond what's explicitly
// set, never guesses a rate.
func LoadStatutoryConfig(ctx context.Context, tx pgx.Tx) (StatutoryConfig, error) {
	var cfg StatutoryConfig
	var slabsJSON []byte
	err := tx.QueryRow(ctx, `
		INSERT INTO statutory_config (id, merchant_id)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid)
		ON CONFLICT (merchant_id) DO UPDATE SET merchant_id = statutory_config.merchant_id
		RETURNING pf_employee_rate, pf_employer_rate, pf_wage_ceiling, esi_employee_rate, esi_employer_rate,
		          esi_wage_ceiling, pt_slabs, lwf_employee_amount, lwf_employer_amount, tds_rate_percent`,
	).Scan(&cfg.PFEmployeeRate, &cfg.PFEmployerRate, &cfg.PFWageCeiling, &cfg.ESIEmployeeRate, &cfg.ESIEmployerRate,
		&cfg.ESIWageCeiling, &slabsJSON, &cfg.LWFEmployee, &cfg.LWFEmployer, &cfg.TDSRatePercent)
	if err != nil {
		return cfg, err
	}
	if len(slabsJSON) > 0 {
		if err := json.Unmarshal(slabsJSON, &cfg.PTSlabs); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

// GetStatutoryConfig: GET /hr/statutory-config
func (h *Handler) GetStatutoryConfig(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var cfg StatutoryConfig
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		cfg, err = LoadStatutoryConfig(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load statutory config")
		return
	}
	httpx.JSON(w, http.StatusOK, cfg)
}

// UpdateStatutoryConfig: PATCH /hr/statutory-config — every field is
// optional; only what's present in the body is changed. pt_slabs, when
// present, replaces the whole array (a partial-slab merge would be
// ambiguous — which bracket is "the same" one being edited).
type updateStatutoryConfigRequest struct {
	PFEmployeeRate  *float64 `json:"pf_employee_rate"`
	PFEmployerRate  *float64 `json:"pf_employer_rate"`
	PFWageCeiling   *float64 `json:"pf_wage_ceiling"`
	ESIEmployeeRate *float64 `json:"esi_employee_rate"`
	ESIEmployerRate *float64 `json:"esi_employer_rate"`
	ESIWageCeiling  *float64 `json:"esi_wage_ceiling"`
	PTSlabs         []PTSlab `json:"pt_slabs"`
	LWFEmployee     *float64 `json:"lwf_employee_amount"`
	LWFEmployer     *float64 `json:"lwf_employer_amount"`
	TDSRatePercent  *float64 `json:"tds_rate_percent"`
}

func (h *Handler) UpdateStatutoryConfig(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req updateStatutoryConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var slabsJSON []byte
	var hasSlabs bool
	if req.PTSlabs != nil {
		var err error
		slabsJSON, err = json.Marshal(req.PTSlabs)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid pt_slabs")
			return
		}
		hasSlabs = true
	}

	var cfg StatutoryConfig
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := LoadStatutoryConfig(ctx, tx); err != nil { // ensures the row exists
			return err
		}
		var slabsArg any
		if hasSlabs {
			slabsArg = slabsJSON
		}
		if _, err := tx.Exec(ctx, `
			UPDATE statutory_config SET
			  pf_employee_rate = COALESCE($1, pf_employee_rate),
			  pf_employer_rate = COALESCE($2, pf_employer_rate),
			  pf_wage_ceiling = COALESCE($3, pf_wage_ceiling),
			  esi_employee_rate = COALESCE($4, esi_employee_rate),
			  esi_employer_rate = COALESCE($5, esi_employer_rate),
			  esi_wage_ceiling = COALESCE($6, esi_wage_ceiling),
			  pt_slabs = COALESCE($7::jsonb, pt_slabs),
			  lwf_employee_amount = COALESCE($8, lwf_employee_amount),
			  lwf_employer_amount = COALESCE($9, lwf_employer_amount),
			  tds_rate_percent = COALESCE($10, tds_rate_percent),
			  updated_at = now()
			WHERE merchant_id = current_setting('app.tenant_id')::uuid`,
			req.PFEmployeeRate, req.PFEmployerRate, req.PFWageCeiling, req.ESIEmployeeRate, req.ESIEmployerRate,
			req.ESIWageCeiling, slabsArg, req.LWFEmployee, req.LWFEmployer, req.TDSRatePercent); err != nil {
			return err
		}
		var err error
		cfg, err = LoadStatutoryConfig(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update statutory config")
		return
	}
	httpx.JSON(w, http.StatusOK, cfg)
}
