package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/jackc/pgx/v5"
)

func (s *Store) BeginCall(ctx context.Context, request gateway.BeginCallRequest, resolver gateway.LeaseResolver) (plan gateway.CallPlan, resultErr error) {
	defer func() { resultErr = mapGatewayError(resultErr) }()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return gateway.CallPlan{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	ledgerTx := &LedgerTransaction{Tx: tx}

	var ownerID, ledgerAccountID, keyPrefix string
	var generation int64
	err = tx.QueryRow(ctx, `
		SELECT key.owner_account_id::text, ledger_account.id::text, key.key_prefix, key.generation
		FROM api_keys key
		JOIN accounts owner ON owner.id = key.owner_account_id
		JOIN ledger_accounts ledger_account ON ledger_account.identity_account_id = owner.id
		WHERE key.id = $1 AND key.key_hash = $2 AND key.generation = $3 AND key.status = 'active'
			AND owner.status = 'active' AND NOT owner.must_change_password`,
		request.Authenticated.ID, request.Authenticated.Hash[:], request.Authenticated.Generation,
	).Scan(&ownerID, &ledgerAccountID, &keyPrefix, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return gateway.CallPlan{}, gateway.ErrInvalidAPIKey
	}
	if err != nil {
		return gateway.CallPlan{}, err
	}
	if ownerID != request.Authenticated.OwnerAccountID || generation != request.Authenticated.Generation {
		return gateway.CallPlan{}, gateway.ErrInvalidAPIKey
	}

	var feeRateVersion, feeRateNano int64
	if err := tx.QueryRow(ctx, `SELECT version, fee_rate_nano FROM api_fee_rates ORDER BY version DESC LIMIT 1`).Scan(&feeRateVersion, &feeRateNano); err != nil {
		return gateway.CallPlan{}, err
	}
	callID, err := allocateDatabaseUUID(ctx, tx)
	if err != nil {
		return gateway.CallPlan{}, err
	}
	reject := func(code string) (gateway.CallPlan, error) {
		created, insertErr := insertRejectedCall(ctx, tx, callID, ownerID, ledgerAccountID, keyPrefix, request, feeRateVersion, feeRateNano, code)
		if insertErr != nil {
			return gateway.CallPlan{}, insertErr
		}
		if commitErr := s.commitGatewayTransaction(ctx, tx, "api_call.begin_rejected", callID); commitErr != nil {
			if recovered, recoverErr := s.recoverRejectedCall(ctx, callID, ownerID, request, code); recoverErr == nil {
				return gateway.CallPlan{Call: recovered}, gateway.ErrRejected
			}
			return gateway.CallPlan{}, commitErr
		}
		return gateway.CallPlan{Call: created}, gateway.ErrRejected
	}

	var poolID string
	var poolVersion int64
	err = tx.QueryRow(ctx, `
		SELECT id::text, version FROM api_model_pools
		WHERE api_key_id = $1 AND canonical_model_id = $2 AND protocol = $3 AND status = 'active'`,
		request.Authenticated.ID, request.CanonicalModelID, request.Protocol,
	).Scan(&poolID, &poolVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return reject("pool_not_found")
	}
	if err != nil {
		return gateway.CallPlan{}, err
	}
	offerReferences, err := poolOfferReferences(ctx, tx, poolID)
	if err != nil {
		return gateway.CallPlan{}, err
	}
	if len(offerReferences) == 0 {
		return reject("pool_empty")
	}
	offerIDs := make([]string, 0, len(offerReferences))
	addedValidationVersionByOffer := make(map[string]int64, len(offerReferences))
	for _, reference := range offerReferences {
		offerIDs = append(offerIDs, reference.OfferID)
		addedValidationVersionByOffer[reference.OfferID] = reference.AddedValidationVersion
	}
	statuses, leases, err := resolver(ctx, ledgerTx, offerIDs)
	if err != nil {
		return gateway.CallPlan{}, err
	}
	leaseByOffer := make(map[string]channel.RoutingLease, len(leases))
	for _, lease := range leases {
		leaseByOffer[lease.OfferID] = lease
	}
	statusByOffer := make(map[string]channel.PoolOfferStatus, len(statuses))
	for _, status := range statuses {
		statusByOffer[status.OfferID] = status
	}
	candidates := make([]gateway.Candidate, 0, len(offerIDs))
	preauthorized := money.Amount(0)
	unrepresentableCandidates := 0
	for index, offerID := range offerIDs {
		status, statusExists := statusByOffer[offerID]
		lease, leaseExists := leaseByOffer[offerID]
		if !statusExists || !leaseExists || !status.Eligible ||
			lease.ValidationVersion != addedValidationVersionByOffer[offerID] ||
			lease.ModelID != request.CanonicalModelID || lease.Protocol != request.Protocol {
			continue
		}
		selfChannel := lease.ProviderAccountID == ownerID
		upper, err := gateway.ConservativeNetDebitUpperBound(lease, feeRateNano, selfChannel)
		if err != nil {
			unrepresentableCandidates++
			continue
		}
		_ = index
		candidate := gateway.Candidate{
			Priority: len(candidates) + 1, Lease: lease, SelfChannel: selfChannel,
			NetDebitUpper: upper, LeaseGeneration: 1,
		}
		candidates = append(candidates, candidate)
		if upper > preauthorized {
			preauthorized = upper
		}
	}
	if len(candidates) == 0 {
		if unrepresentableCandidates > 0 {
			return reject("no_price_representable_offer")
		}
		return reject("no_eligible_offer")
	}

	var hold ledger.Hold
	if preauthorized > 0 {
		savepoint, err := tx.Begin(ctx)
		if err != nil {
			return gateway.CallPlan{}, err
		}
		holdService := ledger.NewService(&LedgerTransaction{Tx: savepoint})
		hold, err = holdService.CreateHold(ctx, ledger.CreateHoldRequest{
			IdempotencyKey: "api-call-" + callID + "-authorize",
			AccountID:      ownerID, Amount: preauthorized,
			FundingPolicy: ledger.HoldFundingCreditAllowed, Purpose: ledger.HoldPurposeSpendAuthorization,
			Reason: "authorize maximum net debit for api call", BusinessType: "api_call", BusinessID: callID,
		})
		if err != nil {
			_ = savepoint.Rollback(ctx)
			if errors.Is(err, ledger.ErrInsufficientFunds) || errors.Is(err, ledger.ErrCreditFrozen) {
				return reject("insufficient_spending_power")
			}
			return gateway.CallPlan{}, err
		}
		if err := savepoint.Commit(ctx); err != nil {
			return gateway.CallPlan{}, err
		}
	}
	holdID := ""
	zeroHoldReason := ""
	if preauthorized > 0 {
		holdID = hold.ID
	} else {
		zeroHoldReason = "all_candidates_self_or_zero_cost"
	}
	leaseDuration := request.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = gateway.DefaultLeaseDuration
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO api_calls (
			id, consumer_account_id, consumer_ledger_account_id, api_key_id, key_prefix, key_generation,
			pool_id, pool_version, canonical_model_id, protocol, status, decision_code,
			candidate_count, hold_id, preauthorized_nano, zero_hold_reason,
			fee_rate_version, fee_rate_nano, formula_version, lease_expires_at, heartbeat_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'in_progress', 'authorized',
			$11, NULLIF($12, '')::uuid, $13, $14, $15, $16, 'formula-v2', now() + $17, now())`,
		callID, ownerID, ledgerAccountID, request.Authenticated.ID, keyPrefix, generation,
		poolID, poolVersion, request.CanonicalModelID, request.Protocol, len(candidates), holdID,
		preauthorized.Nano(), zeroHoldReason, feeRateVersion, feeRateNano, leaseDuration,
	); err != nil {
		return gateway.CallPlan{}, mapGatewayError(err)
	}
	for _, candidate := range candidates {
		lease := candidate.Lease
		if _, err := tx.Exec(ctx, `
			INSERT INTO api_call_candidates (
				call_id, priority, offer_id, channel_id, provider_account_id,
				validation_version, credential_version, upstream_model_id, context_window,
				input_price_nano, output_price_nano, cache_write_price_nano, cache_read_price_nano,
				multiplier_nano, self_channel, net_debit_upper_bound_nano
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
			callID, candidate.Priority, lease.OfferID, lease.ChannelID, lease.ProviderAccountID,
			lease.ValidationVersion, lease.CredentialVersion, lease.UpstreamModelID, lease.ContextWindow,
			lease.InputPrice.Nano(), lease.OutputPrice.Nano(), lease.CacheWritePrice.Nano(), lease.CacheReadPrice.Nano(),
			lease.Multiplier.Nano(), candidate.SelfChannel, candidate.NetDebitUpper.Nano(),
		); err != nil {
			return gateway.CallPlan{}, mapGatewayError(err)
		}
	}
	// Every candidate of one call resolves the same canonical model, so its
	// conditional tier table is a call-level fact. Snapshot it now: tier
	// selection happens at settlement, when the final usage is known.
	if len(candidates) > 0 {
		if err := insertCallPriceTiers(ctx, tx, callID, candidates[0].Lease.PriceTiers); err != nil {
			return gateway.CallPlan{}, err
		}
	}
	created, err := loadCall(ctx, tx, callID, ownerID, false)
	if err != nil {
		return gateway.CallPlan{}, err
	}
	if commitErr := s.commitGatewayTransaction(ctx, tx, "api_call.begin", callID); commitErr != nil {
		if recovered, recoverErr := s.recoverBegunCall(ctx, callID, ownerID, request, candidates, preauthorized); recoverErr == nil {
			return recovered, nil
		}
		return gateway.CallPlan{}, commitErr
	}
	// last_used_at is an observational convenience, not part of the routing or
	// accounting snapshot. Keep this hot-row write outside REPEATABLE READ so
	// concurrent calls using one Key do not force otherwise independent call
	// snapshots into avoidable serialization retries.
	_, _ = s.pool.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1`, request.Authenticated.ID)
	return gateway.CallPlan{Call: created, Candidates: candidates}, nil
}

func (s *Store) recoverRejectedCall(parent context.Context, callID, ownerID string, request gateway.BeginCallRequest, decisionCode string) (gateway.Call, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), gateway.PersistenceTimeout)
	defer cancel()
	recovered, err := loadCall(ctx, s.pool, callID, ownerID, false)
	if err != nil {
		return gateway.Call{}, err
	}
	if recovered.Status != gateway.CallRejected || recovered.ConsumerAccountID != ownerID ||
		recovered.APIKeyID != request.Authenticated.ID || recovered.KeyGeneration != request.Authenticated.Generation ||
		recovered.CanonicalModelID != request.CanonicalModelID || recovered.Protocol != request.Protocol ||
		recovered.DecisionCode != decisionCode || recovered.CandidateCount != 0 ||
		recovered.UpstreamAttemptCount != 0 || recovered.HoldID != "" || recovered.Preauthorized != 0 {
		return gateway.Call{}, gateway.ErrConflict
	}
	return recovered, nil
}

func (s *Store) recoverBegunCall(parent context.Context, callID, ownerID string, request gateway.BeginCallRequest, expected []gateway.Candidate, preauthorized money.Amount) (gateway.CallPlan, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), gateway.PersistenceTimeout)
	defer cancel()
	recovered, err := loadCall(ctx, s.pool, callID, ownerID, false)
	if err != nil {
		return gateway.CallPlan{}, err
	}
	if recovered.Status != gateway.CallInProgress || recovered.ConsumerAccountID != ownerID ||
		recovered.APIKeyID != request.Authenticated.ID || recovered.KeyGeneration != request.Authenticated.Generation ||
		recovered.CanonicalModelID != request.CanonicalModelID || recovered.Protocol != request.Protocol ||
		recovered.CandidateCount != len(expected) || recovered.UpstreamAttemptCount != 0 ||
		recovered.Preauthorized != preauthorized || recovered.LeaseGeneration <= 0 {
		return gateway.CallPlan{}, gateway.ErrConflict
	}
	if preauthorized > 0 {
		var amount, remaining, captured, released int64
		var status string
		if recovered.HoldID == "" {
			return gateway.CallPlan{}, gateway.ErrConflict
		}
		if err := s.pool.QueryRow(ctx, `
			SELECT status, amount_nano, remaining_nano, captured_nano, released_nano
			FROM ledger_holds WHERE id = $1`, recovered.HoldID).Scan(&status, &amount, &remaining, &captured, &released); err != nil {
			return gateway.CallPlan{}, err
		}
		if status != "active" || amount != preauthorized.Nano() || remaining != amount || captured != 0 || released != 0 {
			return gateway.CallPlan{}, gateway.ErrConflict
		}
	} else if recovered.HoldID != "" {
		return gateway.CallPlan{}, gateway.ErrConflict
	}
	rows, err := s.pool.Query(ctx, `
		SELECT priority, offer_id::text, channel_id::text, provider_account_id::text,
			validation_version, credential_version, upstream_model_id, context_window,
			input_price_nano, output_price_nano, cache_write_price_nano, cache_read_price_nano,
			multiplier_nano, self_channel, net_debit_upper_bound_nano
		FROM api_call_candidates WHERE call_id = $1 ORDER BY priority`, callID)
	if err != nil {
		return gateway.CallPlan{}, err
	}
	type storedCandidate struct {
		priority                                                                  int
		offerID, channelID, providerID, upstreamModel                             string
		validationVersion, credentialVersion, contextWindow                       int64
		inputPrice, outputPrice, cacheWritePrice, cacheReadPrice, multiplier, net int64
		self                                                                      bool
	}
	stored := make([]storedCandidate, 0, len(expected))
	for rows.Next() {
		var candidate storedCandidate
		if err := rows.Scan(
			&candidate.priority, &candidate.offerID, &candidate.channelID, &candidate.providerID,
			&candidate.validationVersion, &candidate.credentialVersion, &candidate.upstreamModel, &candidate.contextWindow,
			&candidate.inputPrice, &candidate.outputPrice, &candidate.cacheWritePrice, &candidate.cacheReadPrice,
			&candidate.multiplier, &candidate.self, &candidate.net,
		); err != nil {
			rows.Close()
			return gateway.CallPlan{}, err
		}
		stored = append(stored, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return gateway.CallPlan{}, err
	}
	rows.Close()
	if len(stored) != len(expected) {
		return gateway.CallPlan{}, gateway.ErrConflict
	}
	for index := range expected {
		want, got := expected[index], stored[index]
		lease := want.Lease
		if got.priority != want.Priority || got.offerID != lease.OfferID || got.channelID != lease.ChannelID ||
			got.providerID != lease.ProviderAccountID || got.validationVersion != lease.ValidationVersion ||
			got.credentialVersion != lease.CredentialVersion || got.upstreamModel != lease.UpstreamModelID ||
			got.contextWindow != lease.ContextWindow || got.inputPrice != lease.InputPrice.Nano() ||
			got.outputPrice != lease.OutputPrice.Nano() || got.cacheWritePrice != lease.CacheWritePrice.Nano() ||
			got.cacheReadPrice != lease.CacheReadPrice.Nano() || got.multiplier != lease.Multiplier.Nano() ||
			got.self != want.SelfChannel || got.net != want.NetDebitUpper.Nano() {
			return gateway.CallPlan{}, gateway.ErrConflict
		}
		expected[index].LeaseGeneration = recovered.LeaseGeneration
	}
	expectedTiers := []ledger.PriceTier{}
	if len(expected) > 0 {
		expectedTiers = expected[0].Lease.PriceTiers
	}
	storedTiers, err := loadCallPriceTiers(ctx, s.pool, callID)
	if err != nil {
		return gateway.CallPlan{}, err
	}
	if !priceTiersEqual(storedTiers, expectedTiers) {
		return gateway.CallPlan{}, gateway.ErrConflict
	}
	return gateway.CallPlan{Call: recovered, Candidates: expected}, nil
}

func insertCallPriceTiers(ctx context.Context, tx pgx.Tx, callID string, tiers []ledger.PriceTier) error {
	for index, tier := range tiers {
		if _, err := tx.Exec(ctx, `
			INSERT INTO api_call_price_tiers (
				call_id, seq, name, min_prompt_tokens, max_prompt_tokens, timezone, weekdays,
				start_minute_of_day, end_minute_of_day,
				input_price_nano, output_price_nano, cache_write_price_nano, cache_read_price_nano
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			callID, index+1, tier.Name, tier.MinPromptTokens, tier.MaxPromptTokens, tier.Timezone, tier.Weekdays,
			tier.StartMinute, tier.EndMinute,
			tier.InputPrice.Nano(), tier.OutputPrice.Nano(), tier.CacheWritePrice.Nano(), tier.CacheReadPrice.Nano(),
		); err != nil {
			return mapGatewayError(err)
		}
	}
	return nil
}

