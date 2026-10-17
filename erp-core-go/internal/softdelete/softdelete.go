// Package softdelete is the one way a user-facing DELETE endpoint removes
// a record: it stamps deleted_at/deleted_by instead of removing the row,
// and writes the audit_logs entry with the row as it was. Row-level
// security then hides the row from every other query (migrations/050), so
// callers keep their existing dependency checks and need no read-side
// changes.
//
// Restore and the recycle bin listing live in handlers.go.
package softdelete

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/db"
)

// ErrConflict: restoring the row would duplicate a live one (a new record
// with the same name was created after the delete).
var ErrConflict = errors.New("softdelete: a live record already uses this name")

// Delete soft-deletes the live row of table with this id. pgx.ErrNoRows
// when there is none (never existed, another tenant's, or already
// deleted). table must be one migrations/050 covers; it is never user
// input.
func Delete(ctx context.Context, tx pgx.Tx, table, id, userID, reason string) error {
	t := pgx.Identifier{table}.Sanitize()
	var before json.RawMessage
	err := db.IncludeDeleted(ctx, tx, func() error {
		return tx.QueryRow(ctx, `
			UPDATE `+t+` AS t SET deleted_at = now(), deleted_by = NULLIF($2, '')::uuid
			WHERE t.id::text = $1 AND t.deleted_at IS NULL
			RETURNING to_jsonb(t) - 'deleted_at' - 'deleted_by'`, id, userID).Scan(&before)
	})
	if err != nil {
		return err
	}
	return audit.Log(ctx, tx, audit.Entry{EntityType: table, EntityID: id, Action: "delete",
		PerformedBy: userID, Before: before, Reason: reason})
}

// DeleteChildren soft-deletes every live row of table whose fkColumn is
// parentID — the rows a hard delete of the parent used to take with it
// (ON DELETE CASCADE). The parent's own audit entry covers them.
func DeleteChildren(ctx context.Context, tx pgx.Tx, table, fkColumn, parentID, userID string) error {
	return db.IncludeDeleted(ctx, tx, func() error {
		_, err := tx.Exec(ctx, `UPDATE `+pgx.Identifier{table}.Sanitize()+` SET deleted_at = now(), deleted_by = NULLIF($2, '')::uuid
			WHERE `+pgx.Identifier{fkColumn}.Sanitize()+`::text = $1 AND deleted_at IS NULL`, parentID, userID)
		return err
	})
}

// restoreChildren undoes DeleteChildren for one parent that is still
// deleted: the children deleted in the same transaction as it (same
// deleted_at), not ones removed individually before.
func restoreChildren(ctx context.Context, tx pgx.Tx, parentTable, id, table, fkColumn string) error {
	return db.IncludeDeleted(ctx, tx, func() error {
		_, err := tx.Exec(ctx, `UPDATE `+pgx.Identifier{table}.Sanitize()+` c SET deleted_at = NULL, deleted_by = NULL
			FROM `+pgx.Identifier{parentTable}.Sanitize()+` p
			WHERE p.id::text = $1 AND p.deleted_at IS NOT NULL
			  AND c.`+pgx.Identifier{fkColumn}.Sanitize()+` = p.id AND c.deleted_at = p.deleted_at`, id)
		return err
	})
}

// Restore brings a soft-deleted row back. pgx.ErrNoRows when there is no
// deleted row with this id; ErrConflict when a live row now holds its
// unique name or code.
func Restore(ctx context.Context, tx pgx.Tx, table, id, userID string) error {
	t := pgx.Identifier{table}.Sanitize()
	var after json.RawMessage
	err := db.IncludeDeleted(ctx, tx, func() error {
		return tx.QueryRow(ctx, `
			UPDATE `+t+` AS t SET deleted_at = NULL, deleted_by = NULL
			WHERE t.id::text = $1 AND t.deleted_at IS NOT NULL
			RETURNING to_jsonb(t) - 'deleted_at' - 'deleted_by'`, id).Scan(&after)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	return audit.Log(ctx, tx, audit.Entry{EntityType: table, EntityID: id, Action: "update",
		PerformedBy: userID, After: after, Reason: "restored from the recycle bin"})
}
