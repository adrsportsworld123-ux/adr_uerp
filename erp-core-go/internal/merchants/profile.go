// Package merchants is Merchant Management (migrations/065): the company
// profile a Merchant Admin keeps up to date themselves (Settings → Company
// Profile, merchant.profile), and the same profile plus the account's
// status and its admins' passwords in the SaaS console (platform staff).
//
// merchants has no RLS (a login resolves its tenant through it), so every
// statement here names the merchant by id: the tenant's own in the
// back office, the path's in the console.
package merchants

import (
	"context"
	"encoding/json"
	"net/mail"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/taxes"
)

// Address is the registered address; state_code is a GST state code
// (taxes.States) when given.
type Address struct {
	Line1     string `json:"line1"`
	Line2     string `json:"line2"`
	City      string `json:"city"`
	State     string `json:"state"`
	StateCode string `json:"state_code"`
	Pincode   string `json:"pincode"`
	Country   string `json:"country"`
}

// Lines is the address as printed, one line per part, empty parts left out.
func (a Address) Lines() []string {
	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	add(a.Line1)
	add(a.Line2)
	cityLine := strings.TrimSpace(strings.Join(nonEmpty(a.City, a.State), ", "))
	if a.Pincode != "" {
		cityLine = strings.TrimSpace(cityLine + " " + a.Pincode)
	}
	add(cityLine)
	if !strings.EqualFold(a.Country, "india") {
		add(a.Country)
	}
	return out
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Profile is a merchant's company profile. Code is the sign-in key and is
// never edited here.
type Profile struct {
	MerchantID string  `json:"merchant_id"`
	Code       string  `json:"code"`
	LegalName  string  `json:"legal_name"`
	TradeName  string  `json:"trade_name"`
	GSTIN      string  `json:"gstin"`
	PAN        string  `json:"pan"`
	Email      string  `json:"email"`
	Phone      string  `json:"phone"`
	Website    string  `json:"website"`
	Address    Address `json:"address"`
	Status     string  `json:"status"`
	// StatusReason / StatusChangedAt: why and when the console last
	// changed the status.
	StatusReason    string  `json:"status_reason"`
	StatusChangedAt *string `json:"status_changed_at"`
	CreatedAt       string  `json:"created_at"`
}

// Querier is a pgx transaction or pool.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Load reads one merchant's profile.
func Load(ctx context.Context, q Querier, merchantID string) (Profile, error) {
	var p Profile
	var addr []byte
	err := q.QueryRow(ctx, `
		SELECT id::text, code, legal_name, COALESCE(trade_name, ''), COALESCE(gstin, ''), COALESCE(pan, ''),
		       COALESCE(email, ''), COALESCE(phone, ''), COALESCE(website, ''), address, status,
		       COALESCE(status_reason, ''), status_changed_at::text, created_at::text
		FROM merchants WHERE id::text = $1`, merchantID,
	).Scan(&p.MerchantID, &p.Code, &p.LegalName, &p.TradeName, &p.GSTIN, &p.PAN, &p.Email, &p.Phone, &p.Website,
		&addr, &p.Status, &p.StatusReason, &p.StatusChangedAt, &p.CreatedAt)
	if err == nil && len(addr) > 0 {
		_ = json.Unmarshal(addr, &p.Address)
	}
	return p, err
}

// Update is the editable part of a profile; nil = unchanged.
type Update struct {
	LegalName *string  `json:"legal_name"`
	TradeName *string  `json:"trade_name"`
	GSTIN     *string  `json:"gstin"`
	PAN       *string  `json:"pan"`
	Email     *string  `json:"email"`
	Phone     *string  `json:"phone"`
	Website   *string  `json:"website"`
	Address   *Address `json:"address"`
}

var (
	gstinPattern   = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$`)
	panPattern     = regexp.MustCompile(`^[A-Z]{5}[0-9]{4}[A-Z]$`)
	phonePattern   = regexp.MustCompile(`^[+0-9 ()-]{6,20}$`)
	pincodePattern = regexp.MustCompile(`^[0-9A-Za-z -]{3,10}$`)
)

// Apply merges u into p after trimming and validating it; it returns a
// client-facing message, or "" when the result is valid.
func (u Update) Apply(p *Profile) string {
	set := func(dst *string, src *string, upper bool) {
		if src != nil {
			v := strings.TrimSpace(*src)
			if upper {
				v = strings.ToUpper(v)
			}
			*dst = v
		}
	}
	set(&p.LegalName, u.LegalName, false)
	set(&p.TradeName, u.TradeName, false)
	set(&p.GSTIN, u.GSTIN, true)
	set(&p.PAN, u.PAN, true)
	set(&p.Email, u.Email, false)
	set(&p.Phone, u.Phone, false)
	set(&p.Website, u.Website, false)
	if u.Address != nil {
		a := *u.Address
		for _, f := range []*string{&a.Line1, &a.Line2, &a.City, &a.State, &a.StateCode, &a.Pincode, &a.Country} {
			*f = strings.TrimSpace(*f)
		}
		if a.StateCode != "" && a.State == "" {
			a.State = taxes.StateName(a.StateCode)
		}
		p.Address = a
	}
	switch {
	case p.LegalName == "":
		return "legal_name is required"
	case len(p.LegalName) > 200 || len(p.TradeName) > 200:
		return "legal_name and trade_name are at most 200 characters"
	case p.GSTIN != "" && !gstinPattern.MatchString(p.GSTIN):
		return "gstin is not a valid 15-character GSTIN"
	case p.PAN != "" && !panPattern.MatchString(p.PAN):
		return "pan is not a valid PAN (5 letters, 4 digits, 1 letter)"
	case p.GSTIN != "" && p.PAN != "" && p.GSTIN[2:12] != p.PAN:
		return "the GSTIN does not contain this PAN"
	case p.Email != "" && !validEmail(p.Email):
		return "email is not a valid address"
	case p.Phone != "" && !phonePattern.MatchString(p.Phone):
		return "phone is not a valid phone number"
	case len(p.Website) > 200:
		return "website is at most 200 characters"
	case p.Address.StateCode != "" && !taxes.ValidState(p.Address.StateCode):
		return "address.state_code is not a GST state code"
	case p.Address.Pincode != "" && !pincodePattern.MatchString(p.Address.Pincode):
		return "address.pincode is not valid"
	case len(p.Address.Line1) > 200 || len(p.Address.Line2) > 200 || len(p.Address.City) > 100:
		return "address lines are at most 200 characters"
	}
	return ""
}

func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

// Save writes the editable fields of p.
func Save(ctx context.Context, tx pgx.Tx, p Profile) error {
	addr, _ := json.Marshal(p.Address)
	if p.Address == (Address{}) {
		addr = nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE merchants SET legal_name = $2, trade_name = NULLIF($3, ''), gstin = NULLIF($4, ''), pan = NULLIF($5, ''),
		       email = NULLIF($6, ''), phone = NULLIF($7, ''), website = NULLIF($8, ''), address = $9, updated_at = now()
		WHERE id::text = $1`,
		p.MerchantID, p.LegalName, p.TradeName, p.GSTIN, p.PAN, p.Email, p.Phone, p.Website, addr)
	return err
}

// AddressLines is the merchant's registered address as letterhead lines,
// for a branch whose print settings give none (internal/documents).
func AddressLines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var a Address
	if json.Unmarshal(raw, &a) != nil {
		return nil
	}
	return a.Lines()
}
