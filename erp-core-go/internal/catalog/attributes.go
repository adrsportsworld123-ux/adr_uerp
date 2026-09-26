// Phase 8: Vertical Expansion. attributes/attribute_values have existed
// in the schema since migrations/001_schema.sql specifically for this —
// its own comment on product_variants.attribute_combo ({"Size":"M",
// "Color":"Red"}) shows the intent — but no Go code has ever managed
// them, and CreateProduct (products_write.go) has accepted any
// attribute_combo key/value with zero validation since Phase 1. This
// file closes both gaps: CRUD for the attribute master list and its
// controlled value options, CRUD for which attributes apply to a given
// category (the actual "attribute-set configuration" a new vertical
// needs — jewelry's Purity/Weight, electronics' RAM/Storage, sports'
// Material, etc., see migrations/027_vertical_expansion.sql), and
// validateAttributeCombo, which products_write.go's CreateProduct now
// calls per variant.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// ---------------------------------------------------------------------
// Attributes (the master list): GET/POST /attributes — gated by
// catalog.manage on write, same tier as categories/brands/tax-slabs.
// ---------------------------------------------------------------------

type attributeValueResponse struct {
	ValueID   string `json:"value_id"`
	Value     string `json:"value"`
	SortOrder int    `json:"sort_order"`
}

type attributeResponse struct {
	AttributeID string                   `json:"attribute_id"`
	Name        string                   `json:"name"`
	InputType   string                   `json:"input_type"` // "select" | "text" | "number"
	Values      []attributeValueResponse `json:"values"`     // only meaningful for input_type "select"
}

type createAttributeRequest struct {
	Name      string `json:"name"`
	InputType string `json:"input_type"`
}

func (h *TaxonomyHandler) ListAttributes(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	list := []attributeResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, input_type FROM attributes ORDER BY name`)
		if err != nil {
			return err
		}
		var attrs []attributeResponse
		for rows.Next() {
			var a attributeResponse
			if err := rows.Scan(&a.AttributeID, &a.Name, &a.InputType); err != nil {
				rows.Close()
				return err
			}
			a.Values = []attributeValueResponse{}
			attrs = append(attrs, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for i := range attrs {
			valRows, err := tx.Query(ctx, `
				SELECT id, value, sort_order FROM attribute_values
				WHERE attribute_id = $1 ORDER BY sort_order, value`, attrs[i].AttributeID)
			if err != nil {
				return err
			}
			for valRows.Next() {
				var v attributeValueResponse
				if err := valRows.Scan(&v.ValueID, &v.Value, &v.SortOrder); err != nil {
					valRows.Close()
					return err
				}
				attrs[i].Values = append(attrs[i].Values, v)
			}
			valRows.Close()
			if err := valRows.Err(); err != nil {
				return err
			}
		}
		list = attrs
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list attributes")
		return
	}
	if list == nil {
		list = []attributeResponse{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"attributes": list})
}

func (h *TaxonomyHandler) CreateAttribute(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createAttributeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}
	if req.InputType == "" {
		req.InputType = "select"
	}
	if req.InputType != "select" && req.InputType != "text" && req.InputType != "number" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "input_type must be select, text, or number")
		return
	}

	var resp attributeResponse
	resp.Name = req.Name
	resp.InputType = req.InputType
	resp.Values = []attributeValueResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO attributes (id, merchant_id, name, input_type)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2)
			RETURNING id`, req.Name, req.InputType,
		).Scan(&resp.AttributeID)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create attribute")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ---------------------------------------------------------------------
// Attribute values (the controlled option list for a "select"-type
// attribute): POST/DELETE /attributes/{id}/values — catalog.manage.
// ---------------------------------------------------------------------

type createAttributeValueRequest struct {
	Value     string `json:"value"`
	SortOrder int    `json:"sort_order"`
}

func (h *TaxonomyHandler) CreateAttributeValue(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	attributeID := chi.URLParam(r, "id")
	var req createAttributeValueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Value == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "value is required")
		return
	}

	var resp attributeValueResponse
	resp.Value = req.Value
	resp.SortOrder = req.SortOrder
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var attrExists string
		if err := tx.QueryRow(ctx, `SELECT id FROM attributes WHERE id = $1`, attributeID).Scan(&attrExists); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO attribute_values (id, attribute_id, value, sort_order)
			VALUES (gen_random_uuid(), $1, $2, $3)
			RETURNING id`, attributeID, req.Value, req.SortOrder,
		).Scan(&resp.ValueID)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "attribute not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not add attribute value")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

func (h *TaxonomyHandler) DeleteAttributeValue(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	attributeID := chi.URLParam(r, "id")
	valueID := chi.URLParam(r, "value_id")

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM attribute_values WHERE id = $1 AND attribute_id = $2`, valueID, attributeID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "attribute value not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not remove attribute value")
	default:
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// ---------------------------------------------------------------------
// Category attribute sets — the actual "which attributes does this
// vertical/category need" configuration:
// GET/POST/DELETE /categories/{id}/attributes — catalog.manage on write.
// ---------------------------------------------------------------------

type categoryAttributeResponse struct {
	AttributeID string                   `json:"attribute_id"`
	Name        string                   `json:"name"`
	InputType   string                   `json:"input_type"`
	Required    bool                     `json:"required"`
	Unit        string                   `json:"unit"`
	SortOrder   int                      `json:"sort_order"`
	Values      []attributeValueResponse `json:"values"`
}

