package documents

import (
	"fmt"
	"math"
	"strings"

	"github.com/go-pdf/fpdf"

	"erp-core-go/internal/taxes"
)

// Cheque is one cheque leaf to print (migrations/066): the payee, the
// amount in figures and words, the date and the "A/c Payee" crossing,
// positioned for an Indian CTS-2010 leaf (202 × 92 mm). The bank's own
// pre-printing (its name, MICR band, signature box) is already on the
// leaf, so only the filled-in parts are drawn. OffsetX/OffsetY (mm) move
// everything to calibrate a printer and a bank's leaf.
type Cheque struct {
	PayeeName    string
	Amount       float64
	Date         string // YYYY-MM-DD
	AccountPayee bool
	Currency     taxes.Currency
	OffsetX      float64
	OffsetY      float64
	// SignatoryLine names whoever signs ("For ADR Sports"), printed above
	// the signature box; "" = none.
	SignatoryLine string
}

// ChequeWidth and ChequeHeight are the CTS-2010 leaf, in mm.
const (
	ChequeWidth  = 202.0
	ChequeHeight = 92.0
)

// RenderCheque draws one leaf as a PDF the size of the cheque.
func RenderCheque(c Cheque) ([]byte, error) {
	// "P" keeps the size as given (fpdf swaps width and height for "L").
	pdf := newPDF("P", fpdf.SizeType{Wd: ChequeWidth, Ht: ChequeHeight})
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddPage()
	x := func(v float64) float64 { return v + c.OffsetX }
	y := func(v float64) float64 { return v + c.OffsetY }

	if c.AccountPayee {
		// Two parallel lines across the top-left corner, "A/C PAYEE" between.
		pdf.SetLineWidth(0.3)
		pdf.Line(x(6), y(18), x(24), y(2))
		pdf.Line(x(11), y(20), x(29), y(4))
		pdf.SetFont("geist", "B", 7)
		pdf.TransformBegin()
		pdf.TransformRotate(41.6, x(13), y(15))
		pdf.Text(x(12), y(15), "A/C PAYEE")
		pdf.TransformEnd()
	}

	// Date: DDMMYYYY in the eight boxes at the top right.
	pdf.SetFont("geist", "B", 11)
	digits := dateDigits(c.Date)
	for i, d := range digits {
		pdf.Text(x(153.5+float64(i)*5.4), y(12.5), string(d))
	}

	// Payee, after the printed "Pay".
	pdf.SetFont("geist", "B", 11)
	pdf.Text(x(24), y(23), truncate(strings.ToUpper(strings.TrimSpace(c.PayeeName)), 60))

	// Amount in words over the two "Rupees" lines.
	words := AmountInWords(c.Amount, c.Currency)
	first, second := splitWords(words, 62)
	pdf.SetFont("geist", "", 10)
	pdf.Text(x(30), y(32), first)
	if second != "" {
		pdf.Text(x(10), y(40), second)
	}

	// Figures in the amount box, guarded with asterisks.
	pdf.SetFont("geist", "B", 12)
	pdf.Text(x(158), y(40), "**"+chequeFigures(c.Amount, c.Currency)+"/-")

	if s := strings.TrimSpace(c.SignatoryLine); s != "" {
		pdf.SetFont("geist", "", 8)
		pdf.Text(x(150), y(58), truncate(s, 40))
	}
	return output(pdf)
}

func dateDigits(date string) string {
	if len(date) != 10 {
		return ""
	}
	return date[8:10] + date[5:7] + date[0:4]
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// splitWords breaks the words at a space so the first line holds at most
// n characters.
func splitWords(s string, n int) (string, string) {
	if len(s) <= n {
		return s, ""
	}
	cut := strings.LastIndex(s[:n], " ")
	if cut <= 0 {
		cut = n
	}
	return s[:cut], strings.TrimSpace(s[cut:])
}

// chequeFigures: 123456.5 → "1,23,456.50" for INR, "123,456.50" otherwise.
func chequeFigures(v float64, c taxes.Currency) string {
	s := fmt.Sprintf("%.2f", math.Abs(v))
	whole, frac := s[:len(s)-3], s[len(s)-3:]
	if len(whole) > 3 {
		last3 := whole[len(whole)-3:]
		rest := whole[:len(whole)-3]
		step := 2
		if !c.Indian() {
			step = 3
		}
		var groups []string
		for len(rest) > step {
			groups = append([]string{rest[len(rest)-step:]}, groups...)
			rest = rest[:len(rest)-step]
		}
		if rest != "" {
			groups = append([]string{rest}, groups...)
		}
		whole = strings.Join(append(groups, last3), ",")
	}
	return whole + frac
}

var (
	ones = []string{"", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine", "Ten",
		"Eleven", "Twelve", "Thirteen", "Fourteen", "Fifteen", "Sixteen", "Seventeen", "Eighteen", "Nineteen"}
	tens = []string{"", "", "Twenty", "Thirty", "Forty", "Fifty", "Sixty", "Seventy", "Eighty", "Ninety"}
)

func belowHundred(n int) string {
	if n < 20 {
		return ones[n]
	}
	if n%10 == 0 {
		return tens[n/10]
	}
	return tens[n/10] + " " + ones[n%10]
}

func belowThousand(n int) string {
	var parts []string
	if n >= 100 {
		parts = append(parts, ones[n/100]+" Hundred")
		n %= 100
	}
	if n > 0 {
		parts = append(parts, belowHundred(n))
	}
	return strings.Join(parts, " ")
}

// indianWords spells n with crore, lakh and thousand.
func indianWords(n int64) string {
	if n == 0 {
		return "Zero"
	}
	var parts []string
	if n >= 10000000 {
		parts = append(parts, indianWords(n/10000000)+" Crore")
		n %= 10000000
	}
	if n >= 100000 {
		parts = append(parts, belowHundred(int(n/100000))+" Lakh")
		n %= 100000
	}
	if n >= 1000 {
		parts = append(parts, belowHundred(int(n/1000))+" Thousand")
		n %= 1000
	}
	if n > 0 {
		parts = append(parts, belowThousand(int(n)))
	}
	return strings.Join(parts, " ")
}

// westernWords spells n with billion, million and thousand.
func westernWords(n int64) string {
	if n == 0 {
		return "Zero"
	}
	var parts []string
	for _, s := range []struct {
		div  int64
		name string
	}{{1000000000, "Billion"}, {1000000, "Million"}, {1000, "Thousand"}} {
		if n >= s.div {
			parts = append(parts, westernWords(n/s.div)+" "+s.name)
			n %= s.div
		}
	}
	if n > 0 {
		parts = append(parts, belowThousand(int(n)))
	}
	return strings.Join(parts, " ")
}

// AmountInWords: 123456.50 → "Rupees One Lakh Twenty Three Thousand Four
// Hundred Fifty Six and Fifty Paise Only" in INR; another currency is
// named by its code with western grouping ("AED One Thousand … Only").
func AmountInWords(v float64, c taxes.Currency) string {
	v = math.Round(math.Abs(v)*100) / 100
	whole := int64(v)
	fraction := int(math.Round((v - float64(whole)) * 100))
	if c.Indian() {
		s := "Rupees " + indianWords(whole)
		if fraction > 0 {
			s += " and " + belowHundred(fraction) + " Paise"
		}
		return s + " Only"
	}
	s := c.Code + " " + westernWords(whole)
	if fraction > 0 {
		s += " and " + belowHundred(fraction) + "/100"
	}
	return s + " Only"
}
