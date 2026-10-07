package gatewaypg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
)

// FinalizeCall settles one call in a SERIALIZABLE transaction: it derives the
// final fact, charges or releases through the ledger on that same transaction,
// writes the settlement and moves the call to its (pending-delivery) outcome.
// Replays with the same outcome return the stored call.
func (s *Store) FinalizeCall(ctx context.Context, callID string, outcome gateway.FinalizeOutcome) (call gateway.Call, resultErr error) {
	defer func() { resultErr = mapGatewayError(resultErr) }()
	if outcome.LeaseGeneration <= 0 {
		return gateway.Call{}, gateway.ErrInvalidInput
	}
	if outcome.SuccessAttempt != nil {
		result := outcome.SuccessAttempt
		if outcome.Status != gateway.CallSucceeded || strings.TrimSpace(outcome.SuccessAttemptID) == "" ||
			result.Status != gateway.AttemptSucceeded || result.HTTPStatus < 200 || result.HTTPStatus >= 300 ||
			result.LeaseGeneration != outcome.LeaseGeneration || result.HTTPStatus != outcome.HTTPStatus || result.Usage == nil || !usageEqual(result.Usage, outcome.Usage) || result.ErrorCode != "" || result.RawError != "" ||
			(result.MeasureTPS && !result.TTFTObserved) {
			return gateway.Call{}, gateway.ErrInvalidInput
		}
	} else if outcome.SuccessAttemptID != "" {
		return gateway.Call{}, gateway.ErrInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return gateway.Call{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := s.q.WithTx(tx)
	ledgerService := ledger.NewService(ledgerpg.NewTx(tx))

	locked, err := q.LockCallForFinalize(ctx, callID)
	if err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	consumerID, preauthorized, currentStatus := locked.ConsumerAccountID, locked.PreauthorizedNano, locked.Status
	holdID := ""
	if locked.HoldID != nil {
		holdID = *locked.HoldID
	}
	if outcome.LeaseGeneration != locked.LeaseGeneration {
		return gateway.Call{}, gateway.ErrConflict
	}
	if currentStatus != string(gateway.CallInProgress) {
		if currentStatus != string(gateway.CallPendingDelivery) && currentStatus != string(gateway.CallSucceeded) {
			return gateway.Call{}, gateway.ErrConflict
		}
		current, err := loadCall(ctx, tx, callID, consumerID, false)
		if err != nil {
			return gateway.Call{}, err
		}
		if outcome.Status != "" && outcome.Status != gateway.CallSucceeded {
			return gateway.Call{}, gateway.ErrConflict
		}
		if outcome.CompletionReason != "" && outcome.CompletionReason != current.CompletionReason {
			return gateway.Call{}, gateway.ErrConflict
		}
		if outcome.FinalOfferID != "" && outcome.FinalOfferID != current.FinalOfferID {
			return gateway.Call{}, gateway.ErrConflict
		}
		if outcome.HTTPStatus != 0 && outcome.HTTPStatus != current.FinalHTTPStatus {
			return gateway.Call{}, gateway.ErrConflict
		}
		if outcome.Usage != nil && !usageEqual(outcome.Usage, current.Usage) {
			return gateway.Call{}, gateway.ErrConflict
		}
		if outcome.SuccessAttempt != nil {
			matches, err := successfulAttemptReplayMatches(ctx, q, callID, current, outcome.SuccessAttemptID, *outcome.SuccessAttempt)
			if err != nil {
				return gateway.Call{}, err
			}
			if !matches {
				return gateway.Call{}, gateway.ErrConflict
			}
		}
		expectedHash := gateway.FinalizerHash(gateway.CallSucceeded, current.CompletionReason, current.FinalOfferID, current.FinalHTTPStatus, current.Usage)
		if !bytes.Equal(locked.FinalizerPayloadHash, expectedHash[:]) {
			return gateway.Call{}, gateway.ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return gateway.Call{}, err
		}
		return current, nil
	}
	if outcome.Status == gateway.CallSucceeded && outcome.SuccessAttempt == nil {
		return gateway.Call{}, gateway.ErrInvalidInput
	}
	if outcome.SuccessAttempt != nil {
		pendingResult := *outcome.SuccessAttempt
		pendingResult.Status = gateway.AttemptPendingDelivery
		attempt, err := completeAttemptInTx(ctx, q, outcome.SuccessAttemptID, pendingResult)
		if err != nil {
			return gateway.Call{}, err
		}
		if attempt.CallID != callID {
			return gateway.Call{}, gateway.ErrConflict
		}
	}

	var fact finalizationFact
	if outcome.SuccessAttempt != nil {
		fact = finalizationFact{
			Status: gateway.CallSucceeded, Reason: "completed", OfferID: outcome.FinalOfferID,
			HTTPStatus: outcome.HTTPStatus, Usage: outcome.SuccessAttempt.Usage,
		}
	} else {
		fact, err = deriveFinalizationFact(ctx, q, callID)
		if err != nil {
			return gateway.Call{}, err
		}
	}
	if outcome.Status != "" && outcome.Status != fact.Status {
		return gateway.Call{}, gateway.ErrConflict
	}
	if outcome.CompletionReason != "" && outcome.CompletionReason != fact.Reason {
		return gateway.Call{}, gateway.ErrConflict
	}
	if outcome.FinalOfferID != "" && outcome.FinalOfferID != fact.OfferID {
		return gateway.Call{}, gateway.ErrConflict
	}
	if outcome.HTTPStatus != 0 && outcome.HTTPStatus != fact.HTTPStatus {
		return gateway.Call{}, gateway.ErrConflict
	}
	if outcome.Usage != nil && !usageEqual(outcome.Usage, fact.Usage) {
		return gateway.Call{}, gateway.ErrConflict
	}
	finalizerHash := gateway.FinalizerHash(fact.Status, fact.Reason, fact.OfferID, fact.HTTPStatus, fact.Usage)

	providerCharge, platformFee := money.Amount(0), money.Amount(0)
	providerID, captureTransactionID, selfTransactionID := "", "", ""
	settlementKind := "released"
	settledTierSeq := 0
	if fact.Status == gateway.CallSucceeded {
		if fact.Usage == nil || fact.OfferID == "" {
			return gateway.Call{}, gateway.ErrNoUsage
		}
		candidate, err := q.GetCandidateByOffer(ctx, GetCandidateByOfferParams{CallID: callID, OfferID: fact.OfferID})
		if err != nil {
			return gateway.Call{}, mapGatewayError(err)
		}
		lease := channel.RoutingLease{
			OfferID: candidate.OfferID, ChannelID: candidate.ChannelID, ProviderAccountID: candidate.ProviderAccountID,
			ValidationVersion: candidate.ValidationVersion, CredentialVersion: candidate.CredentialVersion,
			UpstreamModelID: candidate.UpstreamModelID, ContextWindow: candidate.ContextWindow,
			InputPrice: candidate.InputPriceNano, OutputPrice: candidate.OutputPriceNano,
			CacheWritePrice: candidate.CacheWritePriceNano, CacheReadPrice: candidate.CacheReadPriceNano,
			Multiplier: candidate.MultiplierNano,
		}
		selfChannel := candidate.SelfChannel
		providerID = lease.ProviderAccountID
		if err := gateway.ValidateUsage(*fact.Usage, lease.ContextWindow); err != nil {
			return gateway.Call{}, err
		}
		feeRateNano := locked.FeeRateNano.Nano()
		if locked.FormulaVersion == gateway.FormulaVersionV2 {
			// formula-v2 re-reads the call-level tier snapshot taken at BeginCall
			// and selects by prompt-side token volume and the call start time.
			tiers, tierErr := loadCallPriceTiers(ctx, q, callID)
			if tierErr != nil {
				return gateway.Call{}, tierErr
			}
			priceV2, v2Err := ledger.CalculatePriceV2(*fact.Usage, gateway.OfficialPrices(lease), tiers, locked.CreatedAt, lease.Multiplier.Nano(), feeRateNano, selfChannel)
			if v2Err != nil {
				return gateway.Call{}, v2Err
			}
			providerCharge, platformFee = priceV2.ProviderCharge, priceV2.PlatformFee
			settledTierSeq = priceV2.TierSeq
		} else {
			price, err := ledger.CalculatePriceV1(*fact.Usage, gateway.OfficialPrices(lease), lease.Multiplier.Nano(), feeRateNano, selfChannel)
			if err != nil {
				return gateway.Call{}, err
			}
			providerCharge, platformFee = price.ProviderCharge, price.PlatformFee
		}
		if selfChannel {
			if holdID != "" {
				if _, err := ledgerService.ReleaseHold(ctx, ledger.MutateHoldRequest{
					IdempotencyKey: "api-call-" + callID + "-release", HoldID: holdID,
					BusinessID: callID + ":release", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll},
					Reason: "release api authorization after self-channel success",
				}); err != nil && !errors.Is(err, ledger.ErrHoldClosed) {
					return gateway.Call{}, err
				}
			}
			if providerCharge > 0 {
				transaction, err := ledgerService.RecordSelfChannelUsage(ctx, "api-call-"+callID+"-self", consumerID, providerCharge, "api_call_self_usage", callID)
				if err != nil {
					return gateway.Call{}, err
				}
				selfTransactionID = transaction.ID
				settlementKind = "self_usage"
			} else {
				settlementKind = "zero"
			}
		} else {
			actual, err := ledger.Add(providerCharge, platformFee)
			if err != nil || actual > preauthorized {
				return gateway.Call{}, gateway.ErrConflict
			}
			if actual > 0 {
				credits := []ledger.Posting{{Account: ledger.UserAccount(providerID), BusinessRole: ledger.EntryRoleProvider, Amount: providerCharge}}
				if platformFee > 0 {
					credits = append(credits, ledger.Posting{Account: ledger.SystemAccount(ledger.AccountIncentive), BusinessRole: ledger.EntryRolePlatformFee, Amount: platformFee})
				}
				captured, err := ledgerService.CaptureHold(ctx, ledger.CaptureHoldRequest{
					MutateHoldRequest: ledger.MutateHoldRequest{
						IdempotencyKey: "api-call-" + callID + "-capture", HoldID: holdID,
						BusinessID: callID + ":capture", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: actual},
						Reason: "capture exact api call charge",
					},
					Credits: credits, ReferenceType: "api_call_charge", ReferenceID: callID,
				})
				if err != nil {
					return gateway.Call{}, err
				}
				captureTransactionID = captured.Transaction.ID
				settlementKind = "captured"
				if captured.Hold.Remaining > 0 {
					if _, err := ledgerService.ReleaseHold(ctx, ledger.MutateHoldRequest{
						IdempotencyKey: "api-call-" + callID + "-release", HoldID: holdID,
						BusinessID: callID + ":release", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll},
						Reason: "release unused api authorization",
					}); err != nil {
						return gateway.Call{}, err
					}
				}
			} else {
				settlementKind = "zero"
				if holdID != "" {
					if _, err := ledgerService.ReleaseHold(ctx, ledger.MutateHoldRequest{
						IdempotencyKey: "api-call-" + callID + "-release", HoldID: holdID,
						BusinessID: callID + ":release", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll},
						Reason: "release zero-cost api authorization",
					}); err != nil && !errors.Is(err, ledger.ErrHoldClosed) {
						return gateway.Call{}, err
					}
				}
			}
		}
	} else if holdID != "" {
		if _, err := ledgerService.ReleaseHold(ctx, ledger.MutateHoldRequest{
			IdempotencyKey: "api-call-" + callID + "-release", HoldID: holdID,
			BusinessID: callID + ":release", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll},
			Reason: "release api authorization without charge",
		}); err != nil && !errors.Is(err, ledger.ErrHoldClosed) {
			return gateway.Call{}, err
		}
	}

	if err := q.InsertSettlement(ctx, InsertSettlementParams{
		CallID: callID, Kind: settlementKind, ProviderAccountID: providerID,
		ProviderChargeNano: providerCharge, PlatformFeeNano: platformFee,
		CaptureTransactionID: captureTransactionID, SelfTransactionID: selfTransactionID, HoldID: holdID,
	}); err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	var inputTokens, outputTokens, cacheWriteTokens, cacheReadTokens *int64
	if fact.Usage != nil {
		usage := *fact.Usage
		inputTokens, outputTokens = &usage.InputTokens, &usage.OutputTokens
		cacheWriteTokens, cacheReadTokens = &usage.CacheWriteTokens, &usage.CacheReadTokens
	}
	storedStatus := fact.Status
	completedAt := new(time.Time)
	*completedAt = time.Now()
	if fact.Status == gateway.CallSucceeded {
		storedStatus = gateway.CallPendingDelivery
		completedAt = nil
	}
	if err := q.FinalizeCall(ctx, FinalizeCallParams{
		ID: callID, Status: string(storedStatus), Reason: fact.Reason, FinalOfferID: fact.OfferID,
		InputTokens: inputTokens, OutputTokens: outputTokens, CacheWriteTokens: cacheWriteTokens, CacheReadTokens: cacheReadTokens,
		ProviderChargeNano: providerCharge, PlatformFeeNano: platformFee, FinalHttpStatus: int32(fact.HTTPStatus),
		SettledPriceTierSeq: int32(settledTierSeq), LeaseDuration: gateway.DefaultLeaseDuration,
		FinalizerPayloadHash: finalizerHash[:], CompletedAt: completedAt, LeaseGeneration: outcome.LeaseGeneration,
	}); err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	completed, err := loadCall(ctx, tx, callID, consumerID, false)
	if err != nil {
		return gateway.Call{}, err
	}
	if commitErr := s.commit(ctx, tx, "api_call.finalize", callID); commitErr != nil {
		if recovered, recoverErr := s.recoverFinalizedCall(ctx, callID, consumerID, outcome, finalizerHash); recoverErr == nil {
			return recovered, nil
		}
		return gateway.Call{}, commitErr
	}
	return completed, nil
}

