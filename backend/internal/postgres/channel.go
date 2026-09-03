package postgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type channelQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

const channelColumns = `
		c.id::text, c.owner_account_id::text, owner.display_name, owner.status, owner.must_change_password,
		c.display_name, c.normalized_base_url, (credential.channel_id IS NOT NULL),
		c.credential_version, c.credential_updated_at, c.status, c.version,
		COALESCE(rating.average_rating, ''), COALESCE(rating.rating_count, 0), current_rating.score,
		c.created_at, c.updated_at`

func scanChannel(row scanner) (channel.Channel, error) {
	var result channel.Channel
	var ownerStatus, status string
	var credentialUpdated sql.NullTime
	var averageRating string
	var currentRating sql.NullInt64
	if err := row.Scan(
		&result.ID, &result.OwnerAccountID, &result.OwnerDisplayName, &ownerStatus, &result.OwnerMustChangePassword,
		&result.DisplayName, &result.NormalizedBaseURL, &result.CredentialConfigured,
		&result.CredentialVersion, &credentialUpdated, &status, &result.Version,
		&averageRating, &result.RatingCount, &currentRating, &result.CreatedAt, &result.UpdatedAt,
	); err != nil {
		return channel.Channel{}, mapChannelError(err)
	}
	result.OwnerStatus = identity.Status(ownerStatus)
	result.Status = channel.Status(status)
	if credentialUpdated.Valid {
		value := credentialUpdated.Time
		result.CredentialUpdatedAt = &value
	}
	if averageRating != "" {
		result.AverageRating = &averageRating
	}
	if currentRating.Valid {
		value := int(currentRating.Int64)
		result.CurrentUserRating = &value
	}
	return result, nil
}

func getChannel(ctx context.Context, queryer channelQueryer, channelID, viewerID string) (channel.Channel, error) {
	result, err := scanChannel(queryer.QueryRow(ctx, `
		SELECT `+channelColumns+`
		FROM channels c
		JOIN accounts owner ON owner.id = c.owner_account_id
		LEFT JOIN channel_credentials credential ON credential.channel_id = c.id
		LEFT JOIN LATERAL (
			SELECT round(avg(score)::numeric, 2)::text AS average_rating, count(*) AS rating_count
			FROM channel_ratings WHERE channel_id = c.id
		) rating ON true
		LEFT JOIN channel_ratings current_rating
			ON current_rating.channel_id = c.id AND current_rating.account_id = NULLIF($2, '')::uuid
		WHERE c.id = $1`, channelID, viewerID))
	if err != nil {
		return channel.Channel{}, err
	}
	result.Offers, err = offersForChannel(ctx, queryer, channelID, true)
	if err != nil {
		return channel.Channel{}, err
	}
	for index := range result.Offers {
		validationStatus := ""
		if result.Offers[index].LatestValidation != nil {
			validationStatus = string(result.Offers[index].LatestValidation.Status)
		}
		result.Offers[index].Eligible, result.Offers[index].IneligibleReason = routingEligibility(
			result.OwnerStatus,
			result.OwnerMustChangePassword,
			result.Status,
			result.Offers[index].Status,
			result.Offers[index].ModelStatus,
			validationStatus,
			result.CredentialConfigured,
		)
		if _, priceErr := channel.CalculateBenchmarkPrices(result.Offers[index]); priceErr != nil {
			result.Offers[index].Eligible = false
			result.Offers[index].IneligibleReason = "price_unrepresentable"
		} else if _, priceErr := gateway.ConservativeNetDebitUpperBound(offerRoutingLease(result.Offers[index]), ledger.FixedPointScale, false); priceErr != nil {
			result.Offers[index].Eligible = false
			result.Offers[index].IneligibleReason = "price_unrepresentable"
		}
	}
	return result, nil
}

const offerColumns = `
		o.id::text, cm.channel_id::text, cm.model_id, m.name, m.provider, o.protocol,
		o.upstream_model_id, COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano), o.status, o.validation_version, o.version,
		m.status, m.context_window, m.input_price_nano_per_million, m.output_price_nano_per_million,
		m.cache_write_price_nano_per_million, m.cache_read_price_nano_per_million,
		attempt.id::text, attempt.attempt_seq, attempt.actor_account_id::text,
		attempt.status, attempt.error_category, attempt.http_status, attempt.raw_error,
		attempt.raw_error_truncated, attempt.duration_milliseconds,
		attempt.started_at, attempt.completed_at, o.created_at, o.updated_at,
		gateway_metrics.success_rate, gateway_metrics.ttft_milliseconds,
		gateway_metrics.tokens_per_second, gateway_metrics.call_count,
		gateway_income.provider_income_nano`

func scanOffer(row scanner) (channel.Offer, error) {
	var result channel.Offer
	var protocol, status, modelStatus string
	var multiplier, inputPrice, outputPrice, cacheWritePrice, cacheReadPrice int64
	var attemptID, attemptActor, attemptStatus, errorCategory, rawError sql.NullString
	var attemptSeq, httpStatus, duration sql.NullInt64
	var successRate, tokensPerSecond sql.NullString
	var gatewayTTFT, callCount, providerIncome sql.NullInt64
	var rawTruncated sql.NullBool
	var startedAt, completedAt sql.NullTime
	if err := row.Scan(
		&result.ID, &result.ChannelID, &result.ModelID, &result.ModelName, &result.ModelProvider, &protocol,
		&result.UpstreamModelID, &multiplier, &status, &result.ValidationVersion, &result.Version,
		&modelStatus, &result.ContextWindow, &inputPrice, &outputPrice, &cacheWritePrice, &cacheReadPrice,
		&attemptID, &attemptSeq, &attemptActor, &attemptStatus, &errorCategory, &httpStatus, &rawError,
		&rawTruncated, &duration, &startedAt, &completedAt, &result.CreatedAt, &result.UpdatedAt,
		&successRate, &gatewayTTFT, &tokensPerSecond, &callCount, &providerIncome,
	); err != nil {
		return channel.Offer{}, mapChannelError(err)
	}
	result.Protocol = channel.Protocol(protocol)
	result.Status = channel.OfferStatus(status)
	result.ModelStatus = catalogStatus(modelStatus)
	result.Multiplier = money.FromNano(multiplier)
	result.InputPrice = money.FromNano(inputPrice)
	result.OutputPrice = money.FromNano(outputPrice)
	result.CacheWritePrice = money.FromNano(cacheWritePrice)
	result.CacheReadPrice = money.FromNano(cacheReadPrice)
	if attemptID.Valid {
		attempt := channel.ValidationAttempt{
			ID: attemptID.String, OfferID: result.ID, ValidationVersion: result.ValidationVersion,
			AttemptSeq: attemptSeq.Int64, ActorAccountID: attemptActor.String,
			Status: channel.ValidationStatus(attemptStatus.String), ErrorCategory: channel.ErrorCategory(errorCategory.String),
			HTTPStatus: int(httpStatus.Int64),
			RawError:   rawError.String, RawErrorTruncated: rawTruncated.Bool,
			Duration: timeDurationMilliseconds(duration.Int64), StartedAt: startedAt.Time,
		}
		if completedAt.Valid {
			value := completedAt.Time
			attempt.CompletedAt = &value
		}
		result.LatestValidation = &attempt
	}
	if successRate.Valid {
		value := successRate.String
		result.CallSuccessRate = &value
	}
	if gatewayTTFT.Valid {
		value := gatewayTTFT.Int64
		result.TTFTMilliseconds = &value
	}
	if tokensPerSecond.Valid {
		value := tokensPerSecond.String
		result.TokensPerSecond = &value
	}
	if callCount.Valid {
		value := callCount.Int64
		result.CallCount = &value
	}
	if providerIncome.Valid {
		value := money.FromNano(providerIncome.Int64)
		result.ProviderIncome = &value
	}
	return result, nil
}

const offerGatewayMetricJoins = `
		LEFT JOIN LATERAL (
			SELECT
				round((count(*) FILTER (WHERE gateway_attempt.status = 'succeeded'))::numeric /
					NULLIF(count(*) FILTER (WHERE gateway_attempt.status IN ('succeeded', 'failed', 'cancelled', 'incomplete')), 0), 4)::text AS success_rate,
				round(avg(gateway_attempt.ttft_milliseconds) FILTER (WHERE gateway_attempt.status = 'succeeded'))::bigint AS ttft_milliseconds,
				round((avg(gateway_attempt.tokens_per_second_nano) FILTER (WHERE gateway_attempt.status = 'succeeded'))::numeric /
					1000000000, 3)::text AS tokens_per_second,
				NULLIF(count(*) FILTER (WHERE gateway_attempt.status IN ('succeeded', 'failed', 'cancelled', 'incomplete')), 0) AS call_count
			FROM api_call_attempts gateway_attempt WHERE gateway_attempt.offer_id = o.id
		) gateway_metrics ON true
		LEFT JOIN LATERAL (
			SELECT NULLIF(sum(gateway_settlement.provider_charge_nano), 0) AS provider_income_nano
			FROM api_call_settlements gateway_settlement
			JOIN api_calls gateway_call ON gateway_call.id = gateway_settlement.call_id
			WHERE gateway_call.final_offer_id = o.id AND gateway_call.status = 'succeeded'
		) gateway_income ON true`

