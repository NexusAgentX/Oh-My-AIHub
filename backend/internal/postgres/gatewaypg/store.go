// Package gatewaypg is the PostgreSQL implementation of gateway.Store: platform
// keys and model pools, the call snapshot (candidates, price tiers, spend
// authorization hold), upstream attempts, heartbeat leases, idempotent
// finalization with ledger settlement, delivery confirmation or compensation,
// and orphan recovery. SQL lives in queries.sql; the generated code is
// committed beside it.
//
// Transactions are owned here (ADR-0020). BeginCall runs in one REPEATABLE READ
// snapshot transaction, and finalization and delivery compensation run in
// SERIALIZABLE ones; ledger postings and holds join those same transactions
// through ledgerpg.NewTx, so settlement and ledger capture/release commit
// atomically. Offer routing facts come from channelpg.ResolveRoutingTargets on
// the same snapshot transaction.
package gatewaypg

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/channelpg"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
	// commitHook is a deterministic test seam for the PostgreSQL "commit
	// succeeded but the acknowledgement was lost" outcome. Production stores
	// leave it nil. Callers must still disambiguate every returned commit error
	// by rereading the immutable business fact.
	commitHook func(operation, resourceID string) error
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: New(pool)}
}

var _ gateway.Store = (*Store)(nil)

// SetCommitHook installs the commit-acknowledgement test seam described on
// Store.commitHook. It must only be used by tests, before concurrent use.
func (s *Store) SetCommitHook(hook func(operation, resourceID string) error) {
	s.commitHook = hook
}

// commit commits tx and then lets the test seam simulate a lost acknowledgement.
func (s *Store) commit(ctx context.Context, tx pgx.Tx, operation, resourceID string) error {
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if s.commitHook != nil {
		return s.commitHook(operation, resourceID)
	}
	return nil
}

func audit(ctx context.Context, db auditpg.DBTX, actorID, action, targetType, targetID, reason string, details map[string]any) error {
	return auditpg.Record(ctx, db, auditpg.Event{
		ActorID: actorID, Action: action, TargetType: targetType, TargetID: targetID,
		Reason: reason, Details: details,
	})
}

// routingTx lets a snapshot transaction act as a channel.RoutingStore, so the
// lease resolver reads offers inside the same REPEATABLE READ snapshot.
type routingTx struct{ db channelpg.DBTX }

func (r routingTx) ResolveRoutingTargets(ctx context.Context, offerIDs []string) ([]channel.PoolOfferStatus, []channel.RoutingTarget, error) {
	return channelpg.ResolveRoutingTargets(ctx, r.db, offerIDs)
}

func mapGatewayError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return gateway.ErrNotFound
	}
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		switch pgErr.SQLState() {
		case "40001", "40P01":
			return gateway.ErrSnapshotRetry
		case "23505":
			return gateway.ErrConflict
		case "23503", "23514", "22P02":
			return gateway.ErrInvalidInput
		}
	}
	if strings.Contains(err.Error(), "api gateway") {
		return fmt.Errorf("gateway persistence: %w", err)
	}
	return err
}
