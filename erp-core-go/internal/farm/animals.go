package farm

// The animal register (§3.82; migrations/079): a cow, goat, pig or
// breeding rabbit by its tag, in a herd (a batch): one of the batch's
// heads. Its own log is the batch's events that name it (treatments,
// weights, how it left); breeding (heat, service, pregnancy checks,
// births — the young join the register) and milk per milking.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// gestationDays: service to birth, typical.
var gestationDays = map[string]int{"cow": 283, "goat": 150, "pig": 114, "rabbit": 31}

// registerSpecies: the species kept one by one.
var registerSpecies = map[string]bool{"cow": true, "goat": true, "pig": true, "rabbit": true}

type animal struct {
	ID          string  `json:"animal_id"`
	Tag         string  `json:"tag"`
	Name        *string `json:"name"`
	Species     string  `json:"species"`
	Sex         string  `json:"sex"`
	Breed       *string `json:"breed"`
	BornOn      *string `json:"born_on"`
	AgeDays     *int    `json:"age_days"`
	Origin      string  `json:"origin"`
	JoinedOn    string  `json:"joined_on"`
	BranchID    string  `json:"branch_id"`
	Branch      string  `json:"branch"`
	BatchID     string  `json:"batch_id"`
	BatchNumber string  `json:"batch_number"`
	House       *string `json:"house"`
	DamID       *string `json:"dam_id"`
	DamTag      *string `json:"dam_tag"`
	SireID      *string `json:"sire_id"`
	SireTag     *string `json:"sire_tag"`
	SireNote    *string `json:"sire_note"`
	Status      string  `json:"status"`
	ExitOn      *string `json:"exit_on"`
	Notes       *string `json:"notes"`
	// Worked out.
	LatestWeightKg *float64   `json:"latest_weight_kg"`
	WeighedOn      *string    `json:"weighed_on"`
	Breeding       breedState `json:"breeding"`
	Milk7dAvgL     *float64   `json:"milk_7d_avg_l"` // litres a day, the last 7 days
	Withdrawal     withdrawal `json:"withdrawal"`
}

// breedState: where a female is in her cycle, from her breeding record.
type breedState struct {
	Status          string  `json:"status"` // open | served | pregnant | male | ""
	LastServiceOn   *string `json:"last_service_on"`
	ExpectedBirthOn *string `json:"expected_birth_on"`
	LastBirthOn     *string `json:"last_birth_on"`
	Lactating       bool    `json:"lactating"`
	DaysInMilk      *int    `json:"days_in_milk"`
	Births          int     `json:"births"`
}

const animalSelect = `
	SELECT a.id::text, a.tag, a.name, a.species, a.sex, a.breed, a.born_on::text, a.origin, a.joined_on::text, a.branch_id::text, b.name,
	       a.batch_id::text, fb.batch_number, h.name, a.dam_id::text, d.tag, a.sire_id::text, s.tag, a.sire_note, a.status, a.exit_on::text, a.notes
	FROM farm_animals a
	JOIN branches b ON b.id = a.branch_id
	JOIN farm_batches fb ON fb.id = a.batch_id
	LEFT JOIN farm_houses h ON h.id = fb.house_id
	LEFT JOIN farm_animals d ON d.id = a.dam_id
	LEFT JOIN farm_animals s ON s.id = a.sire_id`

func scanAnimal(row pgx.Row, a *animal) error {
	return row.Scan(&a.ID, &a.Tag, &a.Name, &a.Species, &a.Sex, &a.Breed, &a.BornOn, &a.Origin, &a.JoinedOn, &a.BranchID, &a.Branch,
		&a.BatchID, &a.BatchNumber, &a.House, &a.DamID, &a.DamTag, &a.SireID, &a.SireTag, &a.SireNote, &a.Status, &a.ExitOn, &a.Notes)
}

