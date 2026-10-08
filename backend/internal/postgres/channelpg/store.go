// Package channelpg is the PostgreSQL implementation of channel.Store. SQL
// lives in queries.sql; the generated code is committed beside it. It also
// exports ResolveRoutingTargets and RoutingEligibility so the gateway
// persistence can resolve offers inside its own transaction (ADR-0017).
package channelpg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/catalogpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: New(pool)}
}

var _ channel.Store = (*Store)(nil)

func audit(ctx context.Context, db auditpg.DBTX, actorID, action, targetType, targetID, reason string, details map[string]any) error {
	return auditpg.Record(ctx, db, auditpg.Event{
		ActorID: actorID, Action: action, TargetType: targetType, TargetID: targetID,
		Reason: reason, Details: details,
	})
}

// ---------------------------------------------------------------------------
// Row mapping
// ---------------------------------------------------------------------------

func toChannel(row GetChannelRow) channel.Channel {
	result := channel.Channel{
		ID: row.ID, OwnerAccountID: row.OwnerAccountID, OwnerDisplayName: row.OwnerDisplayName,
		OwnerStatus: identity.Status(row.OwnerStatus), OwnerMustChangePassword: row.OwnerMustChangePassword,
		DisplayName: row.DisplayName, NormalizedBaseURL: row.NormalizedBaseUrl,
		CredentialConfigured: row.CredentialConfigured, CredentialVersion: row.CredentialVersion,
		CredentialUpdatedAt: row.CredentialUpdatedAt, Status: channel.Status(row.Status), Version: row.Version,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	return result
}

// toOffer maps the offer projection. ListChannelOffers and GetOffer select the
// same columns, so callers convert their row type to ListChannelOffersRow.
func toOffer(row ListChannelOffersRow) channel.Offer {
	result := channel.Offer{
		ID: row.ID, ChannelID: row.ChannelID, ModelID: row.ModelID, ModelName: row.ModelName,
		ModelProvider: row.ModelProvider, Protocol: channel.Protocol(row.Protocol),
		UpstreamModelID: row.UpstreamModelID, Multiplier: money.FromNano(row.MultiplierNano),
		Status: channel.OfferStatus(row.Status), ValidationVersion: row.ValidationVersion, Version: row.Version,
		ModelStatus: catalog.Status(row.ModelStatus), ContextWindow: row.ContextWindow,
		InputPrice: row.InputPriceNanoPerMillion, OutputPrice: row.OutputPriceNanoPerMillion,
		CacheWritePrice: row.CacheWritePriceNanoPerMillion, CacheReadPrice: row.CacheReadPriceNanoPerMillion,
		CallSuccessRate: row.SuccessRate, TTFTMilliseconds: row.TtftMilliseconds,
		TokensPerSecond: row.TokensPerSecond, CallCount: row.CallCount,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.ProviderIncomeNano != nil {
		value := money.FromNano(*row.ProviderIncomeNano)
		result.ProviderIncome = &value
	}
	if row.AttemptID != nil {
		attempt := channel.ValidationAttempt{
			ID: *row.AttemptID, OfferID: result.ID, ValidationVersion: result.ValidationVersion,
			HTTPStatus:     int(row.HttpStatus),
			Duration:       durationMilliseconds(row.DurationMilliseconds),
			CompletedAt:    row.CompletedAt,
			AttemptSeq:     deref(row.AttemptSeq),
			ActorAccountID: deref(row.AttemptActorAccountID),
			Status:         channel.ValidationStatus(deref(row.AttemptStatus)),
			ErrorCategory:  channel.ErrorCategory(deref(row.ErrorCategory)),
			RawError:       deref(row.RawError), RawErrorTruncated: deref(row.RawErrorTruncated),
			StartedAt: deref(row.StartedAt),
		}
		result.LatestValidation = &attempt
	}
	return result
}

func deref[T any](value *T) T {
	var zero T
	if value == nil {
		return zero
	}
	return *value
}

func durationMilliseconds(value *int64) time.Duration {
	return time.Duration(deref(value)) * time.Millisecond
}

func toValidationAttempt(row ListValidationAttemptsRow) channel.ValidationAttempt {
	return channel.ValidationAttempt{
		ID: row.ID, OfferID: row.OfferID, ValidationVersion: row.ValidationVersion,
		AttemptSeq: row.AttemptSeq, ActorAccountID: row.ActorAccountID,
		Status: channel.ValidationStatus(row.Status), ErrorCategory: channel.ErrorCategory(row.ErrorCategory),
		HTTPStatus: int(row.HttpStatus), RawError: row.RawError, RawErrorTruncated: row.RawErrorTruncated,
		Duration: durationMilliseconds(row.DurationMilliseconds), StartedAt: row.StartedAt,
		CompletedAt: row.CompletedAt,
	}
}

func offerRoutingLease(offer channel.Offer) channel.RoutingLease {
	return channel.RoutingLease{
		ContextWindow: offer.ContextWindow, Multiplier: offer.Multiplier,
		InputPrice: offer.InputPrice, OutputPrice: offer.OutputPrice,
		CacheWritePrice: offer.CacheWritePrice, CacheReadPrice: offer.CacheReadPrice,
		PriceTiers: offer.PriceTiers,
	}
}

// ---------------------------------------------------------------------------
// Composite reads
// ---------------------------------------------------------------------------

// loadChannel loads one channel with its offers, price tiers and routing
// eligibility. db may be a pool or a transaction.
func loadChannel(ctx context.Context, db DBTX, channelID string) (channel.Channel, error) {
	q := New(db)
	row, err := q.GetChannel(ctx, channelID)
	if err != nil {
		return channel.Channel{}, mapChannelError(err)
	}
	result := toChannel(row)
	offerRows, err := q.ListChannelOffers(ctx, ListChannelOffersParams{ChannelID: channelID, IncludeDeleted: true})
	if err != nil {
		return channel.Channel{}, err
	}
	offers := make([]channel.Offer, 0, len(offerRows))
	for _, offerRow := range offerRows {
		offers = append(offers, toOffer(offerRow))
	}
	result.Offers, err = attachOfferPriceTiers(ctx, db, offers)
	if err != nil {
		return channel.Channel{}, err
	}
	for index := range result.Offers {
		validationStatus := ""
		if result.Offers[index].LatestValidation != nil {
			validationStatus = string(result.Offers[index].LatestValidation.Status)
		}
		result.Offers[index].Eligible, result.Offers[index].IneligibleReason = RoutingEligibility(
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

func attachOfferPriceTiers(ctx context.Context, db DBTX, offers []channel.Offer) ([]channel.Offer, error) {
	modelIDs := make([]string, 0, len(offers))
	for _, offer := range offers {
		modelIDs = append(modelIDs, offer.ModelID)
	}
	tiers, err := catalogpg.PriceTiersByModel(ctx, db, modelIDs)
	if err != nil {
		return nil, err
	}
	for index := range offers {
		offers[index].PriceTiers = tiers[offers[index].ModelID]
	}
	return offers, nil
}

func offerByID(ctx context.Context, db DBTX, offerID string) (channel.Offer, error) {
	row, err := New(db).GetOffer(ctx, offerID)
	if err != nil {
		return channel.Offer{}, mapChannelError(err)
	}
	offers, err := attachOfferPriceTiers(ctx, db, []channel.Offer{toOffer(ListChannelOffersRow(row))})
	if err != nil {
		return channel.Offer{}, err
	}
	return offers[0], nil
}

func loadChannels(ctx context.Context, db DBTX, ids []string) ([]channel.Channel, error) {
	result := make([]channel.Channel, 0, len(ids))
	for _, id := range ids {
		value, err := loadChannel(ctx, db, id)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Channels
// ---------------------------------------------------------------------------

func (s *Store) CreateChannel(ctx context.Context, command channel.CreateCommand) (channel.Channel, error) {
	var created channel.Channel
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		inserted, err := q.InsertChannel(ctx, InsertChannelParams{
			ID: command.ChannelID, OwnerAccountID: command.OwnerAccountID, DisplayName: command.DisplayName,
			NormalizedBaseUrl: command.NormalizedBaseURL, CredentialVersion: command.Credential.Version,
		})
		if err != nil {
			return mapChannelError(err)
		}
		if inserted != 1 {
			return channel.ErrForbidden
		}
		if err := q.InsertCredential(ctx, InsertCredentialParams{
			ChannelID: command.ChannelID, CredentialVersion: command.Credential.Version,
			KeyID: command.Credential.KeyID, Nonce: command.Credential.Nonce, Ciphertext: command.Credential.Ciphertext,
		}); err != nil {
			return mapChannelError(err)
		}
		channelModels := make(map[string]string)
		for _, offer := range command.Offers {
			channelModelID := channelModels[offer.ModelID]
			if channelModelID == "" {
				modelStatus, err := q.LockModelStatusShared(ctx, offer.ModelID)
				if err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return channel.ErrUnavailable
					}
					return err
				}
				if catalog.Status(modelStatus) != catalog.StatusActive {
					return channel.ErrUnavailable
				}
				channelModelID, err = q.InsertChannelModel(ctx, InsertChannelModelParams{
					ChannelID: command.ChannelID, ModelID: offer.ModelID, MultiplierNano: offer.Multiplier,
				})
				if err != nil {
					return mapChannelError(err)
				}
				channelModels[offer.ModelID] = channelModelID
			} else {
				existing, err := q.GetChannelModelMultiplier(ctx, channelModelID)
				if err != nil {
					return err
				}
				if existing != offer.Multiplier {
					return channel.ErrInvalidInput
				}
			}
			if err := q.InsertOffer(ctx, InsertOfferParams{
				ID: offer.ID, ChannelModelID: channelModelID, Protocol: string(offer.Protocol),
				UpstreamModelID: offer.UpstreamModelID,
			}); err != nil {
				return mapChannelError(err)
			}
		}
		if err := audit(ctx, tx, command.OwnerAccountID, "channel.created", "channel", command.ChannelID, "owner created channel draft", map[string]any{
			"status": channel.StatusDraft, "offer_count": len(command.Offers), "credential_version": command.Credential.Version,
		}); err != nil {
			return err
		}
		created, err = loadChannel(ctx, tx, command.ChannelID)
		return err
	})
	if err != nil {
		return channel.Channel{}, err
	}
	return created, nil
}

func (s *Store) ListOwnerChannels(ctx context.Context, ownerID string) ([]channel.Channel, error) {
	ids, err := s.q.ListOwnerChannelIDs(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	return loadChannels(ctx, s.pool, ids)
}

func (s *Store) GetOwnerChannel(ctx context.Context, ownerID, channelID string) (channel.Channel, error) {
	allowed, err := s.q.IsChannelOwner(ctx, IsChannelOwnerParams{ID: channelID, OwnerAccountID: ownerID})
	if err != nil {
		return channel.Channel{}, err
	}
	if !allowed {
		return channel.Channel{}, channel.ErrNotFound
	}
	return loadChannel(ctx, s.pool, channelID)
}

func (s *Store) UpdateChannel(ctx context.Context, command channel.UpdateCommand) (channel.Channel, error) {
	var updated channel.Channel
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		current, err := q.LockOwnerChannel(ctx, LockOwnerChannelParams{ID: command.ChannelID, OwnerAccountID: command.ActorAccountID})
		if err != nil {
			return mapChannelError(err)
		}
		if channel.Status(current.Status) == channel.StatusDeleted {
			return channel.ErrConflict
		}
		if current.Version != command.ExpectedVersion {
			return channel.ErrConflict
		}
		semanticCredentialChange := command.Credential != nil
		if semanticCredentialChange && command.Credential.Version != current.CredentialVersion+1 {
			return channel.ErrConflict
		}
		validationChange := command.BaseURLChanged || semanticCredentialChange
		newCredentialVersion := current.CredentialVersion
		if semanticCredentialChange {
			newCredentialVersion = command.Credential.Version
		}
		if err := q.UpdateChannelConfig(ctx, UpdateChannelConfigParams{
			ID: command.ChannelID, DisplayName: command.DisplayName, NormalizedBaseUrl: command.NormalizedBaseURL,
			CredentialVersion: newCredentialVersion, CredentialChanged: semanticCredentialChange,
		}); err != nil {
			return mapChannelError(err)
		}
		if command.Credential != nil {
			if err := q.UpsertCredential(ctx, UpsertCredentialParams{
				ChannelID: command.ChannelID, CredentialVersion: command.Credential.Version,
				KeyID: command.Credential.KeyID, Nonce: command.Credential.Nonce, Ciphertext: command.Credential.Ciphertext,
			}); err != nil {
				return mapChannelError(err)
			}
		}
		if validationChange {
			if err := q.ResetChannelOfferValidation(ctx, command.ChannelID); err != nil {
				return err
			}
		}
		if err := audit(ctx, tx, command.ActorAccountID, "channel.updated", "channel", command.ChannelID, "owner updated channel configuration", map[string]any{
			"version": current.Version + 1, "base_url_changed": command.BaseURLChanged,
			"credential_changed": semanticCredentialChange, "credential_version": newCredentialVersion,
		}); err != nil {
			return err
		}
		updated, err = loadChannel(ctx, tx, command.ChannelID)
		return err
	})
	if err != nil {
		return channel.Channel{}, err
	}
	return updated, nil
}

func (s *Store) SetChannelStatus(ctx context.Context, command channel.StatusCommand) (channel.Channel, error) {
	var updated channel.Channel
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var currentStatus string
		var currentVersion int64
		if command.Administrator {
			row, err := q.LockChannelForAdmin(ctx, LockChannelForAdminParams{ID: command.ChannelID, ActorAccountID: command.ActorAccountID})
			if err != nil {
				return mapChannelError(err)
			}
			currentStatus, currentVersion = row.Status, row.Version
		} else {
			row, err := q.LockChannelForOwner(ctx, LockChannelForOwnerParams{ID: command.ChannelID, ActorAccountID: command.ActorAccountID})
			if err != nil {
				return mapChannelError(err)
			}
			currentStatus, currentVersion = row.Status, row.Version
		}
		if currentVersion != command.ExpectedVersion || !validChannelTransition(channel.Status(currentStatus), command.Status, command.Administrator) {
			return channel.ErrConflict
		}
		if command.Status == channel.StatusPublished {
			eligibleOfferCount, err := q.CountPublishableOffers(ctx, command.ChannelID)
			if err != nil {
				return mapChannelError(err)
			}
			if eligibleOfferCount == 0 {
				return channel.ErrUnavailable
			}
		}
		deleted := command.Status == channel.StatusDeleted
		if err := q.UpdateChannelStatus(ctx, UpdateChannelStatusParams{
			ID: command.ChannelID, Status: string(command.Status), Deleted: deleted,
		}); err != nil {
			return mapChannelError(err)
		}
		if deleted {
			if err := q.DeleteCredential(ctx, command.ChannelID); err != nil {
				return err
			}
			if err := q.ResetChannelOfferValidation(ctx, command.ChannelID); err != nil {
				return err
			}
		}
		reason := command.Reason
		if reason == "" {
			reason = "owner changed channel lifecycle"
		}
		if err := audit(ctx, tx, command.ActorAccountID, "channel.status_changed", "channel", command.ChannelID, reason, map[string]any{
			"from": currentStatus, "to": command.Status, "administrator": command.Administrator, "version": currentVersion + 1,
		}); err != nil {
			return err
		}
		var err error
		updated, err = loadChannel(ctx, tx, command.ChannelID)
		return err
	})
	if err != nil {
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
	var updated channel.Channel
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := q.LockChannelWithCredential(ctx, LockChannelWithCredentialParams{ID: channelID, ActorAccountID: actorID})
		if err != nil {
			return mapChannelError(err)
		}
		if locked.Version != expectedVersion {
			return channel.ErrConflict
		}
		if err := q.DeleteCredential(ctx, channelID); err != nil {
			return err
		}
		if err := q.BumpChannelCredentialRevoked(ctx, channelID); err != nil {
			return err
		}
		if err := q.ResetChannelOfferValidation(ctx, channelID); err != nil {
			return err
		}
		if err := audit(ctx, tx, actorID, "channel.credential_revoked", "channel", channelID, "owner revoked platform copy of upstream credential", map[string]any{
			"version": locked.Version + 1,
		}); err != nil {
			return err
		}
		updated, err = loadChannel(ctx, tx, channelID)
		return err
	})
	if err != nil {
		return channel.Channel{}, err
	}
	return updated, nil
}

