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

func scaledPrices(prices ledger.Prices, multiplierNano int64) map[string]string {
	return map[string]string{
		"input":       ledger.ScalePrice(prices.InputPerMillion, multiplierNano).String(),
		"output":      ledger.ScalePrice(prices.OutputPerMillion, multiplierNano).String(),
		"cache_write": ledger.ScalePrice(prices.CacheWritePerMillion, multiplierNano).String(),
		"cache_read":  ledger.ScalePrice(prices.CacheReadPerMillion, multiplierNano).String(),
	}
}

func catalogModelResponse(model catalog.Model, online []gateway.OnlineChannel, now time.Time) map[string]any {
	prices, tierSeq := currentPrices(model, now)
	var tier, lowest any
	if tierSeq > 0 {
		tier = map[string]any{"seq": tierSeq, "name": model.PriceTiers[tierSeq-1].Name}
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
		lowest = scaledPrices(prices, cheapest)
	}
	ordered := make([]channel.Format, 0, len(formats))
	for _, format := range channel.Formats {
		if slices.Contains(formats, format) {
			ordered = append(ordered, format)
		}
	}
	return map[string]any{
		"id": model.ID, "display_name": model.DisplayName, "provider": model.Provider, "context_window": model.ContextWindow,
		"input_modalities": model.InputModalities, "output_modalities": model.OutputModalities,
		"supports_tools": model.SupportsTools, "supports_structured_output": model.SupportsStructuredOutput,
		"supports_vision": model.SupportsVision, "parameter_info": model.ParameterInfo,
		"base_prices":  pricesResponse(model.InputPrice, model.OutputPrice, model.CacheWritePrice, model.CacheReadPrice, model.TokenPrices),
		"current_tier": tier, "lowest_prices": lowest, "formats": ordered, "online_channels": len(online),
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
	items := make([]map[string]any, 0, len(models))
	for _, model := range models {
		items = append(items, catalogModelResponse(model, grouped[model.ID], now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
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
	channels := make([]map[string]any, 0, len(mine))
	for _, entry := range mine {
		state, remaining := a.gateway.State().Describe(entry.ChannelID, gateway.Limits2(entry.Advanced, defaults), now)
		var successRate, ttft, cooldown any
		if health, ok := stats[entry.ChannelID]; ok && health.Attempts > 0 {
			successRate = ratio(health.SuccessRate())
			if health.TTFTP50MS != nil {
				ttft = int64(*health.TTFTP50MS)
			}
		}
		if state == gateway.StateCooldown {
			cooldown = int64(remaining.Seconds()) + 1
		}
		var capRemaining any
		if cap := entry.Advanced.DailyRevenueCap; cap != nil && *cap > 0 {
			revenue, err := a.browse.ChannelRevenue(ctx, entry.ChannelID, localtime.DayStart(now))
			if err != nil {
				writeDomainError(w, err)
				return
			}
			remaining := 1 - float64(revenue.Nano())/float64(cap.Nano())
			capRemaining = ratio(math.Min(1, math.Max(0, remaining)))
		}
		channels = append(channels, map[string]any{
			"id": entry.ChannelID, "name": entry.ChannelName, "daily_cap_remaining": capRemaining,
			"owner":   map[string]any{"id": entry.OwnerID, "display_name": entry.OwnerName},
			"is_mine": entry.OwnerID == accountID, "formats": entry.Formats,
			"multiplier":       money.FromNano(entry.MultiplierNano).String(),
			"current_prices":   scaledPrices(prices, entry.MultiplierNano),
			"success_rate_24h": successRate, "ttft_p50_ms": ttft, "state": string(state), "cooldown_remaining_seconds": cooldown,
		})
	}
	tiers := make([]map[string]any, 0, len(model.PriceTiers))
	for index, tier := range model.PriceTiers {
		tiers = append(tiers, priceTierResponse(index+1, tier))
	}
	pref, err := a.routing.Get(ctx, accountID, "", model.ID)
	if err != nil {
		pref = routing.Default(model.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"model": catalogModelResponse(model, mine, now), "price_tiers": tiers, "channels": channels, "routing": routingResponse(pref),
	})
}

func callSummaryResponse(call gateway.CallSummary) map[string]any {
	var apiKey, channelRef any
	if call.KeyID != nil {
		name := ""
		if call.KeyName != nil {
			name = *call.KeyName
		}
		apiKey = map[string]any{"id": *call.KeyID, "name": name}
	}
	if call.ChannelID != nil {
		name := ""
		if call.ChannelName != nil {
			name = *call.ChannelName
		}
		channelRef = map[string]any{"id": *call.ChannelID, "name": name}
	}
	charged := money.Amount(0)
	if call.Booked {
		charged = money.FromNano((call.Cost + call.Fee).Nano())
	}
	return map[string]any{
		"id": call.ID, "created_at": call.CreatedAt, "completed_at": call.CompletedAt, "model_id": call.ModelID, "attempt_count": call.AttemptCount,
		"requested_model": call.RequestedModel, "format": call.Format, "stream": call.Stream, "tag": call.Tag,
		"api_key": apiKey, "outcome": call.Outcome, "channel": channelRef,
		"usage": map[string]int64{
			"input_tokens": call.Usage.InputTokens, "output_tokens": call.Usage.OutputTokens,
			"cache_write_tokens": call.Usage.CacheWriteTokens, "cache_read_tokens": call.Usage.CacheReadTokens,
		},
		"cost": call.Cost.String(), "fee": call.Fee.String(), "charged": charged.String(),
		"ttft_ms": call.TTFTMS, "duration_ms": call.DurationMS,
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
	var defaultKey any
	for _, key := range keys {
		if key.IsDefault {
			defaultKey = map[string]any{"id": key.ID, "name": key.Name, "prefix": key.Prefix}
		}
	}
	calls := make([]map[string]any, 0, len(recent))
	for _, call := range recent {
		calls = append(calls, callSummaryResponse(call))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"points": map[string]string{"balance": points.Balance.String(), "credit_limit": points.CreditLimit.String(), "available": points.Available().String()},
		"today":  map[string]any{"spend": stats.TodaySpend.String(), "calls": stats.TodayCalls, "succeeded_calls": stats.TodaySucceeded},
		"channels": map[string]any{
			"today_revenue": stats.RevenueToday.String(), "online": stats.ChannelsOnline, "total": stats.ChannelsTotal,
		},
		"default_key": defaultKey, "pending_c2c_trades": stats.PendingTrades, "recent_calls": calls,
	})
}

// registerBrowseRoutes 注册首页与模型浏览路由（Feature B）。
func (a *app) registerBrowseRoutes(r *router) {
	r.implement("B", "GET /api/home", accessReady, a.getHome)
	r.implement("B", "GET /api/models", accessReady, a.listModels)
	r.implement("B", "GET /api/models/{modelID}", accessReady, a.getModel)
}