func (s *Store) recoverFinalizedCall(parent context.Context, callID, consumerID string, outcome gateway.FinalizeOutcome, expectedHash [32]byte) (gateway.Call, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), gateway.PersistenceTimeout)
	defer cancel()
	state, err := s.q.GetCallFinalizerState(ctx, callID)
	if err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	if state.LeaseGeneration != outcome.LeaseGeneration || !bytes.Equal(state.FinalizerPayloadHash, expectedHash[:]) {
		return gateway.Call{}, gateway.ErrConflict
	}
	if outcome.Status == gateway.CallSucceeded {
		if state.Status != string(gateway.CallPendingDelivery) && state.Status != string(gateway.CallSucceeded) {
			return gateway.Call{}, gateway.ErrConflict
		}
	} else if state.Status != string(outcome.Status) {
		return gateway.Call{}, gateway.ErrConflict
	}
	recovered, err := loadCall(ctx, s.pool, callID, consumerID, false)
	if err != nil {
		return gateway.Call{}, err
	}
	if outcome.SuccessAttempt != nil {
		matches, err := successfulAttemptReplayMatches(ctx, s.q, callID, recovered, outcome.SuccessAttemptID, *outcome.SuccessAttempt)
		if err != nil {
			return gateway.Call{}, err
		}
		if !matches {
			return gateway.Call{}, gateway.ErrConflict
		}
	}
	return recovered, nil
}

