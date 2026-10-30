package catalog

import "testing"

func TestFitsDecimals(t *testing.T) {
	cases := []struct {
		qty      float64
		decimals int
		want     bool
	}{
		{30, 0, true}, {1.5, 0, false}, {29.999999999, 0, true}, // float noise is whole
		{1.25, 3, true}, {1.2345, 3, false}, {0.5, 1, true}, {0.25, 1, false}, {-2, 0, true},
	}
	for _, c := range cases {
		if got := fitsDecimals(c.qty, c.decimals); got != c.want {
			t.Errorf("fitsDecimals(%v, %d) = %v, want %v", c.qty, c.decimals, got, c.want)
		}
	}
}

func TestQuantityErrorMessage(t *testing.T) {
	if got := (&QuantityError{Product: "Eggs", Unit: "pieces", Decimals: 0}).Error(); got != "Eggs is counted in whole pieces" {
		t.Errorf("whole: %q", got)
	}
	if got := (&QuantityError{Product: "Rice", Unit: "kilogram", Decimals: 3}).Error(); got != "Rice is counted to at most 3 decimal place(s) of kilogram" {
		t.Errorf("decimals: %q", got)
	}
}
