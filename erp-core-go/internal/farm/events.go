package farm

// The daily log (farm_events): placement, mortality, culls, feed,
// medicine, vaccination, weights, production, expenses and notes; births,
// animals added later and an animal moved between batches (§3.82). Feed,
// medicine, a vaccine from stock and the animals placed or added leave
// stock at cost into 1500; production enters stock out of 1500. A
// cancelled event puts its stock and its entry back; nothing is deleted.
//
// A medicine or vaccination keeps its withdrawal dates (the day meat,
// milk or eggs may be taken again); produce recorded before then is
// refused (WITHDRAWAL_PERIOD) unless farm.manage overrides it with a
// reason.

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

// eventTypes: what POST /farm/batches/{id}/events records. Placement is the
// batch itself; transfers come from moving an animal.
var eventTypes = map[string]bool{"mortality": true, "cull": true, "feed": true, "medicine": true, "vaccination": true,
	"weight": true, "production": true, "expense": true, "note": true, "birth": true, "addition": true}

const eventTypeList = "mortality, cull, feed, medicine, vaccination, weight, production, expense, note, birth or addition"

var produceKinds = map[string]bool{"meat": true, "milk": true, "eggs": true, "other": true}

type eventInput struct {
	Type           string   `json:"event_type"`
	Date           string   `json:"event_date"`
	HeadCount      *int     `json:"head_count"`
	VariantID      *string  `json:"variant_id"`
	Quantity       *float64 `json:"quantity"`
	UnitCost       *float64 `json:"unit_cost"`
	Amount         *float64 `json:"amount"`
	AvgWeightKg    *float64 `json:"avg_weight_kg"`
	SampleSize     *int     `json:"sample_size"`
	Cause          *string  `json:"cause"`
	Vaccine        *string  `json:"vaccine"`
	Method         *string  `json:"method"`
	ScheduleItemID *string  `json:"schedule_item_id"`
	Notes          *string  `json:"notes"`
	// §3.82
	AnimalID           *string `json:"animal_id"`
	ProduceKind        *string `json:"produce_kind"` // meat | milk | eggs | other; worked out from the item when not given
	Dose               *string `json:"dose"`
	CourseDays         *int    `json:"course_days"`
	Vet                *string `json:"vet"`
	WithdrawalMeatDays *int    `json:"withdrawal_meat_days"` // not given = the farm item's
	WithdrawalMilkDays *int    `json:"withdrawal_milk_days"`
	WithdrawalEggsDays *int    `json:"withdrawal_eggs_days"`
	OverrideWithdrawal bool    `json:"override_withdrawal"`
	OverrideReason     string  `json:"override_reason"`

	canManage     bool   // the caller has farm.manage (may override a withdrawal)
	relatedBatch  string // a transfer's other batch
	batchWideOnly bool   // the milk sheet checked each animal's withdrawal itself
}

type event struct {
	ID                  string   `json:"event_id"`
	BatchID             string   `json:"batch_id"`
	BatchNumber         string   `json:"batch_number"`
	BranchID            string   `json:"branch_id"`
	Date                string   `json:"event_date"`
	Type                string   `json:"event_type"`
	HeadCount           *int     `json:"head_count"`
	VariantID           *string  `json:"variant_id"`
	Product             *string  `json:"product"`
	Unit                *string  `json:"unit"`
	Quantity            *float64 `json:"quantity"`
	UnitCost            *float64 `json:"unit_cost"`
	Amount              *float64 `json:"amount"`
	AvgWeightKg         *float64 `json:"avg_weight_kg"`
	SampleSize          *int     `json:"sample_size"`
	Cause               *string  `json:"cause"`
	Vaccine             *string  `json:"vaccine"`
	Method              *string  `json:"method"`
	ScheduleItemID      *string  `json:"schedule_item_id"`
	Notes               *string  `json:"notes"`
	JournalEntryID      *string  `json:"journal_entry_id"`
	Status              string   `json:"status"`
	CancelReason        *string  `json:"cancel_reason"`
	CreatedBy           *string  `json:"created_by"`
	CreatedAt           string   `json:"created_at"`
	AnimalID            *string  `json:"animal_id"`
	AnimalTag           *string  `json:"animal_tag"`
	ProduceKind         *string  `json:"produce_kind"`
	Dose                *string  `json:"dose"`
	CourseDays          *int     `json:"course_days"`
	Vet                 *string  `json:"vet"`
	WithdrawalMeatUntil *string  `json:"withdrawal_meat_until"`
	WithdrawalMilkUntil *string  `json:"withdrawal_milk_until"`
	WithdrawalEggsUntil *string  `json:"withdrawal_eggs_until"`
	WithdrawalOverride  *string  `json:"withdrawal_override"`
	RelatedBatch        *string  `json:"related_batch"`
}

