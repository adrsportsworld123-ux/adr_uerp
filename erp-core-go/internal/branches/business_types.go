package branches

// What each branch does (§3.80; migrations/077): business types — retail,
// wholesale, grocery, pharmacy, warehouse, or a farm of one species —
// each switching on features. The existing screens never depend on them;
// the farm ones need a farm type (farm.BranchFarms).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

var errUnknownBusinessType = errors.New("unknown business type")

// BusinessType is one entry of the platform's catalogue.
type BusinessType struct {
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Features    []string `json:"features"`
	Species     *string  `json:"species"`
}

// ListBusinessTypes: GET /business-types — every user.
func (h *Handler) ListBusinessTypes(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	out := []BusinessType{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT code, name, description, features, species FROM business_types ORDER BY sort_order`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t BusinessType
			if err := rows.Scan(&t.Code, &t.Name, &t.Description, &t.Features, &t.Species); err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list business types")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"business_types": out})
}

// setBusinessTypes replaces a branch's business types (at least one, each known).
func setBusinessTypes(ctx context.Context, tx pgx.Tx, branchID string, codes []string) error {
	seen := map[string]bool{}
	clean := []string{}
	for _, c := range codes {
		c = strings.TrimSpace(c)
		if c != "" && !seen[c] {
			seen[c] = true
			clean = append(clean, c)
		}
	}
	if len(clean) == 0 {
		return fmt.Errorf("%w: choose at least one business type", errUnknownBusinessType)
	}
	var known int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM business_types WHERE code = ANY($1)`, clean).Scan(&known); err != nil {
		return err
	}
	if known != len(clean) {
		return fmt.Errorf("%w: %s", errUnknownBusinessType, strings.Join(clean, ", "))
	}
	if _, err := tx.Exec(ctx, `DELETE FROM branch_business_types WHERE branch_id::text = $1 AND type_code <> ALL($2)`, branchID, clean); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO branch_business_types (merchant_id, branch_id, type_code)
		SELECT current_setting('app.tenant_id')::uuid, $1::uuid, unnest($2::text[])
		ON CONFLICT DO NOTHING`, branchID, clean)
	return err
}

func branchTypes(ctx context.Context, tx pgx.Tx, branchID string) ([]string, error) {
	var out []string
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(array_agg(bt.type_code ORDER BY t.sort_order), '{}') FROM branch_business_types bt
		JOIN business_types t ON t.code = bt.type_code WHERE bt.branch_id::text = $1`, branchID).Scan(&out)
	return out, err
}

// SetBranchBusinessTypes: PUT /branches/{id}/business-types {business_types: [...]}
// (branches.configure). Taking a farm type away from a branch with an
// active flock or herd is refused.
func (h *Handler) SetBranchBusinessTypes(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := chi.URLParam(r, "id")
	var req struct {
		BusinessTypes []string `json:"business_types"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var after []string
	var stillFarming string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM branches WHERE id::text = $1 AND app_branch_visible(id))`, branchID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		before, err := branchTypes(ctx, tx, branchID)
		if err != nil {
			return err
		}
		if err := setBusinessTypes(ctx, tx, branchID, req.BusinessTypes); err != nil {
			return err
		}
		// An active batch keeps its species' farm type on the branch.
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE((SELECT string_agg(DISTINCT fb.species, ', ') FROM farm_batches fb
			  WHERE fb.branch_id::text = $1 AND fb.status = 'active'
			    AND NOT EXISTS (SELECT 1 FROM branch_business_types bt JOIN business_types t ON t.code = bt.type_code
			                    WHERE bt.branch_id = fb.branch_id AND t.species = fb.species)), '')`, branchID).Scan(&stillFarming); err != nil {
			return err
		}
		if stillFarming != "" {
			return errStillFarming
		}
		if after, err = branchTypes(ctx, tx, branchID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "branch_business_types", EntityID: branchID, Action: "update", PerformedBy: claims.UserID,
			Before: before, After: after})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
	case errors.Is(err, errUnknownBusinessType):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case errors.Is(err, errStillFarming):
		httpx.Error(w, http.StatusConflict, "BRANCH_HAS_ACTIVE_BATCHES", "this branch still has active "+stillFarming+" batches; close them before removing that farm type")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the business types")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"branch_id": branchID, "business_types": after})
	}
}

var errStillFarming = errors.New("active batches")

type branchFeatures struct {
	BranchID      string   `json:"branch_id"`
	Name          string   `json:"name"`
	BusinessTypes []string `json:"business_types"`
	Features      []string `json:"features"`
	Species       []string `json:"species"` // what the branch farms
}

// MyFeatures: GET /me/features — what the caller's branches do: the
// features across them (the web shows the farm screens only when one is
// a farm) and each branch's own.
func (h *Handler) MyFeatures(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	out := []branchFeatures{}
	all := map[string]bool{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT b.id::text, b.name,
			       COALESCE(array_agg(DISTINCT bt.type_code) FILTER (WHERE bt.type_code IS NOT NULL), '{}'),
			       COALESCE((SELECT array_agg(DISTINCT f) FROM branch_business_types x JOIN business_types t ON t.code = x.type_code, unnest(t.features) f
			                 WHERE x.branch_id = b.id), '{}'),
			       COALESCE(array_agg(DISTINCT t.species) FILTER (WHERE t.species IS NOT NULL), '{}')
			FROM branches b
			LEFT JOIN branch_business_types bt ON bt.branch_id = b.id
			LEFT JOIN business_types t ON t.code = bt.type_code
			WHERE b.status = 'active' AND app_branch_visible(b.id)
			GROUP BY b.id, b.name ORDER BY b.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var f branchFeatures
			if err := rows.Scan(&f.BranchID, &f.Name, &f.BusinessTypes, &f.Features, &f.Species); err != nil {
				return err
			}
			for _, x := range f.Features {
				all[x] = true
			}
			out = append(out, f)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the branches' features")
		return
	}
	features := []string{}
	for f := range all {
		features = append(features, f)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"features": features, "branches": out})
}
