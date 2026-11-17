package catalog

import "testing"

func ean(d12 string) string {
	sum := 0
	for i, c := range d12 {
		n := int(c - '0')
		if i%2 == 1 {
			n *= 3
		}
		sum += n
	}
	return d12 + string(rune('0'+(10-sum%10)%10))
}

func TestDecodeScaleFormats(t *testing.T) {
	// The default reads exactly what the fixed convention read.
	code := ean("21" + "00042" + "01250")
	plu, v, f, ok := decodeScale(code, []ScaleFormat{DefaultScaleFormat})
	if !ok || plu != "00042" || v != 1.25 || f.ValueKind != "weight" {
		t.Fatalf("default: %v %q %v %v", ok, plu, v, f)
	}
	oldPLU, oldKg, oldOK := decodeWeighingScaleBarcode(code)
	if !oldOK || oldPLU != plu || oldKg != v {
		t.Fatalf("default differs from the fixed decoder")
	}
	price := ScaleFormat{Prefix: "22", PLUDigits: 4, ValueKind: "price", ValueDigits: 6, ValueDecimals: 2, Active: true}
	formats := []ScaleFormat{DefaultScaleFormat, price}
	plu, v, f, ok = decodeScale(ean("22"+"1234"+"012550"), formats)
	if !ok || plu != "1234" || v != 125.5 || f.ValueKind != "price" {
		t.Fatalf("price: %v %q %v %v", ok, plu, v, f)
	}
	if _, _, _, ok := decodeScale(ean("23"+"1234"+"012550"), formats); ok {
		t.Fatal("an unlisted prefix is not a scale code")
	}
	bad := ean("22" + "1234" + "012550")
	bad = bad[:12] + string(rune('0'+(int(bad[12]-'0')+1)%10))
	if _, _, _, ok := decodeScale(bad, formats); ok {
		t.Fatal("a wrong check digit is not a scale code")
	}
	off := price
	off.Active = false
	if _, _, _, ok := decodeScale(ean("22"+"1234"+"012550"), []ScaleFormat{off}); ok {
		t.Fatal("a switched-off format is not read")
	}
	for _, tc := range []struct {
		f  ScaleFormat
		ok bool
	}{
		{DefaultScaleFormat, true},
		{ScaleFormat{Prefix: "20", PLUDigits: 5, ValueKind: "weight", ValueDigits: 5, ValueDecimals: 3}, false},
		{ScaleFormat{Prefix: "02", PLUDigits: 5, ValueKind: "weight", ValueDigits: 5, ValueDecimals: 3}, true},
		{ScaleFormat{Prefix: "21", PLUDigits: 5, ValueKind: "weight", ValueDigits: 6, ValueDecimals: 3}, false},
		{ScaleFormat{Prefix: "21", PLUDigits: 3, ValueKind: "weight", ValueDigits: 7, ValueDecimals: 3}, false},
		{ScaleFormat{Prefix: "21", PLUDigits: 5, ValueKind: "count", ValueDigits: 5, ValueDecimals: 0}, false},
	} {
		if got := tc.f.Validate() == ""; got != tc.ok {
			t.Errorf("Validate(%+v) ok=%v, want %v", tc.f, got, tc.ok)
		}
	}
}

func TestComboLabel(t *testing.T) {
	if got := ComboLabel([]byte(`{"Size":"L","Colour":"Red"}`)); got != "Red · L" {
		t.Fatalf("got %q", got)
	}
	if got := ComboLabel([]byte(`{}`)); got != "" {
		t.Fatalf("got %q", got)
	}
}
