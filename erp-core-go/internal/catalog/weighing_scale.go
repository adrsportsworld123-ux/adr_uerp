// Phase 8: Vertical Expansion — Grocery/FMCG's "weighing-scale barcode
// integration". A weighing scale prints a barcode encoding the specific
// weight of whatever was just put on it (loose produce, deli items) —
// not a fixed catalog price the way a normal SKU barcode is. This
// decodes the one common convention this pass implements: a 13-digit
// EAN-13-shaped code, prefix "21", followed by a 5-digit PLU (product
// lookup) code and a 5-digit weight in grams, with a real EAN-13 check
// digit over the first 12 digits — so a barcode reader/POS scanner built
// for ordinary EAN-13 still reads it correctly.
//
// The prefix is "21", not just GS1's whole reserved-for-in-store-use "2"
// range (20-29) — found live: this codebase's OWN auto-generated catalog
// barcodes (ean13.go's buildEAN13, internalUsePrefix "20") sit inside
// that same range, and since any code buildEAN13 produces necessarily has
// a valid EAN-13 check digit by construction, an auto-generated code
// (e.g. "2000000000008") satisfied a single-digit "2" prefix check AND
// passed check-digit validation every time, misreading a perfectly
// ordinary catalog barcode as a weighing-scale code and failing lookup
// with "no product matches this barcode" — reproduced live via a real
// Playwright test driving the actual GRN screen, not by reasoning about
// it. "21" is a distinct sub-block of the same reserved range, leaving
// "20" to auto-generated catalog codes and "22"-"29" open for other
// internal uses later.
//
// This is genuinely one real convention among several a store's actual
// scale hardware might use (some encode price-in-paise instead of
// weight-in-grams, some use a different prefix or field width) — the
// codebase's own "configurable masters, not industry-specific code"
// philosophy would eventually make prefix/field-width merchant
// configuration, not a hardcoded format. Scoped down to one, clearly
// documented convention for this pass, the same kind of scope cut this
// session has made elsewhere (e.g. NLP-BI's fixed intent list).
package catalog

import "strconv"

const weighingScalePrefix = "21"

// decodeWeighingScaleBarcode returns (pluCode, weightKg, true) if code
// matches the convention above and its check digit is valid; otherwise
// ("", 0, false) — the caller falls back to the normal registered-
// barcode lookup, so this never breaks a real EAN-13 barcode that simply
// happens to start with the same two digits for an unrelated reason (the
// check-digit validation is what actually distinguishes them, not just
// the prefix).
func decodeWeighingScaleBarcode(code string) (pluCode string, weightKg float64, ok bool) {
	if len(code) != 13 || code[:2] != weighingScalePrefix {
		return "", 0, false
	}
	if err := validateEAN13(code); err != nil {
		return "", 0, false
	}
	pluCode = code[2:7]
	weightGrams, err := strconv.Atoi(code[7:12])
	if err != nil {
		return "", 0, false
	}
	return pluCode, float64(weightGrams) / 1000.0, true
}

// Merchant-configured formats (migrations/084, §3.87): the convention above
// stays the default; a merchant whose scales print something else (another
// prefix, a 4- or 6-digit PLU, or a price in place of the weight) lists
// those formats, and only they are read.

// ScaleFormat is one way a scale lays out its 13 digits: prefix (2) + PLU +
// value + EAN-13 check digit.
type ScaleFormat struct {
	FormatID      string `json:"format_id,omitempty"`
	Prefix        string `json:"prefix"`
	PLUDigits     int    `json:"plu_digits"`
	ValueKind     string `json:"value_kind"` // weight | price
	ValueDigits   int    `json:"value_digits"`
	ValueDecimals int    `json:"value_decimals"`
	Active        bool   `json:"active"`
}

// DefaultScaleFormat: prefix 21, 5-digit PLU, weight in grams.
var DefaultScaleFormat = ScaleFormat{Prefix: weighingScalePrefix, PLUDigits: 5, ValueKind: "weight", ValueDigits: 5, ValueDecimals: 3, Active: true}

// validScalePrefix: 02 or 21-29 (GS1's variable-measure ranges); 20 is this
// system's own generated codes.
func validScalePrefix(p string) bool {
	return p == "02" || (len(p) == 2 && p[0] == '2' && p[1] >= '1' && p[1] <= '9')
}

// Validate returns a client-facing message, or "".
func (f ScaleFormat) Validate() string {
	switch {
	case !validScalePrefix(f.Prefix):
		return "the prefix is 02 or 21 to 29 (20 is this system's own barcodes)"
	case f.PLUDigits < 4 || f.PLUDigits > 6 || f.ValueDigits < 4 || f.ValueDigits > 6 || f.PLUDigits+f.ValueDigits != 10:
		return "the PLU and the value take 4 to 6 digits each, 10 together"
	case f.ValueKind != "weight" && f.ValueKind != "price":
		return "the value is a weight or a price"
	case f.ValueDecimals < 0 || f.ValueDecimals > 3:
		return "the value has 0 to 3 decimals"
	}
	return ""
}

// decodeScale reads code with the first active format whose prefix it
// starts with: the PLU, and the value (kg for a weight, money for a
// price). ok false = not a scale code under these formats.
func decodeScale(code string, formats []ScaleFormat) (plu string, value float64, f ScaleFormat, ok bool) {
	if len(code) != 13 || validateEAN13(code) != nil {
		return "", 0, f, false
	}
	for _, f = range formats {
		if !f.Active || code[:2] != f.Prefix {
			continue
		}
		plu = code[2 : 2+f.PLUDigits]
		n, err := strconv.Atoi(code[2+f.PLUDigits : 12])
		if err != nil {
			return "", 0, f, false
		}
		div := 1.0
		for i := 0; i < f.ValueDecimals; i++ {
			div *= 10
		}
		return plu, float64(n) / div, f, true
	}
	return "", 0, f, false
}
