// Package loyalty implements Phase 3's Promotions & Loyalty sub-area's
// loyalty half (phased_roadmap.md; pos_frd_complete.md §5) — discount
// hierarchy item 5, "Loyalty points redemption," plus the earning side
// that makes redemption possible in the first place.
//
// Expiry is "1 year from last transaction (rolling)" per the FRD — the
// customer's WHOLE balance lapses if they go expiry_months without a new
// ledger entry, not a per-batch FIFO expiry. See
// migrations/012_promotions_loyalty.sql's loyalty_ledger comment for why
// that means no expires_at column and no sweeper: AvailableBalance below
// computes it on read, every time, from the ledger's own timestamps.
package loyalty

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/sales"
)

type Handler struct {
	DB *db.DB
}

type configResponse struct {
	EarnRupeesPerPoint   string `json:"earn_rupees_per_point"`
	RedeemPointsPerRupee string `json:"redeem_points_per_rupee"`
	ExpiryMonths         int    `json:"expiry_months"`
}

// ---------------------------------------------------------------------
// GET /loyalty/config — open to any authenticated user (a cashier needs
// to know the redeem rate to tell a customer what their points are
// worth); PATCH is the gated write.
// ---------------------------------------------------------------------

func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	cfg, err := h.fetchConfig(r.Context(), claims.TenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "loyalty is not configured for this merchant")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load loyalty config")
		return
	}
	httpx.JSON(w, http.StatusOK, cfg)
}

type updateConfigRequest struct {
	EarnRupeesPerPoint   *float64 `json:"earn_rupees_per_point"`
	RedeemPointsPerRupee *float64 `json:"redeem_points_per_rupee"`
	ExpiryMonths         *int     `json:"expiry_months"`
}

// PATCH /loyalty/config — gated by loyalty.manage.
func (h *Handler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req updateConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.EarnRupeesPerPoint != nil && *req.EarnRupeesPerPoint <= 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "earn_rupees_per_point must be positive")
		return
	}
	if req.RedeemPointsPerRupee != nil && *req.RedeemPointsPerRupee <= 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "redeem_points_per_rupee must be positive")
		return
	}
	if req.ExpiryMonths != nil && *req.ExpiryMonths <= 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "expiry_months must be positive")
		return
	}

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO loyalty_config (merchant_id, earn_rupees_per_point, redeem_points_per_rupee, expiry_months)
			VALUES (current_setting('app.tenant_id')::uuid, COALESCE($1,100), COALESCE($2,10), COALESCE($3,12))
			ON CONFLICT (merchant_id) DO UPDATE SET
				earn_rupees_per_point = COALESCE($1, loyalty_config.earn_rupees_per_point),
				redeem_points_per_rupee = COALESCE($2, loyalty_config.redeem_points_per_rupee),
				expiry_months = COALESCE($3, loyalty_config.expiry_months),
				updated_at = now()`,
			req.EarnRupeesPerPoint, req.RedeemPointsPerRupee, req.ExpiryMonths)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update loyalty config")
		return
	}
	cfg, err := h.fetchConfig(r.Context(), claims.TenantID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "config updated but could not be reloaded")
		return
	}
	httpx.JSON(w, http.StatusOK, cfg)
}

func (h *Handler) fetchConfig(ctx context.Context, tenantID string) (configResponse, error) {
	var cfg configResponse
	err := h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT earn_rupees_per_point::text, redeem_points_per_rupee::text, expiry_months
			FROM loyalty_config WHERE merchant_id = current_setting('app.tenant_id')::uuid`,
		).Scan(&cfg.EarnRupeesPerPoint, &cfg.RedeemPointsPerRupee, &cfg.ExpiryMonths)
	})
	return cfg, err
}

// ---------------------------------------------------------------------
// GET /customers/{id}/loyalty — available (rolling-expiry-aware) balance
// plus the raw ledger for audit/display.
// ---------------------------------------------------------------------

type ledgerEntry struct {
	EntryType    string  `json:"entry_type"`
	Points       int     `json:"points"`
	BalanceAfter int     `json:"balance_after"`
	OrderID      *string `json:"sales_order_id"`
	CreatedAt    string  `json:"created_at"`
}

type balanceResponse struct {
	CustomerID      string        `json:"customer_id"`
	AvailablePoints int           `json:"available_points"`
	Ledger          []ledgerEntry `json:"ledger"`
}

