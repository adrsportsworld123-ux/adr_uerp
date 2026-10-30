package farm

// Medicine management (§3.82; migrations/079): which products are feed,
// medicine or vaccine (farm_items, with a medicine's withdrawal days),
// the farm's medicine stock with what is expired or expiring, what is in
// withdrawal now, a pond's water log, and the farm's settings.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type farmItem struct {
	ID                 string   `json:"farm_item_id"`
	ProductID          string   `json:"product_id"`
	Product            string   `json:"product"`
	Unit               string   `json:"unit"`
	VariantID          *string  `json:"variant_id"` // the product's first active variant, for a quick pick
	Role               string   `json:"role"`
	Species            []string `json:"species"`
	WithdrawalMeatDays *int     `json:"withdrawal_meat_days"`
	WithdrawalMilkDays *int     `json:"withdrawal_milk_days"`
	WithdrawalEggsDays *int     `json:"withdrawal_eggs_days"`
	DoseNote           *string  `json:"dose_note"`
	Notes              *string  `json:"notes"`
}

const farmItemSelect = `
	SELECT fi.id::text, fi.product_id::text, p.name, COALESCE(u.symbol, ''),
	       (SELECT v.id::text FROM product_variants v WHERE v.product_id = p.id AND v.status = 'active' ORDER BY v.created_at LIMIT 1),
	       fi.role, COALESCE(fi.species, '{}'), fi.withdrawal_meat_days, fi.withdrawal_milk_days, fi.withdrawal_eggs_days, fi.dose_note, fi.notes
	FROM farm_items fi JOIN products p ON p.id = fi.product_id LEFT JOIN units u ON u.id = p.unit_id`

func scanFarmItem(row pgx.Row, f *farmItem) error {
	return row.Scan(&f.ID, &f.ProductID, &f.Product, &f.Unit, &f.VariantID, &f.Role, &f.Species, &f.WithdrawalMeatDays, &f.WithdrawalMilkDays,
		&f.WithdrawalEggsDays, &f.DoseNote, &f.Notes)
}

// ListFarmItems: GET /farm/items?role= (farm.record)
func (h *Handler) ListFarmItems(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	out := []farmItem{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, farmItemSelect+` WHERE ($1 = '' OR fi.role = $1) ORDER BY fi.role, p.name`, r.URL.Query().Get("role"))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var f farmItem
			if err := scanFarmItem(rows, &f); err != nil {
				return err
			}
			out = append(out, f)
		}
		return rows.Err()
	})
	if err != nil {
		writeErr(w, err, "farm items")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out})
}

