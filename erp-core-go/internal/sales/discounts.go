package sales

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/approvals"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// Tiered discount authorization (Phase 15: the tiers are now the
// merchant's approval policy, internal/approvals, whose default is the
// one below), per pos_frd_complete.md "Manual Discount
// Authorization": 0-5% needs no extra approval, 5-15% needs a Branch
// Manager's PIN, 15-25% needs a Merchant Admin's PIN. The FRD specifies OTP
// for the top tier, but there's no notification channel in this codebase
// yet to deliver one (that's a Phase 3 "Notifications" item) — PIN
// verification against pin_hash is used as the interim control for both
// approval tiers instead, which is a deliberate, documented substitution
// with equivalent-or-stronger strength (a PIN, like an OTP, proves the
// approver was physically present), not a silently weaker shortcut.
const (
	roleBranchManager = "branch manager"
	roleMerchantAdmin = "merchant admin"
)

var errDiscountNotAuthorized = errors.New("discount exceeds what the authorizer's role/PIN permits")
var errDiscountOutOfRange = errors.New("discount value must be between 0 and 25 percent")
var errDiscountAbovePolicy = errors.New("this discount is above the largest the approval policy allows")
var errDiscountExceedsTotal = errors.New("discount is more than the sale's remaining value")

const (
	discountModePercent = "percent"
	discountModeAmount  = "amount"
)

type discountRequest struct {
	Type string `json:"type"` // "manual" | "coupon"
	// Mode is "percent" (default: Value is a percent of the subtotal) or
	// "amount" (Amount is a fixed rupee discount on the sale, e.g. ₹100
	// off). Either way the approval tier is judged on the percent of the
	// subtotal it comes to, so a fixed amount can't sidestep the policy.
	Mode          string  `json:"mode"`
	ValuePercent  float64 `json:"value"`
	Amount        float64 `json:"amount"`
	AuthorizedBy  string  `json:"authorized_by"`  // user id of the approving manager/admin; empty for the 0-5% tier
	AuthorizedPIN string  `json:"authorized_pin"` // required alongside authorized_by for tiers above 5%
	// AuthorizedByCode (Phase 12) names the approver by employee code — what
	// staff actually know, unlike a user id — for the POS's approval PIN pad.
	// Resolved to authorized_by server-side; ignored if authorized_by is set.
	AuthorizedByCode string `json:"authorized_by_code"`
	Reason           string `json:"reason"`
}

