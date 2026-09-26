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