func offersForChannel(ctx context.Context, queryer channelQueryer, channelID string, includeDeleted bool) ([]channel.Offer, error) {
	rows, err := queryer.Query(ctx, `
		SELECT `+offerColumns+`
		FROM channel_offers o
		JOIN channel_models cm ON cm.id = o.channel_model_id
		JOIN models m ON m.id = cm.model_id
		LEFT JOIN channel_validation_attempts attempt
			ON attempt.offer_id = o.id
			AND attempt.validation_version = o.validation_version
			AND attempt.attempt_seq = o.validation_attempt_seq
		`+offerGatewayMetricJoins+`
		WHERE cm.channel_id = $1 AND ($2 OR o.status <> 'deleted')
		ORDER BY m.provider, m.name, o.protocol, o.created_at, o.id`, channelID, includeDeleted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]channel.Offer, 0)
	for rows.Next() {
		offer, err := scanOffer(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, offer)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return attachOfferPriceTiers(ctx, queryer, result)
}

func attachOfferPriceTiers(ctx context.Context, queryer tierQueryer, offers []channel.Offer) ([]channel.Offer, error) {
	modelIDs := make([]string, 0, len(offers))
	for _, offer := range offers {
		modelIDs = append(modelIDs, offer.ModelID)
	}
	tiers, err := loadModelPriceTiers(ctx, queryer, modelIDs)
	if err != nil {
		return nil, err
	}
	for index := range offers {
		offers[index].PriceTiers = tiers[offers[index].ModelID]
	}
	return offers, nil
}

func catalogStatus(value string) catalog.Status { return catalog.Status(value) }

func timeDurationMilliseconds(value int64) time.Duration {
	return time.Duration(value) * time.Millisecond
}

func offerRoutingLease(offer channel.Offer) channel.RoutingLease {
	return channel.RoutingLease{
		ContextWindow: offer.ContextWindow, Multiplier: offer.Multiplier,
		InputPrice: offer.InputPrice, OutputPrice: offer.OutputPrice,
		CacheWritePrice: offer.CacheWritePrice, CacheReadPrice: offer.CacheReadPrice,
		PriceTiers: offer.PriceTiers,
	}
}

func (s *Store) CreateChannel(ctx context.Context, command channel.CreateCommand) (channel.Channel, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return channel.Channel{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	result, err := tx.Exec(ctx, `
		INSERT INTO channels (
			id, owner_account_id, display_name, normalized_base_url,
			credential_version, credential_updated_at
		)
		SELECT $1, a.id, $3, $4, $5, now()
		FROM accounts a
		WHERE a.id = $2 AND a.status = 'active' AND NOT a.must_change_password`,
		command.ChannelID, command.OwnerAccountID, command.DisplayName, command.NormalizedBaseURL, command.Credential.Version)
	if err != nil {
		return channel.Channel{}, mapChannelError(err)
	}
	if result.RowsAffected() != 1 {
		return channel.Channel{}, channel.ErrForbidden
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_credentials (channel_id, credential_version, key_id, nonce, ciphertext)
		VALUES ($1, $2, $3, $4, $5)`, command.ChannelID, command.Credential.Version,
		command.Credential.KeyID, command.Credential.Nonce, command.Credential.Ciphertext); err != nil {
		return channel.Channel{}, mapChannelError(err)
	}
	channelModels := make(map[string]string)
	for _, offer := range command.Offers {
		channelModelID := channelModels[offer.ModelID]
		if channelModelID == "" {
			var modelStatus string
			err := tx.QueryRow(ctx, `SELECT status FROM models WHERE id = $1 FOR SHARE`, offer.ModelID).Scan(&modelStatus)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return channel.Channel{}, channel.ErrUnavailable
				}
				return channel.Channel{}, err
			}
			if catalog.Status(modelStatus) != catalog.StatusActive {
				return channel.Channel{}, channel.ErrUnavailable
			}
			err = tx.QueryRow(ctx, `
				INSERT INTO channel_models (channel_id, model_id, multiplier_nano)
				VALUES ($1, $2, $3)
				RETURNING id::text`, command.ChannelID, offer.ModelID, offer.Multiplier.Nano()).Scan(&channelModelID)
			if err != nil {
				return channel.Channel{}, mapChannelError(err)
			}
			channelModels[offer.ModelID] = channelModelID
		} else {
			var existingMultiplier int64
			if err := tx.QueryRow(ctx, `SELECT multiplier_nano FROM channel_models WHERE id = $1`, channelModelID).Scan(&existingMultiplier); err != nil {
				return channel.Channel{}, err
			}
			if existingMultiplier != offer.Multiplier.Nano() {
				return channel.Channel{}, channel.ErrInvalidInput
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_offers (id, channel_model_id, protocol, upstream_model_id)
			VALUES ($1, $2, $3, $4)`, offer.ID, channelModelID, offer.Protocol, offer.UpstreamModelID); err != nil {
			return channel.Channel{}, mapChannelError(err)
		}
	}
	if err := insertAudit(ctx, tx, command.OwnerAccountID, "channel.created", "channel", command.ChannelID, "owner created channel draft", map[string]any{
		"status": channel.StatusDraft, "offer_count": len(command.Offers), "credential_version": command.Credential.Version,
	}); err != nil {
		return channel.Channel{}, err
	}
	created, err := getChannel(ctx, tx, command.ChannelID, command.OwnerAccountID)
	if err != nil {
		return channel.Channel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return channel.Channel{}, err
	}
	return created, nil
}

func (s *Store) ListOwnerChannels(ctx context.Context, ownerID string) ([]channel.Channel, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM channels WHERE owner_account_id = $1 ORDER BY updated_at DESC, id`, ownerID)
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
	result := make([]channel.Channel, 0, len(ids))
	for _, id := range ids {
		value, err := getChannel(ctx, s.pool, id, ownerID)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *Store) GetOwnerChannel(ctx context.Context, ownerID, channelID string) (channel.Channel, error) {
	var allowed bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM channels WHERE id = $1 AND owner_account_id = $2)`, channelID, ownerID).Scan(&allowed); err != nil {
		return channel.Channel{}, err
	}
	if !allowed {
		return channel.Channel{}, channel.ErrNotFound
	}
	return getChannel(ctx, s.pool, channelID, ownerID)
}

func (s *Store) UpdateChannel(ctx context.Context, command channel.UpdateCommand) (channel.Channel, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return channel.Channel{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var currentVersion, credentialVersion int64
	var currentBase, status string
	err = tx.QueryRow(ctx, `
		SELECT c.version, c.credential_version, c.normalized_base_url, c.status
		FROM channels c JOIN accounts owner ON owner.id = c.owner_account_id
		WHERE c.id = $1 AND c.owner_account_id = $2
			AND owner.status = 'active' AND NOT owner.must_change_password
		FOR UPDATE`, command.ChannelID, command.ActorAccountID).Scan(&currentVersion, &credentialVersion, &currentBase, &status)
	if err != nil {
		return channel.Channel{}, mapChannelError(err)
	}
	if channel.Status(status) == channel.StatusDeleted {
		return channel.Channel{}, channel.ErrConflict
	}
	if currentVersion != command.ExpectedVersion {
		return channel.Channel{}, channel.ErrConflict
	}
	semanticCredentialChange := command.Credential != nil
	if semanticCredentialChange && command.Credential.Version != credentialVersion+1 {
		return channel.Channel{}, channel.ErrConflict
	}
	validationChange := command.BaseURLChanged || semanticCredentialChange
	newCredentialVersion := credentialVersion
	if semanticCredentialChange {
		newCredentialVersion = command.Credential.Version
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channels SET display_name = $2, normalized_base_url = $3,
			credential_version = $4,
			credential_updated_at = CASE WHEN $5 THEN now() ELSE credential_updated_at END,
			version = version + 1, updated_at = now()
		WHERE id = $1`, command.ChannelID, command.DisplayName, command.NormalizedBaseURL,
		newCredentialVersion, semanticCredentialChange); err != nil {
		return channel.Channel{}, mapChannelError(err)
	}
	if command.Credential != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_credentials (channel_id, credential_version, key_id, nonce, ciphertext)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (channel_id) DO UPDATE SET
				credential_version = EXCLUDED.credential_version,
				key_id = EXCLUDED.key_id,
				nonce = EXCLUDED.nonce,
				ciphertext = EXCLUDED.ciphertext,
				configured_at = now(), updated_at = now()`, command.ChannelID, command.Credential.Version,
			command.Credential.KeyID, command.Credential.Nonce, command.Credential.Ciphertext); err != nil {
			return channel.Channel{}, mapChannelError(err)
		}
	}
	if validationChange {
		if _, err := tx.Exec(ctx, `
			UPDATE channel_offers o SET validation_version = validation_version + 1,
				validation_attempt_seq = 0, updated_at = now()
			FROM channel_models cm
			WHERE o.channel_model_id = cm.id AND cm.channel_id = $1 AND o.status <> 'deleted'`, command.ChannelID); err != nil {
			return channel.Channel{}, err
		}
	}
	if err := insertAudit(ctx, tx, command.ActorAccountID, "channel.updated", "channel", command.ChannelID, "owner updated channel configuration", map[string]any{
		"version": currentVersion + 1, "base_url_changed": command.BaseURLChanged,
		"credential_changed": semanticCredentialChange, "credential_version": newCredentialVersion,
	}); err != nil {
		return channel.Channel{}, err
	}
	updated, err := getChannel(ctx, tx, command.ChannelID, command.ActorAccountID)
	if err != nil {
		return channel.Channel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return channel.Channel{}, err
	}
	return updated, nil
}