func (h *Handler) GetCustomerLoyalty(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	customerID := chi.URLParam(r, "id")

	resp := balanceResponse{CustomerID: customerID, Ledger: []ledgerEntry{}}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		available, err := AvailableBalance(ctx, tx, customerID)
		if err != nil {
			return err
		}
		resp.AvailablePoints = available

		rows, err := tx.Query(ctx, `
			SELECT entry_type, points, balance_after, sales_order_id::text, created_at::text
			FROM loyalty_ledger WHERE customer_id = $1 ORDER BY created_at DESC LIMIT 50`, customerID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e ledgerEntry
			if err := rows.Scan(&e.EntryType, &e.Points, &e.BalanceAfter, &e.OrderID, &e.CreatedAt); err != nil {
				return err
			}
			resp.Ledger = append(resp.Ledger, e)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load loyalty balance")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// AvailableBalance computes customerID's currently-usable point balance:
// zero if their most recent ledger entry is older than their merchant's
// expiry_months (the FRD's "rolling" expiry — the whole balance lapses
// together), otherwise the raw sum of every entry ever recorded (earn
// positive, redeem negative — nothing to exclude, since nothing this old
// survives the dormancy check above it).
func AvailableBalance(ctx context.Context, tx pgx.Tx, customerID string) (int, error) {
	var lastEntry *time.Time
	if err := tx.QueryRow(ctx, `SELECT MAX(created_at) FROM loyalty_ledger WHERE customer_id = $1`, customerID).Scan(&lastEntry); err != nil {
		return 0, err
	}
	if lastEntry == nil {
		return 0, nil
	}
	var expiryMonths int
	if err := tx.QueryRow(ctx, `SELECT expiry_months FROM loyalty_config WHERE merchant_id = current_setting('app.tenant_id')::uuid`).
		Scan(&expiryMonths); err != nil {
		if err == pgx.ErrNoRows {
			expiryMonths = 12 // FRD default, matches migrations/012's loyalty_config column default
		} else {
			return 0, err
		}
	}
	if lastEntry.Before(time.Now().AddDate(0, -expiryMonths, 0)) {
		return 0, nil
	}
	var balance int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(points),0) FROM loyalty_ledger WHERE customer_id = $1`, customerID).Scan(&balance); err != nil {
		return 0, err
	}
	return balance, nil
}

// ---------------------------------------------------------------------
// EarnForOrder implements sales.LoyaltyEarner — called from inside
// Checkout's own transaction (internal/sales/handlers.go) so points land
// atomically with the sale they're earned from, never orphaned by a
// crash between the two. Skips silently (not an error — a checkout must
// never fail because loyalty bookkeeping had nothing to do) when: the
// merchant has no loyalty_config row, the earn rate resolves to zero
// points, or this same order already redeemed points ("cannot earn and
// redeem in same transaction" — the FRD's restriction is enforced here,
// on the earn side, rather than by blocking Redeem, since redeem always
// happens before checkout finalizes the order and earn is the one
// deferred until finalization).
// ---------------------------------------------------------------------

func (h *Handler) EarnForOrder(ctx context.Context, tx pgx.Tx, customerID, orderID string, grandTotal float64) (bool, error) {
	var alreadyRedeemed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sales_order_discounts WHERE sales_order_id = $1 AND type = 'loyalty')`, orderID).
		Scan(&alreadyRedeemed); err != nil {
		return false, err
	}
	if alreadyRedeemed {
		return false, nil
	}

	var earnRate float64
	if err := tx.QueryRow(ctx, `SELECT earn_rupees_per_point FROM loyalty_config WHERE merchant_id = current_setting('app.tenant_id')::uuid`).
		Scan(&earnRate); err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if earnRate <= 0 {
		return false, nil
	}
	points := int(grandTotal / earnRate)
	if points <= 0 {
		return false, nil
	}

	var priorTotal int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(points),0) FROM loyalty_ledger WHERE customer_id = $1`, customerID).Scan(&priorTotal); err != nil {
		return false, err
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO loyalty_ledger (id, merchant_id, customer_id, sales_order_id, entry_type, points, balance_after)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, 'earn', $3, $4)`,
		customerID, orderID, points, priorTotal+points)
	return err == nil, err
}

// ---------------------------------------------------------------------
// POST /sales/orders/{id}/loyalty/redeem {points} — discount hierarchy
// item 5. Applies through sales.ApplyDiscountLayer like every other
// layer, so it stacks correctly with whatever promotions/coupon/manual
// discounts already touched this cart.
// ---------------------------------------------------------------------

type redeemRequest struct {
	Points int `json:"points"`
}

var (
	errOrderNotEditable   = errors.New("order is not editable")
	errNoCustomerOnOrder  = errors.New("order has no customer attached — loyalty points require a registered customer")
	errInsufficientPoints = errors.New("customer does not have this many available points")
)

func (h *Handler) Redeem(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var req redeemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Points <= 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "points must be positive")
		return
	}

	var resp any
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var customerID *string
		if err := tx.QueryRow(ctx, `SELECT status, customer_id FROM sales_orders WHERE id = $1`, orderID).
			Scan(&status, &customerID); err != nil {
			return err
		}
		if status != "cart" {
			return errOrderNotEditable
		}
		if customerID == nil {
			return errNoCustomerOnOrder
		}

		available, err := AvailableBalance(ctx, tx, *customerID)
		if err != nil {
			return err
		}
		if req.Points > available {
			return errInsufficientPoints
		}

		var redeemRate float64
		if err := tx.QueryRow(ctx, `SELECT redeem_points_per_rupee FROM loyalty_config WHERE merchant_id = current_setting('app.tenant_id')::uuid`).
			Scan(&redeemRate); err != nil {
			return err
		}
		amount := round2(float64(req.Points) / redeemRate)

		state, err := sales.ApplyDiscountLayer(ctx, tx, sales.DiscountLayer{
			OrderID:   orderID,
			Type:      "loyalty",
			Amount:    amount,
			AppliedBy: claims.UserID,
			Reason:    "loyalty points redeemed",
		})
		if err != nil {
			return err
		}

		var priorTotal int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(points),0) FROM loyalty_ledger WHERE customer_id = $1`, *customerID).Scan(&priorTotal); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO loyalty_ledger (id, merchant_id, customer_id, sales_order_id, entry_type, points, balance_after)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, 'redeem', $3, $4)`,
			*customerID, orderID, -req.Points, priorTotal-req.Points); err != nil {
			return err
		}

		resp = map[string]any{"discount_amount": amount, "order": state}
		return nil
	})

	switch {
	case errors.Is(err, errOrderNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "this order is no longer a cart")
	case errors.Is(err, errNoCustomerOnOrder):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "order has no customer attached — loyalty points require a registered customer")
	case errors.Is(err, errInsufficientPoints):
		httpx.Error(w, http.StatusConflict, "INSUFFICIENT_LOYALTY_POINTS", "customer does not have this many available points")
	case errors.Is(err, sales.ErrDiscountExceedsAvailable):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "order has nothing left to discount")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not redeem loyalty points")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
