// Package taxes is the one place a document's tax is split for printing
// and reporting (migrations/060_tax_split.sql):
//
//   - "gst_split" (India, the default): an intra-state supply shows CGST
//     and SGST separately, an inter-state one IGST. Per line,
//     CGST = round((tax - cess) / 2), SGST = the rest; IGST = tax - cess.
//     The SQL functions gst_cgst / gst_sgst / gst_igst apply the same rule
//     in reports, so an invoice and GSTR-1 can never disagree.
//   - "single": one tax line under the merchant's own label (VAT, Sales
//     Tax, GST) — for merchants outside India.
//
// The tax amount itself is never changed here: the split always adds up
// to the line's (and the document's) tax_amount / tax_total, to the paisa.
package taxes

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

const (
	RegimeGSTSplit = "gst_split"
	RegimeSingle   = "single"

	DefaultLabel = "GST"
)

// Settings is a branch's effective tax configuration: its own override,
// else the merchant's default, else gst_split / "GST".
type Settings struct {
	Regime    string `json:"regime"`
	TaxLabel  string `json:"tax_label"`
	StateCode string `json:"state_code"` // the GST state the branch supplies from; "" = not known
	// The money's currency (migrations/061): ISO 4217 code and printed
	// symbol, INR / ₹ unless the merchant or branch set another.
	CurrencyCode   string `json:"currency_code"`
	CurrencySymbol string `json:"currency_symbol"`
}

// Split reports whether documents print CGST/SGST/IGST.
func (s Settings) Split() bool { return s.Regime != RegimeSingle }

