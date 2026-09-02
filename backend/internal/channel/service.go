package channel

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

const maxMultiplierNano = int64(1_000_000_000_000)

type Service struct {
	store    Store
	keyring  *Keyring
	outbound *OutboundPolicy
}

func NewService(store Store, keyring *Keyring, outbound *OutboundPolicy) (*Service, error) {
	if store == nil || keyring == nil || outbound == nil {
		return nil, errorsNewConfiguration()
	}
	return &Service{store: store, keyring: keyring, outbound: outbound}, nil
}

func errorsNewConfiguration() error { return fmt.Errorf("channel service dependencies are required") }

func (s *Service) Create(ctx context.Context, actor identity.Account, displayName, baseURL, credential string, offers []OfferInput) (Channel, error) {
	displayName = strings.TrimSpace(displayName)
	if !readyActor(actor) || displayName == "" || len([]rune(displayName)) > 80 || !validCredential(credential) || len(offers) == 0 {
		return Channel{}, ErrInvalidInput
	}
	normalizedBaseURL, err := s.outbound.ValidateBaseURL(ctx, baseURL)
	if err != nil {
		return Channel{}, err
	}
	channelID, err := newID()
	if err != nil {
		return Channel{}, err
	}
	encrypted, err := s.keyring.Encrypt(channelID, 1, credential)
	if err != nil {
		return Channel{}, err
	}
	newOffers := make([]NewOffer, 0, len(offers))
	seen := make(map[string]struct{}, len(offers))
	for _, input := range offers {
		normalized, err := normalizeOfferInput(input)
		if err != nil {
			return Channel{}, err
		}
		identityKey := normalized.ModelID + "\x00" + string(normalized.Protocol)
		if _, exists := seen[identityKey]; exists {
			return Channel{}, ErrConflict
		}
		seen[identityKey] = struct{}{}
		offerID, err := newID()
		if err != nil {
			return Channel{}, err
		}
		newOffers = append(newOffers, NewOffer{ID: offerID, OfferInput: normalized})
	}
	return s.store.CreateChannel(ctx, CreateCommand{
		ChannelID: channelID, OwnerAccountID: actor.ID, DisplayName: displayName,
		NormalizedBaseURL: normalizedBaseURL, Credential: encrypted, Offers: newOffers,
	})
}

func (s *Service) ListMine(ctx context.Context, actor identity.Account) ([]Channel, error) {
	if !readyActor(actor) {
		return nil, ErrForbidden
	}
	return s.store.ListOwnerChannels(ctx, actor.ID)
}

func (s *Service) GetMine(ctx context.Context, actor identity.Account, channelID string) (Channel, error) {
	if !readyActor(actor) {
		return Channel{}, ErrForbidden
	}
	return s.store.GetOwnerChannel(ctx, actor.ID, strings.TrimSpace(channelID))
}

