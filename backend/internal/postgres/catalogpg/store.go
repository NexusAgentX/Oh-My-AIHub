// Package catalogpg is the PostgreSQL implementation of catalog.Store. It also
// exports PriceTiersByModel so other domains can attach a model's conditional
// price tiers to their own read models inside their own transaction.
package catalogpg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: New(pool)}
}

func toModel(m Model) catalog.Model {
	return catalog.Model{
		ID:                       m.ID,
		Name:                     m.Name,
		Provider:                 m.Provider,
		ContextWindow:            m.ContextWindow,
		ParameterInfo:            m.ParameterInfo,
		InputModalities:          m.InputModalities,
		OutputModalities:         m.OutputModalities,
		SupportsTools:            m.SupportsTools,
		SupportsStructuredOutput: m.SupportsStructuredOutput,
		SupportsVision:           m.SupportsVision,
		InputPrice:               m.InputPriceNanoPerMillion,
		OutputPrice:              m.OutputPriceNanoPerMillion,
		CacheWritePrice:          m.CacheWritePriceNanoPerMillion,
		CacheReadPrice:           m.CacheReadPriceNanoPerMillion,
		Status:                   catalog.Status(m.Status),
		Version:                  m.Version,
		CreatedAt:                m.CreatedAt,
		UpdatedAt:                m.UpdatedAt,
		PriceUpdatedAt:           m.PriceUpdatedAt,
	}
}

func toPriceTier(t ModelPriceTier) ledger.PriceTier {
	tier := ledger.PriceTier{
		Name:            t.Name,
		MinPromptTokens: t.MinPromptTokens,
		MaxPromptTokens: t.MaxPromptTokens,
		Timezone:        t.Timezone,
		StartMinute:     t.StartMinuteOfDay,
		EndMinute:       t.EndMinuteOfDay,
		InputPrice:      t.InputPriceNanoPerMillion,
		OutputPrice:     t.OutputPriceNanoPerMillion,
		CacheWritePrice: t.CacheWritePriceNanoPerMillion,
		CacheReadPrice:  t.CacheReadPriceNanoPerMillion,
	}
	if len(t.Weekdays) > 0 {
		tier.Weekdays = make([]int, len(t.Weekdays))
		for index, weekday := range t.Weekdays {
			tier.Weekdays[index] = int(weekday)
		}
	}
	return tier
}

// PriceTiersByModel loads the conditional price tiers of the given models,
// keyed by model id. Rows arrive ordered by (model_id, seq), so each slice keeps
// the stored tier sequence. db may be a pool or a transaction.
func PriceTiersByModel(ctx context.Context, db DBTX, modelIDs []string) (map[string][]ledger.PriceTier, error) {
	result := make(map[string][]ledger.PriceTier)
	if len(modelIDs) == 0 {
		return result, nil
	}
	rows, err := New(db).ListModelPriceTiers(ctx, modelIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ModelID] = append(result[row.ModelID], toPriceTier(row))
	}
	return result, nil
}