// ApplyDiscount: POST /sales/orders/{id}/discounts — the discount
// hierarchy's manual layer (pos_frd_complete.md §5, hierarchy item 6:
// "Manual discounts (with authorization)"), applied last, on top of
// whatever internal/promotions/internal/loyalty have already layered onto
// this order via ApplyDiscountLayer (discount_layer.go). The percent is
// still resolved against the order's raw subtotal (a manual "10% off"
// means 10% of the sale, not 10% of whatever's left after other
// discounts), then handed to ApplyDiscountLayer as a rupee amount that
// stacks additively — a cart with only a manual discount behaves exactly
// as before this hierarchy existed (0 existing + share = share).
func (h *Handler) ApplyDiscount(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var req discountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Type != "manual" && req.Type != "coupon" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "type must be 'manual' or 'coupon'")
		return
	}
	switch req.Mode {
	case "", discountModePercent:
		req.Mode = discountModePercent
		if req.ValuePercent <= 0 || req.ValuePercent > 100 {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "value must be above 0 and at most 100 percent")
			return
		}
	case discountModeAmount:
		if req.Amount <= 0 || round2(req.Amount) != req.Amount {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "amount must be above 0 with at most 2 decimals")
			return
		}
	default:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "mode must be 'percent' or 'amount'")
		return
	}

	var resp orderResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status, branchID string
		if err := tx.QueryRow(ctx, `SELECT status, branch_id FROM sales_orders WHERE id = $1`, orderID).Scan(&status, &branchID); err != nil {
			return err
		}
		if status != "cart" {
			return errOrderNotEditable
		}
		// Phase 15: the tiers are the merchant's approval policy
		// (internal/approvals); the default is the old 5/15/25.
		policy, err := approvals.Load(ctx, tx)
		if err != nil {
			return err
		}

		// Sized on the lines a discount can touch (DiscountableLineSQL): a
		// branch that keeps open-price items out of discounts leaves them
		// out of the percent's base too.
		var subtotal, remaining float64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(sol.unit_price*sol.quantity),0), COALESCE(SUM(sol.unit_price*sol.quantity - sol.discount_amount),0)
			FROM sales_order_lines sol JOIN sales_orders so ON so.id = sol.sales_order_id
			WHERE sol.sales_order_id = $1 AND `+DiscountableLineSQL, orderID).
			Scan(&subtotal, &remaining); err != nil {
			return err
		}
		if subtotal <= 0 {
			if only, err := onlyOpenPriceLeft(ctx, tx, orderID); err != nil {
				return err
			} else if only {
				return ErrOpenPriceNotDiscountable
			}
			return errDiscountOutOfRange // nothing to discount against
		}

		discountTotal, pct, valuePercent := resolveManualDiscount(req, subtotal)
		if req.Mode == discountModeAmount && discountTotal > round2(remaining) {
			return fmt.Errorf("%w: at most ₹%.2f", errDiscountExceedsTotal, round2(remaining))
		}
		tier, ok := policy.DiscountTierFor(pct)
		if !ok {
			if req.Mode == discountModeAmount {
				return fmt.Errorf("%w: at most %.4g%% (₹%.2f on this sale)", errDiscountAbovePolicy, policy.MaxDiscount(),
					math.Floor(subtotal*policy.MaxDiscount())/100) // rounded down: rounding up would itself be over
			}
			return fmt.Errorf("%w: at most %.4g%%", errDiscountAbovePolicy, policy.MaxDiscount())
		}

		var authorizerID *string
		if tier.Approver != approvals.LevelNone {
			// By user id (authorized_by) or, from the POS PIN pad, by
			// employee code; an unknown code or wrong PIN, a role below the
			// tier, or a branch outside the approver's scope all answer
			// DISCOUNT_NOT_AUTHORIZED alike.
			code := ""
			if req.AuthorizedBy == "" {
				code = req.AuthorizedByCode
			}
			id, err := approvals.Approve(ctx, tx, code, req.AuthorizedBy, req.AuthorizedPIN, tier.Approver, branchID)
			if err != nil {
				return err
			}
			if id == "" {
				return errDiscountNotAuthorized
			}
			authorizerID = &id
		}

		var applyErr error
		resp, applyErr = ApplyDiscountLayer(ctx, tx, DiscountLayer{
			OrderID:      orderID,
			Type:         req.Type,
			Amount:       discountTotal,
			ValuePercent: valuePercent,
			AuthorizedBy: authorizerID,
			AppliedBy:    claims.UserID,
			Reason:       req.Reason,
		})
		return applyErr
	})

	switch {
	case errors.Is(err, errOrderNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "this order is no longer a cart")
	case errors.Is(err, errDiscountNotAuthorized):
		httpx.Error(w, http.StatusForbidden, "DISCOUNT_NOT_AUTHORIZED", "the presented authorizer/PIN does not permit a discount of this size")
	case errors.Is(err, errDiscountAbovePolicy):
		httpx.Error(w, http.StatusBadRequest, "DISCOUNT_ABOVE_POLICY", err.Error())
	case errors.Is(err, errDiscountExceedsTotal):
		httpx.Error(w, http.StatusBadRequest, "DISCOUNT_EXCEEDS_TOTAL", err.Error())
	case IsOpenPriceNotDiscountable(err):
		httpx.Error(w, http.StatusConflict, "OPEN_PRICE_NOT_DISCOUNTABLE", err.Error())
	case errors.Is(err, errDiscountOutOfRange), errors.Is(err, ErrDiscountExceedsAvailable):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "order has nothing left to discount")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not apply discount")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// resolveManualDiscount returns the rupee amount a manual discount takes
// off, the percent of the subtotal that is (what the approval tiers are
// measured in), and the percent to record: a fixed amount records none,
// as migrations/012 asks of every rupee-amount layer, rather than a
// back-computed one.
func resolveManualDiscount(req discountRequest, subtotal float64) (amount, pct float64, valuePercent *float64) {
	if req.Mode == discountModeAmount {
		return req.Amount, req.Amount / subtotal * 100, nil
	}
	v := req.ValuePercent
	return round2(subtotal * v / 100), v, &v
}

func fetchRolesFor(ctx context.Context, tx pgx.Tx, userID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT r.name FROM roles r JOIN user_roles ur ON ur.role_id = r.id WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		roles = append(roles, name)
	}
	return roles, rows.Err()
}

// round2 rounds half away from zero. (int64(v*100+0.5) truncated
// negatives toward zero: -10.00 became -9.99.)
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
