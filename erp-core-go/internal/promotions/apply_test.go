package promotions

import (
	"math"
	"testing"
)

// A category promotion reaches products filed under any of its
// subcategories (loadEligiblePromotionDefs fills categoryTree), not only
// the ones filed directly under it.
func TestMatchLinesCategoryIncludesSubcategories(t *testing.T) {
	promo := promotionDef{applicationLevel: "category", categoryID: "cricket",
		categoryTree: map[string]bool{"cricket": true, "cricket-bats": true, "bat-grips": true}}
	lines := []cartLine{
		{lineID: "direct", categoryID: "cricket"},
		{lineID: "child", categoryID: "cricket-bats"},
		{lineID: "grandchild", categoryID: "bat-grips"},
		{lineID: "other", categoryID: "football"},
		{lineID: "uncategorized", categoryID: ""},
	}
	got := map[string]bool{}
	for _, l := range matchLines(promo, lines) {
		got[l.lineID] = true
	}
	for _, want := range []string{"direct", "child", "grandchild"} {
		if !got[want] {
			t.Errorf("line %q should match the Cricket promotion", want)
		}
	}
	for _, not := range []string{"other", "uncategorized"} {
		if got[not] {
			t.Errorf("line %q should not match the Cricket promotion", not)
		}
	}

	// Without a loaded tree it still matches the category itself.
	promo.categoryTree = nil
	if m := matchLines(promo, lines); len(m) != 1 || m[0].lineID != "direct" {
		t.Errorf("exact-category match broken: %+v", m)
	}
}

var (
	cricket30 = promotionDef{id: "cricket30", promoType: "percent", applicationLevel: "category", categoryID: "cricket",
		stacking: "exclusive", config: promoConfig{ValuePercent: 30},
		categoryTree: map[string]bool{"cricket": true, "pads": true, "bats": true}}
	pads40 = promotionDef{id: "pads40", promoType: "percent", applicationLevel: "category", categoryID: "pads",
		stacking: "exclusive", config: promoConfig{ValuePercent: 40}, categoryTree: map[string]bool{"pads": true}}
	pads20 = promotionDef{id: "pads20", promoType: "percent", applicationLevel: "category", categoryID: "pads",
		stacking: "exclusive", config: promoConfig{ValuePercent: 20}, categoryTree: map[string]bool{"pads": true}}
	cartFixture = []cartLine{
		{lineID: "pad", categoryID: "pads", qty: 1, unitPrice: 1200},
		{lineID: "bat", categoryID: "bats", qty: 1, unitPrice: 9000},
		{lineID: "shoe", categoryID: "footwear", qty: 1, unitPrice: 1800},
	}
)

func planByPromo(plan []eligiblePromo) map[string]eligiblePromo {
	out := map[string]eligiblePromo{}
	for _, e := range plan {
		out[e.id] = e
	}
	return out
}

// Each line gets its own best price: the pad takes the sub-category's
// 40%, the bat keeps the parent category's 30% — not one promotion for
// the whole order.
func TestPlanPromotionsBestPricePerLine(t *testing.T) {
	plan := planByPromo(planPromotions([]promotionDef{cricket30, pads40}, cartFixture, 12000))
	if e := plan["pads40"]; len(e.lineIDs) != 1 || e.lineIDs[0] != "pad" || math.Abs(e.amount-480) > 0.001 {
		t.Errorf("pad should get 40%% (480): %+v", e)
	}
	if e := plan["cricket30"]; len(e.lineIDs) != 1 || e.lineIDs[0] != "bat" || math.Abs(e.amount-2700) > 0.001 {
		t.Errorf("bat should get 30%% (2700): %+v", e)
	}
	if len(plan) != 2 {
		t.Errorf("expected 2 promotions, got %+v", plan)
	}
}

// A sub-category promotion worth less than the parent's loses on its
// lines: the lowest price wins, whichever promotion gives it.
func TestPlanPromotionsLowerSubcategoryOfferLoses(t *testing.T) {
	plan := planByPromo(planPromotions([]promotionDef{cricket30, pads20}, cartFixture, 12000))
	if _, ok := plan["pads20"]; ok {
		t.Errorf("20%% pad promotion should lose to 30%% cricket: %+v", plan)
	}
	if e := plan["cricket30"]; len(e.lineIDs) != 2 || math.Abs(e.amount-3060) > 0.001 {
		t.Errorf("cricket 30%% should cover pad and bat (3060): %+v", e)
	}
}

