package pricing

import "testing"

func TestApplyRule(t *testing.T) {
	cases := []struct {
		name      string
		base, mrp float64
		ruleType  string
		value     float64
		want      float64
	}{
		{"percent off", 1000, 1200, "percent", -20, 800},
		{"percent up", 1000, 1200, "percent", 10, 1100},
		{"percent up capped at MRP", 1000, 1050, "percent", 10, 1050},
		{"no MRP means no cap", 1000, 0, "percent", 10, 1100},
		{"fixed price", 1000, 1200, "fixed_price", 899, 899},
		{"fixed price above MRP is capped", 1000, 1200, "fixed_price", 1500, 1200},
		{"100% off floors at zero", 1000, 1200, "percent", -100, 0},
		{"rounds to the paisa", 99.99, 0, "percent", -15, 84.99}, // 84.9915
		{"rounds half up", 10.05, 0, "percent", -50, 5.03},       // 5.025
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := applyRule(c.base, c.mrp, c.ruleType, c.value); got != c.want {
				t.Errorf("applyRule(%v, %v, %q, %v) = %v, want %v", c.base, c.mrp, c.ruleType, c.value, got, c.want)
			}
		})
	}
}

func TestValidatePriceRule(t *testing.T) {
	ok := priceRuleRequest{Name: "Happy hour", CategoryID: "c", RuleType: "percent", Value: -20, TimeStart: "14:00", TimeEnd: "17:00", DaysOfWeek: []int{1, 2}}
	if msg := validatePriceRule(ok); msg != "" {
		t.Fatalf("valid rule rejected: %s", msg)
	}
	bad := map[string]priceRuleRequest{
		"no scope":                {Name: "x", RuleType: "percent", Value: -5},
		"both scopes":             {Name: "x", VariantID: "v", CategoryID: "c", RuleType: "percent", Value: -5},
		"fixed price on category": {Name: "x", CategoryID: "c", RuleType: "fixed_price", Value: 10},
		"zero percent":            {Name: "x", CategoryID: "c", RuleType: "percent", Value: 0},
		"below -100%":             {Name: "x", CategoryID: "c", RuleType: "percent", Value: -101},
		"half a time window":      {Name: "x", CategoryID: "c", RuleType: "percent", Value: -5, TimeStart: "14:00"},
		"empty time window":       {Name: "x", CategoryID: "c", RuleType: "percent", Value: -5, TimeStart: "14:00", TimeEnd: "14:00"},
		"bad day":                 {Name: "x", CategoryID: "c", RuleType: "percent", Value: -5, DaysOfWeek: []int{7}},
		"inverted season":         {Name: "x", CategoryID: "c", RuleType: "percent", Value: -5, StartsAt: "2026-12-31T00:00:00Z", EndsAt: "2026-12-01T00:00:00Z"},
	}
	for name, req := range bad {
		if validatePriceRule(req) == "" {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	// A window crossing midnight is legitimate (a late-night special).
	overnight := priceRuleRequest{Name: "Late night", CategoryID: "c", RuleType: "percent", Value: -10, TimeStart: "22:00", TimeEnd: "02:00"}
	if msg := validatePriceRule(overnight); msg != "" {
		t.Errorf("overnight window rejected: %s", msg)
	}
}