func loadCallPriceTiers(ctx context.Context, queryer gatewayQueryer, callID string) ([]ledger.PriceTier, error) {
	rows, err := queryer.Query(ctx, `
		SELECT name, min_prompt_tokens, max_prompt_tokens, timezone, weekdays,
			start_minute_of_day, end_minute_of_day,
			input_price_nano, output_price_nano, cache_write_price_nano, cache_read_price_nano
		FROM api_call_price_tiers WHERE call_id = $1 ORDER BY seq`, callID)
	if err != nil {
		return nil, mapGatewayError(err)
	}
	defer rows.Close()
	tiers := make([]ledger.PriceTier, 0)
	for rows.Next() {
		var tier ledger.PriceTier
		var weekdays []int16
		var inputPrice, outputPrice, cacheWritePrice, cacheReadPrice int64
		if err := rows.Scan(
			&tier.Name, &tier.MinPromptTokens, &tier.MaxPromptTokens, &tier.Timezone, &weekdays,
			&tier.StartMinute, &tier.EndMinute,
			&inputPrice, &outputPrice, &cacheWritePrice, &cacheReadPrice,
		); err != nil {
			return nil, err
		}
		if len(weekdays) > 0 {
			tier.Weekdays = make([]int, len(weekdays))
			for index, weekday := range weekdays {
				tier.Weekdays[index] = int(weekday)
			}
		}
		tier.InputPrice = money.FromNano(inputPrice)
		tier.OutputPrice = money.FromNano(outputPrice)
		tier.CacheWritePrice = money.FromNano(cacheWritePrice)
		tier.CacheReadPrice = money.FromNano(cacheReadPrice)
		tiers = append(tiers, tier)
	}
	return tiers, rows.Err()
}

