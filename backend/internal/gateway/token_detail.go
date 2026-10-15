package gateway

import (
	"encoding/json"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"strconv"
	"strings"
)

// jsonMember is one member of a JSON object read in place: key is the decoded
// name, value the member's JSON text. Both alias the text that was read.
type jsonMember struct{ key, value []byte }

// jsonObject is the members of a JSON object in the order they were written,
// found without copying or decoding any value. It answers what a
// map[string]json.RawMessage decoded from the same text would: get and has see
// the last member of a name.
type jsonObject []jsonMember

// objectOf reads the members of the JSON object in raw, which must be valid
// JSON. Anything that is not an object (null, a string, nothing at all) has no
// members, as when decoding it into a map. The members are laid out in the
// observer's scratch space, which absorbDetails empties before it starts, so a
// frame costs no allocation once the space has grown to its size.
func (o *Observer) objectOf(raw []byte) jsonObject {
	if at := skipSpace(raw, 0); at >= len(raw) || raw[at] != '{' {
		return nil
	}
	first := len(o.members)
	if _, err := walkObject(raw, 0, func(key []byte, start, end int) {
		o.members = append(o.members, jsonMember{key: key, value: raw[start:end]})
	}); err != nil {
		o.members = o.members[:first]
		return nil
	}
	return o.members[first:len(o.members):len(o.members)]
}

// eachElement calls visit with the JSON text of each element of the array in
// raw, which must be valid JSON; anything that is not an array has none.
func eachElement(raw []byte, visit func(element []byte)) {
	at := skipSpace(raw, 0)
	if at >= len(raw) || raw[at] != '[' {
		return
	}
	at = skipSpace(raw, at+1)
	for at < len(raw) && raw[at] != ']' {
		end, err := skipValue(raw, at)
		if err != nil {
			return
		}
		visit(raw[at:end])
		at = skipSpace(raw, end)
		if at < len(raw) && raw[at] == ',' {
			at = skipSpace(raw, at+1)
		}
	}
}

// get returns the value of the last member with the name, or nil if there is none.
func (o jsonObject) get(name string) []byte {
	for index := len(o) - 1; index >= 0; index-- {
		if string(o[index].key) == name {
			return o[index].value
		}
	}
	return nil
}

func (o jsonObject) has(name string) bool { return o.get(name) != nil }

// count reads a JSON integer: the second result is false for anything else
// (null, a string, a fraction, a number too big).
func count(raw []byte) (int64, bool) {
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
		return 0, false
	}
	value, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// textValue reads a JSON string; anything else reads as "".
