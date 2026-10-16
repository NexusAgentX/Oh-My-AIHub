// Package settingspg is the PostgreSQL implementation of settings.Store.
package settingspg

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
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
	var codex settings.Codex
	_ = json.Unmarshal(row.Codex, &codex)
	return settings.Settings{
		Codex:                    codex,
		FeeRateNano:              row.FeeRateNano,
		C2CPaymentTimeoutMinutes: row.C2cPaymentTimeoutMinutes,
		DefaultCreditLimit:       row.DefaultCreditLimitNano,
		DefaultMaxAttempts:       row.DefaultMaxAttempts,
		DefaultTTFTTimeoutMS:     row.DefaultTtftTimeoutMs,
		DefaultRequestTimeoutMS:  row.DefaultRequestTimeoutMs,
		DefaultIdleTimeoutMS:     row.DefaultIdleTimeoutMs,
		DefaultCooldownFailures:  row.DefaultCooldownFailures,
		DefaultCooldownSeconds:   row.DefaultCooldownSeconds,
		AccountMaxInflightCalls:  row.AccountMaxInflightCalls,
		ExtraBlockedHosts:        hosts,
		USDCNYRateNano:           row.UsdCnyRateNano,
		FXAutoSync:               row.FxAutoSync,
		FXSyncMinute:             row.FxSyncMinute,
		USDCNYRateSource:         row.UsdCnyRateSource,
		USDCNYRateUpdatedAt:      row.UsdCnyRateUpdatedAt,
		FXSyncAttemptedAt:        row.FxSyncAttemptedAt,
		FXSyncError:              row.FxSyncError,
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
		codex, err := json.Marshal(value.Codex)
		if err != nil {
			return err
		}
		row, err := q.UpdateSettings(ctx, UpdateSettingsParams{
			Codex:                    codex,
			FeeRateNano:              value.FeeRateNano,
			C2cPaymentTimeoutMinutes: value.C2CPaymentTimeoutMinutes,
			DefaultCreditLimitNano:   value.DefaultCreditLimit,
			DefaultMaxAttempts:       value.DefaultMaxAttempts,
			DefaultTtftTimeoutMs:     value.DefaultTTFTTimeoutMS,
			DefaultRequestTimeoutMs:  value.DefaultRequestTimeoutMS,
			DefaultIdleTimeoutMs:     value.DefaultIdleTimeoutMS,
			DefaultCooldownFailures:  value.DefaultCooldownFailures,
			DefaultCooldownSeconds:   value.DefaultCooldownSeconds,
			AccountMaxInflightCalls:  value.AccountMaxInflightCalls,
			ExtraBlockedHosts:        value.ExtraBlockedHosts,
			FxAutoSync:               value.FXAutoSync,
			FxSyncMinute:             value.FXSyncMinute,
			UsdCnyRateNano:           value.USDCNYRateNano,
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
		"codex_sha256":                fmt.Sprintf("%x", sha256.Sum256([]byte(value.Codex.Whoami+"\x00"+value.Codex.Accounts+"\x00"+value.Codex.Models))),
		"fee_rate_nano":               value.FeeRateNano,
		"c2c_payment_timeout_minutes": value.C2CPaymentTimeoutMinutes,
		"default_credit_limit":        value.DefaultCreditLimit.String(),
		"default_max_attempts":        value.DefaultMaxAttempts,
		"default_ttft_timeout_ms":     value.DefaultTTFTTimeoutMS,
		"default_request_timeout_ms":  value.DefaultRequestTimeoutMS,
		"default_idle_timeout_ms":     value.DefaultIdleTimeoutMS,
		"default_cooldown_failures":   value.DefaultCooldownFailures,
		"default_cooldown_seconds":    value.DefaultCooldownSeconds,
		"account_max_inflight_calls":  value.AccountMaxInflightCalls,
		"extra_blocked_hosts":         value.ExtraBlockedHosts,
		"usd_cny_rate":                money.FromNano(value.USDCNYRateNano).String(),
		"fx_auto_sync":                value.FXAutoSync,
		"fx_sync_minute":              value.FXSyncMinute,
	}
}

func (s *Store) SaveSyncedRate(ctx context.Context, actorID string, rateNano int64, at time.Time) (settings.Settings, error) {
	var updated settings.Settings
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		before, err := q.LockSettings(ctx)
		if err != nil {
			return err
		}
		row, err := q.SaveSyncedRate(ctx, SaveSyncedRateParams{UsdCnyRateNano: rateNano, At: at})
		if err != nil {
			return err
		}
		updated = toSettings(row)
		if before.UsdCnyRateNano == rateNano && actorID == "" {
			return nil
		}
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: actorID, Action: audit.ActionExchangeRateSynced, TargetType: "settings", TargetID: "platform",
			Detail: map[string]any{
				"before": money.FromNano(before.UsdCnyRateNano).String(), "after": money.FromNano(rateNano).String(),
				"source": settings.FrankfurterURL,
			},
		})
	})
	return updated, err
}

func (s *Store) SaveSyncFailure(ctx context.Context, message string, at time.Time) (settings.Settings, error) {
	row, err := s.q.SaveSyncFailure(ctx, SaveSyncFailureParams{At: at, Message: message})
	if err != nil {
		return settings.Settings{}, err
	}
	return toSettings(row), nil
}
