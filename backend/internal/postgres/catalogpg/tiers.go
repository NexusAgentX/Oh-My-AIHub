package catalogpg

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

// PriceTiersEqual reports whether two ordered tier lists are identical.
func PriceTiersEqual(left, right []ledger.PriceTier) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		a, b := left[index], right[index]
		if a.Name != b.Name || a.Timezone != b.Timezone ||
			!pointerEqual(a.MinPromptTokens, b.MinPromptTokens) ||
			!pointerEqual(a.MaxPromptTokens, b.MaxPromptTokens) ||
			!pointerEqual(a.StartMinute, b.StartMinute) ||
			!pointerEqual(a.EndMinute, b.EndMinute) ||
			len(a.Weekdays) != len(b.Weekdays) {
			return false
		}
		for weekday := range a.Weekdays {
			if a.Weekdays[weekday] != b.Weekdays[weekday] {
				return false
			}
		}
		if a.InputPrice != b.InputPrice || a.OutputPrice != b.OutputPrice ||
			a.CacheWritePrice != b.CacheWritePrice || a.CacheReadPrice != b.CacheReadPrice {
			return false
		}
	}
	return true
}

func pointerEqual[T comparable](left, right *T) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func tierAuditDetails(tiers []ledger.PriceTier) []map[string]any {
	details := make([]map[string]any, 0, len(tiers))
	for index, tier := range tiers {
		detail := map[string]any{
			"seq":                                index + 1,
			"name":                               tier.Name,
			"timezone":                           tier.Timezone,
			"input_price_nano_per_million":       tier.InputPrice.Nano(),
			"output_price_nano_per_million":      tier.OutputPrice.Nano(),
			"cache_write_price_nano_per_million": tier.CacheWritePrice.Nano(),
			"cache_read_price_nano_per_million":  tier.CacheReadPrice.Nano(),
		}
		if tier.MinPromptTokens != nil {
			detail["min_prompt_tokens"] = *tier.MinPromptTokens
		}
		if tier.MaxPromptTokens != nil {
			detail["max_prompt_tokens"] = *tier.MaxPromptTokens
		}
		if tier.StartMinute != nil {
			detail["start_minute_of_day"] = *tier.StartMinute
		}
		if tier.EndMinute != nil {
			detail["end_minute_of_day"] = *tier.EndMinute
		}
		if len(tier.Weekdays) > 0 {
			detail["weekdays"] = tier.Weekdays
		}
		details = append(details, detail)
	}
	return details
}

func auditDetails(model catalog.Model, tiers []ledger.PriceTier) map[string]any {
	return map[string]any{
		"status":                             model.Status,
		"input_price_nano_per_million":       model.InputPrice.Nano(),
		"output_price_nano_per_million":      model.OutputPrice.Nano(),
		"cache_write_price_nano_per_million": model.CacheWritePrice.Nano(),
		"cache_read_price_nano_per_million":  model.CacheReadPrice.Nano(),
		"context_window":                     model.ContextWindow,
		"price_tier_count":                   len(tiers),
		"price_tiers":                        tierAuditDetails(tiers),
		"version":                            model.Version,
	}
}