// ---------------------------------------------------------------------------
// Offers
// ---------------------------------------------------------------------------

func (s *Store) AddOffer(ctx context.Context, command channel.AddOfferCommand) (channel.Offer, error) {
	var created channel.Offer
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		currentVersion, err := q.LockLiveOwnerChannelVersion(ctx, LockLiveOwnerChannelVersionParams{
			ID: command.ChannelID, OwnerAccountID: command.ActorAccountID,
		})
		if err != nil {
			return mapChannelError(err)
		}
		if currentVersion != command.ExpectedChannelVersion {
			return channel.ErrConflict
		}
		modelStatus, err := q.LockModelStatusShared(ctx, command.Offer.ModelID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return channel.ErrUnavailable
			}
			return mapChannelError(err)
		}
		if catalog.Status(modelStatus) != catalog.StatusActive {
			return channel.ErrUnavailable
		}
		var channelModelID string
		existing, err := q.LockChannelModel(ctx, LockChannelModelParams{ChannelID: command.ChannelID, ModelID: command.Offer.ModelID})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			channelModelID, err = q.InsertChannelModel(ctx, InsertChannelModelParams{
				ChannelID: command.ChannelID, ModelID: command.Offer.ModelID, MultiplierNano: command.Offer.Multiplier,
			})
			if err != nil {
				return mapChannelError(err)
			}
		case err != nil:
			return mapChannelError(err)
		default:
			channelModelID = existing.ID
			if existing.MultiplierNano != command.Offer.Multiplier {
				if existing.LiveOfferCount > 0 {
					return channel.ErrConflict
				}
				if err := q.UpdateChannelModelMultiplier(ctx, UpdateChannelModelMultiplierParams{
					ID: channelModelID, MultiplierNano: command.Offer.Multiplier,
				}); err != nil {
					return mapChannelError(err)
				}
			}
		}
		if err := q.InsertOffer(ctx, InsertOfferParams{
			ID: command.Offer.ID, ChannelModelID: channelModelID, Protocol: string(command.Offer.Protocol),
			UpstreamModelID: command.Offer.UpstreamModelID,
		}); err != nil {
			return mapChannelError(err)
		}
		if err := q.BumpChannelVersion(ctx, command.ChannelID); err != nil {
			return err
		}
		if err := audit(ctx, tx, command.ActorAccountID, "channel.offer_created", "channel_offer", command.Offer.ID, "owner added protocol offer", map[string]any{
			"channel_id": command.ChannelID, "model_id": command.Offer.ModelID, "protocol": command.Offer.Protocol,
			"channel_version": currentVersion + 1,
		}); err != nil {
			return err
		}
		created, err = offerByID(ctx, tx, command.Offer.ID)
		return err
	})
	if err != nil {
		return channel.Offer{}, err
	}
	return created, nil
}

