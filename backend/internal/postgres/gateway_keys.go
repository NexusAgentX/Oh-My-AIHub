package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/jackc/pgx/v5"
)

type gatewayQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (s *Store) CreateAPIKey(ctx context.Context, ownerID, displayName, prefix string, hash [32]byte, pools []gateway.PoolInput) (gateway.APIKey, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return gateway.APIKey{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var keyID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO api_keys (owner_account_id, display_name, key_prefix, key_hash)
		SELECT a.id, $2, $3, $4
		FROM accounts a
		WHERE a.id = $1 AND a.status = 'active' AND NOT a.must_change_password
		RETURNING id::text`, ownerID, displayName, prefix, hash[:]).Scan(&keyID); err != nil {
		return gateway.APIKey{}, mapGatewayError(err)
	}
	if err := replaceAPIPools(ctx, tx, keyID, pools, nil); err != nil {
		return gateway.APIKey{}, err
	}
	if err := insertAudit(ctx, tx, ownerID, "api_key.created", "api_key", keyID, "account holder created platform api key", map[string]any{
		"key_prefix": prefix, "pool_count": len(pools),
	}); err != nil {
		return gateway.APIKey{}, err
	}
	created, err := loadAPIKey(ctx, tx, ownerID, keyID)
	if err != nil {
		return gateway.APIKey{}, err
	}
	if commitErr := s.commitGatewayTransaction(ctx, tx, "api_key.create", keyID); commitErr != nil {
		if recovered, recoverErr := s.recoverCreatedAPIKey(ctx, ownerID, keyID, displayName, prefix, hash); recoverErr == nil {
			return recovered, nil
		}
		return gateway.APIKey{}, commitErr
	}
	return created, nil
}

func (s *Store) ListAPIKeys(ctx context.Context, ownerID string) ([]gateway.APIKey, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text FROM api_keys
		WHERE owner_account_id = $1 AND status <> 'deleted'
		ORDER BY updated_at DESC, id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := make([]gateway.APIKey, 0, len(ids))
	for _, id := range ids {
		item, err := loadAPIKey(ctx, s.pool, ownerID, id)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) GetAPIKey(ctx context.Context, ownerID, keyID string) (gateway.APIKey, error) {
	return loadAPIKey(ctx, s.pool, ownerID, keyID)
}

func (s *Store) UpdateAPIKey(ctx context.Context, ownerID, keyID string, expectedVersion int64, input gateway.KeyConfigInput) (gateway.APIKey, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return gateway.APIKey{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var currentVersion int64
	var status string
	if err := tx.QueryRow(ctx, `SELECT version, status FROM api_keys WHERE id = $1 AND owner_account_id = $2 FOR UPDATE`, keyID, ownerID).Scan(&currentVersion, &status); err != nil {
		return gateway.APIKey{}, mapGatewayError(err)
	}
	if currentVersion != expectedVersion {
		return gateway.APIKey{}, gateway.ErrConflict
	}
	if status == string(gateway.KeyDeleted) {
		return gateway.APIKey{}, gateway.ErrNotFound
	}
	existing, err := existingPoolOffers(ctx, tx, keyID)
	if err != nil {
		return gateway.APIKey{}, err
	}
	if err := replaceAPIPools(ctx, tx, keyID, input.Pools, existing); err != nil {
		return gateway.APIKey{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE api_keys SET display_name = $3, version = version + 1, updated_at = now()
		WHERE id = $1 AND owner_account_id = $2`, keyID, ownerID, input.DisplayName); err != nil {
		return gateway.APIKey{}, err
	}
	if err := insertAudit(ctx, tx, ownerID, "api_key.configured", "api_key", keyID, "account holder updated api key configuration", map[string]any{
		"pool_count": len(input.Pools), "previous_version": currentVersion,
	}); err != nil {
		return gateway.APIKey{}, err
	}
	updated, err := loadAPIKey(ctx, tx, ownerID, keyID)
	if err != nil {
		return gateway.APIKey{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return gateway.APIKey{}, err
	}
	return updated, nil
}

func (s *Store) RotateAPIKey(ctx context.Context, ownerID, keyID string, expectedVersion int64, prefix string, hash [32]byte) (gateway.APIKey, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return gateway.APIKey{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	result, err := tx.Exec(ctx, `
		UPDATE api_keys
		SET key_prefix = $4, key_hash = $5, generation = generation + 1,
			version = version + 1, updated_at = now()
		WHERE id = $1 AND owner_account_id = $2 AND version = $3 AND status <> 'deleted'`, keyID, ownerID, expectedVersion, prefix, hash[:])
	if err != nil {
		return gateway.APIKey{}, mapGatewayError(err)
	}
	if result.RowsAffected() != 1 {
		return gateway.APIKey{}, gateway.ErrConflict
	}
	if err := insertAudit(ctx, tx, ownerID, "api_key.rotated", "api_key", keyID, "account holder rotated platform api key", map[string]any{
		"key_prefix": prefix, "previous_version": expectedVersion,
	}); err != nil {
		return gateway.APIKey{}, err
	}
	updated, err := loadAPIKey(ctx, tx, ownerID, keyID)
	if err != nil {
		return gateway.APIKey{}, err
	}
	if commitErr := s.commitGatewayTransaction(ctx, tx, "api_key.rotate", keyID); commitErr != nil {
		if recovered, recoverErr := s.recoverRotatedAPIKey(ctx, ownerID, keyID, expectedVersion, prefix, hash); recoverErr == nil {
			return recovered, nil
		}
		return gateway.APIKey{}, commitErr
	}
	return updated, nil
}

func (s *Store) recoverCreatedAPIKey(parent context.Context, ownerID, keyID, displayName, prefix string, hash [32]byte) (gateway.APIKey, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), gateway.PersistenceTimeout)
	defer cancel()
	var matched bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM api_keys
			WHERE id = $1 AND owner_account_id = $2 AND display_name = $3
				AND key_prefix = $4 AND key_hash = $5 AND generation = 1 AND version = 1
		)`, keyID, ownerID, displayName, prefix, hash[:]).Scan(&matched); err != nil {
		return gateway.APIKey{}, err
	}
	if !matched {
		return gateway.APIKey{}, gateway.ErrConflict
	}
	return loadAPIKey(ctx, s.pool, ownerID, keyID)
}

func (s *Store) recoverRotatedAPIKey(parent context.Context, ownerID, keyID string, expectedVersion int64, prefix string, hash [32]byte) (gateway.APIKey, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), gateway.PersistenceTimeout)
	defer cancel()
	var matched bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM api_keys
			WHERE id = $1 AND owner_account_id = $2 AND key_prefix = $3 AND key_hash = $4
				AND version = $5 + 1 AND generation > 1 AND status <> 'deleted'
		)`, keyID, ownerID, prefix, hash[:], expectedVersion).Scan(&matched); err != nil {
		return gateway.APIKey{}, err
	}
	if !matched {
		return gateway.APIKey{}, gateway.ErrConflict
	}
	return loadAPIKey(ctx, s.pool, ownerID, keyID)
}

