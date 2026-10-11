package gateway

// This file is a frozen copy of the side-channel reader as it was before the
// frame pre-scan (G8.5): decode every event into maps and structs, always. It
// is the oracle of TestObserverMatchesTheOriginalOnAnyEvent and
// FuzzObserverMatchesTheOriginal, which say that skipping or lighter decoding
// changes no state of the reader. Do not "fix" it: when the reader's rules
// change on purpose, change this copy in the same commit.

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

// legacyObserver scans a response as it streams past, without ever altering it and
// without validating its shape: anything it cannot parse is ignored.
type legacyObserver struct {
	format channel.Format
	stream bool
	sse    bool

	line      []byte
	skipLine  bool
	buffer    []byte
	overflow  bool
	last      time.Time
	intervals []time.Duration
	frames    int

	detail         ledger.UsageDetail
	qwenObserved   bool
	anthropicSpeed string
	anthropicTier  string
	usage          ledger.Usage
	found          bool
	responseID     string
	// anthropic reports its fields in two events; track which were seen.
	anthropic legacyAnthropicUsage
}

type legacyAnthropicUsage struct{ input, output, cacheWrite, cacheRead int64 }

// legacyNewObserver starts a reader for one response of the given format.
// stream reports whether the client asked for a streamed response.
func legacyNewObserver(format channel.Format, stream bool, contentType string) *legacyObserver {
	return &legacyObserver{format: format, stream: stream, sse: strings.Contains(strings.ToLower(contentType), "text/event-stream")}
}

// Write feeds one chunk that arrived at the given time.
func (o *legacyObserver) Write(chunk []byte, at time.Time) {
	if o.sse {
		o.writeSSE(chunk, at)
		return
	}
	if o.stream {
		o.frame(at)
	}
	if o.overflow {
		return
	}
	if len(o.buffer)+len(chunk) > maxJSONBuffer {
		o.overflow, o.buffer = true, nil
		return
	}
	o.buffer = append(o.buffer, chunk...)
}

func (o *legacyObserver) frame(at time.Time) {
	o.frames++
	if !o.last.IsZero() && len(o.intervals) < maxIntervals {
		o.intervals = append(o.intervals, at.Sub(o.last))
	}
	o.last = at
}

func (o *legacyObserver) writeSSE(chunk []byte, at time.Time) {
	for len(chunk) > 0 {
		newline := bytes.IndexByte(chunk, '\n')
		if newline < 0 {
			if !o.skipLine {
				if len(o.line)+len(chunk) > maxLineBuffer {
					o.skipLine, o.line = true, nil
				} else {
					o.line = append(o.line, chunk...)
				}
			}
			return
		}
		if !o.skipLine {
			if len(o.line)+newline <= maxLineBuffer {
				o.line = append(o.line, chunk[:newline]...)
				o.handleLine(o.line, at)
			}
		}
		o.line, o.skipLine = o.line[:0], false
		chunk = chunk[newline+1:]
	}
}

func (o *legacyObserver) handleLine(line []byte, at time.Time) {
	line = bytes.TrimSuffix(line, []byte("\r"))
	payload, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return
	}
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || payload[0] != '{' {
		return
	}
	o.frame(at)
	o.absorb(payload)
}