type poolOfferReference struct {
	OfferID                string
	AddedValidationVersion int64
}

func poolOfferReferences(ctx context.Context, queryer gatewayQueryer, poolID string) ([]poolOfferReference, error) {
	rows, err := queryer.Query(ctx, `SELECT offer_id::text, added_validation_version FROM api_pool_members WHERE pool_id = $1 ORDER BY priority`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]poolOfferReference, 0)
	for rows.Next() {
		var reference poolOfferReference
		if err := rows.Scan(&reference.OfferID, &reference.AddedValidationVersion); err != nil {
			return nil, err
		}
		result = append(result, reference)
	}
	return result, rows.Err()
}

func allocateDatabaseUUID(ctx context.Context, queryer rowQueryer) (string, error) {
	var id string
	err := queryer.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id)
	return id, err
}

func insertRejectedCall(ctx context.Context, tx pgx.Tx, callID, ownerID, ledgerAccountID, keyPrefix string, request gateway.BeginCallRequest, feeRateVersion, feeRateNano int64, code string) (gateway.Call, error) {
	_, err := tx.Exec(ctx, `
		INSERT INTO api_calls (
			id, consumer_account_id, consumer_ledger_account_id, api_key_id, key_prefix, key_generation,
			canonical_model_id, protocol, status, decision_code, candidate_count,
			upstream_attempt_count, preauthorized_nano, zero_hold_reason,
			fee_rate_version, fee_rate_nano, completion_reason, completed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'rejected', $9, 0, 0, 0,
			'business_precheck_rejected', $10, $11, $9, now())`,
		callID, ownerID, ledgerAccountID, request.Authenticated.ID, keyPrefix, request.Authenticated.Generation,
		request.CanonicalModelID, request.Protocol, code, feeRateVersion, feeRateNano,
	)
	if err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	return loadCall(ctx, tx, callID, ownerID, false)
}

