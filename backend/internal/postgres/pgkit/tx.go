// Package pgkit holds the small helpers shared by every per-domain persistence
// package (internal/postgres/<domain>pg). It contains no SQL and no domain
// knowledge; see ADR-0017.
package pgkit

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Beginner is satisfied by *pgxpool.Pool and pgx.Tx.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// InTx runs fn inside one transaction: it commits when fn returns nil and rolls
// back otherwise. The commit error is returned unchanged so callers can still
// disambiguate "commit acknowledged or not" outcomes.
func InTx(ctx context.Context, db Beginner, fn func(tx pgx.Tx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
