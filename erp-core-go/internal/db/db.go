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
