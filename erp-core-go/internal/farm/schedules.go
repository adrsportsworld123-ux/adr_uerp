package farm

// Vaccination and medicine plans by age (farm_schedules): the standard
// broiler and layer chicken plans (migrations/078) and any the farm adds.

import (
	"context"
	"encoding/json"
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

type scheduleItem struct {
	ID       string  `json:"schedule_item_id"`
	DayOfAge int     `json:"day_of_age"`
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	Method   *string `json:"method"`
	Notes    *string `json:"notes"`
	Repeat   *int    `json:"repeat_every_days"`
}

type schedule struct {
	ID       string         `json:"schedule_id"`
	Species  string         `json:"species"`
	Purpose  *string        `json:"purpose"`
	Name     string         `json:"name"`
	IsSystem bool           `json:"is_system"`
	Status   string         `json:"status"`
	Items    []scheduleItem `json:"items"`
}

func loadSchedules(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]schedule, error) {
	rows, err := tx.Query(ctx, `SELECT id::text, species, purpose, name, is_system, status FROM farm_schedules WHERE `+where+` ORDER BY species, purpose NULLS LAST, name`, args...)
	if err != nil {
		return nil, err
	}
	out := []schedule{}
	for rows.Next() {
		var s schedule
		if err := rows.Scan(&s.ID, &s.Species, &s.Purpose, &s.Name, &s.IsSystem, &s.Status); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		items, err := tx.Query(ctx, `SELECT id::text, day_of_age, kind, name, method, notes, repeat_every_days FROM farm_schedule_items WHERE schedule_id::text = $1 ORDER BY day_of_age, sort_order`, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Items = []scheduleItem{}
		for items.Next() {
			var it scheduleItem
			if err := items.Scan(&it.ID, &it.DayOfAge, &it.Kind, &it.Name, &it.Method, &it.Notes, &it.Repeat); err != nil {
				items.Close()
				return nil, err
			}
			out[i].Items = append(out[i].Items, it)
		}
		items.Close()
		if err := items.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ListSchedules: GET /farm/schedules?species= (farm.record)
func (h *Handler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var out []schedule
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = loadSchedules(ctx, tx, `($1 = '' OR species = $1)`, r.URL.Query().Get("species"))
		return err
	})
	if err != nil {
		writeErr(w, err, "schedules")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"schedules": out})
}

type scheduleInput struct {
	Species *string `json:"species"`
	Purpose *string `json:"purpose"`
	Name    *string `json:"name"`
	Status  *string `json:"status"`
	Items   *[]struct {
		DayOfAge int     `json:"day_of_age"`
		Kind     string  `json:"kind"`
		Name     string  `json:"name"`
		Method   *string `json:"method"`
		Notes    *string `json:"notes"`
		Repeat   *int    `json:"repeat_every_days"` // a herd's FMD every 180 days
	} `json:"items"`
}