func (s *Store) SetAPIKeyStatus(ctx context.Context, ownerID, keyID string, expectedVersion int64, target gateway.KeyStatus) (gateway.APIKey, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return gateway.APIKey{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var current string
	if err := tx.QueryRow(ctx, `SELECT status FROM api_keys WHERE id = $1 AND owner_account_id = $2 AND version = $3 FOR UPDATE`, keyID, ownerID, expectedVersion).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gateway.APIKey{}, gateway.ErrConflict
		}
		return gateway.APIKey{}, mapGatewayError(err)
	}
	if current == string(gateway.KeyDeleted) || (target == gateway.KeyActive && current != string(gateway.KeyDisabled)) || (target == gateway.KeyDisabled && current != string(gateway.KeyActive)) {
		return gateway.APIKey{}, gateway.ErrConflict
	}
	result, err := tx.Exec(ctx, `
		UPDATE api_keys
		SET status = $4, version = version + 1, updated_at = now(),
			deleted_at = CASE WHEN $4 = 'deleted' THEN now() ELSE NULL END
		WHERE id = $1 AND owner_account_id = $2 AND version = $3`, keyID, ownerID, expectedVersion, target)
	if err != nil {
		return gateway.APIKey{}, mapGatewayError(err)
	}
	if result.RowsAffected() != 1 {
		return gateway.APIKey{}, gateway.ErrConflict
	}
	if err := insertAudit(ctx, tx, ownerID, "api_key.status_changed", "api_key", keyID, "account holder changed platform api key status", map[string]any{
		"from": current, "to": target, "previous_version": expectedVersion,
	}); err != nil {
		return gateway.APIKey{}, err
	}
	updated, err := loadAPIKey(ctx, tx, ownerID, keyID)
	if target == gateway.KeyDeleted {
		// Deleted keys remain historical facts but disappear from normal owner reads.
		updated = gateway.APIKey{ID: keyID, OwnerAccountID: ownerID, Status: gateway.KeyDeleted, Version: expectedVersion + 1}
		err = nil
	}
	if err != nil {
		return gateway.APIKey{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return gateway.APIKey{}, err
	}
	return updated, nil
}

