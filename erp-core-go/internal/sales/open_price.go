package sales

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/catalog"
	"erp-core-go/internal/httpx"
)

// Phase 13 open-price items (migrations/039_open_price.sql). A variant
// flagged open_price is sold at a price the cashier types, inside the
// variant's min/max; above its approval threshold a Branch Manager or
// Merchant Admin approves with employee code + PIN (approverFor, the same
// check returns use). Stock is untouched by any of this: the line reserves
// and allocates exactly like a fixed-price one.

// openPriceError carries the variant's range back to the POS, so the
// keypad can open with the limits without a second request.
type openPriceError struct {
	status    int
	code, msg string
	op        catalog.OpenPrice
	suggested string
}

func (e *openPriceError) Error() string { return e.msg }

func (e *openPriceError) write(w http.ResponseWriter) {
	httpx.ErrorWithDetails(w, e.status, e.code, e.msg, map[string]any{"open_price": e.op, "suggested_price": e.suggested})
}

var errPriceNotEditable = fmt.Errorf("this item has a fixed price; only open-price items take a price at the till")

// resolveOpenPrice decides the price source for an AddLine request:
//   - fixed-price variant, no unit_price   -> nil (normal pricing)
//   - fixed-price variant, unit_price sent -> errPriceNotEditable
//   - open-price variant                   -> a validated override, or an
//     *openPriceError (price missing, out of range, approval failed)
func resolveOpenPrice(ctx context.Context, tx pgx.Tx, orderID, variantID string, req addLineRequest) (*LinePriceOverride, error) {
	var enabled, includesTax bool
	var min, max, above *string
	var suggested string
	if err := tx.QueryRow(ctx, `
		SELECT `+catalog.OpenPriceColumns+`, p.price_includes_tax, pv.selling_price::text
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.id = $1`, variantID).Scan(&enabled, &min, &max, &above, &includesTax, &suggested); err != nil {
		return nil, err
	}
	op := catalog.NewOpenPrice(enabled, min, max, above)
	if op == nil {
		if req.UnitPrice != nil {
			return nil, errPriceNotEditable
		}
		return nil, nil
	}
	fail := func(status int, code, msg string) error {
		return &openPriceError{status: status, code: code, msg: msg, op: *op, suggested: suggested}
	}
	if req.UnitPrice == nil {
		return nil, fail(http.StatusUnprocessableEntity, "OPEN_PRICE_REQUIRED", "enter the price for this item ("+op.String()+")")
	}
	inRange, needsApproval := catalog.CheckOpenPrice(*op, *req.UnitPrice)
	if !inRange {
		return nil, fail(http.StatusUnprocessableEntity, "OPEN_PRICE_OUT_OF_RANGE", "the price must be within "+op.String())
	}
	override := &LinePriceOverride{UnitPrice: *req.UnitPrice, PriceIncludesTax: includesTax, OpenPrice: true}
	if needsApproval {
		var branchID string
		if err := tx.QueryRow(ctx, `SELECT branch_id FROM sales_orders WHERE id = $1`, orderID).Scan(&branchID); err != nil {
			return nil, err
		}
		approver, err := approverFor(ctx, tx, req.ApproverCode, req.ApproverPIN, branchID)
		if err != nil {
			return nil, err
		}
		if approver == "" {
			return nil, fail(http.StatusForbidden, "OPEN_PRICE_NOT_APPROVED",
				"a price above ₹"+*op.ApprovalAbove+" needs a Branch Manager or Merchant Admin's employee code and PIN")
		}
		override.ApprovedBy = approver
	}
	return override, nil
}

// auditOpenPriceLine records who entered which price, beside the catalog's
// suggested price and range, so an unusual price can be traced later.
func auditOpenPriceLine(ctx context.Context, tx pgx.Tx, orderID, variantID, cashierID string, o LinePriceOverride) error {
	var lineID, catalogPrice string
	if err := tx.QueryRow(ctx, `
		SELECT sol.id, pv.selling_price::text FROM sales_order_lines sol JOIN product_variants pv ON pv.id = sol.variant_id
		WHERE sol.sales_order_id = $1 AND sol.variant_id = $2 AND sol.open_price
		ORDER BY sol.created_at DESC LIMIT 1`, orderID, variantID).Scan(&lineID, &catalogPrice); err != nil {
		return err
	}
	after := map[string]any{
		"sales_order_id": orderID, "variant_id": variantID, "unit_price": fmt.Sprintf("%.2f", o.UnitPrice),
		"suggested_price": catalogPrice, "open_price": true,
	}
	if o.ApprovedBy != "" {
		after["approved_by"] = o.ApprovedBy
	}
	return audit.Log(ctx, tx, audit.Entry{
		EntityType: "sales_order_lines", EntityID: lineID, Action: "create", PerformedBy: cashierID,
		After: after, Reason: "open price entered at the till",
	})
}