// Load resolves a branch's effective settings inside the caller's tenant
// transaction.
func Load(ctx context.Context, tx pgx.Tx, branchID string) (Settings, error) {
	var s Settings
	// Code and symbol are set as a pair at one level, so they're taken
	// from the same level: the branch's when it has one, else the merchant's.
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(bs.regime, ms.regime, 'gst_split'), COALESCE(bs.tax_label, ms.tax_label, 'GST'),
		       COALESCE(app_branch_state(b.id), ''),
		       COALESCE(bs.currency_code, ms.currency_code, 'INR'),
		       CASE WHEN bs.currency_code IS NOT NULL THEN bs.currency_symbol ELSE COALESCE(ms.currency_symbol, '₹') END
		FROM branches b
		LEFT JOIN tax_settings bs ON bs.branch_id = b.id
		LEFT JOIN tax_settings ms ON ms.merchant_id = b.merchant_id AND ms.branch_id IS NULL
		WHERE b.id::text = $1`, branchID).Scan(&s.Regime, &s.TaxLabel, &s.StateCode, &s.CurrencyCode, &s.CurrencySymbol)
	return s, err
}

// Currency is how money is named and grouped on a document or screen.
type Currency struct {
	Code   string `json:"code"`
	Symbol string `json:"symbol"`
}

// Indian reports whether amounts group the Indian way (1,23,456.00):
// only for INR; every other currency groups in thousands (123,456.00).
func (c Currency) Indian() bool { return c.Code == "" || c.Code == "INR" }

// Currency of the effective settings.
func (s Settings) Currency() Currency {
	return Currency{Code: s.CurrencyCode, Symbol: s.CurrencySymbol}
}

// MerchantCurrency is the merchant's own currency (the back office's),
// INR / ₹ when unset.
func MerchantCurrency(ctx context.Context, tx pgx.Tx) (Currency, error) {
	c := Currency{Code: "INR", Symbol: "₹"}
	err := tx.QueryRow(ctx, `SELECT COALESCE(currency_code, 'INR'), COALESCE(currency_symbol, '₹') FROM tax_settings WHERE branch_id IS NULL`).
		Scan(&c.Code, &c.Symbol)
	if err == pgx.ErrNoRows {
		return c, nil
	}
	return c, err
}

// Currencies the settings screen offers (any ISO code is accepted).
func Currencies() []Currency {
	return []Currency{
		{"INR", "₹"}, {"USD", "$"}, {"EUR", "€"}, {"GBP", "£"}, {"AED", "AED"}, {"SAR", "SAR"}, {"QAR", "QAR"},
		{"OMR", "OMR"}, {"KWD", "KWD"}, {"BHD", "BHD"}, {"SGD", "S$"}, {"MYR", "RM"}, {"LKR", "Rs"}, {"NPR", "Rs"},
		{"BDT", "৳"}, {"AUD", "A$"}, {"NZD", "NZ$"}, {"CAD", "C$"}, {"ZAR", "R"}, {"KES", "KSh"},
	}
}

// Amounts is one line's (or a document's) tax, split.
type Amounts struct {
	CGST, SGST, IGST, Cess float64
}

// SplitLine splits a line's tax (cess included in tax) — the Go twin of
// gst_cgst / gst_sgst / gst_igst. Worked in paise so the halves always
// add back up exactly.
func SplitLine(tax, cess float64, inter bool) Amounts {
	t, c := paise(tax), paise(cess)
	g := t - c
	if inter {
		return Amounts{IGST: rupees(g), Cess: rupees(c)}
	}
	half := halfAway(g)
	return Amounts{CGST: rupees(half), SGST: rupees(g - half), Cess: rupees(c)}
}

// Line is one document line's tax figures.
type Line struct {
	Rate    float64 // GST rate, cess excluded
	Taxable float64
	Tax     float64 // cess included
	Cess    float64
}

// Row is one printed tax line under a document's subtotal ("CGST @ 2.5%").
type Row struct {
	Label  string `json:"label"`
	Amount string `json:"amount"`
}

// Bucket is one rate of a document's tax summary.
type Bucket struct {
	Rate    string `json:"rate"`
	Taxable string `json:"taxable_value"`
	CGST    string `json:"cgst"`
	SGST    string `json:"sgst"`
	IGST    string `json:"igst"`
	Cess    string `json:"cess"`
	Tax     string `json:"tax_amount"`
}

// Breakup is a document's tax, split and summarised, ready to show. It
// is additive on every response that carries it (tax_breakup).
type Breakup struct {
	Regime            string   `json:"regime"`
	TaxLabel          string   `json:"tax_label"`
	InterState        bool     `json:"inter_state"`
	PlaceOfSupply     string   `json:"place_of_supply"` // 2-digit GST state code; "" = not known
	PlaceOfSupplyName string   `json:"place_of_supply_name"`
	CGST              string   `json:"cgst"`
	SGST              string   `json:"sgst"`
	IGST              string   `json:"igst"`
	Cess              string   `json:"cess"`
	Total             string   `json:"total"`
	Rows              []Row    `json:"rows"`
	Summary           []Bucket `json:"summary"`
}

// Build splits a document's lines under its branch's settings. inter and
// pos are the document's own supply (sales_orders.inter_state /
// place_of_supply); a single-tax regime ignores them.
func Build(s Settings, inter bool, pos string, lines []Line) Breakup {
	b := Breakup{Regime: s.Regime, TaxLabel: s.TaxLabel, PlaceOfSupply: pos, PlaceOfSupplyName: StateName(pos)}
	if !s.Split() {
		inter = false
	}
	b.InterState = inter

	type acc struct{ taxable, cgst, sgst, igst, cess, tax int64 }
	buckets := map[float64]*acc{}
	var tot acc
	for _, l := range lines {
		a := SplitLine(l.Tax, l.Cess, inter)
		rate := math.Round(l.Rate*100) / 100
		if buckets[rate] == nil {
			buckets[rate] = &acc{}
		}
		for _, x := range []*acc{buckets[rate], &tot} {
			x.taxable += paise(l.Taxable)
			x.cgst += paise(a.CGST)
			x.sgst += paise(a.SGST)
			x.igst += paise(a.IGST)
			x.cess += paise(a.Cess)
			x.tax += paise(l.Tax)
		}
	}
	rates := make([]float64, 0, len(buckets))
	for r := range buckets {
		rates = append(rates, r)
	}
	sort.Float64s(rates)
	b.Summary = make([]Bucket, 0, len(rates))
	for _, r := range rates {
		a := buckets[r]
		b.Summary = append(b.Summary, Bucket{
			Rate: money(r), Taxable: money(rupees(a.taxable)), CGST: money(rupees(a.cgst)), SGST: money(rupees(a.sgst)),
			IGST: money(rupees(a.igst)), Cess: money(rupees(a.cess)), Tax: money(rupees(a.tax)),
		})
	}
	b.CGST, b.SGST, b.IGST = money(rupees(tot.cgst)), money(rupees(tot.sgst)), money(rupees(tot.igst))
	b.Cess, b.Total = money(rupees(tot.cess)), money(rupees(tot.tax))

	// One printed line per tax, with its rate when the whole document is
	// at a single rate ("CGST @ 2.5%"); several rates are itemised in
	// the summary instead.
	at := func(rate float64) string {
		if len(rates) == 1 && rates[0] > 0 {
			return " @ " + FormatRate(rate) + "%"
		}
		return ""
	}
	single := 0.0
	if len(rates) == 1 {
		single = rates[0]
	}
	switch {
	case !s.Split():
		b.Rows = []Row{{Label: s.TaxLabel + at(single), Amount: b.Total}}
	case inter:
		b.Rows = []Row{{Label: "IGST" + at(single), Amount: b.IGST}}
	default:
		b.Rows = []Row{{Label: "CGST" + at(single/2), Amount: b.CGST}, {Label: "SGST" + at(single/2), Amount: b.SGST}}
	}
	if s.Split() && tot.cess != 0 {
		b.Rows = append(b.Rows, Row{Label: "Cess", Amount: b.Cess})
	}
	return b
}

// SplitTotal splits a document-level tax figure (a purchase bill's
// supplier tax) the same way as a line.
func SplitTotal(tax, cess float64, inter bool) Amounts { return SplitLine(tax, cess, inter) }

// FormatRate prints a rate without trailing zeros: 18, 2.5, 0.25.
func FormatRate(r float64) string {
	return strconv.FormatFloat(math.Round(r*100)/100, 'f', -1, 64)
}

// IsZero reports whether a NUMERIC string is zero ("", "0", "0.00", "-0.00").
func IsZero(s string) bool { return strings.Trim(strings.TrimSpace(s), "-0.") == "" }

func paise(v float64) int64  { return int64(math.Round(v * 100)) }
func rupees(p int64) float64 { return float64(p) / 100 }
func money(v float64) string { return fmt.Sprintf("%.2f", v) }
func halfAway(p int64) int64 {
	if p >= 0 {
		return (p + 1) / 2
	}
	return -((-p + 1) / 2)
}