func (s *Store) UpdateOffer(ctx context.Context, command channel.OfferUpdateCommand) (channel.Offer, error) {
	var updated channel.Offer
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		current, err := q.LockOfferForUpdate(ctx, LockOfferForUpdateParams{ID: command.OfferID, OwnerAccountID: command.ActorAccountID})
		if err != nil {
			return mapChannelError(err)
		}
		if current.Version != command.ExpectedVersion {
			return channel.ErrConflict
		}
		if channel.Protocol(current.Protocol) == channel.ProtocolGemini && (strings.HasPrefix(strings.ToLower(command.UpstreamModelID), "models/") || strings.Contains(command.UpstreamModelID, "/")) {
			return channel.ErrInvalidInput
		}
		upstreamChanged := current.UpstreamModelID != command.UpstreamModelID
		multiplierChanged := current.MultiplierNano != command.Multiplier
		affectedOffers := int64(1)
		if multiplierChanged {
			if err := q.UpdateChannelModelMultiplier(ctx, UpdateChannelModelMultiplierParams{
				ID: current.ChannelModelID, MultiplierNano: command.Multiplier,
			}); err != nil {
				return mapChannelError(err)
			}
			affectedOffers, err = q.UpdateSiblingOffers(ctx, UpdateSiblingOffersParams{
				OfferID: command.OfferID, UpstreamModelID: command.UpstreamModelID,
				UpstreamChanged: upstreamChanged, ChannelModelID: current.ChannelModelID,
			})
			if err != nil {
				return mapChannelError(err)
			}
		} else if err := q.UpdateOfferUpstream(ctx, UpdateOfferUpstreamParams{
			ID: command.OfferID, UpstreamModelID: command.UpstreamModelID, UpstreamChanged: upstreamChanged,
		}); err != nil {
			return mapChannelError(err)
		}
		if err := audit(ctx, tx, command.ActorAccountID, "channel.offer_updated", "channel_offer", command.OfferID, "owner updated offer", map[string]any{
			"version": current.Version + 1, "upstream_model_changed": upstreamChanged, "multiplier_changed": multiplierChanged,
			"multiplier_nano": command.Multiplier.Nano(), "affected_offer_count": affectedOffers,
		}); err != nil {
			return err
		}
		updated, err = offerByID(ctx, tx, command.OfferID)
		return err
	})
	if err != nil {
		return channel.Offer{}, err
	}
	return updated, nil
}