// SaveFarmItem: POST /farm/items and PUT /farm/items/{id} {product_id,
// role, species?, withdrawal_meat_days?, withdrawal_milk_days?,
// withdrawal_eggs_days?, dose_note?, notes?}; DELETE /farm/items/{id}
// (farm.manage). A product is one farm item.
func (h *Handler) SaveFarmItem(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	del := r.Method == http.MethodDelete
	var in struct {
		ProductID          string   `json:"product_id"`
		Role               string   `json:"role"`
		Species            []string `json:"species"`
		WithdrawalMeatDays *int     `json:"withdrawal_meat_days"`
		WithdrawalMilkDays *int     `json:"withdrawal_milk_days"`
		WithdrawalEggsDays *int     `json:"withdrawal_eggs_days"`
		DoseNote           *string  `json:"dose_note"`
		Notes              *string  `json:"notes"`
	}
	if !del {
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
			return
		}
		if in.Role != "feed" && in.Role != "medicine" && in.Role != "vaccine" {
			writeErr(w, invalid("role must be feed, medicine or vaccine"), "farm item")
			return
		}
		if id == "" && in.ProductID == "" {
			writeErr(w, invalid("choose the product"), "farm item")
			return
		}
		for _, s := range in.Species {
			if !validSpecies(s) {
				writeErr(w, invalid("species must be chicken, goat, rabbit, cow, pig or fish"), "farm item")
				return
			}
		}
		for _, d := range []*int{in.WithdrawalMeatDays, in.WithdrawalMilkDays, in.WithdrawalEggsDays} {
			if d != nil && (*d < 0 || *d > 365) {
				writeErr(w, invalid("withdrawal days are from 0 to 365"), "farm item")
				return
			}
		}
		if in.Role == "feed" {
			in.WithdrawalMeatDays, in.WithdrawalMilkDays, in.WithdrawalEggsDays = nil, nil, nil
		}
	}
	var species []string
	if len(in.Species) > 0 {
		species = in.Species
	}
	var out farmItem
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var before *farmItem
		if id != "" {
			var b farmItem
			if err := scanFarmItem(tx.QueryRow(ctx, farmItemSelect+` WHERE fi.id::text = $1`, id), &b); err != nil {
				return err
			}
			before = &b
		}
		switch {
		case del:
			if _, err := tx.Exec(ctx, `DELETE FROM farm_items WHERE id::text = $1`, id); err != nil {
				return err
			}
			return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_items", EntityID: id, Action: "delete", PerformedBy: claims.UserID, Before: before})
		case id == "":
			if err := tx.QueryRow(ctx, `INSERT INTO farm_items (merchant_id, product_id, role, species, withdrawal_meat_days, withdrawal_milk_days, withdrawal_eggs_days, dose_note, notes)
				VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2, $3, $4, $5, $6, NULLIF(trim($7), ''), NULLIF(trim($8), '')) RETURNING id::text`,
				in.ProductID, in.Role, species, in.WithdrawalMeatDays, in.WithdrawalMilkDays, in.WithdrawalEggsDays, deref(in.DoseNote), deref(in.Notes)).Scan(&id); err != nil {
				return err
			}
		default:
			if _, err := tx.Exec(ctx, `UPDATE farm_items SET role = $2, species = $3, withdrawal_meat_days = $4, withdrawal_milk_days = $5, withdrawal_eggs_days = $6,
				dose_note = NULLIF(trim($7), ''), notes = NULLIF(trim($8), '') WHERE id::text = $1`,
				id, in.Role, species, in.WithdrawalMeatDays, in.WithdrawalMilkDays, in.WithdrawalEggsDays, deref(in.DoseNote), deref(in.Notes)); err != nil {
				return err
			}
		}
		if err := scanFarmItem(tx.QueryRow(ctx, farmItemSelect+` WHERE fi.id::text = $1`, id), &out); err != nil {
			return err
		}
		action := "create"
		var b any
		if before != nil {
			action, b = "update", before
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_items", EntityID: id, Action: action, PerformedBy: claims.UserID, Before: b, After: out})
	})
	switch {
	case isUnique(err):
		writeErr(w, conflict("FARM_ITEM_EXISTS", "that product is already a farm item"), "farm item")
	case isFK(err):
		writeErr(w, invalid("choose a product of this business"), "farm item")
	case err != nil:
		writeErr(w, err, "farm item")
	case del:
		w.WriteHeader(http.StatusNoContent)
	default:
		status := http.StatusOK
		if chi.URLParam(r, "id") == "" {
			status = http.StatusCreated
		}
		httpx.JSON(w, status, out)
	}
}

type medicineRow struct {
	BranchID           string  `json:"branch_id"`
	Branch             string  `json:"branch"`
	ProductID          string  `json:"product_id"`
	Product            string  `json:"product"`
	Role               string  `json:"role"`
	Unit               string  `json:"unit"`
	Free               float64 `json:"free"`
	NextExpiry         *string `json:"next_expiry"`
	ExpiredQty         float64 `json:"expired_qty"`
	ExpiringQty        float64 `json:"expiring_qty"` // within the warning days
	Used30d            float64 `json:"used_30d"`
	WithdrawalMeatDays *int    `json:"withdrawal_meat_days"`
	WithdrawalMilkDays *int    `json:"withdrawal_milk_days"`
	WithdrawalEggsDays *int    `json:"withdrawal_eggs_days"`
}