// Finish parses whatever was buffered (non-event-stream bodies) and returns
// the observation.
func (o *legacyObserver) Finish() Observation {
	if o.sse && len(o.line) > 0 && !o.skipLine {
		o.handleLine(o.line, o.last)
		o.line = nil
	}
	if !o.sse && !o.overflow && len(o.buffer) > 0 {
		body := bytes.TrimSpace(o.buffer)
		switch {
		case len(body) > 0 && body[0] == '{':
			o.absorb(body)
		case len(body) > 0 && body[0] == '[':
			var elements []json.RawMessage
			if json.Unmarshal(body, &elements) == nil {
				for _, element := range elements {
					o.absorb(element)
				}
			}
		}
	}
	if o.qwenObserved && o.detail.ThinkingMode == "" {
		o.detail.ThinkingMode = "qwen_non_thinking"
	}
	if len(o.detail.Tokens) > 0 || o.detail.RequestedServiceTier != "" || o.detail.ServiceTier != "" || o.detail.ThinkingMode != "" || len(o.detail.Notes) > 0 {
		o.usage.Detail = &o.detail
	}
	observation := Observation{Usage: o.usage, Found: o.found, ResponseID: o.responseID, Frames: o.frames}
	if len(o.intervals) > 0 {
		sorted := append([]time.Duration(nil), o.intervals...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		p50 := int(percentile(sorted, 0.50).Milliseconds())
		p95 := int(percentile(sorted, 0.95).Milliseconds())
		observation.IntervalP50MS, observation.IntervalP95MS = &p50, &p95
	}
	return observation
}

// absorb reads usage (and the response ID) out of one JSON legacyObject.
func (o *legacyObserver) absorb(raw []byte) {
	defer o.absorbDetails(raw)
	switch o.format {
	case channel.FormatOpenAIChat:
		o.absorbChat(raw)
	case channel.FormatOpenAIResponses:
		o.absorbResponses(raw)
	case channel.FormatAnthropic:
		o.absorbAnthropic(raw)
	case channel.FormatGemini:
		o.absorbGemini(raw)
	}
}

type legacyTokenDetails struct {
	CachedTokens     int64 `json:"cached_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

func (o *legacyObserver) absorbChat(raw []byte) {
	if !bytes.Contains(raw, []byte(`"usage"`)) {
		return
	}
	var value struct {
		Usage *struct {
			PromptTokens        int64               `json:"prompt_tokens"`
			CompletionTokens    int64               `json:"completion_tokens"`
			PromptTokensDetails *legacyTokenDetails `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Usage == nil {
		return
	}
	cached, written := int64(0), int64(0)
	if value.Usage.PromptTokensDetails != nil {
		cached = value.Usage.PromptTokensDetails.CachedTokens
		written = value.Usage.PromptTokensDetails.CacheWriteTokens
	}
	o.usage = ledger.Usage{
		InputTokens: clamp(value.Usage.PromptTokens - cached - written), OutputTokens: clamp(value.Usage.CompletionTokens), CacheReadTokens: clamp(cached), CacheWriteTokens: clamp(written),
	}
	o.found = true
}

type legacyResponsesUsage struct {
	InputTokens        int64               `json:"input_tokens"`
	OutputTokens       int64               `json:"output_tokens"`
	InputTokensDetails *legacyTokenDetails `json:"input_tokens_details"`
}

func (o *legacyObserver) setResponsesUsage(usage *legacyResponsesUsage) {
	if usage == nil {
		return
	}
	cached, written := int64(0), int64(0)
	if usage.InputTokensDetails != nil {
		cached = usage.InputTokensDetails.CachedTokens
		written = usage.InputTokensDetails.CacheWriteTokens
	}
	o.usage = ledger.Usage{InputTokens: clamp(usage.InputTokens - cached - written), OutputTokens: clamp(usage.OutputTokens), CacheReadTokens: clamp(cached), CacheWriteTokens: clamp(written)}
	o.found = true
}

func (o *legacyObserver) absorbResponses(raw []byte) {
	if !bytes.Contains(raw, []byte(`"usage"`)) && !bytes.Contains(raw, []byte(`"id"`)) {
		return
	}
	var value struct {
		Type     string                `json:"type"`
		ID       string                `json:"id"`
		Usage    *legacyResponsesUsage `json:"usage"`
		Response *struct {
			ID    string                `json:"id"`
			Usage *legacyResponsesUsage `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	if value.Response != nil {
		if value.Response.ID != "" {
			o.responseID = value.Response.ID
		}
		o.setResponsesUsage(value.Response.Usage)
	}
	if value.Type == "" {
		if value.ID != "" {
			o.responseID = value.ID
		}
		o.setResponsesUsage(value.Usage)
	}
}

type legacyAnthropicFields struct {
	InputTokens     *int64 `json:"input_tokens"`
	OutputTokens    *int64 `json:"output_tokens"`
	CacheCreation   *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputs *int64 `json:"cache_read_input_tokens"`
}

func (o *legacyObserver) applyAnthropic(fields *legacyAnthropicFields) {
	if fields == nil {
		return
	}
	if fields.InputTokens != nil {
		o.anthropic.input = clamp(*fields.InputTokens)
	}
	if fields.OutputTokens != nil {
		o.anthropic.output = clamp(*fields.OutputTokens)
	}
	if fields.CacheCreation != nil {
		o.anthropic.cacheWrite = clamp(*fields.CacheCreation)
	}
	if fields.CacheReadInputs != nil {
		o.anthropic.cacheRead = clamp(*fields.CacheReadInputs)
	}
	o.usage = ledger.Usage{
		InputTokens: o.anthropic.input, OutputTokens: o.anthropic.output,
		CacheWriteTokens: o.anthropic.cacheWrite, CacheReadTokens: o.anthropic.cacheRead,
	}
	o.found = true
}

func (o *legacyObserver) absorbAnthropic(raw []byte) {
	if !bytes.Contains(raw, []byte(`"usage"`)) {
		return
	}
	var value struct {
		Usage   *legacyAnthropicFields `json:"usage"`
		Message *struct {
			Usage *legacyAnthropicFields `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	if value.Message != nil {
		o.applyAnthropic(value.Message.Usage)
	}
	o.applyAnthropic(value.Usage)
}

func (o *legacyObserver) absorbGemini(raw []byte) {
	if !bytes.Contains(raw, []byte(`"usageMetadata"`)) {
		return
	}
	var value struct {
		UsageMetadata *struct {
			PromptTokenCount        int64 `json:"promptTokenCount"`
			CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
			ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
			CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}
	if json.Unmarshal(raw, &value) != nil || value.UsageMetadata == nil {
		return
	}
	usage := value.UsageMetadata
	o.usage = ledger.Usage{
		InputTokens:     clamp(usage.PromptTokenCount - usage.CachedContentTokenCount),
		OutputTokens:    clamp(usage.CandidatesTokenCount + usage.ThoughtsTokenCount),
		CacheReadTokens: clamp(usage.CachedContentTokenCount),
	}
	o.found = true
}

func legacyObject(raw json.RawMessage) map[string]json.RawMessage {
	var value map[string]json.RawMessage
	_ = json.Unmarshal(raw, &value)
	return value
}
func legacyCount(raw json.RawMessage) (int64, bool) {
	var value int64
	err := json.Unmarshal(raw, &value)
	return value, len(raw) > 0 && string(raw) != "null" && err == nil
}
func legacyTextValue(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func (o *legacyObserver) note(note string) {
	for _, s := range o.detail.Notes {
		if s == note {
			return
		}
	}
	o.detail.Notes = append(o.detail.Notes, note)
}
func (o *legacyObserver) setBucket(prefix string, values map[string]int64, total int64) {
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
func legacyModalityCounts(raw json.RawMessage) (map[string]int64, bool) {
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
func (o *legacyObserver) absorbDetails(raw []byte) {
	if o.detail.Tokens == nil {
		o.detail.Tokens = map[string]int64{}
	}
	value := legacyObject(raw)
	if nested := value["response"]; len(nested) > 0 {
		value = legacyObject(nested)
	}
	prefix := "openai:"
	if o.format == channel.FormatAnthropic {
		prefix = "anthropic:"
	}
	if o.format == channel.FormatGemini {
		prefix = "gemini:"
	}
	if tier := legacyTextValue(value["service_tier"]); tier != "" {
		o.setServiceTier(prefix + tier)
	}
	if o.format == channel.FormatOpenAIChat {
		var choices []map[string]json.RawMessage
		_ = json.Unmarshal(value["choices"], &choices)
		for _, choice := range choices {
			for _, field := range []string{"delta", "message"} {
				part := legacyObject(choice[field])
				if raw, ok := part["reasoning_content"]; ok {
					o.qwenObserved = true
					if legacyTextValue(raw) != "" {
						o.detail.ThinkingMode = "qwen_thinking"
					}
				}
			}
		}
	}
	if nested := value["message"]; len(nested) > 0 {
		value = legacyObject(nested)
	}
	usage := legacyObject(value["usage"])
	if tier := legacyTextValue(usage["service_tier"]); tier != "" {
		o.setServiceTier(prefix + tier)
	}
	switch o.format {
	case channel.FormatAnthropic:
		// Speed is a separate response fact: later tier-only frames must not
		// erase it. An explicit non-fast speed replaces an earlier fast fact.
		if speed, present := usage["speed"]; present {
			o.anthropicSpeed = legacyTextValue(speed)
		}
		o.detail.ServiceTier = o.anthropicTier
		if o.anthropicSpeed == "fast" {
			o.detail.ServiceTier = "anthropic:fast"
		}
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
			details := legacyObject(raw)
			values := map[string]int64{}
			valid := true
			for key, field := range map[string]string{"cache_write_5m": "ephemeral_5m_input_tokens", "cache_write_1h": "ephemeral_1h_input_tokens"} {
				n, ok := legacyCount(details[field])
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
		inputs, outputs := legacyObject(usage[inField]), legacyObject(usage[outField])
		inValues, outValues := map[string]int64{}, map[string]int64{}
		for _, mod := range []string{"text", "image", "audio", "video"} {
			if n, ok := legacyCount(inputs[mod+"_tokens"]); ok {
				inValues["input_"+mod] = n
			}
			if n, ok := legacyCount(outputs[mod+"_tokens"]); ok {
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
		usage = legacyObject(value["usageMetadata"])
		if len(usage) == 0 {
			return
		}
		o.setBucket("input_", nil, o.usage.InputTokens)
		o.setBucket("cache_read_", nil, o.usage.CacheReadTokens)
		o.setBucket("output_", nil, o.usage.OutputTokens)
		if tier := legacyTextValue(usage["serviceTier"]); tier != "" {
			o.setServiceTier(prefix + tier)
		}
		prompt, pok := legacyModalityCounts(usage["promptTokensDetails"])
		cached, cok := legacyModalityCounts(usage["cacheTokensDetails"])
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
		if outputs, ok := legacyModalityCounts(usage["candidatesTokensDetails"]); ok {
			values := map[string]int64{}
			for key, n := range outputs {
				values["output_"+key] = n
			}
			candidates, _ := legacyCount(usage["candidatesTokenCount"])
			o.setBucket("output_", values, candidates)
		}
		if n, ok := legacyCount(usage["toolUsePromptTokenCount"]); ok && n > 0 {
			o.detail.ToolPromptTokens = n
			o.note("工具提示token已观察，独立计价语义未覆盖")
		}
	}
}

// Keep the upstream tier separate from the effective fast pricing condition.
func (o *legacyObserver) setServiceTier(tier string) {
	if o.format == channel.FormatAnthropic {
		o.anthropicTier = tier
	} else {
		o.detail.ServiceTier = tier
	}
}