const eventSelect = `
	SELECT e.id::text, e.batch_id::text, fb.batch_number, e.branch_id::text, e.event_date::text, e.event_type, e.head_count,
	       e.variant_id::text, p.name, u.symbol, e.quantity::float8, e.unit_cost::float8, e.amount::float8, e.avg_weight_kg::float8, e.sample_size,
	       e.cause, e.vaccine, e.method, e.schedule_item_id::text, e.notes, e.journal_entry_id::text, e.status, e.cancel_reason, cu.name, e.created_at::text,
	       e.animal_id::text, a.tag, e.produce_kind, e.dose, e.course_days, e.vet, e.withdrawal_meat_until::text, e.withdrawal_milk_until::text,
	       e.withdrawal_eggs_until::text, e.withdrawal_override, rb.batch_number
	FROM farm_events e
	JOIN farm_batches fb ON fb.id = e.batch_id
	LEFT JOIN product_variants v ON v.id = e.variant_id LEFT JOIN products p ON p.id = v.product_id LEFT JOIN units u ON u.id = p.unit_id
	LEFT JOIN users cu ON cu.id = e.created_by
	LEFT JOIN farm_animals a ON a.id = e.animal_id
	LEFT JOIN farm_batches rb ON rb.id = e.related_batch_id`

func scanEvent(row pgx.Row, e *event) error {
	return row.Scan(&e.ID, &e.BatchID, &e.BatchNumber, &e.BranchID, &e.Date, &e.Type, &e.HeadCount, &e.VariantID, &e.Product, &e.Unit,
		&e.Quantity, &e.UnitCost, &e.Amount, &e.AvgWeightKg, &e.SampleSize, &e.Cause, &e.Vaccine, &e.Method, &e.ScheduleItemID, &e.Notes,
		&e.JournalEntryID, &e.Status, &e.CancelReason, &e.CreatedBy, &e.CreatedAt,
		&e.AnimalID, &e.AnimalTag, &e.ProduceKind, &e.Dose, &e.CourseDays, &e.Vet, &e.WithdrawalMeatUntil, &e.WithdrawalMilkUntil,
		&e.WithdrawalEggsUntil, &e.WithdrawalOverride, &e.RelatedBatch)
}

func listEvents(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]event, error) {
	rows, err := tx.Query(ctx, eventSelect+` WHERE `+where+` ORDER BY e.event_date DESC, e.created_at DESC LIMIT 1000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []event{}
	for rows.Next() {
		var e event
		if err := scanEvent(rows, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func positive(v *float64) bool { return v != nil && *v > 0 }

// headsOut: the event types that take heads out of a batch.
var headsOut = map[string]bool{"mortality": true, "cull": true, "production": true, "transfer_out": true}

// taggedActive: the batch's animals on the register, still in it.
func taggedActive(ctx context.Context, tx pgx.Tx, batchID string) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM farm_animals WHERE batch_id::text = $1 AND status = 'active'`, batchID).Scan(&n)
	return n, err
}

func aliveOf(ctx context.Context, tx pgx.Tx, batchID string) (int, error) {
	var alive int
	err := tx.QueryRow(ctx, `SELECT `+aliveSQL+` FROM farm_batches fb WHERE fb.id::text = $1`, batchID).Scan(&alive)
	return alive, err
}

