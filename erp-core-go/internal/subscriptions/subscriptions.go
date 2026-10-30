// Package subscriptions enforces FRD §19's plans (phased_roadmap.md
// Phase 16; migrations/046_subscriptions.sql): what a merchant's plan
// includes (features), how much of it they may create (limits), how far
// back reports reach (retention), and the subscription lifecycle
// (trial, active, grace, expired). Expired means read-only, never
// deletion: the merchant can still sign in, read, export and pay.
//
// The effective status is computed from the stored dates on every read
// (Effective), so a merchant whose period ended a minute ago is in grace
// now, not when the hourly sweeper next runs. The sweeper persists the
// transitions and issues renewal invoices.
package subscriptions

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
)

// Feature codes. A plan lists the ones it includes (plans.features).
const (
	FeatureBasicReports        = "basic_reports"
	FeatureGSTCompliance       = "gst_compliance"
	FeatureAdvancedReports     = "advanced_reports"
	FeatureCustomReportBuilder = "custom_report_builder"
	FeatureLoyaltyCRM          = "loyalty_crm"
	FeatureAPIAccess           = "api_access"
	FeaturePayroll             = "payroll"
	FeatureMultiCurrency       = "multi_currency"
	FeatureWhiteLabel          = "white_label"
	FeatureCustomIntegrations  = "custom_integrations"
	FeaturePriceSuggestion     = "price_suggestion" // migrations/080
)

// AllFeatures is every feature code the console may put on a plan.
var AllFeatures = []string{FeatureBasicReports, FeatureGSTCompliance, FeatureAdvancedReports, FeatureCustomReportBuilder,
	FeatureLoyaltyCRM, FeatureAPIAccess, FeaturePayroll, FeatureMultiCurrency, FeatureWhiteLabel, FeatureCustomIntegrations, FeaturePriceSuggestion}

// Limit kinds (keys of plans.limits).
const (
	LimitBranches       = "branches"
	LimitPOSTerminals   = "pos_terminals"
	LimitProducts       = "products"
	LimitTransactionsPM = "transactions_per_month"
)

var AllLimits = []string{LimitBranches, LimitPOSTerminals, LimitProducts, LimitTransactionsPM}

// Statuses.
const (
	StatusTrial     = "trial"
	StatusActive    = "active"
	StatusGrace     = "grace"
	StatusExpired   = "expired"
	StatusCancelled = "cancelled"
)

type Plan struct {
	Code             string            `json:"code"`
	Name             string            `json:"name"`
	PricePerPOSMonth string            `json:"price_per_pos_month"`
	Limits           map[string]*int64 `json:"limits"` // nil value or missing key = unlimited
	Features         []string          `json:"features"`
	RetentionDays    *int              `json:"retention_days"` // nil = lifetime
	TrialDays        int               `json:"trial_days"`
	GraceDays        int               `json:"grace_days"`
	SortOrder        int               `json:"sort_order"`
	Active           bool              `json:"active"`
}

func (p Plan) Has(feature string) bool {
	for _, f := range p.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// Limit returns the plan's limit for kind, or nil when unlimited.
func (p Plan) Limit(kind string) *int64 {
	if p.Limits == nil {
		return nil
	}
	return p.Limits[kind]
}

// Free reports whether the plan costs nothing (no invoices, no expiry).
func (p Plan) Free() bool { return p.PricePerPOSMonth == "0" || p.PricePerPOSMonth == "0.00" }

// Stored is the subscriptions row.
type Stored struct {
	PlanCode         string     `json:"plan_code"`
	Status           string     `json:"status"`
	TrialEndsAt      *time.Time `json:"trial_ends_at"`
	TrialUsed        bool       `json:"trial_used"`
	CurrentPeriodEnd *time.Time `json:"current_period_end"`
	GraceEndsAt      *time.Time `json:"grace_ends_at"`
	ReadOnlySince    *time.Time `json:"read_only_since"`
	Notes            *string    `json:"notes"`
}

// Entitlement is what a merchant may do right now.
type Entitlement struct {
	Plan          Plan       `json:"plan"`
	Stored        Stored     `json:"-"`
	Status        string     `json:"status"` // effective
	TrialEndsAt   *time.Time `json:"trial_ends_at"`
	PeriodEnd     *time.Time `json:"current_period_end"`
	GraceEndsAt   *time.Time `json:"grace_ends_at"`
	ReadOnly      bool       `json:"read_only"`
	ReadOnlySince *time.Time `json:"read_only_since"`
	// Missing is true when the merchant has no subscriptions row (treated
	// as Freemium, active).
	Missing bool `json:"-"`
}

// Effective works out the status at now from the stored row and the plan's
// grace period:
//   - trial: until trial_ends_at, then grace for grace_days, then expired;
//   - active: until current_period_end (none = forever), then grace, then expired;
//   - grace: until grace_ends_at, then expired;
//   - expired / cancelled: read-only.
func Effective(plan Plan, s Stored, now time.Time) Entitlement {
	e := Entitlement{Plan: plan, Stored: s, Status: s.Status, TrialEndsAt: s.TrialEndsAt, PeriodEnd: s.CurrentPeriodEnd,
		GraceEndsAt: s.GraceEndsAt, ReadOnlySince: s.ReadOnlySince}
	grace := time.Duration(plan.GraceDays) * 24 * time.Hour
	lapse := func(end time.Time) {
		ge := end.Add(grace)
		if s.GraceEndsAt != nil {
			ge = *s.GraceEndsAt
		}
		e.GraceEndsAt = &ge
		if now.Before(ge) {
			e.Status = StatusGrace
			return
		}
		e.Status = StatusExpired
		e.ReadOnlySince = &ge
	}
	switch s.Status {
	case StatusTrial:
		if s.TrialEndsAt != nil && !now.Before(*s.TrialEndsAt) {
			lapse(*s.TrialEndsAt)
		}
	case StatusActive:
		if s.CurrentPeriodEnd != nil && !now.Before(*s.CurrentPeriodEnd) {
			lapse(*s.CurrentPeriodEnd)
		}
	case StatusGrace:
		if s.GraceEndsAt != nil && !now.Before(*s.GraceEndsAt) {
			e.Status = StatusExpired
			e.ReadOnlySince = s.GraceEndsAt
		}
	}
	if e.Status == StatusExpired || e.Status == StatusCancelled {
		e.ReadOnly = true
		if e.ReadOnlySince == nil {
			t := now
			if s.ReadOnlySince != nil {
				t = *s.ReadOnlySince
			}
			e.ReadOnlySince = &t
		}
	} else {
		e.ReadOnlySince = nil
	}
	return e
}

const planColumns = `code, name, price_per_pos_month::text, limits, features, retention_days, trial_days, grace_days, sort_order, active`

func scanPlan(row pgx.Row) (Plan, error) {
	var p Plan
	var limits []byte
	if err := row.Scan(&p.Code, &p.Name, &p.PricePerPOSMonth, &limits, &p.Features, &p.RetentionDays, &p.TrialDays, &p.GraceDays,
		&p.SortOrder, &p.Active); err != nil {
		return Plan{}, err
	}
	p.Limits = map[string]*int64{}
	if err := json.Unmarshal(limits, &p.Limits); err != nil {
		return Plan{}, err
	}
	if p.Features == nil {
		p.Features = []string{}
	}
	return p, nil
}

// Querier is a pool or a transaction: plans have no RLS.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func LoadPlan(ctx context.Context, q Querier, code string) (Plan, error) {
	return scanPlan(q.QueryRow(ctx, `SELECT `+planColumns+` FROM plans WHERE code = $1`, code))
}

