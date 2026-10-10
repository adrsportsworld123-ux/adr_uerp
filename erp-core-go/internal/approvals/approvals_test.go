package approvals

import "testing"

func f(v float64) *float64 { return &v }

func TestDefaultIsTheOldHardcodedBehaviour(t *testing.T) {
	p := Default()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		pct  float64
		want string
		ok   bool
	}{
		{1, LevelNone, true}, {5, LevelNone, true}, {5.01, LevelBranchManager, true}, {15, LevelBranchManager, true},
		{15.5, LevelMerchantAdmin, true}, {25, LevelMerchantAdmin, true}, {25.01, "", false},
	}
	for _, c := range cases {
		tier, ok := p.DiscountTierFor(c.pct)
		if ok != c.ok || (ok && tier.Approver != c.want) {
			t.Errorf("%.2f%%: got %+v %v, want %s %v", c.pct, tier, ok, c.want, c.ok)
		}
	}
	if p.MaxDiscount() != 25 || p.RefundApprovalAbove != nil || p.StockAdjustApprovalAbove != nil || p.BelowCostPrice != LevelAny {
		t.Errorf("defaults changed: %+v", p)
	}
}

func TestRolesMeet(t *testing.T) {
	bm, ma, pos := []string{"Branch Manager"}, []string{"Merchant Admin"}, []string{"POS User"}
	for _, c := range []struct {
		roles []string
		level string
		want  bool
	}{
		{pos, LevelNone, true}, {pos, LevelAny, true}, {pos, LevelBranchManager, false}, {bm, LevelBranchManager, true},
		{ma, LevelBranchManager, true}, {bm, LevelMerchantAdmin, false}, {ma, LevelMerchantAdmin, true}, {ma, "nonsense", false},
	} {
		if got := RolesMeet(c.roles, c.level); got != c.want {
			t.Errorf("%v at %s = %v", c.roles, c.level, got)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := Default()
	ok.RefundApprovalAbove, ok.StockAdjustApprovalAbove, ok.JVApprovalAbove = f(1000), f(500), f(0)
	ok.DiscountTiers = []DiscountTier{{UpTo: 10, Approver: LevelNone}, {UpTo: 40, Approver: LevelMerchantAdmin}}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid policy refused: %v", err)
	}
	bad := map[string]func(*Policy){
		"no tiers": func(p *Policy) { p.DiscountTiers = nil },
		"falling tiers": func(p *Policy) {
			p.DiscountTiers = []DiscountTier{{UpTo: 20, Approver: LevelNone}, {UpTo: 10, Approver: LevelNone}}
		},
		"over 100":         func(p *Policy) { p.DiscountTiers = []DiscountTier{{UpTo: 120, Approver: LevelNone}} },
		"bad tier level":   func(p *Policy) { p.DiscountTiers[0].Approver = "owner" },
		"negative":         func(p *Policy) { p.RefundApprovalAbove = f(-1) },
		"refund none":      func(p *Policy) { p.RefundApprover = LevelNone },
		"below cost level": func(p *Policy) { p.BelowCostPrice = "cashier" },
	}
	for name, mutate := range bad {
		p := Default()
		mutate(&p)
		if p.Validate() == nil {
			t.Errorf("%s: should not validate", name)
		}
	}
}
