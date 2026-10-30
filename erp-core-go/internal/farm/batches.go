package farm

// Batches: a flock, herd or pond stocking (farm_batches) with what it is
// doing — alive, mortality, feed, weights, FCR, eggs, cost — worked out
// from its log, never stored.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// aliveSQL is a batch fb's head count now: placed, with births, animals
// added and moved in, less deaths, culls, heads that left in a production
// (a harvest) and animals moved out.
const aliveSQL = `fb.initial_count + COALESCE((SELECT sum(CASE WHEN e.event_type IN ('birth', 'addition', 'transfer_in') THEN e.head_count ELSE -e.head_count END)
	FROM farm_events e WHERE e.batch_id = fb.id AND e.status = 'posted'
	AND e.event_type IN ('mortality', 'cull', 'production', 'transfer_out', 'birth', 'addition', 'transfer_in')), 0)`

type batch struct {
	ID                 string   `json:"batch_id"`
	Number             string   `json:"batch_number"`
	Name               *string  `json:"name"`
	BranchID           string   `json:"branch_id"`
	Branch             string   `json:"branch"`
	HouseID            *string  `json:"house_id"`
	House              *string  `json:"house"`
	Species            string   `json:"species"`
	Purpose            string   `json:"purpose"`
	Breed              *string  `json:"breed"`
	SupplierID         *string  `json:"supplier_id"`
	Supplier           *string  `json:"supplier"`
	Source             *string  `json:"source"`
	PlacedOn           string   `json:"placed_on"`
	AgeAtPlacementDays int      `json:"age_at_placement_days"`
	InitialCount       int      `json:"initial_count"`
	InitialAvgWeightKg *float64 `json:"initial_avg_weight_kg"`
	ScheduleID         *string  `json:"schedule_id"`
	Schedule           *string  `json:"schedule"`
	FeedPlanID         *string  `json:"feed_plan_id"`
	FeedPlan           *string  `json:"feed_plan"`
	Status             string   `json:"status"`
	ClosedOn           *string  `json:"closed_on"`
	CloseNote          *string  `json:"close_note"`
	Notes              *string  `json:"notes"`
	KPI                kpis     `json:"kpi"`
}

// kpis: what the log says about a batch.
type kpis struct {
	AgeDays           int      `json:"age_days"`
	Alive             int      `json:"alive"`
	Dead              int      `json:"dead"`
	Culled            int      `json:"culled"`
	Harvested         int      `json:"harvested"`
	MortalityPct      float64  `json:"mortality_pct"`
	FeedKg            float64  `json:"feed_kg"`
	FeedCost          float64  `json:"feed_cost"`
	HealthCost        float64  `json:"health_cost"` // medicine and vaccines
	StockCost         float64  `json:"stock_cost"`  // the animals placed
	OtherCost         float64  `json:"other_cost"`  // expense events (cost only)
	OutputValue       float64  `json:"output_value"`
	NetCost           float64  `json:"net_cost"` // in, less what went out as produce
	CostPerHead       *float64 `json:"cost_per_head"`
	LatestAvgWeightKg *float64 `json:"latest_avg_weight_kg"`
	LatestWeighedOn   *string  `json:"latest_weighed_on"`
	LiveWeightKg      *float64 `json:"live_weight_kg"`
	HarvestedKg       float64  `json:"harvested_kg"`
	FCR               *float64 `json:"fcr"` // feed kg per kg gained
	FeedPerHeadKg     *float64 `json:"feed_per_head_kg"`
	EggsTotal         float64  `json:"eggs_total"`
	EggsToday         float64  `json:"eggs_today"`
	Laying7dPct       *float64 `json:"laying_7d_pct"` // eggs a hen a day, the last 7 days
	MilkTotalL        float64  `json:"milk_total_l"`
	Outputs           []output `json:"outputs"`
	VaccinesDue       int      `json:"vaccines_due"` // due today or overdue
	// §3.82
	Born           int        `json:"born"`
	Added          int        `json:"added"`     // animals added later and moved in
	MovedOut       int        `json:"moved_out"` // animals moved to another batch
	OnRegister     int        `json:"on_register"`
	FeedToday      []ration   `json:"feed_today"`       // the plan's ration for today
	FeedPlannedKg  *float64   `json:"feed_planned_kg"`  // the plan to date (grams a head a day)
	FeedVsPlanPct  *float64   `json:"feed_vs_plan_pct"` // fed / planned × 100
	BiomassKg      *float64   `json:"biomass_kg"`       // alive × latest (or placement) weight
	Withdrawal     withdrawal `json:"withdrawal"`       // the batch's open withdrawal dates
	AnimalsTreated int        `json:"animals_in_withdrawal"`
}

