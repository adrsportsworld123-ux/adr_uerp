package taxcalc

import "testing"

func TestComputeLine(t *testing.T) {
	cases := []struct {
		name                        string
		price, qty, discount, rate  float64
		inclusive                   bool
		wantTaxable, wantTax, total float64
	}{
		{"exclusive basic", 1000, 1, 0, 18, false, 1000, 180, 1180},
		{"exclusive qty+discount", 499.5, 3, 98.5, 12, false, 1400, 168, 1568},
		{"exclusive zero rate", 250, 2, 0, 0, false, 500, 0, 500},
		{"inclusive single unit", 1000, 1, 0, 18, true, 847.46, 152.54, 1000},
		// The case per-unit back-calculation gets wrong (would be 3000.01).
		{"inclusive multi-qty exact total", 1000, 3, 0, 18, true, 2542.37, 457.63, 3000},
		{"inclusive with discount", 1180, 2, 236, 18, true, 1800, 324, 2124},
		{"inclusive zero rate", 99, 2, 0, 0, true, 198, 0, 198},
		{"inclusive fractional qty (weighed)", 45.5, 0.333, 0, 5, true, 14.43, 0.72, 15.15},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ComputeLine(c.price, c.qty, c.discount, c.rate, c.inclusive)
			if got.Taxable != c.wantTaxable || got.Tax != c.wantTax || got.Total != c.total {
				t.Fatalf("got %+v, want taxable=%v tax=%v total=%v", got, c.wantTaxable, c.wantTax, c.total)
			}
			if Round2(got.Taxable+got.Tax) != got.Total {
				t.Fatalf("taxable+tax != total: %+v", got)
			}
		})
	}
}

// The invariant that matters most for a POS: for an inclusive line the
// customer pays exactly price*qty, whatever the rate or quantity.
func TestInclusiveTotalAlwaysMatchesShelfPrice(t *testing.T) {
	for _, rate := range []float64{0, 0.25, 3, 5, 12, 18, 28, 40} {
		for _, price := range []float64{0.5, 9.99, 99, 101.37, 1000, 4999.99} {
			for qty := 1.0; qty <= 25; qty++ {
				got := ComputeLine(price, qty, 0, rate, true)
				if want := Round2(price * qty); got.Total != want {
					t.Fatalf("rate=%v price=%v qty=%v: total %v, want %v", rate, price, qty, got.Total, want)
				}
				if Round2(got.Taxable+got.Tax) != got.Total {
					t.Fatalf("rate=%v price=%v qty=%v: taxable+tax != total: %+v", rate, price, qty, got)
				}
			}
		}
	}
}

func TestNetAndGrossPrice(t *testing.T) {
	if got := NetPrice(1180, 18, true); got != 1000 {
		t.Fatalf("NetPrice inclusive = %v, want 1000", got)
	}
	if got := NetPrice(1000, 18, false); got != 1000 {
		t.Fatalf("NetPrice exclusive = %v, want 1000", got)
	}
	if got := GrossPrice(1000, 18, false); got != 1180 {
		t.Fatalf("GrossPrice exclusive = %v, want 1180", got)
	}
	if got := GrossPrice(1180, 18, true); got != 1180 {
		t.Fatalf("GrossPrice inclusive = %v, want 1180", got)
	}
}
