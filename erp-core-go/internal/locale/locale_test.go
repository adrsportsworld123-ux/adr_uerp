package locale

import (
	"testing"

	"erp-core-go/internal/taxes"
)

func TestResolve(t *testing.T) {
	inr := taxes.Currency{Code: "INR", Symbol: "₹"}
	def := Settings{Language: "en", Timezone: DefaultTimezone, TextDirection: "auto", CartSide: "right"}

	e := Resolve(def, inr, nil, nil)
	if e.Direction != "ltr" || e.CartSide != "right" || e.UTCOffsetMinutes != 330 {
		t.Fatalf("defaults: %+v", e)
	}
	if len(e.Denominations) != 10 || e.Denominations[0] != "2000.00" || e.Denominations[9] != "1.00" {
		t.Fatalf("INR denominations: %v", e.Denominations)
	}

	ar := def
	ar.Language = "ar"
	if e := Resolve(ar, inr, nil, nil); e.Direction != "rtl" {
		t.Fatalf("arabic auto direction: %s", e.Direction)
	}
	ltr, left := "ltr", "left"
	if e := Resolve(ar, inr, &ltr, &left); e.Direction != "ltr" || e.TextDirection != "ltr" || e.CartSide != "left" {
		t.Fatalf("terminal overrides: %+v", e)
	}

	own := def
	own.Denominations = []string{"500.00", "100.00"}
	if e := Resolve(own, inr, nil, nil); len(e.Denominations) != 2 {
		t.Fatalf("merchant denominations: %v", e.Denominations)
	}

	dubai := def
	dubai.Timezone = "Asia/Dubai"
	if e := Resolve(dubai, taxes.Currency{Code: "AED", Symbol: "AED"}, nil, nil); e.UTCOffsetMinutes != 240 || e.Denominations[0] != "1000.00" {
		t.Fatalf("dubai: %+v", e)
	}
}

func TestDefaultDenominationsFallback(t *testing.T) {
	if d := DefaultDenominations("XYZ"); len(d) == 0 || d[0] != "1000.00" {
		t.Fatalf("fallback: %v", d)
	}
}

func TestValidLanguage(t *testing.T) {
	for _, c := range []string{"en", "ta", "hi", "ar", "ml", "te", "kn", "fr", "es"} {
		if !ValidLanguage(c) {
			t.Fatalf("%s should be valid", c)
		}
	}
	if ValidLanguage("de") {
		t.Fatal("de is not translated")
	}
}

// migrations/065: a user's own language and direction sit on top of the
// merchant's; a terminal's direction still wins.
func TestForUser(t *testing.T) {
	inr := taxes.Currency{Code: "INR", Symbol: "₹"}
	m := Settings{Language: "en", Timezone: DefaultTimezone, TextDirection: "ltr", CartSide: "right"}
	str := func(s string) *string { return &s }

	if e := Resolve(m.ForUser(UserPrefs{}), inr, nil, nil); e.Language != "en" || e.Direction != "ltr" {
		t.Fatalf("no preference = the merchant's: %+v", e)
	}
	// Arabic for this user, the merchant forcing ltr: the user's language reads rtl.
	if e := Resolve(m.ForUser(UserPrefs{Language: str("ar")}), inr, nil, nil); e.Language != "ar" || e.Direction != "rtl" {
		t.Fatalf("user Arabic: %+v", e)
	}
	if e := Resolve(m.ForUser(UserPrefs{Language: str("ar"), TextDirection: str("ltr")}), inr, nil, nil); e.Direction != "ltr" {
		t.Fatalf("user Arabic, own ltr: %+v", e)
	}
	if e := Resolve(m.ForUser(UserPrefs{Language: str("ta")}), inr, str("rtl"), nil); e.Language != "ta" || e.Direction != "rtl" {
		t.Fatalf("terminal direction wins: %+v", e)
	}
	if e := Resolve(m.ForUser(UserPrefs{Language: str("xx")}), inr, nil, nil); e.Language != "en" {
		t.Fatalf("unknown stored language ignored: %+v", e)
	}
	if e := Resolve(m.ForUser(UserPrefs{Language: str("es")}), inr, nil, nil); e.Timezone != DefaultTimezone || e.Denominations[0] != "2000.00" {
		t.Fatalf("clock and notes stay the merchant's: %+v", e)
	}
}
