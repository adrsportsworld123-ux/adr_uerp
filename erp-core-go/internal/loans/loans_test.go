package loans

import (
	"math"
	"testing"
)

func TestSchedule(t *testing.T) {
	// ₹10,000 over 3 months, interest-free: 3333.33 + 3333.33 + 3333.34.
	interest, emi, s := Schedule(10000, 0, 3, 11, 2026)
	if interest != 0 || emi != 3333.33 || len(s) != 3 {
		t.Fatalf("interest %.2f emi %.2f n %d", interest, emi, len(s))
	}
	if s[2].PrincipalPart != 3333.34 || s[0].DueMonth != 11 || s[1].DueMonth != 12 || s[2].DueMonth != 1 || s[2].DueYear != 2027 {
		t.Fatalf("schedule: %+v", s)
	}

	// ₹50,000 at 12% flat over 10 months: interest 5,000, EMI 5,500.
	interest, emi, s = Schedule(50000, 12, 10, 4, 2026)
	if interest != 5000 || emi != 5500 {
		t.Fatalf("interest %.2f emi %.2f", interest, emi)
	}
	var p, i float64
	for _, x := range s {
		p += x.PrincipalPart
		i += x.InterestPart
		if math.Abs(x.Amount-(x.PrincipalPart+x.InterestPart)) > 0.001 {
			t.Fatalf("amount != parts: %+v", x)
		}
	}
	if math.Abs(p-50000) > 0.001 || math.Abs(i-5000) > 0.001 {
		t.Fatalf("parts add to %.2f + %.2f", p, i)
	}

	// Rounding goes to the last instalment: 1000 at 7% over 7 months.
	interest, _, s = Schedule(1000, 7, 7, 1, 2026)
	var sum float64
	for _, x := range s {
		sum += x.Amount
	}
	if math.Abs(sum-(1000+interest)) > 0.001 {
		t.Fatalf("total %.2f, want %.2f", sum, 1000+interest)
	}
}
