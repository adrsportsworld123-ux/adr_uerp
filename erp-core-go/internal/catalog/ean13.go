package catalog

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
)

// computeEAN13CheckDigit implements the standard EAN-13 checksum: from the
// left, odd positions (1st, 3rd, ...) are weighted x1, even positions x3;
// the check digit is whatever makes the 13-digit total's last digit land
// on a multiple of 10. Verified against GS1's own published worked
// example: payload "400638133393" -> check digit 1 (i.e. the real-world
// code "4006381333931").
func computeEAN13CheckDigit(payload string) (int, error) {
	if len(payload) != 12 {
		return 0, fmt.Errorf("EAN-13 payload must be exactly 12 digits, got %d", len(payload))
	}
	sum := 0
	for i, c := range payload {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("EAN-13 payload must be all digits")
		}
		d := int(c - '0')
		if i%2 == 0 {
			sum += d
		} else {
			sum += d * 3
		}
	}
	return (10 - sum%10) % 10, nil
}

// validateEAN13 checks a supplier-provided 13-digit code's check digit is
// actually correct — catching a fat-fingered barcode at entry time
// instead of at the register.
func validateEAN13(code string) error {
	if len(code) != 13 {
		return fmt.Errorf("EAN-13 code must be exactly 13 digits")
	}
	check, err := computeEAN13CheckDigit(code[:12])
	if err != nil {
		return err
	}
	if int(code[12]-'0') != check {
		return fmt.Errorf("EAN-13 check digit is invalid for this code")
	}
	return nil
}

// GS1 reserves the 20-29 prefix range for internal/in-store use — a code
// generated with this prefix can never collide with a real, globally
// assigned EAN-13, which is exactly what auto-generation needs
// (pos_frd_complete.md §12: "Auto: Sequential with check digit").
const internalUsePrefix = "20"

func buildEAN13(bodyDigits string) (string, error) {
	payload := internalUsePrefix + bodyDigits
	check, err := computeEAN13CheckDigit(payload)
	if err != nil {
		return "", err
	}
	return payload + strconv.Itoa(check), nil
}

// sequentialEAN13Candidate builds the next in-sequence code given how many
// internal-use codes already exist for the tenant.
func sequentialEAN13Candidate(seq int) (string, error) {
	return buildEAN13(fmt.Sprintf("%010d", seq))
}

// randomEAN13Candidate is the fallback used only if the sequential
// candidate collides — two concurrent requests computing the same
// sequence number — which a 10-digit random body makes vanishingly
// unlikely to collide with in turn.
func randomEAN13Candidate() (string, error) {
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteByte(byte('0' + rand.IntN(10)))
	}
	return buildEAN13(b.String())
}
