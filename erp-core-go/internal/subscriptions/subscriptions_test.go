package subscriptions

import (
	"testing"
	"time"
)

func at(t time.Time) *time.Time { return &t }

func TestEffectiveLifecycle(t *testing.T) {
	plan := Plan{Code: "starter", PricePerPOSMonth: "999.00", GraceDays: 7}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name     string
		s        Stored
		status   string
		readOnly bool
	}{
		{"active, no end", Stored{Status: StatusActive}, StatusActive, false},
		{"active, before end", Stored{Status: StatusActive, CurrentPeriodEnd: at(now.Add(day))}, StatusActive, false},
		{"active, just past end -> grace", Stored{Status: StatusActive, CurrentPeriodEnd: at(now.Add(-day))}, StatusGrace, false},
		{"active, past end + grace -> expired", Stored{Status: StatusActive, CurrentPeriodEnd: at(now.Add(-8 * day))}, StatusExpired, true},
		{"trial running", Stored{Status: StatusTrial, TrialEndsAt: at(now.Add(day))}, StatusTrial, false},
		{"trial over -> grace", Stored{Status: StatusTrial, TrialEndsAt: at(now.Add(-day))}, StatusGrace, false},
		{"trial long over -> expired", Stored{Status: StatusTrial, TrialEndsAt: at(now.Add(-30 * day))}, StatusExpired, true},
		{"stored grace still running", Stored{Status: StatusGrace, GraceEndsAt: at(now.Add(day))}, StatusGrace, false},
		{"stored grace over", Stored{Status: StatusGrace, GraceEndsAt: at(now.Add(-time.Minute))}, StatusExpired, true},
		{"stored expired", Stored{Status: StatusExpired}, StatusExpired, true},
		{"cancelled", Stored{Status: StatusCancelled}, StatusCancelled, true},
	}
	for _, c := range cases {
		e := Effective(plan, c.s, now)
		if e.Status != c.status || e.ReadOnly != c.readOnly {
			t.Errorf("%s: got %s read_only=%v", c.name, e.Status, e.ReadOnly)
		}
		if e.ReadOnly != (e.ReadOnlySince != nil) {
			t.Errorf("%s: read_only_since %v with read_only %v", c.name, e.ReadOnlySince, e.ReadOnly)
		}
	}
	// Expiry is the end of grace, so an offline sale from before it is kept.
	e := Effective(plan, Stored{Status: StatusActive, CurrentPeriodEnd: at(now.Add(-8 * day))}, now)
	if !e.ReadOnlySince.Equal(now.Add(-day)) {
		t.Errorf("read_only_since = %v, want end of grace", e.ReadOnlySince)
	}
	if !e.AllowsOfflineSale(now.Add(-2*day)) || e.AllowsOfflineSale(now.Add(-time.Hour)) {
		t.Error("offline sales: before expiry kept, after refused")
	}
}

func TestFeatureFor(t *testing.T) {
	for path, want := range map[string]string{
		"/gst/gstr1":                       FeatureGSTCompliance,
		"/sales/orders/abc/e-invoice":      FeatureGSTCompliance,
		"/sales/orders/abc/loyalty/redeem": FeatureLoyaltyCRM,
		"/ai/anomalies":                    FeatureAdvancedReports,
		"/payroll/runs":                    FeaturePayroll,
		"/hr/statutory-config":             FeaturePayroll,
		"/hr/employees/x/salary-structure": FeaturePayroll,
		"/hr/employees":                    "",
		"/sales/orders/abc/checkout":       "",
		"/reports/daily-sales":             "",
	} {
		if got := FeatureFor(path); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}

func TestRetentionHorizon(t *testing.T) {
	days := 30
	e := Entitlement{Plan: Plan{RetentionDays: &days}}
	now := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC) // 01:30 on the 12th in IST
	if got := e.RetentionHorizon(now).Format("2006-01-02"); got != "2026-09-12" {
		t.Errorf("horizon %s", got)
	}
	if !(Entitlement{Plan: Plan{}}).RetentionHorizon(now).IsZero() {
		t.Error("lifetime retention should have no horizon")
	}
}
