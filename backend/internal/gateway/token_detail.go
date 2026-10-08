package gateway

import (
	"encoding/json"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"strings"
)

func object(raw json.RawMessage) map[string]json.RawMessage {
	var value map[string]json.RawMessage
	_ = json.Unmarshal(raw, &value)
	return value
}
func count(raw json.RawMessage) (int64, bool) {
	var value int64
	err := json.Unmarshal(raw, &value)
	return value, len(raw) > 0 && string(raw) != "null" && err == nil
}
func textValue(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func (o *Observer) note(note string) {
	for _, s := range o.detail.Notes {
		if s == note {
			return
		}
	}
	o.detail.Notes = append(o.detail.Notes, note)
}
func (o *Observer) setBucket(prefix string, values map[string]int64, total int64) {
	for key := range o.detail.Tokens {
		if strings.HasPrefix(key, prefix) {
			delete(o.detail.Tokens, key)
		}
	}
	remaining := total
	for key, value := range values {
		if ledger.TokenBucket(key) < 0 || value < 0 || value > remaining {
			o.note(prefix + "细分无效，使用通用价格")
			return
		}
		remaining -= value
	}
	for key, value := range values {
		o.detail.Tokens[key] = value
	}
}
func modalityCounts(raw json.RawMessage) (map[string]int64, bool) {
	var items []struct {
		Modality   string `json:"modality"`
		TokenCount *int64 `json:"tokenCount"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return nil, false
	}
	result := map[string]int64{}
	for _, item := range items {
		key := strings.ToLower(item.Modality)
		if _, ok := result[key]; ok || item.TokenCount == nil || *item.TokenCount < 0 {
			return nil, false
		}
		if ledger.TokenBucket("input_"+key) < 0 {
			return nil, false
		}
		result[key] = *item.TokenCount
	}
	return result, true
}

// absorbDetails runs for every event, including reasoning/service events without usage.
func (o *Observer) absorbDetails(raw []byte) {
	if o.detail.Tokens == nil {
		o.detail.Tokens = map[string]int64{}
	}
	value := object(raw)
	if nested := value["response"]; len(nested) > 0 {
		value = object(nested)
	}
	prefix := "openai:"
	if o.format == channel.FormatAnthropic {
		prefix = "anthropic:"
	}
	if o.format == channel.FormatGemini {
		prefix = "gemini:"
	}
	if tier := textValue(value["service_tier"]); tier != "" {
		o.detail.ServiceTier = prefix + tier
	}
	if o.format == channel.FormatOpenAIChat {
		var choices []map[string]json.RawMessage
		_ = json.Unmarshal(value["choices"], &choices)
		for _, choice := range choices {
			for _, field := range []string{"delta", "message"} {
				part := object(choice[field])
				if raw, ok := part["reasoning_content"]; ok {
					o.qwenObserved = true
					if textValue(raw) != "" {
						o.detail.ThinkingMode = "qwen_thinking"
					}
				}
			}
		}
	}
	if nested := value["message"]; len(nested) > 0 {
		value = object(nested)
	}
	usage := object(value["usage"])
	if tier := textValue(usage["service_tier"]); tier != "" {
		o.detail.ServiceTier = prefix + tier
	}
	switch o.format {
	case channel.FormatAnthropic:
		if _, changed := usage["cache_creation_input_tokens"]; changed {
			old := map[string]int64{}
			for key, n := range o.detail.Tokens {
				if strings.HasPrefix(key, "cache_write_") {
					old[key] = n
				}
			}
			o.setBucket("cache_write_", old, o.usage.CacheWriteTokens)
		}
		if raw, ok := usage["cache_creation"]; ok {
			o.setBucket("cache_write_", nil, o.usage.CacheWriteTokens)
			details := object(raw)
			values := map[string]int64{}
			valid := true
			for key, field := range map[string]string{"cache_write_5m": "ephemeral_5m_input_tokens", "cache_write_1h": "ephemeral_1h_input_tokens"} {
				n, ok := count(details[field])
				if ok {
					values[key] = n
				} else {
					valid = false
				}
			}
			if valid {
				o.setBucket("cache_write_", values, o.usage.CacheWriteTokens)
			} else {
				o.note("缓存TTL未细分，使用通用缓存写价")
			}
		}
	case channel.FormatOpenAIChat, channel.FormatOpenAIResponses:
		if len(usage) == 0 {
			return
		}
		o.setBucket("input_", nil, o.usage.InputTokens)
		o.setBucket("output_", nil, o.usage.OutputTokens)
		inField, outField := "prompt_tokens_details", "completion_tokens_details"
		if o.format == channel.FormatOpenAIResponses {
			inField, outField = "input_tokens_details", "output_tokens_details"
		}
		inputs, outputs := object(usage[inField]), object(usage[outField])
		inValues, outValues := map[string]int64{}, map[string]int64{}
		for _, mod := range []string{"text", "image", "audio", "video"} {
			if n, ok := count(inputs[mod+"_tokens"]); ok {
				inValues["input_"+mod] = n
			}
			if n, ok := count(outputs[mod+"_tokens"]); ok {
				outValues["output_"+mod] = n
			}
		}
		if len(inValues) > 0 {
			if o.usage.CacheReadTokens == 0 && o.usage.CacheWriteTokens == 0 {
				o.setBucket("input_", inValues, o.usage.InputTokens)
			} else {
				o.setBucket("input_", nil, o.usage.InputTokens)
				o.note("输入模态与缓存交集未知，使用通用输入及缓存价")
			}
		}
		if len(outValues) > 0 {
			o.setBucket("output_", outValues, o.usage.OutputTokens)
		}
	case channel.FormatGemini:
		usage = object(value["usageMetadata"])
		if len(usage) == 0 {
			return
		}
		o.setBucket("input_", nil, o.usage.InputTokens)
		o.setBucket("cache_read_", nil, o.usage.CacheReadTokens)
		o.setBucket("output_", nil, o.usage.OutputTokens)
		if tier := textValue(usage["serviceTier"]); tier != "" {
			o.detail.ServiceTier = prefix + tier
		}
		prompt, pok := modalityCounts(usage["promptTokensDetails"])
		cached, cok := modalityCounts(usage["cacheTokensDetails"])
		if o.usage.CacheReadTokens == 0 && len(usage["cacheTokensDetails"]) == 0 {
			cached = map[string]int64{}
			cok = true
		}
		cacheTotal := int64(0)
		for _, n := range cached {
			if n > o.usage.CacheReadTokens-cacheTotal {
				cok = false
				break
			}
			cacheTotal += n
		}
		if cok && cacheTotal != o.usage.CacheReadTokens {
			cok = false
		}
		if pok && cok {
			fresh, cache := map[string]int64{}, map[string]int64{}
			valid := true
			for key, n := range cached {
				if n > prompt[key] {
					valid = false
				}
				cache["cache_read_"+key] = n
			}
			for key, n := range prompt {
				fresh["input_"+key] = n - cached[key]
			}
			if valid {
				o.setBucket("input_", fresh, o.usage.InputTokens)
				o.setBucket("cache_read_", cache, o.usage.CacheReadTokens)
			} else {
				o.note("模态缓存交集无效，使用通用价格")
			}
		} else if pok {
			o.note("缓存模态未细分，使用通用输入及缓存价")
		}
		if outputs, ok := modalityCounts(usage["candidatesTokensDetails"]); ok {
			values := map[string]int64{}
			for key, n := range outputs {
				values["output_"+key] = n
			}
			candidates, _ := count(usage["candidatesTokenCount"])
			o.setBucket("output_", values, candidates)
		}
		if n, ok := count(usage["toolUsePromptTokenCount"]); ok && n > 0 {
			o.detail.ToolPromptTokens = n
			o.note("工具提示token已观察，独立计价语义未覆盖")
		}
	}
}