func medicineStock(ctx context.Context, tx pgx.Tx, branchID string, today time.Time) ([]medicineRow, int, error) {
	var warn int
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT expiry_warn_days FROM farm_settings), 30)`).Scan(&warn); err != nil {
		return nil, 0, err
	}
	branches, err := farmBranchIDs(ctx, tx, branchID)
	if err != nil {
		return nil, 0, err
	}
	out := []medicineRow{}
	for _, b := range branches {
		rows, err := tx.Query(ctx, `
			SELECT br.name, p.id::text, p.name, fi.role, COALESCE(u.symbol, ''),
			       COALESCE((SELECT sum(sl.on_hand - sl.reserved) FROM stock_levels sl JOIN product_variants v ON v.id = sl.variant_id
			                 WHERE v.product_id = p.id AND sl.branch_id::text = $1), 0)::float8,
			       (SELECT min(sb.expiry_date)::text FROM stock_batches sb JOIN product_variants v ON v.id = sb.variant_id
			         WHERE v.product_id = p.id AND sb.branch_id::text = $1 AND sb.quantity_remaining > 0 AND sb.expiry_date >= $2::date),
			       COALESCE((SELECT sum(sb.quantity_remaining) FROM stock_batches sb JOIN product_variants v ON v.id = sb.variant_id
			         WHERE v.product_id = p.id AND sb.branch_id::text = $1 AND sb.quantity_remaining > 0 AND sb.expiry_date < $2::date), 0)::float8,
			       COALESCE((SELECT sum(sb.quantity_remaining) FROM stock_batches sb JOIN product_variants v ON v.id = sb.variant_id
			         WHERE v.product_id = p.id AND sb.branch_id::text = $1 AND sb.quantity_remaining > 0
			           AND sb.expiry_date >= $2::date AND sb.expiry_date <= $2::date + $3::int), 0)::float8,
			       COALESCE((SELECT sum(e.quantity) FROM farm_events e JOIN product_variants v ON v.id = e.variant_id
			         WHERE v.product_id = p.id AND e.branch_id::text = $1 AND e.status = 'posted' AND e.event_type IN ('medicine', 'vaccination')
			           AND e.event_date > $2::date - 30), 0)::float8,
			       fi.withdrawal_meat_days, fi.withdrawal_milk_days, fi.withdrawal_eggs_days
			FROM farm_items fi JOIN products p ON p.id = fi.product_id LEFT JOIN units u ON u.id = p.unit_id
			CROSS JOIN (SELECT name FROM branches WHERE id::text = $1) br
			WHERE fi.role IN ('medicine', 'vaccine') ORDER BY fi.role, p.name`, b, today, warn)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var m medicineRow
			if err := rows.Scan(&m.Branch, &m.ProductID, &m.Product, &m.Role, &m.Unit, &m.Free, &m.NextExpiry, &m.ExpiredQty, &m.ExpiringQty,
				&m.Used30d, &m.WithdrawalMeatDays, &m.WithdrawalMilkDays, &m.WithdrawalEggsDays); err != nil {
				rows.Close()
				return nil, 0, err
			}
			m.BranchID = b
			out = append(out, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, 0, err
		}
	}
	return out, warn, nil
}

// MedicineStock: GET /farm/health/stock?branch_id= (farm.record) — each
// farm branch's medicines and vaccines: free stock, the next expiry, what
// is expired and what expires within the warning days.
func (h *Handler) MedicineStock(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var out []medicineRow
	var warn int
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, warn, err = medicineStock(ctx, tx, r.URL.Query().Get("branch_id"), merchantToday(ctx, tx))
		return err
	})
	if err != nil {
		writeErr(w, err, "medicine stock")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out, "expiry_warn_days": warn})
}

type withdrawalRow struct {
	EventID     string  `json:"event_id"`
	BatchID     string  `json:"batch_id"`
	BatchNumber string  `json:"batch_number"`
	BranchID    string  `json:"branch_id"`
	House       *string `json:"house"`
	Species     string  `json:"species"`
	AnimalID    *string `json:"animal_id"`
	AnimalTag   *string `json:"animal_tag"`
	Treatment   string  `json:"treatment"`
	TreatedOn   string  `json:"treated_on"`
	MeatUntil   *string `json:"meat_until"`
	MilkUntil   *string `json:"milk_until"`
	EggsUntil   *string `json:"eggs_until"`
}

func openWithdrawals(ctx context.Context, tx pgx.Tx, branchID string, today time.Time) ([]withdrawalRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT e.id::text, e.batch_id::text, fb.batch_number, e.branch_id::text, h.name, fb.species, e.animal_id::text, a.tag,
		       COALESCE(e.vaccine, p.name, e.event_type), e.event_date::text,
		       CASE WHEN e.withdrawal_meat_until > $2::date THEN e.withdrawal_meat_until::text END,
		       CASE WHEN e.withdrawal_milk_until > $2::date THEN e.withdrawal_milk_until::text END,
		       CASE WHEN e.withdrawal_eggs_until > $2::date THEN e.withdrawal_eggs_until::text END
		FROM farm_events e JOIN farm_batches fb ON fb.id = e.batch_id LEFT JOIN farm_houses h ON h.id = fb.house_id
		LEFT JOIN farm_animals a ON a.id = e.animal_id
		LEFT JOIN product_variants v ON v.id = e.variant_id LEFT JOIN products p ON p.id = v.product_id
		WHERE e.status = 'posted' AND e.event_type IN ('medicine', 'vaccination') AND fb.status = 'active' AND (a.id IS NULL OR a.status = 'active')
		  AND (e.withdrawal_meat_until > $2::date OR e.withdrawal_milk_until > $2::date OR e.withdrawal_eggs_until > $2::date)
		  AND ($1 = '' OR e.branch_id::text = $1)
		ORDER BY GREATEST(e.withdrawal_meat_until, e.withdrawal_milk_until, e.withdrawal_eggs_until) DESC`, branchID, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []withdrawalRow{}
	for rows.Next() {
		var x withdrawalRow
		if err := rows.Scan(&x.EventID, &x.BatchID, &x.BatchNumber, &x.BranchID, &x.House, &x.Species, &x.AnimalID, &x.AnimalTag, &x.Treatment, &x.TreatedOn,
			&x.MeatUntil, &x.MilkUntil, &x.EggsUntil); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Withdrawals: GET /farm/health/withdrawals?branch_id= (farm.record) — the
// treatments whose meat, milk or egg withdrawal hasn't ended.
func (h *Handler) Withdrawals(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var out []withdrawalRow
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = openWithdrawals(ctx, tx, r.URL.Query().Get("branch_id"), merchantToday(ctx, tx))
		return err
	})
	if err != nil {
		writeErr(w, err, "withdrawals")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"withdrawals": out})
}

