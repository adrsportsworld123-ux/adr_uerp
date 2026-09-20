package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type BarcodeAssignHandler struct {
	DB *db.DB
}

type assignBarcodeRequest struct {
	Code      string `json:"code"`      // optional — a real supplier barcode, if one exists
	Symbology string `json:"symbology"` // optional; default EAN13 when Code is omitted, else CODE128
}

type assignBarcodeResponse struct {
	Code      string `json:"code"`
	Symbology string `json:"symbology"`
	IsPrimary bool   `json:"is_primary"`
	Generated bool   `json:"generated"`
}

var validSymbologies = map[string]bool{
	"EAN13": true, "EAN8": true, "UPCA": true, "UPCE": true,
	"CODE128": true, "CODE39": true, "QR": true,
}

const maxGenerateAttempts = 5

// AssignBarcode: POST /products/variants/{id}/barcodes — the FRD's
// "Hybrid (Recommended): use supplier barcode if exists, else
// auto-generate" (pos_frd_complete.md §12), closing the Phase 1 gap
// phased_roadmap.md tracked as "barcode assignment" never having been
// built (only barcode *lookup*, GET /products/barcode/{code}, existed).
// Pass `code` for a real supplier barcode; omit the body/code entirely to
// auto-generate a sequential EAN-13 in GS1's reserved in-store-use range
// (prefix 20-29), which a real, globally-assigned EAN-13 can never land
// in — so a generated code is guaranteed not to collide with a genuine one.
func (h *BarcodeAssignHandler) AssignBarcode(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	variantID := chi.URLParam(r, "id")

	var req assignBarcodeRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
			return
		}
	}

	generated := req.Code == ""
	symbology := req.Symbology
	if symbology == "" {
		if generated {
			symbology = "EAN13"
		} else {
			symbology = "CODE128"
		}
	}
	if !validSymbologies[symbology] {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "unrecognized symbology")
		return
	}
	if !generated && symbology == "EAN13" {
		if err := validateEAN13(req.Code); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
	}

	var resp assignBarcodeResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var variantExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_variants WHERE id = $1)`, variantID).Scan(&variantExists); err != nil {
			return err
		}
		if !variantExists {
			return pgx.ErrNoRows
		}

		var existingCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM barcodes WHERE variant_id = $1`, variantID).Scan(&existingCount); err != nil {
			return err
		}
		isPrimary := existingCount == 0

		code := req.Code
		if generated {
			generatedCode, err := assignGeneratedCode(ctx, tx, variantID, isPrimary)
			if err != nil {
				return err
			}
			code = generatedCode
		} else if err := insertBarcode(ctx, tx, variantID, code, symbology, isPrimary); err != nil {
			return err
		}

		resp = assignBarcodeResponse{Code: code, Symbology: symbology, IsPrimary: isPrimary, Generated: generated}
		return nil
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "variant not found")
	case isUniqueViolation(err):
		httpx.Error(w, http.StatusConflict, "BARCODE_EXISTS", "this barcode is already assigned to another variant")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not assign barcode")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

func insertBarcode(ctx context.Context, tx pgx.Tx, variantID, code, symbology string, isPrimary bool) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO barcodes (id, merchant_id, variant_id, code, symbology, is_primary)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4)`,
		variantID, code, symbology, isPrimary)
	return err
}

// assignGeneratedCode tries the next sequential candidate first, falling
// back to random candidates only if that collides (a concurrent request
// generated the same sequence number) — a real, if rare, race this
// covers rather than assumes away.
func assignGeneratedCode(ctx context.Context, tx pgx.Tx, variantID string, isPrimary bool) (string, error) {
	var seqCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM barcodes WHERE symbology = 'EAN13' AND code LIKE '20%'`).Scan(&seqCount); err != nil {
		return "", err
	}

	for attempt := 0; attempt < maxGenerateAttempts; attempt++ {
		var code string
		var err error
		if attempt == 0 {
			code, err = sequentialEAN13Candidate(seqCount)
		} else {
			code, err = randomEAN13Candidate()
		}
		if err != nil {
			return "", err
		}

		savepoint := "barcode_gen"
		if _, err := tx.Exec(ctx, "SAVEPOINT "+savepoint); err != nil {
			return "", err
		}
		if err := insertBarcode(ctx, tx, variantID, code, "EAN13", isPrimary); err != nil {
			if isUniqueViolation(err) {
				if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+savepoint); rbErr != nil {
					return "", rbErr
				}
				continue
			}
			return "", err
		}
		return code, nil
	}
	return "", errors.New("could not generate a unique barcode after several attempts")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
