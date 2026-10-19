package taxes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

// merchantSettings is the merchant's default row (no row = gst_split,
// "GST", no state).
type merchantSettings struct {
	Regime         string  `json:"regime"`
	TaxLabel       string  `json:"tax_label"`
	StateCode      *string `json:"state_code"`
	CurrencyCode   string  `json:"currency_code"`
	CurrencySymbol string  `json:"currency_symbol"`
}

// branchSettings is one branch's override (null = inherit) and what it
// resolves to.
type branchSettings struct {
	BranchID       string   `json:"branch_id"`
	BranchName     string   `json:"branch_name"`
	BranchGSTIN    string   `json:"branch_gstin"`
	Regime         *string  `json:"regime"`
	TaxLabel       *string  `json:"tax_label"`
	StateCode      *string  `json:"state_code"`
	CurrencyCode   *string  `json:"currency_code"`
	CurrencySymbol *string  `json:"currency_symbol"`
	Effective      Settings `json:"effective"`
}

type settingsResponse struct {
	Merchant   merchantSettings `json:"merchant"`
	Branches   []branchSettings `json:"branches"`
	States     []State          `json:"states"`
	Currencies []Currency       `json:"currencies"`
}

func loadAll(ctx context.Context, tx pgx.Tx) (settingsResponse, error) {
	var resp settingsResponse
	err := tx.QueryRow(ctx, `
		SELECT regime, COALESCE(tax_label, 'GST'), state_code, COALESCE(currency_code, 'INR'), COALESCE(currency_symbol, '₹')
		FROM tax_settings WHERE branch_id IS NULL`).
		Scan(&resp.Merchant.Regime, &resp.Merchant.TaxLabel, &resp.Merchant.StateCode, &resp.Merchant.CurrencyCode, &resp.Merchant.CurrencySymbol)
	if errors.Is(err, pgx.ErrNoRows) {
		resp.Merchant = merchantSettings{Regime: RegimeGSTSplit, TaxLabel: DefaultLabel, CurrencyCode: "INR", CurrencySymbol: "₹"}
	} else if err != nil {
		return resp, err
	}
	rows, err := tx.Query(ctx, `
		SELECT b.id::text, b.name, COALESCE(b.gstin, ''), bs.regime, bs.tax_label, bs.state_code, bs.currency_code, bs.currency_symbol,
		       COALESCE(bs.regime, ms.regime, 'gst_split'), COALESCE(bs.tax_label, ms.tax_label, 'GST'),
		       COALESCE(app_branch_state(b.id), ''),
		       COALESCE(bs.currency_code, ms.currency_code, 'INR'),
		       CASE WHEN bs.currency_code IS NOT NULL THEN bs.currency_symbol ELSE COALESCE(ms.currency_symbol, '₹') END
		FROM branches b
		LEFT JOIN tax_settings bs ON bs.branch_id = b.id
		LEFT JOIN tax_settings ms ON ms.merchant_id = b.merchant_id AND ms.branch_id IS NULL
		ORDER BY b.name`)
	if err != nil {
		return resp, err
	}
	defer rows.Close()
	resp.Branches = []branchSettings{}
	for rows.Next() {
		var b branchSettings
		if err := rows.Scan(&b.BranchID, &b.BranchName, &b.BranchGSTIN, &b.Regime, &b.TaxLabel, &b.StateCode, &b.CurrencyCode, &b.CurrencySymbol,
			&b.Effective.Regime, &b.Effective.TaxLabel, &b.Effective.StateCode, &b.Effective.CurrencyCode, &b.Effective.CurrencySymbol); err != nil {
			return resp, err
		}
		resp.Branches = append(resp.Branches, b)
	}
	resp.States = States()
	resp.Currencies = Currencies()
	return resp, rows.Err()
}

