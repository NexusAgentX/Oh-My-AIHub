package gateway

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

type Service struct {
	store    Store
	outbound OutboundFactory
}

func NewService(store Store, outbound OutboundFactory) (*Service, error) {
	if store == nil || outbound == nil {
		return nil, ErrInvalidInput
	}
	return &Service{store: store, outbound: outbound}, nil
}

func (s *Service) CreateAPIKey(ctx context.Context, actor identity.Account, input KeyConfigInput) (CreatedAPIKey, error) {
	input, err := normalizeKeyConfig(input)
	if !readyActor(actor) || err != nil {
		return CreatedAPIKey{}, ErrInvalidInput
	}
	secret, prefix, hash, err := generateAPIKey()
	if err != nil {
		return CreatedAPIKey{}, err
	}
	created, err := s.store.CreateAPIKey(ctx, actor.ID, input.DisplayName, prefix, hash, input.Pools)
	if err != nil {
		return CreatedAPIKey{}, err
	}
	return CreatedAPIKey{APIKey: created, Secret: secret}, nil
}

func (s *Service) ListAPIKeys(ctx context.Context, actor identity.Account) ([]APIKey, error) {
	if !readyActor(actor) {
		return nil, ErrForbidden
	}
	return s.store.ListAPIKeys(ctx, actor.ID)
}

func (s *Service) GetAPIKey(ctx context.Context, actor identity.Account, keyID string) (APIKey, error) {
	if !readyActor(actor) || strings.TrimSpace(keyID) == "" {
		return APIKey{}, ErrForbidden
	}
	return s.store.GetAPIKey(ctx, actor.ID, strings.TrimSpace(keyID))
}

func (s *Service) UpdateAPIKey(ctx context.Context, actor identity.Account, keyID string, expectedVersion int64, input KeyConfigInput) (APIKey, error) {
	input, err := normalizeKeyConfig(input)
	if !readyActor(actor) || strings.TrimSpace(keyID) == "" || expectedVersion <= 0 || err != nil {
		return APIKey{}, ErrInvalidInput
	}
	return s.store.UpdateAPIKey(ctx, actor.ID, strings.TrimSpace(keyID), expectedVersion, input)
}

func (s *Service) RotateAPIKey(ctx context.Context, actor identity.Account, keyID string, expectedVersion int64) (CreatedAPIKey, error) {
	if !readyActor(actor) || strings.TrimSpace(keyID) == "" || expectedVersion <= 0 {
		return CreatedAPIKey{}, ErrInvalidInput
	}
	secret, prefix, hash, err := generateAPIKey()
	if err != nil {
		return CreatedAPIKey{}, err
	}
	updated, err := s.store.RotateAPIKey(ctx, actor.ID, strings.TrimSpace(keyID), expectedVersion, prefix, hash)
	if err != nil {
		return CreatedAPIKey{}, err
	}
	return CreatedAPIKey{APIKey: updated, Secret: secret}, nil
}

func (s *Service) SetAPIKeyStatus(ctx context.Context, actor identity.Account, keyID string, expectedVersion int64, status KeyStatus) (APIKey, error) {
	if !readyActor(actor) || strings.TrimSpace(keyID) == "" || expectedVersion <= 0 || (status != KeyActive && status != KeyDisabled && status != KeyDeleted) {
		return APIKey{}, ErrInvalidInput
	}
	return s.store.SetAPIKeyStatus(ctx, actor.ID, strings.TrimSpace(keyID), expectedVersion, status)
}

// AddPoolMember is the market-detail shortcut for adding one currently eligible
// offer without forcing the client to reconstruct the key's complete pool graph.
// UpdateAPIKey still performs the single CAS-protected transactional reorder.
func (s *Service) AddPoolMember(ctx context.Context, actor identity.Account, keyID string, expectedVersion int64, modelID string, protocol channel.Protocol, offerID string, priority int) (APIKey, error) {
	if !readyActor(actor) || strings.TrimSpace(keyID) == "" || expectedVersion <= 0 ||
		!validCanonicalModelID(strings.TrimSpace(modelID)) || !validProtocol(protocol) || strings.TrimSpace(offerID) == "" || priority <= 0 {
		return APIKey{}, ErrInvalidInput
	}
	key, err := s.store.GetAPIKey(ctx, actor.ID, strings.TrimSpace(keyID))
	if err != nil {
		return APIKey{}, err
	}
	if key.Version != expectedVersion {
		return APIKey{}, ErrConflict
	}
	inputs := make([]PoolInput, 0, len(key.Pools)+1)
	found := false
	for _, pool := range key.Pools {
		input := PoolInput{CanonicalModelID: pool.CanonicalModelID, Protocol: pool.Protocol, OfferIDs: make([]string, 0, len(pool.Members)+1)}
		for _, member := range pool.Members {
			if member.OfferID == offerID {
				return APIKey{}, ErrConflict
			}
			input.OfferIDs = append(input.OfferIDs, member.OfferID)
		}
		if pool.CanonicalModelID == strings.TrimSpace(modelID) && pool.Protocol == protocol {
			found = true
			insertAt := priority - 1
			if priority > len(input.OfferIDs)+1 {
				return APIKey{}, ErrInvalidInput
			}
			input.OfferIDs = append(input.OfferIDs, "")
			copy(input.OfferIDs[insertAt+1:], input.OfferIDs[insertAt:])
			input.OfferIDs[insertAt] = strings.TrimSpace(offerID)
		}
		inputs = append(inputs, input)
	}
	if !found {
		if priority != 1 {
			return APIKey{}, ErrInvalidInput
		}
		inputs = append(inputs, PoolInput{CanonicalModelID: strings.TrimSpace(modelID), Protocol: protocol, OfferIDs: []string{strings.TrimSpace(offerID)}})
	}
	return s.store.UpdateAPIKey(ctx, actor.ID, key.ID, expectedVersion, KeyConfigInput{DisplayName: key.DisplayName, Pools: inputs})
}

