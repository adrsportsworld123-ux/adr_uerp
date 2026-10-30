// Package farm is farm management (§3.81; migrations/078): sheds and
// ponds, flocks, herds and pond stockings (batches), the daily log
// (mortality, feed, medicine, vaccination, weights, eggs, milk, harvests),
// vaccination schedules and each batch's performance. One model for every
// species — chicken, goat, rabbit, cow, pig, fish — proven on chicken.
//
// Only a branch with a farm business type (migrations/077) holds a farm,
// and a batch only of a species that branch farms. Feed, medicine and the
// animals placed come out of the branch's stock at cost (Dr 1500 Livestock
// & Farm WIP, Cr 1200 Inventory); what the farm produces goes into stock
// (Dr 1200, Cr 1500); closing a batch writes off what is left in 1500 for
// it (5200 Farm Losses, or 4100 Farm Gains).
package farm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/inventory"
	"erp-core-go/internal/locale"
)

type Handler struct {
	DB *db.DB
}

// Accounts (migrations/078).
const (
	AccountFarmWIP    = "1500"
	AccountFarmGains  = "4100"
	AccountFarmLosses = "5200"
)

// Species and the purposes a batch of each can be for.
var Purposes = map[string][]string{
	"chicken": {"broiler", "layer", "breeder", "country"},
	"goat":    {"meat", "breeding", "dairy"},
	"rabbit":  {"meat", "breeding"},
	"cow":     {"dairy", "breeding", "calf_rearing"},
	"pig":     {"fattening", "breeding"},
	"fish":    {"grow_out", "nursery"},
}

var houseKinds = map[string]bool{"shed": true, "pen": true, "cage": true, "paddock": true, "pond": true, "tank": true, "other": true}

// farmErr is a refusal with its HTTP status and error code (and, for
// some, details such as a withdrawal's dates).
type farmErr struct {
	status    int
	code, msg string
	details   any
}

func (e *farmErr) Error() string { return e.msg }

func invalid(format string, a ...any) error {
	return &farmErr{status: http.StatusBadRequest, code: "INVALID_REQUEST", msg: fmt.Sprintf(format, a...)}
}

func conflict(code, format string, a ...any) error {
	return &farmErr{status: http.StatusConflict, code: code, msg: fmt.Sprintf(format, a...)}
}

func forbidden(format string, a ...any) error {
	return &farmErr{status: http.StatusForbidden, code: "FORBIDDEN", msg: fmt.Sprintf(format, a...)}
}

var errStockShort = errors.New("not enough stock")

func writeErr(w http.ResponseWriter, err error, what string) {
	var fe *farmErr
	switch {
	case errors.As(err, &fe) && fe.details != nil:
		httpx.ErrorWithDetails(w, fe.status, fe.code, fe.msg, fe.details)
	case errors.As(err, &fe):
		httpx.Error(w, fe.status, fe.code, fe.msg)
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", what+" not found")
	case errors.Is(err, accounting.ErrPeriodLocked):
		httpx.Error(w, http.StatusConflict, "PERIOD_LOCKED", "that date is in a locked accounting period")
	case errors.Is(err, inventory.ErrBatchInsufficient):
		httpx.Error(w, http.StatusConflict, "FARM_STOCK_SHORT", "that stock batch doesn't hold enough")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the "+what)
	}
}

// farmBranch checks a branch is visible to the caller and farms species
// (any farm when species is ""); it returns the branch's name.
func farmBranch(ctx context.Context, tx pgx.Tx, branchID, species string) (string, error) {
	var name string
	var farms bool
	var farmed []string
	err := tx.QueryRow(ctx, `
		SELECT b.name, 'farm' = ANY(app_branch_features(b.id)),
		       COALESCE((SELECT array_agg(t.species) FROM branch_business_types bt JOIN business_types t ON t.code = bt.type_code
		                 WHERE bt.branch_id = b.id AND t.species IS NOT NULL), '{}')
		FROM branches b WHERE b.id::text = $1 AND app_branch_visible(b.id)`, branchID).Scan(&name, &farms, &farmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", &farmErr{status: http.StatusNotFound, code: "NOT_FOUND", msg: "branch not found"}
	} else if err != nil {
		return "", err
	}
	if !farms {
		return name, conflict("BRANCH_NOT_FARM", "%s isn't set up as a farm; add a farm business type to it (Branches)", name)
	}
	if species != "" {
		for _, s := range farmed {
			if s == species {
				return name, nil
			}
		}
		return name, conflict("SPECIES_NOT_ON_BRANCH", "%s doesn't farm %s; add that farm type to the branch first", name, species)
	}
	return name, nil
}

func validSpecies(s string) bool { _, ok := Purposes[s]; return ok }

func validPurpose(species, purpose string) bool {
	for _, p := range Purposes[species] {
		if p == purpose {
			return true
		}
	}
	return false
}

