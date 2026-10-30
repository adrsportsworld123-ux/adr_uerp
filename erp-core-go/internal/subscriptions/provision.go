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
	// migrations/078: farm work-in-progress, gains and losses.
	{"1500", "Livestock & Farm Work-in-Progress", "asset"}, {"4100", "Farm Gains", "income"}, {"5200", "Farm Losses & Mortality", "expense"},
}

// systemExpenseHeads: the expense heads a new merchant starts with
// (migrations/074 seeded the same for existing ones): head → account code.
var systemExpenseHeads = [][2]string{
	{"Shop Advance", "1300"}, {"Interior", "1400"}, {"Shop Rent", "5100"}, {"Flex & Banner", "5110"}, {"Transport", "5120"},
	{"Electricity", "5130"}, {"Repairs", "5140"}, {"Phone & Internet", "5150"}, {"Other", "5190"},
}

// systemLeaveTypes: the leave types a new merchant starts with
// (migrations/075 seeded the same for existing ones). Quota 0 = no limit.
var systemLeaveTypes = []struct {
	code, name string
	paid       bool
	quota      float64
}{{"CL", "Casual leave", true, 12}, {"SL", "Sick leave", true, 6}, {"LOP", "Loss of pay", false, 0}}

// systemUnits: the units of measure a new merchant starts with
// (migrations/076 seeded the same for existing ones): code, name, symbol,
// kind, factor (0 = none, a pack unit), decimals.
var systemUnits = []struct {
	code, name, symbol, kind string
	factor                   float64
	decimals                 int
}{
	{"PCS", "Pieces", "pcs", "count", 1, 0}, {"DOZ", "Dozen", "dz", "count", 12, 0}, {"PAIR", "Pair", "pr", "count", 2, 0},
	{"KG", "Kilogram", "kg", "weight", 1, 3}, {"G", "Gram", "g", "weight", 0.001, 0}, {"QTL", "Quintal", "qtl", "weight", 100, 3},
	{"L", "Litre", "L", "volume", 1, 3}, {"ML", "Millilitre", "ml", "volume", 0.001, 0},
	{"M", "Metre", "m", "length", 1, 2}, {"CM", "Centimetre", "cm", "length", 0.01, 0}, {"SQFT", "Square foot", "sq ft", "area", 0.092903, 2},
	{"BOX", "Box", "box", "pack", 0, 0}, {"BAG", "Bag", "bag", "pack", 0, 0}, {"TRAY", "Tray", "tray", "pack", 0, 0},
	{"PKT", "Packet", "pkt", "pack", 0, 0}, {"BTL", "Bottle", "btl", "pack", 0, 0}, {"CRATE", "Crate", "crate", "pack", 0, 0}, {"CTN", "Carton", "ctn", "pack", 0, 0},
}

// systemFarmSchedules: the health plans a new merchant starts with
// (migrations/078 and 079 seeded the same for existing ones): species,
// purpose ("" = any), name, then each item's day of age, kind, name,
// method and repeat (days, 0 = once).
type farmScheduleItem struct {
	day            int
	kind, name, by string
	repeat         int
}