// ration: one feed of a plan for a day.
type ration struct {
	FeedName  string   `json:"feed_name"`
	ProductID *string  `json:"product_id"`
	Product   *string  `json:"product"`
	Kg        *float64 `json:"kg"` // nil: a % of body weight with no weight known
}

// withdrawal: the dates from which meat, milk and eggs may be taken again
// (nil = none pending), for the whole batch.
type withdrawal struct {
	MeatUntil *string `json:"meat_until"`
	MilkUntil *string `json:"milk_until"`
	EggsUntil *string `json:"eggs_until"`
}

type output struct {
	VariantID string  `json:"variant_id"`
	Product   string  `json:"product"`
	Unit      string  `json:"unit"`
	Quantity  float64 `json:"quantity"`
	Value     float64 `json:"value"`
}

const batchSelect = `
	SELECT fb.id::text, fb.batch_number, fb.name, fb.branch_id::text, b.name, fb.house_id::text, h.name, fb.species, fb.purpose, fb.breed,
	       fb.supplier_id::text, s.name, fb.source, fb.placed_on::text, fb.age_at_placement_days, fb.initial_count, fb.initial_avg_weight_kg::float8,
	       fb.schedule_id::text, sc.name, fb.feed_plan_id::text, fp.name, fb.status, fb.closed_on::text, fb.close_note, fb.notes
	FROM farm_batches fb
	JOIN branches b ON b.id = fb.branch_id
	LEFT JOIN farm_houses h ON h.id = fb.house_id
	LEFT JOIN suppliers s ON s.id = fb.supplier_id
	LEFT JOIN farm_schedules sc ON sc.id = fb.schedule_id
	LEFT JOIN farm_feed_plans fp ON fp.id = fb.feed_plan_id`

func scanBatch(row pgx.Row, x *batch) error {
	return row.Scan(&x.ID, &x.Number, &x.Name, &x.BranchID, &x.Branch, &x.HouseID, &x.House, &x.Species, &x.Purpose, &x.Breed,
		&x.SupplierID, &x.Supplier, &x.Source, &x.PlacedOn, &x.AgeAtPlacementDays, &x.InitialCount, &x.InitialAvgWeightKg,
		&x.ScheduleID, &x.Schedule, &x.FeedPlanID, &x.FeedPlan, &x.Status, &x.ClosedOn, &x.CloseNote, &x.Notes)
}

