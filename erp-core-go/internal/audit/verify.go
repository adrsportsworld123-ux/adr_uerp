package audit

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// VerifyResult reports whether a tenant's whole audit_logs chain is
// internally consistent — every row's stored checksum matches one
// recomputed from its own fields plus the previous row's checksum, and
// every row's prev_checksum actually matches the previous row's stored
// checksum. A row altered after the fact (even by a superuser bypassing
// RLS/grants entirely — the one actor the REVOKE in migrations/021 can't
// stop) breaks the chain at that exact point, which this reports rather
// than silently missing.
//
// LegacyRowsSkipped counts rows written *before* audit hardening shipped
// (checksum IS NULL) at the *start* of the tenant's history — found
// live, and real for every deployment, not just this dev merchant: this
// feature was added to a table that already had rows in it, so treating
// every one of those as "the chain is broken starting at row 1" would
// make Verify report "invalid" forever for any pre-existing merchant,
// which defeats the point. Those legacy rows are reported separately,
// not as evidence of tampering; strict chain verification starts at the
// first row that actually has a checksum (whose own prev_checksum is
// correctly NULL, the same as if it were the true start of the chain).
// A NULL checksum appearing *after* that point, interleaved with
// checksummed rows, is a different story — that's genuinely reported as
// broken, not skipped.
type VerifyResult struct {
	Valid             bool    `json:"valid"`
	RowsChecked       int     `json:"rows_checked"`
	LegacyRowsSkipped int     `json:"legacy_rows_skipped"`
	BrokenAtID        *int64  `json:"broken_at_id"`
	BrokenReason      *string `json:"broken_reason"`
}

// Verify walks the caller's own tenant's chain in (created_at, id) order
// — the same order Log itself reads "the previous row" in — recomputing
// each row's checksum with the identical `digest(concat_ws(...))`
// expression Log used to write it, so the comparison never depends on
// Go's own JSON serialization (see log.go's own doc comment on why that
// would have been wrong). Runs inside the caller's own tenant-scoped
// transaction (db.WithTenant), same as every other tenant-scoped read in
// this codebase.
func Verify(ctx context.Context, tx pgx.Tx) (VerifyResult, error) {
	rows, err := tx.Query(ctx, `
		WITH chain AS (
			SELECT id, entity_type, entity_id, action, performed_by, before_value, after_value, reason, created_at,
			       checksum, prev_checksum,
			       LAG(checksum) OVER (ORDER BY created_at, id) AS expected_prev
			FROM audit_logs
			WHERE merchant_id = current_setting('app.tenant_id')::uuid
		)
		SELECT id, checksum, prev_checksum, expected_prev,
			encode(digest(concat_ws('|',
				expected_prev, current_setting('app.tenant_id'), entity_type, entity_id::text, action, performed_by::text,
				before_value::text, after_value::text, reason, created_at::text
			), 'sha256'), 'hex') AS recomputed
		FROM chain
		ORDER BY created_at, id`)
	if err != nil {
		return VerifyResult{}, err
	}
	defer rows.Close()

	result := VerifyResult{Valid: true}
	chainStarted := false
	for rows.Next() {
		var id int64
		var checksum, prevChecksum, expectedPrev *string
		var recomputed string
		if err := rows.Scan(&id, &checksum, &prevChecksum, &expectedPrev, &recomputed); err != nil {
			return result, err
		}

		if !chainStarted {
			if checksum == nil {
				result.LegacyRowsSkipped++
				continue // pre-hardening row, before any checksum was ever computed — not evidence of tampering
			}
			chainStarted = true
		}

		result.RowsChecked++
		if !result.Valid {
			continue // already found the break; keep counting rows, don't overwrite where it was found
		}

		switch {
		case checksum == nil:
			result.Valid = false
			result.BrokenAtID = &id
			result.BrokenReason = strPtr("row has no checksum, but later rows do — the checksum column was cleared on this row after the fact")
		case expectedPrev != nil && (prevChecksum == nil || *prevChecksum != *expectedPrev):
			result.Valid = false
			result.BrokenAtID = &id
			result.BrokenReason = strPtr("prev_checksum does not match the preceding row's checksum")
		case *checksum != recomputed:
			result.Valid = false
			result.BrokenAtID = &id
			result.BrokenReason = strPtr("stored checksum does not match recomputed checksum — row contents changed after insert")
		}
	}
	return result, rows.Err()
}

func strPtr(s string) *string { return &s }