// Equal discounts: the more specific promotion takes the line.
func TestPlanPromotionsTieGoesToMoreSpecific(t *testing.T) {
	pads30 := pads40
	pads30.id, pads30.config = "pads30", promoConfig{ValuePercent: 30}
	plan := planByPromo(planPromotions([]promotionDef{cricket30, pads30}, cartFixture, 12000))
	if e := plan["pads30"]; len(e.lineIDs) != 1 || e.lineIDs[0] != "pad" {
		t.Errorf("tie should go to the sub-category promotion: %+v", plan)
	}
}

// Stackable promotions combine, and the combination competes with the
// best exclusive one per line.
func TestPlanPromotionsStackableVersusExclusive(t *testing.T) {
	order10 := promotionDef{id: "order10", promoType: "percent", applicationLevel: "order", stacking: "stackable", config: promoConfig{ValuePercent: 10}}
	order25 := promotionDef{id: "order25", promoType: "percent", applicationLevel: "order", stacking: "stackable", config: promoConfig{ValuePercent: 25}}
	plan := planByPromo(planPromotions([]promotionDef{pads40, order10, order25}, cartFixture, 12000))
	// pad: 40% exclusive beats 35% stacked; bat and shoe: only the stack.
	if e := plan["pads40"]; len(e.lineIDs) != 1 || e.lineIDs[0] != "pad" {
		t.Errorf("pad should keep the 40%% exclusive: %+v", plan)
	}
	for _, id := range []string{"order10", "order25"} {
		if e := plan[id]; len(e.lineIDs) != 2 {
			t.Errorf("%s should cover bat and shoe: %+v", id, e)
		}
	}
}

// A promotion that stops qualifying on the lines it won is dropped, and
// those lines fall back to their next-best offer.
func TestPlanPromotionsFallsBackWhenWinnerNoLongerQualifies(t *testing.T) {
	// Buy 2 get 1 free across cricket: worth most on the pad when the
	// whole set counts, but on its own the pad is no set at all.
	bogo := promotionDef{id: "bogo", promoType: "bogo", applicationLevel: "category", categoryID: "cricket", stacking: "exclusive",
		config: promoConfig{BuyQty: 1, GetQty: 1, GetDiscountPct: 100}, categoryTree: cricket30.categoryTree}
	batOnly := promotionDef{id: "bat90", promoType: "percent", applicationLevel: "category", categoryID: "bats", stacking: "exclusive",
		config: promoConfig{ValuePercent: 90}, categoryTree: map[string]bool{"bats": true}}
	plan := planByPromo(planPromotions([]promotionDef{bogo, batOnly, pads20}, cartFixture, 12000))
	if _, ok := plan["bogo"]; ok {
		t.Errorf("bogo can't stand on the pad alone: %+v", plan)
	}
	if e := plan["pads20"]; len(e.lineIDs) != 1 || e.lineIDs[0] != "pad" {
		t.Errorf("pad should fall back to 20%%: %+v", plan)
	}
}

func TestSamePlan(t *testing.T) {
	plan := []eligiblePromo{{id: "a", amount: 100}, {id: "b", amount: 50}}
	if !samePlan([]appliedLayer{{"b", 50}, {"a", 100}}, plan) {
		t.Error("same promotions and amounts should be the same plan")
	}
	if samePlan([]appliedLayer{{"a", 100}}, plan) || samePlan([]appliedLayer{{"a", 100}, {"b", 40}}, plan) {
		t.Error("a different set or amount is a different plan")
	}
	if !samePlan(nil, nil) {
		t.Error("nothing applied and nothing to apply is unchanged")
	}
}

