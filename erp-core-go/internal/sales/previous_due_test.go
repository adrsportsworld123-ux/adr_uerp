package sales

import (
	"reflect"
	"testing"
)

// The previous outstanding comes out of the tenders that can pay a
// receivable, last first; the sale keeps the rest, and credit never pays it.
func TestSplitTenders(t *testing.T) {
	cases := []struct {
		name      string
		payments  []checkoutPayment
		collect   float64
		sale, due []checkoutPayment
		ok        bool
	}{
		{"cash covers both", []checkoutPayment{{Method: "cash", Amount: 1500}}, 500,
			[]checkoutPayment{{Method: "cash", Amount: 1000}}, []checkoutPayment{{Method: "cash", Amount: 500}}, true},
		{"last tender first", []checkoutPayment{{Method: "cash", Amount: 1000}, {Method: "upi", Amount: 300, Reference: "UTR1"}}, 500,
			[]checkoutPayment{{Method: "cash", Amount: 800}}, []checkoutPayment{{Method: "cash", Amount: 200}, {Method: "upi", Amount: 300, Reference: "UTR1"}}, true},
		{"credit is skipped", []checkoutPayment{{Method: "cash", Amount: 400}, {Method: "credit", Amount: 1000}}, 400,
			[]checkoutPayment{{Method: "credit", Amount: 1000}}, []checkoutPayment{{Method: "cash", Amount: 400}}, true},
		{"not enough cash", []checkoutPayment{{Method: "cash", Amount: 100}, {Method: "credit", Amount: 1000}}, 400,
			[]checkoutPayment{{Method: "cash", Amount: 100}, {Method: "credit", Amount: 1000}}, nil, false},
		{"paise", []checkoutPayment{{Method: "card", Amount: 1250.75}}, 250.25,
			[]checkoutPayment{{Method: "card", Amount: 1000.5}}, []checkoutPayment{{Method: "card", Amount: 250.25}}, true},
	}
	for _, c := range cases {
		sale, due, ok := splitTenders(c.payments, c.collect)
		if ok != c.ok || !reflect.DeepEqual(sale, c.sale) || !reflect.DeepEqual(due, c.due) {
			t.Errorf("%s: sale %+v due %+v ok %v", c.name, sale, due, ok)
		}
	}
}
