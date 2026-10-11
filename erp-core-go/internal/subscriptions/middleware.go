package subscriptions

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// featureRoutes: which feature a path (below /api/v1) needs. Only
// management and reporting surfaces are gated; nothing a cashier needs to
// finish a sale already in progress is.
var featureRoutes = []struct {
	re      *regexp.Regexp
	feature string
}{
	{regexp.MustCompile(`^/gst/`), FeatureGSTCompliance},
	{regexp.MustCompile(`^/sales/orders/[^/]+/(e-invoice|e-way-bill)`), FeatureGSTCompliance},
	{regexp.MustCompile(`^/ai/`), FeatureAdvancedReports},
	{regexp.MustCompile(`^/report-builder/`), FeatureCustomReportBuilder},
	{regexp.MustCompile(`^/loyalty/`), FeatureLoyaltyCRM},
	{regexp.MustCompile(`^/sales/orders/[^/]+/loyalty/`), FeatureLoyaltyCRM},
	{regexp.MustCompile(`^/payroll/`), FeaturePayroll},
	{regexp.MustCompile(`^/hr/(statutory-config|employees/[^/]+/salary-structure)`), FeaturePayroll},
}

// FeatureFor returns the feature a path needs, or "".
func FeatureFor(path string) string {
	for _, fr := range featureRoutes {
		if fr.re.MatchString(path) {
			return fr.feature
		}
	}
	return ""
}

// retentionRoutes: analytical reports whose reach the plan's retention
// limits. Invoices, sales, purchases, ledgers, GST and the audit log are
// statutory records and are deliberately not here: every plan reads and
// exports them in full.
var retentionRoutes = []string{"/reports/", "/ai/", "/cash-reconciliation/history", "/inventory/reconciliation/history"}

var dateParams = []string{"date", "from", "from_date", "start_date", "start"}

// writesWhileReadOnly: the few writes an expired merchant still needs:
// signing out, paying or changing plan, and uploading offline sales made
// before the expiry (sync decides per sale, see AllowsOfflineSale).
var writesWhileReadOnly = []string{"/auth/logout", "/subscription/", "/sync/push"}

// Middleware runs after RequireAuth (and BranchScopeMiddleware) on every
// tenant route: it refuses platform tokens, attaches the entitlement, and
// enforces read-only expiry, plan features and the retention horizon.
func Middleware(database *db.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := authn.FromContext(r.Context())
			if !ok || claims.TenantID == "" {
				httpx.Error(w, http.StatusUnauthorized, "INVALID_TOKEN", "this token isn't for a merchant account")
				return
			}
			e, err := For(r.Context(), database, claims.TenantID)
			if err != nil {
				httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the subscription")
				return
			}
			path := strings.TrimPrefix(r.URL.Path, "/api/v1")
			read := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions

			if e.ReadOnly && !read && !hasPrefix(path, writesWhileReadOnly) {
				httpx.ErrorWithDetails(w, http.StatusPaymentRequired, "SUBSCRIPTION_EXPIRED",
					"the subscription has expired, so the account is read-only: everything can still be viewed and exported. Renew under Subscription to continue",
					map[string]any{"plan": e.Plan.Code, "status": e.Status, "read_only_since": e.ReadOnlySince})
				return
			}
			if f := FeatureFor(path); f != "" && !e.Plan.Has(f) {
				writeFeatureError(r.Context(), w, database, e, f)
				return
			}
			if read && hasPrefix(path, retentionRoutes) {
				if h := e.RetentionHorizon(time.Now()); !h.IsZero() {
					for _, p := range dateParams {
						v := r.URL.Query().Get(p)
						if len(v) < 10 {
							continue
						}
						if d, err := time.ParseInLocation("2006-01-02", v[:10], IST); err == nil && d.Before(h) {
							httpx.ErrorWithDetails(w, http.StatusForbidden, "RETENTION_LIMIT",
								"the "+e.Plan.Name+" plan's reports reach back to "+h.Format("2 Jan 2006")+
									". Invoices, ledgers and GST returns are still available in full",
								map[string]any{"earliest": h.Format("2006-01-02"), "plan": e.Plan.Code, "retention_days": e.Plan.RetentionDays})
							return
						}
					}
				}
			}
			next.ServeHTTP(w, r.WithContext(withEntitlement(r.Context(), e)))
		})
	}
}

func hasPrefix(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func writeFeatureError(ctx context.Context, w http.ResponseWriter, database *db.DB, e Entitlement, feature string) {
	upgrade := cheapestWith(ctx, database, feature)
	msg := "this isn't part of the " + e.Plan.Name + " plan"
	if upgrade != nil {
		msg += "; it comes with " + upgrade.Name
	}
	details := map[string]any{"feature": feature, "plan": e.Plan.Code}
	if upgrade != nil {
		details["available_in"] = upgrade.Code
	}
	httpx.ErrorWithDetails(w, http.StatusForbidden, "FEATURE_NOT_IN_PLAN", msg, details)
}

var (
	plansMu    sync.Mutex
	plansCache []Plan
	plansAt    time.Time
)

// activePlans: the catalogue, cached like entitlements (Invalidate clears it).
func activePlans(ctx context.Context, database *db.DB) []Plan {
	plansMu.Lock()
	defer plansMu.Unlock()
	if plansCache != nil && time.Since(plansAt) < cacheTTL {
		return plansCache
	}
	ps, err := ListPlans(ctx, database.Pool, true)
	if err != nil {
		return plansCache
	}
	plansCache, plansAt = ps, time.Now()
	return ps
}

func cheapestWith(ctx context.Context, database *db.DB, feature string) *Plan {
	for _, p := range activePlans(ctx, database) { // ordered by sort_order
		if p.Has(feature) {
			return &p
		}
	}
	return nil
}

func cheapestAbove(ctx context.Context, database *db.DB, kind string, used int64) *Plan {
	for _, p := range activePlans(ctx, database) {
		if l := p.Limit(kind); l == nil || *l > used {
			return &p
		}
	}
	return nil
}

// AllowsOfflineSale: an offline sale reaches the server after it happened.
// A read-only merchant's sales made before the account became read-only
// are still accepted (the sale is real); later ones are refused.
func (e Entitlement) AllowsOfflineSale(deviceCreatedAt time.Time) bool {
	return !e.ReadOnly || e.ReadOnlySince == nil || deviceCreatedAt.Before(*e.ReadOnlySince)
}
