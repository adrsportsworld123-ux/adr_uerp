// Package audit implements Phase 4's "immutable audit trail" item — see
// migrations/021_audit_trail_hardening.sql's header comment for the full
// account of what was actually missing (no RLS at all, and erp_app held
// UPDATE/DELETE on a table meant to be tamper-proof) and the three
// controls this package plus that migration add: RLS, a database-level
// REVOKE that makes the app's own runtime role unable to rewrite
// history, and a tamper-evident hash chain.
//
// Log is meant to replace every existing `INSERT INTO audit_logs` call
// site (internal/inventory/handlers.go, internal/inventory/reconciliation.go,
// internal/sync/handlers.go all wrote directly before this package
// existed) — a raw INSERT bypasses the checksum chain entirely, silently
// reopening the exact gap this package closes.
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Entry is one audit event — mirrors audit_logs' own columns minus the
// bookkeeping ones (id, created_at, checksum, prev_checksum) this
// package computes.
type Entry struct {
	EntityType  string
	EntityID    string
	Action      string // "create" | "update" | "delete"
	PerformedBy string // "" if not attributable to a specific user
	Before      any    // marshaled to JSONB; nil is a valid "no before state" (a create)
	After       any    // nil is a valid "no after state" (a delete)
	Reason      string
}

// Log inserts one row into the tamper-evident chain inside the caller's
// own tenant-scoped transaction (the same db.WithTenant pattern every
// other tenant-scoped write in this codebase uses) — merchant_id and the
// chain's "previous" row both come from current_setting('app.tenant_id'),
// never a caller-supplied tenant id.
//
// The checksum is computed entirely inside this one SQL statement, from
// Postgres's own `::text` cast of the values actually being stored —
// not from Go's pre-insert json.Marshal bytes, which are not guaranteed
// to match what a later `::text` read of the same JSONB column produces
// (see migrations/021_audit_trail_hardening.sql's comment: confirmed
// live that JSONB reorders object keys and reformats whitespace).
// Verify (verify.go) recomputes with the identical expression shape —
// any change to the digest inputs here needs the same change there.
//
// Concurrency trade-off, stated plainly: reading the previous checksum
// and inserting the new one happen in one statement but aren't under an
// explicit lock, so two audit writes for the same tenant committing at
// almost the same instant could both read the same "previous" checksum
// and each chain from it, producing a fork rather than one strict line.
// That's an accepted limitation of a lightweight per-tenant hash chain
// under Postgres's default READ COMMITTED isolation — detecting *that* a
// row was altered after the fact (this package's actual goal) still
// works fine; only the much narrower guarantee of one single unbroken
// total order under high write concurrency is what's given up.
func Log(ctx context.Context, tx pgx.Tx, e Entry) error {
	if e.EntityType == "" || e.EntityID == "" || e.Action == "" {
		return fmt.Errorf("audit: entity_type, entity_id, and action are required")
	}

	beforeJSON, err := marshalNullable(e.Before)
	if err != nil {
		return fmt.Errorf("audit: marshal before_value: %w", err)
	}
	afterJSON, err := marshalNullable(e.After)
	if err != nil {
		return fmt.Errorf("audit: marshal after_value: %w", err)
	}
	performedBy := nullableString(e.PerformedBy)
	reason := nullableString(e.Reason)

	// Each raw $-parameter is cast to one explicit type exactly once, in
	// the `params` CTE, and every other reference in the statement reuses
	// that already-typed column — found live: reusing a raw placeholder
	// directly in two different cast contexts within one statement (here,
	// `$2::uuid` for the column value and `$2::text` inside the digest
	// expression) is the exact same class of pgx/Postgres parameter-type-
	// inference bug bank_reconciliation.go's own header comment already
	// documents from an earlier phase — confirmed live again: the
	// identical SQL with literal values (no placeholders) ran fine via
	// psql, but failed through this parameterized call until rewritten
	// this way.
	_, err = tx.Exec(ctx, `
		WITH params AS (
			SELECT
				current_setting('app.tenant_id')::uuid AS tenant_id,
				$1::text AS entity_type,
				$2::uuid AS entity_id,
				$3::text AS action,
				$4::uuid AS performed_by,
				$5::jsonb AS before_value,
				$6::jsonb AS after_value,
				$7::text AS reason,
				now() AS created_at
		)
		INSERT INTO audit_logs (merchant_id, entity_type, entity_id, action, performed_by, before_value, after_value, reason, created_at, checksum, prev_checksum)
		SELECT
			p.tenant_id, p.entity_type, p.entity_id, p.action, p.performed_by, p.before_value, p.after_value, p.reason, p.created_at,
			encode(digest(concat_ws('|',
				(SELECT checksum FROM audit_logs WHERE merchant_id = p.tenant_id ORDER BY created_at DESC, id DESC LIMIT 1),
				p.tenant_id::text, p.entity_type, p.entity_id::text, p.action, p.performed_by::text,
				p.before_value::text, p.after_value::text, p.reason, p.created_at::text
			), 'sha256'), 'hex'),
			(SELECT checksum FROM audit_logs WHERE merchant_id = p.tenant_id ORDER BY created_at DESC, id DESC LIMIT 1)
		FROM params p`,
		e.EntityType, e.EntityID, e.Action, performedBy, beforeJSON, afterJSON, reason,
	)
	if err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}
	return nil
}

func marshalNullable(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