var systemFarmSchedules = []struct {
	species, purpose, name string
	items                  []farmScheduleItem
}{
	{"chicken", "broiler", "Broiler (42 days)", []farmScheduleItem{
		{1, "vaccination", "Marek's disease (HVT)", "Injection, at the hatchery", 0}, {6, "vaccination", "Newcastle disease (F1 / Lasota)", "Eye or nasal drop", 0},
		{13, "vaccination", "Gumboro (IBD, intermediate)", "Drinking water", 0}, {21, "vaccination", "Newcastle disease booster (Lasota)", "Drinking water", 0},
		{26, "vaccination", "Gumboro booster (IBD)", "Drinking water", 0}}},
	{"chicken", "layer", "Layer (to point of lay)", []farmScheduleItem{
		{1, "vaccination", "Marek's disease (HVT)", "Injection, at the hatchery", 0}, {5, "vaccination", "Newcastle disease (F1 / Lasota)", "Eye or nasal drop", 0},
		{12, "vaccination", "Gumboro (IBD, intermediate)", "Drinking water", 0}, {21, "vaccination", "Newcastle disease booster (Lasota)", "Drinking water", 0},
		{26, "vaccination", "Gumboro booster (IBD)", "Drinking water", 0}, {35, "vaccination", "Newcastle disease (Lasota)", "Drinking water", 0},
		{49, "vaccination", "Fowl pox", "Wing web", 0}, {56, "vaccination", "Newcastle disease (R2B)", "Injection", 0}, {70, "vaccination", "Infectious coryza", "Injection", 0},
		{112, "vaccination", "Newcastle disease (R2B) booster", "Injection", 0}, {126, "vaccination", "EDS + ND + IB (killed, before lay)", "Injection", 0}}},
	{"goat", "", "Goat health", []farmScheduleItem{
		{60, "medicine", "Deworming", "Oral drench", 90}, {90, "vaccination", "Goat pox", "Injection", 365},
		{120, "vaccination", "PPR", "Injection", 1095}, {120, "vaccination", "Enterotoxaemia (ET)", "Injection", 365},
		{141, "vaccination", "Enterotoxaemia (ET) booster", "Injection", 0}, {120, "vaccination", "Foot and mouth disease (FMD)", "Injection", 180},
		{180, "vaccination", "Haemorrhagic septicaemia (HS)", "Injection", 365}}},
	{"cow", "", "Cattle health", []farmScheduleItem{
		{10, "medicine", "Deworming", "Oral", 90}, {90, "vaccination", "Theileriosis", "Injection", 0},
		{120, "vaccination", "Foot and mouth disease (FMD)", "Injection", 180}, {150, "vaccination", "FMD booster", "Injection", 0},
		{150, "vaccination", "Brucellosis (female calves, once)", "Injection", 0}, {120, "vaccination", "Lumpy skin disease (LSD)", "Injection", 365},
		{180, "vaccination", "Haemorrhagic septicaemia (HS)", "Injection", 365}, {180, "vaccination", "Black quarter (BQ)", "Injection", 365}}},
	{"pig", "", "Pig health", []farmScheduleItem{
		{3, "medicine", "Iron injection", "Injection", 0}, {7, "task", "Castration (male piglets)", "", 0},
		{42, "vaccination", "Classical swine fever (CSF)", "Injection", 365}, {45, "medicine", "Deworming", "Oral", 90},
		{60, "vaccination", "Foot and mouth disease (FMD)", "Injection", 180}}},
	{"rabbit", "", "Rabbit health", []farmScheduleItem{
		{30, "medicine", "Coccidiosis prevention", "Drinking water", 0}, {45, "medicine", "Deworming", "Oral", 90}}},
}

// systemFarmFeedPlans: the feeding plans a new merchant starts with
// (migrations/079 seeded the same): species, purpose, name, basis, then
// each item's days, feed, grams a head a day or % of body weight.
type farmFeedItem struct {
	from, to   int // to 0 = onwards
	feed       string
	grams, pct float64
}

