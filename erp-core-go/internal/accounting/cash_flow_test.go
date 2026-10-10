package accounting

import "testing"

func TestRound2Negative(t *testing.T) {
	// The old int64(v*100+0.5) truncated toward zero: a ₹10 shortage
	// became ₹9.99 and every shortage posted a paisa short.
	cases := map[float64]float64{-10: -10, -10.005: -10.01, -0.004: 0, 10.005: 10.01, 1234.565: 1234.57, -1234.565: -1234.57}
	for in, want := range cases {
		if got := round2(in); got != want {
			t.Errorf("round2(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestCashFlowNet(t *testing.T) {
	f := CashFlow{CashSales: 1000, CashRefunds: 120.5, FloatIn: 500, Drops: 800, PaidIn: 50, PaidOut: 75.25}
	if got := f.Net(); got != 554.25 {
		t.Errorf("Net = %v, want 554.25", got)
	}
}
