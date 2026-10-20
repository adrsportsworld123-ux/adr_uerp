package documents

import (
	"bytes"
	"testing"

	"erp-core-go/internal/taxes"
)

func TestAmountInWords(t *testing.T) {
	inr := taxes.Currency{Code: "INR", Symbol: "₹"}
	for _, tc := range []struct {
		v    float64
		want string
	}{
		{1, "Rupees One Only"},
		{15, "Rupees Fifteen Only"},
		{100, "Rupees One Hundred Only"},
		{1000.5, "Rupees One Thousand and Fifty Paise Only"},
		{123456.5, "Rupees One Lakh Twenty Three Thousand Four Hundred Fifty Six and Fifty Paise Only"},
		{10000000, "Rupees One Crore Only"},
		{250000099.99, "Rupees Twenty Five Crore Ninety Nine and Ninety Nine Paise Only"},
	} {
		if got := AmountInWords(tc.v, inr); got != tc.want {
			t.Errorf("%.2f: %q, want %q", tc.v, got, tc.want)
		}
	}
	if got := AmountInWords(1234567.25, taxes.Currency{Code: "AED", Symbol: "AED"}); got != "AED One Million Two Hundred Thirty Four Thousand Five Hundred Sixty Seven and Twenty Five/100 Only" {
		t.Errorf("AED: %q", got)
	}
}

func TestChequeFigures(t *testing.T) {
	if got := chequeFigures(123456.5, taxes.Currency{Code: "INR"}); got != "1,23,456.50" {
		t.Errorf("INR: %s", got)
	}
	if got := chequeFigures(12345678, taxes.Currency{Code: "INR"}); got != "1,23,45,678.00" {
		t.Errorf("INR crore: %s", got)
	}
	if got := chequeFigures(1234567, taxes.Currency{Code: "USD"}); got != "1,234,567.00" {
		t.Errorf("USD: %s", got)
	}
	if got := chequeFigures(999, taxes.Currency{Code: "INR"}); got != "999.00" {
		t.Errorf("small: %s", got)
	}
}

func TestRenderCheque(t *testing.T) {
	pdf, err := RenderCheque(Cheque{PayeeName: "Sharma Traders", Amount: 25000, Date: "2026-10-21", AccountPayee: true,
		Currency: taxes.Currency{Code: "INR", Symbol: "₹"}, OffsetX: 1.5, SignatoryLine: "For ADR Sports World"})
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF")) || len(pdf) < 1000 {
		t.Fatalf("render: %v, %d bytes", err, len(pdf))
	}
	if got := dateDigits("2026-10-21"); got != "21102026" {
		t.Errorf("date digits: %s", got)
	}
	a, b := splitWords("Rupees One Lakh Twenty Three Thousand Four Hundred Fifty Six and Fifty Paise Only", 40)
	if len(a) > 40 || a+" "+b != "Rupees One Lakh Twenty Three Thousand Four Hundred Fifty Six and Fifty Paise Only" {
		t.Errorf("split: %q | %q", a, b)
	}
}

// The leaf is landscape: 202 × 92 mm (572.6 × 260.8 pt).
func TestRenderChequePageSize(t *testing.T) {
	pdf, err := RenderCheque(Cheque{PayeeName: "X", Amount: 1, Date: "2026-10-21", Currency: taxes.Currency{Code: "INR"}})
	if err != nil || !bytes.Contains(pdf, []byte("/MediaBox [0 0 572.60 260.79]")) {
		t.Fatalf("page size: %v", err)
	}
}
