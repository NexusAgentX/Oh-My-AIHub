// Package feeratepg is the PostgreSQL implementation of feerate.Store.
package feeratepg

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/feerate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
)

const (
	auditAction     = "api_fee_rate.updated"
	auditTargetType = "api_fee_rate"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: New(pool)}
}

func (s *Store) ListFeeRates(ctx context.Context, limit int) ([]feerate.Version, error) {
	rows, err := s.q.ListFeeRates(ctx, ListFeeRatesParams{
		AuditTargetType: auditTargetType,
		AuditAction:     auditAction,
		RowLimit:        int64(limit),
	})
	if err != nil {
		return nil, err
	}
	versions := make([]feerate.Version, 0, len(rows))
	for _, row := range rows {
		versions = append(versions, feerate.Version{
			Version:           row.Version,
			Rate:              row.FeeRateNano,
			CreatedAt:         row.CreatedAt,
			CreatedByID:       row.CreatedByID,
			CreatedByUsername: row.CreatedByUsername,
			Reason:            row.Reason,
		})
	}
	return versions, nil
}

// AppendFeeRate serializes writers with a table lock that does not block
// readers, so gateway snapshots keep reading the latest committed version.
func (s *Store) AppendFeeRate(ctx context.Context, actorID string, expectedVersion int64, rate money.Amount, reason string) (feerate.Version, error) {
	var created feerate.Version
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockFeeRates(ctx); err != nil {
			return err
		}
		current, err := q.GetCurrentFeeRate(ctx)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return feerate.ErrConflict
		}
		if current.FeeRateNano == rate {
			return feerate.ErrUnchanged
		}
		inserted, err := q.InsertFeeRate(ctx, InsertFeeRateParams{FeeRateNano: rate, CreatedBy: &actorID})
		if err != nil {
			return err
		}
		username, err := q.GetActorUsername(ctx, actorID)
		if err != nil {
			return err
		}
		created = feerate.Version{
			Version:           inserted.Version,
			Rate:              rate,
			CreatedAt:         inserted.CreatedAt,
			CreatedByID:       actorID,
			CreatedByUsername: username,
			Reason:            reason,
		}
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: actorID, Action: auditAction, TargetType: auditTargetType,
			TargetID: strconv.FormatInt(created.Version, 10), Reason: reason,
			Details: map[string]any{
				"before": map[string]any{"version": current.Version, "fee_rate_nano": current.FeeRateNano.Nano()},
				"after":  map[string]any{"version": created.Version, "fee_rate_nano": rate.Nano()},
			},
		})
	})
	if err != nil {
		return feerate.Version{}, err
	}
	return created, nil
}

var _ feerate.Store = (*Store)(nil)
