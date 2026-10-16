package api

import (
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/localtime"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

// currentPrices are the model's prices at this moment for a request with no
// prompt tokens yet: for display only. Billing selects the tier from the
// real request (ADR-0012).
func currentPrices(model catalog.Model, now time.Time) (ledger.Prices, int) {
	return ledger.SelectPriceTier(model.BasePrices(), model.PriceTiers, 0, now)
}

// tierRefJSON is the OpenAPI TierRef schema.
type tierRefJSON struct {
	Seq  int    `json:"seq"`
	Name string `json:"name"`
}

// effectivePricesJSON is the OpenAPI EffectivePrices schema.
type effectivePricesJSON struct {
	Input      string `json:"input"`
	Output     string `json:"output"`
	CacheWrite string `json:"cache_write"`
	CacheRead  string `json:"cache_read"`
}

// catalogModelJSON is the OpenAPI CatalogModel schema.
type catalogModelJSON struct {
	ID                       string               `json:"id"`
	DisplayName              string               `json:"display_name"`
	Provider                 string               `json:"provider"`
	ContextWindow            *int64               `json:"context_window"`
	MaxOutputTokens          *int64               `json:"max_output_tokens"`
	InputModalities          []string             `json:"input_modalities"`
	OutputModalities         []string             `json:"output_modalities"`
	SupportsTools            bool                 `json:"supports_tools"`
	SupportsStructuredOutput bool                 `json:"supports_structured_output"`
	SupportsVision           bool                 `json:"supports_vision"`
	ParameterInfo            string               `json:"parameter_info"`
	PriceCurrency            string               `json:"price_currency"`
	USDCNYRate               *string              `json:"usd_cny_rate"`
	BasePrices               modelPricesJSON      `json:"base_prices"`
	CurrentTier              *tierRefJSON         `json:"current_tier"`
	LowestPrices             *effectivePricesJSON `json:"lowest_prices"`
	Formats                  []channel.Format     `json:"formats"`
	OnlineChannels           int                  `json:"online_channels"`
	Custom                   bool                 `json:"custom"`
}

// catalogModelListJSON is the OpenAPI CatalogModelList schema.
type catalogModelListJSON struct {
	Items []catalogModelJSON `json:"items"`
}

// modelChannelJSON is the OpenAPI ModelChannel schema.
type modelChannelJSON struct {
	ID                       string              `json:"id"`
	Name                     string              `json:"name"`
	Owner                    partyJSON           `json:"owner"`
	IsMine                   bool                `json:"is_mine"`
	Formats                  []channel.Format    `json:"formats"`
	Multiplier               string              `json:"multiplier"`
	FXDiscount               bool                `json:"fx_discount"`
	EffectiveMultiplier      string              `json:"effective_multiplier"`
	CurrentPrices            effectivePricesJSON `json:"current_prices"`
	SuccessRate24h           *string             `json:"success_rate_24h"`
	TTFTP50MS                *int64              `json:"ttft_p50_ms"`
	State                    string              `json:"state"`
	CooldownRemainingSeconds *int64              `json:"cooldown_remaining_seconds"`
	DailyCapRemaining        *string             `json:"daily_cap_remaining"`
}

// modelDetailJSON is the OpenAPI ModelDetail schema.
type modelDetailJSON struct {
	Model      catalogModelJSON      `json:"model"`
	PriceTiers []priceTierJSON       `json:"price_tiers"`
	Channels   []modelChannelJSON    `json:"channels"`
	Routing    routingPreferenceJSON `json:"routing"`
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

// newCatalogModelJSON shows every price in points: USD base prices are
// converted at usdRateNano.
func newCatalogModelJSON(model catalog.Model, online []gateway.OnlineChannel, now time.Time, usdRateNano int64) catalogModelJSON {
	prices, tierSeq := currentPrices(model, now)
	points := model.InPoints(usdRateNano)
	var tier *tierRefJSON
	var lowest *effectivePricesJSON
	if tierSeq > 0 {
		tier = &tierRefJSON{Seq: tierSeq, Name: model.PriceTiers[tierSeq-1].Name}
	}
	formats := []channel.Format{}
	var cheapest *gateway.OnlineChannel
	var cheapestPricing channelPricing
	for index, entry := range online {
		pricing := newChannelPricing(model, entry.MultiplierNano, entry.FXDiscount, usdRateNano)
		if cheapest == nil || float64(entry.MultiplierNano)*float64(pricing.RateNano) < float64(cheapest.MultiplierNano)*float64(cheapestPricing.RateNano) {
			cheapest, cheapestPricing = &online[index], pricing
		}
		for _, format := range entry.Formats {
			if !slices.Contains(formats, format) {
				formats = append(formats, format)
			}
		}
	}
	if cheapest != nil {
		scaled := newEffectivePricesJSON(prices, cheapest.MultiplierNano, cheapestPricing.RateNano)
		lowest = &scaled
	}
	var usdRate *string
	if model.PriceCurrency == ledger.CurrencyUSD {
		rate := money.FromNano(usdRateNano).String()
		usdRate = &rate
	}
	ordered := make([]channel.Format, 0, len(formats))
	for _, format := range channel.Formats {
		if slices.Contains(formats, format) {
			ordered = append(ordered, format)
		}
	}
	return catalogModelJSON{
		ID:                       model.ID,
		DisplayName:              model.DisplayName,
		Provider:                 model.Provider,
		ContextWindow:            model.ContextWindow,
		MaxOutputTokens:          model.MaxOutputTokens,
		InputModalities:          model.InputModalities,
		OutputModalities:         model.OutputModalities,
		SupportsTools:            model.SupportsTools,
		SupportsStructuredOutput: model.SupportsStructuredOutput,
		SupportsVision:           model.SupportsVision,
		ParameterInfo:            model.ParameterInfo,
		PriceCurrency:            model.PriceCurrency,
		USDCNYRate:               usdRate,
		BasePrices:               newModelPricesJSON(points.InputPrice, points.OutputPrice, points.CacheWritePrice, points.CacheReadPrice, points.TokenPrices, geoAmounts(points.GeoMultipliers)),
		CurrentTier:              tier,
		LowestPrices:             lowest,
		Formats:                  ordered,
		OnlineChannels:           len(online),
	}
}

// newCustomModelJSON lists a custom model (ADR-0034) of the given online
// channels: only its name, priced at the channel with the lowest 3:1 blend.
func newCustomModelJSON(id string, online []gateway.OnlineChannel) catalogModelJSON {
	var cheapest *channel.CustomPrices
	for _, entry := range online {
		if entry.Custom != nil && (cheapest == nil || entry.Custom.Blended() < cheapest.Blended()) {
			cheapest = entry.Custom
		}
	}
	item := newCatalogModelJSON(cheapest.Model(id), online, time.Time{}, ledger.UnitRate)
	item.Custom = true
	return item
}

// customOnline returns the online channels of a model name that are custom.
func customOnline(entries []gateway.OnlineChannel) []gateway.OnlineChannel {
	var custom []gateway.OnlineChannel
	for _, entry := range entries {
		if entry.Custom != nil {
			custom = append(custom, entry)
		}
	}
	return custom
}

// entryModel is the model an online channel prices: the catalog model, or
// its own prices for a custom model.
func entryModel(model catalog.Model, entry gateway.OnlineChannel) catalog.Model {
	if entry.Custom != nil {
		return entry.Custom.Model(model.ID)
	}
	return model
}

func groupByModel(entries []gateway.OnlineChannel) map[string][]gateway.OnlineChannel {
	grouped := map[string][]gateway.OnlineChannel{}
	for _, entry := range entries {
		grouped[entry.ModelID] = append(grouped[entry.ModelID], entry)
	}
	return grouped
}

func (a *app) listModels(w http.ResponseWriter, r *http.Request) {
	models, err := a.catalog.List(r.Context(), false)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	online, err := a.browse.OnlineChannels(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	defaults, _, err := a.gateway.Settings(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	grouped := groupByModel(online)
	now := time.Now()
	items := make([]catalogModelJSON, 0, len(models))
	listed := map[string]bool{}
	for _, model := range models {
		listed[model.ID] = true
		items = append(items, newCatalogModelJSON(model, grouped[model.ID], now, defaults.USDCNYRateNano))
	}
	var customIDs []string
	for id, entries := range grouped {
		if !listed[id] && len(customOnline(entries)) > 0 {
			customIDs = append(customIDs, id)
		}
	}
	slices.Sort(customIDs)
	for _, id := range customIDs {
		items = append(items, newCustomModelJSON(id, customOnline(grouped[id])))
	}
	writeJSON(w, http.StatusOK, catalogModelListJSON{Items: items})
}

func ratio(value float64) string { return strconv.FormatFloat(value, 'f', 6, 64) }

func (a *app) getModel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	modelID := r.PathValue("modelID")
	model, err := a.catalog.Get(ctx, modelID)
	custom := errors.Is(err, catalog.ErrNotFound)
	if (err != nil && !custom) || (err == nil && !model.Enabled) {
		if err == nil {
			err = catalog.ErrNotFound
		}
		writeDomainError(w, err)
		return
	}
	online, err := a.browse.OnlineChannels(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if custom {
		// Not in the catalog: a custom model if a listed channel offers one.
		var entries []gateway.OnlineChannel
		for _, entry := range online {
			if entry.ModelID == modelID {
				entries = append(entries, entry)
			}
		}
		entries = customOnline(entries)
		if len(entries) == 0 {
			writeDomainError(w, catalog.ErrNotFound)
			return
		}
		model = catalog.Model{ID: modelID, DisplayName: modelID, PriceCurrency: ledger.CurrencyCNY, Enabled: true}
	}
	defaults, _, err := a.gateway.Settings(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	now := time.Now()
	accountID := accountFromContext(ctx).ID
	stats := a.gateway.Stats()

	var mine []gateway.OnlineChannel
	for _, entry := range online {
		if entry.ModelID == model.ID && (entry.Custom != nil) == custom {
			mine = append(mine, entry)
		}
	}
	channels := make([]modelChannelJSON, 0, len(mine))
	for _, entry := range mine {
		state, remaining := a.gateway.State().Describe(entry.ChannelID, gateway.EffectiveLimits(entry.Advanced, defaults), now)
		var successRate, capRemaining *string
		var ttft, cooldown *int64
		if health, ok := stats[entry.ChannelID]; ok && health.Attempts > 0 {
			rate := ratio(health.SuccessRate())
			successRate = &rate
			if health.TTFTP50MS != nil {
				value := int64(*health.TTFTP50MS)
				ttft = &value
			}
		}
		if state == gateway.StateCooldown {
			seconds := int64(remaining.Seconds()) + 1
			cooldown = &seconds
		}
		if cap := entry.Advanced.DailyRevenueCap; cap != nil && *cap > 0 {
			revenue, err := a.browse.ChannelRevenue(ctx, entry.ChannelID, localtime.DayStart(now))
			if err != nil {
				writeDomainError(w, err)
				return
			}
			remaining := 1 - float64(revenue.Nano())/float64(cap.Nano())
			left := ratio(math.Min(1, math.Max(0, remaining)))
			capRemaining = &left
		}
		priced := entryModel(model, entry)
		prices, _ := currentPrices(priced, now)
		pricing := newChannelPricing(priced, entry.MultiplierNano, entry.FXDiscount, defaults.USDCNYRateNano)
		channels = append(channels, modelChannelJSON{
			ID:                       entry.ChannelID,
			Name:                     entry.ChannelName,
			Owner:                    partyJSON{ID: entry.OwnerID, DisplayName: entry.OwnerName},
			IsMine:                   entry.OwnerID == accountID,
			Formats:                  entry.Formats,
			Multiplier:               money.FromNano(entry.MultiplierNano).String(),
			FXDiscount:               pricing.FXDiscount,
			EffectiveMultiplier:      money.FromNano(pricing.EffectiveNano).String(),
			CurrentPrices:            newEffectivePricesJSON(prices, entry.MultiplierNano, pricing.RateNano),
			SuccessRate24h:           successRate,
			TTFTP50MS:                ttft,
			State:                    string(state),
			CooldownRemainingSeconds: cooldown,
			DailyCapRemaining:        capRemaining,
		})
	}
	tiers := make([]priceTierJSON, 0, len(model.PriceTiers))
	for index, tier := range model.InPoints(defaults.USDCNYRateNano).PriceTiers {
		tiers = append(tiers, newPriceTierJSON(index+1, tier))
	}
	pref, err := a.routing.Get(ctx, accountID, "", model.ID)
	if err != nil {
		pref = routing.Default(model.ID)
	}
	summary := newCatalogModelJSON(model, mine, now, defaults.USDCNYRateNano)
	if custom {
		summary = newCustomModelJSON(model.ID, mine)
	}
	writeJSON(w, http.StatusOK, modelDetailJSON{
		Model: summary, PriceTiers: tiers, Channels: channels, Routing: newRoutingPreferenceJSON(pref),
	})
}

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

// registerBrowseRoutes 注册首页与模型浏览路由（Feature B）。
func (a *app) registerBrowseRoutes(r *router) {
	r.implement("B", "GET /api/home", accessReady, a.getHome)
	r.implement("B", "GET /api/models", accessReady, a.listModels)
	r.implement("B", "GET /api/models/{modelID}", accessReady, a.getModel)
}
