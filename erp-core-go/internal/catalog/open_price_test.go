package catalog

import "testing"

func TestCheckOpenPrice(t *testing.T) {
	above := "500.00"
	op := OpenPrice{Min: "100.00", Max: "1000.00", ApprovalAbove: &above}
	cases := []struct {
		price               float64
		inRange, needsApprv bool
	}{
		{99.99, false, false},
		{100, true, false},
		{500, true, false}, // the threshold itself doesn't need approval
		{500.01, true, true},
		{1000, true, true},
		{1000.01, false, false},
		{12.1 + 487.9, true, false}, // float noise must not cross the threshold
	}
	for _, c := range cases {
		in, need := CheckOpenPrice(op, c.price)
		if in != c.inRange || need != c.needsApprv {
			t.Errorf("price %.4f: got (%v,%v), want (%v,%v)", c.price, in, need, c.inRange, c.needsApprv)
		}
	}
	free := OpenPrice{Min: "0.00", Max: "50.00"}
	if in, need := CheckOpenPrice(free, 50); !in || need {
		t.Errorf("no threshold: got (%v,%v)", in, need)
	}
}

func TestOpenPriceInputValidate(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	ok := []OpenPriceInput{
		{Enabled: false, Min: -5},
		{Enabled: true, Min: 0, Max: 10},
		{Enabled: true, Min: 5, Max: 5, ApprovalAbove: f(5)},
	}
	bad := []OpenPriceInput{
		{Enabled: true, Min: -1, Max: 10},
		{Enabled: true, Min: 0, Max: 0},
		{Enabled: true, Min: 20, Max: 10},
		{Enabled: true, Min: 0, Max: 10, ApprovalAbove: f(11)},
	}
	for _, in := range ok {
		if err := in.validate(); err != nil {
			t.Errorf("%+v: unexpected %v", in, err)
		}
	}
	for _, in := range bad {
		if in.validate() == nil {
			t.Errorf("%+v: expected an error", in)
		}
	}
}