// ConfirmCallDelivery turns a pending-delivery call into a succeeded one once
// the downstream response is durably confirmed.
func (s *Store) ConfirmCallDelivery(ctx context.Context, callID string, leaseGeneration int64) (call gateway.Call, resultErr error) {
	defer func() { resultErr = mapGatewayError(resultErr) }()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return gateway.Call{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := s.q.WithTx(tx)
	locked, err := q.LockCallForDelivery(ctx, callID)
	if err != nil {
		return gateway.Call{}, err
	}
	consumerID := locked.ConsumerAccountID
	if locked.LeaseGeneration != leaseGeneration {
		return gateway.Call{}, gateway.ErrConflict
	}
	if locked.Status == string(gateway.CallSucceeded) {
		current, err := loadCall(ctx, tx, callID, consumerID, false)
		if err != nil {
			return gateway.Call{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return gateway.Call{}, err
		}
		return current, nil
	}
	if locked.Status != string(gateway.CallPendingDelivery) {
		return gateway.Call{}, gateway.ErrConflict
	}
	attemptRows, err := q.ConfirmPendingAttempt(ctx, callID)
	if err != nil {
		return gateway.Call{}, err
	}
	if attemptRows != 1 {
		return gateway.Call{}, gateway.ErrConflict
	}
	callRows, err := q.ConfirmPendingCall(ctx, ConfirmPendingCallParams{ID: callID, LeaseGeneration: leaseGeneration})
	if err != nil {
		return gateway.Call{}, err
	}
	if callRows != 1 {
		return gateway.Call{}, gateway.ErrConflict
	}
	confirmed, err := loadCall(ctx, tx, callID, consumerID, false)
	if err != nil {
		return gateway.Call{}, err
	}
	if commitErr := s.commit(ctx, tx, "api_call.confirm_delivery", callID); commitErr != nil {
		if recovered, recoverErr := s.recoverConfirmedCall(ctx, callID, consumerID, leaseGeneration); recoverErr == nil {
			return recovered, nil
		}
		return gateway.Call{}, commitErr
	}
	return confirmed, nil
}

func (s *Store) recoverConfirmedCall(parent context.Context, callID, consumerID string, leaseGeneration int64) (gateway.Call, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), gateway.PersistenceTimeout)
	defer cancel()
	recovered, err := loadCall(ctx, s.pool, callID, consumerID, false)
	if err != nil {
		return gateway.Call{}, err
	}
	if recovered.Status != gateway.CallSucceeded || recovered.LeaseGeneration != leaseGeneration {
		return gateway.Call{}, gateway.ErrConflict
	}
	return recovered, nil
}