func (s *Store) SetOfferStatus(ctx context.Context, command channel.OfferStatusCommand) (channel.Offer, error) {
	var updated channel.Offer
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		current, err := q.LockOfferForStatus(ctx, LockOfferForStatusParams{ID: command.OfferID, OwnerAccountID: command.ActorAccountID})
		if err != nil {
			return mapChannelError(err)
		}
		if current.Version != command.ExpectedVersion || !validOfferTransition(channel.OfferStatus(current.Status), command.Status) {
			return channel.ErrConflict
		}
		if err := q.UpdateOfferStatus(ctx, UpdateOfferStatusParams{ID: command.OfferID, Status: string(command.Status)}); err != nil {
			return mapChannelError(err)
		}
		if err := audit(ctx, tx, command.ActorAccountID, "channel.offer_status_changed", "channel_offer", command.OfferID, "owner changed offer lifecycle", map[string]any{
			"from": current.Status, "to": command.Status, "version": current.Version + 1,
		}); err != nil {
			return err
		}
		updated, err = offerByID(ctx, tx, command.OfferID)
		return err
	})
	if err != nil {
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

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

func (s *Store) StartValidation(ctx context.Context, actor identity.Account, offerID string) (channel.ValidationTarget, error) {
	var target channel.ValidationTarget
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// Lock the channel before the offer. Configuration changes use the same
		// order, so the target below is one coherent Base URL, credential and offer
		// validation-version snapshot rather than a mixture of concurrent edits.
		lockedChannelID, err := q.LockChannelForValidation(ctx, LockChannelForValidationParams{ActorAccountID: actor.ID, OfferID: offerID})
		if err != nil {
			return mapChannelError(err)
		}
		row, err := q.GetValidationTarget(ctx, GetValidationTargetParams{ActorAccountID: actor.ID, OfferID: offerID, ChannelID: lockedChannelID})
		if err != nil {
			return mapChannelError(err)
		}
		attempt := channel.ValidationAttempt{
			OfferID: offerID, ValidationVersion: row.ValidationVersion, AttemptSeq: row.ValidationAttemptSeq + 1,
			ActorAccountID: actor.ID, Status: channel.ValidationInProgress,
		}
		started, err := q.InsertValidationAttempt(ctx, InsertValidationAttemptParams{
			OfferID: offerID, ValidationVersion: row.ValidationVersion, AttemptSeq: attempt.AttemptSeq, ActorAccountID: actor.ID,
		})
		if err != nil {
			return mapChannelError(err)
		}
		attempt.ID, attempt.StartedAt = started.ID, started.StartedAt
		if err := q.SetOfferAttemptSeq(ctx, SetOfferAttemptSeqParams{ID: offerID, AttemptSeq: attempt.AttemptSeq}); err != nil {
			return err
		}
		if err := audit(ctx, tx, actor.ID, "channel.validation_started", "channel_offer", offerID, "authorized actor started explicit upstream validation", map[string]any{
			"validation_version": row.ValidationVersion, "attempt_seq": attempt.AttemptSeq,
		}); err != nil {
			return err
		}
		target = channel.ValidationTarget{
			Attempt: attempt, ChannelID: row.ChannelID, OwnerAccountID: row.OwnerAccountID,
			NormalizedBaseURL: row.NormalizedBaseUrl, Protocol: channel.Protocol(row.Protocol),
			UpstreamModelID: row.UpstreamModelID,
			Credential: channel.EncryptedCredential{
				Version: row.CredentialVersion, KeyID: row.KeyID, Nonce: row.Nonce, Ciphertext: row.Ciphertext,
			},
		}
		return nil
	})
	if err != nil {
		return channel.ValidationTarget{}, err
	}
	return target, nil
}