func (s *Service) Authenticate(ctx context.Context, secret string) (AuthenticatedKey, error) {
	hash, err := hashAPIKey(secret)
	if err != nil {
		return AuthenticatedKey{}, err
	}
	return s.store.AuthenticateAPIKey(ctx, hash)
}

func (s *Service) BeginCall(ctx context.Context, authenticated AuthenticatedKey, protocol channel.Protocol, canonicalModelID string) (CallPlan, error) {
	if authenticated.ID == "" || authenticated.OwnerAccountID == "" || !validProtocol(protocol) || !validCanonicalModelID(canonicalModelID) {
		return CallPlan{}, ErrInvalidInput
	}
	request := BeginCallRequest{
		Authenticated: authenticated, Protocol: protocol, CanonicalModelID: canonicalModelID,
		LeaseDuration: DefaultLeaseDuration,
	}
	for attempt := 0; attempt < 3; attempt++ {
		plan, err := s.store.BeginCall(ctx, request, s.outbound.ResolveRoutingLeasesWithStore)
		if !errors.Is(err, ErrSnapshotRetry) {
			return plan, err
		}
	}
	return CallPlan{}, ErrSnapshotRetry
}

func (s *Service) StartAttempt(ctx context.Context, callID string, candidate Candidate) (Attempt, error) {
	if strings.TrimSpace(callID) == "" || candidate.LeaseGeneration <= 0 {
		return Attempt{}, ErrInvalidInput
	}
	return s.store.StartAttempt(ctx, callID, candidate)
}

func (s *Service) CompleteAttempt(ctx context.Context, attemptID string, result AttemptResult) (Attempt, error) {
	if strings.TrimSpace(attemptID) == "" || result.LeaseGeneration <= 0 || result.Status == AttemptInProgress ||
		result.Status == AttemptPendingDelivery || result.Status == AttemptSucceeded || (result.MeasureTPS && !result.TTFTObserved) {
		return Attempt{}, ErrInvalidInput
	}
	return s.store.CompleteAttempt(ctx, attemptID, result)
}

func (s *Service) MarkAttemptCommitted(ctx context.Context, attemptID string, observation AttemptCommitObservation) error {
	if strings.TrimSpace(attemptID) == "" || observation.LeaseGeneration <= 0 || observation.TTFT < 0 || observation.Duration < observation.TTFT {
		return ErrInvalidInput
	}
	return s.store.MarkAttemptCommitted(ctx, strings.TrimSpace(attemptID), observation)
}

func (s *Service) Heartbeat(ctx context.Context, callID string, leaseGeneration int64) error {
	if strings.TrimSpace(callID) == "" || leaseGeneration <= 0 {
		return ErrInvalidInput
	}
	return s.store.HeartbeatCall(ctx, strings.TrimSpace(callID), leaseGeneration)
}

func (s *Service) Finalize(ctx context.Context, callID string, outcome FinalizeOutcome) (Call, error) {
	if callID == "" || outcome.LeaseGeneration <= 0 || (outcome.Status != CallSucceeded && outcome.Status != CallFailed && outcome.Status != CallIncomplete && outcome.Status != CallCancelled) {
		return Call{}, ErrInvalidInput
	}
	if outcome.SuccessAttempt != nil {
		result := outcome.SuccessAttempt
		if outcome.Status != CallSucceeded || strings.TrimSpace(outcome.SuccessAttemptID) == "" ||
			result.Status != AttemptSucceeded || result.HTTPStatus < 200 || result.HTTPStatus >= 300 ||
			result.LeaseGeneration != outcome.LeaseGeneration || result.HTTPStatus != outcome.HTTPStatus || result.Usage == nil || !sameUsage(result.Usage, outcome.Usage) || result.ErrorCode != "" || result.RawError != "" ||
			(result.MeasureTPS && !result.TTFTObserved) {
			return Call{}, ErrInvalidInput
		}
	} else if outcome.SuccessAttemptID != "" {
		return Call{}, ErrInvalidInput
	}
	for attempt := 0; attempt < 3; attempt++ {
		result, err := s.store.FinalizeCall(ctx, callID, outcome)
		if !errors.Is(err, ErrSnapshotRetry) {
			return result, err
		}
	}
	return Call{}, ErrSnapshotRetry
}