// CompensateCallDelivery reverses a settled-but-undelivered call: the ledger
// reversal, compensation fact and call/attempt state change commit together.
func (s *Store) CompensateCallDelivery(ctx context.Context, callID string, leaseGeneration int64, reason string) (call gateway.Call, resultErr error) {
	defer func() { resultErr = mapGatewayError(resultErr) }()
	reason = normalizeErrorCode(reason)
	if reason == "" || leaseGeneration <= 0 {
		return gateway.Call{}, gateway.ErrInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return gateway.Call{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := s.q.WithTx(tx)
	locked, err := q.LockCallForCompensation(ctx, callID)
	if err != nil {
		return gateway.Call{}, err
	}
	consumerID := locked.ConsumerAccountID
	if locked.LeaseGeneration != leaseGeneration {
		return gateway.Call{}, gateway.ErrConflict
	}
	if locked.Status == string(gateway.CallIncomplete) {
		storedReason, err := q.GetCompensationReason(ctx, callID)
		if err != nil {
			return gateway.Call{}, mapGatewayError(err)
		}
		if storedReason != reason {
			return gateway.Call{}, gateway.ErrConflict
		}
		current, err := loadCall(ctx, tx, callID, consumerID, false)
		if err != nil {
			return gateway.Call{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return gateway.Call{}, err
		}
		return current, nil
	}
	if locked.Status != string(gateway.CallPendingDelivery) {
		return gateway.Call{}, gateway.ErrConflict
	}
	settlement, err := q.LockSettlement(ctx, callID)
	if err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	originalTransactionID := ""
	if settlement.CaptureTransactionID != nil {
		originalTransactionID = *settlement.CaptureTransactionID
	} else if settlement.SelfTransactionID != nil {
		originalTransactionID = *settlement.SelfTransactionID
	}
	reversalID := ""
	if originalTransactionID != "" {
		payloadHash := sha256.Sum256([]byte("api-call-delivery-reversal-v1\x00" + callID + "\x00" + originalTransactionID))
		reversal, err := ledgerpg.NewTx(tx).ReverseSystem(
			ctx, "api-call-"+callID+"-delivery-reversal", originalTransactionID,
			"reverse api charge after incomplete downstream delivery", callID+":delivery-compensation", payloadHash,
		)
		if err != nil {
			return gateway.Call{}, err
		}
		reversalID = reversal.ID
	}
	if err := q.InsertCompensation(ctx, InsertCompensationParams{
		CallID: callID, Reason: reason, OriginalTransactionID: originalTransactionID, ReversalTransactionID: reversalID,
		ProviderChargeReversedNano: settlement.ProviderChargeNano, PlatformFeeReversedNano: settlement.PlatformFeeNano,
	}); err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	attemptRows, err := q.MarkPendingAttemptIncomplete(ctx, MarkPendingAttemptIncompleteParams{CallID: callID, ErrorCode: reason})
	if err != nil {
		return gateway.Call{}, err
	}
	if attemptRows != 1 {
		return gateway.Call{}, gateway.ErrConflict
	}
	finalizerHash := gateway.FinalizerHash(gateway.CallIncomplete, reason, locked.FinalOfferID, int(locked.FinalHttpStatus), nil)
	callRows, err := q.MarkPendingCallIncomplete(ctx, MarkPendingCallIncompleteParams{
		ID: callID, Reason: reason, FinalizerPayloadHash: finalizerHash[:], LeaseGeneration: leaseGeneration,
	})
	if err != nil {
		return gateway.Call{}, err
	}
	if callRows != 1 {
		return gateway.Call{}, gateway.ErrConflict
	}
	compensated, err := loadCall(ctx, tx, callID, consumerID, false)
	if err != nil {
		return gateway.Call{}, err
	}
	if commitErr := s.commit(ctx, tx, "api_call.compensate_delivery", callID); commitErr != nil {
		if recovered, recoverErr := s.recoverCompensatedCall(ctx, callID, consumerID, leaseGeneration, reason); recoverErr == nil {
			return recovered, nil
		}
		return gateway.Call{}, commitErr
	}
	return compensated, nil
}

func (s *Store) recoverCompensatedCall(parent context.Context, callID, consumerID string, leaseGeneration int64, reason string) (gateway.Call, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), gateway.PersistenceTimeout)
	defer cancel()
	storedReason, err := s.q.GetCompensatedCallReason(ctx, GetCompensatedCallReasonParams{ID: callID, LeaseGeneration: leaseGeneration})
	if err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	if storedReason != reason {
		return gateway.Call{}, gateway.ErrConflict
	}
	return loadCall(ctx, s.pool, callID, consumerID, false)
}

type finalizationFact struct {
	Status     gateway.CallStatus
	Reason     string
	OfferID    string
	HTTPStatus int
	Usage      *ledger.UsageV1
}

func deriveFinalizationFact(ctx context.Context, q *Queries, callID string) (finalizationFact, error) {
	attempts, err := q.ListAttemptsForFinalization(ctx, callID)
	if err != nil {
		return finalizationFact{}, err
	}
	for index := range attempts {
		if attempts[index].Status != string(gateway.AttemptInProgress) {
			continue
		}
		reason := "orphan_recovered_before_commit"
		if attempts[index].SemanticCommitted {
			reason = "orphan_recovered_after_commit"
		}
		if err := q.MarkOrphanAttemptIncomplete(ctx, MarkOrphanAttemptIncompleteParams{
			ID: attempts[index].ID, ErrorCode: reason, RawError: "upstream attempt did not durably reach a complete result",
		}); err != nil {
			return finalizationFact{}, mapGatewayError(err)
		}
		attempts[index].Status = string(gateway.AttemptIncomplete)
		attempts[index].ErrorCode = reason
	}
	if len(attempts) == 0 {
		return finalizationFact{Status: gateway.CallIncomplete, Reason: "no_attempt_completed"}, nil
	}
	last := attempts[len(attempts)-1]
	httpStatus := int(last.HttpStatus)
	if last.Status == string(gateway.AttemptSucceeded) {
		if !last.SemanticCommitted || last.InputTokens == nil || last.OutputTokens == nil || last.CacheWriteTokens == nil || last.CacheReadTokens == nil {
			// The database constraint normally makes this state unreachable. Fail
			// safe if an externally repaired or corrupted row ever violates it:
			// incomplete calls release their authorization and never charge.
			return finalizationFact{Status: gateway.CallIncomplete, Reason: "invalid_success_attempt", OfferID: last.OfferID, HTTPStatus: httpStatus}, nil
		}
		usage := &ledger.UsageV1{
			InputTokens: *last.InputTokens, OutputTokens: *last.OutputTokens,
			CacheWriteTokens: *last.CacheWriteTokens, CacheReadTokens: *last.CacheReadTokens,
		}
		return finalizationFact{Status: gateway.CallSucceeded, Reason: "completed", OfferID: last.OfferID, HTTPStatus: httpStatus, Usage: usage}, nil
	}
	reason := coalesceGatewayReason(last.ErrorCode, "all_candidates_failed")
	switch last.Status {
	case string(gateway.AttemptCancelled):
		return finalizationFact{Status: gateway.CallCancelled, Reason: reason, OfferID: last.OfferID, HTTPStatus: httpStatus}, nil
	case string(gateway.AttemptIncomplete):
		return finalizationFact{Status: gateway.CallIncomplete, Reason: reason, OfferID: last.OfferID, HTTPStatus: httpStatus}, nil
	default:
		return finalizationFact{Status: gateway.CallFailed, Reason: reason, HTTPStatus: httpStatus}, nil
	}
}

func usageEqual(left, right *ledger.UsageV1) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func successfulAttemptReplayMatches(ctx context.Context, q *Queries, callID string, call gateway.Call, attemptID string, result gateway.AttemptResult) (bool, error) {
	stored, err := q.GetAttemptReplayFacts(ctx, attemptID)
	if err != nil {
		return false, mapGatewayError(err)
	}
	if stored.CallID != callID || stored.OfferID != call.FinalOfferID ||
		(stored.Status != string(gateway.AttemptPendingDelivery) && stored.Status != string(gateway.AttemptSucceeded)) ||
		stored.HttpStatus == nil || int(*stored.HttpStatus) != result.HTTPStatus || stored.ErrorCode != "" || stored.RawError != "" ||
		(stored.Status == string(gateway.AttemptPendingDelivery) && stored.SemanticCommitted != result.SemanticCommitted) ||
		(stored.Status == string(gateway.AttemptSucceeded) && !stored.SemanticCommitted) ||
		result.Usage == nil || stored.InputTokens == nil || stored.OutputTokens == nil || stored.CacheWriteTokens == nil || stored.CacheReadTokens == nil ||
		*stored.InputTokens != result.Usage.InputTokens || *stored.OutputTokens != result.Usage.OutputTokens ||
		*stored.CacheWriteTokens != result.Usage.CacheWriteTokens || *stored.CacheReadTokens != result.Usage.CacheReadTokens {
		return false, nil
	}
	var expectedTTFT *int64
	measuredTTFT := int64(0)
	if result.TTFTObserved {
		measuredTTFT = max(int64(0), result.TTFT.Milliseconds())
		expectedTTFT = &measuredTTFT
	}
	expectedDuration := max(int64(0), result.Duration.Milliseconds())
	var expectedTPS *int64
	if result.MeasureTPS && result.TTFTObserved && expectedDuration > measuredTTFT {
		value := calculateTPSNano(result.Usage.OutputTokens, expectedDuration-measuredTTFT)
		if value != 0 {
			expectedTPS = &value
		}
	}
	return int64PointerEqual(stored.TtftMilliseconds, expectedTTFT) &&
		int64PointerEqual(stored.DurationMilliseconds, &expectedDuration) &&
		int64PointerEqual(stored.TokensPerSecondNano, expectedTPS), nil
}

func int64PointerEqual(left, right *int64) bool {
	return (left == nil) == (right == nil) && (left == nil || *left == *right)
}

func coalesceGatewayReason(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > 64 {
		return "upstream_error_code_too_long"
	}
	return value
}

// RecoverOrphanCalls claims calls whose heartbeat lease expired by bumping
// their lease generation (fencing the previous owner), then finalizes or
// compensates each claimed call in its own transaction.
func (s *Store) RecoverOrphanCalls(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := s.q.WithTx(tx)
	rows, err := q.ClaimOrphanCalls(ctx, ClaimOrphanCallsParams{Cutoff: cutoff, MaxRows: int64(limit)})
	if err != nil {
		return 0, err
	}
	type orphanClaim struct {
		id, status string
		generation int64
	}
	claims := make([]orphanClaim, 0, len(rows))
	for _, row := range rows {
		claims = append(claims, orphanClaim{id: row.ID, status: row.Status})
	}
	for index := range claims {
		generation, err := q.BumpLeaseGeneration(ctx, BumpLeaseGenerationParams{
			ID: claims[index].id, LeaseDuration: gateway.DefaultLeaseDuration, Status: claims[index].status,
		})
		if err != nil {
			return 0, err
		}
		claims[index].generation = generation
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, mapGatewayError(err)
	}
	recovered := 0
	recoveryErrors := make([]error, 0)
	for _, claim := range claims {
		var finalizeErr error
		for attempt := 0; attempt < 3; attempt++ {
			if claim.status == string(gateway.CallPendingDelivery) {
				_, finalizeErr = s.CompensateCallDelivery(ctx, claim.id, claim.generation, "orphan_delivery_unconfirmed")
			} else {
				_, finalizeErr = s.FinalizeCall(ctx, claim.id, gateway.FinalizeOutcome{LeaseGeneration: claim.generation})
			}
			if !errors.Is(finalizeErr, gateway.ErrSnapshotRetry) {
				break
			}
		}
		if finalizeErr != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("recover api call %s: %w", claim.id, finalizeErr))
			continue
		}
		recovered++
	}
	return recovered, errors.Join(recoveryErrors...)
}
