package catalog

import "testing"

// TestComputeEAN13CheckDigit is checked against GS1's own published
// worked example, not an invented one — a real-world code, not just
// internally self-consistent arithmetic.
func TestComputeEAN13CheckDigit(t *testing.T) {
	tests := []struct {
		payload string
		want    int
	}{
		{"400638133393", 1}, // GS1 worked example -> 4006381333931
		{"629104150021", 3}, // a second published EAN-13 example -> 6291041500213
		{"000000000000", 0},
	}
	for _, tt := range tests {
		got, err := computeEAN13CheckDigit(tt.payload)
		if err != nil {
			t.Fatalf("computeEAN13CheckDigit(%q): %v", tt.payload, err)
		}
		if got != tt.want {
			t.Errorf("computeEAN13CheckDigit(%q) = %d, want %d", tt.payload, got, tt.want)
		}
	}
}

func TestValidateEAN13(t *testing.T) {
	if err := validateEAN13("4006381333931"); err != nil {
		t.Errorf("expected a valid known-good EAN-13 to pass, got %v", err)
	}
	if err := validateEAN13("4006381333932"); err == nil {
		t.Error("expected a wrong check digit to fail validation")
	}
	if err := validateEAN13("40063813339"); err == nil {
		t.Error("expected a too-short code to fail validation")
	}
}

func TestSequentialEAN13CandidateIsValid(t *testing.T) {
	for _, seq := range []int{0, 1, 42, 9999999999} {
		code, err := sequentialEAN13Candidate(seq)
		if err != nil {
			t.Fatalf("sequentialEAN13Candidate(%d): %v", seq, err)
		}
		if len(code) != 13 {
			t.Fatalf("sequentialEAN13Candidate(%d) = %q, want 13 digits", seq, code)
		}
		if code[:2] != internalUsePrefix {
			t.Errorf("sequentialEAN13Candidate(%d) = %q, want %q prefix (GS1 in-store-use range)", seq, code, internalUsePrefix)
		}
		if err := validateEAN13(code); err != nil {
			t.Errorf("sequentialEAN13Candidate(%d) = %q is not check-digit-valid: %v", seq, code, err)
		}
	}
}

func TestRandomEAN13CandidateIsValid(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		code, err := randomEAN13Candidate()
		if err != nil {
			t.Fatalf("randomEAN13Candidate: %v", err)
		}
		if err := validateEAN13(code); err != nil {
			t.Errorf("randomEAN13Candidate = %q is not check-digit-valid: %v", code, err)
		}
		seen[code] = true
	}
	if len(seen) < 45 { // allow a small amount of collision noise, not a real risk at 10 digits of entropy
		t.Errorf("expected mostly-unique random candidates, got only %d distinct out of 50", len(seen))
	}
}
