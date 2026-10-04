package pricing

// Phase 10: scheduled price changes (pos_frd_complete.md §11 "Scheduled
// price changes (future effective date)"; migrations/035). A change is
// stored as pending and applied by RunScheduledPriceSweeper once its
// effective_at passes, through applyVariantPricing — the exact function
// PATCH /pricing/variants/{id} uses — so a change applied later obeys the
// same margin guard and leaves the same price_history trail as one
// applied by hand. A change that can't be applied is never skipped
// silently: it ends 'failed' with the reason, visible in the list.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/taxcalc"
)

var (
	errEffectiveAtNotFuture = errors.New("effective_at must be in the future")
	errScheduleNotPending   = errors.New("only a pending scheduled change can be cancelled")
)

type scheduleRequest struct {
	VariantID    string   `json:"variant_id"`
	CostPrice    *float64 `json:"cost_price"`
	MRP          *float64 `json:"mrp"`
	SellingPrice *float64 `json:"selling_price"`
	EffectiveAt  string   `json:"effective_at"` // RFC3339
	Reason       string   `json:"reason"`
	Override     bool     `json:"override"`
}

type scheduledChange struct {
	ScheduledChangeID string  `json:"scheduled_change_id"`
	VariantID         string  `json:"variant_id"`
	SKU               string  `json:"sku"`
	ProductName       string  `json:"product_name"`
	CostPrice         *string `json:"cost_price"`
	MRP               *string `json:"mrp"`
	SellingPrice      *string `json:"selling_price"`
	EffectiveAt       string  `json:"effective_at"`
	Reason            *string `json:"reason"`
	Override          bool    `json:"override"`
	Status            string  `json:"status"`
	FailureReason     *string `json:"failure_reason"`
	CreatedAt         string  `json:"created_at"`
	AppliedAt         *string `json:"applied_at"`
}

const scheduledChangeSelect = `
	SELECT s.id, s.variant_id, pv.sku, p.name, s.cost_price::text, s.mrp::text, s.selling_price::text,
	       s.effective_at::text, s.reason, s.override, s.status, s.failure_reason, s.created_at::text, s.applied_at::text
	FROM scheduled_price_changes s
	JOIN product_variants pv ON pv.id = s.variant_id
	JOIN products p ON p.id = pv.product_id`

func scanScheduledChange(row pgx.Row, c *scheduledChange) error {
	return row.Scan(&c.ScheduledChangeID, &c.VariantID, &c.SKU, &c.ProductName, &c.CostPrice, &c.MRP, &c.SellingPrice,
		&c.EffectiveAt, &c.Reason, &c.Override, &c.Status, &c.FailureReason, &c.CreatedAt, &c.AppliedAt)
}