// ---- a pond's water --------------------------------------------------------

type waterLog struct {
	ID             string   `json:"water_log_id"`
	BranchID       string   `json:"branch_id"`
	HouseID        string   `json:"house_id"`
	House          string   `json:"house"`
	LoggedOn       string   `json:"logged_on"`
	LoggedTime     *string  `json:"logged_time"`
	DOMgL          *float64 `json:"do_mg_l"`
	PH             *float64 `json:"ph"`
	TempC          *float64 `json:"temp_c"`
	AmmoniaMgL     *float64 `json:"ammonia_mg_l"`
	NitriteMgL     *float64 `json:"nitrite_mg_l"`
	TransparencyCm *float64 `json:"transparency_cm"`
	Notes          *string  `json:"notes"`
	CreatedBy      *string  `json:"created_by"`
	Alerts         []string `json:"alerts"`
}

// waterAlerts: readings outside the usual range for warm-water fish
// ponds (guidance; the farm's own species may differ).
func waterAlerts(x *waterLog) []string {
	out := []string{}
	if x.DOMgL != nil && *x.DOMgL < 4 {
		out = append(out, "Dissolved oxygen below 4 mg/L")
	}
	if x.PH != nil && (*x.PH < 6.5 || *x.PH > 8.5) {
		out = append(out, "pH outside 6.5–8.5")
	}
	if x.TempC != nil && (*x.TempC < 20 || *x.TempC > 34) {
		out = append(out, "Temperature outside 20–34 °C")
	}
	if x.AmmoniaMgL != nil && *x.AmmoniaMgL > 0.5 {
		out = append(out, "Ammonia above 0.5 mg/L")
	}
	if x.NitriteMgL != nil && *x.NitriteMgL > 0.3 {
		out = append(out, "Nitrite above 0.3 mg/L")
	}
	if x.TransparencyCm != nil && (*x.TransparencyCm < 25 || *x.TransparencyCm > 60) {
		out = append(out, "Transparency outside 25–60 cm")
	}
	return out
}