// withdrawalBlock: the treatment that keeps produce of kind from the batch
// (or one animal) on day, if any. animalID "" with batchWideOnly false =
// any treatment of the batch, on any of its animals too.
func withdrawalBlock(ctx context.Context, tx pgx.Tx, batchID, animalID, kind string, day time.Time, batchWideOnly bool) (until, what, on string, found bool, err error) {
	col := map[string]string{"meat": "withdrawal_meat_until", "milk": "withdrawal_milk_until", "eggs": "withdrawal_eggs_until"}[kind]
	if col == "" {
		return "", "", "", false, nil
	}
	err = tx.QueryRow(ctx, `
		SELECT e.`+col+`::text, COALESCE(e.vaccine, p.name, e.event_type), e.event_date::text
		FROM farm_events e LEFT JOIN product_variants v ON v.id = e.variant_id LEFT JOIN products p ON p.id = v.product_id
		WHERE e.batch_id::text = $1 AND e.status = 'posted' AND e.event_type IN ('medicine', 'vaccination') AND e.`+col+` > $2::date
		  AND e.event_date <= $2::date
		  AND (e.animal_id IS NULL OR ($3 = '' AND NOT $4) OR e.animal_id::text = $3)
		ORDER BY e.`+col+` DESC LIMIT 1`, batchID, day, animalID, batchWideOnly).Scan(&until, &what, &on)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", false, nil
	}
	return until, what, on, err == nil, err
}

func withdrawalErr(batchNumber, tag, kind, what, on, until string) error {
	who := "batch " + batchNumber
	if tag != "" {
		who = tag
	}
	o, _ := parseDate(on)
	u, _ := parseDate(until)
	return &farmErr{status: http.StatusConflict, code: "WITHDRAWAL_PERIOD",
		msg: fmt.Sprintf("%s was given %s on %s: no %s until %s. Someone with farm.manage can override it with a reason (e.g. it was discarded)",
			who, what, o.Format("2 Jan"), kind, u.Format("2 Jan 2006")),
		details: map[string]any{"produce_kind": kind, "until": until, "treatment": what, "treated_on": on, "animal": tag}}
}

