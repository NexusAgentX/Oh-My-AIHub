package gateway

// responsesAdapter implements OpenAI Responses (/v1/responses).
type responsesAdapter struct{ baseAdapter }

func (responsesAdapter) eventNameMatches(eventName, dataType string) bool {
	return eventName == dataType
}

func (responsesAdapter) rewriteModelMetadata(value map[string]any, canonicalModelID string) {
	if _, exists := value["model"]; exists {
		value["model"] = canonicalModelID
	}
	if response, ok := value["response"].(map[string]any); ok {
		if _, exists := response["model"]; exists {
			response["model"] = canonicalModelID
		}
	}
}

func (responsesAdapter) usageObservation(value map[string]any) (UsageObservation, UsageState) {
	container := value
	if raw, exists := value["usage"]; !exists || raw == nil {
		response, responseOK := value["response"].(map[string]any)
		if !responseOK {
			if rawResponse, exists := value["response"]; exists && rawResponse != nil {
				return UsageObservation{}, UsageInvalid
			}
			return UsageObservation{}, UsageAbsent
		}
		container = response
	}
	usage, ok := container["usage"].(map[string]any)
	if !ok {
		return UsageObservation{}, missingUsageState(container, "usage")
	}
	return openAIUsageState(usage, "input_tokens", "output_tokens", "input_tokens_details")
}

// envelopeError: `incomplete` and `cancelled` are valid terminal states that
// still carry usage; only `failed` (or an explicit error object) is a failure.
func (responsesAdapter) envelopeError(value map[string]any) *UpstreamResponseError {
	if status := stringField(value, "status"); status == "failed" {
		code, message := errorDetailsFromValue(value, nestedObject(value, "error"))
		return &UpstreamResponseError{Code: coalesce(code, "upstream_"+status), Message: message}
	}
	return nil
}

func (a responsesAdapter) streamError(value map[string]any) *UpstreamResponseError {
	eventType := stringField(value, "type")
	// `response.incomplete` is a terminal event, not a failure: it carries the
	// truncated output and the usage that produced it.
	if eventType == "error" || eventType == "response.failed" {
		errorValue := nestedObject(value, "error")
		if response := nestedObject(value, "response"); len(errorValue) == 0 {
			errorValue = nestedObject(response, "error")
		}
		code, message := errorDetailsFromValue(value, errorValue)
		return &UpstreamResponseError{Code: coalesce(code, "upstream_stream_error"), Message: message}
	}
	// An otherwise normal event can still carry the upstream's failure
	// envelope inside response; only an explicit failure counts.
	if response := nestedObject(value, "response"); len(response) > 0 {
		return adapterEnvelopeError(a, response)
	}
	return nil
}

func (responsesAdapter) semanticEvent(value map[string]any) bool {
	eventType, _ := value["type"].(string)
	switch eventType {
	case "response.output_text.delta", "response.refusal.delta", "response.function_call_arguments.delta",
		"response.custom_tool_call_input.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if stringField(value, "delta") != "" {
			return true
		}
	}
	if eventType == "response.output_item.added" {
		item, _ := value["item"].(map[string]any)
		switch stringField(item, "type") {
		case "function_call", "custom_tool_call":
			return true
		}
	}
	if eventType == "response.completed" {
		response := nestedObject(value, "response")
		return responseOutputIsSemantic(response["output"])
	}
	return false
}

func (responsesAdapter) terminalEvent(value map[string]any) bool {
	eventType := stringField(value, "type")
	return eventType == "response.completed" || eventType == "response.incomplete"
}

// finishObserved: the event type itself is the end-of-stream signal; the inner
// response status is not required to be present.
func (a responsesAdapter) finishObserved(value map[string]any) bool { return a.terminalEvent(value) }

func (a responsesAdapter) streamEndEvent(value map[string]any) bool { return a.finishObserved(value) }

func responseOutputIsSemantic(raw any) bool {
	output, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, rawItem := range output {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		switch stringField(item, "type") {
		case "function_call":
			if stringField(item, "name") != "" || stringField(item, "arguments") != "" {
				return true
			}
		case "custom_tool_call":
			if stringField(item, "name") != "" || stringField(item, "input") != "" {
				return true
			}
		case "message", "reasoning":
			content, _ := item["content"].([]any)
			for _, rawContent := range content {
				block, _ := rawContent.(map[string]any)
				for _, field := range []string{"text", "refusal", "summary"} {
					if stringField(block, field) != "" {
						return true
					}
				}
			}
		}
	}
	return false
}