func (h *TaxonomyHandler) ListCategoryAttributes(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	categoryID := chi.URLParam(r, "id")

	list := []categoryAttributeResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT a.id, a.name, a.input_type, ca.required, COALESCE(ca.unit,''), ca.sort_order
			FROM category_attributes ca
			JOIN attributes a ON a.id = ca.attribute_id
			WHERE ca.category_id = $1
			ORDER BY ca.sort_order, a.name`, categoryID)
		if err != nil {
			return err
		}
		var attrs []categoryAttributeResponse
		for rows.Next() {
			var a categoryAttributeResponse
			if err := rows.Scan(&a.AttributeID, &a.Name, &a.InputType, &a.Required, &a.Unit, &a.SortOrder); err != nil {
				rows.Close()
				return err
			}
			a.Values = []attributeValueResponse{}
			attrs = append(attrs, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for i := range attrs {
			valRows, err := tx.Query(ctx, `
				SELECT id, value, sort_order FROM attribute_values
				WHERE attribute_id = $1 ORDER BY sort_order, value`, attrs[i].AttributeID)
			if err != nil {
				return err
			}
			for valRows.Next() {
				var v attributeValueResponse
				if err := valRows.Scan(&v.ValueID, &v.Value, &v.SortOrder); err != nil {
					valRows.Close()
					return err
				}
				attrs[i].Values = append(attrs[i].Values, v)
			}
			valRows.Close()
			if err := valRows.Err(); err != nil {
				return err
			}
		}
		list = attrs
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list category attributes")
		return
	}
	if list == nil {
		list = []categoryAttributeResponse{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"category_id": categoryID, "attributes": list})
}

type setCategoryAttributeRequest struct {
	AttributeID string `json:"attribute_id"`
	Required    bool   `json:"required"`
	Unit        string `json:"unit"`
	SortOrder   int    `json:"sort_order"`
}

// SetCategoryAttribute: POST /categories/{id}/attributes — upsert (add,
// or edit required/unit/sort_order for an attribute already in this
// category's set).
func (h *TaxonomyHandler) SetCategoryAttribute(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	categoryID := chi.URLParam(r, "id")
	var req setCategoryAttributeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AttributeID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "attribute_id is required")
		return
	}

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var categoryExists, attributeExists string
		if err := tx.QueryRow(ctx, `SELECT id FROM categories WHERE id = $1`, categoryID).Scan(&categoryExists); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM attributes WHERE id = $1`, req.AttributeID).Scan(&attributeExists); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO category_attributes (category_id, attribute_id, required, unit, sort_order)
			VALUES ($1, $2, $3, NULLIF($4,''), $5)
			ON CONFLICT (category_id, attribute_id) DO UPDATE SET
				required = EXCLUDED.required, unit = EXCLUDED.unit, sort_order = EXCLUDED.sort_order`,
			categoryID, req.AttributeID, req.Required, req.Unit, req.SortOrder)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "category or attribute not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not set category attribute")
	default:
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func (h *TaxonomyHandler) DeleteCategoryAttribute(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	categoryID := chi.URLParam(r, "id")
	attributeID := chi.URLParam(r, "attribute_id")

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM category_attributes WHERE category_id = $1 AND attribute_id = $2`, categoryID, attributeID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "this attribute is not in the category's attribute set")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not remove category attribute")
	default:
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// ---------------------------------------------------------------------
// validateAttributeCombo — the actual enforcement point. Called by
// CreateProduct (products_write.go) once per variant, inside the same
// transaction, before that variant's INSERT. A product with no category
// has no attribute set to validate against, so this is a no-op for it —
// permissive by default, exactly like today's behavior, until a category
// actually declares a set.
// ---------------------------------------------------------------------

var errAttributeRequired = errors.New("a required attribute is missing")
var errAttributeInvalidValue = errors.New("an attribute's value is invalid for its type or controlled value list")

func validateAttributeCombo(ctx context.Context, tx pgx.Tx, categoryID *string, combo map[string]any) error {
	if categoryID == nil {
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT a.id, a.name, a.input_type, ca.required
		FROM category_attributes ca
		JOIN attributes a ON a.id = ca.attribute_id
		WHERE ca.category_id = $1`, *categoryID)
	if err != nil {
		return err
	}
	type categoryAttr struct {
		id, name, inputType string
		required            bool
	}
	var attrs []categoryAttr
	for rows.Next() {
		var a categoryAttr
		if err := rows.Scan(&a.id, &a.name, &a.inputType, &a.required); err != nil {
			rows.Close()
			return err
		}
		attrs = append(attrs, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(attrs) == 0 {
		return nil
	}

	for _, a := range attrs {
		raw, present := combo[a.name]
		if !present || raw == nil || raw == "" {
			if a.required {
				return fmt.Errorf("%w: %q", errAttributeRequired, a.name)
			}
			continue
		}
		switch a.inputType {
		case "number":
			switch v := raw.(type) {
			case float64:
				// already numeric (JSON number) — fine
			case string:
				if _, err := strconv.ParseFloat(v, 64); err != nil {
					return fmt.Errorf("%w: %q must be a number", errAttributeInvalidValue, a.name)
				}
			default:
				return fmt.Errorf("%w: %q must be a number", errAttributeInvalidValue, a.name)
			}
		case "select":
			strVal, ok := raw.(string)
			if !ok {
				return fmt.Errorf("%w: %q is not one of its allowed values", errAttributeInvalidValue, a.name)
			}
			var exists string
			err := tx.QueryRow(ctx, `SELECT id FROM attribute_values WHERE attribute_id = $1 AND value = $2`, a.id, strVal).Scan(&exists)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %q is not one of its allowed values", errAttributeInvalidValue, a.name)
			}
			if err != nil {
				return err
			}
		case "text":
			// any string is fine, no controlled list to check against
		}
	}
	return nil
}
