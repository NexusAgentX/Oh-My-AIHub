// Package settingspg is the PostgreSQL implementation of settings.Store.
package settingspg

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: New(pool)} }

func toSettings(row Setting) settings.Settings {
	hosts := row.ExtraBlockedHosts
	if hosts == nil {
		hosts = []string{}
	}
	return settings.Settings{
		FeeRateNano:              row.FeeRateNano,
		C2CPaymentTimeoutMinutes: row.C2cPaymentTimeoutMinutes,
		DefaultCreditLimit:       row.DefaultCreditLimitNano,
		DefaultMaxAttempts:       row.DefaultMaxAttempts,
		DefaultTTFTTimeoutMS:     row.DefaultTtftTimeoutMs,
		DefaultTotalTimeoutMS:    row.DefaultTotalTimeoutMs,
		DefaultCooldownFailures:  row.DefaultCooldownFailures,
		DefaultCooldownSeconds:   row.DefaultCooldownSeconds,
		ExtraBlockedHosts:        hosts,
		UpdatedAt:                row.UpdatedAt,
	}
}

// Get reads the settings through db, so other domains can read them inside
// their own transaction.
func Get(ctx context.Context, db DBTX) (settings.Settings, error) {
	row, err := New(db).GetSettings(ctx)
	if err != nil {
		return settings.Settings{}, err
	}
	return toSettings(row), nil
}

func (s *Store) GetSettings(ctx context.Context) (settings.Settings, error) {
	return Get(ctx, s.pool)
}

func (s *Store) UpdateSettings(ctx context.Context, actorID string, value settings.Settings) (settings.Settings, error) {
	var updated settings.Settings
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		before, err := q.LockSettings(ctx)
		if err != nil {
			return err
		}
		row, err := q.UpdateSettings(ctx, UpdateSettingsParams{
			FeeRateNano:              value.FeeRateNano,
			C2cPaymentTimeoutMinutes: value.C2CPaymentTimeoutMinutes,
			DefaultCreditLimitNano:   value.DefaultCreditLimit,
			DefaultMaxAttempts:       value.DefaultMaxAttempts,
			DefaultTtftTimeoutMs:     value.DefaultTTFTTimeoutMS,
			DefaultTotalTimeoutMs:    value.DefaultTotalTimeoutMS,
			DefaultCooldownFailures:  value.DefaultCooldownFailures,
			DefaultCooldownSeconds:   value.DefaultCooldownSeconds,
			ExtraBlockedHosts:        value.ExtraBlockedHosts,
		})
		if err != nil {
			return err
		}
		updated = toSettings(row)
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: actorID, Action: audit.ActionSettingsUpdated, TargetType: "settings", TargetID: "platform",
			Detail: map[string]any{"before": auditView(toSettings(before)), "after": auditView(updated)},
		})
	})
	return updated, err
}

func auditView(value settings.Settings) map[string]any {
	return map[string]any{
		"fee_rate_nano":               value.FeeRateNano,
		"c2c_payment_timeout_minutes": value.C2CPaymentTimeoutMinutes,
		"default_credit_limit":        value.DefaultCreditLimit.String(),
		"default_max_attempts":        value.DefaultMaxAttempts,
		"default_ttft_timeout_ms":     value.DefaultTTFTTimeoutMS,
		"default_total_timeout_ms":    value.DefaultTotalTimeoutMS,
		"default_cooldown_failures":   value.DefaultCooldownFailures,
		"default_cooldown_seconds":    value.DefaultCooldownSeconds,
		"extra_blocked_hosts":         value.ExtraBlockedHosts,
	}
}
