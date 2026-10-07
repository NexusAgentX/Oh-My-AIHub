package gateway

// chatAdapter implements OpenAI Chat Completions (/v1/chat/completions).
type chatAdapter struct{ baseAdapter }

// expectedChoices tracks the requested choice count (`n`) so the stream can
// size its terminal wind-down buffer before billing validation.
func (chatAdapter) expectedChoices(request map[string]any) (int, bool) {
	n, valid := intFieldDefault(request, "n", 1)
	if !valid || n == 0 || n > 128 {
		return 0, false
	}
	return int(n), true
}

// rewriteRequest forces the usage frame on streams; billing needs it.
func (chatAdapter) rewriteRequest(request map[string]any, stream bool) {
	if !stream {
		return
	}
	options, _ := request["stream_options"].(map[string]any)
	if options == nil {
		options = make(map[string]any)
	}
	options["include_usage"] = true
	request["stream_options"] = options
}

// maxTerminalFrames leaves room for one finish frame per choice plus the usage
// frame and [DONE].
func (chatAdapter) maxTerminalFrames(expectedChoices int) int {
	if expectedChoices+2 > MaxTerminalFrames {
		return expectedChoices + 2
	}
	return MaxTerminalFrames
}

func (chatAdapter) doneSentinelEndsStream() bool { return true }

func (chatAdapter) rewriteModelMetadata(value map[string]any, canonicalModelID string) {
	if _, exists := value["model"]; exists {
		value["model"] = canonicalModelID
	}
}

func (chatAdapter) usageObservation(value map[string]any) (UsageObservation, UsageState) {
	usage, ok := value["usage"].(map[string]any)
	if !ok {
		return UsageObservation{}, missingUsageState(value, "usage")
	}
	return openAIUsageState(usage, "prompt_tokens", "completion_tokens", "prompt_tokens_details")
}

func (chatAdapter) semanticEvent(value map[string]any) bool {
	choices, _ := value["choices"].([]any)
	for _, raw := range choices {
		choice, _ := raw.(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		if content, _ := delta["content"].(string); content != "" {
			return true
		}
		if refusal, _ := delta["refusal"].(string); refusal != "" {
			return true
		}
		if calls, ok := delta["tool_calls"].([]any); ok && len(calls) > 0 {
			return true
		}
		if call, ok := delta["function_call"].(map[string]any); ok && len(call) > 0 {
			return true
		}
	}
	return false
}

func (chatAdapter) terminalEvent(value map[string]any) bool {
	_, hasUsage := value["usage"]
	choices, _ := value["choices"].([]any)
	// A finish_reason closes only that choice. The stream itself enters its
	// bounded terminal wind-down at the usage frame and ends at [DONE].
	return hasUsage && len(choices) == 0
}

func (chatAdapter) finishObserved(value map[string]any) bool {
	choices, _ := value["choices"].([]any)
	for _, raw := range choices {
		choice, _ := raw.(map[string]any)
		if stringField(choice, "finish_reason") != "" {
			return true
		}
	}
	return false
}
