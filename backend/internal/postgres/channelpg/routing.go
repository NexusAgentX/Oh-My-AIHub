package channelpg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/catalogpg"
)

// ResolveRoutingTargets reads the routing facts of the given offers. db may be
// a pool or a transaction, so the gateway persistence can resolve offers inside
// its own snapshot transaction. Unknown offers are reported as ineligible with
// reason "not_found" instead of failing.
func ResolveRoutingTargets(ctx context.Context, db DBTX, offerIDs []string) ([]channel.PoolOfferStatus, []channel.RoutingTarget, error) {
	q := New(db)
	statuses := make([]channel.PoolOfferStatus, 0, len(offerIDs))
	targets := make([]channel.RoutingTarget, 0, len(offerIDs))
	for _, offerID := range offerIDs {
		var status channel.PoolOfferStatus
		status.OfferID = offerID
		row, err := q.GetRoutingRow(ctx, offerID)
		if errors.Is(err, pgx.ErrNoRows) {
			status.Eligible = false
			status.IneligibleReason = "not_found"
			statuses = append(statuses, status)
			continue
		}
		if err != nil {
			return nil, nil, mapChannelError(err)
		}
		status.ChannelID, status.ChannelDisplayName = row.ChannelID, row.ChannelDisplayName
		status.OwnerAccountID, status.OwnerDisplayName = row.OwnerAccountID, row.OwnerDisplayName
		status.ModelID, status.ModelName, status.ModelProvider = row.ModelID, row.ModelName, row.ModelProvider
		status.RatingCount = row.RatingCount
		status.Protocol = channel.Protocol(row.Protocol)
		if row.AverageRating != "" {
			value := row.AverageRating
			status.AverageRating = &value
		}
		status.Multiplier = row.MultiplierNano
		prices, priceErr := channel.CalculateBenchmarkPrices(channel.Offer{
			Multiplier: row.MultiplierNano, InputPrice: row.InputPriceNanoPerMillion, OutputPrice: row.OutputPriceNanoPerMillion,
			CacheWritePrice: row.CacheWritePriceNanoPerMillion, CacheReadPrice: row.CacheReadPriceNanoPerMillion,
		})
		if priceErr == nil {
			status.InputPrice, status.OutputPrice = prices.Input, prices.Output
			status.CacheWritePrice, status.CacheReadPrice = prices.CacheWrite, prices.CacheRead
		}
		validationStatus := deref(row.ValidationStatus)
		status.ValidationStatus = channel.ValidationStatus(validationStatus)
		status.Eligible, status.IneligibleReason = RoutingEligibility(
			identity.Status(row.OwnerStatus), row.OwnerMustChangePassword, channel.Status(row.ChannelStatus),
			channel.OfferStatus(row.OfferStatus), catalog.Status(row.ModelStatus), validationStatus,
			row.CredentialVersion != nil,
		)
		if priceErr != nil {
			status.Eligible = false
			status.IneligibleReason = "price_unrepresentable"
		} else if _, upperErr := gateway.ConservativeNetDebitUpperBound(channel.RoutingLease{
			ContextWindow: row.ContextWindow, Multiplier: row.MultiplierNano,
			InputPrice: row.InputPriceNanoPerMillion, OutputPrice: row.OutputPriceNanoPerMillion,
			CacheWritePrice: row.CacheWritePriceNanoPerMillion, CacheReadPrice: row.CacheReadPriceNanoPerMillion,
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
				ValidationVersion: row.ValidationVersion, CredentialVersion: deref(row.CredentialVersion),
				ContextWindow: row.ContextWindow, InputPrice: row.InputPriceNanoPerMillion, OutputPrice: row.OutputPriceNanoPerMillion,
				CacheWritePrice: row.CacheWritePriceNanoPerMillion, CacheReadPrice: row.CacheReadPriceNanoPerMillion,
				NormalizedBaseURL: row.NormalizedBaseUrl, UpstreamModelID: row.UpstreamModelID,
			},
			Credential: channel.EncryptedCredential{
				Version: deref(row.CredentialVersion), KeyID: deref(row.KeyID), Nonce: row.Nonce, Ciphertext: row.Ciphertext,
			},
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
		tiers, tierErr := catalogpg.PriceTiersByModel(ctx, db, modelIDs)
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

// RoutingEligibility is the single definition of why an offer can or cannot
// serve traffic; the gateway pool projection reuses it.
func RoutingEligibility(owner identity.Status, ownerMustChangePassword bool, channelStatus channel.Status, offerStatus channel.OfferStatus, modelStatus catalog.Status, validationStatus string, credentialConfigured bool) (bool, string) {
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
