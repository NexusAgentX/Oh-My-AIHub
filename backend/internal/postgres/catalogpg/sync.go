package catalogpg

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
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
	x := catalogsync.Status{Providers: row.Providers, ProvidersConfigured: row.ProvidersConfigured, AvailableProviders: row.AvailableProviders, ExchangeRate: row.ExchangeRate, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, Status: row.Status, Error: row.Error}
	if err = json.Unmarshal(row.Report, &x.Report); err != nil {
		return x, err
	}
	err = json.Unmarshal(row.Result, &x.Result)
	return x, err
}
func (s *Store) SetSyncConfig(ctx context.Context, actor, rate string, providers []string) error {
	if providers == nil {
		providers = []string{}
	}
	return pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		old, err := q.LockSyncConfig(ctx)
		if err != nil {
			return err
		}
		if old.ProvidersConfigured && old.ExchangeRate == rate && reflect.DeepEqual(old.Providers, providers) {
			return nil
		}
		if err = q.SetSyncConfig(ctx, SetSyncConfigParams{ExchangeRate: rate, Providers: providers}); err != nil {
			return err
		}
		return auditpg.Record(ctx, tx, auditpg.Event{ActorID: actor, Action: "catalog.sync.config_updated", TargetType: "catalog_sync", TargetID: "bifrost", Detail: map[string]any{"before_rate": old.ExchangeRate, "after_rate": rate, "before_providers": old.Providers, "after_providers": providers}})
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
	config, err := cq.StartSync(ctx)
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
	rate := config.ExchangeRate
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
	currentConfig, err := q.LockSyncConfig(ctx)
	if err != nil {
		return err
	}
	if currentConfig.ConfigVersion != config.ConfigVersion {
		return errors.New("同步配置在抓取期间改变，请重新同步")
	}

	selected, report, options := catalogsync.Select(entries, config.Providers)
	counts := map[string]int{"received": len(entries), "selected": len(selected), "shadowed": len(report), "created": 0, "updated": 0, "unchanged": 0, "needs_review": 0, "skipped": 0, "conflict": 0, "removed": 0, "retained": 0}
	finish := func() error {
		summary, _ := json.Marshal(counts)
		notices, _ := json.Marshal(report)
		if err := q.FinishSync(ctx, FinishSyncParams{Result: summary, Report: notices, AvailableProviders: options}); err != nil {
			return err
		}
		if err := auditpg.Record(ctx, tx, auditpg.Event{Action: "catalog.sync.completed", TargetType: "catalog_sync", TargetID: "bifrost", Detail: map[string]any{"exchange_rate": rate, "providers": config.Providers, "result": counts}}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if !config.ProvidersConfigured {
		counts["awaiting_configuration"] = 1
		return finish()
	}
	// Explicit non-FK references must not appear between inspection and deletion.
	// Acquire these table locks before any model locks to keep writer lock order stable.
	if err = q.LockExplicitModelReferences(ctx); err != nil {
		return err
	}
	if err = q.RemoveOldConflictRecords(ctx); err != nil {
		return err
	}
	sourceKeys := map[string]bool{}
	for _, entry := range entries {
		sourceKeys[entry.Key] = true
	}
	selectedProviders := map[string]bool{}
	for _, provider := range config.Providers {
		selectedProviders[provider] = true
	}
	desired := map[string]bool{}
	for _, entry := range selected {
		desired[entry.Model.ID] = true
	}
	managed, err := q.ListManagedSources(ctx)
	if err != nil {
		return err
	}
	for _, source := range managed {
		if desired[source.ModelID] {
			continue
		}
		if err = q.LockModelWrites(ctx, source.ModelID); err != nil {
			return err
		}
		if _, err = q.LockModel(ctx, source.ModelID); err != nil {
			return err
		}
		// An administrator may have opted out while this transaction waited for a model.
		current, err := q.GetSource(ctx, source.ModelID)
		if err != nil {
			return err
		}
		reason := ""
		retainedStatus := "retained"
		var prior catalog.SourceInfo
		if err = json.Unmarshal(current.Info, &prior); err != nil {
			return err
		}
		var provider string
		_ = json.Unmarshal(prior.Metadata["provider"], &provider)
		if !sourceKeys[source.SourceKey] && selectedProviders[provider] {
			reason = "上游资料暂缺，保留最后有效数据"
			retainedStatus = "missing"
		}
		if !current.SyncEnabled {
			reason = "已退出同步"
		} else if reason == "" {
			refs, err := q.ModelReferenceReasons(ctx, source.ModelID)
			if err != nil {
				return err
			}
			parts := []string{}
			if refs.Channels {
				parts = append(parts, "渠道引用")
			}
			if refs.Calls {
				parts = append(parts, "历史调用/账单引用")
			}
			if refs.Routes {
				parts = append(parts, "路由偏好引用")
			}
			if refs.Keys {
				parts = append(parts, "API Key 显式配置引用")
			}
			reason = strings.Join(parts, "、")
		}
		if reason != "" {
			counts["retained"]++
			report = append(report, catalogsync.Notice{ModelID: source.ModelID, SourceKey: source.SourceKey, Reason: "保留旧模型：" + reason})
			if current.SyncEnabled {
				if err = q.SetSourceRetention(ctx, SetSourceRetentionParams{TargetID: source.ModelID, Reason: reason, Status: retainedStatus}); err != nil {
					return err
				}
			}
			continue
		}
		if err = q.RemoveUnusedModel(ctx, source.ModelID); err != nil {
			return err
		}
		if err = q.RemoveSource(ctx, source.ModelID); err != nil {
			return err
		}
		counts["removed"]++
		if err = auditpg.Record(ctx, tx, auditpg.Event{Action: "model.sync_removed", TargetType: "model", TargetID: source.ModelID, Detail: map[string]any{"source_key": source.SourceKey, "reason": "白名单/名称整理：未使用同步模型"}}); err != nil {
			return err
		}
	}
	deletedIDs, err := q.DeletedModels(ctx)
	if err != nil {
		return err
	}
	deleted := map[string]bool{}
	for _, id := range deletedIDs {
		deleted[id] = true
	}
	now := time.Now().UTC()
	for _, entry := range selected {
		modelID := entry.Model.ID
		if err = q.LockModelWrites(ctx, modelID); err != nil {
			return err
		}
		sourceRow, err := q.GetSource(ctx, modelID)
		existing := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// Recheck tombstones after waiting for a concurrent user deletion.
		if marked, err := q.IsModelDeleted(ctx, modelID); err != nil {
			return err
		} else if marked {
			deleted[modelID] = true
		}
		if deleted[modelID] || sourceRow.Ignored {
			counts["skipped"]++
			report = append(report, catalogsync.Notice{modelID, entry.Key, "用户已删除，不重新创建"})
			continue
		}
		row, err := q.LockModel(ctx, modelID)
		hasModel := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if hasModel && !existing {
			counts["conflict"]++
			report = append(report, catalogsync.Notice{modelID, entry.Key, "已有手工模型，不接管"})
			continue
		}
		if existing && !sourceRow.SyncEnabled {
			counts["skipped"]++
			report = append(report, catalogsync.Notice{modelID, entry.Key, "已退出同步，保留当前模型"})
			continue
		}
		oldInfoRaw := sourceRow.Info
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
			info.AppliedSourceKey = oldInfo.AppliedSourceKey
			if info.AppliedSourceKey == "" {
				info.AppliedSourceKey = oldInfo.Key
			}
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
			info.AppliedSourceKey = entry.Key
			info.AppliedRate = rate
			info.LastAppliedAt = &now
		}
		next.DisplayName = entry.Model.DisplayName
		next.Source = nil
		before.Source = nil
		changed := !hasModel || !sameManaged(before, next) || oldInfo.Key != entry.Key
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
	return finish()
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
