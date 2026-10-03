// Package taxcalc is the one place a sale/quotation line's tax split is
// computed, for both pricing modes a product can be configured with
// (products.price_includes_tax, migrations/033_tax_inclusive_pricing.sql):
//
//   - Tax-exclusive (the original, and still default, behavior): the price
//     is the taxable value; tax is added on top.
//     taxable = price*qty - discount; tax = taxable*rate; total = taxable + tax.
//
//   - Tax-inclusive (MRP-style shelf pricing): the price already contains
//     tax; tax is extracted from it, never added.
//     total = price*qty - discount; taxable = total*100/(100+rate);
//     tax = total - taxable.
//
// The inclusive split is done on the WHOLE LINE, not per unit, so a line
// of 3 x ₹1000 (18% incl.) always totals exactly ₹3000.00 — per-unit
// back-calculation (847.46 x 3 + tax) would drift to ₹3000.01. The
// rounding residue lands in tax_amount; taxable is what's rounded.
//
// Same float64-then-NUMERIC(14,2) trade-off documented on
// sales.AddLineToCart: every result here is rounded to 2dp before it's
// written, and the destination columns are NUMERIC(14,2).
package taxcalc

import "math"

// Line is one line's computed money split. Taxable + Tax == Total always
// holds exactly (to the paisa) for both modes.
type Line struct {
	Taxable float64
	Tax     float64
	Total   float64
}

// Round2 rounds half away from zero to 2dp.
func Round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// ComputeLine splits a line into taxable value, tax, and total.
// discount is in the same basis as unitPrice (i.e. tax-inclusive for an
// inclusive line — it's a reduction of what the customer pays).
func ComputeLine(unitPrice, quantity, discount, taxRatePct float64, inclusive bool) Line {
	gross := Round2(unitPrice*quantity - discount)
	if inclusive {
		taxable := Round2(gross * 100 / (100 + taxRatePct))
		return Line{Taxable: taxable, Tax: Round2(gross - taxable), Total: gross}
	}
	tax := Round2(gross * taxRatePct / 100)
	return Line{Taxable: gross, Tax: tax, Total: Round2(gross + tax)}
}

// NetPrice is a unit price with tax removed — the figure margin, markup,
// and negative-margin checks must use, since cost_price is always
// tax-exclusive (input GST is claimed back as credit, not a cost). For an
// exclusive price it's the price itself.
func NetPrice(price, taxRatePct float64, inclusive bool) float64 {
	if !inclusive {
		return price
	}
	return Round2(price * 100 / (100 + taxRatePct))
}

// GrossPrice is the inverse of NetPrice: what the customer pays for one
// unit, tax included.
func GrossPrice(price, taxRatePct float64, inclusive bool) float64 {
	if inclusive {
		return price
	}
	return Round2(price * (100 + taxRatePct) / 100)
}
