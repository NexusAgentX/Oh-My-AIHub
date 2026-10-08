// Package catalogpg is the PostgreSQL implementation of catalog.Store.
package catalogpg

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: New(pool)} }

func toModel(row Model, tiers []ModelPriceTier) catalog.Model {
	model := catalog.Model{
		ID: row.ID, DisplayName: row.DisplayName, TokenPrices: decodePrices(row.TokenPrices),
		InputPrice: row.InputPriceNanoPerMillion, OutputPrice: row.OutputPriceNanoPerMillion,
		CacheWritePrice: row.CacheWritePriceNanoPerMillion, CacheReadPrice: row.CacheReadPriceNanoPerMillion,
		Enabled: row.Enabled, SortOrder: row.SortOrder, Provider: row.Provider, ContextWindow: row.ContextWindow,
		InputModalities: row.InputModalities, OutputModalities: row.OutputModalities,
		SupportsTools: row.SupportsTools, SupportsStructuredOutput: row.SupportsStructuredOutput, SupportsVision: row.SupportsVision,
		ParameterInfo: row.ParameterInfo, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	for _, tier := range tiers {
		var weekdays []int
		for _, weekday := range tier.Weekdays {
			weekdays = append(weekdays, int(weekday))
		}
		model.PriceTiers = append(model.PriceTiers, ledger.PriceTier{
			TokenPrices: decodePrices(tier.TokenPrices), ServiceTier: tier.ServiceTier, ThinkingMode: tier.ThinkingMode,
			Name: tier.Name, MinPromptTokens: tier.MinPromptTokens, MaxPromptTokens: tier.MaxPromptTokens,
			Timezone: tier.Timezone, Weekdays: weekdays, StartMinute: tier.StartMinuteOfDay, EndMinute: tier.EndMinuteOfDay,
			InputPrice: tier.InputPriceNanoPerMillion, OutputPrice: tier.OutputPriceNanoPerMillion,
			CacheWritePrice: tier.CacheWritePriceNanoPerMillion, CacheReadPrice: tier.CacheReadPriceNanoPerMillion,
		})
	}
	return model
}

// withTiers loads the tiers of the given rows in one query.
func withTiers(ctx context.Context, q *Queries, rows []Model) ([]catalog.Model, error) {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	tiers, err := q.ListTiers(ctx, ids)
	if err != nil {
		return nil, err
	}
	byModel := map[string][]ModelPriceTier{}
	for _, tier := range tiers {
		byModel[tier.ModelID] = append(byModel[tier.ModelID], tier)
	}
	models := make([]catalog.Model, 0, len(rows))
	for _, row := range rows {
		models = append(models, toModel(row, byModel[row.ID]))
	}
	if err := sources(ctx, q.db, models); err != nil {
		return nil, err
	}
	return models, nil
}

// List reads the catalog through db so other domains can read it inside
// their own transaction.
func List(ctx context.Context, db DBTX, includeDisabled bool) ([]catalog.Model, error) {
	q := New(db)
	rows, err := q.ListModels(ctx, includeDisabled)
	if err != nil {
		return nil, err
	}
	return withTiers(ctx, q, rows)
}

func (s *Store) ListModels(ctx context.Context, includeDisabled bool) ([]catalog.Model, error) {
	return List(ctx, s.pool, includeDisabled)
}

func (s *Store) GetModel(ctx context.Context, id string) (catalog.Model, error) {
	row, err := s.q.GetModel(ctx, id)
	if err != nil {
		return catalog.Model{}, mapError(err)
	}
	models, err := withTiers(ctx, s.q, []Model{row})
	if err != nil {
		return catalog.Model{}, err
	}
	return models[0], nil
}

func replaceTiers(ctx context.Context, q *Queries, modelID string, tiers []ledger.PriceTier) error {
	if err := q.DeleteTiers(ctx, modelID); err != nil {
		return err
	}
	for index, tier := range tiers {
		var weekdays []int16
		for _, weekday := range tier.Weekdays {
			weekdays = append(weekdays, int16(weekday))
		}
		if err := q.InsertTier(ctx, InsertTierParams{
			ModelID: modelID, Seq: int32(index + 1), Name: tier.Name,
			MinPromptTokens: tier.MinPromptTokens, MaxPromptTokens: tier.MaxPromptTokens,
			Timezone: tier.Timezone, Weekdays: weekdays, StartMinuteOfDay: tier.StartMinute, EndMinuteOfDay: tier.EndMinute,
			InputPriceNanoPerMillion: tier.InputPrice, OutputPriceNanoPerMillion: tier.OutputPrice,
			CacheWritePriceNanoPerMillion: tier.CacheWritePrice, CacheReadPriceNanoPerMillion: tier.CacheReadPrice,
			TokenPrices: encodePrices(tier.TokenPrices), ServiceTier: tier.ServiceTier, ThinkingMode: tier.ThinkingMode,
		}); err != nil {
			return mapError(err)
		}
	}
	return nil
}

func (s *Store) CreateModel(ctx context.Context, actorID string, model catalog.Model) (catalog.Model, error) {
	var created catalog.Model
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockModelWrites(ctx, model.ID); err != nil {
			return err
		}
		row, err := q.InsertModel(ctx, InsertModelParams{
			ID: model.ID, DisplayName: model.DisplayName, TokenPrices: encodePrices(model.TokenPrices),
			InputPriceNanoPerMillion: model.InputPrice, OutputPriceNanoPerMillion: model.OutputPrice,
			CacheWritePriceNanoPerMillion: model.CacheWritePrice, CacheReadPriceNanoPerMillion: model.CacheReadPrice,
			Enabled: model.Enabled, SortOrder: model.SortOrder, Provider: model.Provider, ContextWindow: model.ContextWindow,
			InputModalities: model.InputModalities, OutputModalities: model.OutputModalities,
			SupportsTools: model.SupportsTools, SupportsStructuredOutput: model.SupportsStructuredOutput,
			SupportsVision: model.SupportsVision, ParameterInfo: model.ParameterInfo,
		})
		if err != nil {
			return mapError(err)
		}
		if err := replaceTiers(ctx, q, row.ID, model.PriceTiers); err != nil {
			return err
		}
		models, err := withTiers(ctx, q, []Model{row})
		if err != nil {
			return err
		}
		created = models[0]
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: actorID, Action: audit.ActionModelCreated, TargetType: "model", TargetID: row.ID,
			Detail: map[string]any{"after": auditView(created)},
		})
	})
	return created, err
}

