package api

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// priceTierResponse serializes one tier. The four price strings are whatever
// the caller projected: catalog responses pass benchmark prices, market and
// pool responses pass multiplier-applied prices.
func priceTierResponse(tier ledger.PriceTier) map[string]any {
	var weekdays any
	if len(tier.Weekdays) > 0 {
		weekdays = tier.Weekdays
	}
	var minPrompt, maxPrompt any
	if tier.MinPromptTokens != nil {
		minPrompt = *tier.MinPromptTokens
	}
	if tier.MaxPromptTokens != nil {
		maxPrompt = *tier.MaxPromptTokens
	}
	var startMinute, endMinute any
	if tier.StartMinute != nil {
		startMinute = *tier.StartMinute
	}
	if tier.EndMinute != nil {
		endMinute = *tier.EndMinute
	}
	return map[string]any{
		"name": tier.Name, "timezone": tier.Timezone,
		"min_prompt_tokens": minPrompt, "max_prompt_tokens": maxPrompt,
		"weekdays": weekdays, "start_minute_of_day": startMinute, "end_minute_of_day": endMinute,
		"input_price":        tier.InputPrice.String(),
		"output_price":       tier.OutputPrice.String(),
		"cache_write_price":  tier.CacheWritePrice.String(),
		"cache_read_price":   tier.CacheReadPrice.String(),
		"price_unit":         "points_per_million_tokens",
	}
}

// effectivePriceTierResponses projects benchmark tiers through the channel
// multiplier so consumers see the same prices a call would settle at.
func effectivePriceTierResponses(multiplier money.Amount, tiers []ledger.PriceTier) []map[string]any {
	result := make([]map[string]any, 0, len(tiers))
	effective, err := channel.EffectivePriceTiers(multiplier, tiers)
	if err != nil {
		// A tier price that cannot be represented is surfaced by the
		// eligibility machinery; displaying no tiers here keeps the response
		// internally consistent.
		return []map[string]any{}
	}
	for _, tier := range effective {
		result = append(result, priceTierResponse(tier))
	}
	return result
}

func benchmarkPriceTierResponses(tiers []ledger.PriceTier) []map[string]any {
	result := make([]map[string]any, 0, len(tiers))
	for _, tier := range tiers {
		result = append(result, priceTierResponse(tier))
	}
	return result
}
