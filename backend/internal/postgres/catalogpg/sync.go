package catalogpg

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalogsync"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
	"github.com/jackc/pgx/v5"
)

func sources(ctx context.Context, db DBTX, models []catalog.Model) error {
	ids := []string{}
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	rows, err := New(db).ListSources(ctx, ids)
	if err != nil {
		return err
	}
	byID := map[string]*catalog.SourceInfo{}
	for _, row := range rows {
		var info catalog.SourceInfo
		if err := json.Unmarshal(row.Info, &info); err != nil {
			return err
		}
		info.SyncEnabled = row.SyncEnabled
		byID[row.ModelID] = &info
	}
	for i := range models {
		models[i].Source = byID[models[i].ID]
	}
	return nil
}
func (s *Store) SyncStatus(ctx context.Context) (catalogsync.Status, error) {
	row, err := s.q.GetSyncStatus(ctx)
	if err != nil {
		return catalogsync.Status{}, err
	}
	x := catalogsync.Status{ExchangeRate: row.ExchangeRate, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, Status: row.Status, Error: row.Error}
	err = json.Unmarshal(row.Result, &x.Result)
	return x, err
}
func (s *Store) SetSyncRate(ctx context.Context, actor, rate string) error {
	return pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		old, err := q.LockSyncRate(ctx)
		if err != nil {
			return err
		}
		if old == rate {
			return nil
		}
		if err = q.SetSyncRate(ctx, rate); err != nil {
			return err
		}
		return auditpg.Record(ctx, tx, auditpg.Event{ActorID: actor, Action: "catalog.sync.rate_updated", TargetType: "catalog_sync", TargetID: "bifrost", Detail: map[string]any{"before": old, "after": rate}})
	})
}
func (s *Store) SourceRaw(ctx context.Context, id string) (json.RawMessage, error) {
	raw, err := s.q.GetSourceRaw(ctx, id)
	return raw, mapError(err)
}