func (s *Store) SetChannelStatus(ctx context.Context, command channel.StatusCommand) (channel.Channel, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return channel.Channel{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var currentStatus string
	var currentVersion int64
	var ownerID string
	if command.Administrator {
		err = tx.QueryRow(ctx, `
			SELECT c.status, c.version, c.owner_account_id::text
			FROM channels c
			WHERE c.id = $1 AND EXISTS (
				SELECT 1 FROM accounts actor WHERE actor.id = $2 AND actor.status = 'active' AND actor.is_admin
			) FOR UPDATE`, command.ChannelID, command.ActorAccountID).Scan(&currentStatus, &currentVersion, &ownerID)
	} else {
		err = tx.QueryRow(ctx, `
			SELECT c.status, c.version, c.owner_account_id::text
			FROM channels c JOIN accounts owner ON owner.id = c.owner_account_id
			WHERE c.id = $1 AND c.owner_account_id = $2
				AND owner.status = 'active' AND NOT owner.must_change_password
			FOR UPDATE`, command.ChannelID, command.ActorAccountID).Scan(&currentStatus, &currentVersion, &ownerID)
	}
	if err != nil {
		return channel.Channel{}, mapChannelError(err)
	}
	if currentVersion != command.ExpectedVersion || !validChannelTransition(channel.Status(currentStatus), command.Status, command.Administrator) {
		return channel.Channel{}, channel.ErrConflict
	}
	if command.Status == channel.StatusPublished {
		var eligibleOfferCount int
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM channel_offers offer
			JOIN channel_models channel_model ON channel_model.id = offer.channel_model_id
			JOIN models model ON model.id = channel_model.model_id
			JOIN channel_credentials credential ON credential.channel_id = channel_model.channel_id
			JOIN channel_validation_attempts attempt
				ON attempt.offer_id = offer.id
				AND attempt.validation_version = offer.validation_version
				AND attempt.attempt_seq = offer.validation_attempt_seq
			WHERE channel_model.channel_id = $1 AND offer.status = 'active'
				AND model.status = 'active' AND attempt.status = 'passed'`, command.ChannelID).Scan(&eligibleOfferCount); err != nil {
			return channel.Channel{}, mapChannelError(err)
		}
		if eligibleOfferCount == 0 {
			return channel.Channel{}, channel.ErrUnavailable
		}
	}
	deleted := command.Status == channel.StatusDeleted
	if _, err := tx.Exec(ctx, `
		UPDATE channels SET status = $2, version = version + 1, updated_at = now(),
			deleted_at = CASE WHEN $3 THEN now() ELSE NULL END,
			credential_version = credential_version + CASE WHEN $3 THEN 1 ELSE 0 END,
			credential_updated_at = CASE WHEN $3 THEN now() ELSE credential_updated_at END
		WHERE id = $1`, command.ChannelID, command.Status, deleted); err != nil {
		return channel.Channel{}, mapChannelError(err)
	}
	if deleted {
		if _, err := tx.Exec(ctx, `DELETE FROM channel_credentials WHERE channel_id = $1`, command.ChannelID); err != nil {
			return channel.Channel{}, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE channel_offers o SET validation_version = validation_version + 1,
				validation_attempt_seq = 0, updated_at = now()
			FROM channel_models cm
			WHERE o.channel_model_id = cm.id AND cm.channel_id = $1 AND o.status <> 'deleted'`, command.ChannelID); err != nil {
			return channel.Channel{}, err
		}
	}
	reason := command.Reason
	if reason == "" {
		reason = "owner changed channel lifecycle"
	}
	if err := insertAudit(ctx, tx, command.ActorAccountID, "channel.status_changed", "channel", command.ChannelID, reason, map[string]any{
		"from": currentStatus, "to": command.Status, "administrator": command.Administrator, "version": currentVersion + 1,
	}); err != nil {
		return channel.Channel{}, err
	}
	updated, err := getChannel(ctx, tx, command.ChannelID, ownerID)
	if err != nil {
		return channel.Channel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return channel.Channel{}, err
	}
	return updated, nil
}

func validChannelTransition(from, to channel.Status, administrator bool) bool {
	if from == channel.StatusDeleted || from == to {
		return false
	}
	if to == channel.StatusDeleted {
		return true
	}
	if administrator {
		return to == channel.StatusPaused && from == channel.StatusPublished
	}
	return (to == channel.StatusPublished && (from == channel.StatusDraft || from == channel.StatusPaused)) ||
		(to == channel.StatusPaused && from == channel.StatusPublished)
}

