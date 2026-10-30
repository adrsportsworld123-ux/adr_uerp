package farm

import "testing"

func TestPurposes(t *testing.T) {
	for species, ok := range map[string]bool{"chicken": true, "fish": true, "dog": false} {
		if validSpecies(species) != ok {
			t.Errorf("validSpecies(%q) = %v", species, !ok)
		}
	}
	if !validPurpose("chicken", "layer") || validPurpose("chicken", "dairy") || !validPurpose("cow", "dairy") || validPurpose("fish", "broiler") {
		t.Error("purposes per species are wrong")
	}
}

func TestTrimNum(t *testing.T) {
	for v, want := range map[float64]string{1700: "1700", 0.5: "0.5", 2.25: "2.25", 0: "0"} {
		if got := trimNum(v); got != want {
			t.Errorf("trimNum(%v) = %q, want %q", v, got, want)
		}
	}
}