func (s *Store) RunSync(ctx context.Context, fetch func(string) ([]catalogsync.Entry, error)) (resultErr error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	cq := New(conn)
	locked, err := cq.TrySyncLock(ctx)
	if err != nil {
		return err
	}
	if !locked {
		return catalogsync.ErrBusy
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = cq.ReleaseSyncLock(cleanup)
	}()
	rate, err := cq.StartSync(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = cq.FailSync(cleanup, resultErr.Error())
		}
	}()
	entries, err := fetch(rate)
	if err != nil {
		return err
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := New(tx)
	currentRate, err := q.LockSyncRate(ctx)
	if err != nil {
		return err
	}
	if currentRate != rate {
		return errors.New("换算率在抓取期间改变，请重新同步")
	}
	counts := map[string]int{"received": len(entries), "created": 0, "updated": 0, "unchanged": 0, "needs_review": 0, "skipped": 0, "conflict": 0}
	seen := []string{}
	now := time.Now().UTC()
	for _, entry := range entries {
		seen = append(seen, entry.Key)
		if err = q.LockModelWrites(ctx, entry.Model.ID); err != nil {
			return err
		}
		sourceRow, err := q.GetSource(ctx, entry.Key)
		oldInfoRaw, modelID, ignored, enabled := sourceRow.Info, sourceRow.ModelID, sourceRow.Ignored, sourceRow.SyncEnabled
		existing := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if !existing {
			modelID = entry.Model.ID
			enabled = true
		}
		if ignored {
			var previous catalog.SourceInfo
			_ = json.Unmarshal(oldInfoRaw, &previous)
			if previous.Status == "conflict" {
				counts["conflict"]++
			}
			counts["skipped"]++
			continue
		}
		row, err := q.LockModel(ctx, modelID)
		hasModel := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if hasModel && !existing {
			counts["conflict"]++
			infoRaw, _ := json.Marshal(catalog.SourceInfo{Key: entry.Key, Status: "conflict", Problems: []string{"本地已有同名模型，不自动接管"}, Warnings: []string{}, Metadata: map[string]json.RawMessage{}, LastSeenAt: now})
			if err = q.InsertSourceConflict(ctx, InsertSourceConflictParams{SourceKey: entry.Key, ModelID: modelID, RawRecord: entry.Raw, Info: infoRaw, SeenAt: now}); err != nil {
				return err
			}
			continue
		}
		if existing && !enabled {
			err = q.SeenSource(ctx, SeenSourceParams{SourceKey: entry.Key, SeenAt: now})
			if err != nil {
				return err
			}
			counts["skipped"]++
			continue
		}
		info := catalog.SourceInfo{Key: entry.Key, SyncEnabled: true, Status: "ready", Problems: entry.Problems, Warnings: entry.Warnings, Metadata: map[string]json.RawMessage{}, LastSeenAt: now}
		var rawMap catalogsync.Record
		_ = json.Unmarshal(entry.Raw, &rawMap)
		for k, v := range rawMap {
			if k == "provider" || k == "base_model" || k == "mode" || k == "source" || k == "max_input_tokens" || k == "max_output_tokens" || k == "is_deprecated" || k == "deprecation_date" || len(k) > 9 && k[:9] == "supports_" {
				info.Metadata[k] = v
			}
		}
		var oldInfo catalog.SourceInfo
		if existing {
			if err = json.Unmarshal(oldInfoRaw, &oldInfo); err != nil {
				return err
			}
			info.LastAppliedAt = oldInfo.LastAppliedAt
			info.AppliedRate = oldInfo.AppliedRate
			info.PriceReady = oldInfo.PriceReady
		}
		next := entry.Model
		var before catalog.Model
		if hasModel {
			models, err := withTiers(ctx, q, []Model{row})
			if err != nil {
				return err
			}
			before = models[0]
			next.ID = before.ID
			next.Enabled = before.Enabled
			next.SortOrder = before.SortOrder
			next.ParameterInfo = before.ParameterInfo
		}
		valid := len(entry.Problems) == 0
		if !valid {
			info.Status = "needs_review"
			if rate == "" {
				info.Status = "waiting_rate"
			}
			counts["needs_review"]++
			if hasModel {
				next = before
			} else {
				next.InputPrice = 0
				next.OutputPrice = 0
				next.CacheWritePrice = 0
				next.CacheReadPrice = 0
				next.TokenPrices = nil
				next.PriceTiers = nil
				next.Enabled = false
				next = catalog.Normalize(next)
			}
		} else {
			info.PriceReady = true
			info.AppliedRate = rate
			info.LastAppliedAt = &now
		}
		next.Source = nil
		before.Source = nil
		changed := !hasModel || !sameManaged(before, next)
		if !hasModel {
			row, err = q.InsertModel(ctx, insertParams(next))
			if err != nil {
				return err
			}
			counts["created"]++
		} else if changed {
			row, err = q.UpdateModel(ctx, updateParams(next))
			if err != nil {
				return err
			}
			counts["updated"]++
		} else {
			counts["unchanged"]++
			if oldInfo.AppliedRate == rate {
				info.LastAppliedAt = oldInfo.LastAppliedAt
			}
		}
		if changed {
			if err = replaceTiers(ctx, q, row.ID, next.PriceTiers); err != nil {
				return err
			}
			if err = auditpg.Record(ctx, tx, auditpg.Event{Action: "model.synced", TargetType: "model", TargetID: row.ID, Detail: map[string]any{"source_key": entry.Key, "exchange_rate": rate, "before": auditView(before), "after": auditView(next)}}); err != nil {
				return err
			}
		}
		infoRaw, _ := json.Marshal(info)
		err = q.UpsertSource(ctx, UpsertSourceParams{SourceKey: entry.Key, ModelID: modelID, RawRecord: entry.Raw, Info: infoRaw, SeenAt: now})
		if err != nil {
			return err
		}
	}
	if err = q.MarkMissingSources(ctx, seen); err != nil {
		return err
	}
	summary, _ := json.Marshal(counts)
	if err = q.FinishSync(ctx, summary); err != nil {
		return err
	}
	if err = auditpg.Record(ctx, tx, auditpg.Event{Action: "catalog.sync.completed", TargetType: "catalog_sync", TargetID: "bifrost", Detail: map[string]any{"exchange_rate": rate, "result": counts}}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func sameManaged(a, b catalog.Model) bool {
	a.Source = nil
	b.Source = nil
	a.CreatedAt = time.Time{}
	b.CreatedAt = time.Time{}
	a.UpdatedAt = time.Time{}
	b.UpdatedAt = time.Time{}
	return reflect.DeepEqual(a, b)
}
func insertParams(m catalog.Model) InsertModelParams {
	return InsertModelParams{ID: m.ID, DisplayName: m.DisplayName, InputPriceNanoPerMillion: m.InputPrice, OutputPriceNanoPerMillion: m.OutputPrice, CacheWritePriceNanoPerMillion: m.CacheWritePrice, CacheReadPriceNanoPerMillion: m.CacheReadPrice, TokenPrices: encodePrices(m.TokenPrices), Enabled: m.Enabled, SortOrder: m.SortOrder, Provider: m.Provider, ContextWindow: m.ContextWindow, InputModalities: m.InputModalities, OutputModalities: m.OutputModalities, SupportsTools: m.SupportsTools, SupportsStructuredOutput: m.SupportsStructuredOutput, SupportsVision: m.SupportsVision, ParameterInfo: m.ParameterInfo}
}
func updateParams(m catalog.Model) UpdateModelParams { return UpdateModelParams(insertParams(m)) }