// GetSettings: GET /tax/settings — the merchant's default, every
// branch's override and what it resolves to, and the GST state list.
// Any signed-in user (the screens that print tax read it).
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var resp settingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp, err = loadAll(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load tax settings")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// GetRegime: GET /tax/regime?branch_id= — one branch's effective
// settings, for the POS (which caches it for offline sales).
func (h *Handler) GetRegime(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	if branchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id is required")
		return
	}
	var s Settings
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		s, err = Load(ctx, tx, branchID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load tax settings")
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

type settingsRequest struct {
	Regime    *string `json:"regime"`
	TaxLabel  *string `json:"tax_label"`
	StateCode *string `json:"state_code"`
	// Set together (or neither): ISO 4217 code and the printed symbol.
	CurrencyCode   *string `json:"currency_code"`
	CurrencySymbol *string `json:"currency_symbol"`
}

// validate trims the request and returns a message for the first bad
// field. A blank label or state means "not set".
func (req *settingsRequest) validate() string {
	if req.Regime != nil && *req.Regime != RegimeGSTSplit && *req.Regime != RegimeSingle {
		return "regime must be gst_split or single"
	}
	if req.TaxLabel != nil {
		if v := strings.TrimSpace(*req.TaxLabel); v == "" {
			req.TaxLabel = nil
		} else if len([]rune(v)) > 20 {
			return "tax_label can be at most 20 characters"
		} else {
			req.TaxLabel = &v
		}
	}
	if req.StateCode != nil {
		if v := strings.TrimSpace(*req.StateCode); v == "" {
			req.StateCode = nil
		} else if !ValidState(v) {
			return "state_code must be a GST state code (e.g. 33 for Tamil Nadu)"
		} else {
			req.StateCode = &v
		}
	}
	blank := func(p **string, upper bool) {
		if *p == nil {
			return
		}
		v := strings.TrimSpace(**p)
		if upper {
			v = strings.ToUpper(v)
		}
		if v == "" {
			*p = nil
		} else {
			*p = &v
		}
	}
	blank(&req.CurrencyCode, true)
	blank(&req.CurrencySymbol, false)
	if (req.CurrencyCode == nil) != (req.CurrencySymbol == nil) {
		return "currency_code and currency_symbol are set together"
	}
	if req.CurrencyCode != nil {
		if !currencyCode.MatchString(*req.CurrencyCode) {
			return "currency_code must be a 3-letter ISO 4217 code (e.g. INR, USD, AED)"
		}
		if n := len([]rune(*req.CurrencySymbol)); n > 5 {
			return "currency_symbol can be at most 5 characters"
		}
	}
	return ""
}

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

func decode(w http.ResponseWriter, r *http.Request) (settingsRequest, bool) {
	var req settingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return req, false
	}
	if msg := req.validate(); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg)
		return req, false
	}
	return req, true
}

// UpdateSettings: PUT /tax/settings — tax.settings. The merchant's
// default; regime is required, the label defaults to "GST".
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	req, ok := decode(w, r)
	if !ok {
		return
	}
	if req.Regime == nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "regime is required")
		return
	}
	label := DefaultLabel
	if req.TaxLabel != nil {
		label = *req.TaxLabel
	}
	var resp settingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := loadAll(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO tax_settings (merchant_id, regime, tax_label, state_code, currency_code, currency_symbol, updated_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $5, $6, $4)
			ON CONFLICT (merchant_id) WHERE branch_id IS NULL
			DO UPDATE SET regime = $1, tax_label = $2, state_code = $3, currency_code = $5, currency_symbol = $6, updated_by = $4, updated_at = now()`,
			*req.Regime, label, req.StateCode, claims.UserID, req.CurrencyCode, req.CurrencySymbol); err != nil {
			return err
		}
		if resp, err = loadAll(ctx, tx); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "tax_settings", EntityID: claims.TenantID, Action: "update",
			PerformedBy: claims.UserID, Before: before.Merchant, After: resp.Merchant})
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save tax settings")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// UpdateBranchSettings: PUT /tax/settings/branches/{id} — tax.settings.
// Every field null (or blank) = inherit the merchant's; a branch with
// nothing overridden has its row removed.
func (h *Handler) UpdateBranchSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := chi.URLParam(r, "id")
	req, ok := decode(w, r)
	if !ok {
		return
	}
	var resp settingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM branches WHERE id::text = $1)`, branchID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		before, err := loadAll(ctx, tx)
		if err != nil {
			return err
		}
		if req.Regime == nil && req.TaxLabel == nil && req.StateCode == nil && req.CurrencyCode == nil {
			_, err = tx.Exec(ctx, `DELETE FROM tax_settings WHERE branch_id::text = $1`, branchID)
		} else {
			_, err = tx.Exec(ctx, `
				INSERT INTO tax_settings (merchant_id, branch_id, regime, tax_label, state_code, currency_code, currency_symbol, updated_by)
				VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2, $3, $4, $6, $7, $5)
				ON CONFLICT (branch_id) WHERE branch_id IS NOT NULL
				DO UPDATE SET regime = $2, tax_label = $3, state_code = $4, currency_code = $6, currency_symbol = $7, updated_by = $5, updated_at = now()`,
				branchID, req.Regime, req.TaxLabel, req.StateCode, claims.UserID, req.CurrencyCode, req.CurrencySymbol)
		}
		if err != nil {
			return err
		}
		if resp, err = loadAll(ctx, tx); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "tax_settings", EntityID: branchID, Action: "update",
			PerformedBy: claims.UserID, Before: branchOf(before, branchID), After: branchOf(resp, branchID)})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save tax settings")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func branchOf(s settingsResponse, id string) *branchSettings {
	for i := range s.Branches {
		if s.Branches[i].BranchID == id {
			return &s.Branches[i]
		}
	}
	return nil
}
