package gatewaypg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/catalogpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/channelpg"
)

func (s *Store) CreateAPIKey(ctx context.Context, ownerID, displayName, prefix string, hash [32]byte, pools []gateway.PoolInput) (gateway.APIKey, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return gateway.APIKey{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := s.q.WithTx(tx)
	keyID, err := q.InsertAPIKey(ctx, InsertAPIKeyParams{
		OwnerAccountID: ownerID, DisplayName: displayName, KeyPrefix: prefix, KeyHash: hash[:],
	})
	if err != nil {
		return gateway.APIKey{}, mapGatewayError(err)
	}
	if err := replaceAPIPools(ctx, tx, keyID, pools, nil); err != nil {
		return gateway.APIKey{}, err
	}
	if err := audit(ctx, tx, ownerID, "api_key.created", "api_key", keyID, "account holder created platform api key", map[string]any{
		"key_prefix": prefix, "pool_count": len(pools),
	}); err != nil {
		return gateway.APIKey{}, err
	}
	created, err := loadAPIKey(ctx, tx, ownerID, keyID)
	if err != nil {
		return gateway.APIKey{}, err
	}
	if commitErr := s.commit(ctx, tx, "api_key.create", keyID); commitErr != nil {
		if recovered, recoverErr := s.recoverCreatedAPIKey(ctx, ownerID, keyID, displayName, prefix, hash); recoverErr == nil {
			return recovered, nil
		}
		return gateway.APIKey{}, commitErr
	}
	return created, nil
}

func (s *Store) ListAPIKeys(ctx context.Context, ownerID string) ([]gateway.APIKey, error) {
	ids, err := s.q.ListAPIKeyIDs(ctx, ownerID)
	if err != nil {
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
	q := s.q.WithTx(tx)
	locked, err := q.LockAPIKey(ctx, LockAPIKeyParams{ID: keyID, OwnerAccountID: ownerID})
	if err != nil {
		return gateway.APIKey{}, mapGatewayError(err)
	}
	if locked.Version != expectedVersion {
		return gateway.APIKey{}, gateway.ErrConflict
	}
	if locked.Status == string(gateway.KeyDeleted) {
		return gateway.APIKey{}, gateway.ErrNotFound
	}
	existing, err := existingPoolOffers(ctx, q, keyID)
	if err != nil {
		return gateway.APIKey{}, err
	}
	if err := replaceAPIPools(ctx, tx, keyID, input.Pools, existing); err != nil {
		return gateway.APIKey{}, err
	}
	if err := q.RenameAPIKey(ctx, RenameAPIKeyParams{ID: keyID, OwnerAccountID: ownerID, DisplayName: input.DisplayName}); err != nil {
		return gateway.APIKey{}, err
	}
	if err := audit(ctx, tx, ownerID, "api_key.configured", "api_key", keyID, "account holder updated api key configuration", map[string]any{
		"pool_count": len(input.Pools), "previous_version": locked.Version,
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
	affected, err := s.q.WithTx(tx).RotateAPIKey(ctx, RotateAPIKeyParams{
		ID: keyID, OwnerAccountID: ownerID, ExpectedVersion: expectedVersion, KeyPrefix: prefix, KeyHash: hash[:],
	})
	if err != nil {
		return gateway.APIKey{}, mapGatewayError(err)
	}
	if affected != 1 {
		return gateway.APIKey{}, gateway.ErrConflict
	}
	if err := audit(ctx, tx, ownerID, "api_key.rotated", "api_key", keyID, "account holder rotated platform api key", map[string]any{
		"key_prefix": prefix, "previous_version": expectedVersion,
	}); err != nil {
		return gateway.APIKey{}, err
	}
	updated, err := loadAPIKey(ctx, tx, ownerID, keyID)
	if err != nil {
		return gateway.APIKey{}, err
	}
	if commitErr := s.commit(ctx, tx, "api_key.rotate", keyID); commitErr != nil {
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
	matched, err := s.q.CreatedAPIKeyMatches(ctx, CreatedAPIKeyMatchesParams{
		ID: keyID, OwnerAccountID: ownerID, DisplayName: displayName, KeyPrefix: prefix, KeyHash: hash[:],
	})
	if err != nil {
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
	matched, err := s.q.RotatedAPIKeyMatches(ctx, RotatedAPIKeyMatchesParams{
		ID: keyID, OwnerAccountID: ownerID, KeyPrefix: prefix, KeyHash: hash[:], NextVersion: expectedVersion + 1,
	})
	if err != nil {
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
	q := s.q.WithTx(tx)
	current, err := q.LockAPIKeyAtVersion(ctx, LockAPIKeyAtVersionParams{ID: keyID, OwnerAccountID: ownerID, Version: expectedVersion})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gateway.APIKey{}, gateway.ErrConflict
		}
		return gateway.APIKey{}, mapGatewayError(err)
	}
	if current == string(gateway.KeyDeleted) || (target == gateway.KeyActive && current != string(gateway.KeyDisabled)) || (target == gateway.KeyDisabled && current != string(gateway.KeyActive)) {
		return gateway.APIKey{}, gateway.ErrConflict
	}
	affected, err := q.SetAPIKeyStatus(ctx, SetAPIKeyStatusParams{
		ID: keyID, OwnerAccountID: ownerID, ExpectedVersion: expectedVersion, Status: string(target),
	})
	if err != nil {
		return gateway.APIKey{}, mapGatewayError(err)
	}
	if affected != 1 {
		return gateway.APIKey{}, gateway.ErrConflict
	}
	if err := audit(ctx, tx, ownerID, "api_key.status_changed", "api_key", keyID, "account holder changed platform api key status", map[string]any{
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
	row, err := s.q.AuthenticateAPIKey(ctx, hash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return gateway.AuthenticatedKey{}, gateway.ErrInvalidAPIKey
	}
	if err != nil {
		return gateway.AuthenticatedKey{}, err
	}
	authenticated := gateway.AuthenticatedKey{ID: row.ID, OwnerAccountID: row.OwnerAccountID, Generation: row.Generation}
	copy(authenticated.Hash[:], row.KeyHash)
	return authenticated, nil
}

func replaceAPIPools(ctx context.Context, tx pgx.Tx, keyID string, inputs []gateway.PoolInput, existingOffers map[string]int64) error {
	q := New(tx)
	keepPools := make([]string, 0, len(inputs))
	for _, input := range inputs {
		statuses, targets, err := channelpg.ResolveRoutingTargets(ctx, tx, input.OfferIDs)
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
		poolID, err := q.LockActivePool(ctx, LockActivePoolParams{
			ApiKeyID: keyID, CanonicalModelID: input.CanonicalModelID, Protocol: string(input.Protocol),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			poolID, err = q.InsertPool(ctx, InsertPoolParams{
				ApiKeyID: keyID, CanonicalModelID: input.CanonicalModelID, Protocol: string(input.Protocol),
			})
		} else if err == nil {
			err = q.BumpPoolVersion(ctx, poolID)
		}
		if err != nil {
			return mapGatewayError(err)
		}
		keepPools = append(keepPools, poolID)
		if err := q.DeletePoolMembers(ctx, poolID); err != nil {
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
			if err := q.InsertPoolMember(ctx, InsertPoolMemberParams{
				PoolID: poolID, OfferID: offerID, Priority: int32(index + 1), AddedValidationVersion: validationVersion,
			}); err != nil {
				return mapGatewayError(err)
			}
		}
	}
	return q.DeleteStalePools(ctx, DeleteStalePoolsParams{ApiKeyID: keyID, KeepPoolIds: keepPools})
}

func existingPoolOffers(ctx context.Context, q *Queries, keyID string) (map[string]int64, error) {
	rows, err := q.ListExistingPoolOffers(ctx, keyID)
	if err != nil {
		return nil, err
	}
	result := make(map[string]int64, len(rows))
	for _, row := range rows {
		result[row.OfferID] = row.AddedValidationVersion
	}
	return result, nil
}

func loadAPIKey(ctx context.Context, db DBTX, ownerID, keyID string) (gateway.APIKey, error) {
	q := New(db)
	row, err := q.GetAPIKey(ctx, GetAPIKeyParams{ID: keyID, OwnerAccountID: ownerID})
	if err != nil {
		return gateway.APIKey{}, mapGatewayError(err)
	}
	result := gateway.APIKey{
		ID: row.ID, OwnerAccountID: row.OwnerAccountID, DisplayName: row.DisplayName, Prefix: row.KeyPrefix,
		Generation: row.Generation, Status: gateway.KeyStatus(row.Status), Version: row.Version,
		LastUsedAt: row.LastUsedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	poolRows, err := q.ListAPIKeyPools(ctx, keyID)
	if err != nil {
		return gateway.APIKey{}, err
	}
	pools := make([]gateway.ModelPool, 0, len(poolRows))
	for _, poolRow := range poolRows {
		members, err := loadPoolMembers(ctx, db, poolRow.ID)
		if err != nil {
			return gateway.APIKey{}, err
		}
		pools = append(pools, gateway.ModelPool{
			ID: poolRow.ID, CanonicalModelID: poolRow.CanonicalModelID, ModelName: poolRow.ModelName,
			Protocol: channel.Protocol(poolRow.Protocol), Version: poolRow.Version,
			CreatedAt: poolRow.CreatedAt, UpdatedAt: poolRow.UpdatedAt, Members: members,
		})
	}
	result.Pools = pools
	return result, nil
}

func loadPoolMembers(ctx context.Context, db DBTX, poolID string) ([]gateway.PoolMember, error) {
	rows, err := New(db).ListPoolMembers(ctx, poolID)
	if err != nil {
		return nil, err
	}
	items := make([]gateway.PoolMember, 0, len(rows))
	contextWindows := make([]int64, 0, len(rows))
	for _, row := range rows {
		item := gateway.PoolMember{
			Priority: int(row.Priority), OfferID: row.OfferID, ChannelID: row.ChannelID,
			ChannelDisplayName: row.ChannelDisplayName, OwnerDisplayName: row.OwnerDisplayName,
			AddedValidationVersion: row.AddedValidationVersion, CurrentValidationVersion: row.CurrentValidationVersion,
			ModelID: row.ModelID, Multiplier: row.MultiplierNano,
			CallSuccessRate: row.SuccessRate, TTFTMilliseconds: row.TtftMilliseconds, TokensPerSecond: row.TokensPerSecond,
		}
		validationStatus := ""
		if row.ValidationStatus != nil {
			validationStatus = *row.ValidationStatus
		}
		item.Eligible, item.IneligibleReason = channelpg.RoutingEligibility(
			identity.Status(row.OwnerStatus), row.OwnerMustChangePassword, channel.Status(row.ChannelStatus), channel.OfferStatus(row.OfferStatus),
			catalog.Status(row.ModelStatus), validationStatus, row.CredentialConfigured,
		)
		if item.AddedValidationVersion != item.CurrentValidationVersion {
			item.Eligible = false
			item.IneligibleReason = "validation_version_changed"
		}
		if row.InputPrice != nil && row.OutputPrice != nil && row.CacheWritePrice != nil && row.CacheReadPrice != nil {
			item.InputPrice, item.OutputPrice = money.FromNano(*row.InputPrice), money.FromNano(*row.OutputPrice)
			item.CacheWritePrice, item.CacheReadPrice = money.FromNano(*row.CacheWritePrice), money.FromNano(*row.CacheReadPrice)
		} else {
			item.Eligible = false
			item.IneligibleReason = "price_unrepresentable"
		}
		items = append(items, item)
		contextWindows = append(contextWindows, row.ContextWindow)
	}
	if len(items) > 0 {
		modelIDs := make([]string, 0, len(items))
		for _, item := range items {
			modelIDs = append(modelIDs, item.ModelID)
		}
		tiers, tierErr := catalogpg.PriceTiersByModel(ctx, db, modelIDs)
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
