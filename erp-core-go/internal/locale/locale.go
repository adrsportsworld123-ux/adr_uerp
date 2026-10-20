// Package locale is the merchant's localization (migrations/064): the
// language every screen speaks, the business time zone, the text direction
// and POS cart side (both overridable per POS terminal), and the cash
// denominations counted at a shift close. Currency itself is
// tax_settings' (internal/taxes); the denominations default from it.
package locale

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
	"erp-core-go/internal/taxes"
)

// Languages the apps are translated into.
var Languages = []Language{
	{"en", "English", "English", "ltr"},
	{"ta", "Tamil", "தமிழ்", "ltr"},
	{"hi", "Hindi", "हिन्दी", "ltr"},
	{"ar", "Arabic", "العربية", "rtl"},
	{"ml", "Malayalam", "മലയാളം", "ltr"},
	{"te", "Telugu", "తెలుగు", "ltr"},
	{"kn", "Kannada", "ಕನ್ನಡ", "ltr"},
	{"fr", "French", "Français", "ltr"},
	{"es", "Spanish", "Español", "ltr"},
}

type Language struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Native    string `json:"native_name"`
	Direction string `json:"direction"`
}

func languageDirection(code string) string {
	for _, l := range Languages {
		if l.Code == code {
			return l.Direction
		}
	}
	return "ltr"
}

// ValidLanguage reports whether code is one of Languages.
func ValidLanguage(code string) bool {
	for _, l := range Languages {
		if l.Code == code {
			return true
		}
	}
	return false
}

const DefaultTimezone = "Asia/Kolkata"

// Settings is the merchant's row (or the defaults when there is none).
type Settings struct {
	Language      string   `json:"language"`
	Timezone      string   `json:"timezone"`
	TextDirection string   `json:"text_direction"` // auto | ltr | rtl
	CartSide      string   `json:"cart_side"`      // right | left
	Denominations []string `json:"denominations"`  // null = the currency's standard set
}

// Load reads the merchant's settings inside a tenant transaction.
func Load(ctx context.Context, tx pgx.Tx) (Settings, error) {
	s := Settings{Language: "en", Timezone: DefaultTimezone, TextDirection: "auto", CartSide: "right"}
	var denoms []string
	err := tx.QueryRow(ctx, `
		SELECT language, timezone, text_direction, cart_side, denominations::text[]
		FROM localization_settings`).Scan(&s.Language, &s.Timezone, &s.TextDirection, &s.CartSide, &denoms)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	s.Denominations = denoms
	return s, err
}