func (s *Store) CompleteValidation(ctx context.Context, attempt channel.ValidationAttempt) error {
	if attempt.Status != channel.ValidationPassed && attempt.Status != channel.ValidationFailed || attempt.CompletedAt == nil {
		return channel.ErrInvalidInput
	}
	return pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		updated, err := s.q.WithTx(tx).CompleteValidationAttempt(ctx, CompleteValidationAttemptParams{
			ID: attempt.ID, OfferID: attempt.OfferID, ValidationVersion: attempt.ValidationVersion, AttemptSeq: attempt.AttemptSeq,
			Status: string(attempt.Status), ErrorCategory: string(attempt.ErrorCategory), HttpStatus: int32(attempt.HTTPStatus),
			RawError: attempt.RawError, RawErrorTruncated: attempt.RawErrorTruncated,
			DurationMilliseconds: attempt.Duration.Milliseconds(),
		})
		if err != nil {
			return mapChannelError(err)
		}
		if updated != 1 {
			return channel.ErrConflict
		}
		return audit(ctx, tx, attempt.ActorAccountID, "channel.validation_completed", "channel_offer", attempt.OfferID, "upstream validation attempt completed", map[string]any{
			"validation_version":    attempt.ValidationVersion,
			"attempt_seq":           attempt.AttemptSeq,
			"status":                attempt.Status,
			"error_category":        attempt.ErrorCategory,
			"duration_milliseconds": attempt.Duration.Milliseconds(),
		})
	})
}