func (s *Service) Update(ctx context.Context, actor identity.Account, channelID string, expectedVersion int64, displayName, baseURL string, replacementCredential *string) (Channel, error) {
	if !readyActor(actor) || expectedVersion <= 0 {
		return Channel{}, ErrInvalidInput
	}
	existing, err := s.store.GetOwnerChannel(ctx, actor.ID, strings.TrimSpace(channelID))
	if err != nil {
		return Channel{}, err
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || len([]rune(displayName)) > 80 {
		return Channel{}, ErrInvalidInput
	}
	normalizedBaseURL, err := s.outbound.ValidateBaseURL(ctx, baseURL)
	if err != nil {
		return Channel{}, err
	}
	var encrypted *EncryptedCredential
	if replacementCredential != nil {
		credential := *replacementCredential
		if !validCredential(credential) {
			return Channel{}, ErrInvalidInput
		}
		value, err := s.keyring.Encrypt(existing.ID, existing.CredentialVersion+1, credential)
		if err != nil {
			return Channel{}, err
		}
		encrypted = &value
	}
	return s.store.UpdateChannel(ctx, UpdateCommand{
		ActorAccountID: actor.ID, ChannelID: existing.ID, ExpectedVersion: expectedVersion,
		DisplayName: displayName, NormalizedBaseURL: normalizedBaseURL,
		BaseURLChanged: normalizedBaseURL != existing.NormalizedBaseURL, Credential: encrypted,
	})
}

func (s *Service) SetStatus(ctx context.Context, actor identity.Account, channelID string, expectedVersion int64, target Status, reason string) (Channel, error) {
	if !readyActor(actor) || expectedVersion <= 0 || (target != StatusPublished && target != StatusPaused && target != StatusDeleted) {
		return Channel{}, ErrInvalidInput
	}
	return s.store.SetChannelStatus(ctx, StatusCommand{
		ActorAccountID: actor.ID, ChannelID: strings.TrimSpace(channelID), ExpectedVersion: expectedVersion,
		Status: target, Reason: strings.TrimSpace(reason), Administrator: false,
	})
}

func (s *Service) RevokeCredential(ctx context.Context, actor identity.Account, channelID string, expectedVersion int64) (Channel, error) {
	if !readyActor(actor) || expectedVersion <= 0 {
		return Channel{}, ErrInvalidInput
	}
	return s.store.RevokeCredential(ctx, actor.ID, strings.TrimSpace(channelID), expectedVersion)
}

func (s *Service) AddOffer(ctx context.Context, actor identity.Account, channelID string, expectedChannelVersion int64, input OfferInput) (Offer, error) {
	if !readyActor(actor) {
		return Offer{}, ErrForbidden
	}
	if expectedChannelVersion <= 0 {
		return Offer{}, ErrInvalidInput
	}
	normalized, err := normalizeOfferInput(input)
	if err != nil {
		return Offer{}, err
	}
	id, err := newID()
	if err != nil {
		return Offer{}, err
	}
	return s.store.AddOffer(ctx, AddOfferCommand{
		ActorAccountID: actor.ID, ChannelID: strings.TrimSpace(channelID), ExpectedChannelVersion: expectedChannelVersion,
		Offer: NewOffer{ID: id, OfferInput: normalized},
	})
}

func (s *Service) UpdateOffer(ctx context.Context, actor identity.Account, offerID string, expectedVersion int64, upstreamModelID string, multiplier money.Amount) (Offer, error) {
	if !readyActor(actor) || expectedVersion <= 0 || multiplier < 0 || multiplier.Nano() > maxMultiplierNano {
		return Offer{}, ErrInvalidInput
	}
	upstreamModelID = strings.TrimSpace(upstreamModelID)
	if !validUpstreamModelID(upstreamModelID, "") {
		return Offer{}, ErrInvalidInput
	}
	return s.store.UpdateOffer(ctx, OfferUpdateCommand{
		ActorAccountID: actor.ID, OfferID: strings.TrimSpace(offerID), ExpectedVersion: expectedVersion,
		UpstreamModelID: upstreamModelID, Multiplier: multiplier, UpstreamIDChanged: true,
	})
}

func (s *Service) SetOfferStatus(ctx context.Context, actor identity.Account, offerID string, expectedVersion int64, target OfferStatus) (Offer, error) {
	if !readyActor(actor) || expectedVersion <= 0 || (target != OfferActive && target != OfferDisabled && target != OfferDeleted) {
		return Offer{}, ErrInvalidInput
	}
	return s.store.SetOfferStatus(ctx, OfferStatusCommand{ActorAccountID: actor.ID, OfferID: strings.TrimSpace(offerID), ExpectedVersion: expectedVersion, Status: target})
}

func (s *Service) ValidateOffer(ctx context.Context, actor identity.Account, offerID string, confirmedUpstreamCost bool) (ValidationAttempt, error) {
	if !readyActor(actor) || !confirmedUpstreamCost {
		return ValidationAttempt{}, ErrInvalidInput
	}
	target, err := s.store.StartValidation(ctx, actor, strings.TrimSpace(offerID))
	if err != nil {
		return ValidationAttempt{}, err
	}
	credential, err := s.keyring.Decrypt(target.ChannelID, target.Credential)
	if err != nil {
		attempt := failedAttempt(target.Attempt, ErrorConfiguration, err.Error())
		if completionErr := s.completeValidation(ctx, attempt); completionErr != nil {
			return ValidationAttempt{}, completionErr
		}
		return attempt, nil
	}
	result := s.probe(ctx, target, credential)
	if err := s.completeValidation(ctx, result); err != nil {
		return ValidationAttempt{}, err
	}
	return result, nil
}

func (s *Service) completeValidation(requestContext context.Context, attempt ValidationAttempt) error {
	completionContext, cancel := context.WithTimeout(context.WithoutCancel(requestContext), 5*time.Second)
	defer cancel()
	return s.store.CompleteValidation(completionContext, attempt)
}

// RecoverAbandonedValidations finalizes attempts that outlived the 15-second
// network probe plus the independent completion window. It is safe for every
// application instance to run because the update only claims in-progress rows.
func (s *Service) RecoverAbandonedValidations(ctx context.Context) (int64, error) {
	return s.store.ExpireValidationAttempts(ctx, time.Now().UTC().Add(-30*time.Second))
}

func (s *Service) ListValidationAttempts(ctx context.Context, actor identity.Account, offerID string, limit int) ([]ValidationAttempt, error) {
	if !readyActor(actor) {
		return nil, ErrForbidden
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		return nil, ErrInvalidInput
	}
	return s.store.ListValidationAttempts(ctx, actor, strings.TrimSpace(offerID), limit)
}

func (s *Service) ListMarket(ctx context.Context, actor identity.Account, query MarketQuery) ([]MarketOffer, string, error) {
	if !readyActor(actor) {
		return nil, "", ErrForbidden
	}
	query.ModelID = strings.TrimSpace(query.ModelID)
	query.OwnerQuery = strings.TrimSpace(query.OwnerQuery)
	if query.Protocol != "" && !validProtocol(query.Protocol) {
		return nil, "", ErrInvalidInput
	}
	if query.Sort == "" {
		query.Sort = "input_price"
	}
	if query.Sort != "input_price" && query.Sort != "output_price" && query.Sort != "cache_write_price" && query.Sort != "cache_read_price" && query.Sort != "rating" {
		return nil, "", ErrInvalidInput
	}
	if query.Limit <= 0 {
		query.Limit = 20
	}
	if query.Limit > 100 {
		return nil, "", ErrInvalidInput
	}
	return s.store.ListMarketOffers(ctx, actor.ID, query)
}

func (s *Service) GetMarketChannel(ctx context.Context, actor identity.Account, channelID string) (Channel, error) {
	if !readyActor(actor) {
		return Channel{}, ErrForbidden
	}
	return s.store.GetMarketChannel(ctx, actor.ID, strings.TrimSpace(channelID))
}

func (s *Service) Rate(ctx context.Context, actor identity.Account, channelID string, score int) (Channel, error) {
	if !readyActor(actor) || score < 1 || score > 5 {
		return Channel{}, ErrInvalidInput
	}
	return s.store.UpsertRating(ctx, actor.ID, strings.TrimSpace(channelID), score)
}

func (s *Service) ListAdmin(ctx context.Context, actor identity.Account) ([]Channel, error) {
	if !actor.IsAdmin {
		return nil, identity.ErrForbidden
	}
	return s.store.ListAdminChannels(ctx)
}

func (s *Service) GetAdmin(ctx context.Context, actor identity.Account, channelID string) (Channel, error) {
	if !actor.IsAdmin {
		return Channel{}, identity.ErrForbidden
	}
	return s.store.GetAdminChannel(ctx, strings.TrimSpace(channelID))
}

func (s *Service) AdminSetStatus(ctx context.Context, actor identity.Account, channelID string, expectedVersion int64, target Status, reason string) (Channel, error) {
	reason = strings.TrimSpace(reason)
	if !actor.IsAdmin {
		return Channel{}, identity.ErrForbidden
	}
	if expectedVersion <= 0 || reason == "" || (target != StatusPaused && target != StatusDeleted) {
		return Channel{}, ErrInvalidInput
	}
	return s.store.SetChannelStatus(ctx, StatusCommand{ActorAccountID: actor.ID, ChannelID: strings.TrimSpace(channelID), ExpectedVersion: expectedVersion, Status: target, Reason: reason, Administrator: true})
}

func (s *Service) ReencryptCredentials(ctx context.Context, actor identity.Account, limit int) (int, error) {
	if !actor.IsAdmin {
		return 0, identity.ErrForbidden
	}
	if limit <= 0 || limit > 1000 {
		return 0, ErrInvalidInput
	}
	targets, err := s.store.CredentialTargetsForReencrypt(ctx, s.keyring.ActiveKeyID(), limit)
	if err != nil {
		return 0, err
	}
	for _, target := range targets {
		plaintext, err := s.keyring.Decrypt(target.ChannelID, target.Credential)
		if err != nil {
			return 0, err
		}
		reencrypted, err := s.keyring.Encrypt(target.ChannelID, target.Credential.Version, plaintext)
		if err != nil {
			return 0, err
		}
		if err := s.store.StoreReencryptedCredential(ctx, target, reencrypted, actor.ID); err != nil {
			return 0, err
		}
	}
	return len(targets), nil
}

func (s *Service) ValidateCredentialInventory(ctx context.Context) error {
	targets, err := s.store.CredentialInventory(ctx)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if _, err := s.keyring.Decrypt(target.ChannelID, target.Credential); err != nil {
			return fmt.Errorf("channel credential inventory is not decryptable: %w", err)
		}
	}
	return nil
}

