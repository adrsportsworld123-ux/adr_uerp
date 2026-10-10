package catalog

import (
	"errors"
	"fmt"
	"strconv"
)

// Phase 13 open-price items (migrations/039_open_price.sql). OpenPrice is
// the one JSON shape every reader uses (barcode scan, product detail, sync
// pull, the cart's OPEN_PRICE_REQUIRED details), and nil means the variant
// has a fixed price.
type OpenPrice struct {
	Min string `json:"min"`
	Max string `json:"max"`
	// ApprovalAbove: a price above this needs a manager's code + PIN;
	// nil = never.
	ApprovalAbove *string `json:"approval_above"`
}

// OpenPriceColumns selects the four columns NewOpenPrice expects, from a
// product_variants alias pv.
const OpenPriceColumns = `pv.open_price, pv.open_price_min::text, pv.open_price_max::text, pv.open_price_approval_above::text`

// NewOpenPrice builds the response value from OpenPriceColumns' scan.
func NewOpenPrice(enabled bool, min, max, approvalAbove *string) *OpenPrice {
	if !enabled || min == nil || max == nil {
		return nil
	}
	return &OpenPrice{Min: *min, Max: *max, ApprovalAbove: approvalAbove}
}

// OpenPriceInput is the write shape (PATCH /products/variants/{id}
// `open_price`): Enabled false clears the range.
type OpenPriceInput struct {
	Enabled       bool     `json:"enabled"`
	Min           float64  `json:"min"`
	Max           float64  `json:"max"`
	ApprovalAbove *float64 `json:"approval_above"`
}

var errOpenPriceRange = errors.New("open price needs 0 ≤ min ≤ max, max above zero, and any approval threshold between min and max")

func (in OpenPriceInput) validate() error {
	if !in.Enabled {
		return nil
	}
	if in.Min < 0 || in.Max <= 0 || in.Min > in.Max {
		return errOpenPriceRange
	}
	if in.ApprovalAbove != nil && (*in.ApprovalAbove < in.Min || *in.ApprovalAbove > in.Max) {
		return errOpenPriceRange
	}
	return nil
}

// CheckOpenPrice says whether an entered price is allowed and whether it
// needs approval. Exported for internal/sales (the cart) and kept here so
// the rule lives next to the data it reads.
func CheckOpenPrice(op OpenPrice, price float64) (inRange, needsApproval bool) {
	min, _ := strconv.ParseFloat(op.Min, 64)
	max, _ := strconv.ParseFloat(op.Max, 64)
	// Compare in paise: a typed 12.10 must equal a stored 12.10.
	p := roundPaise(price)
	if p < roundPaise(min) || p > roundPaise(max) {
		return false, false
	}
	if op.ApprovalAbove != nil {
		above, _ := strconv.ParseFloat(*op.ApprovalAbove, 64)
		return true, p > roundPaise(above)
	}
	return true, false
}

func roundPaise(v float64) int64 {
	if v < 0 {
		return -roundPaise(-v)
	}
	return int64(v*100 + 0.5)
}

func (op OpenPrice) String() string {
	if op.ApprovalAbove == nil {
		return fmt.Sprintf("₹%s–₹%s", op.Min, op.Max)
	}
	return fmt.Sprintf("₹%s–₹%s (approval above ₹%s)", op.Min, op.Max, *op.ApprovalAbove)
}