func (s *Store) RevokeCredential(ctx context.Context, actorID, channelID string, expectedVersion int64) (channel.Channel, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return channel.Channel{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var ownerID string
	var version int64
	err = tx.QueryRow(ctx, `
		SELECT c.owner_account_id::text, c.version
		FROM channels c JOIN accounts owner ON owner.id = c.owner_account_id
		JOIN channel_credentials credential ON credential.channel_id = c.id
		WHERE c.id = $1 AND c.owner_account_id = $2 AND c.status <> 'deleted'
			AND owner.status = 'active' AND NOT owner.must_change_password
		FOR UPDATE`, channelID, actorID).Scan(&ownerID, &version)
	if err != nil {
		return channel.Channel{}, mapChannelError(err)
	}
	if version != expectedVersion {
		return channel.Channel{}, channel.ErrConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM channel_credentials WHERE channel_id = $1`, channelID); err != nil {
		return channel.Channel{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channels SET credential_version = credential_version + 1,
			credential_updated_at = now(), version = version + 1, updated_at = now()
		WHERE id = $1`, channelID); err != nil {
		return channel.Channel{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_offers o SET validation_version = validation_version + 1,
			validation_attempt_seq = 0, updated_at = now()
		FROM channel_models cm
		WHERE o.channel_model_id = cm.id AND cm.channel_id = $1 AND o.status <> 'deleted'`, channelID); err != nil {
		return channel.Channel{}, err
	}
	if err := insertAudit(ctx, tx, actorID, "channel.credential_revoked", "channel", channelID, "owner revoked platform copy of upstream credential", map[string]any{
		"version": version + 1,
	}); err != nil {
		return channel.Channel{}, err
	}
	updated, err := getChannel(ctx, tx, channelID, ownerID)
	if err != nil {
		return channel.Channel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return channel.Channel{}, err
	}
	return updated, nil
}

func (s *Store) AddOffer(ctx context.Context, command channel.AddOfferCommand) (channel.Offer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return channel.Offer{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var currentVersion int64
	if err := tx.QueryRow(ctx, `
		SELECT c.version
		FROM channels c JOIN accounts owner ON owner.id = c.owner_account_id
		WHERE c.id = $1 AND c.owner_account_id = $2 AND c.status <> 'deleted'
			AND owner.status = 'active' AND NOT owner.must_change_password
		FOR UPDATE OF c`, command.ChannelID, command.ActorAccountID).Scan(&currentVersion); err != nil {
		return channel.Offer{}, mapChannelError(err)
	}
	if currentVersion != command.ExpectedChannelVersion {
		return channel.Offer{}, channel.ErrConflict
	}
	var currentModelStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM models WHERE id = $1 FOR SHARE`, command.Offer.ModelID).Scan(&currentModelStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return channel.Offer{}, channel.ErrUnavailable
		}
		return channel.Offer{}, mapChannelError(err)
	}
	if catalog.Status(currentModelStatus) != catalog.StatusActive {
		return channel.Offer{}, channel.ErrUnavailable
	}
	var channelModelID string
	var existingMultiplier int64
	var liveOfferCount int64
	err = tx.QueryRow(ctx, `
		SELECT cm.id::text, cm.multiplier_nano,
			(SELECT count(*) FROM channel_offers existing_offer
			 WHERE existing_offer.channel_model_id = cm.id AND existing_offer.status <> 'deleted')
		FROM channel_models cm
		WHERE cm.channel_id = $1 AND cm.model_id = $2
		FOR UPDATE OF cm`, command.ChannelID, command.Offer.ModelID).Scan(&channelModelID, &existingMultiplier, &liveOfferCount)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO channel_models (channel_id, model_id, multiplier_nano)
			VALUES ($1, $2, $3)
			RETURNING id::text`, command.ChannelID, command.Offer.ModelID, command.Offer.Multiplier.Nano()).Scan(&channelModelID)
		if err != nil {
			return channel.Offer{}, mapChannelError(err)
		}
	} else if err != nil {
		return channel.Offer{}, mapChannelError(err)
	} else if liveOfferCount > 0 && existingMultiplier != command.Offer.Multiplier.Nano() {
		return channel.Offer{}, channel.ErrConflict
	} else if liveOfferCount == 0 && existingMultiplier != command.Offer.Multiplier.Nano() {
		if _, err := tx.Exec(ctx, `UPDATE channel_models SET multiplier_nano = $2, updated_at = now() WHERE id = $1`, channelModelID, command.Offer.Multiplier.Nano()); err != nil {
			return channel.Offer{}, mapChannelError(err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_offers (id, channel_model_id, protocol, upstream_model_id)
		VALUES ($1, $2, $3, $4)`, command.Offer.ID, channelModelID, command.Offer.Protocol, command.Offer.UpstreamModelID); err != nil {
		return channel.Offer{}, mapChannelError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE channels SET version = version + 1, updated_at = now() WHERE id = $1`, command.ChannelID); err != nil {
		return channel.Offer{}, err
	}
	if err := insertAudit(ctx, tx, command.ActorAccountID, "channel.offer_created", "channel_offer", command.Offer.ID, "owner added protocol offer", map[string]any{
		"channel_id": command.ChannelID, "model_id": command.Offer.ModelID, "protocol": command.Offer.Protocol,
		"channel_version": currentVersion + 1,
	}); err != nil {
		return channel.Offer{}, err
	}
	created, err := offerByID(ctx, tx, command.Offer.ID)
	if err != nil {
		return channel.Offer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return channel.Offer{}, err
	}
	return created, nil
}

func offerByID(ctx context.Context, queryer channelQueryer, offerID string) (channel.Offer, error) {
	offer, err := scanOffer(queryer.QueryRow(ctx, `
		SELECT `+offerColumns+`
		FROM channel_offers o
		JOIN channel_models cm ON cm.id = o.channel_model_id
		JOIN models m ON m.id = cm.model_id
		LEFT JOIN channel_validation_attempts attempt
			ON attempt.offer_id = o.id AND attempt.validation_version = o.validation_version
			AND attempt.attempt_seq = o.validation_attempt_seq
		`+offerGatewayMetricJoins+`
		WHERE o.id = $1`, offerID))
	if err != nil {
		return channel.Offer{}, err
	}
	withTiers, err := attachOfferPriceTiers(ctx, queryer, []channel.Offer{offer})
	if err != nil {
		return channel.Offer{}, err
	}
	return withTiers[0], nil
}

func (s *Store) UpdateOffer(ctx context.Context, command channel.OfferUpdateCommand) (channel.Offer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return channel.Offer{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var version int64
	var currentUpstream, protocol, channelModelID string
	var currentMultiplier int64
	err = tx.QueryRow(ctx, `
		SELECT o.version, o.upstream_model_id, o.protocol, o.channel_model_id::text, cm.multiplier_nano
		FROM channel_offers o JOIN channel_models cm ON cm.id = o.channel_model_id
		JOIN channels c ON c.id = cm.channel_id JOIN accounts owner ON owner.id = c.owner_account_id
		WHERE o.id = $1 AND c.owner_account_id = $2 AND o.status <> 'deleted' AND c.status <> 'deleted'
			AND owner.status = 'active' AND NOT owner.must_change_password
		FOR UPDATE OF cm, o`, command.OfferID, command.ActorAccountID).Scan(&version, &currentUpstream, &protocol, &channelModelID, &currentMultiplier)
	if err != nil {
		return channel.Offer{}, mapChannelError(err)
	}
	if version != command.ExpectedVersion {
		return channel.Offer{}, channel.ErrConflict
	}
	if channel.Protocol(protocol) == channel.ProtocolGemini && (strings.HasPrefix(strings.ToLower(command.UpstreamModelID), "models/") || strings.Contains(command.UpstreamModelID, "/")) {
		return channel.Offer{}, channel.ErrInvalidInput
	}
	upstreamChanged := currentUpstream != command.UpstreamModelID
	multiplierChanged := currentMultiplier != command.Multiplier.Nano()
	affectedOffers := int64(1)
	if multiplierChanged {
		if _, err := tx.Exec(ctx, `UPDATE channel_models SET multiplier_nano = $2, updated_at = now() WHERE id = $1`, channelModelID, command.Multiplier.Nano()); err != nil {
			return channel.Offer{}, mapChannelError(err)
		}
		result, err := tx.Exec(ctx, `
			UPDATE channel_offers SET
				upstream_model_id = CASE WHEN id = $1 THEN $2 ELSE upstream_model_id END,
				version = version + 1,
				validation_version = validation_version + CASE WHEN id = $1 AND $3 THEN 1 ELSE 0 END,
				validation_attempt_seq = CASE WHEN id = $1 AND $3 THEN 0 ELSE validation_attempt_seq END,
				updated_at = now()
			WHERE channel_model_id = $4 AND status <> 'deleted'`, command.OfferID, command.UpstreamModelID, upstreamChanged, channelModelID)
		if err != nil {
			return channel.Offer{}, mapChannelError(err)
		}
		affectedOffers = result.RowsAffected()
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE channel_offers SET upstream_model_id = $2, version = version + 1,
				validation_version = validation_version + CASE WHEN $3 THEN 1 ELSE 0 END,
				validation_attempt_seq = CASE WHEN $3 THEN 0 ELSE validation_attempt_seq END,
				updated_at = now()
			WHERE id = $1`, command.OfferID, command.UpstreamModelID, upstreamChanged); err != nil {
			return channel.Offer{}, mapChannelError(err)
		}
	}
	if err := insertAudit(ctx, tx, command.ActorAccountID, "channel.offer_updated", "channel_offer", command.OfferID, "owner updated offer", map[string]any{
		"version": version + 1, "upstream_model_changed": upstreamChanged, "multiplier_changed": multiplierChanged,
		"multiplier_nano": command.Multiplier.Nano(), "affected_offer_count": affectedOffers,
	}); err != nil {
		return channel.Offer{}, err
	}
	updated, err := offerByID(ctx, tx, command.OfferID)
	if err != nil {
		return channel.Offer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return channel.Offer{}, err
	}
	return updated, nil
}