func (s *Store) UpdateModel(ctx context.Context, actorID, id string, mutate func(catalog.Model) (catalog.Model, error)) (catalog.Model, error) {
	var updated catalog.Model
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockModelWrites(ctx, id); err != nil {
			return err
		}
		row, err := q.LockModel(ctx, id)
		if err != nil {
			return mapError(err)
		}
		currentModels, err := withTiers(ctx, q, []Model{row})
		if err != nil {
			return err
		}
		current := currentModels[0]
		next, err := mutate(current)
		if err != nil {
			return err
		}
		if current.Source != nil {
			if next.Source == nil {
				return catalog.ErrInvalidInput
			}
			if current.Source.SyncEnabled && next.Source.SyncEnabled {
				a, b := current, next
				a.Enabled = b.Enabled
				a.SortOrder = b.SortOrder
				a.ParameterInfo = b.ParameterInfo
				if !sameManaged(a, b) {
					return catalog.ErrInvalidInput
				}
			}
			if next.Enabled && !next.Source.PriceReady {
				return catalog.ErrInvalidInput
			}
			raw, _ := json.Marshal(next.Source)
			if err := q.UpdateSourceControl(ctx, UpdateSourceControlParams{ModelID: id, SyncEnabled: next.Source.SyncEnabled, Info: raw}); err != nil {
				return err
			}
		}
		saved, err := q.UpdateModel(ctx, UpdateModelParams{
			ID: id, DisplayName: next.DisplayName, TokenPrices: encodePrices(next.TokenPrices),
			InputPriceNanoPerMillion: next.InputPrice, OutputPriceNanoPerMillion: next.OutputPrice,
			CacheWritePriceNanoPerMillion: next.CacheWritePrice, CacheReadPriceNanoPerMillion: next.CacheReadPrice,
			Enabled: next.Enabled, SortOrder: next.SortOrder, Provider: next.Provider, ContextWindow: next.ContextWindow,
			InputModalities: next.InputModalities, OutputModalities: next.OutputModalities,
			SupportsTools: next.SupportsTools, SupportsStructuredOutput: next.SupportsStructuredOutput,
			SupportsVision: next.SupportsVision, ParameterInfo: next.ParameterInfo,
		})
		if err != nil {
			return mapError(err)
		}
		if err := replaceTiers(ctx, q, id, next.PriceTiers); err != nil {
			return err
		}
		models, err := withTiers(ctx, q, []Model{saved})
		if err != nil {
			return err
		}
		updated = models[0]
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: actorID, Action: audit.ActionModelUpdated, TargetType: "model", TargetID: id,
			Detail: map[string]any{"before": auditView(current), "after": auditView(updated)},
		})
	})
	return updated, err
}

