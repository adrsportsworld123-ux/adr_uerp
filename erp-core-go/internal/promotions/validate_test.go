package promotions

import (
	"strings"
	"testing"
)

func TestValidatePromotion(t *testing.T) {
	ok := createPromotionRequest{Name: " Diwali ", PromoType: "percent", Config: promoConfig{ValuePercent: 10}}
	if msg := validatePromotion(&ok); msg != "" {
		t.Fatalf("valid promotion refused: %s", msg)
	}
	if ok.Name != "Diwali" || ok.ApplicationLevel != "order" || ok.Stacking != "exclusive" {
		t.Errorf("defaults/trim not applied: %+v", ok)
	}

	// A scope id left over from another level is dropped, not refused, so an
	// edit that switches product -> order needn't clear it explicitly.
	switched := createPromotionRequest{Name: "x", PromoType: "percent", Config: promoConfig{ValuePercent: 5}, ProductID: "p1"}
	if msg := validatePromotion(&switched); msg != "" || switched.ProductID != "" {
		t.Errorf("stale product_id on an order promotion: %q %+v", msg, switched)
	}

	bad := map[string]createPromotionRequest{
		"name":           {PromoType: "percent", Config: promoConfig{ValuePercent: 5}},
		"product":        {Name: "x", PromoType: "percent", ApplicationLevel: "product", Config: promoConfig{ValuePercent: 5}},
		"category":       {Name: "x", PromoType: "percent", ApplicationLevel: "category", Config: promoConfig{ValuePercent: 5}},
		"config":         {Name: "x", PromoType: "percent", Config: promoConfig{ValuePercent: 150}},
		"bogo on order":  {Name: "x", PromoType: "bogo", Config: promoConfig{BuyQty: 1, GetQty: 1, GetDiscountPct: 100}},
		"segment":        {Name: "x", PromoType: "percent", TargetSegment: "gold", Config: promoConfig{ValuePercent: 5}},
		"half a window":  {Name: "x", PromoType: "percent", TimeStart: "10:00", Config: promoConfig{ValuePercent: 5}},
		"equal times":    {Name: "x", PromoType: "percent", TimeStart: "10:00", TimeEnd: "10:00", Config: promoConfig{ValuePercent: 5}},
		"day 7":          {Name: "x", PromoType: "percent", DaysOfWeek: []int{7}, Config: promoConfig{ValuePercent: 5}},
		"dates reversed": {Name: "x", PromoType: "percent", StartsAt: "2026-12-01T00:00:00Z", EndsAt: "2026-11-01T00:00:00Z", Config: promoConfig{ValuePercent: 5}},
	}
	for name, req := range bad {
		if msg := validatePromotion(&req); msg == "" {
			t.Errorf("%s: should be refused", name)
		}
	}
}

func TestValidateCoupon(t *testing.T) {
	one := 1
	ok := createCouponRequest{Code: " diwali50 ", PromoType: "fixed", Value: 50, UsageLimitTotal: &one, ValidFrom: "2026-10-01", ValidUntil: "2026-11-01T00:00:00+05:30"}
	if msg := validateCoupon(&ok); msg != "" {
		t.Fatalf("valid coupon refused: %s", msg)
	}
	if ok.Code != "DIWALI50" {
		t.Errorf("code not normalised: %q", ok.Code)
	}
	zero := 0
	bad := map[string]createCouponRequest{
		"no code":     {PromoType: "fixed", Value: 5},
		"over 100%":   {Code: "A", PromoType: "percent", Value: 101},
		"zero limit":  {Code: "A", PromoType: "fixed", Value: 5, UsageLimitTotal: &zero},
		"channel":     {Code: "A", PromoType: "fixed", Value: 5, Channels: []string{"store"}},
		"bad date":    {Code: "A", PromoType: "fixed", Value: 5, ValidUntil: "31/12/2026"},
		"window back": {Code: "A", PromoType: "fixed", Value: 5, ValidFrom: "2026-12-01", ValidUntil: "2026-11-01"},
		"negative":    {Code: "A", PromoType: "fixed", Value: 5, MinPurchaseAmount: -1},
	}
	for name, req := range bad {
		if msg := validateCoupon(&req); msg == "" || strings.TrimSpace(msg) == "" {
			t.Errorf("%s: should be refused", name)
		}
	}
}

func TestRFC3339RoundTrip(t *testing.T) {
	in := "2026-11-01 00:00:00+05:30"
	if got := rfc3339(&in); got != "2026-11-01T00:00:00+05:30" {
		t.Errorf("rfc3339(%q) = %q", in, got)
	}
	utc := "2026-11-01 08:30:00.123+00"
	if got := rfc3339(&utc); got != "2026-11-01T08:30:00Z" {
		t.Errorf("rfc3339(%q) = %q", utc, got)
	}
}
