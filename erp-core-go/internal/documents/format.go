package documents

import "strings"

// Amount groups a NUMERIC money string the Indian way ("123456.5" →
// "1,23,456.50"). String-only, like every money value on its way out of
// this API: the database already rounded it, and a float round-trip
// would only add risk. Anything that isn't a plain decimal is returned
// unchanged.
func Amount(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "0.00"
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, _ := strings.Cut(s, ".")
	for _, r := range intPart + frac {
		if r < '0' || r > '9' {
			return s
		}
	}
	switch {
	case len(frac) == 0:
		frac = "00"
	case len(frac) == 1:
		frac += "0"
	case len(frac) > 2:
		frac = frac[:2] // NUMERIC(14,2) never has more; quantities use Qty
	}
	intPart = strings.TrimLeft(intPart, "0")
	if intPart == "" {
		intPart = "0"
	}
	out := group(intPart) + "." + frac
	if neg && strings.Trim(out, "0.,") != "" {
		out = "-" + out
	}
	return out
}

// INR is Amount with the rupee sign.
func INR(s string) string {
	a := Amount(s)
	if strings.HasPrefix(a, "-") {
		return "-₹" + a[1:]
	}
	return "₹" + a
}

// Qty trims a NUMERIC(14,3) quantity's trailing zeros ("2.000" → "2",
// "1.250" → "1.25").
func Qty(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// group inserts Indian digit separators: the last three digits, then
// pairs (12,34,56,789).
func group(d string) string {
	if len(d) <= 3 {
		return d
	}
	head, tail := d[:len(d)-3], d[len(d)-3:]
	var parts []string
	for len(head) > 2 {
		parts = append([]string{head[len(head)-2:]}, parts...)
		head = head[:len(head)-2]
	}
	if head != "" {
		parts = append([]string{head}, parts...)
	}
	return strings.Join(parts, ",") + "," + tail
}

// Lines splits free text into printed lines, dropping blank ones.
func Lines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Label turns a status/method code into words ("bank_transfer" → "Bank transfer").
func Label(code string) string {
	s := strings.ReplaceAll(code, "_", " ")
	switch strings.ToLower(s) {
	case "upi":
		return "UPI"
	}
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
