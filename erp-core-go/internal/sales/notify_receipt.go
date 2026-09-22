package sales

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// Notifier is the subset of internal/notifications.Handler this package
// needs — a concrete type (not an interface guarding against an import
// cycle, unlike LoyaltyEarner) would work here too, since
// internal/notifications never imports internal/sales. Kept as an
// interface anyway for the same reason internal/inventory/internal/purchase
// don't need one: this package only calls two methods, and naming them
// here documents the real dependency surface without pulling in
// notifications' own Provider/config types.
type Notifier interface {
	DispatchEmail(ctx context.Context, tx pgx.Tx, category, recipient, subject, body, referenceType, referenceID string)
	DispatchPhone(ctx context.Context, tx pgx.Tx, category, recipient, body, referenceType, referenceID string)
}

// notifyReceipt sends a receipt notification to whichever contact info
// the order's customer has on file — email if present, a phone-channel
// message if present, both if both are present, neither if the order is
// a walk-in with no customer attached. Best-effort: Notifier's own
// Dispatch* methods already never return an error (see
// internal/notifications/handlers.go's doc comment), and a failure to
// even load the receipt data here is swallowed rather than propagated —
// called from inside Checkout's transaction, so this must never be able
// to fail a sale that has already otherwise succeeded.
func notifyReceipt(ctx context.Context, tx pgx.Tx, notify Notifier, orderID string) {
	if notify == nil {
		return
	}
	resp, err := loadReceiptDataTx(ctx, tx, orderID)
	if err != nil {
		return
	}
	if resp.CustomerEmail != nil && *resp.CustomerEmail != "" {
		notify.DispatchEmail(ctx, tx, "receipt", *resp.CustomerEmail,
			fmt.Sprintf("Receipt for order %s", resp.OrderNumber), receiptEmailBody(resp), "sales_order", orderID)
	}
	if resp.CustomerPhone != nil && *resp.CustomerPhone != "" {
		notify.DispatchPhone(ctx, tx, "receipt", *resp.CustomerPhone, receiptSMSBody(resp), "sales_order", orderID)
	}
}

func receiptEmailBody(r receiptResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", r.MerchantName)
	fmt.Fprintf(&b, "%s\n\n", r.BranchName)
	fmt.Fprintf(&b, "Order: %s\n", r.OrderNumber)
	if r.FinalizedAt != nil {
		fmt.Fprintf(&b, "Date: %s\n", *r.FinalizedAt)
	}
	b.WriteString("\n")
	for _, l := range r.Lines {
		fmt.Fprintf(&b, "%s x%s @ Rs.%s = Rs.%s\n", l.ProductName, l.Quantity, l.UnitPrice, l.LineTotal)
	}
	fmt.Fprintf(&b, "\nSubtotal: Rs.%s\n", r.Subtotal)
	if r.DiscountTotal != "0.00" && r.DiscountTotal != "" {
		fmt.Fprintf(&b, "Discount: -Rs.%s\n", r.DiscountTotal)
	}
	fmt.Fprintf(&b, "Tax: Rs.%s\n", r.TaxTotal)
	fmt.Fprintf(&b, "Total: Rs.%s\n", r.GrandTotal)
	for _, p := range r.Payments {
		fmt.Fprintf(&b, "Paid (%s): Rs.%s\n", p.Method, p.Amount)
	}
	b.WriteString("\nThank you for shopping with us!")
	return b.String()
}

func receiptSMSBody(r receiptResponse) string {
	return fmt.Sprintf("Thanks for shopping at %s! Order %s: Rs.%s. See you again soon.", r.MerchantName, r.OrderNumber, r.GrandTotal)
}

// ---------------------------------------------------------------------
// POST /sales/orders/{id}/receipt/notify — resend the receipt
// notification on demand (a cashier fixing a customer's forgotten
// contact info after checkout, or simply re-sending on request). Open to
// any authenticated user, same as GetReceipt — not gated, since it can
// only ever re-send this order's own already-finalized receipt, not
// change anything.
// ---------------------------------------------------------------------

func (h *Handler) NotifyReceipt(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var sent bool
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM sales_orders WHERE id = $1`, orderID).Scan(&status); err != nil {
			return err
		}
		if status != "finalized" {
			return errOrderNotFinalized
		}
		resp, err := loadReceiptDataTx(ctx, tx, orderID)
		if err != nil {
			return err
		}
		sent = (resp.CustomerEmail != nil && *resp.CustomerEmail != "") || (resp.CustomerPhone != nil && *resp.CustomerPhone != "")
		notifyReceipt(ctx, tx, h.Notify, orderID)
		return nil
	})

	switch {
	case errors.Is(err, errOrderNotFinalized):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "receipts can only be sent for a finalized order")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not send receipt notification")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"sent": sent})
	}
}

var errOrderNotFinalized = errors.New("order is not finalized")