// fillAnimal works out an animal's age, weight, breeding state, milk and
// withdrawal.
func fillAnimal(ctx context.Context, tx pgx.Tx, a *animal, today time.Time) error {
	if a.BornOn != nil {
		if b, ok := parseDate(*a.BornOn); ok {
			until := today
			if a.ExitOn != nil {
				if e, ok := parseDate(*a.ExitOn); ok {
					until = e
				}
			}
			d := int(until.Sub(b).Hours() / 24)
			a.AgeDays = &d
		}
	}
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT avg_weight_kg::float8 FROM farm_events WHERE animal_id::text = $1 AND event_type = 'weight' AND status = 'posted' ORDER BY event_date DESC, created_at DESC LIMIT 1),
		       (SELECT event_date::text FROM farm_events WHERE animal_id::text = $1 AND event_type = 'weight' AND status = 'posted' ORDER BY event_date DESC, created_at DESC LIMIT 1),
		       (SELECT max(withdrawal_meat_until)::text FROM farm_events WHERE (animal_id::text = $1 OR (animal_id IS NULL AND batch_id::text = $2)) AND status = 'posted' AND withdrawal_meat_until > $3::date),
		       (SELECT max(withdrawal_milk_until)::text FROM farm_events WHERE (animal_id::text = $1 OR (animal_id IS NULL AND batch_id::text = $2)) AND status = 'posted' AND withdrawal_milk_until > $3::date),
		       (SELECT CASE WHEN count(*) = 0 THEN NULL ELSE round(sum(litres) / 7, 3)::float8 END FROM farm_milk_records
		         WHERE animal_id::text = $1 AND milked_on > $3::date - 7 AND milked_on <= $3::date)`, a.ID, a.BatchID, today).
		Scan(&a.LatestWeightKg, &a.WeighedOn, &a.Withdrawal.MeatUntil, &a.Withdrawal.MilkUntil, &a.Milk7dAvgL); err != nil {
		return err
	}
	if a.Sex == "male" {
		a.Breeding.Status = "male"
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT event_date::text, event_type, result FROM farm_breeding WHERE animal_id::text = $1 AND status = 'posted'
		ORDER BY event_date, created_at`, a.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	st := &a.Breeding
	st.Status = "open"
	var dryAfterBirth bool
	for rows.Next() {
		var on, typ string
		var result *string
		if err := rows.Scan(&on, &typ, &result); err != nil {
			return err
		}
		switch typ {
		case "service":
			d := on
			st.LastServiceOn, st.Status = &d, "served"
		case "pregnancy_check":
			if st.LastServiceOn != nil {
				if deref(result) == "positive" {
					st.Status = "pregnant"
				} else {
					st.Status = "open"
				}
			}
		case "birth":
			d := on
			st.LastBirthOn, st.Status, st.LastServiceOn = &d, "open", nil
			st.Births++
			dryAfterBirth = false
		case "abortion":
			st.Status, st.LastServiceOn = "open", nil
		case "dry_off":
			dryAfterBirth = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if st.LastServiceOn != nil && (st.Status == "served" || st.Status == "pregnant") {
		s, _ := parseDate(*st.LastServiceOn)
		e := s.AddDate(0, 0, gestationDays[a.Species]).Format("2006-01-02")
		st.ExpectedBirthOn = &e
	}
	if st.LastBirthOn != nil && !dryAfterBirth && a.Status == "active" {
		st.Lactating = true
		b, _ := parseDate(*st.LastBirthOn)
		d := int(today.Sub(b).Hours() / 24)
		st.DaysInMilk = &d
	}
	return nil
}

func loadAnimal(ctx context.Context, tx pgx.Tx, id string) (animal, error) {
	var a animal
	if err := scanAnimal(tx.QueryRow(ctx, animalSelect+` WHERE a.id::text = $1`, id), &a); err != nil {
		return a, err
	}
	return a, fillAnimal(ctx, tx, &a, merchantToday(ctx, tx))
}

// ListAnimals: GET /farm/animals?branch_id=&batch_id=&species=&status=&q=
// (farm.record); status defaults to active ("all" for every one).
func (h *Handler) ListAnimals(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	status := q.Get("status")
	if status == "" {
		status = "active"
	}
	if status == "all" {
		status = ""
	}
	out := []animal{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, animalSelect+`
			WHERE ($1 = '' OR a.branch_id::text = $1) AND ($2 = '' OR a.batch_id::text = $2) AND ($3 = '' OR a.species = $3) AND ($4 = '' OR a.status = $4)
			  AND ($5 = '' OR a.tag ILIKE '%' || $5 || '%' OR a.name ILIKE '%' || $5 || '%')
			ORDER BY a.status, a.tag LIMIT 1000`, q.Get("branch_id"), q.Get("batch_id"), q.Get("species"), status, strings.TrimSpace(q.Get("q")))
		if err != nil {
			return err
		}
		for rows.Next() {
			var a animal
			if err := scanAnimal(rows, &a); err != nil {
				rows.Close()
				return err
			}
			out = append(out, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		today := merchantToday(ctx, tx)
		for i := range out {
			if err := fillAnimal(ctx, tx, &out[i], today); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeErr(w, err, "animals")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"animals": out})
}

type breedingEvent struct {
	ID        string  `json:"breeding_id"`
	Date      string  `json:"event_date"`
	Type      string  `json:"event_type"`
	Method    *string `json:"method"`
	SireID    *string `json:"sire_id"`
	SireTag   *string `json:"sire_tag"`
	SireNote  *string `json:"sire_note"`
	Result    *string `json:"result"`
	BornAlive *int    `json:"born_alive"`
	BornDead  *int    `json:"born_dead"`
	Notes     *string `json:"notes"`
	Status    string  `json:"status"`
	CreatedBy *string `json:"created_by"`
}

// GetAnimal: GET /farm/animals/{id} — the animal, its log, breeding, the
// last 30 days' milk and its young (farm.record).
func (h *Handler) GetAnimal(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	type milkDay struct {
		Date   string  `json:"date"`
		AM     float64 `json:"am"`
		PM     float64 `json:"pm"`
		Day    float64 `json:"day"`
		Litres float64 `json:"litres"`
	}
	var resp struct {
		animal
		Events   []event         `json:"events"`
		Breeding []breedingEvent `json:"breeding_events"`
		Milk     []milkDay       `json:"milk"`
		Young    []animal        `json:"young"`
	}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if resp.animal, err = loadAnimal(ctx, tx, chi.URLParam(r, "id")); err != nil {
			return err
		}
		if resp.Events, err = listEvents(ctx, tx, `e.animal_id::text = $1`, resp.ID); err != nil {
			return err
		}
		if resp.Breeding, err = listBreeding(ctx, tx, resp.ID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT milked_on::text, COALESCE(sum(litres) FILTER (WHERE session = 'am'), 0)::float8,
			COALESCE(sum(litres) FILTER (WHERE session = 'pm'), 0)::float8, COALESCE(sum(litres) FILTER (WHERE session = 'day'), 0)::float8, sum(litres)::float8
			FROM farm_milk_records WHERE animal_id::text = $1 AND milked_on > $2::date - 30 GROUP BY milked_on ORDER BY milked_on DESC`, resp.ID, merchantToday(ctx, tx))
		if err != nil {
			return err
		}
		resp.Milk = []milkDay{}
		for rows.Next() {
			var m milkDay
			if err := rows.Scan(&m.Date, &m.AM, &m.PM, &m.Day, &m.Litres); err != nil {
				rows.Close()
				return err
			}
			resp.Milk = append(resp.Milk, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		yrows, err := tx.Query(ctx, animalSelect+` WHERE a.dam_id::text = $1 OR a.sire_id::text = $1 ORDER BY a.born_on DESC NULLS LAST, a.tag`, resp.ID)
		if err != nil {
			return err
		}
		resp.Young = []animal{}
		for yrows.Next() {
			var y animal
			if err := scanAnimal(yrows, &y); err != nil {
				yrows.Close()
				return err
			}
			resp.Young = append(resp.Young, y)
		}
		yrows.Close()
		return yrows.Err()
	})
	if err != nil {
		writeErr(w, err, "animal")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type animalInput struct {
	BatchID        string   `json:"batch_id"`
	Tag            *string  `json:"tag"`
	Name           *string  `json:"name"`
	Sex            *string  `json:"sex"`
	Breed          *string  `json:"breed"`
	BornOn         *string  `json:"born_on"`
	Origin         string   `json:"origin"` // existing (one of the batch's heads) | purchased (a new head)
	JoinedOn       string   `json:"joined_on"`
	DamID          *string  `json:"dam_id"`
	SireID         *string  `json:"sire_id"`
	SireNote       *string  `json:"sire_note"`
	Notes          *string  `json:"notes"`
	StockVariantID string   `json:"stock_variant_id"` // a purchased animal out of stock (bought on a GRN)
	StockQuantity  *float64 `json:"stock_quantity"`
}

// checkParents: a mother is a female and a father a male of the species.
func checkParents(ctx context.Context, tx pgx.Tx, species string, dam, sire *string) error {
	for _, p := range []struct {
		id  *string
		sex string
	}{{dam, "female"}, {sire, "male"}} {
		if deref(p.id) == "" {
			continue
		}
		var sp, sex string
		if err := tx.QueryRow(ctx, `SELECT species, sex FROM farm_animals WHERE id::text = $1`, *p.id).Scan(&sp, &sex); errors.Is(err, pgx.ErrNoRows) {
			return invalid("the parent isn't on the register")
		} else if err != nil {
			return err
		}
		if sp != species || sex != p.sex {
			return invalid("the mother must be a female and the father a male %s", species)
		}
	}
	return nil
}

func isFK(err error) bool {
	var pe interface{ SQLState() string }
	return errors.As(err, &pe) && pe.SQLState() == "23503"
}

// CreateAnimal: POST /farm/animals (farm.manage) — an animal onto the
// register: one of its batch's heads already counted ("existing"), or one
// bought later ("purchased"), which adds a head (an addition event, out of
// stock when stock_variant_id is given).
func (h *Handler) CreateAnimal(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var in animalInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if in.Origin == "" {
		in.Origin = "existing"
	}
	switch {
	case in.BatchID == "" || strings.TrimSpace(deref(in.Tag)) == "":
		writeErr(w, invalid("the batch and the tag are required"), "animal")
		return
	case deref(in.Sex) != "female" && deref(in.Sex) != "male":
		writeErr(w, invalid("sex must be female or male"), "animal")
		return
	case in.Origin != "existing" && in.Origin != "purchased":
		writeErr(w, invalid("origin must be existing or purchased (the young come from a birth)"), "animal")
		return
	case in.StockVariantID != "" && in.Origin != "purchased":
		writeErr(w, invalid("only a purchased animal comes out of stock"), "animal")
		return
	}
	var out animal
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM farm_batches WHERE id::text = $1 FOR UPDATE`, in.BatchID); err != nil {
			return err
		}
		var x batch
		if err := scanBatch(tx.QueryRow(ctx, batchSelect+` WHERE fb.id::text = $1`, in.BatchID), &x); errors.Is(err, pgx.ErrNoRows) {
			return invalid("choose a batch")
		} else if err != nil {
			return err
		}
		if !registerSpecies[x.Species] {
			return invalid("the register is for cattle, goats, pigs and rabbits; a %s batch is counted as a whole", x.Species)
		}
		if x.Status != "active" {
			return conflict("BATCH_CLOSED", "batch %s is closed", x.Number)
		}
		today := merchantToday(ctx, tx)
		// One of the batch's heads has been in it since the placement; a
		// purchase joins today unless said otherwise.
		placed, _ := parseDate(x.PlacedOn)
		joined := today
		if in.Origin == "existing" {
			joined = placed
		}
		if in.JoinedOn != "" {
			d, ok := parseDate(in.JoinedOn)
			if !ok || d.After(today) || d.Before(placed) {
				return invalid("joined_on must be between the batch's placement and today")
			}
			joined = d
		}
		var born *time.Time
		if s := deref(in.BornOn); s != "" {
			d, ok := parseDate(s)
			if !ok || d.After(joined) {
				return invalid("born_on must be a date on or before it joined")
			}
			born = &d
		}
		if err := checkParents(ctx, tx, x.Species, in.DamID, in.SireID); err != nil {
			return err
		}
		if in.Origin == "existing" {
			alive, err := aliveOf(ctx, tx, x.ID)
			if err != nil {
				return err
			}
			tagged, err := taggedActive(ctx, tx, x.ID)
			if err != nil {
				return err
			}
			if tagged >= alive {
				return conflict("REGISTER_FULL", "all %d animals of batch %s are on the register; a new animal is 'purchased'", alive, x.Number)
			}
		}
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO farm_animals (merchant_id, branch_id, batch_id, tag, name, species, sex, breed, born_on, origin, joined_on,
				dam_id, sire_id, sire_note, notes, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, trim($3), NULLIF(trim($4), ''), $5, $6, COALESCE(NULLIF(trim($7), ''), $8), $9, $10, $11,
				NULLIF($12, '')::uuid, NULLIF($13, '')::uuid, NULLIF(trim($14), ''), NULLIF(trim($15), ''), $16) RETURNING id::text`,
			x.BranchID, x.ID, *in.Tag, deref(in.Name), x.Species, *in.Sex, deref(in.Breed), x.Breed, born, in.Origin, joined,
			deref(in.DamID), deref(in.SireID), deref(in.SireNote), deref(in.Notes), claims.UserID).Scan(&id); err != nil {
			return err
		}
		if in.Origin == "purchased" {
			one := 1
			ev := eventInput{Type: "addition", Date: joined.Format("2006-01-02"), HeadCount: &one, Notes: strPtr("Bought: " + strings.TrimSpace(*in.Tag))}
			if in.StockVariantID != "" {
				q := 1.0
				if in.StockQuantity != nil {
					q = *in.StockQuantity
				}
				ev.VariantID, ev.Quantity = &in.StockVariantID, &q
			}
			if _, err := recordEvent(ctx, tx, &x, ev, claims.UserID); err != nil {
				return err
			}
		}
		var err error
		if out, err = loadAnimal(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_animals", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: out})
	})
	if isUnique(err) {
		writeErr(w, conflict("TAG_EXISTS", "another animal has tag %s", strings.TrimSpace(deref(in.Tag))), "animal")
		return
	}
	if err != nil {
		writeErr(w, err, "animal")
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func strPtr(s string) *string { return &s }

// UpdateAnimal: PATCH /farm/animals/{id} {tag?, name?, breed?, born_on?,
// dam_id?, sire_id?, sire_note?, notes?} (farm.manage)
func (h *Handler) UpdateAnimal(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in animalInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if in.Tag != nil && strings.TrimSpace(*in.Tag) == "" {
		writeErr(w, invalid("the tag can't be empty"), "animal")
		return
	}
	var out animal
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := loadAnimal(ctx, tx, id)
		if err != nil {
			return err
		}
		if (in.DamID != nil && *in.DamID == id) || (in.SireID != nil && *in.SireID == id) {
			return invalid("an animal can't be its own parent")
		}
		if err := checkParents(ctx, tx, before.Species, in.DamID, in.SireID); err != nil {
			return err
		}
		if s := deref(in.BornOn); s != "" {
			if d, ok := parseDate(s); !ok || d.After(merchantToday(ctx, tx)) {
				return invalid("born_on must be a date up to today")
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE farm_animals SET tag = COALESCE(NULLIF(trim($2), ''), tag),
			name = CASE WHEN $3::text IS NULL THEN name ELSE NULLIF(trim($3), '') END,
			breed = CASE WHEN $4::text IS NULL THEN breed ELSE NULLIF(trim($4), '') END,
			born_on = CASE WHEN $5::text IS NULL THEN born_on ELSE NULLIF($5, '')::date END,
			dam_id = CASE WHEN $6::text IS NULL THEN dam_id ELSE NULLIF($6, '')::uuid END,
			sire_id = CASE WHEN $7::text IS NULL THEN sire_id ELSE NULLIF($7, '')::uuid END,
			sire_note = CASE WHEN $8::text IS NULL THEN sire_note ELSE NULLIF(trim($8), '') END,
			notes = CASE WHEN $9::text IS NULL THEN notes ELSE NULLIF(trim($9), '') END
			WHERE id::text = $1`, id, in.Tag, in.Name, in.Breed, in.BornOn, in.DamID, in.SireID, in.SireNote, in.Notes); err != nil {
			return err
		}
		if out, err = loadAnimal(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_animals", EntityID: id, Action: "update", PerformedBy: claims.UserID, Before: before, After: out})
	})
	if isUnique(err) {
		writeErr(w, conflict("TAG_EXISTS", "another animal has tag %s", strings.TrimSpace(deref(in.Tag))), "animal")
		return
	}
	if err != nil {
		writeErr(w, err, "animal")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// MoveAnimal: POST /farm/animals/{id}/move {batch_id, date?} (farm.manage) —
// to another active batch of its species on the same branch: a head moves
// out of one and into the other (no stock or money moves; its cost so far
// stays with the batch it came from).
func (h *Handler) MoveAnimal(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in struct {
		BatchID string `json:"batch_id"`
		Date    string `json:"date"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<10)).Decode(&in); err != nil || in.BatchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "batch_id is required")
		return
	}
	var out animal
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := loadAnimal(ctx, tx, id)
		if err != nil {
			return err
		}
		if before.Status != "active" {
			return conflict("ANIMAL_NOT_ACTIVE", "%s is no longer on the farm", before.Tag)
		}
		if before.BatchID == in.BatchID {
			return invalid("%s is already in that batch", before.Tag)
		}
		var from, to batch
		for _, b := range []struct {
			id string
			x  *batch
		}{{before.BatchID, &from}, {in.BatchID, &to}} {
			if _, err := tx.Exec(ctx, `SELECT 1 FROM farm_batches WHERE id::text = $1 FOR UPDATE`, b.id); err != nil {
				return err
			}
			if err := scanBatch(tx.QueryRow(ctx, batchSelect+` WHERE fb.id::text = $1`, b.id), b.x); errors.Is(err, pgx.ErrNoRows) {
				return invalid("choose a batch")
			} else if err != nil {
				return err
			}
		}
		if to.Species != from.Species || to.BranchID != from.BranchID || to.Status != "active" {
			return invalid("move it to an active %s batch of the same branch", from.Species)
		}
		date := in.Date
		if date == "" {
			date = merchantToday(ctx, tx).Format("2006-01-02")
		}
		one := 1
		if _, err := recordEvent(ctx, tx, &from, eventInput{Type: "transfer_out", Date: date, HeadCount: &one, AnimalID: &id, relatedBatch: to.ID,
			Notes: strPtr(before.Tag + " moved to " + to.Number)}, claims.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE farm_animals SET batch_id = $2::uuid WHERE id::text = $1`, id, to.ID); err != nil {
			return err
		}
		if _, err := recordEvent(ctx, tx, &to, eventInput{Type: "transfer_in", Date: date, HeadCount: &one, AnimalID: &id, relatedBatch: from.ID,
			Notes: strPtr(before.Tag + " moved from " + from.Number)}, claims.UserID); err != nil {
			return err
		}
		if out, err = loadAnimal(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_animals", EntityID: id, Action: "update", PerformedBy: claims.UserID, Before: before, After: out,
			Reason: "moved to " + to.Number})
	})
	if err != nil {
		writeErr(w, err, "animal")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// ---- breeding ----------------------------------------------------------------

func listBreeding(ctx context.Context, tx pgx.Tx, animalID string) ([]breedingEvent, error) {
	rows, err := tx.Query(ctx, `SELECT b.id::text, b.event_date::text, b.event_type, b.method, b.sire_id::text, s.tag, b.sire_note, b.result, b.born_alive, b.born_dead,
		b.notes, b.status, u.name
		FROM farm_breeding b LEFT JOIN farm_animals s ON s.id = b.sire_id LEFT JOIN users u ON u.id = b.created_by
		WHERE b.animal_id::text = $1 ORDER BY b.event_date DESC, b.created_at DESC`, animalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []breedingEvent{}
	for rows.Next() {
		var b breedingEvent
		if err := rows.Scan(&b.ID, &b.Date, &b.Type, &b.Method, &b.SireID, &b.SireTag, &b.SireNote, &b.Result, &b.BornAlive, &b.BornDead,
			&b.Notes, &b.Status, &b.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

var breedingTypes = map[string]bool{"heat": true, "service": true, "pregnancy_check": true, "birth": true, "abortion": true, "dry_off": true}

// RecordBreeding: POST /farm/animals/{id}/breeding {event_type, event_date,
// method?, sire_id?, sire_note?, result?, born_alive?, born_dead?, young?:
// [{tag, sex, name?}], notes?} (farm.record) — a female's heat, service
// (natural or AI), pregnancy check, birth, abortion or drying off. A birth
// adds its live young to the herd (a birth event); the young named in
// `young` join the register with her as their mother.
func (h *Handler) RecordBreeding(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in struct {
		Type      string  `json:"event_type"`
		Date      string  `json:"event_date"`
		Method    *string `json:"method"`
		SireID    *string `json:"sire_id"`
		SireNote  *string `json:"sire_note"`
		Result    *string `json:"result"`
		BornAlive *int    `json:"born_alive"`
		BornDead  *int    `json:"born_dead"`
		Notes     *string `json:"notes"`
		Young     []struct {
			Tag  string  `json:"tag"`
			Sex  string  `json:"sex"`
			Name *string `json:"name"`
		} `json:"young"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	day, okDate := parseDate(in.Date)
	switch {
	case !breedingTypes[in.Type]:
		writeErr(w, invalid("event_type must be heat, service, pregnancy_check, birth, abortion or dry_off"), "breeding")
		return
	case !okDate:
		writeErr(w, invalid("event_date (YYYY-MM-DD) is required"), "breeding")
		return
	case in.Type == "service" && deref(in.Method) != "natural" && deref(in.Method) != "ai":
		writeErr(w, invalid("a service is natural or ai"), "breeding")
		return
	case in.Type == "pregnancy_check" && deref(in.Result) != "positive" && deref(in.Result) != "negative":
		writeErr(w, invalid("a pregnancy check is positive or negative"), "breeding")
		return
	case in.Type == "birth" && (in.BornAlive == nil || *in.BornAlive < 0 || (in.BornDead != nil && *in.BornDead < 0) || (*in.BornAlive == 0 && deref2(in.BornDead) == 0)):
		writeErr(w, invalid("a birth needs how many were born alive (and dead)"), "breeding")
		return
	case in.Type == "birth" && len(in.Young) > *in.BornAlive:
		writeErr(w, invalid("more young named than were born alive"), "breeding")
		return
	}
	for _, y := range in.Young {
		if strings.TrimSpace(y.Tag) == "" || (y.Sex != "female" && y.Sex != "male") {
			writeErr(w, invalid("each young one needs a tag and its sex"), "breeding")
			return
		}
	}
	var out animal
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		mother, err := loadAnimal(ctx, tx, id)
		if err != nil {
			return err
		}
		switch {
		case mother.Sex != "female":
			return invalid("breeding is recorded on the female")
		case mother.Status != "active":
			return conflict("ANIMAL_NOT_ACTIVE", "%s is no longer on the farm", mother.Tag)
		}
		joined, _ := parseDate(mother.JoinedOn)
		if day.Before(joined) || day.After(merchantToday(ctx, tx)) {
			return invalid("the date must be between when %s joined and today", mother.Tag)
		}
		if in.Type == "service" {
			if err := checkParents(ctx, tx, mother.Species, nil, in.SireID); err != nil {
				return err
			}
		} else {
			in.SireID, in.Method = nil, nil
		}
		if in.Type != "pregnancy_check" {
			in.Result = nil
		}
		if in.Type != "birth" {
			in.BornAlive, in.BornDead = nil, nil
		}
		var x batch
		if _, err := tx.Exec(ctx, `SELECT 1 FROM farm_batches WHERE id::text = $1 FOR UPDATE`, mother.BatchID); err != nil {
			return err
		}
		if err := scanBatch(tx.QueryRow(ctx, batchSelect+` WHERE fb.id::text = $1`, mother.BatchID), &x); err != nil {
			return err
		}
		var birthEvent string
		if in.Type == "birth" && *in.BornAlive > 0 {
			if birthEvent, err = recordEvent(ctx, tx, &x, eventInput{Type: "birth", Date: in.Date, HeadCount: in.BornAlive, AnimalID: &id,
				Notes: strPtr(fmt.Sprintf("Born to %s", mother.Tag))}, claims.UserID); err != nil {
				return err
			}
		}
		var bid string
		if err := tx.QueryRow(ctx, `INSERT INTO farm_breeding (merchant_id, branch_id, animal_id, event_date, event_type, method, sire_id, sire_note, result,
				born_alive, born_dead, birth_event_id, notes, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3, $4, $5, NULLIF($6, '')::uuid, NULLIF(trim($7), ''), $8, $9, $10,
				NULLIF($11, '')::uuid, NULLIF(trim($12), ''), $13) RETURNING id::text`,
			mother.BranchID, id, day, in.Type, in.Method, deref(in.SireID), deref(in.SireNote), in.Result, in.BornAlive, in.BornDead,
			birthEvent, deref(in.Notes), claims.UserID).Scan(&bid); err != nil {
			return err
		}
		if in.Type == "birth" && len(in.Young) > 0 {
			// The father: the last service's.
			var sireID, sireNote *string
			if err := tx.QueryRow(ctx, `SELECT sire_id::text, sire_note FROM farm_breeding WHERE animal_id::text = $1 AND event_type = 'service' AND status = 'posted'
				AND event_date <= $2 ORDER BY event_date DESC LIMIT 1`, id, day).Scan(&sireID, &sireNote); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			for _, y := range in.Young {
				if _, err := tx.Exec(ctx, `INSERT INTO farm_animals (merchant_id, branch_id, batch_id, tag, name, species, sex, breed, born_on, origin, joined_on,
						dam_id, sire_id, sire_note, created_by)
					VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, trim($3), NULLIF(trim($4), ''), $5, $6, $7, $8, 'born', $8, $9::uuid, $10::uuid, $11, $12)`,
					mother.BranchID, mother.BatchID, y.Tag, deref(y.Name), mother.Species, y.Sex, mother.Breed, day, id, sireID, sireNote, claims.UserID); err != nil {
					return err
				}
			}
		}
		if out, err = loadAnimal(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_breeding", EntityID: bid, Action: "create", PerformedBy: claims.UserID,
			After: map[string]any{"animal": mother.Tag, "event_type": in.Type, "event_date": in.Date, "born_alive": in.BornAlive}})
	})
	if isUnique(err) {
		writeErr(w, conflict("TAG_EXISTS", "a young one's tag is already on the register"), "breeding")
		return
	}
	if err != nil {
		writeErr(w, err, "breeding")
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func deref2(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// CancelBreeding: POST /farm/breeding/{id}/cancel {reason} (farm.manage) —
// a birth whose young joined the register stays (record how each left).
func (h *Handler) CancelBreeding(w http.ResponseWriter, r *http.Request) {
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
	var out animal
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var animalID, typ, status, date string
		var birthEvent *string
		if err := tx.QueryRow(ctx, `SELECT animal_id::text, event_type, status, event_date::text, birth_event_id::text FROM farm_breeding WHERE id::text = $1 FOR UPDATE`, id).
			Scan(&animalID, &typ, &status, &date, &birthEvent); err != nil {
			return err
		}
		if status != "posted" {
			return conflict("EVENT_CANCELLED", "already cancelled")
		}
		if typ == "birth" {
			var young int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM farm_animals WHERE dam_id::text = $1 AND born_on = $2::date AND origin = 'born'`, animalID, date).Scan(&young); err != nil {
				return err
			}
			if young > 0 {
				return conflict("BIRTH_HAS_YOUNG", "%d of the young are on the register; record how each left instead", young)
			}
			if birthEvent != nil {
				if _, _, err := cancelEvent(ctx, tx, *birthEvent, strings.TrimSpace(in.Reason), claims.UserID, true); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE farm_breeding SET status = 'cancelled', cancel_reason = trim($2) WHERE id::text = $1`, id, in.Reason); err != nil {
			return err
		}
		var err error
		if out, err = loadAnimal(ctx, tx, animalID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_breeding", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Reason: "cancelled: " + strings.TrimSpace(in.Reason)})
	})
	if err != nil {
		writeErr(w, err, "breeding")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// ---- milk --------------------------------------------------------------------

// MilkSheet: GET /farm/batches/{id}/milk?date= (farm.record) — the herd's
// females with what each gave that day, and who is in withdrawal.
func (h *Handler) MilkSheet(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	batchID := chi.URLParam(r, "id")
	type row struct {
		AnimalID   string   `json:"animal_id"`
		Tag        string   `json:"tag"`
		Name       *string  `json:"name"`
		Lactating  bool     `json:"lactating"`
		DaysInMilk *int     `json:"days_in_milk"`
		AM         *float64 `json:"am"`
		PM         *float64 `json:"pm"`
		Day        *float64 `json:"day"`
		MilkUntil  *string  `json:"withdrawal_milk_until"`
	}
	var resp struct {
		Date         string  `json:"date"`
		BatchID      string  `json:"batch_id"`
		BatchNumber  string  `json:"batch_number"`
		Rows         []row   `json:"animals"`
		BatchWideEnd *string `json:"batch_withdrawal_milk_until"`
	}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		day := merchantToday(ctx, tx)
		if s := r.URL.Query().Get("date"); s != "" {
			d, ok := parseDate(s)
			if !ok {
				return invalid("date must be YYYY-MM-DD")
			}
			day = d
		}
		resp.Date = day.Format("2006-01-02")
		var x batch
		if err := scanBatch(tx.QueryRow(ctx, batchSelect+` WHERE fb.id::text = $1`, batchID), &x); err != nil {
			return err
		}
		resp.BatchID, resp.BatchNumber = x.ID, x.Number
		if until, _, _, found, err := withdrawalBlock(ctx, tx, x.ID, "", "milk", day, true); err != nil {
			return err
		} else if found {
			resp.BatchWideEnd = &until
		}
		rows, err := tx.Query(ctx, animalSelect+` WHERE a.batch_id::text = $1 AND a.status = 'active' AND a.sex = 'female' ORDER BY a.tag`, x.ID)
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
		resp.Rows = []row{}
		for i := range list {
			a := &list[i]
			if err := fillAnimal(ctx, tx, a, day); err != nil {
				return err
			}
			rw := row{AnimalID: a.ID, Tag: a.Tag, Name: a.Name, Lactating: a.Breeding.Lactating, DaysInMilk: a.Breeding.DaysInMilk, MilkUntil: a.Withdrawal.MilkUntil}
			if err := tx.QueryRow(ctx, `SELECT sum(litres) FILTER (WHERE session = 'am')::float8, sum(litres) FILTER (WHERE session = 'pm')::float8,
				sum(litres) FILTER (WHERE session = 'day')::float8 FROM farm_milk_records WHERE animal_id::text = $1 AND milked_on = $2`, a.ID, day).
				Scan(&rw.AM, &rw.PM, &rw.Day); err != nil {
				return err
			}
			resp.Rows = append(resp.Rows, rw)
		}
		return nil
	})
	if err != nil {
		writeErr(w, err, "milk sheet")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// RecordMilk: POST /farm/batches/{id}/milk {date, session (am|pm|day),
// variant_id (the milk item, counted in a volume unit), entries:
// [{animal_id, litres}], unit_cost?, override_withdrawal?, override_reason?}
// (farm.record) — the milking's total goes into stock as one production
// event; each animal's litres are kept. An animal in withdrawal is refused
// unless farm.manage overrides it (the milk was discarded).
func (h *Handler) RecordMilk(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	batchID := chi.URLParam(r, "id")
	var in struct {
		Date      string   `json:"date"`
		Session   string   `json:"session"`
		VariantID string   `json:"variant_id"`
		UnitCost  *float64 `json:"unit_cost"`
		Entries   []struct {
			AnimalID string  `json:"animal_id"`
			Litres   float64 `json:"litres"`
		} `json:"entries"`
		OverrideWithdrawal bool   `json:"override_withdrawal"`
		OverrideReason     string `json:"override_reason"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if in.Session == "" {
		in.Session = "day"
	}
	total := 0.0
	seen := map[string]bool{}
	for _, e := range in.Entries {
		if e.AnimalID == "" || e.Litres < 0 || seen[e.AnimalID] {
			writeErr(w, invalid("each entry is an animal (once) and its litres (0 or more)"), "milk")
			return
		}
		seen[e.AnimalID] = true
		total += e.Litres
	}
	switch {
	case in.Session != "am" && in.Session != "pm" && in.Session != "day":
		writeErr(w, invalid("session must be am, pm or day"), "milk")
		return
	case in.VariantID == "" || total <= 0:
		writeErr(w, invalid("choose the milk item and enter the litres"), "milk")
		return
	}
	var out event
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM farm_batches WHERE id::text = $1 FOR UPDATE`, batchID); err != nil {
			return err
		}
		var x batch
		if err := scanBatch(tx.QueryRow(ctx, batchSelect+` WHERE fb.id::text = $1`, batchID), &x); err != nil {
			return err
		}
		day, ok := parseDate(in.Date)
		if !ok {
			return invalid("date (YYYY-MM-DD) is required")
		}
		var kind string
		var factor float64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(u.kind, ''), COALESCE(u.factor, 0)::float8 FROM product_variants v JOIN products p ON p.id = v.product_id
			LEFT JOIN units u ON u.id = p.unit_id WHERE v.id::text = $1`, in.VariantID).Scan(&kind, &factor); errors.Is(err, pgx.ErrNoRows) {
			return invalid("choose the milk item")
		} else if err != nil {
			return err
		}
		if kind != "volume" || factor <= 0 {
			return invalid("the milk item must be counted in litres (or ml)")
		}
		canManage := false
		if in.OverrideWithdrawal {
			var err error
			if canManage, err = hasPermission(ctx, tx, claims.UserID, "farm.manage"); err != nil {
				return err
			}
		}
		override := ""
		for _, e := range in.Entries {
			var tag, sex, status, b string
			if err := tx.QueryRow(ctx, `SELECT tag, sex, status, batch_id::text FROM farm_animals WHERE id::text = $1`, e.AnimalID).Scan(&tag, &sex, &status, &b); errors.Is(err, pgx.ErrNoRows) {
				return invalid("an animal isn't on the register")
			} else if err != nil {
				return err
			}
			if b != x.ID || sex != "female" || status != "active" {
				return invalid("%s isn't a female of batch %s", tag, x.Number)
			}
			var dup bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM farm_milk_records WHERE animal_id::text = $1 AND milked_on = $2 AND session = $3)`,
				e.AnimalID, day, in.Session).Scan(&dup); err != nil {
				return err
			}
			if dup {
				return conflict("MILK_ALREADY_RECORDED", "%s's %s milk on %s is already recorded; cancel that milking first", tag, in.Session, day.Format("2 Jan"))
			}
			if e.Litres == 0 {
				continue
			}
			until, what, on, found, err := withdrawalBlock(ctx, tx, x.ID, e.AnimalID, "milk", day, false)
			if err != nil {
				return err
			}
			if found {
				switch {
				case !in.OverrideWithdrawal:
					return withdrawalErr(x.Number, tag, "milk", what, on, until)
				case !canManage:
					return forbidden("only someone with farm.manage can record milk during a withdrawal period")
				case strings.TrimSpace(in.OverrideReason) == "":
					return invalid("say why milk is recorded during the withdrawal (override_reason)")
				}
				override = strings.TrimSpace(in.OverrideReason)
			}
		}
		qty := round3(total / factor)
		ev := eventInput{Type: "production", Date: in.Date, VariantID: &in.VariantID, Quantity: &qty, UnitCost: in.UnitCost,
			ProduceKind: strPtr("milk"), Notes: strPtr(fmt.Sprintf("Milking (%s): %s L", in.Session, trimNum(total))), batchWideOnly: true,
			OverrideWithdrawal: in.OverrideWithdrawal, OverrideReason: in.OverrideReason, canManage: canManage}
		id, err := recordEvent(ctx, tx, &x, ev, claims.UserID)
		if err != nil {
			return err
		}
		if override != "" {
			if _, err := tx.Exec(ctx, `UPDATE farm_events SET withdrawal_override = COALESCE(withdrawal_override, $2) WHERE id::text = $1`, id, override); err != nil {
				return err
			}
		}
		for _, e := range in.Entries {
			if _, err := tx.Exec(ctx, `INSERT INTO farm_milk_records (merchant_id, branch_id, batch_id, animal_id, event_id, milked_on, session, litres)
				VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7)`,
				x.BranchID, x.ID, e.AnimalID, id, day, in.Session, e.Litres); err != nil {
				return err
			}
		}
		if err := scanEvent(tx.QueryRow(ctx, eventSelect+` WHERE e.id::text = $1`, id), &out); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_events", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: out, Reason: "milking"})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "batch not found")
		return
	}
	if err != nil {
		writeErr(w, err, "milk")
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// AnimalExit: POST /farm/animals/{id}/exit {how: died|culled|harvested,
// date, cause?, variant_id?, quantity?, unit_cost?, override_withdrawal?,
// override_reason?} (farm.record) — how an animal left: a mortality, cull
// or production (into stock: a live animal or its meat) of one head.
func (h *Handler) AnimalExit(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in struct {
		How                string   `json:"how"`
		Date               string   `json:"date"`
		Cause              *string  `json:"cause"`
		VariantID          *string  `json:"variant_id"`
		Quantity           *float64 `json:"quantity"`
		UnitCost           *float64 `json:"unit_cost"`
		Notes              *string  `json:"notes"`
		OverrideWithdrawal bool     `json:"override_withdrawal"`
		OverrideReason     string   `json:"override_reason"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	typ := map[string]string{"died": "mortality", "culled": "cull", "harvested": "production"}[in.How]
	if typ == "" {
		writeErr(w, invalid("how must be died, culled or harvested"), "animal")
		return
	}
	var out animal
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		a, err := loadAnimal(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM farm_batches WHERE id::text = $1 FOR UPDATE`, a.BatchID); err != nil {
			return err
		}
		var x batch
		if err := scanBatch(tx.QueryRow(ctx, batchSelect+` WHERE fb.id::text = $1`, a.BatchID), &x); err != nil {
			return err
		}
		one := 1
		ev := eventInput{Type: typ, Date: in.Date, HeadCount: &one, AnimalID: &id, Cause: in.Cause, VariantID: in.VariantID, Quantity: in.Quantity,
			UnitCost: in.UnitCost, Notes: in.Notes, OverrideWithdrawal: in.OverrideWithdrawal, OverrideReason: in.OverrideReason}
		if typ == "production" {
			ev.ProduceKind = strPtr("meat")
		}
		if in.OverrideWithdrawal {
			if ev.canManage, err = hasPermission(ctx, tx, claims.UserID, "farm.manage"); err != nil {
				return err
			}
		}
		if _, err := recordEvent(ctx, tx, &x, ev, claims.UserID); err != nil {
			return err
		}
		if out, err = loadAnimal(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_animals", EntityID: id, Action: "update", PerformedBy: claims.UserID, Before: a, After: out, Reason: in.How})
	})
	if err != nil {
		writeErr(w, err, "animal")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