var systemFarmFeedPlans = []struct {
	species, purpose, name, basis string
	items                         []farmFeedItem
}{
	{"chicken", "broiler", "Broiler feeding (42 days)", "per_head", []farmFeedItem{
		{0, 10, "Broiler pre-starter", 25, 0}, {11, 24, "Broiler starter", 85, 0}, {25, 0, "Broiler finisher", 160, 0}}},
	{"chicken", "layer", "Layer feeding", "per_head", []farmFeedItem{
		{0, 56, "Chick mash", 30, 0}, {57, 126, "Grower mash", 65, 0}, {127, 140, "Pre-layer mash", 90, 0}, {141, 0, "Layer mash", 110, 0}}},
	{"goat", "", "Goat feeding", "per_head", []farmFeedItem{
		{15, 90, "Kid creep feed", 100, 0}, {91, 365, "Concentrate", 250, 0}, {91, 365, "Green fodder", 2000, 0},
		{366, 0, "Concentrate", 400, 0}, {366, 0, "Green fodder", 4000, 0}, {366, 0, "Dry fodder", 1000, 0}}},
	{"cow", "dairy", "Dairy cattle feeding", "per_head", []farmFeedItem{
		{0, 90, "Calf starter", 500, 0}, {91, 365, "Concentrate", 1500, 0}, {91, 365, "Green fodder", 10000, 0},
		{366, 0, "Concentrate (cattle feed)", 4000, 0}, {366, 0, "Green fodder", 25000, 0}, {366, 0, "Dry fodder (straw)", 6000, 0}}},
	{"pig", "fattening", "Pig fattening", "per_head", []farmFeedItem{
		{0, 90, "Pig starter", 1000, 0}, {91, 140, "Pig grower", 1800, 0}, {141, 0, "Pig finisher", 2500, 0}}},
	{"rabbit", "", "Rabbit feeding", "per_head", []farmFeedItem{
		{30, 60, "Rabbit pellets", 80, 0}, {61, 90, "Rabbit pellets", 120, 0}, {91, 0, "Rabbit pellets", 150, 0}, {30, 0, "Green fodder", 200, 0}}},
	{"fish", "grow_out", "Fish grow-out (% of body weight)", "pct_biomass", []farmFeedItem{
		{0, 30, "Fish starter feed", 0, 5}, {31, 90, "Fish grower feed", 0, 3}, {91, 180, "Fish grower feed", 0, 2}, {181, 0, "Fish finisher feed", 0, 1.5}}},
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
		for _, fs := range systemFarmSchedules {
			var sid string
			if err := tx.QueryRow(ctx, `INSERT INTO farm_schedules (merchant_id, species, purpose, name, is_system)
				VALUES (current_setting('app.tenant_id')::uuid, $1, NULLIF($2, ''), $3, true) RETURNING id`, fs.species, fs.purpose, fs.name).Scan(&sid); err != nil {
				return err
			}
			for i, it := range fs.items {
				if _, err := tx.Exec(ctx, `INSERT INTO farm_schedule_items (merchant_id, schedule_id, day_of_age, kind, name, method, repeat_every_days, sort_order)
					VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, 0), $7)`, sid, it.day, it.kind, it.name, it.by, it.repeat, i+1); err != nil {
					return err
				}
			}
		}
		for _, fp := range systemFarmFeedPlans {
			var pid string
			if err := tx.QueryRow(ctx, `INSERT INTO farm_feed_plans (merchant_id, species, purpose, name, basis, is_system)
				VALUES (current_setting('app.tenant_id')::uuid, $1, NULLIF($2, ''), $3, $4, true) RETURNING id`, fp.species, fp.purpose, fp.name, fp.basis).Scan(&pid); err != nil {
				return err
			}
			for i, it := range fp.items {
				if _, err := tx.Exec(ctx, `INSERT INTO farm_feed_plan_items (merchant_id, plan_id, day_from, day_to, feed_name, grams_per_head, pct_body_weight, sort_order)
					VALUES (current_setting('app.tenant_id')::uuid, $1, $2, NULLIF($3, 0), $4, NULLIF($5, 0::numeric), NULLIF($6, 0::numeric), $7)`,
					pid, it.from, it.to, it.feed, it.grams, it.pct, i+1); err != nil {
					return err
				}
			}
		}
		for i, u := range systemUnits {
			if _, err := tx.Exec(ctx, `INSERT INTO units (merchant_id, code, name, symbol, kind, factor, decimals, is_system, sort_order)
				VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, NULLIF($5, 0::numeric), $6, true, $7)`,
				u.code, u.name, u.symbol, u.kind, u.factor, u.decimals, i+1); err != nil {
				return err
			}
		}
		for i, lt := range systemLeaveTypes {
			if _, err := tx.Exec(ctx, `INSERT INTO leave_types (merchant_id, code, name, paid, annual_quota, sort_order)
				VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, NULLIF($4, 0::numeric), $5)`, lt.code, lt.name, lt.paid, lt.quota, i+1); err != nil {
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
		// The first branch is a Retail shop (migrations/077); the merchant changes it in Branches.
		if _, err := tx.Exec(ctx, `INSERT INTO branch_business_types (merchant_id, branch_id, type_code)
			VALUES (current_setting('app.tenant_id')::uuid, $1, 'retail')`, branchID); err != nil {
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
