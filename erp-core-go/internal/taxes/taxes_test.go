package taxes

import (
	"math"
	"testing"
)

func TestSplitLineAddsBackUp(t *testing.T) {
	for _, tax := range []float64{0, 0.01, 0.05, 1.21, 121.33, 2712.82, -1.21, -0.05} {
		a := SplitLine(tax, 0, false)
		if math.Abs(a.CGST+a.SGST-tax) > 1e-9 {
			t.Errorf("intra %.2f: CGST %.2f + SGST %.2f != tax", tax, a.CGST, a.SGST)
		}
		if a.IGST != 0 {
			t.Errorf("intra %.2f: IGST %.2f, want 0", tax, a.IGST)
		}
		b := SplitLine(tax, 0, true)
		if b.IGST != tax || b.CGST != 0 || b.SGST != 0 {
			t.Errorf("inter %.2f: got %+v", tax, b)
		}
	}
	// The odd paisa goes to CGST, as SQL's round((tax - cess) / 2, 2) does.
	if a := SplitLine(1.21, 0, false); a.CGST != 0.61 || a.SGST != 0.60 {
		t.Errorf("1.21 split %+v, want CGST 0.61 SGST 0.60", a)
	}
	// Cess is carved out before the split.
	if a := SplitLine(30, 6, false); a.CGST != 12 || a.SGST != 12 || a.Cess != 6 {
		t.Errorf("cess split %+v", a)
	}
}

func TestBuildIntraSingleRate(t *testing.T) {
	b := Build(Settings{Regime: RegimeGSTSplit, TaxLabel: "GST", StateCode: "33"}, false, "33",
		[]Line{{Rate: 5, Taxable: 24.29, Tax: 1.21}})
	if b.CGST != "0.61" || b.SGST != "0.60" || b.IGST != "0.00" || b.Total != "1.21" {
		t.Fatalf("totals %+v", b)
	}
	if len(b.Rows) != 2 || b.Rows[0] != (Row{"CGST @ 2.5%", "0.61"}) || b.Rows[1] != (Row{"SGST @ 2.5%", "0.60"}) {
		t.Fatalf("rows %+v", b.Rows)
	}
	if b.PlaceOfSupplyName != "Tamil Nadu" {
		t.Errorf("place of supply %q", b.PlaceOfSupplyName)
	}
}

func TestBuildInterMixedRates(t *testing.T) {
	b := Build(Settings{Regime: RegimeGSTSplit, TaxLabel: "GST"}, true, "29", []Line{
		{Rate: 18, Taxable: 1000, Tax: 180},
		{Rate: 5, Taxable: 200, Tax: 10},
		{Rate: 18, Taxable: 500, Tax: 90},
	})
	if b.IGST != "280.00" || b.CGST != "0.00" || !b.InterState {
		t.Fatalf("totals %+v", b)
	}
	if len(b.Rows) != 1 || b.Rows[0] != (Row{"IGST", "280.00"}) {
		t.Fatalf("rows %+v (mixed rates carry no rate on the line)", b.Rows)
	}
	if len(b.Summary) != 2 || b.Summary[0].Rate != "5.00" || b.Summary[1].Taxable != "1500.00" || b.Summary[1].IGST != "270.00" {
		t.Fatalf("summary %+v", b.Summary)
	}
}

func TestBuildSingleRegime(t *testing.T) {
	// A single-tax merchant ignores inter-state and prints its own label.
	b := Build(Settings{Regime: RegimeSingle, TaxLabel: "VAT"}, true, "", []Line{{Rate: 5, Taxable: 100, Tax: 5}})
	if b.InterState || len(b.Rows) != 1 || b.Rows[0] != (Row{"VAT @ 5%", "5.00"}) {
		t.Fatalf("single regime %+v", b)
	}
}

func TestFormatRate(t *testing.T) {
	for in, want := range map[float64]string{18: "18", 2.5: "2.5", 0.25: "0.25", 0: "0", 1.5: "1.5"} {
		if got := FormatRate(in); got != want {
			t.Errorf("FormatRate(%v) = %q, want %q", in, got, want)
		}
	}
}