// Location is the merchant's business time zone (IST when unset or
// unreadable, the clock every report used before migrations/064).
func Location(ctx context.Context, tx pgx.Tx) *time.Location {
	var name string
	if err := tx.QueryRow(ctx, `SELECT app_merchant_tz()`).Scan(&name); err == nil {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return IST
}

// IST is the fallback clock.
var IST = func() *time.Location {
	if loc, err := time.LoadLocation(DefaultTimezone); err == nil {
		return loc
	}
	return time.FixedZone("IST", 5*3600+30*60)
}()

// Effective is what one screen (a POS terminal, or the back office) uses.
type Effective struct {
	Language      string `json:"language"`
	Direction     string `json:"direction"` // ltr | rtl, resolved
	TextDirection string `json:"text_direction"`
	CartSide      string `json:"cart_side"`
	Timezone      string `json:"timezone"`
	// UTCOffsetMinutes is the time zone's offset right now, for a client
	// with no time zone database (the POS shows times on this clock).
	UTCOffsetMinutes int            `json:"utc_offset_minutes"`
	CurrencyCode     string         `json:"currency_code"`
	CurrencySymbol   string         `json:"currency_symbol"`
	Denominations    []string       `json:"denominations"`
	Currency         taxes.Currency `json:"-"`
}

// Resolve applies a terminal's overrides (nil = the merchant's) and fills
// in the defaults: auto direction from the language, denominations from
// the currency.
func Resolve(s Settings, cur taxes.Currency, termDirection, termCartSide *string) Effective {
	e := Effective{Language: s.Language, TextDirection: s.TextDirection, CartSide: s.CartSide, Timezone: s.Timezone,
		CurrencyCode: cur.Code, CurrencySymbol: cur.Symbol, Currency: cur}
	if termDirection != nil {
		e.TextDirection = *termDirection
	}
	if termCartSide != nil {
		e.CartSide = *termCartSide
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		loc = IST
	}
	_, off := time.Now().In(loc).Zone()
	e.UTCOffsetMinutes = off / 60
	e.Direction = e.TextDirection
	if e.Direction == "auto" || e.Direction == "" {
		e.Direction = languageDirection(e.Language)
	}
	e.Denominations = s.Denominations
	if len(e.Denominations) == 0 {
		e.Denominations = DefaultDenominations(cur.Code)
	}
	return e
}

// DefaultDenominations: the notes and coins in everyday use, largest
// first. INR's is the set the POS and cash reconciliation always used.
func DefaultDenominations(code string) []string {
	sets := map[string][]float64{
		"INR": {2000, 500, 200, 100, 50, 20, 10, 5, 2, 1},
		"USD": {100, 50, 20, 10, 5, 1, 0.25, 0.1, 0.05, 0.01},
		"EUR": {500, 200, 100, 50, 20, 10, 5, 2, 1, 0.5, 0.2, 0.1, 0.05},
		"GBP": {50, 20, 10, 5, 2, 1, 0.5, 0.2, 0.1, 0.05},
		"AED": {1000, 500, 200, 100, 50, 20, 10, 5, 1, 0.5, 0.25},
		"SAR": {500, 200, 100, 50, 10, 5, 2, 1, 0.5, 0.25},
		"QAR": {500, 100, 50, 10, 5, 1},
		"OMR": {50, 20, 10, 5, 1, 0.5, 0.1, 0.05},
		"KWD": {20, 10, 5, 1, 0.5, 0.25, 0.1, 0.05},
		"BHD": {20, 10, 5, 1, 0.5, 0.1, 0.05},
		"SGD": {100, 50, 10, 5, 2, 1, 0.5, 0.2, 0.1, 0.05},
		"MYR": {100, 50, 20, 10, 5, 1, 0.5, 0.2, 0.1, 0.05},
		"LKR": {5000, 1000, 500, 100, 50, 20, 10, 5, 2, 1},
		"NPR": {1000, 500, 100, 50, 20, 10, 5, 2, 1},
		"BDT": {1000, 500, 200, 100, 50, 20, 10, 5, 2, 1},
		"AUD": {100, 50, 20, 10, 5, 2, 1, 0.5, 0.2, 0.1, 0.05},
		"NZD": {100, 50, 20, 10, 5, 2, 1, 0.5, 0.2, 0.1},
		"CAD": {100, 50, 20, 10, 5, 2, 1, 0.25, 0.1, 0.05},
		"ZAR": {200, 100, 50, 20, 10, 5, 2, 1, 0.5},
		"KES": {1000, 500, 200, 100, 50, 20, 10, 5, 1},
	}
	set, ok := sets[code]
	if !ok {
		set = []float64{1000, 500, 100, 50, 20, 10, 5, 1}
	}
	out := make([]string, len(set))
	for i, v := range set {
		out[i] = strconv.FormatFloat(v, 'f', 2, 64)
	}
	return out
}

// UserPrefs is one user's own language and text direction
// (migrations/065); nil = the merchant's.
type UserPrefs struct {
	Language      *string `json:"language"`
	TextDirection *string `json:"text_direction"`
}

// LoadUser reads a user's own preferences inside a tenant transaction
// (empty for an unknown user).
func LoadUser(ctx context.Context, tx pgx.Tx, userID string) UserPrefs {
	var u UserPrefs
	_ = tx.QueryRow(ctx, `SELECT language, text_direction FROM users WHERE id::text = $1`, userID).Scan(&u.Language, &u.TextDirection)
	return u
}

// ForUser is the merchant's settings with a user's own language and
// direction on top. A terminal's own direction still wins over both
// (Resolve), since that is about the screen, not the person. The business
// clock and the denominations stay the merchant's.
func (s Settings) ForUser(u UserPrefs) Settings {
	if u.Language != nil && ValidLanguage(*u.Language) {
		s.Language = *u.Language
		// A user's language with no direction of their own reads in its
		// own direction, not the merchant's forced one.
		if u.TextDirection == nil {
			s.TextDirection = "auto"
		}
	}
	if u.TextDirection != nil {
		s.TextDirection = *u.TextDirection
	}
	return s
}

// MerchantLocation is Location in a short transaction of its own, for a
// handler that needs the clock before it opens one (parsing a date).
func MerchantLocation(ctx context.Context, database *db.DB, tenantID string) *time.Location {
	loc := IST
	_ = database.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		loc = Location(ctx, tx)
		return nil
	})
	return loc
}
