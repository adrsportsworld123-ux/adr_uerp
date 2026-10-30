package farm

// Sheds, pens, cages, paddocks, ponds and tanks (farm_houses): where a
// batch is kept, with its species and capacity.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type house struct {
	ID          string   `json:"house_id"`
	BranchID    string   `json:"branch_id"`
	Branch      string   `json:"branch"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Species     string   `json:"species"`
	Capacity    *int     `json:"capacity"`
	AreaSqft    *float64 `json:"area_sqft"`
	Notes       *string  `json:"notes"`
	Status      string   `json:"status"`
	Occupied    int      `json:"occupied"`     // animals in its active batches
	ActiveBatch *string  `json:"active_batch"` // the batch number in it, if one
}

const houseSelect = `
	SELECT h.id::text, h.branch_id::text, b.name, h.name, h.kind, h.species, h.capacity, h.area_sqft::float8, h.notes, h.status,
	       COALESCE((SELECT sum(` + aliveSQL + `) FROM farm_batches fb WHERE fb.house_id = h.id AND fb.status = 'active'), 0)::int,
	       (SELECT fb.batch_number FROM farm_batches fb WHERE fb.house_id = h.id AND fb.status = 'active' ORDER BY fb.placed_on DESC LIMIT 1)
	FROM farm_houses h JOIN branches b ON b.id = h.branch_id`

func scanHouse(row pgx.Row, h *house) error {
	return row.Scan(&h.ID, &h.BranchID, &h.Branch, &h.Name, &h.Kind, &h.Species, &h.Capacity, &h.AreaSqft, &h.Notes, &h.Status, &h.Occupied, &h.ActiveBatch)
}

// ListHouses: GET /farm/houses?branch_id=&status= (farm.record)
func (h *Handler) ListHouses(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	out := []house{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, houseSelect+` WHERE ($1 = '' OR h.branch_id::text = $1) AND ($2 = '' OR h.status = $2) ORDER BY b.name, h.name`,
			q.Get("branch_id"), q.Get("status"))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x house
			if err := scanHouse(rows, &x); err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	if err != nil {
		writeErr(w, err, "sheds")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"houses": out})
}

type houseInput struct {
	BranchID string   `json:"branch_id"`
	Name     *string  `json:"name"`
	Kind     *string  `json:"kind"`
	Species  *string  `json:"species"`
	Capacity *int     `json:"capacity"`
	AreaSqft *float64 `json:"area_sqft"`
	Notes    *string  `json:"notes"`
	Status   *string  `json:"status"`
}

func (in houseInput) check() error {
	switch {
	case in.Name != nil && strings.TrimSpace(*in.Name) == "":
		return invalid("name is required")
	case in.Kind != nil && !houseKinds[*in.Kind]:
		return invalid("kind must be shed, pen, cage, paddock, pond, tank or other")
	case in.Species != nil && !validSpecies(*in.Species):
		return invalid("species must be chicken, goat, rabbit, cow, pig or fish")
	case in.Capacity != nil && *in.Capacity <= 0:
		return invalid("capacity must be more than 0")
	case in.AreaSqft != nil && *in.AreaSqft <= 0:
		return invalid("area must be more than 0")
	case in.Status != nil && *in.Status != "active" && *in.Status != "inactive":
		return invalid("status must be active or inactive")
	}
	return nil
}

// CreateHouse: POST /farm/houses {branch_id, name, kind, species, capacity?, area_sqft?, notes?} (farm.manage)
func (h *Handler) CreateHouse(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var in houseInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if in.BranchID == "" || in.Name == nil || in.Kind == nil || in.Species == nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id, name, kind and species are required")
		return
	}
	if err := in.check(); err != nil {
		writeErr(w, err, "shed")
		return
	}
	var out house
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := farmBranch(ctx, tx, in.BranchID, *in.Species); err != nil {
			return err
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO farm_houses (merchant_id, branch_id, name, kind, species, capacity, area_sqft, notes)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, trim($2), $3, $4, $5, $6, NULLIF(trim($7), '')) RETURNING id::text`,
			in.BranchID, *in.Name, *in.Kind, *in.Species, in.Capacity, in.AreaSqft, deref(in.Notes)).Scan(&id); err != nil {
			if isUnique(err) {
				return conflict("HOUSE_EXISTS", "this branch already has a shed or pond called %s", strings.TrimSpace(*in.Name))
			}
			return err
		}
		if err := scanHouse(tx.QueryRow(ctx, houseSelect+` WHERE h.id::text = $1`, id), &out); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_houses", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: out})
	})
	if err != nil {
		writeErr(w, err, "shed")
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// UpdateHouse: PATCH /farm/houses/{id} (farm.manage) — its species stays
// while a batch is in it.
func (h *Handler) UpdateHouse(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in houseInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := in.check(); err != nil {
		writeErr(w, err, "shed")
		return
	}
	var out house
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var before house
		if err := scanHouse(tx.QueryRow(ctx, houseSelect+` WHERE h.id::text = $1 FOR UPDATE OF h`, id), &before); err != nil {
			return err
		}
		if before.ActiveBatch != nil && ((in.Species != nil && *in.Species != before.Species) || (in.Status != nil && *in.Status == "inactive")) {
			return conflict("HOUSE_IN_USE", "batch %s is in it; close or move it first", *before.ActiveBatch)
		}
		if in.Species != nil && *in.Species != before.Species {
			if _, err := farmBranch(ctx, tx, before.BranchID, *in.Species); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE farm_houses SET name = COALESCE(NULLIF(trim($2), ''), name), kind = COALESCE($3, kind), species = COALESCE($4, species),
			  capacity = COALESCE($5, capacity), area_sqft = COALESCE($6, area_sqft),
			  notes = CASE WHEN $7::text IS NULL THEN notes ELSE NULLIF(trim($7), '') END, status = COALESCE($8, status)
			WHERE id::text = $1`, id, in.Name, in.Kind, in.Species, in.Capacity, in.AreaSqft, in.Notes, in.Status); err != nil {
			if isUnique(err) {
				return conflict("HOUSE_EXISTS", "this branch already has one by that name")
			}
			return err
		}
		if err := scanHouse(tx.QueryRow(ctx, houseSelect+` WHERE h.id::text = $1`, id), &out); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "farm_houses", EntityID: id, Action: "update", PerformedBy: claims.UserID, Before: before, After: out})
	})
	if err != nil {
		writeErr(w, err, "shed")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
