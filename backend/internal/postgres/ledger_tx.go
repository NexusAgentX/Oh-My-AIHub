package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
)

type ledgerTx = ledgerpg.Tx

// LedgerTransaction is the transitional bridge for the domains that are still
// hand-written in this package (c2c, channel, gateway). It exposes the caller's
// pgx transaction for their own SQL and, through the embedded ledgerpg.Tx, the
// full ledger.Store bound to that same transaction, so ledger postings and
// business rows commit atomically. Delete it once those domains are migrated;
// migrated domains open their own pgx.Tx and call ledgerpg.NewTx directly
// (ARCHITECTURE.md, ADR-0020).
type LedgerTransaction struct {
	pgx.Tx
	*ledgerTx
}

func newLedgerTransaction(tx pgx.Tx) *LedgerTransaction {
	return &LedgerTransaction{Tx: tx, ledgerTx: ledgerpg.NewTx(tx)}
}

// WithLedgerTransaction runs work in one database transaction and commits it
// with the ledger error mapping.
func (s *Store) WithLedgerTransaction(ctx context.Context, work func(*LedgerTransaction) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := work(newLedgerTransaction(tx)); err != nil {
		return err
	}
	return ledgerpg.MapError(tx.Commit(ctx))
}
