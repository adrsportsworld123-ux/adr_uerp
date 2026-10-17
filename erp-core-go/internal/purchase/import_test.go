package purchase

import "testing"

func TestParseNumberReadsInvoiceFormatting(t *testing.T) {
	for in, want := range map[string]float64{"₹ 1,100": 1100, "36 PCS": 36, "5%": 5, "26,804.41": 26804.41, " 347.46 ": 347.46, "Rs. 90": 90} {
		if got, ok := parseNumber(in); !ok || got != want {
			t.Errorf("parseNumber(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "-", "abc", "NaN"} {
		if _, ok := parseNumber(in); ok {
			t.Errorf("parseNumber(%q) should fail", in)
		}
	}
}

func TestParseImportCSVAcceptsAliasesAndBOM(t *testing.T) {
	rows, errs := parseImportCSV("\ufeffItems,Qty.,Rate,GST %\nSIXIT T/B GREEN LITE,36 PCS,60,0\n,,,\n")
	if len(errs) != 0 || len(rows) != 1 {
		t.Fatalf("rows=%v errs=%v", rows, errs)
	}
	r := rows[0]
	if r.n != 2 || r.cols["item_name"] != "SIXIT T/B GREEN LITE" || r.cols["quantity"] != "36 PCS" || r.cols["unit_cost"] != "60" || r.cols["gst_rate"] != "0" {
		t.Fatalf("unexpected row %+v", r)
	}
	if _, errs := parseImportCSV("name,price\nx,1\n"); len(errs) == 0 {
		t.Fatal("a file without a quantity column must be refused")
	}
}

func TestNormKeyIgnoresCaseAndSpacing(t *testing.T) {
	if normKey(" MERCURY +  green") != normKey("Mercury + Green") {
		t.Fatal("names differing only in case and spacing must match")
	}
}

func TestSuggestNeverAutoMatchesButRanks(t *testing.T) {
	c := &catalogue{}
	for _, v := range []*knownVariant{{id: "a", name: " MERCURY + GREEN", sku: " MERCURY + GREEN"}, {id: "b", name: "COSCO FOOTBALL", sku: "COSCO FOOTBALL"}} {
		v.words = words(v.name + " " + v.sku)
		c.variants = append(c.variants, v)
	}
	s := c.suggest("MERCURY + GREEN WOT")
	if len(s) != 1 || s[0].VariantID != "a" {
		t.Fatalf("suggestions = %+v", s)
	}
}
