package subscriptions

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// systemAccounts: the chart of accounts every merchant starts with — the
// accounts the automatic postings use (migrations 007, 016, 017, 040
// seeded these for the merchants that existed then).
var systemAccounts = [][3]string{
	{"1001", "Cash", "asset"}, {"1002", "Bank", "asset"}, {"1003", "Card Clearing", "asset"}, {"1004", "UPI Clearing", "asset"},
	{"1005", "GST Input Credit", "asset"}, {"1100", "Accounts Receivable", "asset"}, {"1200", "Inventory Asset", "asset"},
	{"2001", "GST Output Payable", "liability"}, {"2002", "Accounts Payable", "liability"},
	{"4001", "Sales Revenue", "income"}, {"4002", "Other Income", "income"},
	{"5001", "Inventory Shrinkage", "expense"}, {"5002", "Cash Over/Short", "expense"}, {"5003", "Payment Gateway Fees", "expense"},
	{"5004", "Petty Cash Expenses", "expense"}, {"5005", "Round Off", "expense"},
	{"1150", "Employee Loans & Advances", "asset"}, {"2150", "Salaries Payable", "liability"}, {"4050", "Interest Income", "income"},
	// migrations/074: expenses and partners' capital.
	{"1300", "Deposits & Advances", "asset"}, {"1400", "Furniture & Fixtures", "asset"}, {"2200", "Expenses Payable", "liability"},
	{"3000", "Partners' Capital", "equity"}, {"5100", "Rent", "expense"}, {"5110", "Advertising & Promotion", "expense"},
	{"5120", "Freight & Transport", "expense"}, {"5130", "Electricity", "expense"}, {"5140", "Repairs & Maintenance", "expense"},
	{"5150", "Telephone & Internet", "expense"}, {"5190", "General Expenses", "expense"},
}

// systemExpenseHeads: the expense heads a new merchant starts with
// (migrations/074 seeded the same for existing ones): head → account code.
var systemExpenseHeads = [][2]string{
	{"Shop Advance", "1300"}, {"Interior", "1400"}, {"Shop Rent", "5100"}, {"Flex & Banner", "5110"}, {"Transport", "5120"},
	{"Electricity", "5130"}, {"Repairs", "5140"}, {"Phone & Internet", "5150"}, {"Other", "5190"},
}

var merchantCodeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,39}$`)

type provisionRequest struct {
	Code       string `json:"code"`
	LegalName  string `json:"legal_name"`
	TradeName  string `json:"trade_name"`
	GSTIN      string `json:"gstin"`
	PlanCode   string `json:"plan_code"`
	StartTrial bool   `json:"start_trial"`
	Branch     struct {
		Name string `json:"name"`
		Code string `json:"code"`
	} `json:"branch"`
	Admin struct {
		Name         string `json:"name"`
		Email        string `json:"email"`
		EmployeeCode string `json:"employee_code"`
		Password     string `json:"password"`
	} `json:"admin"`
	Reason string `json:"reason"`
}

// CreateMerchant: POST /platform/merchants — onboard a merchant in one
// transaction: the merchant, its system chart of accounts, its system
// roles (Merchant Admin with every permission; Branch Manager and POS User
// from role_templates), the first branch with its virtual terminals, the
// owner's Merchant Admin account, and the subscription (the plan's trial
// when start_trial, otherwise active with no expiry, e.g. a sales-led deal).
func (h *PlatformHandler) CreateMerchant(w http.ResponseWriter, r *http.Request) {
	staff, _ := authn.FromContext(r.Context())
	var req provisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	req.Code = strings.ToLower(strings.TrimSpace(req.Code))
	req.Admin.Email = strings.ToLower(strings.TrimSpace(req.Admin.Email))
	bad := func(msg string) { httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg) }
	switch {
	case !merchantCodeRe.MatchString(req.Code):
		bad("code: 3 to 40 lower-case letters, digits and hyphens")
		return
	case strings.TrimSpace(req.LegalName) == "":
		bad("legal_name is required")
		return
	case strings.TrimSpace(req.Branch.Name) == "" || strings.TrimSpace(req.Branch.Code) == "":
		bad("the first branch needs a name and code")
		return
	case strings.TrimSpace(req.Admin.Name) == "" || !strings.Contains(req.Admin.Email, "@"):
		bad("the owner needs a name and email")
		return
	case len(req.Admin.Password) < 8 || len(req.Admin.Password) > 72:
		bad("the owner's first password must be 8 to 72 characters")
		return
	case strings.TrimSpace(req.Reason) == "":
		bad("a reason is required")
		return
	}
	if req.PlanCode == "" {
		req.PlanCode = "freemium"
	}
	plan, err := LoadPlan(r.Context(), h.DB.Pool, req.PlanCode)
	if err != nil || !plan.Active {
		bad("unknown or retired plan_code")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Admin.Password), bcrypt.DefaultCost)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not hash the password")
		return
	}

	var merchantID string
	err = h.DB.Pool.QueryRow(r.Context(), `
		INSERT INTO merchants (code, legal_name, trade_name, gstin, subscription_tier)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), CASE WHEN $5 IN ('freemium','starter','professional','enterprise') THEN $5 ELSE 'freemium' END)
		RETURNING id`, req.Code, strings.TrimSpace(req.LegalName), strings.TrimSpace(req.TradeName), strings.TrimSpace(req.GSTIN), plan.Code).
		Scan(&merchantID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		httpx.Error(w, http.StatusConflict, "MERCHANT_CODE_TAKEN", "that merchant code is already in use")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create the merchant")
		return
	}
	var adminID, branchID string
	err = h.DB.WithTenant(r.Context(), merchantID, func(ctx context.Context, tx pgx.Tx) error {
		for _, a := range systemAccounts {
			if _, err := tx.Exec(ctx, `INSERT INTO chart_of_accounts (merchant_id, code, name, account_type, is_system)
				VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, true)`, a[0], a[1], a[2]); err != nil {
				return err
			}
		}
		for _, h := range systemExpenseHeads {
			if _, err := tx.Exec(ctx, `INSERT INTO expense_heads (merchant_id, name, account_id)
				SELECT current_setting('app.tenant_id')::uuid, $1, id FROM chart_of_accounts WHERE code = $2`, h[0], h[1]); err != nil {
				return err
			}
		}
		roleIDs := map[string]string{}
		for _, role := range []struct{ name, scope string }{{"Merchant Admin", "all"}, {"Branch Manager", "branch"}, {"POS User", "all"}} {
			var id string
			if err := tx.QueryRow(ctx, `INSERT INTO roles (merchant_id, name, data_scope) VALUES (current_setting('app.tenant_id')::uuid, $1, $2) RETURNING id`,
				role.name, role.scope).Scan(&id); err != nil {
				return err
			}
			roleIDs[role.name] = id
		}
		if _, err := tx.Exec(ctx, `INSERT INTO role_permissions (role_id, permission_id) SELECT $1, id FROM permissions`, roleIDs["Merchant Admin"]); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO role_permissions (role_id, permission_id)
			SELECT CASE t.role_name WHEN 'Branch Manager' THEN $1::uuid ELSE $2::uuid END, p.id
			FROM role_templates t JOIN permissions p ON p.code = t.permission_code`, roleIDs["Branch Manager"], roleIDs["POS User"]); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO branches (merchant_id, name, code) VALUES (current_setting('app.tenant_id')::uuid, $1, $2) RETURNING id`,
			strings.TrimSpace(req.Branch.Name), strings.TrimSpace(req.Branch.Code)).Scan(&branchID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO pos_terminals (merchant_id, branch_id, name, channel, status) VALUES
			(current_setting('app.tenant_id')::uuid, $1, 'Online Storefront', 'online', 'active'),
			(current_setting('app.tenant_id')::uuid, $1, 'Back Office', 'backoffice', 'active')`, branchID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO users (merchant_id, branch_id, name, email, employee_code, password_hash, status)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, NULLIF($4, ''), $5, 'active') RETURNING id`,
			branchID, strings.TrimSpace(req.Admin.Name), req.Admin.Email, strings.TrimSpace(req.Admin.EmployeeCode), string(hash)).Scan(&adminID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`, adminID, roleIDs["Merchant Admin"]); err != nil {
			return err
		}
		status, trialEnds := StatusActive, (*time.Time)(nil)
		if req.StartTrial && plan.TrialDays > 0 {
			t := time.Now().AddDate(0, 0, plan.TrialDays)
			status, trialEnds = StatusTrial, &t
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subscriptions (merchant_id, plan_code, status, trial_ends_at, trial_used, updated_by, notes)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3::timestamptz, $3::timestamptz IS NOT NULL, $4, $5)`,
			plan.Code, status, trialEnds, staff.UserID, "Onboarded: "+req.Reason); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "merchants", EntityID: merchantID, Action: "create", PerformedBy: staff.UserID,
			After:  map[string]any{"code": req.Code, "plan": plan.Code, "status": status, "branch_id": branchID, "admin_user_id": adminID},
			Reason: "platform staff: " + req.Reason})
	})
	if err != nil {
		log.Printf("platform: provision merchant %s: %v", req.Code, err)
		// Undo the merchant row so the code can be used again.
		_, _ = h.DB.Pool.Exec(r.Context(), `DELETE FROM merchants WHERE id = $1`, merchantID)
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			httpx.Error(w, http.StatusConflict, "INVALID_REQUEST", "duplicate value: "+pgErr.ConstraintName)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not set up the merchant")
		return
	}
	_ = h.logPlatform(r.Context(), staff.UserID, "merchant.create", "merchant", merchantID, nil,
		map[string]any{"code": req.Code, "plan": plan.Code, "trial": req.StartTrial}, req.Reason)
	httpx.JSON(w, http.StatusCreated, map[string]any{"merchant_id": merchantID, "code": req.Code, "branch_id": branchID, "admin_user_id": adminID,
		"plan": plan.Code})
}
