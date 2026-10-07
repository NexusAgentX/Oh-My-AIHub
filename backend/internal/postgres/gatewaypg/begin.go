package gatewaypg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/catalogpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
)

// BeginCall snapshots routing, price and spend authorization for one call in a
// single REPEATABLE READ transaction. The spend-authorization hold is created
// in a savepoint so an insufficient-funds failure can still record a rejected
// call in the same transaction.
func (s *Store) BeginCall(ctx context.Context, request gateway.BeginCallRequest, resolver gateway.LeaseResolver) (plan gateway.CallPlan, resultErr error) {
	defer func() { resultErr = mapGatewayError(resultErr) }()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return gateway.CallPlan{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := s.q.WithTx(tx)

	key, err := q.GetKeyForCall(ctx, GetKeyForCallParams{
		ID: request.Authenticated.ID, KeyHash: request.Authenticated.Hash[:], Generation: request.Authenticated.Generation,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return gateway.CallPlan{}, gateway.ErrInvalidAPIKey
	}
	if err != nil {
		return gateway.CallPlan{}, err
	}
	ownerID, ledgerAccountID, keyPrefix, generation := key.OwnerAccountID, key.LedgerAccountID, key.KeyPrefix, key.Generation
	if ownerID != request.Authenticated.OwnerAccountID || generation != request.Authenticated.Generation {
		return gateway.CallPlan{}, gateway.ErrInvalidAPIKey
	}

	feeRate, err := q.LatestFeeRate(ctx)
	if err != nil {
		return gateway.CallPlan{}, err
	}
	callID, err := q.AllocateUUID(ctx)
	if err != nil {
		return gateway.CallPlan{}, err
	}
	reject := func(code string) (gateway.CallPlan, error) {
		created, insertErr := recordRejectedCall(ctx, tx, callID, ownerID, ledgerAccountID, keyPrefix, request, feeRate, code)
		if insertErr != nil {
			return gateway.CallPlan{}, insertErr
		}
		if commitErr := s.commit(ctx, tx, "api_call.begin_rejected", callID); commitErr != nil {
			if recovered, recoverErr := s.recoverRejectedCall(ctx, callID, ownerID, request, code); recoverErr == nil {
				return gateway.CallPlan{Call: recovered}, gateway.ErrRejected
			}
			return gateway.CallPlan{}, commitErr
		}
		return gateway.CallPlan{Call: created}, gateway.ErrRejected
	}

	pool, err := q.FindActivePool(ctx, FindActivePoolParams{
		ApiKeyID: request.Authenticated.ID, CanonicalModelID: request.CanonicalModelID, Protocol: string(request.Protocol),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return reject("pool_not_found")
	}
	if err != nil {
		return gateway.CallPlan{}, err
	}
	poolID, poolVersion := pool.ID, pool.Version
	offerReferences, err := q.ListPoolOfferReferences(ctx, poolID)
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
	statuses, leases, err := resolver(ctx, routingTx{db: tx}, offerIDs)
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
	for _, offerID := range offerIDs {
		status, statusExists := statusByOffer[offerID]
		lease, leaseExists := leaseByOffer[offerID]
		if !statusExists || !leaseExists || !status.Eligible ||
			lease.ValidationVersion != addedValidationVersionByOffer[offerID] ||
			lease.ModelID != request.CanonicalModelID || lease.Protocol != request.Protocol {
			continue
		}
		selfChannel := lease.ProviderAccountID == ownerID
		upper, err := gateway.ConservativeNetDebitUpperBound(lease, feeRate.FeeRateNano.Nano(), selfChannel)
		if err != nil {
			unrepresentableCandidates++
			continue
		}
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
		holdService := ledger.NewService(ledgerpg.NewTx(savepoint))
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
	if err := q.InsertAuthorizedCall(ctx, InsertAuthorizedCallParams{
		ID: callID, ConsumerAccountID: ownerID, ConsumerLedgerAccountID: ledgerAccountID,
		ApiKeyID: request.Authenticated.ID, KeyPrefix: keyPrefix, KeyGeneration: generation,
		PoolID: &poolID, PoolVersion: &poolVersion, CanonicalModelID: request.CanonicalModelID,
		Protocol: string(request.Protocol), CandidateCount: int32(len(candidates)), HoldID: holdID,
		PreauthorizedNano: preauthorized, ZeroHoldReason: zeroHoldReason,
		FeeRateVersion: feeRate.Version, FeeRateNano: feeRate.FeeRateNano, LeaseDuration: leaseDuration,
	}); err != nil {
		return gateway.CallPlan{}, mapGatewayError(err)
	}
	for _, candidate := range candidates {
		lease := candidate.Lease
		if err := q.InsertCandidate(ctx, InsertCandidateParams{
			CallID: callID, Priority: int32(candidate.Priority), OfferID: lease.OfferID, ChannelID: lease.ChannelID,
			ProviderAccountID: lease.ProviderAccountID, ValidationVersion: lease.ValidationVersion,
			CredentialVersion: lease.CredentialVersion, UpstreamModelID: lease.UpstreamModelID, ContextWindow: lease.ContextWindow,
			InputPriceNano: lease.InputPrice, OutputPriceNano: lease.OutputPrice,
			CacheWritePriceNano: lease.CacheWritePrice, CacheReadPriceNano: lease.CacheReadPrice,
			MultiplierNano: lease.Multiplier, SelfChannel: candidate.SelfChannel, NetDebitUpperBoundNano: candidate.NetDebitUpper,
		}); err != nil {
			return gateway.CallPlan{}, mapGatewayError(err)
		}
	}
	// Every candidate of one call resolves the same canonical model, so its
	// conditional tier table is a call-level fact. Snapshot it now: tier
	// selection happens at settlement, when the final usage is known.
	if len(candidates) > 0 {
		if err := insertCallPriceTiers(ctx, q, callID, candidates[0].Lease.PriceTiers); err != nil {
			return gateway.CallPlan{}, err
		}
	}
	created, err := loadCall(ctx, tx, callID, ownerID, false)
	if err != nil {
		return gateway.CallPlan{}, err
	}
	if commitErr := s.commit(ctx, tx, "api_call.begin", callID); commitErr != nil {
		if recovered, recoverErr := s.recoverBegunCall(ctx, callID, ownerID, request, candidates, preauthorized); recoverErr == nil {
			return recovered, nil
		}
		return gateway.CallPlan{}, commitErr
	}
	// last_used_at is an observational convenience, not part of the routing or
	// accounting snapshot. Keep this hot-row write outside REPEATABLE READ so
	// concurrent calls using one Key do not force otherwise independent call
	// snapshots into avoidable serialization retries.
	_ = s.q.TouchAPIKey(ctx, request.Authenticated.ID)
	return gateway.CallPlan{Call: created, Candidates: candidates}, nil
}

func recordRejectedCall(ctx context.Context, tx pgx.Tx, callID, ownerID, ledgerAccountID, keyPrefix string, request gateway.BeginCallRequest, feeRate LatestFeeRateRow, code string) (gateway.Call, error) {
	err := New(tx).InsertRejectedCall(ctx, InsertRejectedCallParams{
		ID: callID, ConsumerAccountID: ownerID, ConsumerLedgerAccountID: ledgerAccountID,
		ApiKeyID: request.Authenticated.ID, KeyPrefix: keyPrefix, KeyGeneration: request.Authenticated.Generation,
		CanonicalModelID: request.CanonicalModelID, Protocol: string(request.Protocol), DecisionCode: code,
		FeeRateVersion: feeRate.Version, FeeRateNano: feeRate.FeeRateNano,
	})
	if err != nil {
		return gateway.Call{}, mapGatewayError(err)
	}
	return loadCall(ctx, tx, callID, ownerID, false)
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
		if recovered.HoldID == "" {
			return gateway.CallPlan{}, gateway.ErrConflict
		}
		hold, err := s.q.GetHoldFacts(ctx, recovered.HoldID)
		if err != nil {
			return gateway.CallPlan{}, err
		}
		if hold.Status != "active" || hold.AmountNano != preauthorized || hold.RemainingNano != hold.AmountNano ||
			hold.CapturedNano != 0 || hold.ReleasedNano != 0 {
			return gateway.CallPlan{}, gateway.ErrConflict
		}
	} else if recovered.HoldID != "" {
		return gateway.CallPlan{}, gateway.ErrConflict
	}
	stored, err := s.q.ListCallCandidates(ctx, callID)
	if err != nil {
		return gateway.CallPlan{}, err
	}
	if len(stored) != len(expected) {
		return gateway.CallPlan{}, gateway.ErrConflict
	}
	for index := range expected {
		want, got := expected[index], stored[index]
		lease := want.Lease
		if int(got.Priority) != want.Priority || got.OfferID != lease.OfferID || got.ChannelID != lease.ChannelID ||
			got.ProviderAccountID != lease.ProviderAccountID || got.ValidationVersion != lease.ValidationVersion ||
			got.CredentialVersion != lease.CredentialVersion || got.UpstreamModelID != lease.UpstreamModelID ||
			got.ContextWindow != lease.ContextWindow || got.InputPriceNano != lease.InputPrice ||
			got.OutputPriceNano != lease.OutputPrice || got.CacheWritePriceNano != lease.CacheWritePrice ||
			got.CacheReadPriceNano != lease.CacheReadPrice || got.MultiplierNano != lease.Multiplier ||
			got.SelfChannel != want.SelfChannel || got.NetDebitUpperBoundNano != want.NetDebitUpper {
			return gateway.CallPlan{}, gateway.ErrConflict
		}
		expected[index].LeaseGeneration = recovered.LeaseGeneration
	}
	expectedTiers := []ledger.PriceTier{}
	if len(expected) > 0 {
		expectedTiers = expected[0].Lease.PriceTiers
	}
	storedTiers, err := loadCallPriceTiers(ctx, s.q, callID)
	if err != nil {
		return gateway.CallPlan{}, err
	}
	if !catalogpg.PriceTiersEqual(storedTiers, expectedTiers) {
		return gateway.CallPlan{}, gateway.ErrConflict
	}
	return gateway.CallPlan{Call: recovered, Candidates: expected}, nil
}

func insertCallPriceTiers(ctx context.Context, q *Queries, callID string, tiers []ledger.PriceTier) error {
	for index, tier := range tiers {
		var weekdays []int16
		for _, weekday := range tier.Weekdays {
			weekdays = append(weekdays, int16(weekday))
		}
		if err := q.InsertCallPriceTier(ctx, InsertCallPriceTierParams{
			CallID: callID, Seq: int32(index + 1), Name: tier.Name,
			MinPromptTokens: tier.MinPromptTokens, MaxPromptTokens: tier.MaxPromptTokens,
			Timezone: tier.Timezone, Weekdays: weekdays,
			StartMinuteOfDay: tier.StartMinute, EndMinuteOfDay: tier.EndMinute,
			InputPriceNano: tier.InputPrice, OutputPriceNano: tier.OutputPrice,
			CacheWritePriceNano: tier.CacheWritePrice, CacheReadPriceNano: tier.CacheReadPrice,
		}); err != nil {
			return mapGatewayError(err)
		}
	}
	return nil
}

func loadCallPriceTiers(ctx context.Context, q *Queries, callID string) ([]ledger.PriceTier, error) {
	rows, err := q.ListCallPriceTiers(ctx, callID)
	if err != nil {
		return nil, mapGatewayError(err)
	}
	tiers := make([]ledger.PriceTier, 0, len(rows))
	for _, row := range rows {
		tier := ledger.PriceTier{
			Name: row.Name, MinPromptTokens: row.MinPromptTokens, MaxPromptTokens: row.MaxPromptTokens,
			Timezone: row.Timezone, StartMinute: row.StartMinuteOfDay, EndMinute: row.EndMinuteOfDay,
			InputPrice: row.InputPriceNano, OutputPrice: row.OutputPriceNano,
			CacheWritePrice: row.CacheWritePriceNano, CacheReadPrice: row.CacheReadPriceNano,
		}
		if len(row.Weekdays) > 0 {
			tier.Weekdays = make([]int, len(row.Weekdays))
			for index, weekday := range row.Weekdays {
				tier.Weekdays[index] = int(weekday)
			}
		}
		tiers = append(tiers, tier)
	}
	return tiers, nil
}
