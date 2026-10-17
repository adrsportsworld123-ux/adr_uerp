// Package db wraps the Postgres connection pool and enforces the one rule
// every tenant-scoped query in this codebase must follow: never touch a
// tenant table outside a WithTenant block. See phase0_1_design.md §2.1 —
// the row-level-security policies in phase0_1_database_schema.sql make it
// impossible to accidentally read cross-tenant data through this path,
// because a transaction with no tenant context set errors out rather than
// returning rows.
package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return &DB{Pool: pool}, nil
}

func (d *DB) Close() {
	d.Pool.Close()
}

// WithTenant runs fn inside a transaction with the Postgres session
// variable app.tenant_id set for the transaction's lifetime, so every
// RLS-protected table fn queries is scoped to tenantID.
//
// Uses set_config(...) rather than "SET LOCAL app.tenant_id = $1" because
// Postgres's SET command does not accept bind parameters over the extended
// query protocol — set_config is the parameterized equivalent, and it's
// what keeps this safe from injection since tenantID ultimately comes from
// a JWT claim, not a trusted constant. Verified against a live Postgres 16
// instance during design (see phase0_1_design.md).
func (d *DB) WithTenant(ctx context.Context, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once committed

	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		return fmt.Errorf("db: set tenant context: %w", err)
	}
	// Phase 15: the request's branch scope, when the API attached one
	// (migrations/044_branch_data_scope.sql). Absent = unrestricted, for
	// background work that doesn't act for a user.
	if scope, ok := ScopeFrom(ctx); ok {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.branch_scope', $1, true)`, scope.setting()); err != nil {
			return fmt.Errorf("db: set branch scope: %w", err)
		}
	}

	if err := fn(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

// ErrNotFound is returned by lookups that use pgx.ErrNoRows internally, so
// callers in other packages don't need to import pgx just to check this.
var ErrNotFound = pgx.ErrNoRows

// BranchScope is which branches' rows a request may see and write
// (Phase 15). All = every branch of the merchant.
type BranchScope struct {
	All       bool
	BranchIDs []string
}

// Allows reports whether a branch id is inside the scope.
func (s BranchScope) Allows(branchID string) bool {
	if s.All {
		return true
	}
	for _, b := range s.BranchIDs {
		if b == branchID {
			return true
		}
	}
	return false
}

func (s BranchScope) setting() string {
	switch {
	case s.All:
		return "all"
	case len(s.BranchIDs) == 0:
		return "none" // a scoped user with no branch sees no branch's rows
	}
	return strings.Join(s.BranchIDs, ",")
}

type scopeKey struct{}

// WithBranchScope attaches a scope that every WithTenant transaction in
// this context then applies.
func WithBranchScope(ctx context.Context, s BranchScope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

// ScopeFrom returns the context's branch scope, if one was attached.
func ScopeFrom(ctx context.Context) (BranchScope, bool) {
	s, ok := ctx.Value(scopeKey{}).(BranchScope)
	return s, ok
}

// Unrestricted runs fn with the branch scope lifted, then puts it back.
// Only for system bookkeeping that a scoped user's legitimate action
// implies in every branch, such as the zero-stock rows a new product
// variant gets everywhere. Never for reading or acting on another branch's
// data on a user's behalf.
func Unrestricted(ctx context.Context, tx pgx.Tx, fn func() error) error {
	var prev string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(current_setting('app.branch_scope', true), '')`).Scan(&prev); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.branch_scope', 'all', true)`); err != nil {
		return err
	}
	ferr := fn()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.branch_scope', $1, true)`, prev); err != nil && ferr == nil {
		ferr = err
	}
	return ferr
}

// IncludeDeleted runs fn with soft-deleted rows visible and writable
// (app.include_deleted, migrations/050), then hides them again. Only for
// the soft-delete and restore statements themselves and the recycle bin
// (internal/softdelete) — every other query relies on deleted rows being
// invisible, the way it relies on another tenant's rows being invisible.
func IncludeDeleted(ctx context.Context, tx pgx.Tx, fn func() error) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.include_deleted', 'on', true)`); err != nil {
		return err
	}
	ferr := fn()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.include_deleted', 'off', true)`); err != nil && ferr == nil {
		ferr = err
	}
	return ferr
}