func (s *Store) SetOfferStatus(ctx context.Context, command channel.OfferStatusCommand) (channel.Offer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return channel.Offer{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var version int64
	var currentStatus string
	err = tx.QueryRow(ctx, `
		SELECT o.version, o.status
		FROM channel_offers o JOIN channel_models cm ON cm.id = o.channel_model_id
		JOIN channels c ON c.id = cm.channel_id JOIN accounts owner ON owner.id = c.owner_account_id
		WHERE o.id = $1 AND c.owner_account_id = $2 AND c.status <> 'deleted'
			AND owner.status = 'active' AND NOT owner.must_change_password
		FOR UPDATE OF o`, command.OfferID, command.ActorAccountID).Scan(&version, &currentStatus)
	if err != nil {
		return channel.Offer{}, mapChannelError(err)
	}
	if version != command.ExpectedVersion || !validOfferTransition(channel.OfferStatus(currentStatus), command.Status) {
		return channel.Offer{}, channel.ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_offers offer SET status = $2, version = version + 1, updated_at = now(),
			deleted_at = CASE WHEN $2 = 'deleted' THEN now() ELSE NULL END,
			deleted_multiplier_nano = CASE WHEN $2 = 'deleted' THEN model.multiplier_nano ELSE NULL END
		FROM channel_models model
		WHERE offer.id = $1 AND model.id = offer.channel_model_id`, command.OfferID, command.Status); err != nil {
		return channel.Offer{}, mapChannelError(err)
	}
	if err := insertAudit(ctx, tx, command.ActorAccountID, "channel.offer_status_changed", "channel_offer", command.OfferID, "owner changed offer lifecycle", map[string]any{
		"from": currentStatus, "to": command.Status, "version": version + 1,
	}); err != nil {
		return channel.Offer{}, err
	}
	updated, err := offerByID(ctx, tx, command.OfferID)
	if err != nil {
		return channel.Offer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return channel.Offer{}, err
	}
	return updated, nil
}

func validOfferTransition(from, to channel.OfferStatus) bool {
	if from == channel.OfferDeleted || from == to {
		return false
	}
	return to == channel.OfferDeleted || (from == channel.OfferActive && to == channel.OfferDisabled) || (from == channel.OfferDisabled && to == channel.OfferActive)
}

func (s *Store) StartValidation(ctx context.Context, actor identity.Account, offerID string) (channel.ValidationTarget, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return channel.ValidationTarget{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var target channel.ValidationTarget
	var protocol string
	var credentialVersion int64
	var keyID string
	var nonce, ciphertext []byte
	var validationVersion, currentAttemptSeq int64
	// Lock the channel before the offer. Configuration changes use the same
	// order, so the target below is one coherent Base URL, credential and offer
	// validation-version snapshot rather than a mixture of concurrent edits.
	var lockedChannelID string
	err = tx.QueryRow(ctx, `
		SELECT c.id::text
		FROM channel_offers o
		JOIN channel_models cm ON cm.id = o.channel_model_id
		JOIN channels c ON c.id = cm.channel_id
		JOIN accounts actor ON actor.id = $2
		WHERE o.id = $1 AND o.status <> 'deleted' AND c.status <> 'deleted'
			AND actor.status = 'active' AND NOT actor.must_change_password
			AND (c.owner_account_id = actor.id OR actor.is_admin)
		FOR UPDATE OF c`, offerID, actor.ID).Scan(&lockedChannelID)
	if err != nil {
		return channel.ValidationTarget{}, mapChannelError(err)
	}
	err = tx.QueryRow(ctx, `
		SELECT cm.channel_id::text, c.owner_account_id::text, c.normalized_base_url,
			o.protocol, o.upstream_model_id, o.validation_version, o.validation_attempt_seq,
			credential.credential_version, credential.key_id, credential.nonce, credential.ciphertext
		FROM channel_offers o
		JOIN channel_models cm ON cm.id = o.channel_model_id
		JOIN channels c ON c.id = cm.channel_id
		JOIN channel_credentials credential
			ON credential.channel_id = c.id AND credential.credential_version = c.credential_version
		JOIN accounts actor ON actor.id = $2
		WHERE o.id = $1 AND o.status <> 'deleted' AND c.status <> 'deleted'
			AND actor.status = 'active' AND NOT actor.must_change_password
			AND (c.owner_account_id = actor.id OR actor.is_admin)
			AND c.id = $3
		FOR UPDATE OF o`, offerID, actor.ID, lockedChannelID).Scan(
		&target.ChannelID, &target.OwnerAccountID, &target.NormalizedBaseURL,
		&protocol, &target.UpstreamModelID, &validationVersion, &currentAttemptSeq,
		&credentialVersion, &keyID, &nonce, &ciphertext,
	)
	if err != nil {
		return channel.ValidationTarget{}, mapChannelError(err)
	}
	var attempt channel.ValidationAttempt
	attempt.OfferID = offerID
	attempt.ValidationVersion = validationVersion
	attempt.AttemptSeq = currentAttemptSeq + 1
	attempt.ActorAccountID = actor.ID
	attempt.Status = channel.ValidationInProgress
	if err := tx.QueryRow(ctx, `
		INSERT INTO channel_validation_attempts (
			offer_id, validation_version, attempt_seq, actor_account_id, status
		) VALUES ($1, $2, $3, $4, 'in_progress')
		RETURNING id::text, started_at`, offerID, validationVersion, attempt.AttemptSeq, actor.ID).Scan(&attempt.ID, &attempt.StartedAt); err != nil {
		return channel.ValidationTarget{}, mapChannelError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_offers SET validation_attempt_seq = $2, updated_at = now()
		WHERE id = $1`, offerID, attempt.AttemptSeq); err != nil {
		return channel.ValidationTarget{}, err
	}
	if err := insertAudit(ctx, tx, actor.ID, "channel.validation_started", "channel_offer", offerID, "authorized actor started explicit upstream validation", map[string]any{
		"validation_version": validationVersion, "attempt_seq": attempt.AttemptSeq,
	}); err != nil {
		return channel.ValidationTarget{}, err
	}
	target.Attempt = attempt
	target.Protocol = channel.Protocol(protocol)
	target.Credential = channel.EncryptedCredential{Version: credentialVersion, KeyID: keyID, Nonce: nonce, Ciphertext: ciphertext}
	if err := tx.Commit(ctx); err != nil {
		return channel.ValidationTarget{}, err
	}
	return target, nil
}

func (s *Store) CompleteValidation(ctx context.Context, attempt channel.ValidationAttempt) error {
	if attempt.Status != channel.ValidationPassed && attempt.Status != channel.ValidationFailed || attempt.CompletedAt == nil {
		return channel.ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	result, err := tx.Exec(ctx, `
		UPDATE channel_validation_attempts SET
			status = $5, error_category = $6, http_status = NULLIF($7, 0), raw_error = $8,
			raw_error_truncated = $9, duration_milliseconds = $10,
			completed_at = GREATEST(clock_timestamp(), started_at)
		WHERE id = $1 AND offer_id = $2 AND validation_version = $3 AND attempt_seq = $4
			AND status = 'in_progress'`, attempt.ID, attempt.OfferID, attempt.ValidationVersion, attempt.AttemptSeq,
		attempt.Status, attempt.ErrorCategory, attempt.HTTPStatus, attempt.RawError, attempt.RawErrorTruncated,
		attempt.Duration.Milliseconds())
	if err != nil {
		return mapChannelError(err)
	}
	if result.RowsAffected() != 1 {
		return channel.ErrConflict
	}
	if err := insertAudit(ctx, tx, attempt.ActorAccountID, "channel.validation_completed", "channel_offer", attempt.OfferID, "upstream validation attempt completed", map[string]any{
		"validation_version":    attempt.ValidationVersion,
		"attempt_seq":           attempt.AttemptSeq,
		"status":                attempt.Status,
		"error_category":        attempt.ErrorCategory,
		"duration_milliseconds": attempt.Duration.Milliseconds(),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ExpireValidationAttempts(ctx context.Context, before time.Time) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	rows, err := tx.Query(ctx, `
		UPDATE channel_validation_attempts SET
			status = 'failed', error_category = 'timeout',
			raw_error = 'validation worker did not complete before recovery deadline',
			raw_error_truncated = false,
			duration_milliseconds = GREATEST(0, floor(extract(epoch FROM (clock_timestamp() - started_at)) * 1000))::bigint,
			completed_at = GREATEST(clock_timestamp(), started_at)
		WHERE status = 'in_progress' AND started_at < $1
		RETURNING offer_id::text, actor_account_id::text, validation_version, attempt_seq`, before)
	if err != nil {
		return 0, mapChannelError(err)
	}
	type expiredAttempt struct {
		offerID, actorID            string
		validationVersion, sequence int64
	}
	expired := make([]expiredAttempt, 0)
	for rows.Next() {
		var item expiredAttempt
		if err := rows.Scan(&item.offerID, &item.actorID, &item.validationVersion, &item.sequence); err != nil {
			rows.Close()
			return 0, err
		}
		expired = append(expired, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, item := range expired {
		if err := insertAudit(ctx, tx, item.actorID, "channel.validation_recovered", "channel_offer", item.offerID, "abandoned upstream validation attempt expired", map[string]any{
			"validation_version": item.validationVersion, "attempt_seq": item.sequence, "error_category": channel.ErrorTimeout,
		}); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int64(len(expired)), nil
}

func (s *Store) ListValidationAttempts(ctx context.Context, actor identity.Account, offerID string, limit int) ([]channel.ValidationAttempt, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT attempt.id::text, attempt.offer_id::text, attempt.validation_version,
			attempt.attempt_seq, attempt.actor_account_id::text, attempt.status,
			attempt.error_category, attempt.http_status, attempt.raw_error, attempt.raw_error_truncated,
			attempt.duration_milliseconds, attempt.started_at, attempt.completed_at
		FROM channel_validation_attempts attempt
		JOIN channel_offers offer ON offer.id = attempt.offer_id
		JOIN channel_models channel_model ON channel_model.id = offer.channel_model_id
		JOIN channels c ON c.id = channel_model.channel_id
		JOIN accounts actor ON actor.id = $2
		WHERE attempt.offer_id = $1
			AND actor.status = 'active' AND NOT actor.must_change_password
			AND (c.owner_account_id = actor.id OR actor.is_admin)
		ORDER BY attempt.validation_version DESC, attempt.attempt_seq DESC
		LIMIT $3`, offerID, actor.ID, limit)
	if err != nil {
		return nil, mapChannelError(err)
	}
	defer rows.Close()
	attempts := make([]channel.ValidationAttempt, 0)
	for rows.Next() {
		var attempt channel.ValidationAttempt
		var status, category string
		var httpStatus, duration sql.NullInt64
		var completedAt sql.NullTime
		if err := rows.Scan(
			&attempt.ID, &attempt.OfferID, &attempt.ValidationVersion,
			&attempt.AttemptSeq, &attempt.ActorAccountID, &status,
			&category, &httpStatus, &attempt.RawError, &attempt.RawErrorTruncated,
			&duration, &attempt.StartedAt, &completedAt,
		); err != nil {
			return nil, err
		}
		attempt.Status = channel.ValidationStatus(status)
		attempt.ErrorCategory = channel.ErrorCategory(category)
		attempt.HTTPStatus = int(httpStatus.Int64)
		if duration.Valid {
			attempt.Duration = timeDurationMilliseconds(duration.Int64)
		}
		if completedAt.Valid {
			value := completedAt.Time
			attempt.CompletedAt = &value
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(attempts) == 0 {
		var permitted bool
		err := s.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM channel_offers offer
				JOIN channel_models channel_model ON channel_model.id = offer.channel_model_id
				JOIN channels c ON c.id = channel_model.channel_id
				JOIN accounts actor ON actor.id = $2
				WHERE offer.id = $1 AND actor.status = 'active' AND NOT actor.must_change_password
					AND (c.owner_account_id = actor.id OR actor.is_admin)
			)`, offerID, actor.ID).Scan(&permitted)
		if err != nil {
			return nil, mapChannelError(err)
		}
		if !permitted {
			return nil, channel.ErrNotFound
		}
	}
	return attempts, nil
}

type marketCursor struct {
	Sort        string           `json:"sort"`
	ModelID     string           `json:"model_id,omitempty"`
	Protocol    channel.Protocol `json:"protocol,omitempty"`
	OwnerQuery  string           `json:"owner,omitempty"`
	OfferID     string           `json:"offer_id"`
	PriceNano   int64            `json:"price_nano,omitempty"`
	Rating      *string          `json:"rating,omitempty"`
	RatingCount int64            `json:"rating_count,omitempty"`
	Metric      *string          `json:"metric,omitempty"`
}

func encodeMarketCursor(value marketCursor) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeMarketCursor(raw string, query channel.MarketQuery) (marketCursor, error) {
	if raw == "" {
		return marketCursor{Sort: query.Sort, ModelID: query.ModelID, Protocol: query.Protocol, OwnerQuery: query.OwnerQuery}, nil
	}
	encoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(encoded) > 2048 {
		return marketCursor{}, channel.ErrInvalidInput
	}
	var value marketCursor
	if err := json.Unmarshal(encoded, &value); err != nil || value.OfferID == "" ||
		value.Sort != query.Sort || value.ModelID != query.ModelID || value.Protocol != query.Protocol || value.OwnerQuery != query.OwnerQuery {
		return marketCursor{}, channel.ErrInvalidInput
	}
	return value, nil
}

func (s *Store) ListMarketOffers(ctx context.Context, viewerID string, query channel.MarketQuery) ([]channel.MarketOffer, string, error) {
	cursor, err := decodeMarketCursor(query.Cursor, query)
	if err != nil {
		return nil, "", err
	}
	sortExpression := "input_price_nano ASC, offer_id ASC"
	priceColumn := "input_price_nano"
	switch query.Sort {
	case "output_price":
		sortExpression = "output_price_nano ASC, offer_id ASC"
		priceColumn = "output_price_nano"
	case "cache_write_price":
		sortExpression = "cache_write_price_nano ASC, offer_id ASC"
		priceColumn = "cache_write_price_nano"
	case "cache_read_price":
		sortExpression = "cache_read_price_nano ASC, offer_id ASC"
		priceColumn = "cache_read_price_nano"
	case "rating":
		sortExpression = "average_rating DESC NULLS LAST, rating_count DESC, offer_id ASC"
	case "success_rate":
		sortExpression = "success_rate DESC NULLS LAST, offer_id ASC"
	case "ttft":
		sortExpression = "ttft_milliseconds ASC NULLS LAST, offer_id ASC"
	case "tps":
		sortExpression = "tokens_per_second DESC NULLS LAST, offer_id ASC"
	}
	afterExpression := "($4 = '' OR " + priceColumn + " > $5 OR (" + priceColumn + " = $5 AND offer_id > $4::uuid))"
	limitPlaceholder := "$6"
	if query.Sort == "rating" {
		limitPlaceholder = "$7"
		afterExpression = `($4 = '' OR
			($5::numeric IS NOT NULL AND (average_rating IS NULL OR average_rating < $5::numeric OR
				(average_rating = $5::numeric AND (rating_count < $6 OR (rating_count = $6 AND offer_id > $4::uuid))))) OR
			($5::numeric IS NULL AND average_rating IS NULL AND
				(rating_count < $6 OR (rating_count = $6 AND offer_id > $4::uuid))))`
	}
	if query.Sort == "success_rate" || query.Sort == "ttft" || query.Sort == "tps" {
		metricColumn := query.Sort
		if query.Sort == "ttft" {
			metricColumn = "ttft_milliseconds"
		} else if query.Sort == "tps" {
			metricColumn = "tokens_per_second"
		}
		comparison := "<"
		if query.Sort == "ttft" {
			comparison = ">"
		}
		afterExpression = `($4 = '' OR
			($5::numeric IS NOT NULL AND (` + metricColumn + ` IS NULL OR ` + metricColumn + ` ` + comparison + ` $5::numeric OR
				(` + metricColumn + ` = $5::numeric AND offer_id > $4::uuid))) OR
			($5::numeric IS NULL AND ` + metricColumn + ` IS NULL AND offer_id > $4::uuid))`
	}
	cursorValue := any(cursor.PriceNano)
	if query.Sort == "rating" {
		cursorValue = nil
		if cursor.Rating != nil {
			cursorValue = *cursor.Rating
		}
	} else if query.Sort == "success_rate" || query.Sort == "ttft" || query.Sort == "tps" {
		cursorValue = nil
		if cursor.Metric != nil {
			cursorValue = *cursor.Metric
		}
	}
	statement := `
		WITH candidates AS (
			SELECT o.id AS offer_id, cm.channel_id, c.display_name AS channel_name,
				owner.id AS owner_account_id, owner.display_name AS owner_name, cm.model_id, m.name AS model_name,
				m.provider AS model_provider, o.protocol, COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano) AS multiplier_nano,
				CASE WHEN m.input_price_nano_per_million BETWEEN 0 AND 100000000000000
						AND COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano) BETWEEN 0 AND 1000000000000
					THEN ceil(m.input_price_nano_per_million::numeric * COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano)::numeric / 1000000000)::bigint END AS input_price_nano,
				CASE WHEN m.output_price_nano_per_million BETWEEN 0 AND 100000000000000
						AND COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano) BETWEEN 0 AND 1000000000000
					THEN ceil(m.output_price_nano_per_million::numeric * COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano)::numeric / 1000000000)::bigint END AS output_price_nano,
				CASE WHEN m.cache_write_price_nano_per_million BETWEEN 0 AND 100000000000000
						AND COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano) BETWEEN 0 AND 1000000000000
					THEN ceil(m.cache_write_price_nano_per_million::numeric * COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano)::numeric / 1000000000)::bigint END AS cache_write_price_nano,
				CASE WHEN m.cache_read_price_nano_per_million BETWEEN 0 AND 100000000000000
						AND COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano) BETWEEN 0 AND 1000000000000
					THEN ceil(m.cache_read_price_nano_per_million::numeric * COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano)::numeric / 1000000000)::bigint END AS cache_read_price_nano,
				rating.average_rating, COALESCE(rating.rating_count, 0) AS rating_count, attempt.completed_at AS last_tested_at,
				gateway_metrics.success_rate::numeric AS success_rate,
				gateway_metrics.ttft_milliseconds,
				gateway_metrics.tokens_per_second::numeric AS tokens_per_second,
				gateway_metrics.call_count,
				(owner.status = 'active' AND NOT owner.must_change_password
					AND c.status = 'published' AND m.status = 'active' AND o.status = 'active'
					AND m.input_price_nano_per_million BETWEEN 0 AND 100000000000000
					AND m.output_price_nano_per_million BETWEEN 0 AND 100000000000000
					AND m.cache_write_price_nano_per_million BETWEEN 0 AND 100000000000000
					AND m.cache_read_price_nano_per_million BETWEEN 0 AND 100000000000000
					AND COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano) BETWEEN 0 AND 1000000000000
					AND credential.channel_id IS NOT NULL AND attempt.status = 'passed') AS eligible
			FROM channel_offers o
			JOIN channel_models cm ON cm.id = o.channel_model_id
			JOIN channels c ON c.id = cm.channel_id
			JOIN accounts owner ON owner.id = c.owner_account_id
			JOIN models m ON m.id = cm.model_id
			LEFT JOIN channel_credentials credential
				ON credential.channel_id = c.id AND credential.credential_version = c.credential_version
			LEFT JOIN channel_validation_attempts attempt
				ON attempt.offer_id = o.id AND attempt.validation_version = o.validation_version
				AND attempt.attempt_seq = o.validation_attempt_seq
			LEFT JOIN LATERAL (
				SELECT round(avg(score)::numeric, 2) AS average_rating, count(*) AS rating_count
				FROM channel_ratings WHERE channel_id = c.id
			) rating ON true
			` + offerGatewayMetricJoins + `
		)
		SELECT offer_id::text, channel_id::text, channel_name, owner_account_id::text, owner_name,
			model_id, model_name, model_provider, protocol, multiplier_nano,
			input_price_nano, output_price_nano, cache_write_price_nano, cache_read_price_nano,
			COALESCE(average_rating::text, ''), rating_count, last_tested_at,
			success_rate::text, ttft_milliseconds, tokens_per_second::text, call_count
		FROM candidates
		WHERE eligible
			AND ($1 = '' OR model_id = $1)
			AND ($2 = '' OR protocol = $2)
			AND ($3 = '' OR owner_name ILIKE '%' || $3 || '%')
			AND ` + afterExpression + `
		ORDER BY ` + sortExpression + ` LIMIT ` + limitPlaceholder
	arguments := []any{query.ModelID, query.Protocol, query.OwnerQuery, cursor.OfferID, cursorValue}
	if query.Sort == "rating" {
		arguments = append(arguments, cursor.RatingCount)
	}
	arguments = append(arguments, query.Limit+1)
	rows, err := s.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, "", mapChannelError(err)
	}
	defer rows.Close()
	items := make([]channel.MarketOffer, 0, query.Limit+1)
	for rows.Next() {
		var item channel.MarketOffer
		var protocol string
		var multiplier, inputPrice, outputPrice, cacheWritePrice, cacheReadPrice int64
		var rating string
		var lastTestedAt sql.NullTime
		var successRate, tokensPerSecond sql.NullString
		var ttft, callCount sql.NullInt64
		if err := rows.Scan(
			&item.OfferID, &item.ChannelID, &item.ChannelDisplayName, &item.OwnerAccountID, &item.OwnerDisplayName,
			&item.ModelID, &item.ModelName, &item.ModelProvider, &protocol, &multiplier,
			&inputPrice, &outputPrice, &cacheWritePrice, &cacheReadPrice, &rating, &item.RatingCount, &lastTestedAt,
			&successRate, &ttft, &tokensPerSecond, &callCount,
		); err != nil {
			return nil, "", err
		}
		item.Protocol = channel.Protocol(protocol)
		item.Multiplier = money.FromNano(multiplier)
		item.InputPrice = money.FromNano(inputPrice)
		item.OutputPrice = money.FromNano(outputPrice)
		item.CacheWritePrice = money.FromNano(cacheWritePrice)
		item.CacheReadPrice = money.FromNano(cacheReadPrice)
		item.ValidationStatus = channel.ValidationPassed
		if rating != "" {
			item.AverageRating = &rating
		}
		if lastTestedAt.Valid {
			value := lastTestedAt.Time
			item.LastTestedAt = &value
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
		if callCount.Valid {
			value := callCount.Int64
			item.CallCount = &value
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(items) > 0 {
		modelIDs := make([]string, 0, len(items))
		for _, item := range items {
			modelIDs = append(modelIDs, item.ModelID)
		}
		tiers, tierErr := loadModelPriceTiers(ctx, s.pool, modelIDs)
		if tierErr != nil {
			return nil, "", tierErr
		}
		for index := range items {
			items[index].PriceTiers = tiers[items[index].ModelID]
		}
	}
	next := ""
	if len(items) > query.Limit {
		last := items[query.Limit-1]
		nextCursor := marketCursor{
			Sort: query.Sort, ModelID: query.ModelID, Protocol: query.Protocol, OwnerQuery: query.OwnerQuery,
			OfferID: last.OfferID, Rating: last.AverageRating, RatingCount: last.RatingCount,
		}
		switch query.Sort {
		case "output_price":
			nextCursor.PriceNano = last.OutputPrice.Nano()
		case "cache_write_price":
			nextCursor.PriceNano = last.CacheWritePrice.Nano()
		case "cache_read_price":
			nextCursor.PriceNano = last.CacheReadPrice.Nano()
		case "success_rate":
			nextCursor.Metric = last.CallSuccessRate
		case "ttft":
			if last.TTFTMilliseconds != nil {
				value := strconv.FormatInt(*last.TTFTMilliseconds, 10)
				nextCursor.Metric = &value
			}
		case "tps":
			nextCursor.Metric = last.TokensPerSecond
		default:
			nextCursor.PriceNano = last.InputPrice.Nano()
		}
		var err error
		next, err = encodeMarketCursor(nextCursor)
		if err != nil {
			return nil, "", err
		}
		items = items[:query.Limit]
	}
	return items, next, nil
}

func (s *Store) GetMarketChannel(ctx context.Context, viewerID, channelID string) (channel.Channel, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return channel.Channel{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	result, err := getMarketChannel(ctx, tx, viewerID, channelID)
	if err != nil {
		return channel.Channel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return channel.Channel{}, err
	}
	return result, nil
}

func getMarketChannel(ctx context.Context, queryer channelQueryer, viewerID, channelID string) (channel.Channel, error) {
	result, err := getChannel(ctx, queryer, channelID, viewerID)
	if err != nil {
		return channel.Channel{}, err
	}
	if result.Status != channel.StatusPublished && result.Status != channel.StatusPaused {
		return channel.Channel{}, channel.ErrNotFound
	}
	eligible := make([]channel.Offer, 0, len(result.Offers))
	if result.Status == channel.StatusPublished && result.OwnerStatus == identity.StatusActive && !result.OwnerMustChangePassword && result.CredentialConfigured {
		for _, offer := range result.Offers {
			if offer.Status == channel.OfferActive && offer.ModelStatus == catalog.StatusActive && offer.LatestValidation != nil && offer.LatestValidation.Status == channel.ValidationPassed {
				eligible = append(eligible, offer)
			}
		}
	}
	result.Offers = eligible
	return result, nil
}

func (s *Store) UpsertRating(ctx context.Context, accountID, channelID string, score int) (channel.Channel, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return channel.Channel{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var lockedChannelID string
	if err := tx.QueryRow(ctx, `
		SELECT c.id::text
		FROM channels c JOIN accounts actor ON actor.id = $2
		WHERE c.id = $1 AND c.status IN ('published', 'paused')
			AND actor.status = 'active' AND NOT actor.must_change_password
		FOR UPDATE OF c`, channelID, accountID).Scan(&lockedChannelID); err != nil {
		return channel.Channel{}, mapChannelError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_ratings (channel_id, account_id, score)
		VALUES ($1, $2, $3)
		ON CONFLICT (channel_id, account_id) DO UPDATE SET score = EXCLUDED.score, updated_at = now()`, lockedChannelID, accountID, score); err != nil {
		return channel.Channel{}, mapChannelError(err)
	}
	if err := insertAudit(ctx, tx, accountID, "channel.rating_upserted", "channel", channelID, "account holder set channel rating", map[string]any{"score": score}); err != nil {
		return channel.Channel{}, err
	}
	updated, err := getMarketChannel(ctx, tx, accountID, channelID)
	if err != nil {
		return channel.Channel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return channel.Channel{}, err
	}
	return updated, nil
}

func (s *Store) ListAdminChannels(ctx context.Context) ([]channel.Channel, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM channels ORDER BY updated_at DESC, id`)
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
	result := make([]channel.Channel, 0, len(ids))
	for _, id := range ids {
		value, err := getChannel(ctx, s.pool, id, "")
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *Store) GetAdminChannel(ctx context.Context, channelID string) (channel.Channel, error) {
	return getChannel(ctx, s.pool, channelID, "")
}

func (s *Store) CredentialInventory(ctx context.Context) ([]channel.ReencryptTarget, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT credential.channel_id::text, credential.credential_version,
			credential.key_id, credential.nonce, credential.ciphertext
		FROM channel_credentials credential
		JOIN channels c ON c.id = credential.channel_id
		WHERE c.status <> 'deleted'
		ORDER BY credential.channel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]channel.ReencryptTarget, 0)
	for rows.Next() {
		var target channel.ReencryptTarget
		if err := rows.Scan(&target.ChannelID, &target.Credential.Version, &target.Credential.KeyID, &target.Credential.Nonce, &target.Credential.Ciphertext); err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	return result, rows.Err()
}

func (s *Store) CredentialTargetsForReencrypt(ctx context.Context, activeKeyID string, limit int) ([]channel.ReencryptTarget, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT credential.channel_id::text, credential.credential_version,
			credential.key_id, credential.nonce, credential.ciphertext
		FROM channel_credentials credential
		JOIN channels c ON c.id = credential.channel_id
		WHERE credential.key_id <> $1 AND c.status <> 'deleted'
		ORDER BY credential.updated_at, credential.channel_id
		LIMIT $2`, activeKeyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]channel.ReencryptTarget, 0)
	for rows.Next() {
		var target channel.ReencryptTarget
		if err := rows.Scan(&target.ChannelID, &target.Credential.Version, &target.Credential.KeyID, &target.Credential.Nonce, &target.Credential.Ciphertext); err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	return result, rows.Err()
}

func (s *Store) StoreReencryptedCredential(ctx context.Context, target channel.ReencryptTarget, credential channel.EncryptedCredential, actorID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var channelVersion int64
	if err := tx.QueryRow(ctx, `
		SELECT c.version FROM channels c
		JOIN accounts actor ON actor.id = $2
		WHERE c.id = $1 AND c.status <> 'deleted' AND actor.status = 'active' AND actor.is_admin
		FOR UPDATE OF c`, target.ChannelID, actorID).Scan(&channelVersion); err != nil {
		return mapChannelError(err)
	}
	result, err := tx.Exec(ctx, `
		UPDATE channel_credentials SET key_id = $3, nonce = $4, ciphertext = $5, updated_at = now()
		WHERE channel_id = $1 AND credential_version = $2
			AND key_id = $6 AND nonce = $7 AND ciphertext = $8`,
		target.ChannelID, credential.Version, credential.KeyID, credential.Nonce, credential.Ciphertext,
		target.Credential.KeyID, target.Credential.Nonce, target.Credential.Ciphertext)
	if err != nil {
		return mapChannelError(err)
	}
	if result.RowsAffected() != 1 {
		return channel.ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE channels SET version = version + 1, updated_at = now() WHERE id = $1`, target.ChannelID); err != nil {
		return err
	}
	if err := insertAudit(ctx, tx, actorID, "channel.credential_reencrypted", "channel", target.ChannelID, "administrator reencrypted credential with active platform key", map[string]any{
		"credential_version": credential.Version, "key_id": credential.KeyID,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ResolveRoutingTargets(ctx context.Context, offerIDs []string) ([]channel.PoolOfferStatus, []channel.RoutingTarget, error) {
	return resolveRoutingTargets(ctx, s.pool, offerIDs)
}

func (t *LedgerTransaction) ResolveRoutingTargets(ctx context.Context, offerIDs []string) ([]channel.PoolOfferStatus, []channel.RoutingTarget, error) {
	return resolveRoutingTargets(ctx, t.Tx, offerIDs)
}

func resolveRoutingTargets(ctx context.Context, queryer channelQueryer, offerIDs []string) ([]channel.PoolOfferStatus, []channel.RoutingTarget, error) {
	statuses := make([]channel.PoolOfferStatus, 0, len(offerIDs))
	targets := make([]channel.RoutingTarget, 0, len(offerIDs))
	for _, offerID := range offerIDs {
		var status channel.PoolOfferStatus
		status.OfferID = offerID
		var protocol, ownerStatus, channelStatus, offerStatus, modelStatus string
		var ownerMustChangePassword bool
		var multiplier, inputPrice, outputPrice, cacheWritePrice, cacheReadPrice int64
		var validationStatus, keyID sql.NullString
		var credentialVersion sql.NullInt64
		var nonce, ciphertext []byte
		var baseURL, upstreamModelID string
		var averageRating string
		var contextWindow int64
		var validationVersion int64
		err := queryer.QueryRow(ctx, `
			SELECT cm.channel_id::text, c.display_name, c.owner_account_id::text, owner.display_name,
				owner.status, owner.must_change_password, c.status, cm.model_id, m.name, m.provider, m.status,
				o.protocol, o.status, cm.multiplier_nano, o.validation_version,
				m.context_window, m.input_price_nano_per_million, m.output_price_nano_per_million,
				m.cache_write_price_nano_per_million, m.cache_read_price_nano_per_million,
				attempt.status, credential.credential_version, credential.key_id,
				credential.nonce, credential.ciphertext, c.normalized_base_url, o.upstream_model_id,
				COALESCE(rating.average_rating::text, ''), COALESCE(rating.rating_count, 0)
			FROM channel_offers o
			JOIN channel_models cm ON cm.id = o.channel_model_id
			JOIN channels c ON c.id = cm.channel_id
			JOIN accounts owner ON owner.id = c.owner_account_id
			JOIN models m ON m.id = cm.model_id
			LEFT JOIN channel_credentials credential
				ON credential.channel_id = c.id AND credential.credential_version = c.credential_version
			LEFT JOIN channel_validation_attempts attempt
				ON attempt.offer_id = o.id AND attempt.validation_version = o.validation_version
				AND attempt.attempt_seq = o.validation_attempt_seq
			LEFT JOIN LATERAL (
				SELECT round(avg(score)::numeric, 2) AS average_rating, count(*) AS rating_count
				FROM channel_ratings WHERE channel_id = c.id
			) rating ON true
			WHERE o.id = $1`, offerID).Scan(
			&status.ChannelID, &status.ChannelDisplayName, &status.OwnerAccountID, &status.OwnerDisplayName,
			&ownerStatus, &ownerMustChangePassword, &channelStatus, &status.ModelID, &status.ModelName, &status.ModelProvider, &modelStatus,
			&protocol, &offerStatus, &multiplier, &validationVersion,
			&contextWindow, &inputPrice, &outputPrice, &cacheWritePrice, &cacheReadPrice,
			&validationStatus, &credentialVersion, &keyID, &nonce, &ciphertext, &baseURL, &upstreamModelID,
			&averageRating, &status.RatingCount,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			status.Eligible = false
			status.IneligibleReason = "not_found"
			statuses = append(statuses, status)
			continue
		}
		if err != nil {
			return nil, nil, mapChannelError(err)
		}
		status.Protocol = channel.Protocol(protocol)
		if averageRating != "" {
			status.AverageRating = &averageRating
		}
		status.Multiplier = money.FromNano(multiplier)
		prices, priceErr := channel.CalculateBenchmarkPrices(channel.Offer{
			Multiplier: money.FromNano(multiplier), InputPrice: money.FromNano(inputPrice), OutputPrice: money.FromNano(outputPrice),
			CacheWritePrice: money.FromNano(cacheWritePrice), CacheReadPrice: money.FromNano(cacheReadPrice),
		})
		if priceErr == nil {
			status.InputPrice, status.OutputPrice = prices.Input, prices.Output
			status.CacheWritePrice, status.CacheReadPrice = prices.CacheWrite, prices.CacheRead
		}
		status.ValidationStatus = channel.ValidationStatus(validationStatus.String)
		status.Eligible, status.IneligibleReason = routingEligibility(
			identity.Status(ownerStatus), ownerMustChangePassword, channel.Status(channelStatus), channel.OfferStatus(offerStatus),
			catalog.Status(modelStatus), validationStatus.String, credentialVersion.Valid,
		)
		if priceErr != nil {
			status.Eligible = false
			status.IneligibleReason = "price_unrepresentable"
		} else if _, upperErr := gateway.ConservativeNetDebitUpperBound(channel.RoutingLease{
			ContextWindow: contextWindow, Multiplier: money.FromNano(multiplier),
			InputPrice: money.FromNano(inputPrice), OutputPrice: money.FromNano(outputPrice),
			CacheWritePrice: money.FromNano(cacheWritePrice), CacheReadPrice: money.FromNano(cacheReadPrice),
		}, ledger.FixedPointScale, false); upperErr != nil {
			status.Eligible = false
			status.IneligibleReason = "price_unrepresentable"
		}
		statuses = append(statuses, status)
		if !status.Eligible {
			continue
		}
		targets = append(targets, channel.RoutingTarget{
			Lease: channel.RoutingLease{
				OfferID: offerID, ChannelID: status.ChannelID, ProviderAccountID: status.OwnerAccountID,
				ModelID: status.ModelID, Protocol: status.Protocol, Multiplier: status.Multiplier,
				ValidationVersion: validationVersion, CredentialVersion: credentialVersion.Int64,
				ContextWindow: contextWindow, InputPrice: money.FromNano(inputPrice), OutputPrice: money.FromNano(outputPrice),
				CacheWritePrice: money.FromNano(cacheWritePrice), CacheReadPrice: money.FromNano(cacheReadPrice),
				NormalizedBaseURL: baseURL, UpstreamModelID: upstreamModelID,
			},
			Credential: channel.EncryptedCredential{Version: credentialVersion.Int64, KeyID: keyID.String, Nonce: nonce, Ciphertext: ciphertext},
		})
	}
	// All resolved offers of one pool share the model, but resolve generically:
	// load every referenced model's conditional tiers once and attach them so
	// the routing lease snapshot and the pool display carry the same facts.
	if len(targets) > 0 {
		modelIDs := make([]string, 0, len(targets))
		for _, target := range targets {
			modelIDs = append(modelIDs, target.Lease.ModelID)
		}
		tiers, tierErr := loadModelPriceTiers(ctx, queryer, modelIDs)
		if tierErr != nil {
			return nil, nil, tierErr
		}
		for index := range targets {
			targets[index].Lease.PriceTiers = tiers[targets[index].Lease.ModelID]
		}
		for index := range statuses {
			statuses[index].PriceTiers = tiers[statuses[index].ModelID]
		}
	}
	return statuses, targets, nil
}

func routingEligibility(owner identity.Status, ownerMustChangePassword bool, channelStatus channel.Status, offerStatus channel.OfferStatus, modelStatus catalog.Status, validationStatus string, credentialConfigured bool) (bool, string) {
	switch {
	case owner != identity.StatusActive:
		return false, "owner_inactive"
	case ownerMustChangePassword:
		return false, "owner_password_change_required"
	case channelStatus != channel.StatusPublished:
		return false, "channel_unpublished"
	case !credentialConfigured:
		return false, "credential_unavailable"
	case modelStatus != catalog.StatusActive:
		return false, "model_inactive"
	case offerStatus != channel.OfferActive:
		return false, "offer_inactive"
	case validationStatus != string(channel.ValidationPassed):
		return false, "validation_required"
	default:
		return true, ""
	}
}

func mapChannelError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return channel.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return channel.ErrConflict
		case "23503", "23514":
			return channel.ErrInvalidInput
		case "22P02":
			return channel.ErrNotFound
		}
	}
	return fmt.Errorf("channel store: %w", err)
}
