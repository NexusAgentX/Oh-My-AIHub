package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func (s *Store) ListCalls(ctx context.Context, actor identity.Account, limit int) ([]gateway.Call, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT call.id::text, call.created_at
		FROM api_calls call
		LEFT JOIN api_call_attempts attempt ON attempt.call_id = call.id
		WHERE $2 OR call.consumer_account_id = $1 OR attempt.provider_account_id = $1
		ORDER BY call.created_at DESC, call.id::text DESC LIMIT $3`, actor.ID, actor.IsAdmin, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		var createdAt time.Time
		if err := rows.Scan(&id, &createdAt); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := make([]gateway.Call, 0, len(ids))
	for _, id := range ids {
		item, err := loadCall(ctx, s.pool, id, actor.ID, actor.IsAdmin)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) GetCall(ctx context.Context, actor identity.Account, callID string) (gateway.Call, error) {
	return loadCall(ctx, s.pool, callID, actor.ID, actor.IsAdmin)
}

func loadCall(ctx context.Context, queryer gatewayQueryer, callID, viewerID string, administrator bool) (gateway.Call, error) {
	var result gateway.Call
	var protocol, status string
	var poolID, holdID, finalOfferID, finalChannelName sql.NullString
	var poolVersion sql.NullInt64
	var inputTokens, outputTokens, cacheWriteTokens, cacheReadTokens sql.NullInt64
	var finalHTTPStatus sql.NullInt64
	var completedAt sql.NullTime
	var preauthorized, providerCharge, platformFee int64
	err := queryer.QueryRow(ctx, `
		SELECT call.id::text, call.consumer_account_id::text, call.api_key_id::text, call.key_prefix,
			call.key_generation, call.pool_id::text, call.pool_version, call.canonical_model_id, call.protocol,
			call.status, call.decision_code, call.candidate_count, call.upstream_attempt_count,
			call.hold_id::text, call.preauthorized_nano, call.zero_hold_reason,
			call.fee_rate_version, call.fee_rate_nano, call.lease_generation, call.final_offer_id::text,
			final_channel.display_name, call.completion_reason,
			call.input_tokens, call.output_tokens, call.cache_write_tokens, call.cache_read_tokens,
			call.provider_charge_nano, call.platform_fee_nano, call.final_http_status,
			call.created_at, call.completed_at
		FROM api_calls call
		LEFT JOIN channel_offers final_offer ON final_offer.id = call.final_offer_id
		LEFT JOIN channel_models final_model ON final_model.id = final_offer.channel_model_id
		LEFT JOIN channels final_channel ON final_channel.id = final_model.channel_id
		WHERE call.id = $1 AND ($3 OR call.consumer_account_id = $2 OR EXISTS (
			SELECT 1 FROM api_call_attempts attempt
			WHERE attempt.call_id = call.id AND attempt.provider_account_id = $2
		))`, callID, viewerID, administrator).Scan(
		&result.ID, &result.ConsumerAccountID, &result.APIKeyID, &result.KeyPrefix,
		&result.KeyGeneration, &poolID, &poolVersion, &result.CanonicalModelID, &protocol,
		&status, &result.DecisionCode, &result.CandidateCount, &result.UpstreamAttemptCount,
		&holdID, &preauthorized, &result.ZeroHoldReason,
		&result.FeeRateVersion, &result.FeeRateNano, &result.LeaseGeneration, &finalOfferID,
		&finalChannelName, &result.CompletionReason,
		&inputTokens, &outputTokens, &cacheWriteTokens, &cacheReadTokens,
		&providerCharge, &platformFee, &finalHTTPStatus,
		&result.CreatedAt, &completedAt,
	)
	if err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	result.Protocol = channelProtocol(protocol)
	result.Status = gateway.CallStatus(status)
	result.PoolID, result.HoldID, result.FinalOfferID = poolID.String, holdID.String, finalOfferID.String
	result.PoolVersion = poolVersion.Int64
	result.FinalChannelName = finalChannelName.String
	result.Preauthorized = money.FromNano(preauthorized)
	result.ProviderCharge = money.FromNano(providerCharge)
	result.PlatformFee = money.FromNano(platformFee)
	if inputTokens.Valid && outputTokens.Valid && cacheWriteTokens.Valid && cacheReadTokens.Valid {
		usage := ledger.UsageV1{
			InputTokens: inputTokens.Int64, OutputTokens: outputTokens.Int64,
			CacheWriteTokens: cacheWriteTokens.Int64, CacheReadTokens: cacheReadTokens.Int64,
		}
		result.Usage = &usage
	}
	if finalHTTPStatus.Valid {
		result.FinalHTTPStatus = int(finalHTTPStatus.Int64)
	}
	if completedAt.Valid {
		value := completedAt.Time
		result.CompletedAt = &value
	}
	attempts, err := loadCallAttempts(ctx, queryer, callID, viewerID, administrator, viewerID == result.ConsumerAccountID)
	if err != nil {
		return gateway.Call{}, err
	}
	result.Attempts = attempts
	if !administrator && viewerID != result.ConsumerAccountID {
		finalProvider := false
		if result.Status == gateway.CallSucceeded {
			for _, attempt := range attempts {
				if attempt.ProviderAccountID == viewerID && attempt.OfferID == result.FinalOfferID && attempt.Status == gateway.AttemptSucceeded {
					finalProvider = true
					break
				}
			}
		}
		result.ConsumerAccountID = ""
		result.APIKeyID = ""
		result.KeyPrefix = ""
		result.KeyGeneration = 0
		result.PoolID = ""
		result.PoolVersion = 0
		result.HoldID = ""
		result.Preauthorized = 0
		result.ZeroHoldReason = ""
		result.FeeRateVersion = 0
		result.FeeRateNano = 0
		result.PlatformFee = 0
		result.CandidateCount = 0
		result.UpstreamAttemptCount = len(attempts)
		if !finalProvider {
			if len(attempts) > 0 {
				visible := attempts[len(attempts)-1]
				result.Status = providerCallStatus(visible.Status)
				result.DecisionCode = visible.ErrorCode
				result.CompletionReason = visible.ErrorCode
				result.CompletedAt = visible.CompletedAt
			}
			result.FinalOfferID = ""
			result.FinalChannelName = ""
			result.ProviderCharge = 0
			result.Usage = nil
			result.FinalHTTPStatus = 0
		}
	}
	return result, nil
}

func providerCallStatus(status gateway.AttemptStatus) gateway.CallStatus {
	switch status {
	case gateway.AttemptPendingDelivery:
		return gateway.CallPendingDelivery
	case gateway.AttemptSucceeded:
		return gateway.CallSucceeded
	case gateway.AttemptFailed:
		return gateway.CallFailed
	case gateway.AttemptCancelled:
		return gateway.CallCancelled
	case gateway.AttemptIncomplete:
		return gateway.CallIncomplete
	default:
		return gateway.CallInProgress
	}
}

func loadCallAttempts(ctx context.Context, queryer gatewayQueryer, callID, viewerID string, administrator, consumer bool) ([]gateway.Attempt, error) {
	rows, err := queryer.Query(ctx, `
		SELECT attempt.id::text, attempt.call_id::text, attempt.sequence, attempt.offer_id::text,
			channel.display_name, attempt.provider_account_id::text, attempt.status,
			attempt.http_status, attempt.error_code,
			CASE WHEN $3 OR $4 OR attempt.provider_account_id = $2 THEN attempt.raw_error ELSE '' END,
			CASE WHEN $3 OR $4 OR attempt.provider_account_id = $2 THEN attempt.raw_error_truncated ELSE false END,
			attempt.semantic_committed, attempt.ttft_milliseconds, attempt.duration_milliseconds,
			attempt.input_tokens, attempt.output_tokens, attempt.cache_write_tokens, attempt.cache_read_tokens,
			attempt.tokens_per_second_nano, call.lease_generation, attempt.started_at, attempt.completed_at
		FROM api_call_attempts attempt
		JOIN api_calls call ON call.id = attempt.call_id
		JOIN channel_offers offer ON offer.id = attempt.offer_id
		JOIN channel_models model ON model.id = offer.channel_model_id
		JOIN channels channel ON channel.id = model.channel_id
		WHERE attempt.call_id = $1 AND ($3 OR $4 OR attempt.provider_account_id = $2)
		ORDER BY attempt.sequence`, callID, viewerID, administrator, consumer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]gateway.Attempt, 0)
	for rows.Next() {
		var item gateway.Attempt
		var status string
		var httpStatus, ttft, duration, input, output, cacheWrite, cacheRead, tps sql.NullInt64
		var completed sql.NullTime
		if err := rows.Scan(
			&item.ID, &item.CallID, &item.Sequence, &item.OfferID, &item.ChannelDisplayName,
			&item.ProviderAccountID, &status, &httpStatus, &item.ErrorCode, &item.RawError,
			&item.RawErrorTruncated, &item.SemanticCommitted, &ttft, &duration,
			&input, &output, &cacheWrite, &cacheRead, &tps, &item.LeaseGeneration, &item.StartedAt, &completed,
		); err != nil {
			return nil, err
		}
		item.Status = gateway.AttemptStatus(status)
		if httpStatus.Valid {
			item.HTTPStatus = int(httpStatus.Int64)
		}
		if ttft.Valid {
			value := time.Duration(ttft.Int64) * time.Millisecond
			item.TTFT = &value
		}
		if duration.Valid {
			value := time.Duration(duration.Int64) * time.Millisecond
			item.Duration = &value
		}
		if tps.Valid {
			value := tps.Int64
			item.TokensPerSecondNano = &value
		}
		if input.Valid && output.Valid && cacheWrite.Valid && cacheRead.Valid {
			usage := ledger.UsageV1{InputTokens: input.Int64, OutputTokens: output.Int64, CacheWriteTokens: cacheWrite.Int64, CacheReadTokens: cacheRead.Int64}
			item.Usage = &usage
		}
		if completed.Valid {
			value := completed.Time
			item.CompletedAt = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) Dashboard(ctx context.Context, accountID string) (gateway.Dashboard, error) {
	var result gateway.Dashboard
	var consumerSpent, providerIncome int64
	err := s.pool.QueryRow(ctx, `
		SELECT
			COALESCE((SELECT sum(call.provider_charge_nano + call.platform_fee_nano)
				FROM api_calls call
				JOIN api_call_candidates candidate ON candidate.call_id = call.id AND candidate.offer_id = call.final_offer_id
				WHERE call.consumer_account_id = $1 AND call.status = 'succeeded'), 0),
			COALESCE((SELECT sum(call.provider_charge_nano)
				FROM api_calls call
				JOIN api_call_candidates candidate ON candidate.call_id = call.id AND candidate.offer_id = call.final_offer_id
				WHERE candidate.provider_account_id = $1 AND call.status = 'succeeded'), 0),
			(SELECT count(*) FROM api_keys WHERE owner_account_id = $1 AND status = 'active'),
			(SELECT count(*) FROM api_model_pools pool JOIN api_keys key ON key.id = pool.api_key_id
				WHERE key.owner_account_id = $1 AND key.status <> 'deleted' AND pool.status = 'active'),
			(SELECT count(*) FROM channel_offers offer
				JOIN channel_models model ON model.id = offer.channel_model_id
				JOIN channels channel ON channel.id = model.channel_id
				LEFT JOIN channel_validation_attempts attempt ON attempt.offer_id = offer.id
					AND attempt.validation_version = offer.validation_version AND attempt.attempt_seq = offer.validation_attempt_seq
				WHERE channel.owner_account_id = $1 AND channel.status = 'published' AND offer.status = 'active' AND attempt.status = 'passed'),
			(SELECT count(*) FROM channel_offers offer
				JOIN channel_models model ON model.id = offer.channel_model_id
				JOIN channels channel ON channel.id = model.channel_id
				LEFT JOIN channel_validation_attempts attempt ON attempt.offer_id = offer.id
					AND attempt.validation_version = offer.validation_version AND attempt.attempt_seq = offer.validation_attempt_seq
				WHERE channel.owner_account_id = $1 AND offer.status <> 'deleted'
					AND NOT (channel.status = 'published' AND offer.status = 'active' AND attempt.status = 'passed')),
			(SELECT count(*) FROM api_pool_members member
				JOIN api_model_pools pool ON pool.id = member.pool_id
				JOIN api_keys key ON key.id = pool.api_key_id
				JOIN channel_offers offer ON offer.id = member.offer_id
				JOIN channel_models channel_model ON channel_model.id = offer.channel_model_id
				JOIN channels channel ON channel.id = channel_model.channel_id
				JOIN accounts channel_owner ON channel_owner.id = channel.owner_account_id
				JOIN models catalog_model ON catalog_model.id = channel_model.model_id
				LEFT JOIN channel_credentials credential ON credential.channel_id = channel.id
					AND credential.credential_version = channel.credential_version
				LEFT JOIN channel_validation_attempts attempt ON attempt.offer_id = offer.id
					AND attempt.validation_version = offer.validation_version AND attempt.attempt_seq = offer.validation_attempt_seq
				WHERE key.owner_account_id = $1 AND key.status <> 'deleted' AND pool.status = 'active'
					AND (member.added_validation_version <> offer.validation_version
						OR channel_owner.status <> 'active' OR channel_owner.must_change_password
						OR channel.status <> 'published' OR catalog_model.status <> 'active'
						OR offer.status <> 'active' OR credential.channel_id IS NULL
						OR attempt.status IS DISTINCT FROM 'passed'))`, accountID).Scan(
		&consumerSpent, &providerIncome, &result.ActiveKeyCount, &result.PoolCount,
		&result.HealthyOfferCount, &result.UnhealthyOfferCount, &result.PendingItems,
	)
	if err != nil {
		return gateway.Dashboard{}, err
	}
	result.ConsumerSpent = money.FromNano(consumerSpent)
	result.ProviderIncome = money.FromNano(providerIncome)
	actor := identity.Account{ID: accountID, Status: identity.StatusActive}
	recent, err := s.ListCalls(ctx, actor, 5)
	if err != nil {
		return gateway.Dashboard{}, err
	}
	result.RecentCalls = recent
	return result, nil
}

func channelProtocol(value string) channel.Protocol {
	return channel.Protocol(value)
}
