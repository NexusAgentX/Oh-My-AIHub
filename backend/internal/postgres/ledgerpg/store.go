// Package ledgerpg is the PostgreSQL implementation of ledger.Store. SQL lives
// in queries.sql; the generated code is committed beside it.
//
// Two entry points share one implementation:
//
//   - Store runs each ledger operation in its own transaction (or, for reads,
//     directly on the pool). It is what the services embed.
//   - Tx runs the same operations on a caller-owned transaction. Business
//     domains that must commit ledger postings atomically with their own rows
//     (c2c, gateway) open one pgx.Tx, wrap it with NewTx and hand it to
//     ledger.NewService. Tx never begins, commits or rolls back; the caller
//     owns the transaction, its isolation level and its commit.
package ledgerpg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: New(pool)}
}

var _ ledger.Store = (*Store)(nil)

// WithTransaction opens a transaction, runs work against a Tx bound to it and
// commits. The commit error is mapped like every other ledger error; the work
// error is returned unchanged.
func (s *Store) WithTransaction(ctx context.Context, work func(*Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := work(NewTx(tx)); err != nil {
		return err
	}
	return MapError(tx.Commit(ctx))
}

func (s *Store) Wallet(ctx context.Context, accountID string) (ledger.Wallet, error) {
	return walletByRef(ctx, s.q, ledger.UserAccount(accountID))
}

func (s *Store) WalletByRef(ctx context.Context, account ledger.AccountRef) (ledger.Wallet, error) {
	return walletByRef(ctx, s.q, account)
}

func (s *Store) Entries(ctx context.Context, accountID string, beforeID int64, limit int) ([]ledger.Entry, error) {
	return entriesByRef(ctx, s.q, ledger.UserAccount(accountID), beforeID, limit)
}

func (s *Store) EntriesByRef(ctx context.Context, account ledger.AccountRef, beforeID int64, limit int) ([]ledger.Entry, error) {
	return entriesByRef(ctx, s.q, account, beforeID, limit)
}

func (s *Store) Metrics(ctx context.Context) (ledger.Metrics, error) {
	return ledgerMetrics(ctx, s.q)
}

func (s *Store) Post(ctx context.Context, request ledger.PostRequest, hash [32]byte) (ledger.Transaction, error) {
	var result ledger.Transaction
	err := s.WithTransaction(ctx, func(tx *Tx) error {
		var err error
		result, err = tx.Post(ctx, request, hash)
		return err
	})
	return result, err
}

func (s *Store) Reverse(ctx context.Context, key, originalID, reason, referenceID, actorID string, hash [32]byte) (ledger.Transaction, error) {
	var result ledger.Transaction
	err := s.WithTransaction(ctx, func(tx *Tx) error {
		var err error
		result, err = tx.Reverse(ctx, key, originalID, reason, referenceID, actorID, hash)
		return err
	})
	return result, err
}

func (s *Store) CreateHold(ctx context.Context, request ledger.CreateHoldRequest, hash [32]byte) (ledger.Hold, error) {
	var result ledger.Hold
	err := s.WithTransaction(ctx, func(tx *Tx) error {
		var err error
		result, err = tx.CreateHold(ctx, request, hash)
		return err
	})
	return result, err
}

func (s *Store) ReleaseHold(ctx context.Context, request ledger.MutateHoldRequest, hash [32]byte) (ledger.Hold, error) {
	var result ledger.Hold
	err := s.WithTransaction(ctx, func(tx *Tx) error {
		var err error
		result, err = tx.ReleaseHold(ctx, request, hash)
		return err
	})
	return result, err
}

func (s *Store) CaptureHold(ctx context.Context, request ledger.CaptureHoldRequest, hash [32]byte) (ledger.CaptureResult, error) {
	var result ledger.CaptureResult
	err := s.WithTransaction(ctx, func(tx *Tx) error {
		var err error
		result, err = tx.CaptureHold(ctx, request, hash)
		return err
	})
	return result, err
}

func (s *Store) TransferBadDebt(ctx context.Context, accountID string, amount money.Amount, key, reason, referenceID, actorID string, hash [32]byte) (ledger.Transaction, error) {
	var result ledger.Transaction
	err := s.WithTransaction(ctx, func(tx *Tx) error {
		var err error
		result, err = tx.TransferBadDebt(ctx, accountID, amount, key, reason, referenceID, actorID, hash)
		return err
	})
	return result, err
}

// MapError converts PostgreSQL errors to ledger errors. It is exported for the
// callers that commit a transaction they share with the ledger.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ledger.ErrConflict
		case "23514", "22003":
			return ledger.ErrInvalidInput
		}
	}
	return fmt.Errorf("ledger store: %w", err)
}