func attachPriceTiers(ctx context.Context, db DBTX, models []catalog.Model) ([]catalog.Model, error) {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	tiers, err := PriceTiersByModel(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	for index := range models {
		models[index].PriceTiers = tiers[models[index].ID]
	}
	return models, nil
}

func insertPriceTiers(ctx context.Context, q *Queries, modelID string, tiers []ledger.PriceTier) error {
	for index, tier := range tiers {
		var weekdays []int16
		for _, weekday := range tier.Weekdays {
			weekdays = append(weekdays, int16(weekday))
		}
		if err := q.InsertModelPriceTier(ctx, InsertModelPriceTierParams{
			ModelID:                       modelID,
			Seq:                           int32(index + 1),
			Name:                          tier.Name,
			MinPromptTokens:               tier.MinPromptTokens,
			MaxPromptTokens:               tier.MaxPromptTokens,
			Timezone:                      tier.Timezone,
			Weekdays:                      weekdays,
			StartMinuteOfDay:              tier.StartMinute,
			EndMinuteOfDay:                tier.EndMinute,
			InputPriceNanoPerMillion:      tier.InputPrice,
			OutputPriceNanoPerMillion:     tier.OutputPrice,
			CacheWritePriceNanoPerMillion: tier.CacheWritePrice,
			CacheReadPriceNanoPerMillion:  tier.CacheReadPrice,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListModels(ctx context.Context, includeDisabled bool, query string) ([]catalog.Model, error) {
	rows, err := s.q.ListModels(ctx, ListModelsParams{IncludeDisabled: includeDisabled, Query: query})
	if err != nil {
		return nil, err
	}
	models := make([]catalog.Model, 0, len(rows))
	for _, row := range rows {
		models = append(models, toModel(row))
	}
	return attachPriceTiers(ctx, s.pool, models)
}

func (s *Store) GetModel(ctx context.Context, id string, includeDisabled bool) (catalog.Model, error) {
	row, err := s.q.GetModel(ctx, GetModelParams{ID: id, IncludeDisabled: includeDisabled})
	if err != nil {
		return catalog.Model{}, mapError(err)
	}
	withTiers, err := attachPriceTiers(ctx, s.pool, []catalog.Model{toModel(row)})
	if err != nil {
		return catalog.Model{}, err
	}
	return withTiers[0], nil
}

func (s *Store) CreateModel(ctx context.Context, actorID string, model catalog.Model) (catalog.Model, error) {
	var created catalog.Model
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		row, err := q.CreateModel(ctx, CreateModelParams{
			ID:                            model.ID,
			Name:                          model.Name,
			Provider:                      model.Provider,
			ContextWindow:                 model.ContextWindow,
			ParameterInfo:                 model.ParameterInfo,
			InputModalities:               model.InputModalities,
			OutputModalities:              model.OutputModalities,
			SupportsTools:                 model.SupportsTools,
			SupportsStructuredOutput:      model.SupportsStructuredOutput,
			SupportsVision:                model.SupportsVision,
			InputPriceNanoPerMillion:      model.InputPrice,
			OutputPriceNanoPerMillion:     model.OutputPrice,
			CacheWritePriceNanoPerMillion: model.CacheWritePrice,
			CacheReadPriceNanoPerMillion:  model.CacheReadPrice,
			Status:                        string(model.Status),
		})
		if err != nil {
			// The original insert path returned raw errors; keep duplicates
			// surfacing as the catalog conflict through mapError below.
			return mapError(err)
		}
		created = toModel(row)
		if err := insertPriceTiers(ctx, q, created.ID, model.PriceTiers); err != nil {
			return mapError(err)
		}
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: actorID, Action: "model.created", TargetType: "model", TargetID: created.ID,
			Reason: "administrator created catalog model", Details: auditDetails(created, model.PriceTiers),
		})
	})
	if err != nil {
		return catalog.Model{}, err
	}
	created.PriceTiers = model.PriceTiers
	return created, nil
}

func (s *Store) UpdateModel(ctx context.Context, actorID, id string, expectedVersion int64, model catalog.Model) (catalog.Model, error) {
	var updated catalog.Model
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		existingTiers, err := PriceTiersByModel(ctx, tx, []string{id})
		if err != nil {
			return err
		}
		row, err := q.UpdateModel(ctx, UpdateModelParams{
			ID:                            id,
			ExpectedVersion:               expectedVersion,
			Name:                          model.Name,
			Provider:                      model.Provider,
			ContextWindow:                 model.ContextWindow,
			ParameterInfo:                 model.ParameterInfo,
			InputModalities:               model.InputModalities,
			OutputModalities:              model.OutputModalities,
			SupportsTools:                 model.SupportsTools,
			SupportsStructuredOutput:      model.SupportsStructuredOutput,
			SupportsVision:                model.SupportsVision,
			InputPriceNanoPerMillion:      model.InputPrice,
			OutputPriceNanoPerMillion:     model.OutputPrice,
			CacheWritePriceNanoPerMillion: model.CacheWritePrice,
			CacheReadPriceNanoPerMillion:  model.CacheReadPrice,
			Status:                        string(model.Status),
			PriceTiersChanged:             !PriceTiersEqual(existingTiers[id], model.PriceTiers),
		})
		if err != nil {
			err = mapError(err)
			if errors.Is(err, catalog.ErrNotFound) {
				exists, queryErr := q.ModelExists(ctx, id)
				if queryErr != nil {
					return queryErr
				}
				if exists {
					return catalog.ErrConflict
				}
			}
			return err
		}
		updated = toModel(row)
		if err := q.DeleteModelPriceTiers(ctx, id); err != nil {
			return mapError(err)
		}
		if err := insertPriceTiers(ctx, q, id, model.PriceTiers); err != nil {
			return mapError(err)
		}
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: actorID, Action: "model.updated", TargetType: "model", TargetID: id,
			Reason: "administrator updated catalog model", Details: auditDetails(updated, model.PriceTiers),
		})
	})
	if err != nil {
		return catalog.Model{}, err
	}
	updated.PriceTiers = model.PriceTiers
	return updated, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return catalog.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return catalog.ErrConflict
	}
	return fmt.Errorf("catalog store: %w", err)
}

var _ catalog.Store = (*Store)(nil)
