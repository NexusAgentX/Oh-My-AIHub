package postgres

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/feerate"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

const (
	feeRateAuditAction     = "api_fee_rate.updated"
	feeRateAuditTargetType = "api_fee_rate"
)

func (s *Store) ListFeeRates(ctx context.Context, limit int) ([]feerate.Version, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT rate.version, rate.fee_rate_nano, rate.created_at,
			COALESCE(rate.created_by::text, ''), COALESCE(actor.username, ''), COALESCE(audit.reason, '')
		FROM api_fee_rates rate
		LEFT JOIN accounts actor ON actor.id = rate.created_by
		LEFT JOIN LATERAL (
			SELECT reason FROM audit_events
			WHERE target_type = $2 AND target_id = rate.version::text AND action = $3
			ORDER BY id LIMIT 1
		) audit ON true
		ORDER BY rate.version DESC
		LIMIT $1`, limit, feeRateAuditTargetType, feeRateAuditAction)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (feerate.Version, error) {
		var version feerate.Version
		var nano int64
		if err := row.Scan(&version.Version, &nano, &version.CreatedAt, &version.CreatedByID, &version.CreatedByUsername, &version.Reason); err != nil {
			return feerate.Version{}, err
		}
		version.Rate = money.FromNano(nano)
		return version, nil
	})
}

// AppendFeeRate serializes writers with a table lock that does not block
// readers, so gateway snapshots keep reading the latest committed version.
func (s *Store) AppendFeeRate(ctx context.Context, actorID string, expectedVersion int64, rate money.Amount, reason string) (feerate.Version, error) {
	transaction, err := s.pool.Begin(ctx)
	if err != nil {
		return feerate.Version{}, err
	}
	defer transaction.Rollback(ctx) //nolint:errcheck

	if _, err := transaction.Exec(ctx, `LOCK TABLE api_fee_rates IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return feerate.Version{}, err
	}
	var currentVersion, currentNano int64
	if err := transaction.QueryRow(ctx, `SELECT version, fee_rate_nano FROM api_fee_rates ORDER BY version DESC LIMIT 1`).Scan(&currentVersion, &currentNano); err != nil {
		return feerate.Version{}, err
	}
	if currentVersion != expectedVersion {
		return feerate.Version{}, feerate.ErrConflict
	}
	if currentNano == rate.Nano() {
		return feerate.Version{}, feerate.ErrUnchanged
	}

	created := feerate.Version{Rate: rate, CreatedByID: actorID, Reason: reason}
	if err := transaction.QueryRow(ctx, `
		INSERT INTO api_fee_rates (fee_rate_nano, created_by) VALUES ($1, $2)
		RETURNING version, created_at`, rate.Nano(), actorID).Scan(&created.Version, &created.CreatedAt); err != nil {
		return feerate.Version{}, err
	}
	if err := transaction.QueryRow(ctx, `SELECT username FROM accounts WHERE id = $1`, actorID).Scan(&created.CreatedByUsername); err != nil {
		return feerate.Version{}, err
	}
	details := map[string]any{
		"before": map[string]any{"version": currentVersion, "fee_rate_nano": currentNano},
		"after":  map[string]any{"version": created.Version, "fee_rate_nano": rate.Nano()},
	}
	if err := insertAudit(ctx, transaction, actorID, feeRateAuditAction, feeRateAuditTargetType, strconv.FormatInt(created.Version, 10), reason, details); err != nil {
		return feerate.Version{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return feerate.Version{}, err
	}
	return created, nil
}