// CreateScheduledChange: POST /pricing/scheduled-changes. The margin
// guard runs now too, against today's cost — catching an obviously
// below-cost schedule at entry beats discovering it as 'failed' later.
// It runs again at apply time regardless, since cost can move in between.
func (h *Handler) CreateScheduledChange(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.VariantID == "" || (req.CostPrice == nil && req.MRP == nil && req.SellingPrice == nil) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "variant_id and at least one of cost_price/mrp/selling_price are required")
		return
	}
	for _, v := range []*float64{req.CostPrice, req.MRP, req.SellingPrice} {
		if v != nil && *v < 0 {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "prices cannot be negative")
			return
		}
	}
	effectiveAt, err := time.Parse(time.RFC3339, req.EffectiveAt)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "effective_at must be an RFC3339 timestamp, e.g. 2026-11-01T00:00:00+05:30")
		return
	}
	if !effectiveAt.After(time.Now()) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", errEffectiveAtNotFuture.Error())
		return
	}

	var resp scheduledChange
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var cost, selling float64
		if err := tx.QueryRow(ctx, `SELECT cost_price, selling_price FROM product_variants WHERE id = $1`, req.VariantID).
			Scan(&cost, &selling); err != nil {
			return err
		}
		if req.CostPrice != nil {
			cost = *req.CostPrice
		}
		if req.SellingPrice != nil {
			selling = *req.SellingPrice
		}
		taxRatePct, inclusive, err := loadTaxBasis(ctx, tx, req.VariantID)
		if err != nil {
			return err
		}
		if taxcalc.NetPrice(selling, taxRatePct, inclusive) < cost && !req.Override {
			return errNegativeMargin
		}

		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO scheduled_price_changes (merchant_id, variant_id, cost_price, mrp, selling_price, effective_at, reason, override, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, NULLIF($6,''), $7, $8)
			RETURNING id`,
			req.VariantID, req.CostPrice, req.MRP, req.SellingPrice, effectiveAt, req.Reason, req.Override, claims.UserID,
		).Scan(&id); err != nil {
			return err
		}
		return scanScheduledChange(tx.QueryRow(ctx, scheduledChangeSelect+` WHERE s.id = $1`, id), &resp)
	})

	switch {
	case errors.Is(err, errNegativeMargin):
		httpx.Error(w, http.StatusConflict, "NEGATIVE_MARGIN", "this price would sell below cost — pass override:true to allow it anyway")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "variant not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not schedule price change")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// ListScheduledChanges: GET /pricing/scheduled-changes?status=&variant_id=
// — soonest effective first.
func (h *Handler) ListScheduledChanges(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	status := r.URL.Query().Get("status")
	variantID := r.URL.Query().Get("variant_id")

	changes := []scheduledChange{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, scheduledChangeSelect+`
			WHERE ($1 = '' OR s.status = $1) AND ($2 = '' OR s.variant_id::text = $2)
			ORDER BY s.effective_at, s.created_at
			LIMIT 500`, status, variantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c scheduledChange
			if err := scanScheduledChange(rows, &c); err != nil {
				return err
			}
			changes = append(changes, c)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list scheduled price changes")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"scheduled_changes": changes})
}

// CancelScheduledChange: POST /pricing/scheduled-changes/{id}/cancel —
// pending only; an applied change is history, undone by a new change.
func (h *Handler) CancelScheduledChange(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var resp scheduledChange
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM scheduled_price_changes WHERE id = $1 FOR UPDATE`, id).Scan(&status); err != nil {
			return err
		}
		if status != "pending" {
			return errScheduleNotPending
		}
		if _, err := tx.Exec(ctx, `UPDATE scheduled_price_changes SET status = 'cancelled' WHERE id = $1`, id); err != nil {
			return err
		}
		return scanScheduledChange(tx.QueryRow(ctx, scheduledChangeSelect+` WHERE s.id = $1`, id), &resp)
	})
	switch {
	case errors.Is(err, errScheduleNotPending):
		httpx.Error(w, http.StatusConflict, "SCHEDULED_CHANGE_NOT_PENDING", errScheduleNotPending.Error())
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "scheduled change not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not cancel scheduled change")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// RunScheduledPriceSweeper applies due scheduled changes every interval,
// same merchant-by-merchant shape as the other sweepers
// (quotations.RunExpirySweeper). A method on Handler so it can reindex
// search after each applied change, like the PATCH endpoint does.
func (h *Handler) RunScheduledPriceSweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		h.ApplyDueScheduledChanges(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ApplyDueScheduledChanges runs one sweep across every active merchant.
func (h *Handler) ApplyDueScheduledChanges(ctx context.Context) {
	rows, err := h.DB.Pool.Query(ctx, `SELECT id FROM merchants WHERE status = 'active'`)
	if err != nil {
		log.Printf("scheduled-price sweeper: list merchants: %v", err)
		return
	}
	var merchantIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			log.Printf("scheduled-price sweeper: scan merchant id: %v", err)
			return
		}
		merchantIDs = append(merchantIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("scheduled-price sweeper: list merchants: %v", err)
		return
	}

	for _, merchantID := range merchantIDs {
		var due []string
		if err := h.DB.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `
				SELECT id FROM scheduled_price_changes
				WHERE status = 'pending' AND effective_at <= now()
				ORDER BY effective_at, created_at`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					return err
				}
				due = append(due, id)
			}
			return rows.Err()
		}); err != nil {
			log.Printf("scheduled-price sweeper: merchant %s: list due: %v", merchantID, err)
			continue
		}
		// One transaction per change: a failure in one never rolls back
		// another that already applied, and changes for the same variant
		// apply in effective_at order.
		for _, id := range due {
			h.applyScheduledChange(ctx, merchantID, id)
		}
	}
}

func (h *Handler) applyScheduledChange(ctx context.Context, merchantID, id string) {
	var variantID string
	applied := false
	err := h.DB.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
		var status, reason string
		var cost, mrp, selling *float64
		var override bool
		var createdBy *string
		// SKIP LOCKED + the status re-check make a second API instance's
		// sweeper skip a change this one is already applying, rather than
		// applying it twice.
		err := tx.QueryRow(ctx, `
			SELECT variant_id, status, cost_price, mrp, selling_price, COALESCE(reason,''), override, created_by
			FROM scheduled_price_changes WHERE id = $1 FOR UPDATE SKIP LOCKED`, id,
		).Scan(&variantID, &status, &cost, &mrp, &selling, &reason, &override, &createdBy)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && status != "pending") {
			return nil
		}
		if err != nil {
			return err
		}

		if reason == "" {
			reason = "scheduled price change"
		} else {
			reason = "scheduled: " + reason
		}
		changedBy := ""
		if createdBy != nil {
			changedBy = *createdBy
		}

		// Savepoint, so a refused change can still be marked 'failed' in
		// this same transaction.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := applyVariantPricing(ctx, sp, variantID, cost, mrp, selling, reason, override, changedBy); err != nil {
			_ = sp.Rollback(ctx)
			failure := "could not apply: " + err.Error()
			if errors.Is(err, errNegativeMargin) {
				failure = "would sell below current cost (schedule it with override to allow)"
			}
			_, err := tx.Exec(ctx, `UPDATE scheduled_price_changes SET status = 'failed', failure_reason = $1, applied_at = now() WHERE id = $2`, failure, id)
			return err
		}
		if err := sp.Commit(ctx); err != nil {
			return err
		}
		applied = true
		_, err = tx.Exec(ctx, `UPDATE scheduled_price_changes SET status = 'applied', applied_at = now() WHERE id = $1`, id)
		return err
	})
	if err != nil {
		log.Printf("scheduled-price sweeper: merchant %s change %s: %v", merchantID, id, err)
		return
	}
	if applied {
		h.reindex(ctx, merchantID, variantID)
	}
}