// produceKindOf: meat, milk or eggs from what is produced — a volume unit
// is milk, a count unit from a layer or breeder flock eggs, heads leaving
// or a weight unit meat.
func produceKindOf(ctx context.Context, tx pgx.Tx, x *batch, in eventInput) (string, error) {
	if k := deref(in.ProduceKind); k != "" {
		if !produceKinds[k] {
			return "", invalid("produce_kind must be meat, milk, eggs or other")
		}
		return k, nil
	}
	var kind string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(u.kind, '') FROM product_variants v JOIN products p ON p.id = v.product_id LEFT JOIN units u ON u.id = p.unit_id
		WHERE v.id::text = $1`, deref(in.VariantID)).Scan(&kind); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	switch {
	case kind == "volume":
		return "milk", nil
	case kind == "count" && x.Species == "chicken" && in.HeadCount == nil:
		return "eggs", nil
	case in.HeadCount != nil || kind == "weight":
		return "meat", nil
	}
	return "other", nil
}

// recordEvent validates and writes one event of batch x, moving stock and
// posting as its type needs. x needs ID, Number, BranchID, Status,
// PlacedOn and Species.
func recordEvent(ctx context.Context, tx pgx.Tx, x *batch, in eventInput, by string) (string, error) {
	day, ok := parseDate(in.Date)
	if !ok {
		return "", invalid("event_date (YYYY-MM-DD) is required")
	}
	if x.Status != "active" {
		return "", conflict("BATCH_CLOSED", "batch %s is closed", x.Number)
	}
	placed, _ := parseDate(x.PlacedOn)
	if day.Before(placed) || day.After(merchantToday(ctx, tx)) {
		return "", invalid("the date must be between the placement (%s) and today", placed.Format("02 Jan 2006"))
	}
	if in.HeadCount != nil && *in.HeadCount <= 0 {
		return "", invalid("head_count must be more than 0")
	}
	if in.Quantity != nil && *in.Quantity <= 0 {
		return "", invalid("quantity must be more than 0")
	}
	if in.CourseDays != nil && *in.CourseDays <= 0 {
		return "", invalid("course_days must be more than 0")
	}
	for _, d := range []*int{in.WithdrawalMeatDays, in.WithdrawalMilkDays, in.WithdrawalEggsDays} {
		if d != nil && *d < 0 {
			return "", invalid("withdrawal days can't be negative")
		}
	}
	// One animal of the batch (the register, §3.82).
	var tag, sex string
	if id := deref(in.AnimalID); id != "" {
		var batchID, status string
		if err := tx.QueryRow(ctx, `SELECT batch_id::text, status, tag, sex FROM farm_animals WHERE id::text = $1`, id).Scan(&batchID, &status, &tag, &sex); errors.Is(err, pgx.ErrNoRows) {
			return "", invalid("that animal isn't on the register")
		} else if err != nil {
			return "", err
		}
		if batchID != x.ID {
			return "", invalid("%s isn't in batch %s", tag, x.Number)
		}
		if status != "active" && in.Type != "transfer_in" {
			return "", conflict("ANIMAL_NOT_ACTIVE", "%s is no longer on the farm", tag)
		}
		switch in.Type {
		case "mortality", "cull", "production", "transfer_out", "transfer_in":
			if in.HeadCount != nil && *in.HeadCount != 1 {
				return "", invalid("one animal is one head")
			}
			one := 1
			in.HeadCount = &one
		case "medicine", "vaccination", "weight", "note", "birth":
		default:
			return "", invalid("a %s is for the whole batch, not one animal", in.Type)
		}
	} else {
		in.AnimalID = nil
	}
	needItem := false
	switch in.Type {
	case "placement":
	case "mortality", "cull", "birth", "addition", "transfer_in", "transfer_out":
		if in.HeadCount == nil {
			return "", invalid("how many (head_count)")
		}
	case "feed", "medicine":
		needItem = true
	case "vaccination":
		if strings.TrimSpace(deref(in.Vaccine)) == "" && deref(in.ScheduleItemID) == "" {
			return "", invalid("name the vaccine, or the schedule item it is")
		}
	case "weight":
		if !positive(in.AvgWeightKg) {
			return "", invalid("the average weight (avg_weight_kg)")
		}
	case "production":
		needItem = true
	case "expense":
		if !positive(in.Amount) || strings.TrimSpace(deref(in.Notes)) == "" {
			return "", invalid("an expense needs an amount and what it was for (notes)")
		}
	case "note":
		if strings.TrimSpace(deref(in.Notes)) == "" {
			return "", invalid("write the note")
		}
	default:
		return "", invalid("event_type must be %s", eventTypeList)
	}
	if needItem && (deref(in.VariantID) == "" || !positive(in.Quantity)) {
		return "", invalid("choose the item and the quantity")
	}
	if (in.VariantID == nil || *in.VariantID == "") != (in.Quantity == nil) && in.Type != "placement" {
		return "", invalid("an item needs a quantity, and a quantity an item")
	}
	if (in.Type == "birth" || in.Type == "transfer_in" || in.Type == "transfer_out") && deref(in.VariantID) != "" {
		return "", invalid("a %s moves no stock", in.Type)
	}
	// Heads that leave can't be more than are alive, nor leave the
	// register's animals uncounted.
	if in.HeadCount != nil && headsOut[in.Type] {
		alive, err := aliveOf(ctx, tx, x.ID)
		if err != nil {
			return "", err
		}
		if *in.HeadCount > alive {
			return "", conflict("MORE_THAN_ALIVE", "only %d are alive in batch %s", alive, x.Number)
		}
		if in.AnimalID == nil {
			tagged, err := taggedActive(ctx, tx, x.ID)
			if err != nil {
				return "", err
			}
			if alive-*in.HeadCount < tagged {
				return "", conflict("ANIMAL_REQUIRED", "batch %s has %d animals on the register and %d alive: say which animal it was", x.Number, tagged, alive)
			}
		}
	}
	if id := deref(in.ScheduleItemID); id != "" {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM farm_schedule_items i JOIN farm_batches fb ON fb.schedule_id = i.schedule_id
			WHERE i.id::text = $1 AND fb.id::text = $2)`, id, x.ID).Scan(&ok); err != nil {
			return "", err
		}
		if !ok {
			return "", invalid("that schedule item isn't on this batch's schedule")
		}
		if strings.TrimSpace(deref(in.Vaccine)) == "" {
			var name string
			if err := tx.QueryRow(ctx, `SELECT name FROM farm_schedule_items WHERE id::text = $1`, id).Scan(&name); err != nil {
				return "", err
			}
			in.Vaccine = &name
		}
	}
	// A treatment's withdrawal: the days given, else the farm item's.
	var wMeat, wMilk, wEggs *time.Time
	if in.Type == "medicine" || in.Type == "vaccination" {
		var dMeat, dMilk, dEggs *int
		if v := deref(in.VariantID); v != "" {
			if err := tx.QueryRow(ctx, `SELECT fi.withdrawal_meat_days, fi.withdrawal_milk_days, fi.withdrawal_eggs_days
				FROM product_variants pv JOIN farm_items fi ON fi.product_id = pv.product_id WHERE pv.id::text = $1`, v).
				Scan(&dMeat, &dMilk, &dEggs); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return "", err
			}
		}
		pick := func(given, item *int) *time.Time {
			d := item
			if given != nil {
				d = given
			}
			if d == nil || *d == 0 {
				return nil
			}
			t := day.AddDate(0, 0, *d)
			return &t
		}
		wMeat, wMilk, wEggs = pick(in.WithdrawalMeatDays, dMeat), pick(in.WithdrawalMilkDays, dMilk), pick(in.WithdrawalEggsDays, dEggs)
	}
	// Produce during a treatment's withdrawal.
	var kind, override string
	if in.Type == "production" {
		var err error
		if kind, err = produceKindOf(ctx, tx, x, in); err != nil {
			return "", err
		}
		until, what, on, found, err := withdrawalBlock(ctx, tx, x.ID, deref(in.AnimalID), kind, day, in.batchWideOnly)
		if err != nil {
			return "", err
		}
		if found {
			switch {
			case !in.OverrideWithdrawal:
				return "", withdrawalErr(x.Number, tag, kind, what, on, until)
			case !in.canManage:
				return "", forbidden("only someone with farm.manage can record produce during a withdrawal period")
			case strings.TrimSpace(in.OverrideReason) == "":
				return "", invalid("say why produce is recorded during the withdrawal (override_reason)")
			}
			override = strings.TrimSpace(in.OverrideReason)
		}
	}
	var eventID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO farm_events (merchant_id, branch_id, batch_id, event_date, event_type, head_count, variant_id, quantity, amount,
		    avg_weight_kg, sample_size, cause, vaccine, method, schedule_item_id, notes, created_by,
		    animal_id, produce_kind, dose, course_days, vet, withdrawal_meat_until, withdrawal_milk_until, withdrawal_eggs_until,
		    withdrawal_override, related_batch_id)
		VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3, $4, $5, NULLIF($6, '')::uuid, $7, $8, $9, $10,
		    NULLIF(trim($11), ''), NULLIF(trim($12), ''), NULLIF(trim($13), ''), NULLIF($14, '')::uuid, NULLIF(trim($15), ''), $16,
		    NULLIF($17, '')::uuid, NULLIF($18, ''), NULLIF(trim($19), ''), $20, NULLIF(trim($21), ''), $22, $23, $24, NULLIF($25, ''), NULLIF($26, '')::uuid)
		RETURNING id::text`,
		x.BranchID, x.ID, day, in.Type, in.HeadCount, deref(in.VariantID), in.Quantity, expenseAmount(in),
		in.AvgWeightKg, in.SampleSize, deref(in.Cause), deref(in.Vaccine), deref(in.Method), deref(in.ScheduleItemID), deref(in.Notes), by,
		deref(in.AnimalID), kind, deref(in.Dose), in.CourseDays, deref(in.Vet), wMeat, wMilk, wEggs, override, in.relatedBatch).Scan(&eventID); err != nil {
		return "", err
	}
	if v := deref(in.VariantID); v != "" && in.Quantity != nil && in.Type != "expense" && in.Type != "weight" && in.Type != "note" {
		date := day.Format("2006-01-02")
		label := strings.ReplaceAll(x.Number, "FB-", "F") + "-" + day.Format("060102")
		var unit, value float64
		var journalID string
		var err error
		if in.Type == "production" {
			cost := 0.0
			if in.UnitCost != nil {
				if *in.UnitCost < 0 {
					return "", invalid("unit_cost can't be negative")
				}
				cost = *in.UnitCost
			}
			if unit, err = moveStock(ctx, tx, x.BranchID, v, *in.Quantity, cost, "farm_event", eventID, "Farm "+x.Number+" production", by, label); err != nil {
				return "", err
			}
			value = round2(unit * *in.Quantity)
			journalID, err = post(ctx, tx, x.BranchID, "farm_output", eventID, "Farm "+x.Number+": produce into stock", by, date, value, "1200", AccountFarmWIP)
		} else {
			if unit, err = moveStock(ctx, tx, x.BranchID, v, -*in.Quantity, 0, "farm_event", eventID, "Farm "+x.Number+" "+in.Type, by, label); err != nil {
				return "", err
			}
			value = round2(unit * *in.Quantity)
			journalID, err = post(ctx, tx, x.BranchID, "farm_use", eventID, "Farm "+x.Number+": "+in.Type+" from stock", by, date, value, AccountFarmWIP, "1200")
		}
		if err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE farm_events SET unit_cost = $2, amount = $3, journal_entry_id = NULLIF($4, '')::uuid WHERE id::text = $1`,
			eventID, unit, value, journalID); err != nil {
			return "", err
		}
	}
	// An animal that died, was culled or harvested leaves the register.
	if id := deref(in.AnimalID); id != "" {
		status := map[string]string{"mortality": "dead", "cull": "culled", "production": "harvested"}[in.Type]
		if status != "" {
			if _, err := tx.Exec(ctx, `UPDATE farm_animals SET status = $2, exit_on = $3, exit_event_id = $4::uuid WHERE id::text = $1`,
				id, status, day, eventID); err != nil {
				return "", err
			}
		}
	}
	return eventID, nil
}

