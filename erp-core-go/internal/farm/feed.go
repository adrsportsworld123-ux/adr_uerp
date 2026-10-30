package farm

// Feed management (§3.82; migrations/079): feeding plans by age (grams a
// head a day, or a pond's % of body weight), each batch's ration today and
// fed against plan, the farm's feed stock in days left, and the farm's own
// feed — a recipe of ingredients mixed into a feed in stock at their
// combined cost (stock to stock, so no ledger entry).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/catalog"
	"erp-core-go/internal/httpx"
)

// pickFeedPlan: the plan a new batch follows — given, "none", or ("") the
// standard active one for its species and purpose (a plan for any purpose
// of the species after one for this purpose).
func pickFeedPlan(ctx context.Context, tx pgx.Tx, species, purpose, given string) (*string, error) {
	switch given {
	case "none":
		return nil, nil
	case "":
		var id string
		if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT id::text FROM farm_feed_plans WHERE species = $1 AND (purpose = $2 OR purpose IS NULL) AND status = 'active'
			ORDER BY (purpose IS NOT NULL) DESC, is_system DESC, created_at LIMIT 1), '')`, species, purpose).Scan(&id); err != nil {
			return nil, err
		}
		if id == "" {
			return nil, nil
		}
		return &id, nil
	}
	var sp string
	if err := tx.QueryRow(ctx, `SELECT species FROM farm_feed_plans WHERE id::text = $1 AND status = 'active'`, given).Scan(&sp); errors.Is(err, pgx.ErrNoRows) || sp != species {
		return nil, invalid("choose an active %s feed plan", species)
	} else if err != nil {
		return nil, err
	}
	return &given, nil
}

type planItem struct {
	ID            string   `json:"plan_item_id"`
	DayFrom       int      `json:"day_from"`
	DayTo         *int     `json:"day_to"`
	FeedName      string   `json:"feed_name"`
	ProductID     *string  `json:"product_id"`
	Product       *string  `json:"product"`
	GramsPerHead  *float64 `json:"grams_per_head"`
	PctBodyWeight *float64 `json:"pct_body_weight"`
}

func (it planItem) covers(age int) bool {
	return age >= it.DayFrom && (it.DayTo == nil || age <= *it.DayTo)
}

func loadPlanItems(ctx context.Context, tx pgx.Tx, planID string) ([]planItem, error) {
	rows, err := tx.Query(ctx, `
		SELECT i.id::text, i.day_from, i.day_to, i.feed_name, i.product_id::text, p.name, i.grams_per_head::float8, i.pct_body_weight::float8
		FROM farm_feed_plan_items i LEFT JOIN products p ON p.id = i.product_id
		WHERE i.plan_id::text = $1 ORDER BY i.day_from, i.sort_order`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []planItem{}
	for rows.Next() {
		var it planItem
		if err := rows.Scan(&it.ID, &it.DayFrom, &it.DayTo, &it.FeedName, &it.ProductID, &it.Product, &it.GramsPerHead, &it.PctBodyWeight); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// loadFeedPlan adds the feed figures to x.KPI: biomass, today's ration and
// (grams a head a day) what the plan says to date against what was fed.
func loadFeedPlan(ctx context.Context, tx pgx.Tx, x *batch, today time.Time) error {
	k := &x.KPI
	k.FeedToday = []ration{}
	w := k.LatestAvgWeightKg
	if w == nil {
		w = x.InitialAvgWeightKg
	}
	if w != nil && k.Alive > 0 {
		b := round3(float64(k.Alive) * *w)
		k.BiomassKg = &b
	}
	if x.FeedPlanID == nil {
		return nil
	}
	items, err := loadPlanItems(ctx, tx, *x.FeedPlanID)
	if err != nil {
		return err
	}
	if x.Status == "active" {
		for _, it := range items {
			if !it.covers(k.AgeDays) {
				continue
			}
			r := ration{FeedName: it.FeedName, ProductID: it.ProductID, Product: it.Product}
			switch {
			case it.GramsPerHead != nil:
				kg := round3(float64(k.Alive) * *it.GramsPerHead / 1000)
				r.Kg = &kg
			case it.PctBodyWeight != nil && k.BiomassKg != nil:
				kg := round3(*k.BiomassKg * *it.PctBodyWeight / 100)
				r.Kg = &kg
			}
			k.FeedToday = append(k.FeedToday, r)
		}
	}
	// To date: each day's heads (placed, with what came in and went out
	// before it) × the grams for that day's age.
	perHead := false
	for _, it := range items {
		if it.GramsPerHead != nil {
			perHead = true
		}
	}
	if !perHead {
		return nil
	}
	placed, _ := parseDate(x.PlacedOn)
	until := today
	if x.ClosedOn != nil {
		if c, ok := parseDate(*x.ClosedOn); ok {
			until = c
		}
	}
	rows, err := tx.Query(ctx, `
		SELECT event_date, sum(CASE WHEN event_type IN ('birth', 'addition', 'transfer_in') THEN head_count ELSE -head_count END)::int
		FROM farm_events WHERE batch_id::text = $1 AND status = 'posted' AND head_count IS NOT NULL
		  AND event_type IN ('mortality', 'cull', 'production', 'transfer_out', 'birth', 'addition', 'transfer_in')
		GROUP BY event_date ORDER BY event_date`, x.ID)
	if err != nil {
		return err
	}
	changes := map[string]int{}
	for rows.Next() {
		var d time.Time
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			rows.Close()
			return err
		}
		changes[d.Format("2006-01-02")] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	heads := x.InitialCount
	planned := 0.0
	for i, d := 0, placed; !d.After(until) && i < 3650; i, d = i+1, d.AddDate(0, 0, 1) {
		age := x.AgeAtPlacementDays + i
		grams := 0.0
		for _, it := range items {
			if it.GramsPerHead != nil && it.covers(age) {
				grams += *it.GramsPerHead
			}
		}
		planned += float64(heads) * grams / 1000
		heads += changes[d.Format("2006-01-02")]
	}
	if planned > 0 {
		p := round3(planned)
		k.FeedPlannedKg = &p
		pct := round2(k.FeedKg / planned * 100)
		k.FeedVsPlanPct = &pct
	}
	return nil
}

type feedPlan struct {
	ID       string     `json:"feed_plan_id"`
	Species  string     `json:"species"`
	Purpose  *string    `json:"purpose"`
	Name     string     `json:"name"`
	Basis    string     `json:"basis"`
	IsSystem bool       `json:"is_system"`
	Status   string     `json:"status"`
	Items    []planItem `json:"items"`
	InUse    int        `json:"batches"` // active batches on it
}

func loadFeedPlans(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]feedPlan, error) {
	rows, err := tx.Query(ctx, `SELECT id::text, species, purpose, name, basis, is_system, status,
		(SELECT count(*) FROM farm_batches fb WHERE fb.feed_plan_id = fp.id AND fb.status = 'active')::int
		FROM farm_feed_plans fp WHERE `+where+` ORDER BY species, purpose NULLS LAST, name`, args...)
	if err != nil {
		return nil, err
	}
	out := []feedPlan{}
	for rows.Next() {
		var p feedPlan
		if err := rows.Scan(&p.ID, &p.Species, &p.Purpose, &p.Name, &p.Basis, &p.IsSystem, &p.Status, &p.InUse); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Items, err = loadPlanItems(ctx, tx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ListFeedPlans: GET /farm/feed-plans?species= (farm.record)
func (h *Handler) ListFeedPlans(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var out []feedPlan
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = loadFeedPlans(ctx, tx, `($1 = '' OR species = $1)`, r.URL.Query().Get("species"))
		return err
	})
	if err != nil {
		writeErr(w, err, "feed plans")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"feed_plans": out})
}

type feedPlanInput struct {
	Species *string `json:"species"`
	Purpose *string `json:"purpose"`
	Name    *string `json:"name"`
	Basis   *string `json:"basis"`
	Status  *string `json:"status"`
	Items   *[]struct {
		DayFrom       int      `json:"day_from"`
		DayTo         *int     `json:"day_to"`
		FeedName      string   `json:"feed_name"`
		ProductID     *string  `json:"product_id"`
		GramsPerHead  *float64 `json:"grams_per_head"`
		PctBodyWeight *float64 `json:"pct_body_weight"`
	} `json:"items"`
}

// SaveFeedPlan: POST /farm/feed-plans and PUT /farm/feed-plans/{id}
// {species, purpose?, name, basis, status?, items: [{day_from, day_to?,
// feed_name, product_id?, grams_per_head | pct_body_weight}]} (farm.manage).
// Items are replaced as a whole (a plan holds no history: what was fed is
// in the log).
func (h *Handler) SaveFeedPlan(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in feedPlanInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	check := func() error {
		if id == "" && (in.Species == nil || in.Name == nil || in.Items == nil) {
			return invalid("species, name and items are required")
		}
		if in.Species != nil && !validSpecies(*in.Species) {
			return invalid("species must be chicken, goat, rabbit, cow, pig or fish")
		}
		if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
			return invalid("name is required")
		}
		if in.Basis != nil && *in.Basis != "per_head" && *in.Basis != "pct_biomass" {
			return invalid("basis must be per_head or pct_biomass")
		}
		if in.Status != nil && *in.Status != "active" && *in.Status != "inactive" {
			return invalid("status must be active or inactive")
		}
		if in.Items != nil {
			for _, it := range *in.Items {
				switch {
				case it.DayFrom < 0 || (it.DayTo != nil && *it.DayTo < it.DayFrom) || strings.TrimSpace(it.FeedName) == "":
					return invalid("each item needs a feed name and days from (0 or more) to (on or after from, or empty for onwards)")
				case (it.GramsPerHead == nil) == (it.PctBodyWeight == nil):
					return invalid("each item gives either grams a head a day or a %% of body weight")
				case (it.GramsPerHead != nil && *it.GramsPerHead <= 0) || (it.PctBodyWeight != nil && (*it.PctBodyWeight <= 0 || *it.PctBodyWeight > 100)):
					return invalid("grams and %% of body weight must be more than 0")
				}
			}
		}
		return nil
	}
	if err := check(); err != nil {
		writeErr(w, err, "feed plan")
		return
	}
	var out feedPlan
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var before []feedPlan
		if id == "" {
			if p := deref(in.Purpose); p != "" && !validPurpose(*in.Species, p) {
				return invalid("a %s plan is for %s", *in.Species, strings.Join(Purposes[*in.Species], ", "))
			}
			basis := "per_head"
			if in.Basis != nil {
				basis = *in.Basis
			}
			if err := tx.QueryRow(ctx, `INSERT INTO farm_feed_plans (merchant_id, species, purpose, name, basis)
				VALUES (current_setting('app.tenant_id')::uuid, $1, NULLIF($2, ''), trim($3), $4) RETURNING id::text`,
				*in.Species, deref(in.Purpose), *in.Name, basis).Scan(&id); err != nil {
				return err
			}
		} else {
			var err error
			if before, err = loadFeedPlans(ctx, tx, `id::text = $1`, id); err != nil {
				return err
			}
			if len(before) == 0 {
				return pgx.ErrNoRows
			}
			if in.Species != nil && *in.Species != before[0].Species {
				return invalid("a plan's species can't change")
			}
			if _, err := tx.Exec(ctx, `UPDATE farm_feed_plans SET name = COALESCE(NULLIF(trim($2), ''), name), status = COALESCE($3, status),
				basis = COALESCE($4, basis), purpose = CASE WHEN $5::text IS NULL THEN purpose ELSE NULLIF($5, '') END WHERE id::text = $1`,
				id, in.Name, in.Status, in.Basis, in.Purpose); err != nil {
				return err
			}
		}
		if in.Items != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM farm_feed_plan_items WHERE plan_id::text = $1`, id); err != nil {
				return err
			}
			for i, it := range *in.Items {
				if _, err := tx.Exec(ctx, `INSERT INTO farm_feed_plan_items (merchant_id, plan_id, day_from, day_to, feed_name, product_id, grams_per_head, pct_body_weight, sort_order)
					VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2, $3, trim($4), NULLIF($5, '')::uuid, $6, $7, $8)`,
					id, it.DayFrom, it.DayTo, it.FeedName, deref(it.ProductID), it.GramsPerHead, it.PctBodyWeight, i+1); err != nil {
					return err
				}
			}
		}
		list, err := loadFeedPlans(ctx, tx, `id::text = $1`, id)
		if err != nil {
			return err
		}
		out = list[0]
		action := "create"
		var b any
		if len(before) > 0 {
			action, b = "update", before[0]
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_feed_plans", EntityID: id, Action: action, PerformedBy: claims.UserID, Before: b, After: out})
	})
	if err != nil {
		writeErr(w, err, "feed plan")
		return
	}
	status := http.StatusOK
	if chi.URLParam(r, "id") == "" {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, out)
}

