package locale

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/taxes"
)

type Handler struct {
	DB *db.DB
}

type settingsResponse struct {
	Settings
	Effective Effective `json:"effective"` // the back office's for this user (no terminal override)
	// User is the signed-in user's own choice (migrations/065); null
	// fields follow the merchant.
	User      UserPrefs  `json:"user"`
	Languages []Language `json:"languages"`
	Timezones []string   `json:"timezones"`
}

func load(ctx context.Context, tx pgx.Tx, userID string) (settingsResponse, error) {
	var r settingsResponse
	var err error
	if r.Settings, err = Load(ctx, tx); err != nil {
		return r, err
	}
	cur, err := taxes.MerchantCurrency(ctx, tx)
	if err != nil {
		return r, err
	}
	r.User = LoadUser(ctx, tx, userID)
	r.Effective = Resolve(r.Settings.ForUser(r.User), cur, nil, nil)
	r.Languages, r.Timezones = Languages, Timezones
	return r, nil
}

// GetSettings: GET /settings/localization — any signed-in user (every
// screen reads its language from here).
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var resp settingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp, err = load(ctx, tx, claims.UserID)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load localization settings")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// UpdateSettings: PUT /settings/localization — localization.manage. Any
// field left out keeps its value; "denominations": null goes back to the
// currency's standard set. apply_timezone_to_branches also moves every
// branch's own clock (receipt times) to the new zone.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var applyTZ bool
	if v, ok := raw["apply_timezone_to_branches"]; ok {
		_ = json.Unmarshal(v, &applyTZ)
	}
	var resp settingsResponse
	var bad string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := Load(ctx, tx)
		if err != nil {
			return err
		}
		next := before
		str := func(key string, set func(string) string) {
			if v, ok := raw[key]; ok && bad == "" {
				var s string
				if json.Unmarshal(v, &s) != nil {
					bad = key + " must be a string"
					return
				}
				bad = set(strings.TrimSpace(s))
			}
		}
		str("language", func(s string) string {
			if !ValidLanguage(s) {
				return "language must be one of en, ta, hi, ar, ml, te, kn, fr, es"
			}
			next.Language = s
			return ""
		})
		str("timezone", func(s string) string {
			if _, err := time.LoadLocation(s); err != nil || s == "" || s == "Local" {
				return "timezone must be an IANA name such as Asia/Kolkata or Asia/Dubai"
			}
			next.Timezone = s
			return ""
		})
		str("text_direction", func(s string) string {
			if s != "auto" && s != "ltr" && s != "rtl" {
				return "text_direction must be auto, ltr or rtl"
			}
			next.TextDirection = s
			return ""
		})
		str("cart_side", func(s string) string {
			if s != "left" && s != "right" {
				return "cart_side must be left or right"
			}
			next.CartSide = s
			return ""
		})
		if v, ok := raw["denominations"]; ok && bad == "" {
			var list []json.Number
			if string(v) == "null" {
				next.Denominations = nil
			} else if json.Unmarshal(v, &list) != nil || len(list) == 0 || len(list) > 20 {
				bad = "denominations must be a list of 1 to 20 positive amounts, or null"
			} else {
				seen := map[string]bool{}
				var vals []float64
				for _, n := range list {
					f, err := n.Float64()
					if err != nil || f <= 0 || f > 1e7 {
						bad = "denominations must be positive amounts"
						break
					}
					k := strconv.FormatFloat(f, 'f', 2, 64)
					if !seen[k] {
						seen[k] = true
						vals = append(vals, f)
					}
				}
				sort.Sort(sort.Reverse(sort.Float64Slice(vals)))
				next.Denominations = make([]string, len(vals))
				for i, f := range vals {
					next.Denominations[i] = strconv.FormatFloat(f, 'f', 2, 64)
				}
			}
		}
		if bad != "" {
			return errBad
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO localization_settings (merchant_id, language, timezone, text_direction, cart_side, denominations, updated_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5::numeric[], $6)
			ON CONFLICT (merchant_id) DO UPDATE SET language = $1, timezone = $2, text_direction = $3, cart_side = $4,
			    denominations = $5::numeric[], updated_by = $6, updated_at = now()`,
			next.Language, next.Timezone, next.TextDirection, next.CartSide, next.Denominations, claims.UserID); err != nil {
			return err
		}
		if applyTZ {
			if _, err := tx.Exec(ctx, `UPDATE branches SET timezone = $1 WHERE timezone <> $1`, next.Timezone); err != nil {
				return err
			}
		}
		if resp, err = load(ctx, tx, claims.UserID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "localization_settings", EntityID: claims.TenantID, Action: "update",
			PerformedBy: claims.UserID, Before: before, After: map[string]any{"settings": resp.Settings, "timezone_applied_to_branches": applyTZ}})
	})
	if errors.Is(err, errBad) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", bad)
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save localization settings")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

var errBad = errors.New("invalid request")

// ParseUserPrefs reads {"language", "text_direction"} for a user's own
// choice: "" or null = follow the merchant. ok=false with a message when a
// value isn't allowed. Fields left out stay nil and set=false.
func ParseUserPrefs(raw map[string]json.RawMessage) (langSet, dirSet bool, lang, dir *string, bad string) {
	read := func(key string) (bool, *string, bool) {
		v, ok := raw[key]
		if !ok {
			return false, nil, true
		}
		var s *string
		if json.Unmarshal(v, &s) != nil {
			return true, nil, false
		}
		if s != nil {
			t := strings.TrimSpace(*s)
			if t == "" {
				s = nil
			} else {
				s = &t
			}
		}
		return true, s, true
	}
	var ok bool
	if langSet, lang, ok = read("language"); !ok || (lang != nil && !ValidLanguage(*lang)) {
		return false, false, nil, nil, "language must be one of en, ta, hi, ar, ml, te, kn, fr, es, or empty for the merchant's"
	}
	if dirSet, dir, ok = read("text_direction"); !ok || (dir != nil && *dir != "auto" && *dir != "ltr" && *dir != "rtl") {
		return false, false, nil, nil, "text_direction must be auto, ltr, rtl, or empty for the merchant's"
	}
	return langSet, dirSet, lang, dir, ""
}

// SaveUserPrefs writes a user's own choice (only the fields that were set).
func SaveUserPrefs(ctx context.Context, tx pgx.Tx, userID string, langSet, dirSet bool, lang, dir *string) error {
	_, err := tx.Exec(ctx, `
		UPDATE users SET language = CASE WHEN $2 THEN $3 ELSE language END,
		                 text_direction = CASE WHEN $4 THEN $5 ELSE text_direction END
		WHERE id::text = $1`, userID, langSet, lang, dirSet, dir)
	return err
}

// UpdateMine: PUT /me/localization {language?, text_direction?} — any
// signed-in user's own language and direction; "" or null = the
// merchant's. Answers with the settings as that user now sees them.
func (h *Handler) UpdateMine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	langSet, dirSet, lang, dir, bad := ParseUserPrefs(raw)
	if bad != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", bad)
		return
	}
	var resp settingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before := LoadUser(ctx, tx, claims.UserID)
		if err := SaveUserPrefs(ctx, tx, claims.UserID, langSet, dirSet, lang, dir); err != nil {
			return err
		}
		var err error
		if resp, err = load(ctx, tx, claims.UserID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "user", EntityID: claims.UserID, Action: "update", Reason: "own language",
			PerformedBy: claims.UserID, Before: before, After: resp.User})
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save your language")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// ForTerminal: GET /settings/localization/terminal?pos_terminal_id= — what
// one till uses: the merchant's settings with the terminal's text
// direction and cart side, the branch currency (internal/taxes) and its
// denominations. Any signed-in user.
func (h *Handler) ForTerminal(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	termID := r.URL.Query().Get("pos_terminal_id")
	var eff Effective
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		s, err := Load(ctx, tx)
		if err != nil {
			return err
		}
		var branchID string
		var dir, side *string
		if err := tx.QueryRow(ctx, `SELECT branch_id::text, text_direction, cart_side FROM pos_terminals WHERE id::text = $1`, termID).
			Scan(&branchID, &dir, &side); err != nil {
			return err
		}
		ts, err := taxes.Load(ctx, tx, branchID)
		if err != nil {
			return err
		}
		// The signed-in cashier's own language (migrations/065), then the
		// till's own direction and cart side.
		eff = Resolve(s.ForUser(LoadUser(ctx, tx, claims.UserID)), ts.Currency(), dir, side)
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "terminal not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load localization settings")
		return
	}
	httpx.JSON(w, http.StatusOK, eff)
}

// Timezones offered by the settings screen (any IANA name is accepted).
var Timezones = []string{
	"Asia/Kolkata", "Asia/Colombo", "Asia/Kathmandu", "Asia/Dhaka", "Asia/Dubai", "Asia/Muscat", "Asia/Qatar", "Asia/Riyadh",
	"Asia/Kuwait", "Asia/Bahrain", "Asia/Singapore", "Asia/Kuala_Lumpur", "Asia/Bangkok", "Asia/Jakarta", "Asia/Hong_Kong",
	"Asia/Tokyo", "Australia/Sydney", "Australia/Perth", "Pacific/Auckland", "Africa/Nairobi", "Africa/Johannesburg",
	"Africa/Lagos", "Africa/Cairo", "Europe/London", "Europe/Paris", "Europe/Berlin", "America/New_York", "America/Chicago",
	"America/Denver", "America/Los_Angeles", "America/Toronto", "UTC",
}
