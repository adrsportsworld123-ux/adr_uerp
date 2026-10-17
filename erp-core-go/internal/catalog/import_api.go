package catalog

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// SimpleProduct is a one-variant product as the purchase invoice importer
// (internal/purchase/import.go) creates it from an invoice line.
type SimpleProduct struct {
	Name         string
	HSNCode      string
	CategoryID   string // "" = none
	BrandID      string // "" = none
	TaxSlabID    string // "" = none
	SKU          string
	MRP          float64
	SellingPrice float64
	CostPrice    float64
	Barcode      string // the barcode printed on the item, "" = none
	// GenerateBarcode gives the variant this system's own barcode when the
	// item carries none, so it can be scanned at the till straight away.
	GenerateBarcode bool
}

// CreateSimpleProduct creates the product and its single variant inside
// the caller's tenant transaction, through the same path as POST /products.
func CreateSimpleProduct(ctx context.Context, tx pgx.Tx, p SimpleProduct) (productID, variantID string, err error) {
	resp, err := createProductTx(ctx, tx, createProductRequest{
		Name: p.Name, HSNCode: p.HSNCode, CategoryID: p.CategoryID, BrandID: p.BrandID, TaxSlabID: p.TaxSlabID,
		ProductType: "simple",
		Variants: []createVariantInput{{
			SKU: p.SKU, MRP: p.MRP, SellingPrice: p.SellingPrice, CostPrice: p.CostPrice,
			OriginalBarcode: p.Barcode, GenerateBarcode: p.Barcode == "" && p.GenerateBarcode,
		}},
	})
	if err != nil {
		return "", "", err
	}
	return resp.ProductID, resp.Variants[0].VariantID, nil
}

// ProductErrorMessage turns a CreateSimpleProduct failure into the same
// wording POST /products answers with, or "" when it isn't one of those.
func ProductErrorMessage(err error) string {
	switch {
	case uniqueViolationConstraint(err) == "barcodes_merchant_id_code_key":
		return "this barcode is already assigned to another product"
	case isUniqueViolation(err):
		return "this SKU is already in use"
	case errors.Is(err, errInvalidBarcode), errors.Is(err, errInvalidReference), errors.Is(err, errInactiveReference),
		errors.Is(err, errAttributeRequired), errors.Is(err, errAttributeInvalidValue):
		return err.Error()
	}
	return ""
}