func textValue(raw []byte) string {
	if len(raw) == 0 || raw[0] != '"' {
		return "" // absent, null, a number, an object: decoding any of them into a string leaves it empty
	}
	if len(raw) >= 2 && raw[len(raw)-1] == '"' {
		plain := true
		for _, c := range raw[1 : len(raw)-1] {
			if c == '\\' || c < 0x20 || c >= 0x80 {
				plain = false
				break
			}
		}
		if plain {
			return string(raw[1 : len(raw)-1])
		}
	}
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
	if o.detail.Tokens == nil {
		o.detail.Tokens = map[string]int64{}
	}
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
	if len(raw) == 0 {
		return nil, false
	}
	var items []struct {
		Modality   string `json:"modality"`
		TokenCount *int64 `json:"tokenCount"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return nil, false
	}
	result := map[string]int64{}
	seen := map[string]bool{}
	for _, item := range items {
		original := strings.ToLower(item.Modality)
		key := original
		if key == "document" {
			key = "text"
		}
		if seen[original] || item.TokenCount == nil || *item.TokenCount < 0 {
			return nil, false
		}
		if ledger.TokenBucket("input_"+key) < 0 {
			return nil, false
		}
		if *item.TokenCount > int64(^uint64(0)>>1)-result[key] {
			return nil, false
		}
		seen[original] = true
		result[key] += *item.TokenCount
	}
	return result, true
}

// absorbDetails runs for every event, including reasoning/service events without usage.
// keys are the member names scanFrameKeys found in raw: an event with none of
// the members below changes nothing here, and is not decoded.
func (o *Observer) absorbDetails(raw []byte, keys frameKeys) {
	if o.detail.Tokens == nil {
		o.detail.Tokens = map[string]int64{}
	}
	if keys&detailKeys == 0 {
		return
	}
	// The members are read in place. Text that is not valid JSON as a whole has
	// none, as when decoding it into a map: encoding/json would fail on it.
	o.members = o.members[:0]
	var value jsonObject
	if json.Valid(raw) {
		value = o.objectOf(raw)
	}
	if nested := value.get("response"); len(nested) > 0 {
		value = o.objectOf(nested)
	}
	prefix := "openai:"
	if o.format == channel.FormatAnthropic {
		prefix = "anthropic:"
	}
	if o.format == channel.FormatGemini {
		prefix = "gemini:"
	}
	if tier := textValue(value.get("service_tier")); tier != "" {
		o.setServiceTier(prefix + tier)
	}
	if o.format == channel.FormatOpenAIChat && keys&keyReasoning != 0 {
		// A reasoning_content member in the delta (or message) of any choice
		// marks a model that thinks; there is no need to keep what it says.
		eachElement(value.get("choices"), func(choice []byte) {
			choiceMembers := o.objectOf(choice)
			for _, field := range [...]string{"delta", "message"} {
				if raw := o.objectOf(choiceMembers.get(field)).get("reasoning_content"); raw != nil {
					o.qwenObserved = true
					if textValue(raw) != "" {
						o.detail.ThinkingMode = "qwen_thinking"
					}
				}
			}
		})
	}
	if nested := value.get("message"); len(nested) > 0 {
		value = o.objectOf(nested)
	}
	usage := o.objectOf(value.get("usage"))
	if tier := textValue(usage.get("service_tier")); tier != "" {
		o.setServiceTier(prefix + tier)
	}
	switch o.format {
	case channel.FormatAnthropic:
		// Speed is a separate response fact: later tier-only frames must not
		// erase it. An explicit non-fast speed replaces an earlier fast fact.
		if speed := usage.get("speed"); speed != nil {
			o.anthropicSpeed = textValue(speed)
		}
		o.detail.ServiceTier = o.anthropicTier
		if o.anthropicSpeed == "fast" {
			o.detail.ServiceTier = "anthropic:fast"
		}
		if raw := usage.get("iterations"); len(raw) > 0 && string(raw) != "[]" && string(raw) != "null" {
			break
		}
		if usage.has("cache_creation_input_tokens") {
			old := map[string]int64{}
			for key, n := range o.detail.Tokens {
				if strings.HasPrefix(key, "cache_write_") {
					old[key] = n
				}
			}
			o.setBucket("cache_write_", old, o.usage.CacheWriteTokens)
		}
		if raw := usage.get("cache_creation"); raw != nil {
			o.setBucket("cache_write_", nil, o.usage.CacheWriteTokens)
			if o.iterationsSeen {
				o.note("迭代后顶层TTL缺少聚合交集，使用通用缓存写价")
				break
			}

			details := o.objectOf(raw)
			values := map[string]int64{}
			valid := true
			for key, field := range map[string]string{"cache_write_5m": "ephemeral_5m_input_tokens", "cache_write_1h": "ephemeral_1h_input_tokens"} {
				n, ok := count(details.get(field))
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
		inField, outField := "prompt_tokens_details", "completion_tokens_details"
		if o.format == channel.FormatOpenAIResponses {
			inField, outField = "input_tokens_details", "output_tokens_details"
		}
		inputs, outputs := o.objectOf(usage.get(inField)), o.objectOf(usage.get(outField))
		// Missing/null fields retain prior detail; a new detail object replaces it.
		// Total-only updates revalidate the retained counts against the new bucket.
		for _, bucket := range []struct {
			prefix  string
			total   int64
			replace bool
		}{
			{"input_", o.usage.InputTokens, inputs != nil}, {"cache_read_", o.usage.CacheReadTokens, inputs != nil},
			{"output_", o.usage.OutputTokens, outputs != nil}, {"cache_write_", o.usage.CacheWriteTokens, false},
		} {
			old := map[string]int64{}
			if !bucket.replace {
				for key, n := range o.detail.Tokens {
					if strings.HasPrefix(key, bucket.prefix) {
						old[key] = n
					}
				}
			}
			o.setBucket(bucket.prefix, old, bucket.total)
		}

		if ttl := inputs.get("cache_creation_token_details"); ttl != nil {
			d := o.objectOf(ttl)
			values := map[string]int64{}
			valid := true
			for key, field := range map[string]string{"cache_write_5m": "ephemeral_5m_input_tokens", "cache_write_1h": "ephemeral_1h_input_tokens"} {
				if n, ok := count(d.get(field)); ok {
					values[key] = n
				} else {
					valid = false
				}
			}
			o.setBucket("cache_write_", nil, o.usage.CacheWriteTokens)
			if valid {
				o.setBucket("cache_write_", values, o.usage.CacheWriteTokens)
			} else {
				o.note("缓存TTL未细分，使用通用缓存写价")
			}
		}
		inValues, outValues := map[string]int64{}, map[string]int64{}
		for _, mod := range []string{"text", "image", "audio", "video"} {
			if n, ok := count(inputs.get(mod + "_tokens")); ok {
				inValues["input_"+mod] = n
			}
			if n, ok := count(outputs.get(mod + "_tokens")); ok {
				outValues["output_"+mod] = n
			}
		}
		if len(inValues) > 0 {
			if o.usage.CacheReadTokens == 0 && o.usage.CacheWriteTokens == 0 {
				o.setBucket("input_", inValues, o.usage.InputTokens)
			} else {
				cached := o.objectOf(inputs.get("cached_tokens_details"))
				fresh, cache := map[string]int64{}, map[string]int64{}
				remaining := o.usage.CacheReadTokens
				valid := len(cached) > 0 && o.usage.CacheWriteTokens == 0
				for _, mod := range []string{"text", "image", "audio", "video"} {
					n, ok := count(cached.get(mod + "_tokens"))
					if !ok {
						continue
					}
					if n < 0 || n > remaining || n > inValues["input_"+mod] {
						valid = false
						break
					}
					remaining -= n
					cache["cache_read_"+mod] = n
				}
				for key, n := range inValues {
					fresh[key] = n - cache["cache_read_"+strings.TrimPrefix(key, "input_")]
				}
				if valid && remaining == 0 {
					o.setBucket("input_", fresh, o.usage.InputTokens)
					o.setBucket("cache_read_", cache, o.usage.CacheReadTokens)
				} else {
					o.setBucket("input_", nil, o.usage.InputTokens)
					o.setBucket("cache_read_", nil, o.usage.CacheReadTokens)
					o.note("输入模态与缓存交集未知，使用通用输入及缓存价")
				}
			}
		}
		if len(outValues) > 0 {
			o.setBucket("output_", outValues, o.usage.OutputTokens)
		}
	case channel.FormatGemini:
		usage = o.objectOf(value.get("usageMetadata"))
		if len(usage) == 0 {
			return
		}
		o.setBucket("input_", nil, o.usage.InputTokens)
		o.setBucket("cache_read_", nil, o.usage.CacheReadTokens)
		o.setBucket("output_", nil, o.usage.OutputTokens)
		if tier := textValue(usage.get("serviceTier")); tier != "" {
			o.setServiceTier(prefix + tier)
		}
		prompt, pok := modalityCounts(usage.get("promptTokensDetails"))
		cached, cok := modalityCounts(usage.get("cacheTokensDetails"))
		if o.usage.CacheReadTokens == 0 && len(usage.get("cacheTokensDetails")) == 0 {
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
		if outputs, ok := modalityCounts(usage.get("candidatesTokensDetails")); ok {
			values := map[string]int64{}
			for key, n := range outputs {
				values["output_"+key] = n
			}
			candidates, _ := count(usage.get("candidatesTokenCount"))
			o.setBucket("output_", values, candidates)
		}
		if n, ok := count(usage.get("toolUsePromptTokenCount")); ok && n > 0 {
			o.detail.ToolPromptTokens = n
			o.note("工具提示token已观察，独立计价语义未覆盖")
		}
	}
}

// Keep the upstream tier separate from the effective fast pricing condition.
func (o *Observer) setServiceTier(tier string) {
	if o.format == channel.FormatAnthropic {
		o.anthropicTier = tier
	} else {
		o.detail.ServiceTier = tier
	}
}