// SaveSchedule: POST /farm/schedules and PUT /farm/schedules/{id}
// {species, purpose?, name, status?, items: [{day_of_age, kind, name, method?, notes?}]}
// (farm.manage). Items are replaced as a whole; one a batch already
// recorded can't go (switch the schedule off and make another).
func (h *Handler) SaveSchedule(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in scheduleInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if id == "" && (in.Species == nil || in.Name == nil || in.Items == nil) {
		writeErr(w, invalid("species, name and items are required"), "schedule")
		return
	}
	if in.Species != nil && !validSpecies(*in.Species) {
		writeErr(w, invalid("species must be chicken, goat, rabbit, cow, pig or fish"), "schedule")
		return
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		writeErr(w, invalid("name is required"), "schedule")
		return
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "inactive" {
		writeErr(w, invalid("status must be active or inactive"), "schedule")
		return
	}
	if in.Items != nil {
		for _, it := range *in.Items {
			if it.DayOfAge < 0 || strings.TrimSpace(it.Name) == "" || (it.Kind != "" && it.Kind != "vaccination" && it.Kind != "medicine" && it.Kind != "task") {
				writeErr(w, invalid("each item needs a day of age (0 or more), a name, and a kind of vaccination, medicine or task"), "schedule")
				return
			}
			if it.Repeat != nil && *it.Repeat <= 0 {
				writeErr(w, invalid("repeat_every_days must be more than 0 (or left out)"), "schedule")
				return
			}
		}
	}
	var out schedule
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var before []schedule
		if id == "" {
			if in.Purpose != nil && *in.Purpose != "" && !validPurpose(*in.Species, *in.Purpose) {
				return invalid("a %s schedule is for %s", *in.Species, strings.Join(Purposes[*in.Species], ", "))
			}
			if err := tx.QueryRow(ctx, `INSERT INTO farm_schedules (merchant_id, species, purpose, name) VALUES (current_setting('app.tenant_id')::uuid, $1, NULLIF($2, ''), trim($3))
				RETURNING id::text`, *in.Species, deref(in.Purpose), *in.Name).Scan(&id); err != nil {
				return err
			}
		} else {
			var err error
			if before, err = loadSchedules(ctx, tx, `id::text = $1`, id); err != nil {
				return err
			}
			if len(before) == 0 {
				return pgx.ErrNoRows
			}
			if in.Species != nil && *in.Species != before[0].Species {
				return invalid("a schedule's species can't change")
			}
			if _, err := tx.Exec(ctx, `UPDATE farm_schedules SET name = COALESCE(NULLIF(trim($2), ''), name), status = COALESCE($3, status),
				purpose = CASE WHEN $4::text IS NULL THEN purpose ELSE NULLIF($4, '') END WHERE id::text = $1`, id, in.Name, in.Status, in.Purpose); err != nil {
				return err
			}
		}
		if in.Items != nil {
			var used int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM farm_events e JOIN farm_schedule_items i ON i.id = e.schedule_item_id WHERE i.schedule_id::text = $1`, id).Scan(&used); err != nil {
				return err
			}
			if used > 0 {
				return conflict("SCHEDULE_IN_USE", "batches have recorded items of this schedule; switch it off and make a new one")
			}
			if _, err := tx.Exec(ctx, `DELETE FROM farm_schedule_items WHERE schedule_id::text = $1`, id); err != nil {
				return err
			}
			for i, it := range *in.Items {
				kind := it.Kind
				if kind == "" {
					kind = "vaccination"
				}
				if _, err := tx.Exec(ctx, `INSERT INTO farm_schedule_items (merchant_id, schedule_id, day_of_age, kind, name, method, notes, sort_order, repeat_every_days)
					VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2, $3, trim($4), NULLIF(trim($5), ''), NULLIF(trim($6), ''), $7, $8)`,
					id, it.DayOfAge, kind, it.Name, deref(it.Method), deref(it.Notes), i+1, it.Repeat); err != nil {
					return err
				}
			}
		}
		list, err := loadSchedules(ctx, tx, `id::text = $1`, id)
		if err != nil {
			return err
		}
		out = list[0]
		action := "create"
		var b any
		if len(before) > 0 {
			action, b = "update", before[0]
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_schedules", EntityID: id, Action: action, PerformedBy: claims.UserID, Before: b, After: out})
	})
	if err != nil {
		writeErr(w, err, "schedule")
		return
	}
	status := http.StatusOK
	if chi.URLParam(r, "id") == "" {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, out)
}

// Dashboard: GET /farm/dashboard?branch_id= (farm.record) — the farm
// branches the caller sees, every active batch with its figures, what is
// due (today and overdue, then the next 7 days) and today's totals.
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	type dueRow struct {
		dueItem
		BatchID     string `json:"batch_id"`
		BatchNumber string `json:"batch_number"`
		House       string `json:"house"`
	}
	var resp struct {
		Today    string `json:"today"`
		Branches []struct {
			BranchID string   `json:"branch_id"`
			Name     string   `json:"name"`
			Species  []string `json:"species"`
		} `json:"branches"`
		Batches  []batch  `json:"batches"`
		Due      []dueRow `json:"due"`
		Upcoming []dueRow `json:"upcoming"`
		Totals   struct {
			Alive          int     `json:"alive"`
			MortalityToday int     `json:"mortality_today"`
			FeedTodayKg    float64 `json:"feed_today_kg"`
			EggsToday      float64 `json:"eggs_today"`
			MilkTodayL     float64 `json:"milk_today_l"`
		} `json:"totals"`
		// §3.82
		FeedLow     []feedStockRow  `json:"feed_low"`
		Withdrawals []withdrawalRow `json:"withdrawals"`
		BirthsDue   []birthDue      `json:"births_due"`      // expected in the next 14 days (or overdue)
		Medicine    []medicineRow   `json:"medicine_alerts"` // expired or expiring
	}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		today := merchantToday(ctx, tx)
		resp.Today = today.Format("2006-01-02")
		rows, err := tx.Query(ctx, `
			SELECT b.id::text, b.name, COALESCE(array_agg(t.species ORDER BY t.sort_order) FILTER (WHERE t.species IS NOT NULL), '{}')
			FROM branches b JOIN branch_business_types bt ON bt.branch_id = b.id JOIN business_types t ON t.code = bt.type_code
			WHERE b.status = 'active' AND app_branch_visible(b.id) AND 'farm' = ANY(t.features)
			GROUP BY b.id, b.name ORDER BY b.name`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var b struct {
				BranchID string   `json:"branch_id"`
				Name     string   `json:"name"`
				Species  []string `json:"species"`
			}
			if err := rows.Scan(&b.BranchID, &b.Name, &b.Species); err != nil {
				rows.Close()
				return err
			}
			resp.Branches = append(resp.Branches, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		brows, err := tx.Query(ctx, batchSelect+` WHERE fb.status = 'active' AND ($1 = '' OR fb.branch_id::text = $1) ORDER BY b.name, fb.placed_on`, branchID)
		if err != nil {
			return err
		}
		for brows.Next() {
			var x batch
			if err := scanBatch(brows, &x); err != nil {
				brows.Close()
				return err
			}
			resp.Batches = append(resp.Batches, x)
		}
		brows.Close()
		if err := brows.Err(); err != nil {
			return err
		}
		week := today.AddDate(0, 0, 7)
		for i := range resp.Batches {
			x := &resp.Batches[i]
			if err := loadKPIs(ctx, tx, x, today); err != nil {
				return err
			}
			resp.Totals.Alive += x.KPI.Alive
			resp.Totals.EggsToday += x.KPI.EggsToday
			items, err := scheduleFor(ctx, tx, x, today)
			if err != nil {
				return err
			}
			for _, it := range items {
				row := dueRow{dueItem: it, BatchID: x.ID, BatchNumber: x.Number, House: deref(x.House)}
				due, _ := time.Parse("2006-01-02", it.DueOn)
				switch {
				case it.State == "overdue" || it.State == "due_today":
					resp.Due = append(resp.Due, row)
				case it.State == "upcoming" && !due.After(week):
					resp.Upcoming = append(resp.Upcoming, row)
				}
			}
		}
		if err := dashboardExtras(ctx, tx, branchID, today, &resp.FeedLow, &resp.Withdrawals, &resp.BirthsDue, &resp.Medicine); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			SELECT COALESCE(sum(e.head_count) FILTER (WHERE e.event_type = 'mortality'), 0)::int,
			       COALESCE(sum(e.quantity * COALESCE(u.factor, 1)) FILTER (WHERE e.event_type = 'feed' AND u.kind = 'weight'), 0)::float8,
			       COALESCE(sum(e.quantity * COALESCE(u.factor, 1)) FILTER (WHERE e.event_type = 'production' AND u.kind = 'volume'), 0)::float8
			FROM farm_events e LEFT JOIN product_variants v ON v.id = e.variant_id LEFT JOIN products p ON p.id = v.product_id LEFT JOIN units u ON u.id = p.unit_id
			WHERE e.status = 'posted' AND e.event_date = $1 AND ($2 = '' OR e.branch_id::text = $2)`, today, branchID).
			Scan(&resp.Totals.MortalityToday, &resp.Totals.FeedTodayKg, &resp.Totals.MilkTodayL)
	})
	if err != nil {
		writeErr(w, err, "farm dashboard")
		return
	}
	if resp.Batches == nil {
		resp.Batches = []batch{}
	}
	if resp.Due == nil {
		resp.Due = []dueRow{}
	}
	if resp.Upcoming == nil {
		resp.Upcoming = []dueRow{}
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type birthDue struct {
	AnimalID    string  `json:"animal_id"`
	Tag         string  `json:"tag"`
	Species     string  `json:"species"`
	BatchNumber string  `json:"batch_number"`
	ServedOn    string  `json:"served_on"`
	ExpectedOn  string  `json:"expected_on"`
	Status      string  `json:"status"` // served | pregnant
	Name        *string `json:"name"`
}

// dashboardExtras: feed running low, open withdrawals, births due and
// medicine expired or expiring.
func dashboardExtras(ctx context.Context, tx pgx.Tx, branchID string, today time.Time, feedLow *[]feedStockRow, wd *[]withdrawalRow, births *[]birthDue, med *[]medicineRow) error {
	*feedLow, *wd, *births, *med = []feedStockRow{}, []withdrawalRow{}, []birthDue{}, []medicineRow{}
	stock, err := feedStock(ctx, tx, branchID, today)
	if err != nil {
		return err
	}
	for _, s := range stock {
		if s.Low {
			*feedLow = append(*feedLow, s)
		}
	}
	if *wd, err = openWithdrawals(ctx, tx, branchID, today); err != nil {
		return err
	}
	meds, _, err := medicineStock(ctx, tx, branchID, today)
	if err != nil {
		return err
	}
	for _, m := range meds {
		if m.ExpiredQty > 0 || m.ExpiringQty > 0 {
			*med = append(*med, m)
		}
	}
	rows, err := tx.Query(ctx, animalSelect+` WHERE a.status = 'active' AND a.sex = 'female' AND ($1 = '' OR a.branch_id::text = $1)
		AND EXISTS (SELECT 1 FROM farm_breeding b WHERE b.animal_id = a.id AND b.status = 'posted' AND b.event_type = 'service')`, branchID)
	if err != nil {
		return err
	}
	var list []animal
	for rows.Next() {
		var a animal
		if err := scanAnimal(rows, &a); err != nil {
			rows.Close()
			return err
		}
		list = append(list, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	horizon := today.AddDate(0, 0, 14)
	for i := range list {
		a := &list[i]
		if err := fillAnimal(ctx, tx, a, today); err != nil {
			return err
		}
		if a.Breeding.ExpectedBirthOn == nil {
			continue
		}
		e, _ := parseDate(*a.Breeding.ExpectedBirthOn)
		if e.After(horizon) {
			continue
		}
		*births = append(*births, birthDue{AnimalID: a.ID, Tag: a.Tag, Name: a.Name, Species: a.Species, BatchNumber: a.BatchNumber,
			ServedOn: *a.Breeding.LastServiceOn, ExpectedOn: *a.Breeding.ExpectedBirthOn, Status: a.Breeding.Status})
	}
	return nil
}

// Species: GET /farm/species — the species and each one's purposes.
func (h *Handler) Species(w http.ResponseWriter, r *http.Request) {
	type sp struct {
		Species  string   `json:"species"`
		Purposes []string `json:"purposes"`
	}
	out := []sp{}
	for _, s := range []string{"chicken", "goat", "rabbit", "cow", "pig", "fish"} {
		out = append(out, sp{s, Purposes[s]})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"species": out})
}