func (s *Store) ExpireValidationAttempts(ctx context.Context, before time.Time) (int64, error) {
	var count int64
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		expired, err := s.q.WithTx(tx).ExpireValidationAttempts(ctx, before)
		if err != nil {
			return mapChannelError(err)
		}
		for _, item := range expired {
			if err := audit(ctx, tx, item.ActorAccountID, "channel.validation_recovered", "channel_offer", item.OfferID, "abandoned upstream validation attempt expired", map[string]any{
				"validation_version": item.ValidationVersion, "attempt_seq": item.AttemptSeq, "error_category": channel.ErrorTimeout,
			}); err != nil {
				return err
			}
		}
		count = int64(len(expired))
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) ListValidationAttempts(ctx context.Context, actor identity.Account, offerID string, limit int) ([]channel.ValidationAttempt, error) {
	rows, err := s.q.ListValidationAttempts(ctx, ListValidationAttemptsParams{ActorAccountID: actor.ID, OfferID: offerID, RowLimit: int64(limit)})
	if err != nil {
		return nil, mapChannelError(err)
	}
	attempts := make([]channel.ValidationAttempt, 0, len(rows))
	for _, row := range rows {
		attempts = append(attempts, toValidationAttempt(row))
	}
	if len(attempts) == 0 {
		permitted, err := s.q.CanViewOfferValidation(ctx, CanViewOfferValidationParams{ActorAccountID: actor.ID, OfferID: offerID})
		if err != nil {
			return nil, mapChannelError(err)
		}
		if !permitted {
			return nil, channel.ErrNotFound
		}
	}
	return attempts, nil
}