func (s *Store) StartAttempt(ctx context.Context, callID string, candidate gateway.Candidate) (gateway.Attempt, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return gateway.Attempt{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var status string
	var leaseGeneration int64
	if err := tx.QueryRow(ctx, `SELECT status, lease_generation FROM api_calls WHERE id = $1 FOR UPDATE`, callID).Scan(&status, &leaseGeneration); err != nil {
		return gateway.Attempt{}, mapGatewayError(err)
	}
	if status != string(gateway.CallInProgress) || candidate.LeaseGeneration != leaseGeneration {
		return gateway.Attempt{}, gateway.ErrConflict
	}
	var completedAttempts, inProgressAttempts int
	var anyCommitted, anySucceeded bool
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status <> 'in_progress'),
			count(*) FILTER (WHERE status = 'in_progress'),
			COALESCE(bool_or(semantic_committed), false),
			COALESCE(bool_or(status = 'succeeded'), false)
		FROM api_call_attempts WHERE call_id = $1`, callID).Scan(
		&completedAttempts, &inProgressAttempts, &anyCommitted, &anySucceeded,
	); err != nil {
		return gateway.Attempt{}, err
	}
	if inProgressAttempts != 0 || anyCommitted || anySucceeded || candidate.Priority != completedAttempts+1 {
		return gateway.Attempt{}, gateway.ErrConflict
	}
	var providerID string
	if err := tx.QueryRow(ctx, `
		SELECT provider_account_id::text FROM api_call_candidates
		WHERE call_id = $1 AND priority = $2 AND offer_id = $3`, callID, candidate.Priority, candidate.Lease.OfferID).Scan(&providerID); err != nil {
		return gateway.Attempt{}, mapGatewayError(err)
	}
	var attempt gateway.Attempt
	if err := tx.QueryRow(ctx, `
		WITH next_sequence AS (
			SELECT COALESCE(max(sequence), 0) + 1 AS value FROM api_call_attempts WHERE call_id = $1
		)
		INSERT INTO api_call_attempts (call_id, sequence, offer_id, provider_account_id, status)
		SELECT $1, value, $2, $3, 'in_progress' FROM next_sequence
		RETURNING id::text, call_id::text, sequence, offer_id::text, provider_account_id::text, status, started_at`,
		callID, candidate.Lease.OfferID, providerID,
	).Scan(&attempt.ID, &attempt.CallID, &attempt.Sequence, &attempt.OfferID, &attempt.ProviderAccountID, &status, &attempt.StartedAt); err != nil {
		return gateway.Attempt{}, mapGatewayError(err)
	}
	attempt.Status = gateway.AttemptStatus(status)
	attempt.LeaseGeneration = leaseGeneration
	if _, err := tx.Exec(ctx, `
		UPDATE api_calls SET upstream_attempt_count = upstream_attempt_count + 1,
			heartbeat_at = now(), lease_expires_at = now() + $2
			WHERE id = $1 AND lease_generation = $3`, callID, gateway.DefaultLeaseDuration, leaseGeneration); err != nil {
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
	attempt, err := completeAttemptInTx(ctx, tx, attemptID, result)
	if err != nil {
		return gateway.Attempt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return gateway.Attempt{}, mapGatewayError(err)
	}
	return attempt, nil
}

func completeAttemptInTx(ctx context.Context, tx pgx.Tx, attemptID string, result gateway.AttemptResult) (gateway.Attempt, error) {
	raw, truncated := normalizeRawError(result.RawError)
	truncated = truncated || len(result.RawError) > len(raw)
	errorCode := normalizeErrorCode(result.ErrorCode)
	durationMS := max(int64(0), result.Duration.Milliseconds())
	var ttftMS any
	measuredTTFT := int64(0)
	if result.TTFTObserved {
		measuredTTFT = max(int64(0), result.TTFT.Milliseconds())
		ttftMS = measuredTTFT
	}
	var inputTokens, outputTokens, cacheWriteTokens, cacheReadTokens any
	tokensPerSecond := int64(0)
	if result.Usage != nil {
		inputTokens, outputTokens = result.Usage.InputTokens, result.Usage.OutputTokens
		cacheWriteTokens, cacheReadTokens = result.Usage.CacheWriteTokens, result.Usage.CacheReadTokens
		if result.MeasureTPS && result.TTFTObserved && durationMS > measuredTTFT {
			tokensPerSecond = calculateTPSNano(result.Usage.OutputTokens, durationMS-measuredTTFT)
		}
	}
	var callID, callStatus, attemptStatus string
	var leaseGeneration int64
	if err := tx.QueryRow(ctx, `
		SELECT attempt.call_id::text, call.status, attempt.status, call.lease_generation
		FROM api_call_attempts attempt
		JOIN api_calls call ON call.id = attempt.call_id
		WHERE attempt.id = $1 FOR UPDATE OF call, attempt`, attemptID).Scan(&callID, &callStatus, &attemptStatus, &leaseGeneration); err != nil {
		return gateway.Attempt{}, mapGatewayError(err)
	}
	if callStatus != string(gateway.CallInProgress) || attemptStatus != string(gateway.AttemptInProgress) || result.LeaseGeneration != leaseGeneration {
		return gateway.Attempt{}, gateway.ErrConflict
	}
	var attempt gateway.Attempt
	var status string
	var httpStatus, ttft, duration, tps sql.NullInt64
	var completed sql.NullTime
	err := tx.QueryRow(ctx, `
		UPDATE api_call_attempts
		SET status = $2, http_status = NULLIF($3, 0), error_code = $4,
			raw_error = $5, raw_error_truncated = $6, semantic_committed = semantic_committed OR $7,
			ttft_milliseconds = $8, duration_milliseconds = $9,
			input_tokens = $10, output_tokens = $11, cache_write_tokens = $12, cache_read_tokens = $13,
			tokens_per_second_nano = NULLIF($14::bigint, 0::bigint),
			completed_at = CASE WHEN $2 = 'pending_delivery' THEN NULL ELSE now() END
		WHERE id = $1 AND status = 'in_progress'
		RETURNING id::text, call_id::text, sequence, offer_id::text, provider_account_id::text,
			status, http_status, error_code, raw_error, raw_error_truncated, semantic_committed,
			ttft_milliseconds, duration_milliseconds, tokens_per_second_nano, started_at, completed_at`,
		attemptID, result.Status, result.HTTPStatus, errorCode, raw, truncated, result.SemanticCommitted,
		ttftMS, durationMS, inputTokens, outputTokens, cacheWriteTokens, cacheReadTokens, tokensPerSecond,
	).Scan(
		&attempt.ID, &attempt.CallID, &attempt.Sequence, &attempt.OfferID, &attempt.ProviderAccountID,
		&status, &httpStatus, &attempt.ErrorCode, &attempt.RawError, &attempt.RawErrorTruncated, &attempt.SemanticCommitted,
		&ttft, &duration, &tps, &attempt.StartedAt, &completed,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return gateway.Attempt{}, gateway.ErrConflict
	}
	if err != nil {
		return gateway.Attempt{}, mapGatewayError(err)
	}
	attempt.Status = gateway.AttemptStatus(status)
	attempt.LeaseGeneration = leaseGeneration
	if httpStatus.Valid {
		attempt.HTTPStatus = int(httpStatus.Int64)
	}
	if ttft.Valid {
		value := time.Duration(ttft.Int64) * time.Millisecond
		attempt.TTFT = &value
	}
	if duration.Valid {
		value := time.Duration(duration.Int64) * time.Millisecond
		attempt.Duration = &value
	}
	if tps.Valid {
		value := tps.Int64
		attempt.TokensPerSecondNano = &value
	}
	attempt.Usage = result.Usage
	if completed.Valid {
		attempt.CompletedAt = &completed.Time
	}
	return attempt, nil
}

func (s *Store) MarkAttemptCommitted(ctx context.Context, attemptID string, observation gateway.AttemptCommitObservation) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var callStatus, attemptStatus string
	var currentGeneration int64
	var alreadyCommitted bool
	var outputTokens sql.NullInt64
	if err := tx.QueryRow(ctx, `
		SELECT call.status, attempt.status, call.lease_generation,
		       attempt.semantic_committed, attempt.output_tokens
		FROM api_call_attempts attempt JOIN api_calls call ON call.id = attempt.call_id
		WHERE attempt.id = $1 FOR UPDATE OF call, attempt`, attemptID).Scan(
		&callStatus, &attemptStatus, &currentGeneration, &alreadyCommitted, &outputTokens,
	); err != nil {
		return mapGatewayError(err)
	}
	validActivePair := callStatus == string(gateway.CallInProgress) && attemptStatus == string(gateway.AttemptInProgress)
	validPendingPair := callStatus == string(gateway.CallPendingDelivery) && attemptStatus == string(gateway.AttemptPendingDelivery)
	if (!validActivePair && !validPendingPair) || observation.LeaseGeneration != currentGeneration {
		return gateway.ErrConflict
	}
	if alreadyCommitted {
		if err := tx.Commit(ctx); err != nil {
			return mapGatewayError(err)
		}
		return nil
	}
	if validActivePair {
		if _, err := tx.Exec(ctx, `UPDATE api_call_attempts SET semantic_committed = true WHERE id = $1 AND NOT semantic_committed`, attemptID); err != nil {
			return mapGatewayError(err)
		}
	} else {
		ttftMS := max(int64(0), observation.TTFT.Milliseconds())
		durationMS := max(ttftMS, observation.Duration.Milliseconds())
		tokensPerSecond := int64(0)
		if observation.MeasureTPS && outputTokens.Valid && durationMS > ttftMS {
			tokensPerSecond = calculateTPSNano(outputTokens.Int64, durationMS-ttftMS)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE api_call_attempts
			SET semantic_committed = true, ttft_milliseconds = $2,
			    duration_milliseconds = $3, tokens_per_second_nano = NULLIF($4::bigint, 0::bigint)
			WHERE id = $1 AND status = 'pending_delivery' AND NOT semantic_committed`,
			attemptID, ttftMS, durationMS, tokensPerSecond,
		); err != nil {
			return mapGatewayError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return mapGatewayError(err)
	}
	return nil
}

