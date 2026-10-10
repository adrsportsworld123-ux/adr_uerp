// Package approvals holds the merchant's approval policy (Phase 15,
// phased_roadmap.md "Configurable approval policies ... stored per merchant
// and read by the existing inline-PIN pattern"; migrations/045) and the one
// check every inline approval goes through: an approver's employee code and
// PIN, a role at or above the policy's level, and the branch the action is
// in inside the approver's own data scope (a Branch Manager of one store
// can't approve a refund in another).
//
// The defaults are exactly the behaviour that was hardcoded before, so a
// merchant who never opens the policy page sees no change.
package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"erp-core-go/internal/authn"
)

// Approval levels. "none" needs no approver; "branch_manager" is satisfied
// by a Branch Manager or a Merchant Admin; "merchant_admin" only by a
// Merchant Admin. "any" (below-cost prices only) means whoever already holds
// pricing.manage, as before.
const (
	LevelNone          = "none"
	LevelAny           = "any"
	LevelBranchManager = "branch_manager"
	LevelMerchantAdmin = "merchant_admin"
)

// DiscountTier: a manual discount up to UpTo percent needs Approver.
type DiscountTier struct {
	UpTo     float64 `json:"up_to"`
	Approver string  `json:"approver"`
}

type Policy struct {
	// Ascending; the last tier's UpTo is the largest discount allowed.
	DiscountTiers []DiscountTier `json:"discount_tiers"`
	// Refunds (when the merchant's return policy asks for approval):
	// only refunds above this amount need it; nil = every refund.
	RefundApprovalAbove *float64 `json:"refund_approval_above"`
	RefundApprover      string   `json:"refund_approver"`
	// Who may save a selling price below cost (the override).
	BelowCostPrice string `json:"below_cost_price"`
	// Stock adjustments worth more than this at cost need an approver's
	// code + PIN; nil = never.
	StockAdjustApprovalAbove *float64 `json:"stock_adjust_approval_above"`
	StockAdjustApprover      string   `json:"stock_adjust_approver"`
	// With JV approval on: vouchers totalling at most this post without
	// approval; nil = every voucher needs it.
	JVApprovalAbove *float64 `json:"jv_approval_above"`
}

// Default is the behaviour before policies existed.
func Default() Policy {
	return Policy{
		DiscountTiers: []DiscountTier{
			{UpTo: 5, Approver: LevelNone},
			{UpTo: 15, Approver: LevelBranchManager},
			{UpTo: 25, Approver: LevelMerchantAdmin},
		},
		RefundApprover:      LevelBranchManager,
		BelowCostPrice:      LevelAny,
		StockAdjustApprover: LevelBranchManager,
	}
}

var ErrInvalid = errors.New("invalid approval policy")

func validLevel(l string, allowNone bool) bool {
	return l == LevelBranchManager || l == LevelMerchantAdmin || (allowNone && l == LevelNone)
}

// Validate checks a policy is coherent before it's saved.
func (p Policy) Validate() error {
	if len(p.DiscountTiers) == 0 || len(p.DiscountTiers) > 6 {
		return fmt.Errorf("%w: 1 to 6 discount tiers", ErrInvalid)
	}
	prev := 0.0
	for i, t := range p.DiscountTiers {
		if t.UpTo <= prev || t.UpTo > 100 {
			return fmt.Errorf("%w: discount tiers must rise, above 0 and at most 100%% (tier %d)", ErrInvalid, i+1)
		}
		if !validLevel(t.Approver, true) {
			return fmt.Errorf("%w: tier %d approver must be none, branch_manager or merchant_admin", ErrInvalid, i+1)
		}
		prev = t.UpTo
	}
	for _, v := range []*float64{p.RefundApprovalAbove, p.StockAdjustApprovalAbove, p.JVApprovalAbove} {
		if v != nil && *v < 0 {
			return fmt.Errorf("%w: thresholds can't be negative", ErrInvalid)
		}
	}
	if !validLevel(p.RefundApprover, false) || !validLevel(p.StockAdjustApprover, false) {
		return fmt.Errorf("%w: approvers must be branch_manager or merchant_admin", ErrInvalid)
	}
	if p.BelowCostPrice != LevelAny && !validLevel(p.BelowCostPrice, false) {
		return fmt.Errorf("%w: below_cost_price must be any, branch_manager or merchant_admin", ErrInvalid)
	}
	return nil
}

// Load reads the merchant's policy, or the default when there is none.
func Load(ctx context.Context, tx pgx.Tx) (Policy, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT policy FROM approval_policies`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Default(), nil
	}
	if err != nil {
		return Policy{}, err
	}
	p := Default()
	if err := json.Unmarshal(raw, &p); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// DiscountTierFor returns the tier covering pct, or false when pct is above
// the largest allowed discount.
func (p Policy) DiscountTierFor(pct float64) (DiscountTier, bool) {
	for _, t := range p.DiscountTiers {
		if pct <= t.UpTo+1e-9 {
			return t, true
		}
	}
	return DiscountTier{}, false
}

// MaxDiscount is the largest discount the tiers allow.
func (p Policy) MaxDiscount() float64 {
	if len(p.DiscountTiers) == 0 {
		return 0
	}
	return p.DiscountTiers[len(p.DiscountTiers)-1].UpTo
}

// RolesMeet reports whether a set of role names satisfies a level.
func RolesMeet(roles []string, level string) bool {
	switch level {
	case LevelNone, LevelAny, "":
		return true
	case LevelBranchManager:
		return authn.HasRole(roles, "branch manager") || authn.HasRole(roles, "merchant admin")
	case LevelMerchantAdmin:
		return authn.HasRole(roles, "merchant admin")
	}
	return false
}

func rolesOf(ctx context.Context, tx pgx.Tx, userID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT r.name FROM roles r JOIN user_roles ur ON ur.role_id = r.id WHERE ur.user_id::text = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		roles = append(roles, n)
	}
	return roles, rows.Err()
}

// UserMeets reports whether a user (by id) holds a role at the level.
func UserMeets(ctx context.Context, tx pgx.Tx, userID, level string) (bool, error) {
	roles, err := rolesOf(ctx, tx, userID)
	if err != nil {
		return false, err
	}
	return RolesMeet(roles, level), nil
}

// Approve checks an inline approval: the user with this employee code (or,
// when code is empty, this user id), their PIN, a role meeting level, and
// branchID inside their own data scope (skipped when branchID is ""). It
// returns the approver's user id, or "" when any of those fails: one answer
// for all, so a code can't be probed.
func Approve(ctx context.Context, tx pgx.Tx, code, userID, pin, level, branchID string) (string, error) {
	code = strings.TrimSpace(code)
	if pin == "" || (code == "" && userID == "") {
		return "", nil
	}
	var id string
	var pinHash *string
	var err error
	if code != "" {
		err = tx.QueryRow(ctx, `SELECT id, pin_hash FROM users WHERE employee_code = $1 AND status = 'active'`, code).Scan(&id, &pinHash)
	} else {
		err = tx.QueryRow(ctx, `SELECT id, pin_hash FROM users WHERE id::text = $1 AND status = 'active'`, userID).Scan(&id, &pinHash)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	if pinHash == nil || bcrypt.CompareHashAndPassword([]byte(*pinHash), []byte(pin)) != nil {
		return "", nil
	}
	ok, err := UserMeets(ctx, tx, id, level)
	if err != nil || !ok {
		return "", err
	}
	if branchID != "" {
		scope, err := authn.ResolveScope(ctx, tx, id, "")
		if err != nil {
			return "", err
		}
		if !scope.Allows(branchID) {
			return "", nil
		}
	}
	return id, nil
}