// DeleteModel removes catalog configuration atomically; historical calls and keys
// identify models by name and are deliberately retained.
func (s *Store) DeleteModel(ctx context.Context, actorID, id string) error {
	return pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockModelWrites(ctx, id); err != nil {
			return err
		}
		row, err := q.LockModel(ctx, id)
		if err != nil {
			return mapError(err)
		}
		models, err := withTiers(ctx, q, []Model{row})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM channel_models cm USING channels c WHERE cm.channel_id = c.id AND c.deleted_at IS NOT NULL AND cm.model_id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM route_prefs WHERE model_id = $1`, id); err != nil {
			return err
		}
		if err := q.IgnoreSource(ctx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM models WHERE id = $1`, id); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				return catalog.ErrInUse
			}
			return err
		}
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: actorID, Action: audit.ActionModelDeleted, TargetType: "model", TargetID: id,
			Detail: map[string]any{"before": auditView(models[0])},
		})
	})
}

// auditView keeps the price-relevant fields of a model for the audit log.
func auditView(model catalog.Model) map[string]any {
	tiers := make([]map[string]any, 0, len(model.PriceTiers))
	for _, tier := range model.PriceTiers {
		tiers = append(tiers, map[string]any{
			"name": tier.Name, "min_prompt_tokens": tier.MinPromptTokens, "max_prompt_tokens": tier.MaxPromptTokens,
			"timezone": tier.Timezone, "weekdays": tier.Weekdays, "start_minute_of_day": tier.StartMinute, "end_minute_of_day": tier.EndMinute,
			"input_price": tier.InputPrice.String(), "output_price": tier.OutputPrice.String(),
			"token_prices": tier.TokenPrices, "service_tier": tier.ServiceTier, "thinking_mode": tier.ThinkingMode,
			"cache_write_price": tier.CacheWritePrice.String(), "cache_read_price": tier.CacheReadPrice.String(),
		})
	}
	return map[string]any{
		"display_name": model.DisplayName, "enabled": model.Enabled, "source": model.Source,
		"input_price": model.InputPrice.String(), "output_price": model.OutputPrice.String(),
		"token_prices":      model.TokenPrices,
		"cache_write_price": model.CacheWritePrice.String(), "cache_read_price": model.CacheReadPrice.String(),
		"price_tiers": tiers,
	}
}

func mapError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return catalog.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return catalog.ErrConflict
		case "23514":
			return catalog.ErrInvalidInput
		}
	}
	return err
}

func encodePrices(prices map[string]money.Amount) []byte {
	if prices == nil {
		return []byte("{}")
	}
	raw, _ := json.Marshal(prices)
	return raw
}
func decodePrices(raw []byte) map[string]money.Amount {
	var prices map[string]money.Amount
	_ = json.Unmarshal(raw, &prices)
	return prices
}