// farmBranchIDs: the active farm branches the caller sees (one, when given).
func farmBranchIDs(ctx context.Context, tx pgx.Tx, branchID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT b.id::text FROM branches b WHERE b.status = 'active' AND app_branch_visible(b.id)
		AND 'farm' = ANY(app_branch_features(b.id)) AND ($1 = '' OR b.id::text = $1) ORDER BY b.name`, branchID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

type feedStockRow struct {
	BranchID     string   `json:"branch_id"`
	Branch       string   `json:"branch"`
	ProductID    string   `json:"product_id"`
	Product      string   `json:"product"`
	Unit         string   `json:"unit"`
	Free         float64  `json:"free"`          // on hand less held
	PlannedDaily *float64 `json:"planned_daily"` // today's rations of the active batches, in the item's unit
	UsedDaily    float64  `json:"used_daily"`    // the last 7 days' feed, a day
	DaysLeft     *float64 `json:"days_left"`
	Low          bool     `json:"low"`
}

// feedStock: each farm branch's feed (farm items of role feed, the feeds
// its batches' plans name, and what it fed in the last 30 days) with the
// days it lasts at the larger of the plan and the last week's use.
func feedStock(ctx context.Context, tx pgx.Tx, branchID string, today time.Time) ([]feedStockRow, error) {
	branches, err := farmBranchIDs(ctx, tx, branchID)
	if err != nil {
		return nil, err
	}
	var lowDays int
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT feed_low_days FROM farm_settings), 7)`).Scan(&lowDays); err != nil {
		return nil, err
	}
	out := []feedStockRow{}
	for _, b := range branches {
		// Today's plan, from the branch's active batches.
		planned := map[string]float64{} // product → kg
		brows, err := tx.Query(ctx, `SELECT id::text FROM farm_batches WHERE branch_id::text = $1 AND status = 'active' AND feed_plan_id IS NOT NULL`, b)
		if err != nil {
			return nil, err
		}
		ids, err := pgx.CollectRows(brows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			x, err := loadBatch(ctx, tx, id)
			if err != nil {
				return nil, err
			}
			for _, r := range x.KPI.FeedToday {
				if r.ProductID != nil && r.Kg != nil {
					planned[*r.ProductID] += *r.Kg
				}
			}
		}
		rows, err := tx.Query(ctx, `
			WITH prods AS (
				SELECT fi.product_id FROM farm_items fi WHERE fi.role = 'feed'
				UNION SELECT i.product_id FROM farm_batches fb JOIN farm_feed_plan_items i ON i.plan_id = fb.feed_plan_id
				      WHERE fb.branch_id::text = $1 AND fb.status = 'active' AND i.product_id IS NOT NULL
				UNION SELECT v.product_id FROM farm_events e JOIN product_variants v ON v.id = e.variant_id
				      WHERE e.branch_id::text = $1 AND e.event_type = 'feed' AND e.status = 'posted' AND e.event_date > $2::date - 30)
			SELECT br.name, p.id::text, p.name, COALESCE(u.symbol, ''), COALESCE(u.kind, ''), COALESCE(u.factor, 0)::float8,
			       COALESCE((SELECT sum(sl.on_hand - sl.reserved) FROM stock_levels sl JOIN product_variants v ON v.id = sl.variant_id
			                 WHERE v.product_id = p.id AND sl.branch_id::text = $1), 0)::float8,
			       COALESCE((SELECT sum(e.quantity) FROM farm_events e JOIN product_variants v ON v.id = e.variant_id
			                 WHERE v.product_id = p.id AND e.branch_id::text = $1 AND e.event_type = 'feed' AND e.status = 'posted'
			                   AND e.event_date > $2::date - 7 AND e.event_date <= $2::date), 0)::float8
			FROM prods JOIN products p ON p.id = prods.product_id LEFT JOIN units u ON u.id = p.unit_id
			CROSS JOIN (SELECT name FROM branches WHERE id::text = $1) br
			ORDER BY p.name`, b, today)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var row feedStockRow
			var kind string
			var factor, used7 float64
			if err := rows.Scan(&row.Branch, &row.ProductID, &row.Product, &row.Unit, &kind, &factor, &row.Free, &used7); err != nil {
				rows.Close()
				return nil, err
			}
			row.BranchID = b
			row.UsedDaily = round3(used7 / 7)
			if kg, ok := planned[row.ProductID]; ok && kind == "weight" && factor > 0 {
				p := round3(kg / factor)
				row.PlannedDaily = &p
			}
			rate := row.UsedDaily
			if row.PlannedDaily != nil {
				rate = math.Max(rate, *row.PlannedDaily)
			}
			if rate > 0 {
				d := math.Round(math.Max(row.Free, 0)/rate*10) / 10
				row.DaysLeft = &d
				row.Low = d < float64(lowDays)
			}
			out = append(out, row)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// FeedStock: GET /farm/feed/stock?branch_id= (farm.record)
func (h *Handler) FeedStock(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var out []feedStockRow
	var lowDays int
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if out, err = feedStock(ctx, tx, r.URL.Query().Get("branch_id"), merchantToday(ctx, tx)); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT COALESCE((SELECT feed_low_days FROM farm_settings), 7)`).Scan(&lowDays)
	})
	if err != nil {
		writeErr(w, err, "feed stock")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out, "feed_low_days": lowDays})
}

// ---- recipes and mixes -----------------------------------------------------

type recipeItem struct {
	VariantID string  `json:"variant_id"`
	Product   string  `json:"product"`
	Unit      string  `json:"unit"`
	Quantity  float64 `json:"quantity"`
	UnitCost  float64 `json:"unit_cost"` // the ingredient's cost now
}

type recipe struct {
	ID              string       `json:"recipe_id"`
	Name            string       `json:"name"`
	OutputVariantID string       `json:"output_variant_id"`
	Output          string       `json:"output"`
	OutputUnit      string       `json:"output_unit"`
	BatchSize       float64      `json:"batch_size"`
	Notes           *string      `json:"notes"`
	Status          string       `json:"status"`
	Items           []recipeItem `json:"items"`
	CostPerBatch    float64      `json:"cost_per_batch"` // at today's costs
	CostPerUnit     float64      `json:"cost_per_unit"`
}

func loadRecipes(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]recipe, error) {
	rows, err := tx.Query(ctx, `SELECT r.id::text, r.name, r.output_variant_id::text, p.name, COALESCE(u.symbol, ''), r.batch_size::float8, r.notes, r.status
		FROM farm_feed_recipes r JOIN product_variants v ON v.id = r.output_variant_id JOIN products p ON p.id = v.product_id LEFT JOIN units u ON u.id = p.unit_id
		WHERE `+where+` ORDER BY r.status, r.name`, args...)
	if err != nil {
		return nil, err
	}
	out := []recipe{}
	for rows.Next() {
		var x recipe
		if err := rows.Scan(&x.ID, &x.Name, &x.OutputVariantID, &x.Output, &x.OutputUnit, &x.BatchSize, &x.Notes, &x.Status); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		irows, err := tx.Query(ctx, `SELECT i.variant_id::text, p.name, COALESCE(u.symbol, ''), i.quantity::float8, v.cost_price::float8
			FROM farm_feed_recipe_items i JOIN product_variants v ON v.id = i.variant_id JOIN products p ON p.id = v.product_id LEFT JOIN units u ON u.id = p.unit_id
			WHERE i.recipe_id::text = $1 ORDER BY i.sort_order`, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Items = []recipeItem{}
		for irows.Next() {
			var it recipeItem
			if err := irows.Scan(&it.VariantID, &it.Product, &it.Unit, &it.Quantity, &it.UnitCost); err != nil {
				irows.Close()
				return nil, err
			}
			out[i].Items = append(out[i].Items, it)
			out[i].CostPerBatch += it.Quantity * it.UnitCost
		}
		irows.Close()
		if err := irows.Err(); err != nil {
			return nil, err
		}
		out[i].CostPerBatch = round2(out[i].CostPerBatch)
		out[i].CostPerUnit = round2(out[i].CostPerBatch / out[i].BatchSize)
	}
	return out, nil
}

// ListRecipes: GET /farm/feed/recipes (farm.record)
func (h *Handler) ListRecipes(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var out []recipe
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = loadRecipes(ctx, tx, `true`)
		return err
	})
	if err != nil {
		writeErr(w, err, "recipes")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"recipes": out})
}

// SaveRecipe: POST /farm/feed/recipes and PUT /farm/feed/recipes/{id}
// {name, output_variant_id, batch_size, notes?, status?, items: [{variant_id,
// quantity}]} (farm.manage). Quantities are for one batch_size of output.
func (h *Handler) SaveRecipe(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in struct {
		Name            string  `json:"name"`
		OutputVariantID string  `json:"output_variant_id"`
		BatchSize       float64 `json:"batch_size"`
		Notes           *string `json:"notes"`
		Status          string  `json:"status"`
		Items           []struct {
			VariantID string  `json:"variant_id"`
			Quantity  float64 `json:"quantity"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if in.Status == "" {
		in.Status = "active"
	}
	switch {
	case strings.TrimSpace(in.Name) == "" || in.OutputVariantID == "" || in.BatchSize <= 0:
		writeErr(w, invalid("a recipe needs a name, the feed it makes and how much one batch makes"), "recipe")
		return
	case len(in.Items) == 0:
		writeErr(w, invalid("add the ingredients"), "recipe")
		return
	case in.Status != "active" && in.Status != "inactive":
		writeErr(w, invalid("status must be active or inactive"), "recipe")
		return
	}
	seen := map[string]bool{}
	for _, it := range in.Items {
		switch {
		case it.VariantID == "" || it.Quantity <= 0:
			writeErr(w, invalid("each ingredient needs an item and a quantity"), "recipe")
			return
		case it.VariantID == in.OutputVariantID:
			writeErr(w, invalid("the feed can't be its own ingredient"), "recipe")
			return
		case seen[it.VariantID]:
			writeErr(w, invalid("an ingredient is listed twice"), "recipe")
			return
		}
		seen[it.VariantID] = true
	}
	var out recipe
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var before []recipe
		if id == "" {
			if err := tx.QueryRow(ctx, `INSERT INTO farm_feed_recipes (merchant_id, name, output_variant_id, batch_size, notes, status)
				VALUES (current_setting('app.tenant_id')::uuid, trim($1), $2::uuid, $3, NULLIF(trim($4), ''), $5) RETURNING id::text`,
				in.Name, in.OutputVariantID, in.BatchSize, deref(in.Notes), in.Status).Scan(&id); err != nil {
				return err
			}
		} else {
			var err error
			if before, err = loadRecipes(ctx, tx, `r.id::text = $1`, id); err != nil {
				return err
			}
			if len(before) == 0 {
				return pgx.ErrNoRows
			}
			if _, err := tx.Exec(ctx, `UPDATE farm_feed_recipes SET name = trim($2), output_variant_id = $3::uuid, batch_size = $4, notes = NULLIF(trim($5), ''), status = $6
				WHERE id::text = $1`, id, in.Name, in.OutputVariantID, in.BatchSize, deref(in.Notes), in.Status); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM farm_feed_recipe_items WHERE recipe_id::text = $1`, id); err != nil {
				return err
			}
		}
		for i, it := range in.Items {
			if _, err := tx.Exec(ctx, `INSERT INTO farm_feed_recipe_items (merchant_id, recipe_id, variant_id, quantity, sort_order)
				VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3, $4)`, id, it.VariantID, it.Quantity, i+1); err != nil {
				return err
			}
		}
		list, err := loadRecipes(ctx, tx, `r.id::text = $1`, id)
		if err != nil {
			return err
		}
		out = list[0]
		action := "create"
		var b any
		if len(before) > 0 {
			action, b = "update", before[0]
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_feed_recipes", EntityID: id, Action: action, PerformedBy: claims.UserID, Before: b, After: out})
	})
	switch {
	case isUnique(err):
		writeErr(w, conflict("RECIPE_EXISTS", "there is already a recipe called %s", strings.TrimSpace(in.Name)), "recipe")
	case isFK(err):
		writeErr(w, invalid("choose items of this business"), "recipe")
	case err != nil:
		writeErr(w, err, "recipe")
	default:
		status := http.StatusOK
		if chi.URLParam(r, "id") == "" {
			status = http.StatusCreated
		}
		httpx.JSON(w, status, out)
	}
}

type mixLine struct {
	VariantID string  `json:"variant_id"`
	Product   string  `json:"product"`
	Unit      string  `json:"unit"`
	Quantity  float64 `json:"quantity"`
	UnitCost  float64 `json:"unit_cost"`
	Amount    float64 `json:"amount"`
}

type mix struct {
	ID              string    `json:"mix_id"`
	Number          string    `json:"mix_number"`
	BranchID        string    `json:"branch_id"`
	Branch          string    `json:"branch"`
	RecipeID        *string   `json:"recipe_id"`
	Recipe          *string   `json:"recipe"`
	OutputVariantID string    `json:"output_variant_id"`
	Output          string    `json:"output"`
	OutputUnit      string    `json:"output_unit"`
	OutputQty       float64   `json:"output_qty"`
	TotalCost       float64   `json:"total_cost"`
	UnitCost        float64   `json:"unit_cost"`
	MixedOn         string    `json:"mixed_on"`
	Notes           *string   `json:"notes"`
	Status          string    `json:"status"`
	CancelReason    *string   `json:"cancel_reason"`
	CreatedBy       *string   `json:"created_by"`
	Lines           []mixLine `json:"lines"`
}

func loadMixes(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]mix, error) {
	rows, err := tx.Query(ctx, `SELECT m.id::text, m.mix_number, m.branch_id::text, b.name, m.recipe_id::text, r.name, m.output_variant_id::text, p.name,
		COALESCE(u.symbol, ''), m.output_qty::float8, m.total_cost::float8, m.unit_cost::float8, m.mixed_on::text, m.notes, m.status, m.cancel_reason, cu.name
		FROM farm_feed_mixes m JOIN branches b ON b.id = m.branch_id LEFT JOIN farm_feed_recipes r ON r.id = m.recipe_id
		JOIN product_variants v ON v.id = m.output_variant_id JOIN products p ON p.id = v.product_id LEFT JOIN units u ON u.id = p.unit_id
		LEFT JOIN users cu ON cu.id = m.created_by
		WHERE `+where+` ORDER BY m.mixed_on DESC, m.created_at DESC LIMIT 300`, args...)
	if err != nil {
		return nil, err
	}
	out := []mix{}
	for rows.Next() {
		var x mix
		if err := rows.Scan(&x.ID, &x.Number, &x.BranchID, &x.Branch, &x.RecipeID, &x.Recipe, &x.OutputVariantID, &x.Output, &x.OutputUnit,
			&x.OutputQty, &x.TotalCost, &x.UnitCost, &x.MixedOn, &x.Notes, &x.Status, &x.CancelReason, &x.CreatedBy); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		lrows, err := tx.Query(ctx, `SELECT l.variant_id::text, p.name, COALESCE(u.symbol, ''), l.quantity::float8, l.unit_cost::float8, l.amount::float8
			FROM farm_feed_mix_lines l JOIN product_variants v ON v.id = l.variant_id JOIN products p ON p.id = v.product_id LEFT JOIN units u ON u.id = p.unit_id
			WHERE l.mix_id::text = $1 ORDER BY p.name`, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Lines = []mixLine{}
		for lrows.Next() {
			var l mixLine
			if err := lrows.Scan(&l.VariantID, &l.Product, &l.Unit, &l.Quantity, &l.UnitCost, &l.Amount); err != nil {
				lrows.Close()
				return nil, err
			}
			out[i].Lines = append(out[i].Lines, l)
		}
		lrows.Close()
		if err := lrows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ListMixes: GET /farm/feed/mixes?branch_id=&from=&to= (farm.record)
func (h *Handler) ListMixes(w http.ResponseWriter, r *http.Request) {
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
	var out []mix
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = loadMixes(ctx, tx, `($1 = '' OR m.branch_id::text = $1) AND ($2 = '' OR m.mixed_on >= $2::date) AND ($3 = '' OR m.mixed_on <= $3::date)`,
			q.Get("branch_id"), date(q.Get("from")), date(q.Get("to")))
		return err
	})
	if err != nil {
		writeErr(w, err, "feed mixes")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"mixes": out})
}

// CreateMix: POST /farm/feed/mixes {branch_id, recipe_id?, output_variant_id?,
// output_qty, mixed_on?, notes?, lines?: [{variant_id, quantity}]}
// (farm.record) — the ingredients leave the branch's stock at their cost
// and the feed comes in at their total ÷ its quantity. The recipe's
// quantities scale to output_qty unless lines give what was really used.
func (h *Handler) CreateMix(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var in struct {
		BranchID        string  `json:"branch_id"`
		RecipeID        string  `json:"recipe_id"`
		OutputVariantID string  `json:"output_variant_id"`
		OutputQty       float64 `json:"output_qty"`
		MixedOn         string  `json:"mixed_on"`
		Notes           *string `json:"notes"`
		Lines           []struct {
			VariantID string  `json:"variant_id"`
			Quantity  float64 `json:"quantity"`
		} `json:"lines"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if in.BranchID == "" || in.OutputQty <= 0 || (in.RecipeID == "" && (in.OutputVariantID == "" || len(in.Lines) == 0)) {
		writeErr(w, invalid("a mix needs the branch, how much it made, and a recipe (or the feed and its ingredients)"), "mix")
		return
	}
	var out mix
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := farmBranch(ctx, tx, in.BranchID, ""); err != nil {
			return err
		}
		today := merchantToday(ctx, tx)
		day := today
		if in.MixedOn != "" {
			d, ok := parseDate(in.MixedOn)
			if !ok || d.After(today) {
				return invalid("mixed_on must be a date up to today")
			}
			day = d
		}
		type line struct {
			variant string
			qty     float64
		}
		var lines []line
		output := in.OutputVariantID
		if in.RecipeID != "" {
			list, err := loadRecipes(ctx, tx, `r.id::text = $1`, in.RecipeID)
			if err != nil {
				return err
			}
			if len(list) == 0 || list[0].Status != "active" {
				return invalid("choose an active recipe")
			}
			rc := list[0]
			output = rc.OutputVariantID
			if len(in.Lines) == 0 {
				for _, it := range rc.Items {
					lines = append(lines, line{it.VariantID, round3(it.Quantity * in.OutputQty / rc.BatchSize)})
				}
			}
		}
		for _, l := range in.Lines {
			if l.VariantID == "" || l.Quantity <= 0 || l.VariantID == output {
				return invalid("each ingredient needs an item (not the feed itself) and a quantity")
			}
			lines = append(lines, line{l.VariantID, l.Quantity})
		}
		if err := catalog.CheckQuantity(ctx, tx, output, in.OutputQty); err != nil {
			return err
		}
		for _, l := range lines {
			if err := catalog.CheckQuantity(ctx, tx, l.variant, l.qty); err != nil {
				return err
			}
		}
		var n int
		if err := tx.QueryRow(ctx, `
			INSERT INTO farm_counters (merchant_id, last_no, last_mix_no) VALUES (current_setting('app.tenant_id')::uuid, 0, 1)
			ON CONFLICT (merchant_id) DO UPDATE SET last_mix_no = farm_counters.last_mix_no + 1 RETURNING last_mix_no`).Scan(&n); err != nil {
			return err
		}
		number := fmt.Sprintf("FM-%05d", n)
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO farm_feed_mixes (merchant_id, branch_id, mix_number, recipe_id, output_variant_id, output_qty, total_cost, unit_cost,
				mixed_on, notes, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2, NULLIF($3, '')::uuid, $4::uuid, $5, 0, 0, $6, NULLIF(trim($7), ''), $8) RETURNING id::text`,
			in.BranchID, number, in.RecipeID, output, in.OutputQty, day, deref(in.Notes), claims.UserID).Scan(&id); err != nil {
			return err
		}
		total := 0.0
		for _, l := range lines {
			unit, err := moveStock(ctx, tx, in.BranchID, l.variant, -l.qty, 0, "farm_feed_mix", id, "Feed mix "+number, claims.UserID, "")
			if err != nil {
				return err
			}
			amount := round2(unit * l.qty)
			total += amount
			if _, err := tx.Exec(ctx, `INSERT INTO farm_feed_mix_lines (merchant_id, mix_id, variant_id, quantity, unit_cost, amount)
				VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3, $4, $5)`, id, l.variant, l.qty, unit, amount); err != nil {
				return err
			}
		}
		total = round2(total)
		unitCost := round2(total / in.OutputQty)
		label := "FM" + fmt.Sprintf("%05d", n)
		if unitCost, err := moveStock(ctx, tx, in.BranchID, output, in.OutputQty, unitCost, "farm_feed_mix", id, "Feed mix "+number, claims.UserID, label); err != nil {
			return err
		} else if _, err := tx.Exec(ctx, `UPDATE farm_feed_mixes SET total_cost = $2, unit_cost = $3 WHERE id::text = $1`, id, total, unitCost); err != nil {
			return err
		}
		list, err := loadMixes(ctx, tx, `m.id::text = $1`, id)
		if err != nil {
			return err
		}
		out = list[0]
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_feed_mixes", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: out})
	})
	if catalog.WriteQuantityError(w, err) {
		return
	}
	if err != nil {
		writeErr(w, err, "mix")
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// CancelMix: POST /farm/feed/mixes/{id}/cancel {reason} (farm.manage) —
// the feed leaves stock again (it must still be there) and the
// ingredients come back at the cost they left at.
func (h *Handler) CancelMix(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil || strings.TrimSpace(in.Reason) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason is required")
		return
	}
	var out mix
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM farm_feed_mixes WHERE id::text = $1 FOR UPDATE`, id); err != nil {
			return err
		}
		list, err := loadMixes(ctx, tx, `m.id::text = $1`, id)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return pgx.ErrNoRows
		}
		before := list[0]
		if before.Status != "posted" {
			return conflict("MIX_CANCELLED", "already cancelled")
		}
		reason := "Feed mix " + before.Number + " cancelled: " + strings.TrimSpace(in.Reason)
		if _, err := moveStock(ctx, tx, before.BranchID, before.OutputVariantID, -before.OutputQty, 0, "farm_feed_mix", id, reason, claims.UserID, ""); err != nil {
			return err
		}
		for _, l := range before.Lines {
			if _, err := moveStock(ctx, tx, before.BranchID, l.VariantID, l.Quantity, l.UnitCost, "farm_feed_mix", id, reason, claims.UserID, "FM"+strings.TrimPrefix(before.Number, "FM-")+"R"); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE farm_feed_mixes SET status = 'cancelled', cancel_reason = trim($2), cancelled_by = $3, cancelled_at = now() WHERE id::text = $1`,
			id, in.Reason, claims.UserID); err != nil {
			return err
		}
		if list, err = loadMixes(ctx, tx, `m.id::text = $1`, id); err != nil {
			return err
		}
		out = list[0]
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_feed_mixes", EntityID: id, Action: "update", PerformedBy: claims.UserID, Before: before, After: out,
			Reason: "cancelled: " + strings.TrimSpace(in.Reason)})
	})
	if err != nil {
		writeErr(w, err, "mix")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