func (s *Store) AuthenticateAPIKey(ctx context.Context, hash [32]byte) (gateway.AuthenticatedKey, error) {
	var authenticated gateway.AuthenticatedKey
	var storedHash []byte
	err := s.pool.QueryRow(ctx, `
		SELECT k.id::text, k.owner_account_id::text, k.generation, k.key_hash
		FROM api_keys k
		JOIN accounts a ON a.id = k.owner_account_id
		WHERE k.key_hash = $1 AND k.status = 'active'
			AND a.status = 'active' AND NOT a.must_change_password`, hash[:]).Scan(
		&authenticated.ID, &authenticated.OwnerAccountID, &authenticated.Generation, &storedHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return gateway.AuthenticatedKey{}, gateway.ErrInvalidAPIKey
	}
	if err != nil {
		return gateway.AuthenticatedKey{}, err
	}
	copy(authenticated.Hash[:], storedHash)
	return authenticated, nil
}

func replaceAPIPools(ctx context.Context, tx pgx.Tx, keyID string, inputs []gateway.PoolInput, existingOffers map[string]int64) error {
	keepPools := make([]string, 0, len(inputs))
	for _, input := range inputs {
		statuses, targets, err := resolveRoutingTargets(ctx, tx, input.OfferIDs)
		if err != nil {
			return err
		}
		if len(statuses) != len(input.OfferIDs) {
			return gateway.ErrInvalidInput
		}
		resolvedValidationVersions := make(map[string]int64, len(targets))
		for _, target := range targets {
			resolvedValidationVersions[target.Lease.OfferID] = target.Lease.ValidationVersion
		}
		for _, status := range statuses {
			_, alreadyPresent := existingOffers[status.OfferID]
			if status.ModelID != input.CanonicalModelID || status.Protocol != input.Protocol || (!status.Eligible && !alreadyPresent) {
				return gateway.ErrInvalidInput
			}
		}
		var poolID string
		err = tx.QueryRow(ctx, `
			SELECT id::text FROM api_model_pools
			WHERE api_key_id = $1 AND canonical_model_id = $2 AND protocol = $3 AND status = 'active'
			FOR UPDATE`, keyID, input.CanonicalModelID, input.Protocol).Scan(&poolID)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `
				INSERT INTO api_model_pools (api_key_id, canonical_model_id, protocol)
				VALUES ($1, $2, $3) RETURNING id::text`, keyID, input.CanonicalModelID, input.Protocol).Scan(&poolID)
		} else if err == nil {
			_, err = tx.Exec(ctx, `UPDATE api_model_pools SET version = version + 1, updated_at = now() WHERE id = $1`, poolID)
		}
		if err != nil {
			return mapGatewayError(err)
		}
		keepPools = append(keepPools, poolID)
		if _, err := tx.Exec(ctx, `DELETE FROM api_pool_members WHERE pool_id = $1`, poolID); err != nil {
			return err
		}
		for index, offerID := range input.OfferIDs {
			validationVersion, alreadyPresent := existingOffers[offerID]
			if !alreadyPresent {
				var resolved bool
				validationVersion, resolved = resolvedValidationVersions[offerID]
				if !resolved {
					return gateway.ErrInvalidInput
				}
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO api_pool_members (pool_id, offer_id, priority, added_validation_version)
				VALUES ($1, $2, $3, $4)`, poolID, offerID, index+1, validationVersion); err != nil {
				return mapGatewayError(err)
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE api_model_pools SET status = 'deleted', deleted_at = now(), version = version + 1, updated_at = now()
		WHERE api_key_id = $1 AND status = 'active' AND NOT (id = ANY($2::uuid[]))`, keyID, keepPools); err != nil {
		return err
	}
	return nil
}

func existingPoolOffers(ctx context.Context, queryer gatewayQueryer, keyID string) (map[string]int64, error) {
	rows, err := queryer.Query(ctx, `
		SELECT member.offer_id::text, member.added_validation_version
		FROM api_pool_members member
		JOIN api_model_pools pool ON pool.id = member.pool_id
		WHERE pool.api_key_id = $1 AND pool.status = 'active'`, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]int64)
	for rows.Next() {
		var id string
		var validationVersion int64
		if err := rows.Scan(&id, &validationVersion); err != nil {
			return nil, err
		}
		result[id] = validationVersion
	}
	return result, rows.Err()
}

