package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/localtime"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// currentPrices are the model's prices at this moment for a request with no
// prompt tokens yet: for display only. Billing selects the tier from the
// real request (ADR-0012).
func currentPrices(model catalog.Model, now time.Time) (ledger.Prices, int) {
	return ledger.SelectPriceTier(model.BasePrices(), model.PriceTiers, 0, now)
}

// effectivePricesJSON is the OpenAPI EffectivePrices schema.
type effectivePricesJSON struct {
	Input      string `json:"input"`
	Output     string `json:"output"`
	CacheWrite string `json:"cache_write"`
	CacheRead  string `json:"cache_read"`
}

// defaultKeyJSON is the OpenAPI DefaultKey schema.
type defaultKeyJSON struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
}

// homeTodayJSON is the today object of the OpenAPI HomeResponse schema.
type homeTodayJSON struct {
	Spend          string `json:"spend"`
	Calls          int64  `json:"calls"`
	SucceededCalls int64  `json:"succeeded_calls"`
}

// homeChannelsJSON is the channels object of the OpenAPI HomeResponse schema.
type homeChannelsJSON struct {
	TodayRevenue string `json:"today_revenue"`
	Online       int64  `json:"online"`
	Total        int64  `json:"total"`
}

// homeResponseJSON is the OpenAPI HomeResponse schema.
type homeResponseJSON struct {
	Points           pointsSummaryJSON `json:"points"`
	Today            homeTodayJSON     `json:"today"`
	Channels         homeChannelsJSON  `json:"channels"`
	DefaultKey       *defaultKeyJSON   `json:"default_key"`
	PendingC2CTrades int64             `json:"pending_c2c_trades"`
	RecentCalls      []callSummaryJSON `json:"recent_calls"`
}

// newEffectivePricesJSON converts prices in the model's currency into the
// points a channel charges: multiplier × rate (ledger.ChannelRate).
func newEffectivePricesJSON(prices ledger.Prices, multiplierNano, rateNano int64) effectivePricesJSON {
	return effectivePricesJSON{
		Input:      ledger.ScalePriceAt(prices.InputPerMillion, multiplierNano, rateNano).String(),
		Output:     ledger.ScalePriceAt(prices.OutputPerMillion, multiplierNano, rateNano).String(),
		CacheWrite: ledger.ScalePriceAt(prices.CacheWritePerMillion, multiplierNano, rateNano).String(),
		CacheRead:  ledger.ScalePriceAt(prices.CacheReadPerMillion, multiplierNano, rateNano).String(),
	}
}

// channelPricing is how one channel prices one model at the current rate:
// the conversion rate its multiplier applies with and the multiplier it
// effectively applies to the model's point price.
type channelPricing struct {
	RateNano      int64
	EffectiveNano int64
	FXDiscount    bool
}

func newChannelPricing(model catalog.Model, multiplierNano int64, fxDiscount bool, usdRateNano int64) channelPricing {
	rate := ledger.ChannelRate(model.PriceCurrency, fxDiscount, usdRateNano)
	return channelPricing{
		RateNano:      rate,
		EffectiveNano: ledger.EffectiveMultiplier(multiplierNano, rate, model.Rate(usdRateNano)),
		FXDiscount:    model.PriceCurrency == ledger.CurrencyUSD && fxDiscount,
	}
}

func ratio(value float64) string { return strconv.FormatFloat(value, 'f', 6, 64) }

// newGatewayCallSummaryJSON builds the CallSummary schema from the gateway's home page rows.
func newGatewayCallSummaryJSON(call gateway.CallSummary) callSummaryJSON {
	var channelRef *channelRefJSON
	if call.ChannelID != nil {
		channelRef = &channelRefJSON{ID: *call.ChannelID}
		if call.ChannelName != nil {
			channelRef.Name = *call.ChannelName
		}
	}
	charged := money.Amount(0)
	if call.Booked {
		charged = money.FromNano((call.Cost + call.Fee).Nano())
	}
	return callSummaryJSON{
		ID:             call.ID,
		CreatedAt:      call.CreatedAt,
		CompletedAt:    call.CompletedAt,
		ModelID:        call.ModelID,
		RequestedModel: call.RequestedModel,
		Format:         string(call.Format),
		Stream:         call.Stream,
		Tag:            call.Tag,
		APIKey:         newKeyRefJSON(call.KeyID, call.KeyName),
		Outcome:        call.Outcome,
		Channel:        channelRef,
		AttemptCount:   call.AttemptCount,
		Usage: usageJSON{
			InputTokens: call.Usage.InputTokens, OutputTokens: call.Usage.OutputTokens,
			CacheWriteTokens: call.Usage.CacheWriteTokens, CacheReadTokens: call.Usage.CacheReadTokens,
		},
		Cost:       call.Cost.String(),
		Fee:        call.Fee.String(),
		Charged:    charged.String(),
		TTFTMS:     widenMillis(call.TTFTMS),
		DurationMS: widenMillis(call.DurationMS),
	}
}

// widenMillis converts the gateway's int32 milliseconds to the int the call schemas share.
func widenMillis(value *int32) *int {
	if value == nil {
		return nil
	}
	wide := int(*value)
	return &wide
}

func (a *app) getHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := accountFromContext(ctx).ID
	var marketState *groupchannel.State
	if a.groupChannels != nil {
		if a.groupChannels.Runtime == nil {
			writeGroupChannelError(w, groupchannel.ErrUnavailable)
			return
		}
		var err error
		marketState, err = a.groupChannels.Runtime.Current()
		if err != nil {
			writeGroupChannelError(w, err)
			return
		}
	}
	if err := a.keys.EnsureDefault(ctx, accountID); err != nil {
		writeDomainError(w, err)
		return
	}
	points, err := a.ledger.Points(ctx, accountID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	stats, err := a.browse.Home(ctx, accountID, localtime.DayStart(time.Now()))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if marketState != nil {
		stats.ChannelsOnline, stats.ChannelsTotal = 0, 0
		for _, v := range marketState.Views {
			if v.Owner.ID == accountID {
				stats.ChannelsTotal++
				if v.Available {
					stats.ChannelsOnline++
				}
			}
		}
	}
	recent, err := a.browse.RecentCalls(ctx, accountID, 5)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	keys, err := a.keys.List(ctx, accountID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	var defaultKey *defaultKeyJSON
	for _, key := range keys {
		if key.IsDefault {
			defaultKey = &defaultKeyJSON{ID: key.ID, Name: key.Name, Prefix: key.Prefix}
		}
	}
	calls := make([]callSummaryJSON, 0, len(recent))
	for _, call := range recent {
		calls = append(calls, newGatewayCallSummaryJSON(call))
	}
	writeJSON(w, http.StatusOK, homeResponseJSON{
		Points:           pointsSummaryJSON{Balance: points.Balance.String(), CreditLimit: points.CreditLimit.String(), Available: points.Available().String()},
		Today:            homeTodayJSON{Spend: stats.TodaySpend.String(), Calls: stats.TodayCalls, SucceededCalls: stats.TodaySucceeded},
		Channels:         homeChannelsJSON{TodayRevenue: stats.RevenueToday.String(), Online: stats.ChannelsOnline, Total: stats.ChannelsTotal},
		DefaultKey:       defaultKey,
		PendingC2CTrades: stats.PendingTrades,
		RecentCalls:      calls,
	})
}

// registerBrowseRoutes registers the account home page. Market browsing has its own routes.
func (a *app) registerBrowseRoutes(r *router) {
	r.implement("B", "GET /api/home", accessReady, a.getHome)
}
