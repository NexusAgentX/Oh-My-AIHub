package gatewaypg

import (
	"context"
	"errors"
	"math"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

func (s *Store) StartAttempt(ctx context.Context, callID string, candidate gateway.Candidate) (gateway.Attempt, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return gateway.Attempt{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := s.q.WithTx(tx)
	call, err := q.LockCallForAttempt(ctx, callID)
	if err != nil {
		return gateway.Attempt{}, mapGatewayError(err)
	}
	if call.Status != string(gateway.CallInProgress) || candidate.LeaseGeneration != call.LeaseGeneration {
		return gateway.Attempt{}, gateway.ErrConflict
	}
	stats, err := q.AttemptStats(ctx, callID)
	if err != nil {
		return gateway.Attempt{}, err
	}
	if stats.InProgressAttempts != 0 || stats.AnyCommitted || stats.AnySucceeded || int64(candidate.Priority) != stats.CompletedAttempts+1 {
		return gateway.Attempt{}, gateway.ErrConflict
	}
	providerID, err := q.GetCandidateProvider(ctx, GetCandidateProviderParams{
		CallID: callID, Priority: int32(candidate.Priority), OfferID: candidate.Lease.OfferID,
	})
	if err != nil {
		return gateway.Attempt{}, mapGatewayError(err)
	}
	row, err := q.InsertAttempt(ctx, InsertAttemptParams{CallID: callID, OfferID: candidate.Lease.OfferID, ProviderAccountID: providerID})
	if err != nil {
		return gateway.Attempt{}, mapGatewayError(err)
	}
	attempt := gateway.Attempt{
		ID: row.ID, CallID: row.CallID, Sequence: int(row.Sequence), OfferID: row.OfferID,
		ProviderAccountID: row.ProviderAccountID, Status: gateway.AttemptStatus(row.Status),
		StartedAt: row.StartedAt, LeaseGeneration: call.LeaseGeneration,
	}
	if err := q.BumpAttemptCount(ctx, BumpAttemptCountParams{
		ID: callID, LeaseDuration: gateway.DefaultLeaseDuration, LeaseGeneration: call.LeaseGeneration,
	}); err != nil {
		return gateway.Attempt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return gateway.Attempt{}, err
	}
	return attempt, nil
}

func (s *Store) CompleteAttempt(ctx context.Context, attemptID string, result gateway.AttemptResult) (gateway.Attempt, error) {
	if result.LeaseGeneration <= 0 || result.Status == gateway.AttemptInProgress || result.Status == gateway.AttemptPendingDelivery || result.Status == gateway.AttemptSucceeded {
		return gateway.Attempt{}, gateway.ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return gateway.Attempt{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	attempt, err := completeAttemptInTx(ctx, s.q.WithTx(tx), attemptID, result)
	if err != nil {
		return gateway.Attempt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return gateway.Attempt{}, mapGatewayError(err)
	}
	return attempt, nil
}

func completeAttemptInTx(ctx context.Context, q *Queries, attemptID string, result gateway.AttemptResult) (gateway.Attempt, error) {
	raw, truncated := normalizeRawError(result.RawError)
	truncated = truncated || len(result.RawError) > len(raw)
	errorCode := normalizeErrorCode(result.ErrorCode)
	durationMS := max(int64(0), result.Duration.Milliseconds())
	var ttftMS *int64
	measuredTTFT := int64(0)
	if result.TTFTObserved {
		measuredTTFT = max(int64(0), result.TTFT.Milliseconds())
		ttftMS = &measuredTTFT
	}
	var inputTokens, outputTokens, cacheWriteTokens, cacheReadTokens *int64
	tokensPerSecond := int64(0)
	if result.Usage != nil {
		inputTokens, outputTokens = &result.Usage.InputTokens, &result.Usage.OutputTokens
		cacheWriteTokens, cacheReadTokens = &result.Usage.CacheWriteTokens, &result.Usage.CacheReadTokens
		if result.MeasureTPS && result.TTFTObserved && durationMS > measuredTTFT {
			tokensPerSecond = calculateTPSNano(result.Usage.OutputTokens, durationMS-measuredTTFT)
		}
	}
	locked, err := q.LockAttemptWithCall(ctx, attemptID)
	if err != nil {
		return gateway.Attempt{}, mapGatewayError(err)
	}
	if locked.CallStatus != string(gateway.CallInProgress) || locked.AttemptStatus != string(gateway.AttemptInProgress) || result.LeaseGeneration != locked.LeaseGeneration {
		return gateway.Attempt{}, gateway.ErrConflict
	}
	row, err := q.CompleteAttempt(ctx, CompleteAttemptParams{
		ID: attemptID, Status: string(result.Status), HttpStatus: int32(result.HTTPStatus), ErrorCode: errorCode,
		RawError: raw, RawErrorTruncated: truncated, SemanticCommitted: result.SemanticCommitted,
		TtftMilliseconds: ttftMS, DurationMilliseconds: &durationMS,
		InputTokens: inputTokens, OutputTokens: outputTokens, CacheWriteTokens: cacheWriteTokens, CacheReadTokens: cacheReadTokens,
		TokensPerSecondNano: tokensPerSecond,
	})
	if isNoRows(err) {
		return gateway.Attempt{}, gateway.ErrConflict
	}
	if err != nil {
		return gateway.Attempt{}, mapGatewayError(err)
	}
	attempt := gateway.Attempt{
		ID: row.ID, CallID: row.CallID, Sequence: int(row.Sequence), OfferID: row.OfferID,
		ProviderAccountID: row.ProviderAccountID, Status: gateway.AttemptStatus(row.Status),
		ErrorCode: row.ErrorCode, RawError: row.RawError, RawErrorTruncated: row.RawErrorTruncated,
		SemanticCommitted: row.SemanticCommitted, TokensPerSecondNano: row.TokensPerSecondNano,
		StartedAt: row.StartedAt, CompletedAt: row.CompletedAt, LeaseGeneration: locked.LeaseGeneration,
		Usage: result.Usage,
	}
	if row.HttpStatus != nil {
		attempt.HTTPStatus = int(*row.HttpStatus)
	}
	attempt.TTFT = millisecondsPointer(row.TtftMilliseconds)
	attempt.Duration = millisecondsPointer(row.DurationMilliseconds)
	return attempt, nil
}

func (s *Store) MarkAttemptCommitted(ctx context.Context, attemptID string, observation gateway.AttemptCommitObservation) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := s.q.WithTx(tx)
	state, err := q.LockAttemptCommitState(ctx, attemptID)
	if err != nil {
		return mapGatewayError(err)
	}
	validActivePair := state.CallStatus == string(gateway.CallInProgress) && state.AttemptStatus == string(gateway.AttemptInProgress)
	validPendingPair := state.CallStatus == string(gateway.CallPendingDelivery) && state.AttemptStatus == string(gateway.AttemptPendingDelivery)
	if (!validActivePair && !validPendingPair) || observation.LeaseGeneration != state.LeaseGeneration {
		return gateway.ErrConflict
	}
	if state.SemanticCommitted {
		if err := tx.Commit(ctx); err != nil {
			return mapGatewayError(err)
		}
		return nil
	}
	if validActivePair {
		if err := q.MarkAttemptCommitted(ctx, attemptID); err != nil {
			return mapGatewayError(err)
		}
	} else {
		ttftMS := max(int64(0), observation.TTFT.Milliseconds())
		durationMS := max(ttftMS, observation.Duration.Milliseconds())
		tokensPerSecond := int64(0)
		if observation.MeasureTPS && state.OutputTokens != nil && durationMS > ttftMS {
			tokensPerSecond = calculateTPSNano(*state.OutputTokens, durationMS-ttftMS)
		}
		if err := q.MarkPendingAttemptCommitted(ctx, MarkPendingAttemptCommittedParams{
			ID: attemptID, TtftMilliseconds: &ttftMS, DurationMilliseconds: &durationMS, TokensPerSecondNano: tokensPerSecond,
		}); err != nil {
			return mapGatewayError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return mapGatewayError(err)
	}
	return nil
}

func (s *Store) HeartbeatCall(ctx context.Context, callID string, leaseGeneration int64) error {
	affected, err := s.q.HeartbeatCall(ctx, HeartbeatCallParams{
		ID: callID, LeaseDuration: gateway.DefaultLeaseDuration, LeaseGeneration: leaseGeneration,
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return gateway.ErrConflict
	}
	return nil
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func normalizeRawError(raw string) (string, bool) {
	normalized := strings.ReplaceAll(strings.ToValidUTF8(raw, "�"), "\x00", "")
	truncated := normalized != raw
	if len(normalized) <= gateway.MaxStoredRawErrorBytes {
		return normalized, truncated
	}
	cut := gateway.MaxStoredRawErrorBytes
	for cut > 0 && !utf8.RuneStart(normalized[cut]) {
		cut--
	}
	return normalized[:cut], true
}

func normalizeErrorCode(raw string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(strings.ToValidUTF8(raw, "�"), "\x00", ""))
	runes := []rune(normalized)
	if len(runes) > 128 {
		normalized = string(runes[:128])
	}
	return normalized
}

func calculateTPSNano(outputTokens, durationMilliseconds int64) int64 {
	if outputTokens <= 0 || durationMilliseconds <= 0 {
		return 0
	}
	value := new(big.Int).Mul(big.NewInt(outputTokens), big.NewInt(1_000_000_000_000))
	value.Quo(value, big.NewInt(durationMilliseconds))
	if !value.IsInt64() || value.Cmp(big.NewInt(math.MaxInt64)) > 0 {
		return math.MaxInt64
	}
	return value.Int64()
}