// loadKPIs works out a batch's figures from its posted events. Feed and
// harvest weights count events whose item is in a weight unit (kg, g,
// qtl …), eggs those in a count unit (pcs, dozen) of a layer or breeder
// flock, milk those in a volume unit.
func loadKPIs(ctx context.Context, tx pgx.Tx, x *batch, today time.Time) error {
	k := &x.KPI
	placed, _ := parseDate(x.PlacedOn)
	until := today
	if x.ClosedOn != nil {
		if c, ok := parseDate(*x.ClosedOn); ok {
			until = c
		}
	}
	k.AgeDays = int(until.Sub(placed).Hours()/24) + x.AgeAtPlacementDays
	var latest *float64
	var latestOn *string
	var alive, dead, culled, harvested, born, added, movedOut int
	var eggs7d float64
	if err := tx.QueryRow(ctx, `
		WITH ev AS (
		  SELECT e.*, COALESCE(u.kind, '') AS ukind, COALESCE(u.factor, 1)::float8 AS ufactor
		  FROM farm_events e
		  LEFT JOIN product_variants v ON v.id = e.variant_id LEFT JOIN products p ON p.id = v.product_id LEFT JOIN units u ON u.id = p.unit_id
		  WHERE e.batch_id::text = $1 AND e.status = 'posted')
		SELECT
		  COALESCE(sum(head_count) FILTER (WHERE event_type = 'mortality'), 0)::int,
		  COALESCE(sum(head_count) FILTER (WHERE event_type = 'cull'), 0)::int,
		  COALESCE(sum(head_count) FILTER (WHERE event_type = 'production'), 0)::int,
		  COALESCE(sum(quantity * ufactor) FILTER (WHERE event_type = 'feed' AND ukind = 'weight'), 0)::float8,
		  COALESCE(sum(amount) FILTER (WHERE event_type = 'feed'), 0)::float8,
		  COALESCE(sum(amount) FILTER (WHERE event_type IN ('medicine', 'vaccination')), 0)::float8,
		  COALESCE(sum(amount) FILTER (WHERE event_type IN ('placement', 'addition')), 0)::float8,
		  COALESCE(sum(amount) FILTER (WHERE event_type = 'expense'), 0)::float8,
		  COALESCE(sum(amount) FILTER (WHERE event_type = 'production'), 0)::float8,
		  COALESCE(sum(quantity * ufactor) FILTER (WHERE event_type = 'production' AND head_count IS NOT NULL AND ukind = 'weight'), 0)::float8,
		  COALESCE(sum(quantity * ufactor) FILTER (WHERE event_type = 'production' AND ukind = 'count' AND $2), 0)::float8,
		  COALESCE(sum(quantity * ufactor) FILTER (WHERE event_type = 'production' AND ukind = 'count' AND $2 AND event_date = $3::date), 0)::float8,
		  COALESCE(sum(quantity * ufactor) FILTER (WHERE event_type = 'production' AND ukind = 'count' AND $2 AND event_date > $3::date - 7), 0)::float8,
		  COALESCE(sum(quantity * ufactor) FILTER (WHERE event_type = 'production' AND ukind = 'volume'), 0)::float8,
		  (SELECT avg_weight_kg::float8 FROM ev WHERE event_type = 'weight' AND animal_id IS NULL ORDER BY event_date DESC, created_at DESC LIMIT 1),
		  (SELECT event_date::text FROM ev WHERE event_type = 'weight' AND animal_id IS NULL ORDER BY event_date DESC, created_at DESC LIMIT 1),
		  COALESCE(sum(head_count) FILTER (WHERE event_type = 'birth'), 0)::int,
		  COALESCE(sum(head_count) FILTER (WHERE event_type IN ('addition', 'transfer_in')), 0)::int,
		  COALESCE(sum(head_count) FILTER (WHERE event_type = 'transfer_out'), 0)::int,
		  (SELECT max(withdrawal_meat_until)::text FROM ev WHERE animal_id IS NULL AND withdrawal_meat_until > $3::date),
		  (SELECT max(withdrawal_milk_until)::text FROM ev WHERE animal_id IS NULL AND withdrawal_milk_until > $3::date),
		  (SELECT max(withdrawal_eggs_until)::text FROM ev WHERE animal_id IS NULL AND withdrawal_eggs_until > $3::date),
		  (SELECT count(DISTINCT animal_id) FROM ev WHERE animal_id IS NOT NULL
		     AND (withdrawal_meat_until > $3::date OR withdrawal_milk_until > $3::date OR withdrawal_eggs_until > $3::date))::int,
		  (SELECT count(*) FROM farm_animals a WHERE a.batch_id::text = $1 AND a.status = 'active')::int
		FROM ev`, x.ID, x.Purpose == "layer" || x.Purpose == "breeder", today).
		Scan(&dead, &culled, &harvested, &k.FeedKg, &k.FeedCost, &k.HealthCost, &k.StockCost, &k.OtherCost, &k.OutputValue,
			&k.HarvestedKg, &k.EggsTotal, &k.EggsToday, &eggs7d, &k.MilkTotalL, &latest, &latestOn, &born, &added, &movedOut,
			&k.Withdrawal.MeatUntil, &k.Withdrawal.MilkUntil, &k.Withdrawal.EggsUntil, &k.AnimalsTreated, &k.OnRegister); err != nil {
		return err
	}
	alive = x.InitialCount + born + added - dead - culled - harvested - movedOut
	k.Alive, k.Dead, k.Culled, k.Harvested, k.Born, k.Added, k.MovedOut = alive, dead, culled, harvested, born, added, movedOut
	started := x.InitialCount + born + added
	k.MortalityPct = round2(float64(dead) / float64(started) * 100)
	k.FeedKg = round3(k.FeedKg)
	k.NetCost = round2(k.StockCost + k.FeedCost + k.HealthCost + k.OtherCost - k.OutputValue)
	if alive > 0 {
		c := round2(k.NetCost / float64(alive))
		k.CostPerHead = &c
		if k.FeedKg > 0 {
			f := round3(k.FeedKg / float64(started)) // per head placed (born, added)
			k.FeedPerHeadKg = &f
		}
	}
	k.LatestAvgWeightKg, k.LatestWeighedOn = latest, latestOn
	if latest != nil {
		lw := round3(float64(alive) * *latest)
		k.LiveWeightKg = &lw
	}
	start := 0.0
	if x.InitialAvgWeightKg != nil {
		start = float64(x.InitialCount) * *x.InitialAvgWeightKg
	}
	if k.FeedKg > 0 && (latest != nil || k.HarvestedKg > 0) {
		live := 0.0
		if k.LiveWeightKg != nil {
			live = *k.LiveWeightKg
		}
		if gain := live + k.HarvestedKg - start; gain > 0 {
			fcr := round2(k.FeedKg / gain)
			k.FCR = &fcr
		}
	}
	if (x.Purpose == "layer" || x.Purpose == "breeder") && alive > 0 {
		days := math.Min(7, float64(k.AgeDays-x.AgeAtPlacementDays+1))
		if days > 0 {
			pct := round2(eggs7d / (float64(alive) * days) * 100)
			k.Laying7dPct = &pct
		}
	}
	rows, err := tx.Query(ctx, `
		SELECT e.variant_id::text, p.name, COALESCE(u.symbol, ''), sum(e.quantity)::float8, COALESCE(sum(e.amount), 0)::float8
		FROM farm_events e JOIN product_variants v ON v.id = e.variant_id JOIN products p ON p.id = v.product_id LEFT JOIN units u ON u.id = p.unit_id
		WHERE e.batch_id::text = $1 AND e.status = 'posted' AND e.event_type = 'production'
		GROUP BY e.variant_id, p.name, u.symbol ORDER BY p.name`, x.ID)
	if err != nil {
		return err
	}
	k.Outputs = []output{}
	for rows.Next() {
		var o output
		if err := rows.Scan(&o.VariantID, &o.Product, &o.Unit, &o.Quantity, &o.Value); err != nil {
			rows.Close()
			return err
		}
		k.Outputs = append(k.Outputs, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	due, err := scheduleFor(ctx, tx, x, today)
	if err != nil {
		return err
	}
	for _, d := range due {
		if d.State == "overdue" || d.State == "due_today" {
			k.VaccinesDue++
		}
	}
	return loadFeedPlan(ctx, tx, x, today)
}

// dueItem is one schedule item for a batch.
type dueItem struct {
	ItemID    string  `json:"schedule_item_id"`
	DayOfAge  int     `json:"day_of_age"`
	Kind      string  `json:"kind"`
	Name      string  `json:"name"`
	Method    *string `json:"method"`
	DueOn     string  `json:"due_on"`
	State     string  `json:"state"` // done | overdue | due_today | upcoming | before_placement
	DoneOn    *string `json:"done_on"`
	DoneEvent *string `json:"event_id"`
	// RepeatEveryDays: a repeating item (a herd's FMD every 180 days)
	// shows its next due date; DoneOn is when it was last given.
	RepeatEveryDays *int `json:"repeat_every_days"`
}

func scheduleFor(ctx context.Context, tx pgx.Tx, x *batch, today time.Time) ([]dueItem, error) {
	out := []dueItem{}
	if x.ScheduleID == nil {
		return out, nil
	}
	placed, _ := parseDate(x.PlacedOn)
	rows, err := tx.Query(ctx, `
		SELECT i.id::text, i.day_of_age, i.kind, i.name, i.method, i.repeat_every_days,
		       (SELECT e.event_date::text FROM farm_events e WHERE e.schedule_item_id = i.id AND e.batch_id::text = $2 AND e.status = 'posted'
		        ORDER BY e.event_date DESC LIMIT 1),
		       (SELECT e.id::text FROM farm_events e WHERE e.schedule_item_id = i.id AND e.batch_id::text = $2 AND e.status = 'posted'
		        ORDER BY e.event_date DESC LIMIT 1)
		FROM farm_schedule_items i WHERE i.schedule_id::text = $1 ORDER BY i.day_of_age, i.sort_order`, *x.ScheduleID, x.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d dueItem
		if err := rows.Scan(&d.ItemID, &d.DayOfAge, &d.Kind, &d.Name, &d.Method, &d.RepeatEveryDays, &d.DoneOn, &d.DoneEvent); err != nil {
			return nil, err
		}
		due := placed.AddDate(0, 0, d.DayOfAge-x.AgeAtPlacementDays)
		if r := d.RepeatEveryDays; r != nil {
			// The next one: after the last given, else the first on or after placement.
			if d.DoneOn != nil {
				last, _ := parseDate(*d.DoneOn)
				due = last.AddDate(0, 0, *r)
			} else {
				for due.Before(placed) {
					due = due.AddDate(0, 0, *r)
				}
			}
		}
		d.DueOn = due.Format("2006-01-02")
		switch {
		case d.DoneOn != nil && d.RepeatEveryDays == nil:
			d.State = "done"
		case d.DayOfAge < x.AgeAtPlacementDays && d.RepeatEveryDays == nil:
			d.State = "before_placement"
		case x.Status == "closed":
			d.State = "not_done"
		case due.Before(today):
			d.State = "overdue"
		case due.Equal(today):
			d.State = "due_today"
		default:
			d.State = "upcoming"
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListBatches: GET /farm/batches?branch_id=&status=active|closed&species= (farm.record)
func (h *Handler) ListBatches(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	out := []batch{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, batchSelect+`
			WHERE ($1 = '' OR fb.branch_id::text = $1) AND ($2 = '' OR fb.status = $2) AND ($3 = '' OR fb.species = $3)
			ORDER BY fb.status, fb.placed_on DESC, fb.batch_number DESC LIMIT 300`, q.Get("branch_id"), q.Get("status"), q.Get("species"))
		if err != nil {
			return err
		}
		for rows.Next() {
			var x batch
			if err := scanBatch(rows, &x); err != nil {
				rows.Close()
				return err
			}
			out = append(out, x)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		today := merchantToday(ctx, tx)
		for i := range out {
			if err := loadKPIs(ctx, tx, &out[i], today); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeErr(w, err, "batches")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"batches": out})
}

func loadBatch(ctx context.Context, tx pgx.Tx, id string) (batch, error) {
	var x batch
	if err := scanBatch(tx.QueryRow(ctx, batchSelect+` WHERE fb.id::text = $1`, id), &x); err != nil {
		return x, err
	}
	return x, loadKPIs(ctx, tx, &x, merchantToday(ctx, tx))
}

// GetBatch: GET /farm/batches/{id} — the batch, its figures, its schedule
// and its log (farm.record).
func (h *Handler) GetBatch(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var resp struct {
		batch
		ScheduleItems []dueItem `json:"schedule_items"`
		Events        []event   `json:"events"`
	}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if resp.batch, err = loadBatch(ctx, tx, chi.URLParam(r, "id")); err != nil {
			return err
		}
		if resp.ScheduleItems, err = scheduleFor(ctx, tx, &resp.batch, merchantToday(ctx, tx)); err != nil {
			return err
		}
		resp.Events, err = listEvents(ctx, tx, `e.batch_id::text = $1`, resp.ID)
		return err
	})
	if err != nil {
		writeErr(w, err, "batch")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type batchInput struct {
	BranchID           string   `json:"branch_id"`
	HouseID            *string  `json:"house_id"`
	Name               *string  `json:"name"`
	Species            string   `json:"species"`
	Purpose            string   `json:"purpose"`
	Breed              *string  `json:"breed"`
	SupplierID         *string  `json:"supplier_id"`
	Source             *string  `json:"source"`
	PlacedOn           string   `json:"placed_on"`
	AgeAtPlacementDays int      `json:"age_at_placement_days"`
	InitialCount       int      `json:"initial_count"`
	InitialAvgWeightKg *float64 `json:"initial_avg_weight_kg"`
	// ScheduleID: "" = the standard one for the species and purpose (if
	// any); "none" = no schedule.
	ScheduleID string `json:"schedule_id"`
	// FeedPlanID: "" = the standard one for the species and purpose (if
	// any); "none" = no plan.
	FeedPlanID string `json:"feed_plan_id"`
	// StockVariantID: the animals come out of the branch's stock (chicks
	// bought on a GRN), StockQuantity of them (default InitialCount).
	StockVariantID string   `json:"stock_variant_id"`
	StockQuantity  *float64 `json:"stock_quantity"`
	Notes          *string  `json:"notes"`
}

// CreateBatch: POST /farm/batches (farm.manage) — places the animals: a
// placement event, from stock when StockVariantID is given (Dr 1500, Cr 1200).
func (h *Handler) CreateBatch(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var in batchInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	placed, okDate := parseDate(in.PlacedOn)
	switch {
	case in.BranchID == "" || !okDate:
		writeErr(w, invalid("branch_id and placed_on (YYYY-MM-DD) are required"), "batch")
		return
	case !validSpecies(in.Species):
		writeErr(w, invalid("species must be chicken, goat, rabbit, cow, pig or fish"), "batch")
		return
	case !validPurpose(in.Species, in.Purpose):
		writeErr(w, invalid("a %s batch is for %s", in.Species, strings.Join(Purposes[in.Species], ", ")), "batch")
		return
	case in.InitialCount <= 0:
		writeErr(w, invalid("how many animals (initial_count) must be more than 0"), "batch")
		return
	case in.AgeAtPlacementDays < 0 || (in.InitialAvgWeightKg != nil && *in.InitialAvgWeightKg < 0):
		writeErr(w, invalid("age and weight at placement can't be negative"), "batch")
		return
	case in.StockQuantity != nil && *in.StockQuantity <= 0:
		writeErr(w, invalid("stock_quantity must be more than 0"), "batch")
		return
	}
	var out batch
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := farmBranch(ctx, tx, in.BranchID, in.Species); err != nil {
			return err
		}
		if placed.After(merchantToday(ctx, tx)) {
			return invalid("placed_on can't be in the future")
		}
		if in.HouseID != nil && *in.HouseID != "" {
			var branch, species, status string
			var capacity *int
			var occupied int
			if err := tx.QueryRow(ctx, `
				SELECT h.branch_id::text, h.species, h.status, h.capacity,
				       COALESCE((SELECT sum(`+aliveSQL+`) FROM farm_batches fb WHERE fb.house_id = h.id AND fb.status = 'active'), 0)::int
				FROM farm_houses h WHERE h.id::text = $1`, *in.HouseID).Scan(&branch, &species, &status, &capacity, &occupied); errors.Is(err, pgx.ErrNoRows) {
				return invalid("choose a shed or pond of this branch")
			} else if err != nil {
				return err
			}
			if branch != in.BranchID || status != "active" || species != in.Species {
				return invalid("the shed must be an active %s one of this branch", in.Species)
			}
			if capacity != nil && occupied+in.InitialCount > *capacity {
				return conflict("HOUSE_FULL", "it holds %d; %d are in it already", *capacity, occupied)
			}
		} else {
			in.HouseID = nil
		}
		var scheduleID *string
		switch in.ScheduleID {
		case "none":
		case "":
			var id string
			if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT id::text FROM farm_schedules WHERE species = $1 AND (purpose = $2 OR purpose IS NULL) AND status = 'active'
				ORDER BY (purpose IS NOT NULL) DESC, is_system DESC, created_at LIMIT 1), '')`, in.Species, in.Purpose).Scan(&id); err != nil {
				return err
			}
			if id != "" {
				scheduleID = &id
			}
		default:
			var species string
			if err := tx.QueryRow(ctx, `SELECT species FROM farm_schedules WHERE id::text = $1 AND status = 'active'`, in.ScheduleID).Scan(&species); errors.Is(err, pgx.ErrNoRows) || species != in.Species {
				return invalid("choose an active %s schedule", in.Species)
			} else if err != nil {
				return err
			}
			scheduleID = &in.ScheduleID
		}
		feedPlanID, err := pickFeedPlan(ctx, tx, in.Species, in.Purpose, in.FeedPlanID)
		if err != nil {
			return err
		}
		number, err := nextBatchNumber(ctx, tx)
		if err != nil {
			return err
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO farm_batches (merchant_id, branch_id, house_id, batch_number, name, species, purpose, breed, supplier_id, source, placed_on,
			    age_at_placement_days, initial_count, initial_avg_weight_kg, schedule_id, notes, created_by, feed_plan_id)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3, NULLIF(trim($4), ''), $5, $6, NULLIF(trim($7), ''),
			    NULLIF($8, '')::uuid, NULLIF(trim($9), ''), $10, $11, $12, $13, $14::uuid, NULLIF(trim($15), ''), $16, $17::uuid)
			RETURNING id::text`,
			in.BranchID, in.HouseID, number, deref(in.Name), in.Species, in.Purpose, deref(in.Breed), deref(in.SupplierID), deref(in.Source),
			placed, in.AgeAtPlacementDays, in.InitialCount, in.InitialAvgWeightKg, scheduleID, deref(in.Notes), claims.UserID, feedPlanID).Scan(&id); err != nil {
			return err
		}
		pe := eventInput{Type: "placement", Date: in.PlacedOn, HeadCount: &in.InitialCount, AvgWeightKg: in.InitialAvgWeightKg}
		if in.StockVariantID != "" {
			q := float64(in.InitialCount)
			if in.StockQuantity != nil {
				q = *in.StockQuantity
			}
			pe.VariantID, pe.Quantity = &in.StockVariantID, &q
		}
		x := batch{ID: id, Number: number, BranchID: in.BranchID, Status: "active", PlacedOn: in.PlacedOn, Species: in.Species, Purpose: in.Purpose}
		if _, err := recordEvent(ctx, tx, &x, pe, claims.UserID); err != nil {
			return err
		}
		if out, err = loadBatch(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_batches", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: out})
	})
	if err != nil {
		writeErr(w, err, "batch")
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// UpdateBatch: PATCH /farm/batches/{id} {name?, house_id?, breed?, notes?, schedule_id?} (farm.manage)
func (h *Handler) UpdateBatch(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in struct {
		Name       *string `json:"name"`
		HouseID    *string `json:"house_id"`
		Breed      *string `json:"breed"`
		Notes      *string `json:"notes"`
		ScheduleID *string `json:"schedule_id"`  // "" = none
		FeedPlanID *string `json:"feed_plan_id"` // "" = none
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var out batch
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := loadBatch(ctx, tx, id)
		if err != nil {
			return err
		}
		if before.Status != "active" {
			return conflict("BATCH_CLOSED", "batch %s is closed", before.Number)
		}
		if in.HouseID != nil && *in.HouseID != "" {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM farm_houses WHERE id::text = $1 AND branch_id::text = $2 AND species = $3 AND status = 'active')`,
				*in.HouseID, before.BranchID, before.Species).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return invalid("the shed must be an active %s one of this branch", before.Species)
			}
		}
		if in.ScheduleID != nil && *in.ScheduleID != "" {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM farm_schedules WHERE id::text = $1 AND species = $2 AND status = 'active')`,
				*in.ScheduleID, before.Species).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return invalid("choose an active %s schedule", before.Species)
			}
		}
		if in.FeedPlanID != nil && *in.FeedPlanID != "" {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM farm_feed_plans WHERE id::text = $1 AND species = $2 AND status = 'active')`,
				*in.FeedPlanID, before.Species).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return invalid("choose an active %s feed plan", before.Species)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE farm_batches SET feed_plan_id = CASE WHEN $2::text IS NULL THEN feed_plan_id ELSE NULLIF($2, '')::uuid END
			WHERE id::text = $1`, id, in.FeedPlanID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE farm_batches SET name = CASE WHEN $2::text IS NULL THEN name ELSE NULLIF(trim($2), '') END,
			  house_id = CASE WHEN $3::text IS NULL THEN house_id ELSE NULLIF($3, '')::uuid END,
			  breed = CASE WHEN $4::text IS NULL THEN breed ELSE NULLIF(trim($4), '') END,
			  notes = CASE WHEN $5::text IS NULL THEN notes ELSE NULLIF(trim($5), '') END,
			  schedule_id = CASE WHEN $6::text IS NULL THEN schedule_id ELSE NULLIF($6, '')::uuid END
			WHERE id::text = $1`, id, in.Name, in.HouseID, in.Breed, in.Notes, in.ScheduleID); err != nil {
			return err
		}
		if out, err = loadBatch(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_batches", EntityID: id, Action: "update", PerformedBy: claims.UserID, Before: before, After: out})
	})
	if err != nil {
		writeErr(w, err, "batch")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// CloseBatch: POST /farm/batches/{id}/close {closed_on?, note?} (farm.manage)
// — the cost still in 1500 for the batch (in, less produce out) goes to
// 5200 Farm Losses (or, when produce was valued above cost, 4100 Farm Gains).
func (h *Handler) CloseBatch(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in struct {
		ClosedOn string `json:"closed_on"`
		Note     string `json:"note"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
			return
		}
	}
	var out batch
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM farm_batches WHERE id::text = $1 FOR UPDATE`, id); err != nil {
			return err
		}
		before, err := loadBatch(ctx, tx, id)
		if err != nil {
			return err
		}
		if before.Status != "active" {
			return conflict("BATCH_CLOSED", "batch %s is already closed", before.Number)
		}
		today := merchantToday(ctx, tx)
		closed := today
		if in.ClosedOn != "" {
			c, ok := parseDate(in.ClosedOn)
			placed, _ := parseDate(before.PlacedOn)
			if !ok || c.After(today) || c.Before(placed) {
				return invalid("closed_on must be between the placement and today")
			}
			closed = c
		}
		var last *time.Time
		if err := tx.QueryRow(ctx, `SELECT max(event_date) FROM farm_events WHERE batch_id::text = $1 AND status = 'posted'`, id).Scan(&last); err != nil {
			return err
		}
		if last != nil && last.After(closed) {
			return invalid("its log runs to %s; close it on or after that", last.Format("02 Jan 2006"))
		}
		if n, err := taggedActive(ctx, tx, id); err != nil {
			return err
		} else if n > 0 {
			return conflict("BATCH_HAS_ANIMALS", "%d animals of batch %s are still on the register: record how each left, or move it to another batch", n, before.Number)
		}
		var residual float64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(sum(CASE WHEN event_type = 'production' THEN -amount
			                         WHEN event_type IN ('placement', 'addition', 'feed', 'medicine', 'vaccination') THEN amount ELSE 0 END), 0)::float8
			FROM farm_events WHERE batch_id::text = $1 AND status = 'posted' AND journal_entry_id IS NOT NULL`, id).Scan(&residual); err != nil {
			return err
		}
		desc := "Farm batch " + before.Number + " closed"
		var journalID string
		switch {
		case round2(residual) > 0:
			journalID, err = post(ctx, tx, before.BranchID, "farm_close", id, desc, claims.UserID, closed.Format("2006-01-02"), residual, AccountFarmLosses, AccountFarmWIP)
		case round2(residual) < 0:
			journalID, err = post(ctx, tx, before.BranchID, "farm_close", id, desc, claims.UserID, closed.Format("2006-01-02"), -residual, AccountFarmWIP, AccountFarmGains)
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE farm_batches SET status = 'closed', closed_on = $2, close_note = NULLIF(trim($3), ''), close_journal_id = NULLIF($4, '')::uuid
			WHERE id::text = $1`, id, closed, in.Note, journalID); err != nil {
			return err
		}
		if out, err = loadBatch(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_batches", EntityID: id, Action: "update", PerformedBy: claims.UserID, Before: before, After: out, Reason: "closed"})
	})
	if err != nil {
		writeErr(w, err, "batch")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