func ListPlans(ctx context.Context, q Querier, activeOnly bool) ([]Plan, error) {
	rows, err := q.Query(ctx, `SELECT `+planColumns+` FROM plans WHERE $1::bool = false OR active ORDER BY sort_order, code`, activeOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func loadStored(ctx context.Context, tx pgx.Tx) (Stored, bool, error) {
	var s Stored
	err := tx.QueryRow(ctx, `
		SELECT plan_code, status, trial_ends_at, trial_used, current_period_end, grace_ends_at, read_only_since, notes
		FROM subscriptions`).Scan(&s.PlanCode, &s.Status, &s.TrialEndsAt, &s.TrialUsed, &s.CurrentPeriodEnd, &s.GraceEndsAt,
		&s.ReadOnlySince, &s.Notes)
	if errors.Is(err, pgx.ErrNoRows) {
		return Stored{PlanCode: "freemium", Status: StatusActive}, false, nil
	}
	return s, err == nil, err
}

// Load reads the tenant's entitlement inside its WithTenant transaction.
func Load(ctx context.Context, tx pgx.Tx) (Entitlement, error) {
	s, found, err := loadStored(ctx, tx)
	if err != nil {
		return Entitlement{}, err
	}
	plan, err := LoadPlan(ctx, tx, s.PlanCode)
	if err != nil {
		return Entitlement{}, err
	}
	e := Effective(plan, s, time.Now())
	e.Missing = !found
	return e, nil
}

// --------------------------------------------------------------- cache

const cacheTTL = 30 * time.Second

type cached struct {
	e  Entitlement
	at time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cached{}
)

// Invalidate drops a merchant's cached entitlement ("" = every merchant):
// call after any change to a subscription or plan.
func Invalidate(merchantID string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if merchantID == "" {
		cache = map[string]cached{}
		plansMu.Lock()
		plansCache = nil
		plansMu.Unlock()
		return
	}
	delete(cache, merchantID)
}

// For returns the merchant's entitlement, cached for 30 s. The cache holds
// the stored row and plan; the status is recomputed at every call, so a
// period that ends between cache fills takes effect at once.
func For(ctx context.Context, database *db.DB, merchantID string) (Entitlement, error) {
	cacheMu.Lock()
	c, ok := cache[merchantID]
	cacheMu.Unlock()
	if ok && time.Since(c.at) < cacheTTL {
		e := Effective(c.e.Plan, c.e.Stored, time.Now())
		e.Missing = c.e.Missing
		return e, nil
	}
	var e Entitlement
	err := database.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		e, err = Load(ctx, tx)
		return err
	})
	if err != nil {
		return Entitlement{}, err
	}
	cacheMu.Lock()
	cache[merchantID] = cached{e: e, at: time.Now()}
	cacheMu.Unlock()
	return e, nil
}

type ctxKey struct{}

func withEntitlement(ctx context.Context, e Entitlement) context.Context {
	return context.WithValue(ctx, ctxKey{}, e)
}

// FromContext returns the request's entitlement (set by Middleware).
func FromContext(ctx context.Context) (Entitlement, bool) {
	e, ok := ctx.Value(ctxKey{}).(Entitlement)
	return e, ok
}

// RetentionHorizon is the earliest date reports reach on this plan, or
// the zero time when unlimited. Midnight IST, days back from today.
func (e Entitlement) RetentionHorizon(now time.Time) time.Time {
	if e.Plan.RetentionDays == nil {
		return time.Time{}
	}
	ist := now.In(IST)
	today := time.Date(ist.Year(), ist.Month(), ist.Day(), 0, 0, 0, 0, IST)
	return today.AddDate(0, 0, -*e.Plan.RetentionDays)
}

// IST: plan months and retention days are counted in India time, like
// the rest of the merchant-facing dates in this codebase.
var IST = time.FixedZone("IST", 5*3600+1800)
