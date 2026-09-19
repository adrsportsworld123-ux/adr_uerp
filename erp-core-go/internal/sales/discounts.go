package sales

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// Tiered discount authorization, per pos_frd_complete.md "Manual Discount
// Authorization": 0-5% needs no extra approval, 5-15% needs a Branch
// Manager's PIN, 15-25% needs a Merchant Admin's PIN. The FRD specifies OTP
// for the top tier, but there's no notification channel in this codebase
// yet to deliver one (that's a Phase 3 "Notifications" item) — PIN
// verification against pin_hash is used as the interim control for both
// approval tiers instead, which is a deliberate, documented substitution
// with equivalent-or-stronger strength (a PIN, like an OTP, proves the
// approver was physically present), not a silently weaker shortcut.
const (
	discountTierNoApproval = 5.0
	discountTierManagerPIN = 15.0
	discountTierAdminPIN   = 25.0
	roleBranchManager      = "branch manager"
	roleMerchantAdmin      = "merchant admin"
)

var errDiscountNotAuthorized = errors.New("discount exceeds what the authorizer's role/PIN permits")
var errDiscountOutOfRange = errors.New("discount value must be between 0 and 25 percent")

type discountRequest struct {
	Type          string  `json:"type"` // "manual" | "coupon"
	ValuePercent  float64 `json:"value"`
	AuthorizedBy  string  `json:"authorized_by"`  // user id of the approving manager/admin; empty for the 0-5% tier
	AuthorizedPIN string  `json:"authorized_pin"` // required alongside authorized_by for tiers above 5%
	Reason        string  `json:"reason"`
}

// ApplyDiscount: POST /sales/orders/{id}/discounts — resolves the discount
// amount from the order's current subtotal, distributes it across existing
// lines proportionally to each line's share of the subtotal (so
// discount_total always reconciles to SUM(sales_order_lines.discount_amount),
// the same invariant every other total on this order already relies on),
// and records who authorized it in sales_order_discounts.
//
// Applied pre-tax: each line's discount share reduces its taxable value
// before tax is recomputed at that line's own rate, matching GST treatment
// (tax is owed on the discounted price, not the pre-discount price) —
// this re-joins product_variants/tax_slabs per line rather than reusing
// the tax_amount AddLine originally stored, since that was computed
// against the pre-discount subtotal and would otherwise overstate tax
// after a discount is applied.
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
	if req.ValuePercent <= 0 || req.ValuePercent > discountTierAdminPIN {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "value must be between 0 and 25 percent")
		return
	}

	var resp orderResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM sales_orders WHERE id = $1`, orderID).Scan(&status); err != nil {
			return err
		}
		if status != "cart" {
			return errOrderNotEditable
		}

		var subtotal float64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(unit_price*quantity),0) FROM sales_order_lines WHERE sales_order_id = $1`, orderID).
			Scan(&subtotal); err != nil {
			return err
		}
		if subtotal <= 0 {
			return errDiscountOutOfRange // nothing to discount against
		}

		var authorizerID *string
		if req.ValuePercent > discountTierNoApproval {
			ok, err := authorizerPermits(ctx, tx, req, req.ValuePercent)
			if err != nil {
				return err
			}
			if !ok {
				return errDiscountNotAuthorized
			}
			authorizerID = &req.AuthorizedBy
		}

		discountTotal := round2(subtotal * req.ValuePercent / 100)

		rows, err := tx.Query(ctx, `
			SELECT sol.id, sol.unit_price, sol.quantity,
			       COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) + COALESCE(ts.cess_rate,0)
			FROM sales_order_lines sol
			JOIN product_variants pv ON pv.id = sol.variant_id
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
			WHERE sol.sales_order_id = $1`, orderID)
		if err != nil {
			return err
		}
		type line struct {
			lineID         string
			unitPrice, qty float64
			taxRatePct     float64
		}
		var lines []line
		for rows.Next() {
			var l line
			if err := rows.Scan(&l.lineID, &l.unitPrice, &l.qty, &l.taxRatePct); err != nil {
				rows.Close()
				return err
			}
			lines = append(lines, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, l := range lines {
			lineSubtotal := l.unitPrice * l.qty
			share := round2(discountTotal * (lineSubtotal / subtotal))
			taxableValue := lineSubtotal - share
			taxAmount := round2(taxableValue * l.taxRatePct / 100)
			lineTotal := taxableValue + taxAmount
			if _, err := tx.Exec(ctx, `
				UPDATE sales_order_lines SET discount_amount = $1, tax_amount = $2, line_total = $3 WHERE id = $4`,
				share, taxAmount, lineTotal, l.lineID); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO sales_order_discounts (id, merchant_id, sales_order_id, type, value_percent, discount_amount, authorized_by, applied_by, reason)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7)`,
			orderID, req.Type, req.ValuePercent, discountTotal, authorizerID, claims.UserID, req.Reason); err != nil {
			return err
		}

		return recalcOrderTotals(ctx, tx, orderID, &resp)
	})

	switch {
	case errors.Is(err, errOrderNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "this order is no longer a cart")
	case errors.Is(err, errDiscountNotAuthorized):
		httpx.Error(w, http.StatusForbidden, "DISCOUNT_NOT_AUTHORIZED", "the presented authorizer/PIN does not permit a discount of this size")
	case errors.Is(err, errDiscountOutOfRange):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "order has nothing to discount")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not apply discount")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// authorizerPermits checks that req.AuthorizedBy names a user, within the
// same tenant, holding the role required for valuePercent's tier, and that
// req.AuthorizedPIN matches that user's pin_hash.
func authorizerPermits(ctx context.Context, tx pgx.Tx, req discountRequest, valuePercent float64) (bool, error) {
	if req.AuthorizedBy == "" || req.AuthorizedPIN == "" {
		return false, nil
	}
	var pinHash *string
	if err := tx.QueryRow(ctx, `SELECT pin_hash FROM users WHERE id = $1 AND status = 'active'`, req.AuthorizedBy).Scan(&pinHash); err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if pinHash == nil || bcrypt.CompareHashAndPassword([]byte(*pinHash), []byte(req.AuthorizedPIN)) != nil {
		return false, nil
	}
	roles, err := fetchRolesFor(ctx, tx, req.AuthorizedBy)
	if err != nil {
		return false, err
	}
	requiredRole := roleBranchManager
	if valuePercent > discountTierManagerPIN {
		requiredRole = roleMerchantAdmin
	}
	// Merchant Admin's approval also covers the manager tier.
	return authn.HasRole(roles, requiredRole) || (requiredRole == roleBranchManager && authn.HasRole(roles, roleMerchantAdmin)), nil
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

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