func (s *Store) HeartbeatCall(ctx context.Context, callID string, leaseGeneration int64) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE api_calls SET heartbeat_at = now(), lease_expires_at = now() + $2
		WHERE id = $1 AND status IN ('in_progress', 'pending_delivery') AND lease_generation = $3`, callID, gateway.DefaultLeaseDuration, leaseGeneration)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return gateway.ErrConflict
	}
	return nil
}

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
	ledgerTx := &LedgerTransaction{Tx: tx}
	ledgerService := ledger.NewService(ledgerTx)

	var consumerID, holdID, currentStatus, formulaVersion string
	var preauthorized, currentGeneration int64
	var createdAt time.Time
	var storedFinalizerHash []byte
	if err := tx.QueryRow(ctx, `
		SELECT consumer_account_id::text, COALESCE(hold_id::text, ''), preauthorized_nano,
			status, lease_generation, finalizer_payload_hash, formula_version, created_at
		FROM api_calls WHERE id = $1 FOR UPDATE`, callID).Scan(
		&consumerID, &holdID, &preauthorized, &currentStatus, &currentGeneration, &storedFinalizerHash,
		&formulaVersion, &createdAt,
	); err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	if outcome.LeaseGeneration != currentGeneration {
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
			matches, err := successfulAttemptReplayMatches(ctx, tx, callID, current, outcome.SuccessAttemptID, *outcome.SuccessAttempt)
			if err != nil {
				return gateway.Call{}, err
			}
			if !matches {
				return gateway.Call{}, gateway.ErrConflict
			}
		}
		expectedHash := gateway.FinalizerHash(gateway.CallSucceeded, current.CompletionReason, current.FinalOfferID, current.FinalHTTPStatus, current.Usage)
		if !bytes.Equal(storedFinalizerHash, expectedHash[:]) {
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
		attempt, err := completeAttemptInTx(ctx, tx, outcome.SuccessAttemptID, pendingResult)
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
		fact, err = deriveFinalizationFact(ctx, tx, callID)
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
		var lease channel.RoutingLease
		var selfChannel bool
		var inputPrice, outputPrice, cacheWritePrice, cacheReadPrice, multiplier int64
		if err := tx.QueryRow(ctx, `
			SELECT offer_id::text, channel_id::text, provider_account_id::text,
				validation_version, credential_version, upstream_model_id, context_window,
				input_price_nano, output_price_nano, cache_write_price_nano, cache_read_price_nano,
				multiplier_nano, self_channel
			FROM api_call_candidates WHERE call_id = $1 AND offer_id = $2`, callID, fact.OfferID).Scan(
			&lease.OfferID, &lease.ChannelID, &lease.ProviderAccountID,
			&lease.ValidationVersion, &lease.CredentialVersion, &lease.UpstreamModelID, &lease.ContextWindow,
			&inputPrice, &outputPrice, &cacheWritePrice, &cacheReadPrice,
			&multiplier, &selfChannel,
		); err != nil {
			return gateway.Call{}, mapGatewayError(err)
		}
		lease.InputPrice, lease.OutputPrice = money.FromNano(inputPrice), money.FromNano(outputPrice)
		lease.CacheWritePrice, lease.CacheReadPrice = money.FromNano(cacheWritePrice), money.FromNano(cacheReadPrice)
		lease.Multiplier = money.FromNano(multiplier)
		providerID = lease.ProviderAccountID
		if err := gateway.ValidateUsage(*fact.Usage, lease.ContextWindow); err != nil {
			return gateway.Call{}, err
		}
		var feeRateNano int64
		if err := tx.QueryRow(ctx, `SELECT fee_rate_nano FROM api_calls WHERE id = $1`, callID).Scan(&feeRateNano); err != nil {
			return gateway.Call{}, err
		}
		if formulaVersion == gateway.FormulaVersionV2 {
			// formula-v2 re-reads the call-level tier snapshot taken at BeginCall
			// and selects by prompt-side token volume and the call start time.
			tiers, tierErr := loadCallPriceTiers(ctx, tx, callID)
			if tierErr != nil {
				return gateway.Call{}, tierErr
			}
			priceV2, v2Err := ledger.CalculatePriceV2(*fact.Usage, gateway.OfficialPrices(lease), tiers, createdAt, lease.Multiplier.Nano(), feeRateNano, selfChannel)
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
			if err != nil || actual > money.FromNano(preauthorized) {
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

	if _, err := tx.Exec(ctx, `
		INSERT INTO api_call_settlements (
			call_id, kind, provider_account_id, provider_charge_nano, platform_fee_nano,
			capture_transaction_id, self_transaction_id, hold_id
		) VALUES ($1, $2, NULLIF($3, '')::uuid, $4, $5, NULLIF($6, '')::uuid, NULLIF($7, '')::uuid, NULLIF($8, '')::uuid)`,
		callID, settlementKind, providerID, providerCharge.Nano(), platformFee.Nano(), captureTransactionID, selfTransactionID, holdID,
	); err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	var usage ledger.UsageV1
	var inputTokens, outputTokens, cacheWriteTokens, cacheReadTokens any
	if fact.Usage != nil {
		usage = *fact.Usage
		inputTokens, outputTokens = usage.InputTokens, usage.OutputTokens
		cacheWriteTokens, cacheReadTokens = usage.CacheWriteTokens, usage.CacheReadTokens
	}
	storedStatus := fact.Status
	var completedAt any = time.Now()
	if fact.Status == gateway.CallSucceeded {
		storedStatus = gateway.CallPendingDelivery
		completedAt = nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE api_calls SET status = $2, decision_code = $3, final_offer_id = NULLIF($4, '')::uuid,
			completion_reason = $3, input_tokens = $5, output_tokens = $6,
			cache_write_tokens = $7, cache_read_tokens = $8,
			provider_charge_nano = $9, platform_fee_nano = $10,
			final_http_status = NULLIF($11, 0),
			settled_price_tier_seq = $16,
			heartbeat_at = CASE WHEN $2 = 'pending_delivery' THEN now() ELSE NULL END,
			lease_expires_at = CASE WHEN $2 = 'pending_delivery' THEN now() + $13 ELSE NULL END,
			finalizer_payload_hash = $12, completed_at = $14
		WHERE id = $1 AND status = 'in_progress' AND lease_generation = $15`, callID, storedStatus, fact.Reason,
		fact.OfferID, inputTokens, outputTokens, cacheWriteTokens, cacheReadTokens,
		providerCharge.Nano(), platformFee.Nano(), fact.HTTPStatus, finalizerHash[:],
		gateway.DefaultLeaseDuration, completedAt, outcome.LeaseGeneration, settledTierSeq,
	); err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	completed, err := loadCall(ctx, tx, callID, consumerID, false)
	if err != nil {
		return gateway.Call{}, err
	}
	if commitErr := s.commitGatewayTransaction(ctx, tx, "api_call.finalize", callID); commitErr != nil {
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
	var status string
	var generation int64
	var storedHash []byte
	if err := s.pool.QueryRow(ctx, `
		SELECT status, lease_generation, finalizer_payload_hash
		FROM api_calls WHERE id = $1`, callID).Scan(&status, &generation, &storedHash); err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	if generation != outcome.LeaseGeneration || !bytes.Equal(storedHash, expectedHash[:]) {
		return gateway.Call{}, gateway.ErrConflict
	}
	if outcome.Status == gateway.CallSucceeded {
		if status != string(gateway.CallPendingDelivery) && status != string(gateway.CallSucceeded) {
			return gateway.Call{}, gateway.ErrConflict
		}
	} else if status != string(outcome.Status) {
		return gateway.Call{}, gateway.ErrConflict
	}
	recovered, err := loadCall(ctx, s.pool, callID, consumerID, false)
	if err != nil {
		return gateway.Call{}, err
	}
	if outcome.SuccessAttempt != nil {
		matches, err := successfulAttemptReplayMatches(ctx, s.pool, callID, recovered, outcome.SuccessAttemptID, *outcome.SuccessAttempt)
		if err != nil {
			return gateway.Call{}, err
		}
		if !matches {
			return gateway.Call{}, gateway.ErrConflict
		}
	}
	return recovered, nil
}

