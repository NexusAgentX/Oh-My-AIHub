package api

import (
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
	InputModalities          []string             `json:"input_modalities"`
	OutputModalities         []string             `json:"output_modalities"`
	SupportsTools            bool                 `json:"supports_tools"`
	SupportsStructuredOutput bool                 `json:"supports_structured_output"`
	SupportsVision           bool                 `json:"supports_vision"`
	ParameterInfo            string               `json:"parameter_info"`
	BasePrices               modelPricesJSON      `json:"base_prices"`
	CurrentTier              *tierRefJSON         `json:"current_tier"`
	LowestPrices             *effectivePricesJSON `json:"lowest_prices"`
	Formats                  []channel.Format     `json:"formats"`
	OnlineChannels           int                  `json:"online_channels"`
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

// tokenUsageJSON is the OpenAPI Usage schema. It is not named usageJSON because observe.go still
// uses that name for its map-based helper.
type tokenUsageJSON struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
}

// callChannelRefJSON is the OpenAPI ChannelRef schema. It is not named channelRefJSON because
// observe.go still uses that name for its map-based helper.
type callChannelRefJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// recentCallJSON is the OpenAPI CallSummary schema as built from the gateway's home page rows. It
// is not named callSummaryJSON because observe.go still uses that name for its map-based helper.
type recentCallJSON struct {
	ID             string              `json:"id"`
	CreatedAt      time.Time           `json:"created_at"`
	CompletedAt    *time.Time          `json:"completed_at"`
	ModelID        *string             `json:"model_id"`
	RequestedModel string              `json:"requested_model"`
	Format         channel.Format      `json:"format"`
	Stream         bool                `json:"stream"`
	Tag            *string             `json:"tag"`
	APIKey         *apiKeyRefJSON      `json:"api_key"`
	Outcome        string              `json:"outcome"`
	Channel        *callChannelRefJSON `json:"channel"`
	AttemptCount   int                 `json:"attempt_count"`
	Usage          tokenUsageJSON      `json:"usage"`
	Cost           string              `json:"cost"`
	Fee            string              `json:"fee"`
	Charged        string              `json:"charged"`
	TTFTMS         *int32              `json:"ttft_ms"`
	DurationMS     *int32              `json:"duration_ms"`
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
	RecentCalls      []recentCallJSON  `json:"recent_calls"`
}

func newEffectivePricesJSON(prices ledger.Prices, multiplierNano int64) effectivePricesJSON {
	return effectivePricesJSON{
		Input:      ledger.ScalePrice(prices.InputPerMillion, multiplierNano).String(),
		Output:     ledger.ScalePrice(prices.OutputPerMillion, multiplierNano).String(),
		CacheWrite: ledger.ScalePrice(prices.CacheWritePerMillion, multiplierNano).String(),
		CacheRead:  ledger.ScalePrice(prices.CacheReadPerMillion, multiplierNano).String(),
	}
}

func newCatalogModelJSON(model catalog.Model, online []gateway.OnlineChannel, now time.Time) catalogModelJSON {
	prices, tierSeq := currentPrices(model, now)
	var tier *tierRefJSON
	var lowest *effectivePricesJSON
	if tierSeq > 0 {
		tier = &tierRefJSON{Seq: tierSeq, Name: model.PriceTiers[tierSeq-1].Name}
	}
	formats := []channel.Format{}
	var cheapest int64 = -1
	for _, entry := range online {
		if cheapest < 0 || entry.MultiplierNano < cheapest {
			cheapest = entry.MultiplierNano
		}
		for _, format := range entry.Formats {
			if !slices.Contains(formats, format) {
				formats = append(formats, format)
			}
		}
	}
	if cheapest >= 0 {
		scaled := newEffectivePricesJSON(prices, cheapest)
		lowest = &scaled
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
		InputModalities:          model.InputModalities,
		OutputModalities:         model.OutputModalities,
		SupportsTools:            model.SupportsTools,
		SupportsStructuredOutput: model.SupportsStructuredOutput,
		SupportsVision:           model.SupportsVision,
		ParameterInfo:            model.ParameterInfo,
		BasePrices:               newModelPricesJSON(model.InputPrice, model.OutputPrice, model.CacheWritePrice, model.CacheReadPrice, model.TokenPrices),
		CurrentTier:              tier,
		LowestPrices:             lowest,
		Formats:                  ordered,
		OnlineChannels:           len(online),
	}
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
	grouped := groupByModel(online)
	now := time.Now()
	items := make([]catalogModelJSON, 0, len(models))
	for _, model := range models {
		items = append(items, newCatalogModelJSON(model, grouped[model.ID], now))
	}
	writeJSON(w, http.StatusOK, catalogModelListJSON{Items: items})
}