const waterSelect = `SELECT w.id::text, w.branch_id::text, w.house_id::text, h.name, w.logged_on::text, to_char(w.logged_time, 'HH24:MI'),
	w.do_mg_l::float8, w.ph::float8, w.temp_c::float8, w.ammonia_mg_l::float8, w.nitrite_mg_l::float8, w.transparency_cm::float8, w.notes, u.name
	FROM farm_water_logs w JOIN farm_houses h ON h.id = w.house_id LEFT JOIN users u ON u.id = w.created_by`

func scanWater(row pgx.Row, x *waterLog) error {
	if err := row.Scan(&x.ID, &x.BranchID, &x.HouseID, &x.House, &x.LoggedOn, &x.LoggedTime, &x.DOMgL, &x.PH, &x.TempC, &x.AmmoniaMgL,
		&x.NitriteMgL, &x.TransparencyCm, &x.Notes, &x.CreatedBy); err != nil {
		return err
	}
	x.Alerts = waterAlerts(x)
	return nil
}

// ListWater: GET /farm/water?branch_id=&house_id=&from=&to= (farm.record)
func (h *Handler) ListWater(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	date := func(s string) string {
		if _, ok := parseDate(s); ok {
			return s
		}
		return ""
	}
	out := []waterLog{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, waterSelect+` WHERE ($1 = '' OR w.branch_id::text = $1) AND ($2 = '' OR w.house_id::text = $2)
			AND ($3 = '' OR w.logged_on >= $3::date) AND ($4 = '' OR w.logged_on <= $4::date)
			ORDER BY w.logged_on DESC, w.logged_time DESC NULLS LAST, w.created_at DESC LIMIT 500`,
			q.Get("branch_id"), q.Get("house_id"), date(q.Get("from")), date(q.Get("to")))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x waterLog
			if err := scanWater(rows, &x); err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	if err != nil {
		writeErr(w, err, "water log")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"logs": out})
}