// ---------------------------------------------------------------------------
// Administration
// ---------------------------------------------------------------------------

func (s *Store) ListAdminChannels(ctx context.Context) ([]channel.Channel, error) {
	ids, err := s.q.ListAllChannelIDs(ctx)
	if err != nil {
		return nil, err
	}
	return loadChannels(ctx, s.pool, ids)
}

func (s *Store) GetAdminChannel(ctx context.Context, channelID string) (channel.Channel, error) {
	return loadChannel(ctx, s.pool, channelID)
}

func (s *Store) CredentialInventory(ctx context.Context) ([]channel.ReencryptTarget, error) {
	rows, err := s.q.CredentialInventory(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]channel.ReencryptTarget, 0, len(rows))
	for _, row := range rows {
		result = append(result, reencryptTarget(row.ChannelID, row.CredentialVersion, row.KeyID, row.Nonce, row.Ciphertext))
	}
	return result, nil
}

func (s *Store) CredentialTargetsForReencrypt(ctx context.Context, activeKeyID string, limit int) ([]channel.ReencryptTarget, error) {
	rows, err := s.q.CredentialTargetsForReencrypt(ctx, CredentialTargetsForReencryptParams{ActiveKeyID: activeKeyID, RowLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	result := make([]channel.ReencryptTarget, 0, len(rows))
	for _, row := range rows {
		result = append(result, reencryptTarget(row.ChannelID, row.CredentialVersion, row.KeyID, row.Nonce, row.Ciphertext))
	}
	return result, nil
}

func reencryptTarget(channelID string, version int64, keyID string, nonce, ciphertext []byte) channel.ReencryptTarget {
	return channel.ReencryptTarget{
		ChannelID:  channelID,
		Credential: channel.EncryptedCredential{Version: version, KeyID: keyID, Nonce: nonce, Ciphertext: ciphertext},
	}
}

func (s *Store) StoreReencryptedCredential(ctx context.Context, target channel.ReencryptTarget, credential channel.EncryptedCredential, actorID string) error {
	return pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.LockChannelForReencrypt(ctx, LockChannelForReencryptParams{ID: target.ChannelID, ActorAccountID: actorID}); err != nil {
			return mapChannelError(err)
		}
		// Compare-and-swap: only replace the ciphertext still equal to the value read.
		replaced, err := q.ReplaceCredentialCiphertext(ctx, ReplaceCredentialCiphertextParams{
			ChannelID: target.ChannelID, CredentialVersion: credential.Version,
			KeyID: credential.KeyID, Nonce: credential.Nonce, Ciphertext: credential.Ciphertext,
			OldKeyID: target.Credential.KeyID, OldNonce: target.Credential.Nonce, OldCiphertext: target.Credential.Ciphertext,
		})
		if err != nil {
			return mapChannelError(err)
		}
		if replaced != 1 {
			return channel.ErrConflict
		}
		if err := q.BumpChannelVersion(ctx, target.ChannelID); err != nil {
			return err
		}
		return audit(ctx, tx, actorID, "channel.credential_reencrypted", "channel", target.ChannelID, "administrator reencrypted credential with active platform key", map[string]any{
			"credential_version": credential.Version, "key_id": credential.KeyID,
		})
	})
}

func (s *Store) ResolveRoutingTargets(ctx context.Context, offerIDs []string) ([]channel.PoolOfferStatus, []channel.RoutingTarget, error) {
	return ResolveRoutingTargets(ctx, s.pool, offerIDs)
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