func loadAPIKey(ctx context.Context, queryer gatewayQueryer, ownerID, keyID string) (gateway.APIKey, error) {
	var result gateway.APIKey
	var status string
	var lastUsed sql.NullTime
	err := queryer.QueryRow(ctx, `
		SELECT id::text, owner_account_id::text, display_name, key_prefix, generation, status, version,
			last_used_at, created_at, updated_at
		FROM api_keys
		WHERE id = $1 AND owner_account_id = $2 AND status <> 'deleted'`, keyID, ownerID).Scan(
		&result.ID, &result.OwnerAccountID, &result.DisplayName, &result.Prefix, &result.Generation, &status, &result.Version,
		&lastUsed, &result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return gateway.APIKey{}, mapGatewayError(err)
	}
	result.Status = gateway.KeyStatus(status)
	if lastUsed.Valid {
		value := lastUsed.Time
		result.LastUsedAt = &value
	}
	rows, err := queryer.Query(ctx, `
		SELECT pool.id::text, pool.canonical_model_id, model.name, pool.protocol, pool.version,
			pool.created_at, pool.updated_at
		FROM api_model_pools pool
		JOIN models model ON model.id = pool.canonical_model_id
		WHERE pool.api_key_id = $1 AND pool.status = 'active'
		ORDER BY pool.created_at, pool.id`, keyID)
	if err != nil {
		return gateway.APIKey{}, err
	}
	pools := make([]gateway.ModelPool, 0)
	for rows.Next() {
		var pool gateway.ModelPool
		var protocol string
		if err := rows.Scan(&pool.ID, &pool.CanonicalModelID, &pool.ModelName, &protocol, &pool.Version, &pool.CreatedAt, &pool.UpdatedAt); err != nil {
			return gateway.APIKey{}, err
		}
		pool.Protocol = channel.Protocol(protocol)
		pools = append(pools, pool)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return gateway.APIKey{}, err
	}
	rows.Close()
	for index := range pools {
		members, err := loadPoolMembers(ctx, queryer, pools[index].ID)
		if err != nil {
			return gateway.APIKey{}, err
		}
		pools[index].Members = members
	}
	result.Pools = pools
	return result, nil
}