// migrations/065: a promotion on MRP is worked out on the MRP and applies
// only where the promotional price comes out below the selling price.
func TestEvaluatePromotionOnMRP(t *testing.T) {
	line := []cartLine{{lineID: "bat", qty: 1, unitPrice: 900, mrp: 1000}}
	pct := func(applyOn string, v float64) promotionDef {
		return promotionDef{promoType: "percent", applicationLevel: "order", applyOn: applyOn, config: promoConfig{ValuePercent: v}}
	}
	for _, tc := range []struct {
		name string
		p    promotionDef
		want float64
		ok   bool
	}{
		{"20% off selling price: 900 → 720", pct("selling_price", 20), 180, true},
		{"20% off MRP: 1000 → 800, below 900", pct("mrp", 20), 100, true},
		{"5% off MRP: 950 is not below 900", pct("mrp", 5), 0, false},
		{"10% off MRP: exactly 900, nothing to give", pct("mrp", 10), 0, false},
		{"₹150 off MRP: 850", promotionDef{promoType: "fixed", applyOn: "mrp", config: promoConfig{ValueAmount: 150}}, 50, true},
		{"default (empty) is selling price", pct("", 20), 180, true},
	} {
		got, ok := evaluatePromotion(tc.p, line, 900)
		if ok != tc.ok || math.Abs(got-tc.want) > 0.001 {
			t.Errorf("%s: got %.2f %v, want %.2f %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}

	// Two units, one with no MRP above its price: the gap is only the first's.
	lines := []cartLine{
		{lineID: "a", qty: 2, unitPrice: 900, mrp: 1000}, // MRP 2000, SP 1800
		{lineID: "b", qty: 1, unitPrice: 500, mrp: 500},  // no gap
	}
	got, ok := evaluatePromotion(pct("mrp", 20), lines, 2300) // 20% of 2500 = 500, gap 200
	if !ok || math.Abs(got-300) > 0.001 {
		t.Errorf("mixed lines: got %.2f %v, want 300", got, ok)
	}

	// min_value on MRP: threshold on what is spent, percentage on MRP value.
	mv := promotionDef{promoType: "min_value", applyOn: "mrp", config: promoConfig{MinPurchaseAmount: 2000, DiscountPct: 20}}
	got, ok = evaluatePromotion(mv, lines, 2300)
	if !ok || math.Abs(got-300) > 0.001 {
		t.Errorf("min_value on MRP: got %.2f %v, want 300", got, ok)
	}
	if _, ok := evaluatePromotion(mv, lines[:1], 1800); ok {
		t.Error("min_value on MRP: below the spend threshold must not apply")
	}
}

// A whole-order promotion limited by category, brand and product
// (migrations/069): a line must match every filter that is set; none set
// is the whole order.
func TestMatchLinesOrderScope(t *testing.T) {
	lines := []cartLine{
		{lineID: "yonex-grip", productID: "p1", categoryID: "grips", brandID: "yonex", qty: 2, unitPrice: 180},
		{lineID: "gp-grip", productID: "p2", categoryID: "grips", brandID: "gp", qty: 1, unitPrice: 150},
		{lineID: "yonex-bat", productID: "p3", categoryID: "bats", brandID: "yonex", qty: 1, unitPrice: 3000},
		{lineID: "nobrand", productID: "p4", categoryID: "grips", qty: 1, unitPrice: 100},
	}
	ids := func(p promotionDef) string {
		s := ""
		for _, l := range matchLines(p, lines) {
			s += l.lineID + " "
		}
		return s
	}
	base := promotionDef{applicationLevel: "order"}
	cases := []struct {
		name string
		p    promotionDef
		want string
	}{
		{"no filters: whole order", base, "yonex-grip gp-grip yonex-bat nobrand "},
		{"category (with its subtree)", promotionDef{applicationLevel: "order", scopeCategories: map[string]bool{"badminton": true, "grips": true}}, "yonex-grip gp-grip nobrand "},
		{"brand", promotionDef{applicationLevel: "order", scopeBrands: map[string]bool{"yonex": true}}, "yonex-grip yonex-bat "},
		{"category AND brand", promotionDef{applicationLevel: "order", scopeCategories: map[string]bool{"grips": true}, scopeBrands: map[string]bool{"yonex": true, "gp": true}}, "yonex-grip gp-grip "},
		{"products", promotionDef{applicationLevel: "order", scopeProducts: map[string]bool{"p3": true, "p4": true}}, "yonex-bat nobrand "},
		{"product outside the brand", promotionDef{applicationLevel: "order", scopeBrands: map[string]bool{"gp": true}, scopeProducts: map[string]bool{"p1": true}}, ""},
	}
	for _, c := range cases {
		if got := ids(c.p); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// A scoped order promotion's minimum purchase is measured on its items.
	min := promotionDef{id: "yonex500", promoType: "min_value", applicationLevel: "order", stacking: "exclusive",
		scopeBrands: map[string]bool{"yonex": true}, config: promoConfig{MinPurchaseAmount: 500, DiscountPct: 10}}
	plan := planPromotions([]promotionDef{min}, lines, 3610)
	if len(plan) != 1 || math.Abs(plan[0].amount-336) > 0.001 || len(plan[0].lineIDs) != 2 {
		t.Errorf("YONEX ₹500+ → 10%% of the ₹3,360 YONEX items: %+v", plan)
	}
	min.config.MinPurchaseAmount = 4000 // the cart is ₹3,610 but YONEX only ₹3,360
	if plan := planPromotions([]promotionDef{min}, lines, 3610); len(plan) != 0 {
		t.Errorf("threshold must use the YONEX items, not the whole cart: %+v", plan)
	}
	// It is more specific than the plain whole-order one, less than a category one.
	if !moreSpecific(min, base) || moreSpecific(min, cricket30) {
		t.Error("specificity: category > scoped order > order")
	}
}
