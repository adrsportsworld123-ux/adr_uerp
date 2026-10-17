package hr

import (
	"math"
	"testing"
)

func f(v float64) *float64 { return &v }

func TestHaversine(t *testing.T) {
	// Chennai Central → Chennai Egmore is about 1.2 km.
	d := haversineM(13.0827, 80.2707, 13.0784, 80.2602)
	if math.Abs(d-1230) > 150 {
		t.Fatalf("distance = %.0f m", d)
	}
}

func TestClassify(t *testing.T) {
	g := &geofence{Latitude: 12.8340, Longitude: 80.1700, RadiusM: 200, Enabled: true}
	cases := []struct {
		name string
		ev   punchEvidence
		want string
	}{
		{"no fence", punchEvidence{Latitude: f(12.834), Longitude: f(80.17)}, "not_configured"},
		{"no location", punchEvidence{}, "unknown"},
		{"at the shop", punchEvidence{Latitude: f(12.8341), Longitude: f(80.1701)}, "inside"},
		{"far away", punchEvidence{Latitude: f(12.9), Longitude: f(80.2)}, "outside"},
		// ~230 m out with a ±40 m fix: the benefit of the doubt.
		{"edge with accuracy", punchEvidence{Latitude: f(12.83607), Longitude: f(80.1700), AccuracyM: f(40)}, "inside"},
		{"edge without accuracy", punchEvidence{Latitude: f(12.83607), Longitude: f(80.1700)}, "outside"},
	}
	for _, c := range cases {
		fence := g
		if c.name == "no fence" {
			fence = nil
		}
		ev := c.ev
		classify(fence, &ev)
		if *ev.Geofence != c.want {
			t.Errorf("%s: got %s (distance %v), want %s", c.name, *ev.Geofence, ev.DistanceM, c.want)
		}
	}
	disabled := *g
	disabled.Enabled = false
	ev := punchEvidence{Latitude: f(12.9), Longitude: f(80.2)}
	classify(&disabled, &ev)
	if *ev.Geofence != "not_configured" {
		t.Errorf("a disabled fence must not classify, got %s", *ev.Geofence)
	}
}