// RecordWater: POST /farm/water {house_id, logged_on, logged_time?, do_mg_l?,
// ph?, temp_c?, ammonia_mg_l?, nitrite_mg_l?, transparency_cm?, notes?}
// (farm.record) — a pond or tank's readings; at least one. DELETE
// /farm/water/{id} (farm.manage) takes a wrong one out.
func (h *Handler) RecordWater(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	if id := chi.URLParam(r, "id"); id != "" && r.Method == http.MethodDelete {
		err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
			var before waterLog
			if err := scanWater(tx.QueryRow(ctx, waterSelect+` WHERE w.id::text = $1`, id), &before); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM farm_water_logs WHERE id::text = $1`, id); err != nil {
				return err
			}
			return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_water_logs", EntityID: id, Action: "delete", PerformedBy: claims.UserID, Before: before})
		})
		if err != nil {
			writeErr(w, err, "water reading")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var in struct {
		HouseID        string   `json:"house_id"`
		LoggedOn       string   `json:"logged_on"`
		LoggedTime     string   `json:"logged_time"`
		DOMgL          *float64 `json:"do_mg_l"`
		PH             *float64 `json:"ph"`
		TempC          *float64 `json:"temp_c"`
		AmmoniaMgL     *float64 `json:"ammonia_mg_l"`
		NitriteMgL     *float64 `json:"nitrite_mg_l"`
		TransparencyCm *float64 `json:"transparency_cm"`
		Notes          *string  `json:"notes"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	readings := []*float64{in.DOMgL, in.PH, in.TempC, in.AmmoniaMgL, in.NitriteMgL, in.TransparencyCm}
	any := false
	for _, v := range readings {
		if v != nil {
			any = true
			if *v < 0 {
				writeErr(w, invalid("readings can't be negative"), "water reading")
				return
			}
		}
	}
	if in.PH != nil && *in.PH > 14 {
		writeErr(w, invalid("pH is from 0 to 14"), "water reading")
		return
	}
	if in.HouseID == "" || (!any && strings.TrimSpace(deref(in.Notes)) == "") {
		writeErr(w, invalid("choose the pond and enter at least one reading"), "water reading")
		return
	}
	if in.LoggedTime != "" {
		if _, err := time.Parse("15:04", in.LoggedTime); err != nil {
			writeErr(w, invalid("logged_time is HH:MM"), "water reading")
			return
		}
	}
	var out waterLog
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID string
		if err := tx.QueryRow(ctx, `SELECT branch_id::text FROM farm_houses WHERE id::text = $1 AND status = 'active'`, in.HouseID).Scan(&branchID); errors.Is(err, pgx.ErrNoRows) {
			return invalid("choose an active pond or tank")
		} else if err != nil {
			return err
		}
		today := merchantToday(ctx, tx)
		day := today
		if in.LoggedOn != "" {
			d, ok := parseDate(in.LoggedOn)
			if !ok || d.After(today) {
				return invalid("logged_on must be a date up to today")
			}
			day = d
		}
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO farm_water_logs (merchant_id, branch_id, house_id, logged_on, logged_time, do_mg_l, ph, temp_c, ammonia_mg_l,
				nitrite_mg_l, transparency_cm, notes, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3, NULLIF($4, '')::time, $5, $6, $7, $8, $9, $10, NULLIF(trim($11), ''), $12)
			RETURNING id::text`, branchID, in.HouseID, day, in.LoggedTime, in.DOMgL, in.PH, in.TempC, in.AmmoniaMgL, in.NitriteMgL, in.TransparencyCm,
			deref(in.Notes), claims.UserID).Scan(&id); err != nil {
			return err
		}
		if err := scanWater(tx.QueryRow(ctx, waterSelect+` WHERE w.id::text = $1`, id), &out); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_water_logs", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: out})
	})
	if err != nil {
		writeErr(w, err, "water reading")
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// ---- settings ----------------------------------------------------------------

type farmSettings struct {
	FeedLowDays    int `json:"feed_low_days"`
	ExpiryWarnDays int `json:"expiry_warn_days"`
}

// Settings: GET /farm/settings (farm.record), PUT /farm/settings
// {feed_low_days?, expiry_warn_days?} (farm.manage).
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var in struct {
		FeedLowDays    *int `json:"feed_low_days"`
		ExpiryWarnDays *int `json:"expiry_warn_days"`
	}
	put := r.Method == http.MethodPut
	if put {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
			return
		}
		if (in.FeedLowDays != nil && (*in.FeedLowDays < 1 || *in.FeedLowDays > 365)) || (in.ExpiryWarnDays != nil && (*in.ExpiryWarnDays < 1 || *in.ExpiryWarnDays > 365)) {
			writeErr(w, invalid("days are from 1 to 365"), "farm settings")
			return
		}
	}
	var out farmSettings
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if put {
			if _, err := tx.Exec(ctx, `INSERT INTO farm_settings (merchant_id, feed_low_days, expiry_warn_days, updated_by)
				VALUES (current_setting('app.tenant_id')::uuid, COALESCE($1, 7), COALESCE($2, 30), $3)
				ON CONFLICT (merchant_id) DO UPDATE SET feed_low_days = COALESCE($1, farm_settings.feed_low_days),
				  expiry_warn_days = COALESCE($2, farm_settings.expiry_warn_days), updated_by = $3, updated_at = now()`,
				in.FeedLowDays, in.ExpiryWarnDays, claims.UserID); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT feed_low_days FROM farm_settings), 7), COALESCE((SELECT expiry_warn_days FROM farm_settings), 30)`).
			Scan(&out.FeedLowDays, &out.ExpiryWarnDays); err != nil {
			return err
		}
		if put {
			return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_settings", EntityID: claims.TenantID, Action: "update", PerformedBy: claims.UserID, After: out})
		}
		return nil
	})
	if err != nil {
		writeErr(w, err, "farm settings")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
