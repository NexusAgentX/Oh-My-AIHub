package gateway

import "net/http"

// anthropicAdapter implements Anthropic Messages (/v1/messages).
type anthropicAdapter struct{ baseAdapter }

func (anthropicAdapter) platformCredentialHeader() string { return "x-api-key" }

func (anthropicAdapter) setUpstreamAuthentication(header http.Header, credential string) {
	header.Set("x-api-key", credential)
}

// defaultRequestHeaders defaults only a missing version: a client-sent value is
// forwarded verbatim, but the upstream requires one.
func (anthropicAdapter) defaultRequestHeaders(header http.Header) {
	if header.Get("anthropic-version") == "" {
		header.Set("anthropic-version", defaultAnthropicVersion)
	}
}

func (anthropicAdapter) eventNameMatches(eventName, dataType string) bool {
	return eventName == dataType
}

func (anthropicAdapter) rewriteModelMetadata(value map[string]any, canonicalModelID string) {
	if _, exists := value["model"]; exists {
		value["model"] = canonicalModelID
	}
	if message, ok := value["message"].(map[string]any); ok {
		if _, exists := message["model"]; exists {
			message["model"] = canonicalModelID
		}
	}
}

func (anthropicAdapter) streamError(value map[string]any) *UpstreamResponseError {
	if stringField(value, "type") == "error" {
		code, message := errorDetailsFromValue(value, nestedObject(value, "error"))
		return &UpstreamResponseError{Code: coalesce(code, "upstream_stream_error"), Message: message}
	}
	return nil
}

func (anthropicAdapter) usageObservation(value map[string]any) (UsageObservation, UsageState) {
	container := value
	if raw, exists := value["usage"]; !exists || raw == nil {
		message, messageOK := value["message"].(map[string]any)
		if !messageOK {
			if rawMessage, exists := value["message"]; exists && rawMessage != nil {
				return UsageObservation{}, UsageInvalid
			}
			return UsageObservation{}, UsageAbsent
		}
		container = message
	}
	usage, ok := container["usage"].(map[string]any)
	if !ok {
		return UsageObservation{}, missingUsageState(container, "usage")
	}
	if !anthropicUsageBillable(usage) {
		return UsageObservation{}, UsageInvalid
	}
	observation := UsageObservation{}
	// Cache buckets are read unconditionally: a non-zero cache count without
	// input_tokens is a shape the formula cannot express.
	cacheWrite, cacheWriteOK := intFieldDefault(usage, "cache_creation_input_tokens", 0)
	cacheRead, cacheReadOK := intFieldDefault(usage, "cache_read_input_tokens", 0)
	if !cacheWriteOK || !cacheReadOK {
		return UsageObservation{}, UsageInvalid
	}
	if _, exists := usage["input_tokens"]; exists {
		input, inputOK := intField(usage, "input_tokens")
		if !inputOK {
			return UsageObservation{}, UsageInvalid
		}
		observation.InputTokens = intPointer(input)
		observation.CacheWriteTokens = intPointer(cacheWrite)
		observation.CacheReadTokens = intPointer(cacheRead)
	} else if cacheWrite != 0 || cacheRead != 0 {
		return UsageObservation{}, UsageInvalid
	}
	if _, exists := usage["output_tokens"]; exists {
		output, outputOK := intField(usage, "output_tokens")
		if !outputOK {
			return UsageObservation{}, UsageInvalid
		}
		observation.OutputTokens = intPointer(output)
	}
	if observation.InputTokens == nil && observation.OutputTokens == nil {
		return UsageObservation{}, UsageAbsent
	}
	return observation, UsageValid
}

func (anthropicAdapter) semanticEvent(value map[string]any) bool {
	eventType, _ := value["type"].(string)
	if eventType == "content_block_delta" {
		delta, _ := value["delta"].(map[string]any)
		return stringField(delta, "text") != "" || stringField(delta, "partial_json") != "" || stringField(delta, "thinking") != "" || stringField(delta, "signature") != ""
	}
	if eventType == "content_block_start" {
		block, _ := value["content_block"].(map[string]any)
		return stringField(block, "type") == "tool_use"
	}
	return false
}

func (anthropicAdapter) terminalEvent(value map[string]any) bool {
	eventType := stringField(value, "type")
	if eventType == "message_stop" {
		return true
	}
	return eventType == "message_delta" && value["usage"] != nil
}

func (anthropicAdapter) finishObserved(value map[string]any) bool {
	return stringField(value, "type") == "message_delta" && stringField(nestedObject(value, "delta"), "stop_reason") != ""
}

func (anthropicAdapter) streamEndEvent(value map[string]any) bool {
	return stringField(value, "type") == "message_stop"
}

func (anthropicAdapter) errorPayload(_ int, code, message, callID string) any {
	errorValue := map[string]any{"type": code, "message": message}
	if callID != "" {
		errorValue["call_id"] = callID
	}
	return map[string]any{"type": "error", "error": errorValue}
}

func (anthropicAdapter) sseErrorFrame(payload []byte) []byte {
	return append(append([]byte("event: error\ndata: "), payload...), []byte("\n\n")...)
}

// acceptsUpstreamErrorBody requires the typed envelope Anthropic SDKs expect.
func (anthropicAdapter) acceptsUpstreamErrorBody(value map[string]any) bool {
	return stringField(value, "type") == "error"
}

func anthropicUsageBillable(usage map[string]any) bool {
	if cacheCreation, exists := usage["cache_creation"]; exists && cacheCreation != nil {
		details, ok := cacheCreation.(map[string]any)
		if !ok {
			return false
		}
		// The 1h cache TTL is billed as a separate upstream product the
		// four-bucket formula cannot price; 5m writes map to cache-write.
		if !zeroOptionalTokenField(details, "ephemeral_1h_input_tokens") {
			return false
		}
		fiveMinute, _ := intFieldDefault(details, "ephemeral_5m_input_tokens", 0)
		cacheWrite, valid := intFieldDefault(usage, "cache_creation_input_tokens", 0)
		if !valid || fiveMinute != cacheWrite {
			return false
		}
	}
	if serverTools, exists := usage["server_tool_use"]; exists && serverTools != nil {
		details, ok := serverTools.(map[string]any)
		if !ok || !allZeroTokenFields(details) {
			return false
		}
	}
	return true
}