func loadPoolMembers(ctx context.Context, queryer gatewayQueryer, poolID string) ([]gateway.PoolMember, error) {
	rows, err := queryer.Query(ctx, `
		SELECT member.priority, member.offer_id::text, cm.channel_id::text, c.display_name, owner.display_name,
			member.added_validation_version, offer.validation_version, model.context_window,
			cm.model_id, cm.multiplier_nano,
			owner.status, owner.must_change_password, c.status, model.status, offer.status,
			latest.status, credential.channel_id IS NOT NULL,
			CASE WHEN model.input_price_nano_per_million BETWEEN 0 AND 100000000000000
				THEN ceil(model.input_price_nano_per_million::numeric * cm.multiplier_nano::numeric / 1000000000)::bigint END,
			CASE WHEN model.output_price_nano_per_million BETWEEN 0 AND 100000000000000
				THEN ceil(model.output_price_nano_per_million::numeric * cm.multiplier_nano::numeric / 1000000000)::bigint END,
			CASE WHEN model.cache_write_price_nano_per_million BETWEEN 0 AND 100000000000000
				THEN ceil(model.cache_write_price_nano_per_million::numeric * cm.multiplier_nano::numeric / 1000000000)::bigint END,
			CASE WHEN model.cache_read_price_nano_per_million BETWEEN 0 AND 100000000000000
				THEN ceil(model.cache_read_price_nano_per_million::numeric * cm.multiplier_nano::numeric / 1000000000)::bigint END,
			metrics.success_rate, metrics.ttft_milliseconds, metrics.tokens_per_second
		FROM api_pool_members member
		JOIN channel_offers offer ON offer.id = member.offer_id
		JOIN channel_models cm ON cm.id = offer.channel_model_id
		JOIN channels c ON c.id = cm.channel_id
		JOIN accounts owner ON owner.id = c.owner_account_id
		JOIN models model ON model.id = cm.model_id
		LEFT JOIN channel_credentials credential ON credential.channel_id = c.id AND credential.credential_version = c.credential_version
		LEFT JOIN channel_validation_attempts latest ON latest.offer_id = offer.id
			AND latest.validation_version = offer.validation_version AND latest.attempt_seq = offer.validation_attempt_seq
		LEFT JOIN LATERAL (
			SELECT round((count(*) FILTER (WHERE status = 'succeeded'))::numeric /
					NULLIF(count(*) FILTER (WHERE status IN ('succeeded', 'failed', 'cancelled', 'incomplete')), 0), 4)::text AS success_rate,
				round(avg(ttft_milliseconds) FILTER (WHERE status = 'succeeded'))::bigint AS ttft_milliseconds,
				round((avg(tokens_per_second_nano) FILTER (WHERE status = 'succeeded'))::numeric / 1000000000, 3)::text AS tokens_per_second
			FROM api_call_attempts WHERE offer_id = offer.id
		) metrics ON true
		WHERE member.pool_id = $1
		ORDER BY member.priority`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gateway.PoolMember, 0)
	contextWindows := make([]int64, 0)
	for rows.Next() {
		var item gateway.PoolMember
		var contextWindow int64
		var multiplierNano int64
		var ownerStatus, channelStatus, modelStatus, offerStatus string
		var mustChange bool
		var validationStatus sql.NullString
		var credentialConfigured bool
		var inputPrice, outputPrice, cacheWritePrice, cacheReadPrice sql.NullInt64
		var successRate, tokensPerSecond sql.NullString
		var ttft sql.NullInt64
		if err := rows.Scan(
			&item.Priority, &item.OfferID, &item.ChannelID, &item.ChannelDisplayName, &item.OwnerDisplayName,
			&item.AddedValidationVersion, &item.CurrentValidationVersion, &contextWindow,
			&item.ModelID, &multiplierNano,
			&ownerStatus, &mustChange, &channelStatus, &modelStatus, &offerStatus,
			&validationStatus, &credentialConfigured,
			&inputPrice, &outputPrice, &cacheWritePrice, &cacheReadPrice,
			&successRate, &ttft, &tokensPerSecond,
		); err != nil {
			return nil, err
		}
		item.Multiplier = money.FromNano(multiplierNano)
		item.Eligible, item.IneligibleReason = routingEligibility(
			identity.Status(ownerStatus), mustChange, channel.Status(channelStatus), channel.OfferStatus(offerStatus),
			catalog.Status(modelStatus), validationStatus.String, credentialConfigured,
		)
		if item.AddedValidationVersion != item.CurrentValidationVersion {
			item.Eligible = false
			item.IneligibleReason = "validation_version_changed"
		}
		if inputPrice.Valid && outputPrice.Valid && cacheWritePrice.Valid && cacheReadPrice.Valid {
			item.InputPrice, item.OutputPrice = money.FromNano(inputPrice.Int64), money.FromNano(outputPrice.Int64)
			item.CacheWritePrice, item.CacheReadPrice = money.FromNano(cacheWritePrice.Int64), money.FromNano(cacheReadPrice.Int64)
		} else {
			item.Eligible = false
			item.IneligibleReason = "price_unrepresentable"
		}
		if successRate.Valid {
			value := successRate.String
			item.CallSuccessRate = &value
		}
		if ttft.Valid {
			value := ttft.Int64
			item.TTFTMilliseconds = &value
		}
		if tokensPerSecond.Valid {
			value := tokensPerSecond.String
			item.TokensPerSecond = &value
		}
		items = append(items, item)
		contextWindows = append(contextWindows, contextWindow)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) > 0 {
		modelIDs := make([]string, 0, len(items))
		for _, item := range items {
			modelIDs = append(modelIDs, item.ModelID)
		}
		tiers, tierErr := loadModelPriceTiers(ctx, queryer, modelIDs)
		if tierErr != nil {
			return nil, tierErr
		}
		for index := range items {
			items[index].PriceTiers = tiers[items[index].ModelID]
		}
		// The tier-aware bound check runs after tiers are attached so it sees
		// the same facts the routing-time check will see.
		for index := range items {
			if !items[index].Eligible {
				continue
			}
			effectiveTiers, effectiveErr := channel.EffectivePriceTiers(items[index].Multiplier, items[index].PriceTiers)
			if effectiveErr != nil {
				items[index].Eligible = false
				items[index].IneligibleReason = "price_unrepresentable"
				continue
			}
			if _, upperErr := gateway.ConservativeNetDebitUpperBound(channel.RoutingLease{
				ContextWindow: contextWindows[index], Multiplier: money.FromNano(ledger.FixedPointScale),
				InputPrice: items[index].InputPrice, OutputPrice: items[index].OutputPrice,
				CacheWritePrice: items[index].CacheWritePrice, CacheReadPrice: items[index].CacheReadPrice,
				PriceTiers: effectiveTiers,
			}, ledger.FixedPointScale, false); upperErr != nil {
				items[index].Eligible = false
				items[index].IneligibleReason = "price_unrepresentable"
			}
		}
	}
	return items, nil
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