// merchantToday is today on the merchant's clock.
func merchantToday(ctx context.Context, tx pgx.Tx) time.Time {
	now := time.Now().In(locale.Location(ctx, tx))
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func parseDate(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	return t, err == nil
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// nextBatchNumber: FB-00001, one counter per merchant.
func nextBatchNumber(ctx context.Context, tx pgx.Tx) (string, error) {
	var n int
	err := tx.QueryRow(ctx, `
		INSERT INTO farm_counters (merchant_id, last_no) VALUES (current_setting('app.tenant_id')::uuid, 1)
		ON CONFLICT (merchant_id) DO UPDATE SET last_no = farm_counters.last_no + 1 RETURNING last_no`).Scan(&n)
	return fmt.Sprintf("FB-%05d", n), err
}

// moveStock takes delta of a variant into (delta > 0) or out of (< 0) a
// branch's stock for a farm event (refType "farm_event") or a feed mix
// ("farm_feed_mix"), keeping batches in step for a batch-tracked item, and
// returns the cost per unit it moved at. Out: at the item's current cost
// (WAC), never below what is free. In: at unitCost (0 = the item's
// current cost), blended into its WAC.
func moveStock(ctx context.Context, tx pgx.Tx, branchID, variantID string, delta, unitCost float64, refType, refID, reason, by, batchLabel string) (float64, error) {
	var cost float64
	var tracks bool
	var name string
	if err := tx.QueryRow(ctx, `SELECT v.cost_price::float8, v.track_batch, p.name FROM product_variants v JOIN products p ON p.id = v.product_id WHERE v.id::text = $1`,
		variantID).Scan(&cost, &tracks, &name); errors.Is(err, pgx.ErrNoRows) {
		return 0, invalid("choose an item from stock")
	} else if err != nil {
		return 0, err
	}
	var onHand, reserved float64
	err := tx.QueryRow(ctx, `SELECT on_hand::float8, reserved::float8 FROM stock_levels WHERE branch_id::text = $1 AND variant_id::text = $2 FOR UPDATE`,
		branchID, variantID).Scan(&onHand, &reserved)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	movement := "farm_use"
	if delta < 0 {
		if onHand-reserved < -delta-1e-9 {
			return 0, conflict("FARM_STOCK_SHORT", "only %s of %s is free in this branch's stock", trimNum(math.Max(onHand-reserved, 0)), name)
		}
		unitCost = cost
	} else {
		movement = "farm_output"
		if unitCost <= 0 {
			unitCost = cost
		}
		// Weighted average with what is already in stock (as a GRN does).
		if total := math.Max(onHand, 0) + delta; total > 0 && unitCost != cost {
			newCost := round2((math.Max(onHand, 0)*cost + delta*unitCost) / total)
			if _, err := tx.Exec(ctx, `UPDATE product_variants SET cost_price = $2, updated_at = now() WHERE id::text = $1`, variantID, newCost); err != nil {
				return 0, err
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO stock_levels (id, merchant_id, branch_id, variant_id, on_hand, reserved, version)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3, 0, 0)
		ON CONFLICT (branch_id, variant_id) DO UPDATE SET on_hand = stock_levels.on_hand + EXCLUDED.on_hand,
		  version = stock_levels.version + 1, updated_at = now()`, branchID, variantID, delta); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO stock_movements (merchant_id, branch_id, variant_id, movement_type, quantity_delta, reference_type, reference_id, reason, performed_by)
		VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7, $8::uuid)`,
		branchID, variantID, movement, delta, refType, refID, reason, by); err != nil {
		return 0, err
	}
	if tracks {
		if delta < 0 {
			if _, err := inventory.DrainBatches(ctx, tx, branchID, variantID, -delta, "", inventory.DrainSellableFirst, refType, refID, by); err != nil {
				return 0, err
			}
		} else if err := inventory.ReceiveBatchWithMovement(ctx, tx, branchID, variantID, batchLabel, nil, unitCost, delta, refType, refID, by); err != nil {
			return 0, err
		}
	}
	return unitCost, nil
}

func trimNum(v float64) string {
	s := fmt.Sprintf("%.3f", v)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// post books a farm journal entry and returns its id ("" when value is 0).
func post(ctx context.Context, tx pgx.Tx, branchID, source, sourceID, desc, by, date string, value float64, debit, credit string) (string, error) {
	value = round2(value)
	if value == 0 {
		return "", nil
	}
	if value < 0 {
		debit, credit, value = credit, debit, -value
	}
	num, err := accounting.PostJournalEntryOnDate(ctx, tx, branchID, source, sourceID, desc, by, date, []accounting.JournalLine{
		{AccountCode: debit, Debit: value}, {AccountCode: credit, Credit: value},
	})
	if err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, `SELECT id::text FROM journal_entries WHERE entry_number = $1`, num).Scan(&id)
	return id, err
}
