package sales

import "testing"

func TestResolveManualDiscount(t *testing.T) {
	cases := []struct {
		name        string
		req         discountRequest
		subtotal    float64
		wantAmount  float64
		wantPct     float64
		wantPercent bool // a value_percent is recorded
	}{
		{"percent", discountRequest{Mode: discountModePercent, ValuePercent: 10}, 1999, 199.9, 10, true},
		{"percent rounds the amount", discountRequest{Mode: discountModePercent, ValuePercent: 7.5}, 333.33, 25, 7.5, true},
		{"fixed amount", discountRequest{Mode: discountModeAmount, Amount: 100}, 2000, 100, 5, false},
		{"fixed amount judged on its percent", discountRequest{Mode: discountModeAmount, Amount: 300}, 2000, 300, 15, false},
	}
	for _, c := range cases {
		amount, pct, vp := resolveManualDiscount(c.req, c.subtotal)
		if amount != c.wantAmount || round2(pct) != c.wantPct || (vp != nil) != c.wantPercent {
			t.Errorf("%s: got amount %.2f pct %.4f percent recorded %v, want %.2f %.4f %v",
				c.name, amount, pct, vp != nil, c.wantAmount, c.wantPct, c.wantPercent)
		}
	}
}