// expenseAmount: only an expense event takes its amount as given; the
// others' amount is the stock value moved.
func expenseAmount(in eventInput) *float64 {
	if in.Type == "expense" {
		return in.Amount
	}
	return nil
}

func hasPermission(ctx context.Context, tx pgx.Tx, userID, code string) (bool, error) {
	codes, err := authn.FetchPermissions(ctx, tx, userID)
	return authn.HasPermission(codes, code), err
}

// RecordEvent: POST /farm/batches/{id}/events (farm.record)
func (h *Handler) RecordEvent(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	batchID := chi.URLParam(r, "id")
	var in eventInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if !eventTypes[in.Type] {
		writeErr(w, invalid("event_type must be %s", eventTypeList), "event")
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
		var err error
		if in.OverrideWithdrawal {
			if in.canManage, err = hasPermission(ctx, tx, claims.UserID, "farm.manage"); err != nil {
				return err
			}
		}
		id, err := recordEvent(ctx, tx, &x, in, claims.UserID)
		if err != nil {
			return err
		}
		if err := scanEvent(tx.QueryRow(ctx, eventSelect+` WHERE e.id::text = $1`, id), &out); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_events", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: out})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "batch not found")
		return
	}
	if err != nil {
		writeErr(w, err, "event")
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// cancelEvent puts one posted event back: its stock and entry, an
// animal's exit, a day's milk sheet. by and reason for the record.
func cancelEvent(ctx context.Context, tx pgx.Tx, id, reason, by string, fromBreeding bool) (event, event, error) {
	var before, out event
	if err := scanEvent(tx.QueryRow(ctx, eventSelect+` WHERE e.id::text = $1 FOR UPDATE OF e`, id), &before); err != nil {
		return before, out, err
	}
	var x batch
	if err := scanBatch(tx.QueryRow(ctx, batchSelect+` WHERE fb.id::text = $1`, before.BatchID), &x); err != nil {
		return before, out, err
	}
	switch {
	case before.Status != "posted":
		return before, out, conflict("EVENT_CANCELLED", "already cancelled")
	case before.Type == "placement":
		return before, out, conflict("EVENT_PLACEMENT", "the placement is the batch itself; it can't be cancelled")
	case before.Type == "transfer_in" || before.Type == "transfer_out":
		return before, out, conflict("EVENT_TRANSFER", "this is an animal moved between batches; move it back instead")
	case x.Status != "active":
		return before, out, conflict("BATCH_CLOSED", "batch %s is closed", x.Number)
	}
	if before.Type == "birth" && !fromBreeding {
		var linked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM farm_breeding WHERE birth_event_id::text = $1 AND status = 'posted')`, id).Scan(&linked); err != nil {
			return before, out, err
		}
		if linked {
			return before, out, conflict("EVENT_FROM_BREEDING", "this birth was recorded on the mother; cancel it there")
		}
	}
	// Heads that came in (a birth, an addition) can go only while they
	// are still there for the register.
	if (before.Type == "birth" || before.Type == "addition") && before.HeadCount != nil {
		alive, err := aliveOf(ctx, tx, x.ID)
		if err != nil {
			return before, out, err
		}
		tagged, err := taggedActive(ctx, tx, x.ID)
		if err != nil {
			return before, out, err
		}
		if alive-*before.HeadCount < tagged || alive-*before.HeadCount < 0 {
			return before, out, conflict("MORE_THAN_ALIVE", "batch %s no longer has those %d heads to take back", x.Number, *before.HeadCount)
		}
	}
	if before.VariantID != nil && before.Quantity != nil && before.UnitCost != nil {
		label := strings.ReplaceAll(x.Number, "FB-", "F") + "-" + strings.ReplaceAll(before.Date[2:], "-", "")
		delta := *before.Quantity
		if before.Type == "production" {
			delta = -delta // the produce leaves stock again
		}
		if _, err := moveStock(ctx, tx, x.BranchID, *before.VariantID, delta, *before.UnitCost, "farm_event", id, "Farm "+x.Number+" cancelled: "+reason, by, label); err != nil {
			return before, out, err
		}
		if before.Amount != nil && *before.Amount != 0 {
			debit, credit := "1200", AccountFarmWIP // undo a use
			if before.Type == "production" {
				debit, credit = AccountFarmWIP, "1200"
			}
			if _, err := post(ctx, tx, x.BranchID, "farm_cancel", id, "Farm "+x.Number+": "+before.Type+" cancelled", by,
				merchantToday(ctx, tx).Format("2006-01-02"), *before.Amount, debit, credit); err != nil {
				return before, out, err
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE farm_events SET status = 'cancelled', cancelled_by = $2, cancelled_at = now(), cancel_reason = $3 WHERE id::text = $1`,
		id, by, reason); err != nil {
		return before, out, err
	}
	if _, err := tx.Exec(ctx, `UPDATE farm_animals SET status = 'active', exit_on = NULL, exit_event_id = NULL WHERE exit_event_id::text = $1`, id); err != nil {
		return before, out, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM farm_milk_records WHERE event_id::text = $1`, id); err != nil {
		return before, out, err
	}
	err := scanEvent(tx.QueryRow(ctx, eventSelect+` WHERE e.id::text = $1`, id), &out)
	return before, out, err
}

// CancelEvent: POST /farm/events/{id}/cancel {reason} (farm.manage) — its
// stock and entry go back; the placement can't be (close the batch).
func (h *Handler) CancelEvent(w http.ResponseWriter, r *http.Request) {
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
	var out event
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, after, err := cancelEvent(ctx, tx, id, strings.TrimSpace(in.Reason), claims.UserID, false)
		if err != nil {
			return err
		}
		out = after
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_events", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Before: before, After: out, Reason: "cancelled: " + strings.TrimSpace(in.Reason)})
	})
	if err != nil {
		writeErr(w, err, "event")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// ListEvents: GET /farm/events?branch_id=&from=&to=&type=&batch_id=&animal_id= —
// the farm's log across its batches (farm.record). type may list several
// (medicine,vaccination).
func (h *Handler) ListEvents(w http.ResponseWriter, r *http.Request) {
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
	types := []string{}
	for _, t := range strings.Split(q.Get("type"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			types = append(types, t)
		}
	}
	var out []event
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = listEvents(ctx, tx, `($1 = '' OR e.branch_id::text = $1) AND ($2 = '' OR e.event_date >= $2::date) AND ($3 = '' OR e.event_date <= $3::date)
			AND (COALESCE(cardinality($4::text[]), 0) = 0 OR e.event_type = ANY($4)) AND ($5 = '' OR e.batch_id::text = $5) AND ($6 = '' OR e.animal_id::text = $6)`,
			q.Get("branch_id"), date(q.Get("from")), date(q.Get("to")), types, q.Get("batch_id"), q.Get("animal_id"))
		return err
	})
	if err != nil {
		writeErr(w, err, "events")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"events": out})
}