func (s *Service) ResolveRoutingLeases(ctx context.Context, offerIDs []string) ([]PoolOfferStatus, []RoutingLease, error) {
	return s.ResolveRoutingLeasesWithStore(ctx, s.store, offerIDs)
}

func (s *Service) ResolveRoutingLeasesWithStore(ctx context.Context, store RoutingStore, offerIDs []string) ([]PoolOfferStatus, []RoutingLease, error) {
	statuses, targets, err := store.ResolveRoutingTargets(ctx, offerIDs)
	if err != nil {
		return nil, nil, err
	}
	leases := make([]RoutingLease, 0, len(targets))
	for _, target := range targets {
		credential, err := s.keyring.Decrypt(target.Lease.ChannelID, target.Credential)
		if err != nil {
			for index := range statuses {
				if statuses[index].OfferID == target.Lease.OfferID {
					statuses[index].Eligible = false
					statuses[index].IneligibleReason = "credential_unavailable"
				}
			}
			continue
		}
		target.Lease.Credential = credential
		leases = append(leases, target.Lease)
	}
	return statuses, leases, nil
}

func readyActor(actor identity.Account) bool {
	return actor.ID != "" && actor.Status == identity.StatusActive && !actor.MustChangePassword
}

func normalizeOfferInput(input OfferInput) (OfferInput, error) {
	input.ModelID = strings.TrimSpace(input.ModelID)
	input.UpstreamModelID = strings.TrimSpace(input.UpstreamModelID)
	if input.UpstreamModelID == "" {
		input.UpstreamModelID = input.ModelID
	}
	if input.ModelID == "" || !validProtocol(input.Protocol) || input.Multiplier < 0 || input.Multiplier.Nano() > maxMultiplierNano || !validUpstreamModelID(input.UpstreamModelID, input.Protocol) {
		return OfferInput{}, ErrInvalidInput
	}
	return input, nil
}

func validProtocol(protocol Protocol) bool {
	return protocol == ProtocolOpenAIChat || protocol == ProtocolOpenAIResponse || protocol == ProtocolAnthropic || protocol == ProtocolGemini
}

func validUpstreamModelID(value string, protocol Protocol) bool {
	if value == "" || len([]byte(value)) > 255 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	if protocol == ProtocolGemini && (strings.HasPrefix(strings.ToLower(value), "models/") || strings.Contains(value, "/")) {
		return false
	}
	return true
}

func validCredential(value string) bool {
	if len([]byte(value)) < 1 || len([]byte(value)) > 8192 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
