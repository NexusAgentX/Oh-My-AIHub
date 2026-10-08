package channelpg

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/catalogpg"
)

// marketCursor is the opaque keyset cursor. It is bound to the query it was
// issued for (sort, model, protocol and owner filter), so a cursor cannot be
// replayed against a different listing.
type marketCursor struct {
	Sort       string           `json:"sort"`
	ModelID    string           `json:"model_id,omitempty"`
	Protocol   channel.Protocol `json:"protocol,omitempty"`
	OwnerQuery string           `json:"owner,omitempty"`
	OfferID    string           `json:"offer_id"`
	PriceNano  int64            `json:"price_nano,omitempty"`
	Metric     *string          `json:"metric,omitempty"`
}

func encodeMarketCursor(value marketCursor) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeMarketCursor(raw string, query channel.MarketQuery) (marketCursor, error) {
	if raw == "" {
		return marketCursor{Sort: query.Sort, ModelID: query.ModelID, Protocol: query.Protocol, OwnerQuery: query.OwnerQuery}, nil
	}
	encoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(encoded) > 2048 {
		return marketCursor{}, channel.ErrInvalidInput
	}
	var value marketCursor
	if err := json.Unmarshal(encoded, &value); err != nil || value.OfferID == "" ||
		value.Sort != query.Sort || value.ModelID != query.ModelID || value.Protocol != query.Protocol || value.OwnerQuery != query.OwnerQuery {
		return marketCursor{}, channel.ErrInvalidInput
	}
	return value, nil
}

func isMetricSort(sort string) bool { return sort == "success_rate" || sort == "ttft" || sort == "tps" }

func (s *Store) ListMarketOffers(ctx context.Context, query channel.MarketQuery) ([]channel.MarketOffer, string, error) {
	cursor, err := decodeMarketCursor(query.Cursor, query)
	if err != nil {
		return nil, "", err
	}
	params := ListMarketOffersParams{
		ModelID: query.ModelID, Protocol: string(query.Protocol), OwnerQuery: query.OwnerQuery,
		Sort: query.Sort, RowLimit: int64(query.Limit) + 1,
	}
	if cursor.OfferID != "" {
		params.CursorOfferID = &cursor.OfferID
		switch {
		case isMetricSort(query.Sort):
			params.CursorMetric = cursor.Metric
		default:
			params.CursorPrice = cursor.PriceNano
		}
	}
	rows, err := s.q.ListMarketOffers(ctx, params)
	if err != nil {
		return nil, "", mapChannelError(err)
	}
	items := make([]channel.MarketOffer, 0, len(rows))
	for _, row := range rows {
		item := channel.MarketOffer{
			OfferID: row.OfferID, ChannelID: row.ChannelID, ChannelDisplayName: row.ChannelName,
			OwnerAccountID: row.OwnerAccountID, OwnerDisplayName: row.OwnerName,
			ModelID: row.ModelID, ModelName: row.ModelName, ModelProvider: row.ModelProvider,
			Protocol: channel.Protocol(row.Protocol), Multiplier: money.FromNano(row.MultiplierNano),
			InputPrice: money.FromNano(row.InputPriceNano), OutputPrice: money.FromNano(row.OutputPriceNano),
			CacheWritePrice: money.FromNano(row.CacheWritePriceNano), CacheReadPrice: money.FromNano(row.CacheReadPriceNano),
			ValidationStatus: channel.ValidationPassed, LastTestedAt: row.LastTestedAt,
			CallSuccessRate: row.SuccessRate, TTFTMilliseconds: row.TtftMilliseconds,
			TokensPerSecond: row.TokensPerSecond, CallCount: row.CallCount,
		}
		items = append(items, item)
	}
	if len(items) > 0 {
		modelIDs := make([]string, 0, len(items))
		for _, item := range items {
			modelIDs = append(modelIDs, item.ModelID)
		}
		tiers, tierErr := catalogpg.PriceTiersByModel(ctx, s.pool, modelIDs)
		if tierErr != nil {
			return nil, "", tierErr
		}
		for index := range items {
			items[index].PriceTiers = tiers[items[index].ModelID]
		}
	}
	next := ""
	if len(items) > query.Limit {
		last := items[query.Limit-1]
		nextCursor := marketCursor{
			Sort: query.Sort, ModelID: query.ModelID, Protocol: query.Protocol, OwnerQuery: query.OwnerQuery,
			OfferID: last.OfferID,
		}
		switch query.Sort {
		case "output_price":
			nextCursor.PriceNano = last.OutputPrice.Nano()
		case "cache_write_price":
			nextCursor.PriceNano = last.CacheWritePrice.Nano()
		case "cache_read_price":
			nextCursor.PriceNano = last.CacheReadPrice.Nano()
		case "success_rate":
			nextCursor.Metric = last.CallSuccessRate
		case "ttft":
			if last.TTFTMilliseconds != nil {
				value := strconv.FormatInt(*last.TTFTMilliseconds, 10)
				nextCursor.Metric = &value
			}
		case "tps":
			nextCursor.Metric = last.TokensPerSecond
		default:
			nextCursor.PriceNano = last.InputPrice.Nano()
		}
		next, err = encodeMarketCursor(nextCursor)
		if err != nil {
			return nil, "", err
		}
		items = items[:query.Limit]
	}
	return items, next, nil
}

func (s *Store) GetMarketChannel(ctx context.Context, channelID string) (channel.Channel, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return channel.Channel{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	result, err := getMarketChannel(ctx, tx, channelID)
	if err != nil {
		return channel.Channel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return channel.Channel{}, err
	}
	return result, nil
}

// getMarketChannel is the public view of a channel: only published or paused
// channels, and only the offers that are currently routable.
func getMarketChannel(ctx context.Context, db DBTX, channelID string) (channel.Channel, error) {
	result, err := loadChannel(ctx, db, channelID)
	if err != nil {
		return channel.Channel{}, err
	}
	if result.Status != channel.StatusPublished && result.Status != channel.StatusPaused {
		return channel.Channel{}, channel.ErrNotFound
	}
	eligible := make([]channel.Offer, 0, len(result.Offers))
	if result.Status == channel.StatusPublished && result.OwnerStatus == identity.StatusActive && !result.OwnerMustChangePassword && result.CredentialConfigured {
		for _, offer := range result.Offers {
			if offer.Status == channel.OfferActive && offer.ModelStatus == catalog.StatusActive && offer.LatestValidation != nil && offer.LatestValidation.Status == channel.ValidationPassed {
				eligible = append(eligible, offer)
			}
		}
	}
	result.Offers = eligible
	return result, nil
}
