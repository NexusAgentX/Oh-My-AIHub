package gatewaypg

import (
	"context"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func (s *Store) ListCalls(ctx context.Context, actor identity.Account, limit int) ([]gateway.Call, error) {
	rows, err := s.q.ListVisibleCallIDs(ctx, ListVisibleCallIDsParams{
		Administrator: actor.IsAdmin, ViewerID: actor.ID, MaxRows: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	items := make([]gateway.Call, 0, len(rows))
	for _, row := range rows {
		item, err := loadCall(ctx, s.pool, row.ID, actor.ID, actor.IsAdmin)
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

func (s *Store) Dashboard(ctx context.Context, accountID string) (gateway.Dashboard, error) {
	totals, err := s.q.DashboardTotals(ctx, accountID)
	if err != nil {
		return gateway.Dashboard{}, err
	}
	result := gateway.Dashboard{
		ConsumerSpent:               money.FromNano(totals.ConsumerSpent),
		ProviderIncome:              money.FromNano(totals.ProviderIncome),
		TodaySpent:                  money.FromNano(totals.TodaySpent),
		TodaySucceededCalls:         totals.TodaySucceededCalls,
		TodayExternalProviderIncome: money.FromNano(totals.TodayExternalIncome),
		ActiveKeyCount:              totals.ActiveKeyCount,
		PoolCount:                   totals.PoolCount,
		HealthyOfferCount:           totals.HealthyOfferCount,
		UnhealthyOfferCount:         totals.UnhealthyOfferCount,
		PendingItems:                totals.PendingItems,
	}
	actor := identity.Account{ID: accountID, Status: identity.StatusActive}
	recent, err := s.ListCalls(ctx, actor, 5)
	if err != nil {
		return gateway.Dashboard{}, err
	}
	result.RecentCalls = recent
	return result, nil
}

// loadCall reads one call with the attempts the viewer may see and applies the
// provider-side redaction. db may be the pool or an open transaction.
func loadCall(ctx context.Context, db DBTX, callID, viewerID string, administrator bool) (gateway.Call, error) {
	q := New(db)
	row, err := q.GetCall(ctx, GetCallParams{ID: callID, Administrator: administrator, ViewerID: viewerID})
	if err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	result := gateway.Call{
		ID: row.ID, ConsumerAccountID: row.ConsumerAccountID, APIKeyID: row.ApiKeyID, KeyPrefix: row.KeyPrefix,
		KeyGeneration: row.KeyGeneration, CanonicalModelID: row.CanonicalModelID,
		Protocol: channel.Protocol(row.Protocol), Status: gateway.CallStatus(row.Status),
		DecisionCode: row.DecisionCode, CandidateCount: int(row.CandidateCount),
		UpstreamAttemptCount: int(row.UpstreamAttemptCount), Preauthorized: row.PreauthorizedNano,
		ZeroHoldReason: row.ZeroHoldReason, FeeRateVersion: row.FeeRateVersion, FeeRateNano: row.FeeRateNano.Nano(),
		LeaseGeneration: row.LeaseGeneration, CompletionReason: row.CompletionReason,
		ProviderCharge: row.ProviderChargeNano, PlatformFee: row.PlatformFeeNano,
		SettledPriceTierSeq: int(row.SettledPriceTierSeq), CreatedAt: row.CreatedAt, CompletedAt: row.CompletedAt,
	}
	if row.PoolID != nil {
		result.PoolID = *row.PoolID
	}
	if row.PoolVersion != nil {
		result.PoolVersion = *row.PoolVersion
	}
	if row.HoldID != nil {
		result.HoldID = *row.HoldID
	}
	if row.FinalOfferID != nil {
		result.FinalOfferID = *row.FinalOfferID
	}
	if row.FinalChannelName != nil {
		result.FinalChannelName = *row.FinalChannelName
	}
	if row.InputTokens != nil && row.OutputTokens != nil && row.CacheWriteTokens != nil && row.CacheReadTokens != nil {
		result.Usage = &ledger.UsageV1{
			InputTokens: *row.InputTokens, OutputTokens: *row.OutputTokens,
			CacheWriteTokens: *row.CacheWriteTokens, CacheReadTokens: *row.CacheReadTokens,
		}
	}
	if row.FinalHttpStatus != nil {
		result.FinalHTTPStatus = int(*row.FinalHttpStatus)
	}
	attempts, err := loadCallAttempts(ctx, q, callID, viewerID, administrator, viewerID == result.ConsumerAccountID)
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

func loadCallAttempts(ctx context.Context, q *Queries, callID, viewerID string, administrator, consumer bool) ([]gateway.Attempt, error) {
	rows, err := q.ListCallAttempts(ctx, ListCallAttemptsParams{
		CallID: callID, ViewerID: viewerID, Administrator: administrator, Consumer: consumer,
	})
	if err != nil {
		return nil, err
	}
	items := make([]gateway.Attempt, 0, len(rows))
	for _, row := range rows {
		item := gateway.Attempt{
			ID: row.ID, CallID: row.CallID, Sequence: int(row.Sequence), OfferID: row.OfferID,
			ChannelDisplayName: row.ChannelDisplayName, ProviderAccountID: row.ProviderAccountID,
			LeaseGeneration: row.LeaseGeneration, Status: gateway.AttemptStatus(row.Status),
			ErrorCode: row.ErrorCode, RawError: row.RawError, RawErrorTruncated: row.RawErrorTruncated,
			SemanticCommitted: row.SemanticCommitted, TokensPerSecondNano: row.TokensPerSecondNano,
			StartedAt: row.StartedAt, CompletedAt: row.CompletedAt,
		}
		if row.HttpStatus != nil {
			item.HTTPStatus = int(*row.HttpStatus)
		}
		item.TTFT = millisecondsPointer(row.TtftMilliseconds)
		item.Duration = millisecondsPointer(row.DurationMilliseconds)
		if row.InputTokens != nil && row.OutputTokens != nil && row.CacheWriteTokens != nil && row.CacheReadTokens != nil {
			item.Usage = &ledger.UsageV1{
				InputTokens: *row.InputTokens, OutputTokens: *row.OutputTokens,
				CacheWriteTokens: *row.CacheWriteTokens, CacheReadTokens: *row.CacheReadTokens,
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func millisecondsPointer(value *int64) *time.Duration {
	if value == nil {
		return nil
	}
	duration := time.Duration(*value) * time.Millisecond
	return &duration
}