func ratio(value float64) string { return strconv.FormatFloat(value, 'f', 6, 64) }

func (a *app) getModel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	model, err := a.catalog.Get(ctx, r.PathValue("modelID"))
	if err != nil || !model.Enabled {
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
	defaults, _, err := a.gateway.Settings(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	now := time.Now()
	accountID := accountFromContext(ctx).ID
	prices, _ := currentPrices(model, now)
	stats := a.gateway.Stats(ctx)

	var mine []gateway.OnlineChannel
	for _, entry := range online {
		if entry.ModelID == model.ID {
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
		channels = append(channels, modelChannelJSON{
			ID:                       entry.ChannelID,
			Name:                     entry.ChannelName,
			Owner:                    partyJSON{ID: entry.OwnerID, DisplayName: entry.OwnerName},
			IsMine:                   entry.OwnerID == accountID,
			Formats:                  entry.Formats,
			Multiplier:               money.FromNano(entry.MultiplierNano).String(),
			CurrentPrices:            newEffectivePricesJSON(prices, entry.MultiplierNano),
			SuccessRate24h:           successRate,
			TTFTP50MS:                ttft,
			State:                    string(state),
			CooldownRemainingSeconds: cooldown,
			DailyCapRemaining:        capRemaining,
		})
	}
	tiers := make([]priceTierJSON, 0, len(model.PriceTiers))
	for index, tier := range model.PriceTiers {
		tiers = append(tiers, newPriceTierJSON(index+1, tier))
	}
	pref, err := a.routing.Get(ctx, accountID, "", model.ID)
	if err != nil {
		pref = routing.Default(model.ID)
	}
	writeJSON(w, http.StatusOK, modelDetailJSON{
		Model: newCatalogModelJSON(model, mine, now), PriceTiers: tiers, Channels: channels, Routing: newRoutingPreferenceJSON(pref),
	})
}

func newRecentCallJSON(call gateway.CallSummary) recentCallJSON {
	var channelRef *callChannelRefJSON
	if call.ChannelID != nil {
		channelRef = &callChannelRefJSON{ID: *call.ChannelID}
		if call.ChannelName != nil {
			channelRef.Name = *call.ChannelName
		}
	}
	charged := money.Amount(0)
	if call.Booked {
		charged = money.FromNano((call.Cost + call.Fee).Nano())
	}
	return recentCallJSON{
		ID:             call.ID,
		CreatedAt:      call.CreatedAt,
		CompletedAt:    call.CompletedAt,
		ModelID:        call.ModelID,
		RequestedModel: call.RequestedModel,
		Format:         call.Format,
		Stream:         call.Stream,
		Tag:            call.Tag,
		APIKey:         newApiKeyRefJSON(call.KeyID, call.KeyName),
		Outcome:        call.Outcome,
		Channel:        channelRef,
		AttemptCount:   call.AttemptCount,
		Usage: tokenUsageJSON{
			InputTokens: call.Usage.InputTokens, OutputTokens: call.Usage.OutputTokens,
			CacheWriteTokens: call.Usage.CacheWriteTokens, CacheReadTokens: call.Usage.CacheReadTokens,
		},
		Cost:       call.Cost.String(),
		Fee:        call.Fee.String(),
		Charged:    charged.String(),
		TTFTMS:     call.TTFTMS,
		DurationMS: call.DurationMS,
	}
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
	calls := make([]recentCallJSON, 0, len(recent))
	for _, call := range recent {
		calls = append(calls, newRecentCallJSON(call))
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
