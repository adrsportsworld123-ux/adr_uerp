package customers

import "testing"

func TestValidEmail(t *testing.T) {
	for _, ok := range []string{"", "ravi@acme.in", "a.b+c@shop.co.in"} {
		if !ValidEmail(ok) {
			t.Errorf("%q should be accepted", ok)
		}
	}
	for _, bad := range []string{"ravi.gmail.com", "a@b", "@acme.in", "ravi@", "a b@c.in", "a@@c.in", "a@.in", "a@c.in."} {
		if ValidEmail(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
