package ledger

import "strings"

// UsageDetail contains only billing facts, never prompt or reasoning content.
// Tokens are disjoint subsets of the four summary buckets, not extra usage.
type UsageDetail struct {
	RequestedServiceTier string           `json:"requested_service_tier,omitempty"`
	ToolPromptTokens     int64            `json:"tool_prompt_tokens,omitempty"`
	Tokens               map[string]int64 `json:"tokens,omitempty"`
	ServiceTier          string           `json:"service_tier,omitempty"`
	ThinkingMode         string           `json:"thinking_mode,omitempty"`
	Notes                []string         `json:"notes,omitempty"`
}

func TokenBucket(key string) int {
	if key == "cache_write_5m" || key == "cache_write_1h" {
		return 2
	}
	for i, prefix := range []string{"input_", "output_", "", "cache_read_"} {
		if prefix == "" || !strings.HasPrefix(key, prefix) {
			continue
		}
		switch strings.TrimPrefix(key, prefix) {
		case "text", "image", "audio", "video":
			return i
		}
	}
	return -1
}
