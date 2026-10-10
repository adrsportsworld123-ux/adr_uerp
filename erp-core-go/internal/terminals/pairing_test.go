package terminals

import (
	"strings"
	"testing"
)

func TestPairingCodeShapeAndAlphabet(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		code, err := newPairingCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != pairingCodeLen {
			t.Fatalf("code %q has length %d, want %d", code, len(code), pairingCodeLen)
		}
		for _, c := range code {
			if !strings.ContainsRune(pairingAlphabet, c) {
				t.Fatalf("code %q contains %q, outside the unambiguous alphabet", code, c)
			}
		}
		if seen[code] {
			t.Fatalf("duplicate code %q in 2000 draws — generator isn't random", code)
		}
		seen[code] = true
	}
}

func TestPairingAlphabetHasNoLookalikes(t *testing.T) {
	for _, c := range "01ILO" {
		if strings.ContainsRune(pairingAlphabet, c) {
			t.Fatalf("alphabet contains look-alike %q", c)
		}
	}
}

// A code typed in lower case or with stray spaces must still match the
// hash stored when it was issued.
func TestHashCodeNormalizes(t *testing.T) {
	if hashCode("ab3k9mxz") != hashCode(" AB3K9MXZ ") {
		t.Fatal("hashCode should ignore case and surrounding whitespace")
	}
	if hashCode("AB3K9MXZ") == hashCode("AB3K9MXY") {
		t.Fatal("different codes must hash differently")
	}
}
