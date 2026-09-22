// Phase 4, sub-area 4: Reconciliation & Audit — Inventory reconciliation
// (phased_roadmap.md; pos_frd_complete.md §16). See
// migrations/018_inventory_reconciliation.sql's header comment for scope
// (reconciliation type #3 of 4 — #1 Cash, #2 Payment Gateway, and #4
// Inter-branch Transfer are already closed; this is the last
// reconciliation type in this sub-area) and for why a non-zero variance
// posts a real stock adjustment + ledger entry immediately rather than
// going through the FRD's named flag→investigate→resolve→approve→adjust
// workflow.
package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// ---------------------------------------------------------------------
// POST /inventory/reconciliation — one physical count submission, cycle
// (a named subset of variants) or full (every variant with stock at this
// branch). Open to any authenticated user when every line's variance is
// zero (a stock clerk's count matching the books needs no one's
// approval); a non-zero variance anywhere in the count requires a
// Branch Manager/Merchant Admin PIN, checked inline exactly the way
// accounting.CreateCashReconciliation already authorizes a variance —
// not the router-level permission middleware, since the same endpoint
// has to stay open for the (far more common) clean-count case.
// ---------------------------------------------------------------------

type countLineInput struct {
	VariantID  string  `json:"variant_id"`
	CountedQty float64 `json:"counted_qty"`
}

type reconciliationLineResponse struct {
	VariantID     string `json:"variant_id"`
	SystemQty     string `json:"system_qty"`
	CountedQty    string `json:"counted_qty"`
	Variance      string `json:"variance"`
	VarianceValue string `json:"variance_value"`
}

type createReconciliationRequest struct {
	BranchID      string           `json:"branch_id"`
	ReconType     string           `json:"recon_type"` // "cycle" | "full"
	ReconDate     string           `json:"recon_date"` // YYYY-MM-DD
	Counts        []countLineInput `json:"counts"`
	Reason        string           `json:"reason"`         // required whenever any line has a non-zero variance
	AuthorizedBy  string           `json:"authorized_by"`  // required whenever any line has a non-zero variance
	AuthorizedPIN string           `json:"authorized_pin"` // required whenever any line has a non-zero variance
}

type reconciliationResponse struct {
	ReconciliationID string                       `json:"reconciliation_id"`
	BranchID         string                       `json:"branch_id"`
	ReconType        string                       `json:"recon_type"`
	ReconDate        string                       `json:"recon_date"`
	Reason           string                       `json:"reason"`
	VarianceValue    string                       `json:"variance_value"`
	Authorized       bool                         `json:"authorized"`
	Lines            []reconciliationLineResponse `json:"lines"`
	CreatedAt        string                       `json:"created_at"`
}

var (
	errInventoryVarianceNeedsReason   = errors.New("a non-zero variance requires a reason")
	errInventoryVarianceNotAuthorized = errors.New("variance exceeds what the authorizer's role/PIN permits")
)

