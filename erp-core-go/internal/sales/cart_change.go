package sales

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5"
)

// CartChangeHook re-plans a cart after it changed, inside the same
// transaction. internal/promotions' Reapply is the one implementation
// (wired in internal/httpserver; promotions imports this package, so the
// call can't go the other way directly).
type CartChangeHook func(ctx context.Context, tx pgx.Tx, orderID, userID string) error

// runCartHook runs OnCartChange in a savepoint: a promotion that fails to
// evaluate must never cost the cashier the scan, so its error is logged
// and rolled back to the savepoint, and the line change itself commits.
func (h *Handler) runCartHook(ctx context.Context, tx pgx.Tx, orderID, userID string) error {
	if h.OnCartChange == nil {
		return nil
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	if err := h.OnCartChange(ctx, sp, orderID, userID); err != nil {
		log.Printf("sales: re-applying promotions on order %s: %v", orderID, err)
		return sp.Rollback(ctx)
	}
	return sp.Commit(ctx)
}

// refreshPromotions is runCartHook for the line endpoints, which answer
// with the order: resp is reloaded so the response shows the promotions
// the change brought in (or took away). DiscountsCleared is left as set.
func (h *Handler) refreshPromotions(ctx context.Context, tx pgx.Tx, orderID, userID string, resp *orderResponse) error {
	if h.OnCartChange == nil {
		return nil
	}
	if err := h.runCartHook(ctx, tx, orderID, userID); err != nil {
		return err
	}
	return loadOrder(ctx, tx, orderID, resp)
}

// discountsNeedReapplying: the layers a line edit is about to clear
// include one the cashier applied by hand (manual, coupon, loyalty), so
// the till should say so. Promotions alone don't count when the hook is
// wired — refreshPromotions puts them straight back.
func (h *Handler) discountsNeedReapplying(ctx context.Context, tx pgx.Tx, orderID string) (bool, error) {
	if h.OnCartChange == nil {
		return true, nil
	}
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM sales_order_discounts WHERE sales_order_id = $1 AND type <> 'promotion'`, orderID).Scan(&n)
	return n > 0, err
}