func sameUsage(left, right *ledger.UsageV1) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (s *Service) ConfirmDelivery(ctx context.Context, callID string, leaseGeneration int64) (Call, error) {
	if strings.TrimSpace(callID) == "" || leaseGeneration <= 0 {
		return Call{}, ErrInvalidInput
	}
	for attempt := 0; attempt < 3; attempt++ {
		result, err := s.store.ConfirmCallDelivery(ctx, strings.TrimSpace(callID), leaseGeneration)
		if !errors.Is(err, ErrSnapshotRetry) {
			return result, err
		}
	}
	return Call{}, ErrSnapshotRetry
}

func (s *Service) CompensateDelivery(ctx context.Context, callID string, leaseGeneration int64, reason string) (Call, error) {
	reason = strings.TrimSpace(reason)
	if strings.TrimSpace(callID) == "" || leaseGeneration <= 0 || reason == "" || len(reason) > 128 {
		return Call{}, ErrInvalidInput
	}
	for attempt := 0; attempt < 3; attempt++ {
		result, err := s.store.CompensateCallDelivery(ctx, strings.TrimSpace(callID), leaseGeneration, reason)
		if !errors.Is(err, ErrSnapshotRetry) {
			return result, err
		}
	}
	return Call{}, ErrSnapshotRetry
}

func (s *Service) RecoverOrphans(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		return 0, ErrInvalidInput
	}
	return s.store.RecoverOrphanCalls(ctx, cutoff, limit)
}

func (s *Service) ListCalls(ctx context.Context, actor identity.Account, limit int) ([]Call, error) {
	if !readyActor(actor) || limit < 1 || limit > 100 {
		return nil, ErrInvalidInput
	}
	return s.store.ListCalls(ctx, actor, limit)
}

func (s *Service) GetCall(ctx context.Context, actor identity.Account, callID string) (Call, error) {
	if !readyActor(actor) || strings.TrimSpace(callID) == "" {
		return Call{}, ErrInvalidInput
	}
	return s.store.GetCall(ctx, actor, strings.TrimSpace(callID))
}

func (s *Service) Dashboard(ctx context.Context, actor identity.Account) (Dashboard, error) {
	if !readyActor(actor) {
		return Dashboard{}, ErrForbidden
	}
	return s.store.Dashboard(ctx, actor.ID)
}

func normalizeKeyConfig(input KeyConfigInput) (KeyConfigInput, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" || len([]rune(input.DisplayName)) > 80 || len(input.Pools) > 100 {
		return KeyConfigInput{}, ErrInvalidInput
	}
	seenPools := make(map[string]struct{}, len(input.Pools))
	for poolIndex := range input.Pools {
		pool := &input.Pools[poolIndex]
		pool.CanonicalModelID = strings.TrimSpace(pool.CanonicalModelID)
		identityKey := pool.CanonicalModelID + "\x00" + string(pool.Protocol)
		if !validCanonicalModelID(pool.CanonicalModelID) || !validProtocol(pool.Protocol) || len(pool.OfferIDs) == 0 || len(pool.OfferIDs) > 100 {
			return KeyConfigInput{}, ErrInvalidInput
		}
		if _, exists := seenPools[identityKey]; exists {
			return KeyConfigInput{}, ErrConflict
		}
		seenPools[identityKey] = struct{}{}
		seenOffers := make(map[string]struct{}, len(pool.OfferIDs))
		for offerIndex := range pool.OfferIDs {
			pool.OfferIDs[offerIndex] = strings.TrimSpace(pool.OfferIDs[offerIndex])
			if pool.OfferIDs[offerIndex] == "" {
				return KeyConfigInput{}, ErrInvalidInput
			}
			if _, exists := seenOffers[pool.OfferIDs[offerIndex]]; exists {
				return KeyConfigInput{}, ErrConflict
			}
			seenOffers[pool.OfferIDs[offerIndex]] = struct{}{}
		}
	}
	return input, nil
}

func validProtocol(protocol channel.Protocol) bool {
	return protocol == channel.ProtocolOpenAIChat || protocol == channel.ProtocolOpenAIResponse || protocol == channel.ProtocolAnthropic || protocol == channel.ProtocolGemini
}

func validCanonicalModelID(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "//") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) || character == '\\' || character == '%' {
			return false
		}
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func readyActor(actor identity.Account) bool {
	return actor.ID != "" && actor.Status == identity.StatusActive && !actor.MustChangePassword
}
