package pricing

import "testing"

func pct(v float64) *float64 { return &v }

// Rows of ADR Sports' pricing playbook (docs/ADR_SPORTS_Pricing_Strategy_
// and_Selling_Price_List.xlsx): cost / (1 − the category's margin),
// rounded the charm way, never above MRP. Its costs include GST, on
// GST-inclusive prices.
func TestSuggestMatchesThePlaybook(t *testing.T) {
	cases := []struct {
		cost, mrp, margin, want float64
		status                  string
	}{
		{52, 85, 15, 65, "ok"},
		{62, 90, 15, 75, "ok"},
		{30, 40, 18, 40, "ok"},
		{17, 20, 18, 20, "at_mrp"},
		{360, 575, 18, 449, "ok"},
		{260, 290, 18, 290, "at_mrp"},
		{680, 1045, 15, 809, "ok"},
		{1170, 1799, 15, 1379, "ok"},
		{85, 165, 15, 100, "ok"},
		{25, 40, 18, 35, "ok"},
		{620, 899, 18, 759, "ok"},
	}
	for _, c := range cases {
		r := SuggestionRule{Method: "cost", CostType: "margin", CostPct: pct(c.margin), Rounding: "charm"}
		got := suggest(suggestIn{Cost: c.cost, MRP: c.mrp, TaxRatePct: 18, Inclusive: true, CostIncludesTax: true}, r)
		if got.Price == nil || *got.Price != c.want || got.Status != c.status {
			p := -1.0
			if got.Price != nil {
				p = *got.Price
			}
			t.Errorf("cost %v mrp %v margin %v: got %v %s, want %v %s", c.cost, c.mrp, c.margin, p, got.Status, c.want, c.status)
		}
	}
}

func TestSuggestRules(t *testing.T) {
	// Cost at or above MRP: hold, no price.
	r := SuggestionRule{Method: "cost", CostType: "margin", CostPct: pct(15), Rounding: "charm"}
	if got := suggest(suggestIn{Cost: 300, MRP: 290, Inclusive: true, CostIncludesTax: true}, r); got.Status != "hold" || got.Price != nil {
		t.Errorf("cost over MRP: %+v", got)
	}
	// No MRP: a price, marked check.
	if got := suggest(suggestIn{Cost: 100, Inclusive: true, CostIncludesTax: true}, r); got.Status != "check" || got.Price == nil || *got.Price != 119 {
		t.Errorf("no MRP: %+v", got)
	}
	// No cost.
	if got := suggest(suggestIn{MRP: 100}, r); got.Status != "no_cost" {
		t.Errorf("no cost: %+v", got)
	}
	// Cost before GST on a GST-inclusive price: GST is added to cost first
	// (100 × 1.18 / 0.8 = 147.50).
	m := SuggestionRule{Method: "cost", CostType: "margin", CostPct: pct(20), Rounding: "none"}
	if got := suggest(suggestIn{Cost: 100, MRP: 300, TaxRatePct: 18, Inclusive: true}, m); got.Price == nil || *got.Price != 147.5 {
		t.Errorf("inclusive: %+v", got)
	}
	// A GST-exclusive price: cost / 0.8 = 125; MRP 200 is 169.49 before GST.
	if got := suggest(suggestIn{Cost: 100, MRP: 200, TaxRatePct: 18}, m); got.Price == nil || *got.Price != 125 || got.Status != "ok" {
		t.Errorf("exclusive: %+v", got)
	}
	// Markup, MRP less a discount, the lower and the higher of the two.
	mk := SuggestionRule{Method: "cost", CostType: "markup", CostPct: pct(30), Rounding: "none"}
	if got := suggest(suggestIn{Cost: 100, MRP: 200, CostIncludesTax: true, Inclusive: true}, mk); *got.Price != 130 {
		t.Errorf("markup: %+v", got)
	}
	md := SuggestionRule{Method: "mrp", CostType: "margin", MRPDiscountPct: pct(10), Rounding: "none"}
	if got := suggest(suggestIn{Cost: 100, MRP: 200, CostIncludesTax: true, Inclusive: true}, md); *got.Price != 180 {
		t.Errorf("mrp less 10%%: %+v", got)
	}
	lo := SuggestionRule{Method: "lower", CostType: "markup", CostPct: pct(30), MRPDiscountPct: pct(10), Rounding: "none"}
	if got := suggest(suggestIn{Cost: 100, MRP: 200, CostIncludesTax: true, Inclusive: true}, lo); *got.Price != 130 {
		t.Errorf("lower: %+v", got)
	}
	hi := lo
	hi.Method = "higher"
	if got := suggest(suggestIn{Cost: 100, MRP: 200, CostIncludesTax: true, Inclusive: true}, hi); *got.Price != 180 {
		t.Errorf("higher: %+v", got)
	}
	// The minimum margin lifts an MRP discount that would sell too cheap.
	floor := SuggestionRule{Method: "mrp", CostType: "margin", MRPDiscountPct: pct(50), MinMarginPct: 20, Rounding: "none"}
	if got := suggest(suggestIn{Cost: 100, MRP: 200, CostIncludesTax: true, Inclusive: true}, floor); *got.Price != 125 {
		t.Errorf("floor: %+v", got)
	}
}

func TestRoundUp(t *testing.T) {
	cases := []struct {
		p    float64
		how  string
		want float64
	}{
		{61.18, "up_1", 62}, {61.18, "up_5", 65}, {61.18, "up_10", 70}, {61.18, "end_9", 69}, {439.02, "end_9", 449},
		{439, "end_9", 439}, {61.18, "end_99", 61.99}, {61, "end_99", 61.99}, {100, "charm", 100}, {100.01, "charm", 109},
		{60, "up_5", 60}, {5, "end_9", 9}, {61.18, "none", 61.18},
	}
	for _, c := range cases {
		if got := roundUp(c.p, c.how); got != c.want {
			t.Errorf("roundUp(%v, %s) = %v, want %v", c.p, c.how, got, c.want)
		}
	}
}