func (s *Store) ConfirmCallDelivery(ctx context.Context, callID string, leaseGeneration int64) (call gateway.Call, resultErr error) {
	defer func() { resultErr = mapGatewayError(resultErr) }()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return gateway.Call{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var consumerID, status string
	var currentGeneration int64
	if err := tx.QueryRow(ctx, `
		SELECT consumer_account_id::text, status, lease_generation
		FROM api_calls WHERE id = $1 FOR UPDATE`, callID).Scan(&consumerID, &status, &currentGeneration); err != nil {
		return gateway.Call{}, err
	}
	if currentGeneration != leaseGeneration {
		return gateway.Call{}, gateway.ErrConflict
	}
	if status == string(gateway.CallSucceeded) {
		current, err := loadCall(ctx, tx, callID, consumerID, false)
		if err != nil {
			return gateway.Call{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return gateway.Call{}, err
		}
		return current, nil
	}
	if status != string(gateway.CallPendingDelivery) {
		return gateway.Call{}, gateway.ErrConflict
	}
	attemptResult, err := tx.Exec(ctx, `
		UPDATE api_call_attempts
		SET status = 'succeeded', semantic_committed = true, completed_at = now()
		WHERE call_id = $1 AND status = 'pending_delivery'`, callID)
	if err != nil {
		return gateway.Call{}, err
	}
	if attemptResult.RowsAffected() != 1 {
		return gateway.Call{}, gateway.ErrConflict
	}
	callResult, err := tx.Exec(ctx, `
		UPDATE api_calls
		SET status = 'succeeded', heartbeat_at = NULL, lease_expires_at = NULL, completed_at = now()
		WHERE id = $1 AND status = 'pending_delivery' AND lease_generation = $2`, callID, leaseGeneration)
	if err != nil {
		return gateway.Call{}, err
	}
	if callResult.RowsAffected() != 1 {
		return gateway.Call{}, gateway.ErrConflict
	}
	confirmed, err := loadCall(ctx, tx, callID, consumerID, false)
	if err != nil {
		return gateway.Call{}, err
	}
	if commitErr := s.commitGatewayTransaction(ctx, tx, "api_call.confirm_delivery", callID); commitErr != nil {
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
	var consumerID, status, finalOfferID string
	var currentGeneration int64
	var finalHTTPStatus int
	if err := tx.QueryRow(ctx, `
		SELECT consumer_account_id::text, status, lease_generation,
			COALESCE(final_offer_id::text, ''), COALESCE(final_http_status, 0)
		FROM api_calls WHERE id = $1 FOR UPDATE`, callID).Scan(
		&consumerID, &status, &currentGeneration, &finalOfferID, &finalHTTPStatus,
	); err != nil {
		return gateway.Call{}, err
	}
	if currentGeneration != leaseGeneration {
		return gateway.Call{}, gateway.ErrConflict
	}
	if status == string(gateway.CallIncomplete) {
		var storedReason string
		if err := tx.QueryRow(ctx, `SELECT reason FROM api_call_compensations WHERE call_id = $1`, callID).Scan(&storedReason); err != nil {
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
	if status != string(gateway.CallPendingDelivery) {
		return gateway.Call{}, gateway.ErrConflict
	}
	var providerCharge, platformFee int64
	var originalTransactionID sql.NullString
	if err := tx.QueryRow(ctx, `
		SELECT provider_charge_nano, platform_fee_nano,
			COALESCE(capture_transaction_id::text, self_transaction_id::text)
		FROM api_call_settlements WHERE call_id = $1 FOR UPDATE`, callID).Scan(
		&providerCharge, &platformFee, &originalTransactionID,
	); err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	reversalID := ""
	if originalTransactionID.Valid {
		payloadHash := sha256.Sum256([]byte("api-call-delivery-reversal-v1\x00" + callID + "\x00" + originalTransactionID.String))
		reversal, err := (&LedgerTransaction{Tx: tx}).ReverseSystem(
			ctx, "api-call-"+callID+"-delivery-reversal", originalTransactionID.String,
			"reverse api charge after incomplete downstream delivery", callID+":delivery-compensation", payloadHash,
		)
		if err != nil {
			return gateway.Call{}, err
		}
		reversalID = reversal.ID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO api_call_compensations (
			call_id, reason, original_transaction_id, reversal_transaction_id,
			provider_charge_reversed_nano, platform_fee_reversed_nano
		) VALUES ($1, $2, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, $5, $6)`,
		callID, reason, originalTransactionID.String, reversalID, providerCharge, platformFee,
	); err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	attemptResult, err := tx.Exec(ctx, `
		UPDATE api_call_attempts
		SET status = 'incomplete', error_code = $2,
			raw_error = 'downstream delivery was not durably confirmed', raw_error_truncated = false,
			input_tokens = NULL, output_tokens = NULL, cache_write_tokens = NULL, cache_read_tokens = NULL,
			tokens_per_second_nano = NULL, completed_at = now()
		WHERE call_id = $1 AND status = 'pending_delivery'`, callID, reason)
	if err != nil {
		return gateway.Call{}, err
	}
	if attemptResult.RowsAffected() != 1 {
		return gateway.Call{}, gateway.ErrConflict
	}
	finalizerHash := gateway.FinalizerHash(gateway.CallIncomplete, reason, finalOfferID, finalHTTPStatus, nil)
	callResult, err := tx.Exec(ctx, `
		UPDATE api_calls
		SET status = 'incomplete', decision_code = $2, completion_reason = $2,
			input_tokens = NULL, output_tokens = NULL, cache_write_tokens = NULL, cache_read_tokens = NULL,
			provider_charge_nano = 0, platform_fee_nano = 0,
			heartbeat_at = NULL, lease_expires_at = NULL, finalizer_payload_hash = $3, completed_at = now()
		WHERE id = $1 AND status = 'pending_delivery' AND lease_generation = $4`,
		callID, reason, finalizerHash[:], leaseGeneration,
	)
	if err != nil {
		return gateway.Call{}, err
	}
	if callResult.RowsAffected() != 1 {
		return gateway.Call{}, gateway.ErrConflict
	}
	compensated, err := loadCall(ctx, tx, callID, consumerID, false)
	if err != nil {
		return gateway.Call{}, err
	}
	if commitErr := s.commitGatewayTransaction(ctx, tx, "api_call.compensate_delivery", callID); commitErr != nil {
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
	var storedReason string
	if err := s.pool.QueryRow(ctx, `
		SELECT compensation.reason
		FROM api_calls call
		JOIN api_call_compensations compensation ON compensation.call_id = call.id
		WHERE call.id = $1 AND call.status = 'incomplete' AND call.lease_generation = $2`,
		callID, leaseGeneration,
	).Scan(&storedReason); err != nil {
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

func deriveFinalizationFact(ctx context.Context, tx pgx.Tx, callID string) (finalizationFact, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, status, offer_id::text, COALESCE(http_status, 0), error_code,
			semantic_committed, input_tokens, output_tokens, cache_write_tokens, cache_read_tokens
		FROM api_call_attempts WHERE call_id = $1 ORDER BY sequence`, callID)
	if err != nil {
		return finalizationFact{}, err
	}
	type storedAttempt struct {
		id, status, offerID, errorCode string
		httpStatus                     int
		semanticCommitted              bool
		input, output, write, read     sql.NullInt64
	}
	attempts := make([]storedAttempt, 0)
	for rows.Next() {
		var attempt storedAttempt
		if err := rows.Scan(&attempt.id, &attempt.status, &attempt.offerID, &attempt.httpStatus, &attempt.errorCode,
			&attempt.semanticCommitted, &attempt.input, &attempt.output, &attempt.write, &attempt.read); err != nil {
			rows.Close()
			return finalizationFact{}, err
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return finalizationFact{}, err
	}
	rows.Close()
	for index := range attempts {
		if attempts[index].status != string(gateway.AttemptInProgress) {
			continue
		}
		reason := "orphan_recovered_before_commit"
		if attempts[index].semanticCommitted {
			reason = "orphan_recovered_after_commit"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE api_call_attempts
			SET status = 'incomplete', error_code = $2, raw_error = $3,
				raw_error_truncated = false, completed_at = now()
			WHERE id = $1 AND status = 'in_progress'`, attempts[index].id, reason, "upstream attempt did not durably reach a complete result"); err != nil {
			return finalizationFact{}, mapGatewayError(err)
		}
		attempts[index].status = string(gateway.AttemptIncomplete)
		attempts[index].errorCode = reason
	}
	if len(attempts) == 0 {
		return finalizationFact{Status: gateway.CallIncomplete, Reason: "no_attempt_completed"}, nil
	}
	last := attempts[len(attempts)-1]
	if last.status == string(gateway.AttemptSucceeded) {
		if !last.semanticCommitted || !last.input.Valid || !last.output.Valid || !last.write.Valid || !last.read.Valid {
			// The database constraint normally makes this state unreachable. Fail
			// safe if an externally repaired or corrupted row ever violates it:
			// incomplete calls release their authorization and never charge.
			return finalizationFact{Status: gateway.CallIncomplete, Reason: "invalid_success_attempt", OfferID: last.offerID, HTTPStatus: last.httpStatus}, nil
		}
		usage := &ledger.UsageV1{
			InputTokens: last.input.Int64, OutputTokens: last.output.Int64,
			CacheWriteTokens: last.write.Int64, CacheReadTokens: last.read.Int64,
		}
		return finalizationFact{Status: gateway.CallSucceeded, Reason: "completed", OfferID: last.offerID, HTTPStatus: last.httpStatus, Usage: usage}, nil
	}
	reason := coalesceGatewayReason(last.errorCode, "all_candidates_failed")
	switch last.status {
	case string(gateway.AttemptCancelled):
		return finalizationFact{Status: gateway.CallCancelled, Reason: reason, OfferID: last.offerID, HTTPStatus: last.httpStatus}, nil
	case string(gateway.AttemptIncomplete):
		return finalizationFact{Status: gateway.CallIncomplete, Reason: reason, OfferID: last.offerID, HTTPStatus: last.httpStatus}, nil
	default:
		return finalizationFact{Status: gateway.CallFailed, Reason: reason, HTTPStatus: last.httpStatus}, nil
	}
}

func usageEqual(left, right *ledger.UsageV1) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func successfulAttemptReplayMatches(ctx context.Context, queryer gatewayQueryer, callID string, call gateway.Call, attemptID string, result gateway.AttemptResult) (bool, error) {
	var storedCallID, offerID, status, errorCode, rawError string
	var httpStatus, ttft, duration, input, output, cacheWrite, cacheRead, tps sql.NullInt64
	var semanticCommitted bool
	if err := queryer.QueryRow(ctx, `
		SELECT call_id::text, offer_id::text, status, http_status, error_code, raw_error,
			semantic_committed, ttft_milliseconds, duration_milliseconds,
			input_tokens, output_tokens, cache_write_tokens, cache_read_tokens,
			tokens_per_second_nano
		FROM api_call_attempts WHERE id = $1`, attemptID).Scan(
		&storedCallID, &offerID, &status, &httpStatus, &errorCode, &rawError,
		&semanticCommitted, &ttft, &duration, &input, &output, &cacheWrite, &cacheRead, &tps,
	); err != nil {
		return false, mapGatewayError(err)
	}
	if storedCallID != callID || offerID != call.FinalOfferID ||
		(status != string(gateway.AttemptPendingDelivery) && status != string(gateway.AttemptSucceeded)) ||
		!httpStatus.Valid || int(httpStatus.Int64) != result.HTTPStatus || errorCode != "" || rawError != "" ||
		(status == string(gateway.AttemptPendingDelivery) && semanticCommitted != result.SemanticCommitted) ||
		(status == string(gateway.AttemptSucceeded) && !semanticCommitted) ||
		result.Usage == nil || !input.Valid || !output.Valid || !cacheWrite.Valid || !cacheRead.Valid ||
		input.Int64 != result.Usage.InputTokens || output.Int64 != result.Usage.OutputTokens ||
		cacheWrite.Int64 != result.Usage.CacheWriteTokens || cacheRead.Int64 != result.Usage.CacheReadTokens {
		return false, nil
	}
	expectedTTFT := sql.NullInt64{}
	measuredTTFT := int64(0)
	if result.TTFTObserved {
		measuredTTFT = max(int64(0), result.TTFT.Milliseconds())
		expectedTTFT = sql.NullInt64{Int64: measuredTTFT, Valid: true}
	}
	expectedDuration := sql.NullInt64{Int64: max(int64(0), result.Duration.Milliseconds()), Valid: true}
	expectedTPS := sql.NullInt64{}
	if result.MeasureTPS && result.TTFTObserved && expectedDuration.Int64 > measuredTTFT {
		value := calculateTPSNano(result.Usage.OutputTokens, expectedDuration.Int64-measuredTTFT)
		if value != 0 {
			expectedTPS = sql.NullInt64{Int64: value, Valid: true}
		}
	}
	return nullInt64Equal(ttft, expectedTTFT) && nullInt64Equal(duration, expectedDuration) && nullInt64Equal(tps, expectedTPS), nil
}

func nullInt64Equal(left, right sql.NullInt64) bool {
	return left.Valid == right.Valid && (!left.Valid || left.Int64 == right.Int64)
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

func (s *Store) RecoverOrphanCalls(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	rows, err := tx.Query(ctx, `
		SELECT id::text, status FROM api_calls
		WHERE status IN ('in_progress', 'pending_delivery') AND (heartbeat_at < $1 OR lease_expires_at < now())
		ORDER BY heartbeat_at NULLS FIRST
		FOR UPDATE SKIP LOCKED LIMIT $2`, cutoff, limit)
	if err != nil {
		return 0, err
	}
	type orphanClaim struct {
		id, status string
		generation int64
	}
	claims := make([]orphanClaim, 0, limit)
	for rows.Next() {
		var claim orphanClaim
		if err := rows.Scan(&claim.id, &claim.status); err != nil {
			rows.Close()
			return 0, err
		}
		claims = append(claims, claim)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for index := range claims {
		if err := tx.QueryRow(ctx, `
			UPDATE api_calls
			SET lease_generation = lease_generation + 1,
				heartbeat_at = now(), lease_expires_at = now() + $2
			WHERE id = $1 AND status = $3
			RETURNING lease_generation`, claims[index].id, gateway.DefaultLeaseDuration, claims[index].status).Scan(&claims[index].generation); err != nil {
			return 0, err
		}
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