func (h *Handler) CreateReconciliation(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createReconciliationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.BranchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id is required")
		return
	}
	if req.ReconType != "cycle" && req.ReconType != "full" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "recon_type must be 'cycle' or 'full'")
		return
	}
	if _, err := time.Parse("2006-01-02", req.ReconDate); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "recon_date must be YYYY-MM-DD")
		return
	}
	if len(req.Counts) == 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "at least one counted line is required")
		return
	}
	for _, c := range req.Counts {
		if c.VariantID == "" || c.CountedQty < 0 {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "each line needs a variant_id and a non-negative counted_qty")
			return
		}
	}

	var resp reconciliationResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		type computedLine struct {
			variantID     string
			systemQty     float64
			countedQty    float64
			variance      float64
			costPrice     float64
			varianceValue float64
		}
		lines := make([]computedLine, 0, len(req.Counts))
		var totalVarianceValue float64
		anyVariance := false
		for _, c := range req.Counts {
			var systemQty float64
			_ = tx.QueryRow(ctx, `SELECT on_hand FROM stock_levels WHERE branch_id = $1 AND variant_id = $2`, req.BranchID, c.VariantID).Scan(&systemQty)

			var costPrice float64
			if err := tx.QueryRow(ctx, `SELECT cost_price FROM product_variants WHERE id = $1`, c.VariantID).Scan(&costPrice); err != nil {
				return err
			}

			variance := round3(c.CountedQty - systemQty)
			if variance != 0 {
				anyVariance = true
			}
			varianceValue := round2(variance * costPrice)
			totalVarianceValue = round2(totalVarianceValue + varianceValue)
			lines = append(lines, computedLine{
				variantID: c.VariantID, systemQty: systemQty, countedQty: c.CountedQty,
				variance: variance, costPrice: costPrice, varianceValue: varianceValue,
			})
		}

		var authorizerID *string
		if anyVariance {
			if req.Reason == "" {
				return errInventoryVarianceNeedsReason
			}
			ok, err := inventoryAuthorizerPermits(ctx, tx, req.AuthorizedBy, req.AuthorizedPIN)
			if err != nil {
				return err
			}
			if !ok {
				return errInventoryVarianceNotAuthorized
			}
			authorizerID = &req.AuthorizedBy
		}

		var reconID, createdAt string
		if err := tx.QueryRow(ctx, `
			INSERT INTO inventory_reconciliations
				(id, merchant_id, branch_id, recon_type, recon_date, reason, variance_value, counted_by, authorized_by)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3::date, $4, $5, $6, $7)
			RETURNING id, created_at::text`,
			req.BranchID, req.ReconType, req.ReconDate, req.Reason, totalVarianceValue, claims.UserID, authorizerID,
		).Scan(&reconID, &createdAt); err != nil {
			return err
		}

		respLines := make([]reconciliationLineResponse, 0, len(lines))
		for _, l := range lines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO inventory_reconciliation_lines (id, reconciliation_id, variant_id, system_qty, counted_qty, variance, variance_value)
				VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6)`,
				reconID, l.variantID, l.systemQty, l.countedQty, l.variance, l.varianceValue); err != nil {
				return err
			}
			respLines = append(respLines, reconciliationLineResponse{
				VariantID: l.variantID, SystemQty: formatQty(l.systemQty), CountedQty: formatQty(l.countedQty),
				Variance: formatQty(l.variance), VarianceValue: formatMoney(l.varianceValue),
			})

			if l.variance == 0 {
				continue
			}

			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_levels (id, merchant_id, branch_id, variant_id, on_hand, reserved, version)
				VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, 0, 0)
				ON CONFLICT (branch_id, variant_id) DO UPDATE SET
				  on_hand = stock_levels.on_hand + EXCLUDED.on_hand,
				  version = stock_levels.version + 1,
				  updated_at = now()`,
				req.BranchID, l.variantID, l.variance); err != nil {
				return err
			}

			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_movements (merchant_id, branch_id, variant_id, movement_type, quantity_delta, reference_type, reference_id, reason, performed_by)
				VALUES (current_setting('app.tenant_id')::uuid, $1, $2, 'adjustment', $3, 'inventory_reconciliation', $4, $5, $6)`,
				req.BranchID, l.variantID, l.variance, reconID, req.Reason, claims.UserID); err != nil {
				return err
			}

			beforeJSON, _ := json.Marshal(map[string]float64{"on_hand": l.systemQty})
			afterJSON, _ := json.Marshal(map[string]float64{"on_hand": l.systemQty + l.variance})
			if _, err := tx.Exec(ctx, `
				INSERT INTO audit_logs (merchant_id, entity_type, entity_id, action, performed_by, before_value, after_value, reason)
				VALUES (current_setting('app.tenant_id')::uuid, 'stock_levels', $1, 'update', $2, $3, $4, $5)`,
				l.variantID, claims.UserID, beforeJSON, afterJSON, req.Reason); err != nil {
				return err
			}

			if l.varianceValue != 0 {
				journalLines := []accounting.JournalLine{
					{AccountCode: accounting.AccountInventory, Debit: posOrZero(l.varianceValue), Credit: posOrZero(-l.varianceValue)},
					{AccountCode: accounting.AccountInventoryLoss, Debit: posOrZero(-l.varianceValue), Credit: posOrZero(l.varianceValue)},
				}
				if _, err := accounting.PostJournalEntryOnDate(ctx, tx, req.BranchID, "inventory_reconciliation", reconID, "Inventory reconciliation: "+req.Reason, claims.UserID, req.ReconDate, journalLines); err != nil {
					return err
				}
			}
		}

		resp = reconciliationResponse{
			ReconciliationID: reconID, BranchID: req.BranchID, ReconType: req.ReconType, ReconDate: req.ReconDate,
			Reason: req.Reason, VarianceValue: formatMoney(totalVarianceValue), Authorized: authorizerID != nil,
			Lines: respLines, CreatedAt: createdAt,
		}
		return nil
	})

	switch {
	case errors.Is(err, errInventoryVarianceNeedsReason):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a non-zero variance requires a reason")
	case errors.Is(err, errInventoryVarianceNotAuthorized):
		httpx.Error(w, http.StatusForbidden, "VARIANCE_NOT_AUTHORIZED", "a Branch Manager or Merchant Admin PIN is required to close a reconciliation with a variance")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create inventory reconciliation")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// inventoryAuthorizerPermits mirrors accounting.cashAuthorizerPermits
// exactly (role-name check against Branch Manager/Merchant Admin, PIN
// verified via bcrypt) — duplicated rather than imported since that
// function is unexported in a different package and this check is small
// enough that copying it is clearer than exporting a cross-package
// dependency for one helper, same reasoning cash_reconciliation.go's own
// copy already gives.
func inventoryAuthorizerPermits(ctx context.Context, tx pgx.Tx, authorizedBy, authorizedPIN string) (bool, error) {
	if authorizedBy == "" || authorizedPIN == "" {
		return false, nil
	}
	var pinHash *string
	if err := tx.QueryRow(ctx, `SELECT pin_hash FROM users WHERE id = $1 AND status = 'active'`, authorizedBy).Scan(&pinHash); err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if pinHash == nil || bcrypt.CompareHashAndPassword([]byte(*pinHash), []byte(authorizedPIN)) != nil {
		return false, nil
	}
	rows, err := tx.Query(ctx, `SELECT r.name FROM roles r JOIN user_roles ur ON ur.role_id = r.id WHERE ur.user_id = $1`, authorizedBy)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		roles = append(roles, name)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return authn.HasRole(roles, "branch manager") || authn.HasRole(roles, "merchant admin"), nil
}

// ---------------------------------------------------------------------
// GET /inventory/reconciliation/{id} — one reconciliation's full record.
// ---------------------------------------------------------------------

func (h *Handler) GetReconciliation(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	reconID := chi.URLParam(r, "id")

	var resp reconciliationResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var authorized bool
		if err := tx.QueryRow(ctx, `
			SELECT id, branch_id, recon_type, recon_date::text, COALESCE(reason,''), variance_value::text, (authorized_by IS NOT NULL), created_at::text
			FROM inventory_reconciliations WHERE id = $1`, reconID,
		).Scan(&resp.ReconciliationID, &resp.BranchID, &resp.ReconType, &resp.ReconDate, &resp.Reason, &resp.VarianceValue, &authorized, &resp.CreatedAt); err != nil {
			return err
		}
		resp.Authorized = authorized

		rows, err := tx.Query(ctx, `
			SELECT variant_id, system_qty::text, counted_qty::text, variance::text, variance_value::text
			FROM inventory_reconciliation_lines WHERE reconciliation_id = $1 ORDER BY variant_id`, resp.ReconciliationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		resp.Lines = []reconciliationLineResponse{}
		for rows.Next() {
			var l reconciliationLineResponse
			if err := rows.Scan(&l.VariantID, &l.SystemQty, &l.CountedQty, &l.Variance, &l.VarianceValue); err != nil {
				return err
			}
			resp.Lines = append(resp.Lines, l)
		}
		return rows.Err()
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "reconciliation not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load reconciliation")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------
// GET /inventory/reconciliation/history?branch_id=&start=&end= — the
// FRD's "Settlement Reports" analog for inventory, one row per
// reconciliation session in range.
// ---------------------------------------------------------------------

type reconciliationSummary struct {
	ReconciliationID string `json:"reconciliation_id"`
	ReconType        string `json:"recon_type"`
	ReconDate        string `json:"recon_date"`
	VarianceValue    string `json:"variance_value"`
	Authorized       bool   `json:"authorized"`
}

func (h *Handler) ReconciliationHistory(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")
	if branchID == "" || start == "" || end == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id, start, and end are required")
		return
	}

	history := []reconciliationSummary{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, recon_type, recon_date::text, variance_value::text, (authorized_by IS NOT NULL)
			FROM inventory_reconciliations
			WHERE branch_id = $1 AND recon_date BETWEEN $2::date AND $3::date
			ORDER BY recon_date, created_at`, branchID, start, end)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s reconciliationSummary
			if err := rows.Scan(&s.ReconciliationID, &s.ReconType, &s.ReconDate, &s.VarianceValue, &s.Authorized); err != nil {
				return err
			}
			history = append(history, s)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load reconciliation history")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"branch_id": branchID, "start": start, "end": end, "history": history})
}

func round2(v float64) float64 {
	return float64(int64(v*100+sign(v)*0.5)) / 100
}

func round3(v float64) float64 {
	return float64(int64(v*1000+sign(v)*0.5)) / 1000
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

func formatMoney(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}
