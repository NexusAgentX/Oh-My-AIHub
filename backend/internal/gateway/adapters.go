package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

type ParsedRequest struct {
	CanonicalModelID string
	Stream           bool
	ExpectedChoices  int
}

type UsageObservation struct {
	InputTokens      *int64
	OutputTokens     *int64
	CacheWriteTokens *int64
	CacheReadTokens  *int64
	Conflict         bool
}

func (o *UsageObservation) Merge(next UsageObservation) {
	o.Conflict = o.Conflict || next.Conflict || mergeUsageValue(&o.InputTokens, next.InputTokens) ||
		mergeCumulativeUsageValue(&o.OutputTokens, next.OutputTokens) || mergeUsageValue(&o.CacheWriteTokens, next.CacheWriteTokens) ||
		mergeUsageValue(&o.CacheReadTokens, next.CacheReadTokens)
}

func mergeCumulativeUsageValue(current **int64, next *int64) bool {
	if next == nil {
		return false
	}
	if *current != nil && *next < **current {
		return true
	}
	value := *next
	*current = &value
	return false
}

func (o UsageObservation) Complete() (*ledger.UsageV1, bool) {
	if o.Conflict || o.InputTokens == nil || o.OutputTokens == nil || o.CacheWriteTokens == nil || o.CacheReadTokens == nil {
		return nil, false
	}
	usage := &ledger.UsageV1{
		InputTokens: *o.InputTokens, OutputTokens: *o.OutputTokens,
		CacheWriteTokens: *o.CacheWriteTokens, CacheReadTokens: *o.CacheReadTokens,
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheWriteTokens < 0 || usage.CacheReadTokens < 0 {
		return nil, false
	}
	return usage, true
}

func mergeUsageValue(current **int64, next *int64) bool {
	if next == nil {
		return false
	}
	if *current != nil && **current != *next {
		return true
	}
	value := *next
	*current = &value
	return false
}

type SSEAnalysis struct {
	Frame                 []byte
	Semantic              bool
	Terminal              bool
	StreamEnd             bool
	FinishObserved        bool
	AfterTerminalAllowed  bool
	ChoiceIndexes         []int
	FinishedChoiceIndexes []int
	ErrorCode             string
	ErrorMessage          string
	CredentialFragments   []SSECredentialFragment
	Observation           UsageObservation
}

type SSECredentialFragment struct {
	StreamKey string
	Text      string
}

type UpstreamResponseError struct {
	Code    string
	Message string
}

func (e *UpstreamResponseError) Error() string {
	return e.Code + ": " + e.Message
}

func ParseRequest(protocol channel.Protocol, body []byte) (ParsedRequest, error) {
	if !validProtocol(protocol) || len(body) == 0 {
		return ParsedRequest{}, ErrInvalidInput
	}
	value, err := decodeJSONObject(body)
	if err != nil {
		return ParsedRequest{}, err
	}
	model, ok := value["model"].(string)
	if !ok || !validCanonicalModelID(model) {
		return ParsedRequest{}, ErrInvalidInput
	}
	stream, _ := value["stream"].(bool)
	expectedChoices := 0
	if protocol == channel.ProtocolOpenAIChat {
		n, valid := intFieldDefault(value, "n", 1)
		if !valid || n == 0 || n > 128 {
			return ParsedRequest{}, ErrInvalidInput
		}
		expectedChoices = int(n)
	}
	if protocol == channel.ProtocolAnthropic && stream {
		// Anthropic uses the same endpoint and an explicit body flag.
		return ParsedRequest{CanonicalModelID: model, Stream: true, ExpectedChoices: expectedChoices}, nil
	}
	return ParsedRequest{CanonicalModelID: model, Stream: stream, ExpectedChoices: expectedChoices}, nil
}

// validateV1BillableRequest rejects request shapes whose worst-case upstream
// cost cannot be bounded by the immutable four-token-price snapshot. The v1
// gateway deliberately supports one generated candidate and client-executed
// function/custom tools only; server-side paid or implicit multi-turn tools
// require a future pricing model and preauthorization formula.
func validateV1BillableRequest(protocol channel.Protocol, body []byte) error {
	value, err := decodeJSONObject(body)
	if err != nil {
		return err
	}
	switch protocol {
	case channel.ProtocolOpenAIChat:
		n, valid := intFieldDefault(value, "n", 1)
		if !onlyKnownFields(value,
			"model", "messages", "frequency_penalty", "logit_bias", "logprobs", "top_logprobs",
			"max_completion_tokens", "max_tokens", "n", "modalities", "prediction", "presence_penalty",
			"reasoning_effort", "response_format", "seed", "service_tier", "stop", "store", "stream",
			"stream_options", "temperature", "tool_choice", "tools", "top_p", "user", "metadata",
			"parallel_tool_calls", "verbosity", "web_search_options", "audio", "function_call", "functions",
			"safety_identifier", "prompt_cache_key") ||
			!valid || n != 1 || requestFieldPresent(value, "web_search_options") || requestFieldEnabled(value, "store") ||
			!serviceTierAllowed(value, "service_tier", "auto", "default") || !chatTextOnly(value) ||
			!openAIClientToolsOnly(value["tools"]) || !openAILegacyFunctionsOnly(value["functions"]) ||
			!openAIToolChoiceSafe(value["tool_choice"]) || !chatPredictionTextOnly(value["prediction"]) {
			return ErrInvalidInput
		}
	case channel.ProtocolOpenAIResponse:
		if !onlyKnownFields(value,
			"model", "input", "include", "instructions", "max_output_tokens", "max_tool_calls", "metadata",
			"parallel_tool_calls", "previous_response_id", "prompt", "prompt_cache_key", "prompt_cache_retention",
			"reasoning", "safety_identifier", "service_tier", "store", "stream", "stream_options", "temperature",
			"text", "tool_choice", "tools", "top_logprobs", "top_p", "truncation", "user", "background",
			"conversation", "container", "context_management", "modalities", "audio", "image", "speech",
			"speech_config", "image_config") ||
			requestFieldEnabled(value, "background") || requestFieldPresent(value, "conversation") ||
			requestFieldPresent(value, "previous_response_id") || requestFieldPresent(value, "prompt") ||
			requestFieldPresent(value, "container") || requestFieldPresent(value, "context_management") ||
			requestFieldEnabled(value, "store") || !serviceTierAllowed(value, "service_tier", "auto", "default") || !standardReasoningMode(value) ||
			!responsesTextOnly(value) || !openAIClientToolsOnly(value["tools"]) || !openAIToolChoiceSafe(value["tool_choice"]) ||
			!nilOrEmptyArray(value["include"]) || !cacheRetentionAllowed(value, "prompt_cache_retention", "in_memory") {
			return ErrInvalidInput
		}
	case channel.ProtocolAnthropic:
		if !onlyKnownFields(value,
			"model", "max_tokens", "messages", "system", "metadata", "stop_sequences", "stream", "temperature",
			"thinking", "tool_choice", "tools", "top_k", "top_p", "service_tier", "inference_geo", "speed",
			"cache_control", "output_config", "mcp_servers", "container", "context_management", "priority", "fast") ||
			requestFieldPresent(value, "mcp_servers") || requestFieldPresent(value, "container") ||
			requestFieldPresent(value, "context_management") || requestFieldEnabled(value, "priority") ||
			requestFieldEnabled(value, "fast") || !serviceTierAllowed(value, "service_tier", "standard_only") ||
			!standardSpeed(value, "speed") || !anthropicInferenceGeo(value) || !anthropicTextOnly(value) ||
			!anthropicCacheControlSafe(value) || !anthropicClientToolsOnly(value["tools"]) || !anthropicToolChoiceSafe(value["tool_choice"]) {
			return ErrInvalidInput
		}
	case channel.ProtocolGemini:
		if !onlyKnownFields(value,
			"contents", "systemInstruction", "generationConfig", "safetySettings", "tools", "toolConfig", "labels",
			"cachedContent", "store", "serviceTier", "service_tier", "googleSearch", "googleSearchRetrieval",
			"codeExecution", "computerUse", "urlContext", "fileSearch", "mcpServers", "maps") {
			return ErrInvalidInput
		}
		generationConfig, _ := value["generationConfig"].(map[string]any)
		if raw, exists := value["generationConfig"]; exists && generationConfig == nil && raw != nil {
			return ErrInvalidInput
		}
		candidateCount, valid := intFieldDefault(generationConfig, "candidateCount", 1)
		if !onlyKnownFields(generationConfig,
			"candidateCount", "stopSequences", "maxOutputTokens", "temperature", "topP", "topK", "seed",
			"presencePenalty", "frequencyPenalty", "responseMimeType", "responseSchema", "responseJsonSchema",
			"responseModalities", "thinkingConfig", "speechConfig", "imageConfig", "responseSpeechConfig",
			"responseImageConfig", "mediaResolution", "logprobs", "responseLogprobs", "serviceTier") ||
			!valid || candidateCount != 1 || requestFieldPresent(value, "cachedContent") || requestFieldPresent(value, "store") ||
			!serviceTierAllowed(value, "serviceTier", "unspecified", "standard") ||
			!serviceTierAllowed(value, "service_tier", "unspecified", "standard") ||
			!serviceTierAllowed(generationConfig, "serviceTier", "unspecified", "standard") ||
			!geminiTextOnly(value, generationConfig) || !geminiClientToolsOnly(value["tools"]) || !geminiToolConfigSafe(value["toolConfig"]) {
			return ErrInvalidInput
		}
		for _, field := range []string{"googleSearch", "googleSearchRetrieval", "codeExecution", "computerUse", "urlContext", "fileSearch", "mcpServers", "maps"} {
			if requestFieldPresent(value, field) {
				return ErrInvalidInput
			}
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

func cacheRetentionAllowed(value map[string]any, key string, allowed ...string) bool {
	raw, exists := value[key]
	if !exists || raw == nil {
		return true
	}
	text, ok := raw.(string)
	if !ok {
		return false
	}
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(text), candidate) {
			return true
		}
	}
	return false
}

func openAIToolChoiceSafe(raw any) bool {
	if raw == nil {
		return true
	}
	if text, ok := raw.(string); ok {
		switch text {
		case "auto", "none", "required":
			return true
		default:
			return false
		}
	}
	choice, ok := raw.(map[string]any)
	if !ok || !onlyKnownFields(choice, "type", "function", "name") {
		return false
	}
	switch stringField(choice, "type") {
	case "function":
		function, ok := choice["function"].(map[string]any)
		return ok && onlyKnownFields(function, "name") && requiredStringField(function, "name")
	case "custom":
		return requiredStringField(choice, "name")
	default:
		return false
	}
}

func chatPredictionTextOnly(raw any) bool {
	if raw == nil {
		return true
	}
	prediction, ok := raw.(map[string]any)
	if !ok || !onlyKnownFields(prediction, "type", "content") || stringField(prediction, "type") != "content" {
		return false
	}
	return textContentOnly(prediction["content"], map[string]struct{}{"text": {}})
}

func anthropicToolChoiceSafe(raw any) bool {
	if raw == nil {
		return true
	}
	choice, ok := raw.(map[string]any)
	if !ok || !onlyKnownFields(choice, "type", "name", "disable_parallel_tool_use") {
		return false
	}
	if disabled, exists := choice["disable_parallel_tool_use"]; exists && disabled != nil && !isBool(disabled) {
		return false
	}
	switch stringField(choice, "type") {
	case "auto", "any", "none":
		return !requestFieldPresent(choice, "name")
	case "tool":
		return requiredStringField(choice, "name")
	default:
		return false
	}
}

func geminiToolConfigSafe(raw any) bool {
	if raw == nil {
		return true
	}
	config, ok := raw.(map[string]any)
	if !ok || !onlyKnownFields(config, "functionCallingConfig") {
		return false
	}
	functionConfig, ok := config["functionCallingConfig"].(map[string]any)
	if !ok || !onlyKnownFields(functionConfig, "mode", "allowedFunctionNames") {
		return false
	}
	if mode := strings.ToUpper(stringField(functionConfig, "mode")); mode != "" && mode != "AUTO" && mode != "ANY" && mode != "NONE" {
		return false
	}
	if names, exists := functionConfig["allowedFunctionNames"]; exists && names != nil {
		list, ok := names.([]any)
		if !ok {
			return false
		}
		for _, name := range list {
			if _, ok := name.(string); !ok {
				return false
			}
		}
	}
	return true
}

func standardReasoningMode(value map[string]any) bool {
	raw, exists := value["reasoning"]
	if !exists || raw == nil {
		return true
	}
	reasoning, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	mode, exists := reasoning["mode"]
	if !exists || mode == nil {
		return true
	}
	text, ok := mode.(string)
	return ok && strings.EqualFold(strings.TrimSpace(text), "standard")
}

func requestFieldPresent(value map[string]any, key string) bool {
	raw, exists := value[key]
	return exists && raw != nil
}

func requestFieldEnabled(value map[string]any, key string) bool {
	raw, exists := value[key]
	if !exists || raw == nil {
		return false
	}
	switch typed := raw.(type) {
	case bool:
		return typed
	case string:
		return strings.TrimSpace(typed) != ""
	case json.Number:
		return typed.String() != "0"
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func serviceTierAllowed(value map[string]any, key string, allowed ...string) bool {
	raw, exists := value[key]
	if !exists || raw == nil {
		return true
	}
	tier, ok := raw.(string)
	if !ok {
		return false
	}
	tier = strings.ToLower(strings.TrimSpace(tier))
	if tier == "" {
		return true
	}
	for _, candidate := range allowed {
		if tier == candidate {
			return true
		}
	}
	return false
}

func standardSpeed(value map[string]any, key string) bool {
	raw, exists := value[key]
	if !exists || raw == nil {
		return true
	}
	speed, ok := raw.(string)
	return ok && (strings.TrimSpace(speed) == "" || strings.EqualFold(strings.TrimSpace(speed), "standard"))
}

func chatTextOnly(value map[string]any) bool {
	if modalities, exists := value["modalities"]; exists {
		items, ok := modalities.([]any)
		if !ok || len(items) != 1 || items[0] != "text" {
			return false
		}
	}
	if requestFieldPresent(value, "audio") || containsForbiddenJSON(value["messages"], map[string]struct{}{
		"audio": {}, "image_url": {}, "input_audio": {}, "file": {},
	}, map[string]struct{}{
		"audio": {}, "image_url": {}, "input_audio": {}, "file": {},
	}) {
		return false
	}
	rawMessages, exists := value["messages"]
	if !exists {
		return true
	}
	messages, ok := rawMessages.([]any)
	if !ok {
		return false
	}
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			return false
		}
		switch stringField(message, "role") {
		case "developer", "system", "user":
			if !onlyKnownFields(message, "role", "content", "name") || !optionalStringField(message, "name") ||
				!textContentOnly(message["content"], map[string]struct{}{"text": {}}) {
				return false
			}
		case "assistant":
			if !onlyKnownFields(message, "role", "content", "name", "refusal", "tool_calls", "function_call") ||
				!optionalStringField(message, "name") || !optionalStringField(message, "refusal") ||
				!textContentOnly(message["content"], map[string]struct{}{"text": {}}) {
				return false
			}
			if calls, exists := message["tool_calls"]; exists && calls != nil && !billableOpenAIToolCalls(calls, false) {
				return false
			}
			if call, exists := message["function_call"]; exists && call != nil {
				function, ok := call.(map[string]any)
				if !ok || !billableOpenAIFunction(function, false) {
					return false
				}
			}
		case "tool":
			if !onlyKnownFields(message, "role", "content", "tool_call_id") || !requiredStringField(message, "tool_call_id") ||
				!textContentOnly(message["content"], map[string]struct{}{"text": {}}) {
				return false
			}
		case "function":
			if !onlyKnownFields(message, "role", "content", "name") || !requiredStringField(message, "name") ||
				!textContentOnly(message["content"], map[string]struct{}{"text": {}}) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func responsesTextOnly(value map[string]any) bool {
	if instructions, exists := value["instructions"]; exists && instructions != nil {
		if _, ok := instructions.(string); !ok {
			return false
		}
	}
	if modalities, exists := value["modalities"]; exists {
		items, ok := modalities.([]any)
		if !ok || len(items) != 1 || items[0] != "text" {
			return false
		}
	}
	if requestFieldPresent(value, "audio") || requestFieldPresent(value, "image") || requestFieldPresent(value, "speech") ||
		requestFieldPresent(value, "speech_config") || requestFieldPresent(value, "image_config") ||
		containsForbiddenJSON(value["input"], map[string]struct{}{
			"input_image": {}, "input_file": {}, "image_url": {}, "file_id": {}, "vector_store_id": {}, "vector_store_ids": {},
			"audio": {}, "image": {}, "speech": {}, "speech_config": {}, "image_config": {},
		}, map[string]struct{}{
			"input_image": {}, "input_file": {}, "image": {}, "audio": {},
		}) {
		return false
	}
	rawInput, exists := value["input"]
	if !exists || rawInput == nil {
		return true
	}
	if _, ok := rawInput.(string); ok {
		return true
	}
	items, ok := rawInput.([]any)
	if !ok {
		return false
	}
	for _, rawItem := range items {
		if _, ok := rawItem.(string); ok {
			continue
		}
		item, ok := rawItem.(map[string]any)
		if !ok {
			return false
		}
		switch stringField(item, "type") {
		case "", "message":
			if !onlyKnownFields(item, "id", "type", "role", "status", "content") ||
				!optionalStringField(item, "role") || !optionalStringField(item, "status") ||
				!textContentOnly(item["content"], map[string]struct{}{"text": {}, "input_text": {}, "output_text": {}}) {
				return false
			}
		case "function_call_output", "custom_tool_call_output":
			if !onlyKnownFields(item, "id", "type", "call_id", "output", "status") ||
				!requiredStringField(item, "call_id") || !optionalStringField(item, "status") {
				return false
			}
			output, exists := item["output"]
			if !exists || !textContentOnly(output, map[string]struct{}{"text": {}, "input_text": {}, "output_text": {}}) {
				return false
			}
		case "function_call":
			if !onlyKnownFields(item, "id", "type", "call_id", "name", "arguments", "status") ||
				!requiredStringField(item, "call_id") || !requiredStringField(item, "name") || !requiredStringField(item, "arguments") || !optionalStringField(item, "status") {
				return false
			}
		case "custom_tool_call":
			if !onlyKnownFields(item, "id", "type", "call_id", "name", "input", "status") ||
				!requiredStringField(item, "call_id") || !requiredStringField(item, "name") || !requiredStringField(item, "input") || !optionalStringField(item, "status") {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func anthropicInferenceGeo(value map[string]any) bool {
	raw, exists := value["inference_geo"]
	if !exists || raw == nil {
		return true
	}
	geo, ok := raw.(string)
	return ok && strings.EqualFold(strings.TrimSpace(geo), "global")
}

func anthropicCacheControlSafe(value map[string]any) bool {
	if !validAnthropicCacheControl(value) || !anthropicCacheBlocksSafe(value["system"]) {
		return false
	}
	if messages, ok := value["messages"].([]any); ok {
		for _, rawMessage := range messages {
			message, _ := rawMessage.(map[string]any)
			if !anthropicCacheBlocksSafe(message["content"]) {
				return false
			}
		}
	}
	if tools, ok := value["tools"].([]any); ok {
		for _, rawTool := range tools {
			tool, _ := rawTool.(map[string]any)
			if !validAnthropicCacheControl(tool) {
				return false
			}
		}
	}
	return true
}

func anthropicCacheBlocksSafe(raw any) bool {
	if raw == nil {
		return true
	}
	blocks, ok := raw.([]any)
	if !ok {
		_, text := raw.(string)
		return text
	}
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok || !validAnthropicCacheControl(block) {
			return false
		}
		// A tool_result may itself contain text content blocks. Its user-supplied
		// tool payload otherwise remains opaque, so an input property named
		// cache_control is never interpreted as a provider directive.
		if stringField(block, "type") == "tool_result" {
			if content, nested := block["content"].([]any); nested && !anthropicCacheBlocksSafe(content) {
				return false
			}
		}
	}
	return true
}

func validAnthropicCacheControl(container map[string]any) bool {
	raw, exists := container["cache_control"]
	if !exists || raw == nil {
		return true
	}
	control, ok := raw.(map[string]any)
	if !ok || len(control) == 0 || len(control) > 2 || stringField(control, "type") != "ephemeral" {
		return false
	}
	if ttl, exists := control["ttl"]; exists {
		ttlValue, ok := ttl.(string)
		return ok && ttlValue == "5m"
	}
	return true
}

func anthropicTextOnly(value map[string]any) bool {
	if system, exists := value["system"]; exists && !textContentOnly(system, map[string]struct{}{"text": {}}) {
		return false
	}
	if containsForbiddenJSON(value["messages"], map[string]struct{}{
		"image": {}, "document": {}, "file": {}, "file_id": {}, "source": {},
	}, map[string]struct{}{
		"image": {}, "document": {}, "file": {}, "server_tool_use": {}, "web_search_tool_result": {},
	}) {
		return false
	}
	rawMessages, exists := value["messages"]
	if !exists {
		return true
	}
	messages, ok := rawMessages.([]any)
	if !ok {
		return false
	}
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok || !onlyKnownFields(message, "role", "content") ||
			(stringField(message, "role") != "user" && stringField(message, "role") != "assistant") ||
			!anthropicContentOnly(message["content"]) {
			return false
		}
	}
	return true
}

func anthropicContentOnly(raw any) bool {
	if raw == nil {
		return true
	}
	if _, ok := raw.(string); ok {
		return true
	}
	blocks, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			return false
		}
		switch stringField(block, "type") {
		case "text":
			if !onlyKnownFields(block, "type", "text", "cache_control") || !requiredStringField(block, "text") {
				return false
			}
		case "thinking":
			if !onlyKnownFields(block, "type", "thinking", "signature", "cache_control") || !requiredStringField(block, "thinking") || !optionalStringField(block, "signature") {
				return false
			}
		case "tool_use":
			if !onlyKnownFields(block, "type", "id", "name", "input", "cache_control") ||
				!requiredStringField(block, "id") || !requiredStringField(block, "name") {
				return false
			}
			if _, ok := block["input"].(map[string]any); !ok {
				return false
			}
		case "tool_result":
			if !onlyKnownFields(block, "type", "tool_use_id", "content", "is_error", "cache_control") || !requiredStringField(block, "tool_use_id") {
				return false
			}
			if rawError, exists := block["is_error"]; exists && rawError != nil && !isBool(rawError) {
				return false
			}
			if content, exists := block["content"]; exists && !textContentOnly(content, map[string]struct{}{"text": {}}) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func geminiTextOnly(value, generationConfig map[string]any) bool {
	if modalities, exists := generationConfig["responseModalities"]; exists {
		items, ok := modalities.([]any)
		if !ok {
			return false
		}
		for _, raw := range items {
			modality, ok := raw.(string)
			if !ok || !strings.EqualFold(strings.TrimSpace(modality), "TEXT") {
				return false
			}
		}
	}
	if containsForbiddenJSON(generationConfig, map[string]struct{}{
		"speechConfig": {}, "imageConfig": {}, "responseSpeechConfig": {}, "responseImageConfig": {},
	}, nil) {
		return false
	}
	for _, field := range []string{"contents", "systemInstruction"} {
		raw, exists := value[field]
		if !exists || raw == nil {
			continue
		}
		contents := []any{raw}
		if list, ok := raw.([]any); ok {
			contents = list
		}
		for _, rawContent := range contents {
			content, ok := rawContent.(map[string]any)
			if !ok || !onlyKnownFields(content, "role", "parts") || !optionalStringField(content, "role") {
				return false
			}
			parts, ok := content["parts"].([]any)
			if !ok {
				return false
			}
			for _, rawPart := range parts {
				part, ok := rawPart.(map[string]any)
				if !ok || len(part) == 0 {
					return false
				}
				for key := range part {
					if key != "text" && key != "functionCall" && key != "functionResponse" && key != "thought" && key != "thoughtSignature" {
						return false
					}
				}
				if thought, exists := part["thought"]; exists && thought != nil && !isBool(thought) {
					return false
				}
				if !optionalStringField(part, "thoughtSignature") {
					return false
				}
				semanticFields := 0
				if _, exists := part["text"]; exists {
					if !requiredStringField(part, "text") {
						return false
					}
					semanticFields++
				}
				if call, exists := part["functionCall"]; exists {
					function, ok := call.(map[string]any)
					if !ok || !onlyKnownFields(function, "id", "name", "args") || !requiredStringField(function, "name") {
						return false
					}
					if args, exists := function["args"]; exists && args != nil {
						if _, ok := args.(map[string]any); !ok {
							return false
						}
					}
					semanticFields++
				}
				if response, exists := part["functionResponse"]; exists && !geminiFunctionResponseSafe(response) {
					return false
				}
				if _, exists := part["functionResponse"]; exists {
					semanticFields++
				}
				if semanticFields != 1 {
					return false
				}
			}
		}
	}
	return true
}

func geminiFunctionResponseSafe(raw any) bool {
	if _, ok := raw.(string); ok {
		return true
	}
	if _, ok := raw.(map[string]any); !ok {
		return false
	}
	return !containsForbiddenJSON(raw, map[string]struct{}{
		"parts": {}, "inlineData": {}, "fileData": {}, "executableCode": {}, "codeExecutionResult": {},
		"blob": {}, "mimeType": {}, "fileUri": {}, "uri": {}, "$ref": {},
	}, map[string]struct{}{
		"image": {}, "audio": {}, "file": {}, "video": {},
	})
}

func textContentOnly(raw any, allowedTypes map[string]struct{}) bool {
	if raw == nil {
		return true
	}
	if _, ok := raw.(string); ok {
		return true
	}
	parts, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			return false
		}
		if _, allowed := allowedTypes[stringField(part, "type")]; !allowed {
			return false
		}
		if !onlyKnownFields(part, "type", "text") || !requiredStringField(part, "text") {
			return false
		}
	}
	return true
}

func containsForbiddenJSON(raw any, forbiddenKeys, forbiddenTypes map[string]struct{}) bool {
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			if containsForbiddenJSON(item, forbiddenKeys, forbiddenTypes) {
				return true
			}
		}
	case map[string]any:
		for key, item := range value {
			if _, forbidden := forbiddenKeys[key]; forbidden {
				return true
			}
			if key == "type" {
				if itemType, ok := item.(string); ok {
					if _, forbidden := forbiddenTypes[itemType]; forbidden {
						return true
					}
				}
			}
			if containsForbiddenJSON(item, forbiddenKeys, forbiddenTypes) {
				return true
			}
		}
	}
	return false
}

func openAIClientToolsOnly(raw any) bool {
	if raw == nil {
		return true
	}
	tools, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			return false
		}
		toolType, ok := tool["type"].(string)
		if !ok || (toolType != "function" && toolType != "custom") {
			return false
		}
		if toolType == "function" {
			if !onlyKnownFields(tool, "type", "function") {
				return false
			}
			function, ok := tool["function"].(map[string]any)
			if !ok || !openAIFunctionDefinitionSafe(function) {
				return false
			}
		} else if !onlyKnownFields(tool, "type", "name", "description", "format") ||
			!requiredStringField(tool, "name") || !optionalStringField(tool, "description") {
			return false
		}
	}
	return true
}

func openAILegacyFunctionsOnly(raw any) bool {
	if raw == nil {
		return true
	}
	functions, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, rawFunction := range functions {
		function, ok := rawFunction.(map[string]any)
		if !ok || !openAIFunctionDefinitionSafe(function) {
			return false
		}
	}
	return true
}

func openAIFunctionDefinitionSafe(function map[string]any) bool {
	if !onlyKnownFields(function, "name", "description", "parameters", "strict") ||
		!requiredStringField(function, "name") || !optionalStringField(function, "description") {
		return false
	}
	if parameters, exists := function["parameters"]; exists && parameters != nil {
		if _, ok := parameters.(map[string]any); !ok {
			return false
		}
	}
	if strict, exists := function["strict"]; exists && strict != nil && !isBool(strict) {
		return false
	}
	return true
}

func anthropicClientToolsOnly(raw any) bool {
	if raw == nil {
		return true
	}
	tools, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok || !onlyKnownFields(tool, "type", "name", "description", "input_schema", "cache_control") ||
			!requiredStringField(tool, "name") || !optionalStringField(tool, "description") {
			return false
		}
		if rawType, exists := tool["type"]; exists {
			toolType, ok := rawType.(string)
			if !ok || (toolType != "function" && toolType != "custom") {
				return false
			}
		}
		if schema, exists := tool["input_schema"]; exists && schema != nil {
			if _, ok := schema.(map[string]any); !ok {
				return false
			}
		}
	}
	return true
}

func geminiClientToolsOnly(raw any) bool {
	if raw == nil {
		return true
	}
	tools, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok || len(tool) != 1 {
			return false
		}
		declarations, exists := tool["functionDeclarations"]
		if !exists {
			declarations, exists = tool["function_declarations"]
		}
		if !exists {
			return false
		}
		list, ok := declarations.([]any)
		if !ok {
			return false
		}
		for _, rawDeclaration := range list {
			declaration, ok := rawDeclaration.(map[string]any)
			if !ok || !onlyKnownFields(declaration, "name", "description", "parameters", "parametersJsonSchema", "response", "responseJsonSchema") ||
				!requiredStringField(declaration, "name") || !optionalStringField(declaration, "description") {
				return false
			}
			for _, schemaField := range []string{"parameters", "parametersJsonSchema", "response", "responseJsonSchema"} {
				if schema, exists := declaration[schemaField]; exists && schema != nil {
					if _, ok := schema.(map[string]any); !ok {
						return false
					}
				}
			}
		}
	}
	return true
}

func RewriteRequest(protocol channel.Protocol, original []byte, upstreamModelID string, stream bool) ([]byte, error) {
	if !validProtocol(protocol) || strings.TrimSpace(upstreamModelID) == "" {
		return nil, ErrInvalidInput
	}
	value, err := decodeJSONObject(original)
	if err != nil {
		return nil, err
	}
	value["model"] = upstreamModelID
	if protocol == channel.ProtocolOpenAIChat || protocol == channel.ProtocolOpenAIResponse {
		value["service_tier"] = "default"
	}
	if protocol == channel.ProtocolOpenAIChat && stream {
		options, _ := value["stream_options"].(map[string]any)
		if options == nil {
			options = make(map[string]any)
		}
		options["include_usage"] = true
		options["include_obfuscation"] = false
		value["stream_options"] = options
	}
	return marshalJSONObjectWithin(value, MaxRequestBytes)
}

func RewriteNonStreamingResponse(protocol channel.Protocol, body []byte, canonicalModelID string, expectedChoices int) ([]byte, *ledger.UsageV1, error) {
	value, err := decodeJSONObject(body)
	if err != nil {
		return nil, nil, err
	}
	if responseErr := validateSuccessfulResponse(protocol, value, expectedChoices); responseErr != nil {
		return nil, nil, responseErr
	}
	rewriteModelMetadata(protocol, value, canonicalModelID)
	observation, ok := usageObservation(protocol, value)
	if !ok {
		return nil, nil, ErrNoUsage
	}
	usage, complete := observation.Complete()
	if !complete {
		return nil, nil, ErrNoUsage
	}
	rewritten, err := marshalJSONObjectWithin(value, MaxNonStreamingBytes)
	if err != nil {
		return nil, nil, err
	}
	return rewritten, usage, nil
}

func AnalyzeSSEFrame(protocol channel.Protocol, frame []byte, canonicalModelID string) (SSEAnalysis, error) {
	analysis := SSEAnalysis{Frame: append([]byte(nil), frame...)}
	data, eventName, ok, envelopeErr := splitSSEData(frame)
	if envelopeErr != nil {
		return SSEAnalysis{}, envelopeErr
	}
	if !ok {
		analysis.Frame = nil
		analysis.AfterTerminalAllowed = true
		return analysis, nil
	}
	if strings.TrimSpace(string(data)) == "[DONE]" {
		if protocol != channel.ProtocolOpenAIChat || eventName != "" {
			return SSEAnalysis{}, ErrInvalidInput
		}
		analysis.Terminal = true
		analysis.StreamEnd = true
		return analysis, nil
	}
	value, err := decodeJSONObject(data)
	if err != nil {
		return SSEAnalysis{}, err
	}
	if responseErr := streamingResponseError(protocol, value); responseErr != nil {
		if !matchingSSEEventName(protocol, eventName, stringField(value, "type")) {
			return SSEAnalysis{}, ErrInvalidInput
		}
		analysis.ErrorCode = responseErr.Code
		analysis.ErrorMessage = responseErr.Message
		analysis.Frame = rebuildSSEFrame(eventName, data)
		return analysis, nil
	}
	if !matchingSSEEventName(protocol, eventName, stringField(value, "type")) {
		return SSEAnalysis{}, ErrInvalidInput
	}
	if !billableStreamingResponse(protocol, value) {
		return SSEAnalysis{}, &UpstreamResponseError{
			Code:    "unsupported_billing_shape",
			Message: "upstream returned content outside the v1 text and client-tool billing contract",
		}
	}
	rewriteModelMetadata(protocol, value, canonicalModelID)
	analysis.Semantic = semanticEvent(protocol, value)
	analysis.CredentialFragments, err = streamingCredentialFragments(protocol, value)
	if err != nil {
		return SSEAnalysis{}, err
	}
	analysis.Terminal = terminalEvent(protocol, value)
	analysis.StreamEnd = streamEndEvent(protocol, value)
	analysis.FinishObserved = finishObserved(protocol, value)
	analysis.AfterTerminalAllowed = afterTerminalAllowed(protocol, value)
	if protocol == channel.ProtocolOpenAIChat {
		analysis.ChoiceIndexes, analysis.FinishedChoiceIndexes = chatChoiceProgress(value)
	}
	if observation, found := usageObservation(protocol, value); found {
		analysis.Observation = observation
	}
	rewritten, err := marshalJSONObjectWithin(value, MaxSSEEventBytes)
	if err != nil {
		return SSEAnalysis{}, err
	}
	analysis.Frame = rebuildSSEFrame(eventName, rewritten)
	if len(analysis.Frame) > MaxSSEEventBytes {
		return SSEAnalysis{}, ErrResponseTooBig
	}
	return analysis, nil
}

func matchingSSEEventName(protocol channel.Protocol, eventName, dataType string) bool {
	if eventName == "" {
		return true
	}
	if protocol != channel.ProtocolOpenAIResponse && protocol != channel.ProtocolAnthropic {
		return false
	}
	return eventName == dataType
}

func rebuildSSEFrame(eventName string, data []byte) []byte {
	var buffer bytes.Buffer
	if eventName != "" {
		buffer.WriteString("event: ")
		buffer.WriteString(eventName)
		buffer.WriteByte('\n')
	}
	buffer.WriteString("data: ")
	buffer.Write(data)
	buffer.WriteString("\n\n")
	return buffer.Bytes()
}

func marshalJSONObject(value map[string]any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func marshalJSONObjectWithin(value map[string]any, maximum int64) ([]byte, error) {
	encoded, err := marshalJSONObject(value)
	if err != nil {
		return nil, err
	}
	if int64(len(encoded)) > maximum {
		return nil, ErrResponseTooBig
	}
	return encoded, nil
}

func validateSuccessfulResponse(protocol channel.Protocol, value map[string]any, expectedChoices int) *UpstreamResponseError {
	if responseErr := responseEnvelopeError(protocol, value); responseErr != nil {
		return responseErr
	}
	invalid := func() *UpstreamResponseError {
		return &UpstreamResponseError{Code: "invalid_upstream_response", Message: "upstream returned no protocol success object"}
	}
	switch protocol {
	case channel.ProtocolOpenAIChat:
		if !onlyKnownFields(value, "id", "object", "created", "model", "system_fingerprint", "service_tier", "choices", "usage", "metadata", "moderation") {
			return invalid()
		}
		choices, ok := value["choices"].([]any)
		if !ok || expectedChoices <= 0 || len(choices) != expectedChoices || stringField(value, "object") != "chat.completion" ||
			!serviceTierAllowed(value, "service_tier", "auto", "default") ||
			stringField(value, "id") == "" || stringField(value, "model") == "" {
			return invalid()
		}
		indexes := make(map[int64]struct{}, expectedChoices)
		for _, raw := range choices {
			choice, _ := raw.(map[string]any)
			if !onlyKnownFields(choice, "index", "message", "logprobs", "finish_reason") {
				return invalid()
			}
			index, indexOK := intField(choice, "index")
			message, messageOK := choice["message"].(map[string]any)
			if !indexOK || index < 0 || index >= int64(expectedChoices) {
				return invalid()
			}
			if _, duplicate := indexes[index]; duplicate {
				return invalid()
			}
			indexes[index] = struct{}{}
			if !messageOK || stringField(message, "role") != "assistant" || stringField(choice, "finish_reason") == "" || !billableChatMessage(message) {
				return invalid()
			}
		}
	case channel.ProtocolOpenAIResponse:
		if !billableResponsesEnvelope(value, true) {
			return invalid()
		}
	case channel.ProtocolAnthropic:
		if !onlyKnownFields(value, "id", "type", "role", "model", "content", "stop_reason", "stop_sequence", "stop_details", "usage", "container") || requestFieldPresent(value, "container") {
			return invalid()
		}
		content, ok := value["content"].([]any)
		if !ok || stringField(value, "type") != "message" || stringField(value, "role") != "assistant" || stringField(value, "stop_reason") == "" ||
			stringField(value, "id") == "" || stringField(value, "model") == "" || !billableAnthropicContent(content) {
			return invalid()
		}
	case channel.ProtocolGemini:
		if !onlyKnownFields(value, "candidates", "usageMetadata", "modelVersion", "responseId", "promptFeedback", "createTime", "modelStatus") {
			return invalid()
		}
		candidates, ok := value["candidates"].([]any)
		if !ok || len(candidates) != 1 {
			return invalid()
		}
		candidate, _ := candidates[0].(map[string]any)
		if stringField(candidate, "finishReason") == "" || !geminiCandidateIndexZero(candidate) || !billableGeminiCandidate(candidate) {
			return invalid()
		}
	default:
		return invalid()
	}
	return nil
}

func billableChatMessage(message map[string]any) bool {
	if !onlyKnownFields(message, "role", "content", "refusal", "tool_calls", "function_call") {
		return false
	}
	if role, exists := message["role"]; exists && role != nil && role != "assistant" {
		return false
	}
	if content, exists := message["content"]; exists && content != nil {
		if _, ok := content.(string); !ok {
			return false
		}
	}
	if refusal, exists := message["refusal"]; exists && refusal != nil {
		if _, ok := refusal.(string); !ok {
			return false
		}
	}
	if calls, exists := message["tool_calls"]; exists && calls != nil && !billableOpenAIToolCalls(calls, false) {
		return false
	}
	if call, exists := message["function_call"]; exists && call != nil {
		function, ok := call.(map[string]any)
		if !ok || !billableOpenAIFunction(function, false) {
			return false
		}
	}
	return requestFieldPresent(message, "content") || requestFieldPresent(message, "refusal") ||
		requestFieldPresent(message, "tool_calls") || requestFieldPresent(message, "function_call")
}

func billableChatDelta(message map[string]any) bool {
	if !onlyKnownFields(message, "role", "content", "refusal", "tool_calls", "function_call") ||
		!optionalStringField(message, "role") || !optionalStringField(message, "content") || !optionalStringField(message, "refusal") {
		return false
	}
	if role := stringField(message, "role"); role != "" && role != "assistant" {
		return false
	}
	if calls, exists := message["tool_calls"]; exists && calls != nil && !billableOpenAIToolCalls(calls, true) {
		return false
	}
	if rawCall, exists := message["function_call"]; exists && rawCall != nil {
		call, ok := rawCall.(map[string]any)
		if !ok || !billableOpenAIFunction(call, true) {
			return false
		}
	}
	return true
}

func billableOpenAIToolCalls(raw any, partial bool) bool {
	calls, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, rawCall := range calls {
		call, ok := rawCall.(map[string]any)
		if !ok || !onlyKnownFields(call, "index", "id", "type", "function", "custom") ||
			!optionalStringField(call, "id") || !optionalStringField(call, "type") {
			return false
		}
		typeName := strings.ToLower(stringField(call, "type"))
		_, hasFunction := call["function"]
		_, hasCustom := call["custom"]
		if hasFunction && hasCustom {
			return false
		}
		if partial && !hasFunction && !hasCustom {
			if typeName != "" && typeName != "function" && typeName != "custom" {
				return false
			}
			continue
		}
		if partial && typeName == "" {
			if hasFunction {
				typeName = "function"
			} else if hasCustom {
				typeName = "custom"
			}
		}
		if typeName != "function" && typeName != "custom" {
			return false
		}
		if typeName == "function" {
			function, ok := call["function"].(map[string]any)
			if !ok || !billableOpenAIFunction(function, partial) || (!partial && !requiredStringField(call, "id")) {
				return false
			}
		} else {
			custom, ok := call["custom"].(map[string]any)
			if !ok || !onlyKnownFields(custom, "name", "input") ||
				(!partial && (!requiredStringField(call, "id") || !requiredStringField(custom, "name") || !requiredStringField(custom, "input"))) ||
				(partial && (!optionalStringField(custom, "name") || !optionalStringField(custom, "input"))) {
				return false
			}
		}
	}
	return true
}

func billableOpenAIFunction(function map[string]any, partial bool) bool {
	if !onlyKnownFields(function, "name", "arguments") {
		return false
	}
	if partial {
		return optionalStringField(function, "name") && optionalStringField(function, "arguments")
	}
	return requiredStringField(function, "name") && requiredStringField(function, "arguments")
}

func billableResponsesOutput(output []any, complete bool) bool {
	for _, rawItem := range output {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return false
		}
		switch stringField(item, "type") {
		case "message":
			if !onlyKnownFields(item, "id", "type", "status", "role", "content") ||
				(item["role"] != nil && item["role"] != "assistant") {
				return false
			}
			content, ok := item["content"].([]any)
			if !ok || !billableResponsesContent(content) || (complete && (!requiredStringField(item, "id") || stringField(item, "status") != "completed")) {
				return false
			}
		case "reasoning":
			if !onlyKnownFields(item, "id", "type", "status", "summary", "content", "encrypted_content") || !optionalStringField(item, "encrypted_content") {
				return false
			}
			if summary, exists := item["summary"]; exists && summary != nil {
				items, ok := summary.([]any)
				if !ok || !billableResponsesContent(items) {
					return false
				}
			}
			if content, exists := item["content"]; exists && content != nil {
				items, ok := content.([]any)
				if !ok || !billableResponsesContent(items) {
					return false
				}
			}
			if complete && !optionalCompletedStatus(item) {
				return false
			}
		case "function_call":
			if !onlyKnownFields(item, "id", "type", "status", "call_id", "name", "arguments") ||
				((complete && (!requiredStringField(item, "call_id") || !requiredStringField(item, "name") || !requiredStringField(item, "arguments") || !optionalCompletedStatus(item))) ||
					(!complete && (!optionalStringField(item, "call_id") || !optionalStringField(item, "name") || !optionalStringField(item, "arguments")))) {
				return false
			}
		case "custom_tool_call":
			if !onlyKnownFields(item, "id", "type", "status", "call_id", "name", "input") ||
				((complete && (!requiredStringField(item, "call_id") || !requiredStringField(item, "name") || !requiredStringField(item, "input") || !optionalCompletedStatus(item))) ||
					(!complete && (!optionalStringField(item, "call_id") || !optionalStringField(item, "name") || !optionalStringField(item, "input")))) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func optionalCompletedStatus(value map[string]any) bool {
	status, exists := value["status"]
	return !exists || status == nil || status == "completed"
}

func billableResponsesEnvelope(response map[string]any, complete bool) bool {
	if !onlyKnownFields(response,
		"id", "object", "created_at", "completed_at", "status", "error", "incomplete_details", "instructions",
		"max_output_tokens", "max_tool_calls", "model", "output", "parallel_tool_calls", "previous_response_id",
		"reasoning", "service_tier", "store", "temperature", "text", "tool_choice", "tools", "top_p",
		"truncation", "usage", "user", "metadata", "background", "conversation", "moderation", "prompt",
		"prompt_cache_key", "prompt_cache_retention", "prompt_cache_options", "safety_identifier", "top_logprobs") ||
		stringField(response, "object") != "response" || requestFieldEnabled(response, "background") ||
		requestFieldPresent(response, "conversation") || requestFieldPresent(response, "moderation") || requestFieldPresent(response, "prompt") ||
		requestFieldPresent(response, "previous_response_id") || requestFieldEnabled(response, "store") ||
		!serviceTierAllowed(response, "service_tier", "auto", "default") {
		return false
	}
	rawOutput, exists := response["output"]
	if !exists {
		return !complete
	}
	output, ok := rawOutput.([]any)
	if !ok || !billableResponsesOutput(output, complete) {
		return false
	}
	return !complete || (stringField(response, "id") != "" && stringField(response, "model") != "" && stringField(response, "status") == "completed")
}

func billableResponsesContent(content []any) bool {
	for _, rawBlock := range content {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			return false
		}
		switch stringField(block, "type") {
		case "output_text", "text", "refusal", "summary_text", "reasoning_text":
			if !onlyKnownFields(block, "type", "text", "refusal", "annotations", "logprobs") ||
				!optionalStringField(block, "text") || !optionalStringField(block, "refusal") || !nilOrEmptyArray(block["annotations"]) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func billableAnthropicContent(content []any) bool {
	for _, rawBlock := range content {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			return false
		}
		switch stringField(block, "type") {
		case "text":
			if !onlyKnownFields(block, "type", "text", "citations") || !requiredStringField(block, "text") || !nilOrEmptyArray(block["citations"]) {
				return false
			}
		case "thinking":
			if !onlyKnownFields(block, "type", "thinking", "signature") || !requiredStringField(block, "thinking") || !optionalStringField(block, "signature") {
				return false
			}
		case "redacted_thinking":
			if !onlyKnownFields(block, "type", "data") || !requiredStringField(block, "data") {
				return false
			}
		case "tool_use":
			if !onlyKnownFields(block, "type", "id", "name", "input") || !requiredStringField(block, "id") || !requiredStringField(block, "name") {
				return false
			}
			if _, ok := block["input"].(map[string]any); !ok {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func geminiCandidateIndexZero(candidate map[string]any) bool {
	if _, exists := candidate["index"]; !exists {
		return true
	}
	index, ok := intField(candidate, "index")
	return ok && index == 0
}

func billableGeminiCandidate(candidate map[string]any) bool {
	if !onlyKnownFields(candidate, "content", "finishReason", "index", "safetyRatings", "finishMessage") {
		return false
	}
	rawContent, exists := candidate["content"]
	if !exists || rawContent == nil {
		return true
	}
	content, ok := rawContent.(map[string]any)
	if !ok || !onlyKnownFields(content, "role", "parts") || !optionalStringField(content, "role") {
		return false
	}
	rawParts, exists := content["parts"]
	if !exists || rawParts == nil {
		return true
	}
	parts, ok := rawParts.([]any)
	if !ok {
		return false
	}
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			return false
		}
		if !onlyKnownFields(part, "text", "functionCall", "thought", "thoughtSignature") ||
			(part["thought"] != nil && !isBool(part["thought"])) || !optionalStringField(part, "thoughtSignature") {
			return false
		}
		allowed := 0
		if _, exists := part["text"]; exists {
			if _, ok := part["text"].(string); !ok {
				return false
			}
			allowed++
		}
		if _, exists := part["functionCall"]; exists {
			call, ok := part["functionCall"].(map[string]any)
			if !ok || !onlyKnownFields(call, "id", "name", "args") || !requiredStringField(call, "name") {
				return false
			}
			if args, exists := call["args"]; exists && args != nil {
				if _, ok := args.(map[string]any); !ok {
					return false
				}
			}
			allowed++
		}
		if allowed != 1 {
			return false
		}
	}
	return true
}

func onlyKnownFields(value map[string]any, fields ...string) bool {
	allowed := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		allowed[field] = struct{}{}
	}
	for field := range value {
		if _, ok := allowed[field]; !ok {
			return false
		}
	}
	return true
}

func optionalStringField(value map[string]any, key string) bool {
	raw, exists := value[key]
	if !exists || raw == nil {
		return true
	}
	_, ok := raw.(string)
	return ok
}

func requiredStringField(value map[string]any, key string) bool {
	raw, exists := value[key]
	_, ok := raw.(string)
	return exists && ok
}

func isBool(value any) bool {
	_, ok := value.(bool)
	return ok
}

func nilOrEmptyArray(value any) bool {
	if value == nil {
		return true
	}
	items, ok := value.([]any)
	return ok && len(items) == 0
}

func billableStreamingResponse(protocol channel.Protocol, value map[string]any) bool {
	switch protocol {
	case channel.ProtocolOpenAIChat:
		if !onlyKnownFields(value, "id", "object", "created", "model", "system_fingerprint", "service_tier", "choices", "usage", "obfuscation", "moderation") || !optionalStringField(value, "obfuscation") {
			return false
		}
		if object := stringField(value, "object"); object != "" && object != "chat.completion.chunk" {
			return false
		}
		choices, ok := value["choices"].([]any)
		if !ok || len(choices) > 1 {
			return false
		}
		for position, rawChoice := range choices {
			choice, ok := rawChoice.(map[string]any)
			if !ok || !onlyKnownFields(choice, "index", "delta", "logprobs", "finish_reason") || !optionalStringField(choice, "finish_reason") {
				return false
			}
			index, valid := intField(choice, "index")
			if !valid {
				index = int64(position)
			}
			if index != 0 {
				return false
			}
			if delta, exists := choice["delta"]; exists && delta != nil {
				message, ok := delta.(map[string]any)
				if !ok || !billableChatDelta(message) {
					return false
				}
			}
		}
		return true
	case channel.ProtocolOpenAIResponse:
		return billableResponsesStreamEvent(value)
	case channel.ProtocolAnthropic:
		switch stringField(value, "type") {
		case "message_start":
			if !onlyKnownFields(value, "type", "message") {
				return false
			}
			message := nestedObject(value, "message")
			if len(message) == 0 || !onlyKnownFields(message, "id", "type", "role", "model", "content", "stop_reason", "stop_sequence", "stop_details", "usage", "container") ||
				requestFieldPresent(message, "container") ||
				stringField(message, "type") != "message" || stringField(message, "role") != "assistant" {
				return false
			}
			if rawContent, exists := message["content"]; exists {
				content, ok := rawContent.([]any)
				return ok && billableAnthropicContent(content)
			}
			return true
		case "content_block_start":
			if !onlyKnownFields(value, "type", "index", "content_block") {
				return false
			}
			return billableAnthropicContent([]any{value["content_block"]})
		case "content_block_delta":
			if !onlyKnownFields(value, "type", "index", "delta") {
				return false
			}
			delta := nestedObject(value, "delta")
			switch stringField(delta, "type") {
			case "text_delta":
				return onlyKnownFields(delta, "type", "text") && requiredStringField(delta, "text")
			case "input_json_delta":
				return onlyKnownFields(delta, "type", "partial_json") && requiredStringField(delta, "partial_json")
			case "thinking_delta":
				return onlyKnownFields(delta, "type", "thinking") && requiredStringField(delta, "thinking")
			case "signature_delta":
				return onlyKnownFields(delta, "type", "signature") && requiredStringField(delta, "signature")
			default:
				return false
			}
		case "content_block_stop":
			return onlyKnownFields(value, "type", "index")
		case "message_delta":
			if !onlyKnownFields(value, "type", "delta", "usage") {
				return false
			}
			delta := nestedObject(value, "delta")
			return len(delta) > 0 && onlyKnownFields(delta, "stop_reason", "stop_sequence") &&
				optionalStringField(delta, "stop_reason") && optionalStringField(delta, "stop_sequence")
		case "message_stop", "ping":
			return onlyKnownFields(value, "type")
		default:
			return false
		}
	case channel.ProtocolGemini:
		if !onlyKnownFields(value, "candidates", "usageMetadata", "modelVersion", "responseId", "promptFeedback", "createTime", "modelStatus") {
			return false
		}
		candidates, ok := value["candidates"].([]any)
		if !ok || len(candidates) > 1 {
			return false
		}
		if len(candidates) == 0 {
			return value["usageMetadata"] != nil
		}
		candidate, ok := candidates[0].(map[string]any)
		return ok && geminiCandidateIndexZero(candidate) && billableGeminiCandidate(candidate)
	default:
		return false
	}
}

func billableResponsesStreamEvent(value map[string]any) bool {
	eventType := stringField(value, "type")
	common := func(fields ...string) bool {
		return onlyKnownFields(value, append([]string{"type", "sequence_number"}, fields...)...)
	}
	stringEvent := func(field string) bool {
		return common("item_id", "output_index", "content_index", field, "logprobs", "obfuscation") && requiredStringField(value, field)
	}
	switch eventType {
	case "response.created", "response.in_progress", "response.queued":
		if !common("response") {
			return false
		}
		response := nestedObject(value, "response")
		if len(response) == 0 {
			return false
		}
		return billableResponsesEnvelope(response, false)
	case "response.output_item.added", "response.output_item.done":
		return common("output_index", "item") && billableResponsesOutput([]any{value["item"]}, eventType == "response.output_item.done")
	case "response.content_part.added", "response.content_part.done":
		return common("item_id", "output_index", "content_index", "part") && billableResponsesContent([]any{value["part"]})
	case "response.output_text.delta", "response.refusal.delta",
		"response.function_call_arguments.delta", "response.custom_tool_call_input.delta",
		"response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		return stringEvent("delta")
	case "response.output_text.done", "response.reasoning_summary_text.done", "response.reasoning_text.done":
		return stringEvent("text")
	case "response.refusal.done":
		return stringEvent("refusal")
	case "response.function_call_arguments.done":
		return stringEvent("arguments")
	case "response.custom_tool_call_input.done":
		return stringEvent("input")
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		if !common("item_id", "output_index", "summary_index", "part") {
			return false
		}
		part, ok := value["part"].(map[string]any)
		return ok && billableResponsesContent([]any{part})
	case "response.completed":
		if !common("response") {
			return false
		}
		response := nestedObject(value, "response")
		return billableResponsesEnvelope(response, true)
	default:
		return false
	}
}

func responseEnvelopeError(protocol channel.Protocol, value map[string]any) *UpstreamResponseError {
	if errorValue, ok := value["error"].(map[string]any); ok && len(errorValue) > 0 {
		code, message := errorDetailsFromValue(value, errorValue)
		return &UpstreamResponseError{Code: code, Message: message}
	}
	if protocol == channel.ProtocolOpenAIResponse {
		status := stringField(value, "status")
		if status == "failed" || status == "incomplete" || status == "cancelled" {
			code, message := errorDetailsFromValue(value, nestedObject(value, "error"))
			return &UpstreamResponseError{Code: coalesce(code, "upstream_"+status), Message: message}
		}
	}
	return nil
}

func streamingResponseError(protocol channel.Protocol, value map[string]any) *UpstreamResponseError {
	if responseErr := responseEnvelopeError(protocol, value); responseErr != nil {
		return responseErr
	}
	switch protocol {
	case channel.ProtocolOpenAIResponse:
		eventType := stringField(value, "type")
		if eventType == "error" || eventType == "response.failed" || eventType == "response.incomplete" {
			errorValue := nestedObject(value, "error")
			if response := nestedObject(value, "response"); len(errorValue) == 0 {
				errorValue = nestedObject(response, "error")
			}
			code, message := errorDetailsFromValue(value, errorValue)
			return &UpstreamResponseError{Code: coalesce(code, "upstream_stream_error"), Message: message}
		}
	case channel.ProtocolAnthropic:
		if stringField(value, "type") == "error" {
			code, message := errorDetailsFromValue(value, nestedObject(value, "error"))
			return &UpstreamResponseError{Code: coalesce(code, "upstream_stream_error"), Message: message}
		}
	}
	return nil
}

func errorDetailsFromValue(value, errorValue map[string]any) (string, string) {
	code := "upstream_stream_error"
	for _, key := range []string{"code", "type", "status"} {
		if candidate := stringField(errorValue, key); candidate != "" {
			code = candidate
			break
		}
		if candidate := stringField(value, key); candidate != "" && candidate != "error" {
			code = candidate
			break
		}
	}
	message := coalesceErrorMessage(errorValue)
	if candidate := stringField(value, "message"); candidate != "" && message == "upstream returned a non-success HTTP response" {
		message = candidate
	}
	return code, message
}

func nestedObject(value map[string]any, key string) map[string]any {
	result, _ := value[key].(map[string]any)
	return result
}

func UpstreamErrorCode(protocol channel.Protocol, raw []byte) string {
	code, _ := UpstreamErrorDetails(protocol, raw)
	return code
}

func UpstreamErrorDetails(protocol channel.Protocol, raw []byte) (string, string) {
	value, err := decodeJSONObject(raw)
	if err != nil {
		return "upstream_http_error", "upstream returned a non-success HTTP response"
	}
	errorValue, _ := value["error"].(map[string]any)
	if protocol == channel.ProtocolAnthropic && errorValue == nil {
		errorValue, _ = value["error"].(map[string]any)
	}
	for _, key := range []string{"code", "type", "status"} {
		if candidate, ok := errorValue[key].(string); ok && strings.TrimSpace(candidate) != "" {
			return candidate, coalesceErrorMessage(errorValue)
		}
		if candidate, ok := value[key].(string); ok && strings.TrimSpace(candidate) != "" {
			return candidate, coalesceErrorMessage(errorValue)
		}
	}
	return "upstream_http_error", coalesceErrorMessage(errorValue)
}

func coalesceErrorMessage(errorValue map[string]any) string {
	if message, ok := errorValue["message"].(string); ok && strings.TrimSpace(message) != "" {
		return message
	}
	return "upstream returned a non-success HTTP response"
}

func decodeJSONObject(encoded []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrInvalidInput
	}
	if value == nil {
		return nil, ErrInvalidInput
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, ErrInvalidInput
	}
	return value, nil
}

func rewriteModelMetadata(protocol channel.Protocol, value map[string]any, canonicalModelID string) {
	switch protocol {
	case channel.ProtocolOpenAIChat:
		if _, exists := value["model"]; exists {
			value["model"] = canonicalModelID
		}
	case channel.ProtocolOpenAIResponse:
		if _, exists := value["model"]; exists {
			value["model"] = canonicalModelID
		}
		if response, ok := value["response"].(map[string]any); ok {
			if _, exists := response["model"]; exists {
				response["model"] = canonicalModelID
			}
		}
	case channel.ProtocolAnthropic:
		if _, exists := value["model"]; exists {
			value["model"] = canonicalModelID
		}
		if message, ok := value["message"].(map[string]any); ok {
			if _, exists := message["model"]; exists {
				message["model"] = canonicalModelID
			}
		}
	case channel.ProtocolGemini:
		if _, exists := value["modelVersion"]; exists {
			value["modelVersion"] = canonicalModelID
		}
	}
}

func usageObservation(protocol channel.Protocol, value map[string]any) (UsageObservation, bool) {
	switch protocol {
	case channel.ProtocolOpenAIChat:
		usage, ok := value["usage"].(map[string]any)
		if !ok {
			return UsageObservation{}, false
		}
		return openAIUsage(usage, "prompt_tokens", "completion_tokens", "prompt_tokens_details")
	case channel.ProtocolOpenAIResponse:
		usage, ok := value["usage"].(map[string]any)
		if !ok {
			if response, responseOK := value["response"].(map[string]any); responseOK {
				usage, ok = response["usage"].(map[string]any)
			}
		}
		if !ok {
			return UsageObservation{}, false
		}
		return openAIUsage(usage, "input_tokens", "output_tokens", "input_tokens_details")
	case channel.ProtocolAnthropic:
		usage, ok := value["usage"].(map[string]any)
		if !ok {
			if message, messageOK := value["message"].(map[string]any); messageOK {
				usage, ok = message["usage"].(map[string]any)
			}
		}
		if !ok {
			return UsageObservation{}, false
		}
		if !anthropicUsageBillable(usage) {
			return UsageObservation{}, false
		}
		observation := UsageObservation{}
		if input, found := intField(usage, "input_tokens"); found {
			observation.InputTokens = intPointer(input)
			cacheWrite, cacheWriteOK := intFieldDefault(usage, "cache_creation_input_tokens", 0)
			cacheRead, cacheReadOK := intFieldDefault(usage, "cache_read_input_tokens", 0)
			if !cacheWriteOK || !cacheReadOK {
				return UsageObservation{}, false
			}
			observation.CacheWriteTokens = intPointer(cacheWrite)
			observation.CacheReadTokens = intPointer(cacheRead)
		}
		if output, found := intField(usage, "output_tokens"); found {
			observation.OutputTokens = intPointer(output)
		}
		return observation, observation.InputTokens != nil || observation.OutputTokens != nil
	case channel.ProtocolGemini:
		usage, ok := value["usageMetadata"].(map[string]any)
		if !ok {
			return UsageObservation{}, false
		}
		if !geminiUsageBillable(usage) {
			return UsageObservation{}, false
		}
		prompt, promptOK := intField(usage, "promptTokenCount")
		output, outputOK := intFieldDefault(usage, "candidatesTokenCount", 0)
		thoughts, thoughtsOK := intFieldDefault(usage, "thoughtsTokenCount", 0)
		toolUse, toolUseOK := intFieldDefault(usage, "toolUsePromptTokenCount", 0)
		cached, cachedOK := intFieldDefault(usage, "cachedContentTokenCount", 0)
		if !promptOK || !outputOK || !thoughtsOK || !toolUseOK || !cachedOK || prompt < cached ||
			output > math.MaxInt64-thoughts || prompt-cached > math.MaxInt64-toolUse {
			return UsageObservation{}, false
		}
		unpartitionedTotal, totalOK := addNonnegativeTokens(prompt, output, toolUse, thoughts)
		if !totalOK {
			return UsageObservation{}, false
		}
		if _, exists := usage["totalTokenCount"]; exists {
			total, valid := intField(usage, "totalTokenCount")
			if !valid || total != unpartitionedTotal {
				return UsageObservation{}, false
			}
		}
		output += thoughts
		zero := int64(0)
		input := prompt - cached + toolUse
		return UsageObservation{
			InputTokens: &input, OutputTokens: &output, CacheWriteTokens: &zero, CacheReadTokens: &cached,
		}, true
	default:
		return UsageObservation{}, false
	}
}

func addNonnegativeTokens(values ...int64) (int64, bool) {
	total := int64(0)
	for _, value := range values {
		if value < 0 || total > math.MaxInt64-value {
			return 0, false
		}
		total += value
	}
	return total, true
}

func openAIUsage(usage map[string]any, inputKey, outputKey, detailKey string) (UsageObservation, bool) {
	outputDetailKey := "output_tokens_details"
	if outputKey == "completion_tokens" {
		outputDetailKey = "completion_tokens_details"
	}
	if !onlyKnownFields(usage, inputKey, outputKey, "total_tokens", detailKey, outputDetailKey) {
		return UsageObservation{}, false
	}
	inputTotal, inputOK := intField(usage, inputKey)
	output, outputOK := intField(usage, outputKey)
	if !inputOK || !outputOK {
		return UsageObservation{}, false
	}
	combined, combinedOK := addNonnegativeTokens(inputTotal, output)
	if !combinedOK {
		return UsageObservation{}, false
	}
	if _, exists := usage["total_tokens"]; exists {
		total, valid := intField(usage, "total_tokens")
		if !valid || total != combined {
			return UsageObservation{}, false
		}
	}
	cached := int64(0)
	cacheWrite := int64(0)
	if details, ok := usage[detailKey].(map[string]any); ok {
		if !onlyKnownFields(details, "cached_tokens", "cache_write_tokens", "audio_tokens") || !zeroOptionalTokenField(details, "audio_tokens") {
			return UsageObservation{}, false
		}
		var cachedOK, cacheWriteOK bool
		cached, cachedOK = intFieldDefault(details, "cached_tokens", 0)
		cacheWrite, cacheWriteOK = intFieldDefault(details, "cache_write_tokens", 0)
		if !cachedOK || !cacheWriteOK {
			return UsageObservation{}, false
		}
	} else if _, exists := usage[detailKey]; exists {
		return UsageObservation{}, false
	}
	if details, ok := usage[outputDetailKey].(map[string]any); ok {
		if !onlyKnownFields(details, "reasoning_tokens", "audio_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens") ||
			!nonnegativeOptionalTokenField(details, "reasoning_tokens") || !zeroOptionalTokenField(details, "audio_tokens") ||
			!nonnegativeOptionalTokenField(details, "accepted_prediction_tokens") || !nonnegativeOptionalTokenField(details, "rejected_prediction_tokens") {
			return UsageObservation{}, false
		}
	} else if _, exists := usage[outputDetailKey]; exists {
		return UsageObservation{}, false
	}
	partitioned, partitionedOK := addNonnegativeTokens(cached, cacheWrite)
	if !partitionedOK || inputTotal < partitioned {
		return UsageObservation{}, false
	}
	input := inputTotal - partitioned
	return UsageObservation{
		InputTokens: &input, OutputTokens: &output, CacheWriteTokens: &cacheWrite, CacheReadTokens: &cached,
	}, true
}

func anthropicUsageBillable(usage map[string]any) bool {
	if !onlyKnownFields(usage, "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "cache_creation", "server_tool_use", "service_tier", "inference_geo") {
		return false
	}
	if cacheCreation, exists := usage["cache_creation"]; exists && cacheCreation != nil {
		details, ok := cacheCreation.(map[string]any)
		if !ok || !onlyKnownFields(details, "ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens") ||
			!nonnegativeOptionalTokenField(details, "ephemeral_5m_input_tokens") || !zeroOptionalTokenField(details, "ephemeral_1h_input_tokens") {
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
	if !serviceTierAllowed(usage, "service_tier", "standard", "standard_only") || !anthropicInferenceGeo(usage) {
		return false
	}
	return true
}

func geminiUsageBillable(usage map[string]any) bool {
	if !onlyKnownFields(usage,
		"promptTokenCount", "candidatesTokenCount", "totalTokenCount", "cachedContentTokenCount",
		"toolUsePromptTokenCount", "thoughtsTokenCount", "promptTokensDetails", "cacheTokensDetails",
		"candidatesTokensDetails", "toolUsePromptTokensDetails", "serviceTier") ||
		!serviceTierAllowed(usage, "serviceTier", "unspecified", "standard") {
		return false
	}
	detailTotals := map[string]string{
		"promptTokensDetails":        "promptTokenCount",
		"cacheTokensDetails":         "cachedContentTokenCount",
		"candidatesTokensDetails":    "candidatesTokenCount",
		"toolUsePromptTokensDetails": "toolUsePromptTokenCount",
	}
	for field, totalField := range detailTotals {
		raw, exists := usage[field]
		if !exists || raw == nil {
			continue
		}
		details, ok := raw.([]any)
		if !ok {
			return false
		}
		sum := int64(0)
		for _, rawDetail := range details {
			detail, ok := rawDetail.(map[string]any)
			if !ok || !onlyKnownFields(detail, "modality", "tokenCount") {
				return false
			}
			count, valid := intField(detail, "tokenCount")
			if !valid || sum > math.MaxInt64-count || (strings.ToUpper(stringField(detail, "modality")) != "TEXT" && count != 0) {
				return false
			}
			sum += count
		}
		total, valid := intFieldDefault(usage, totalField, 0)
		if !valid || sum != total {
			return false
		}
	}
	return true
}

func allZeroTokenFields(value map[string]any) bool {
	for key := range value {
		if !zeroOptionalTokenField(value, key) {
			return false
		}
	}
	return true
}

func nonnegativeOptionalTokenField(value map[string]any, key string) bool {
	if _, exists := value[key]; !exists {
		return true
	}
	parsed, ok := intField(value, key)
	return ok && parsed >= 0
}

func zeroOptionalTokenField(value map[string]any, key string) bool {
	if _, exists := value[key]; !exists {
		return true
	}
	parsed, ok := intField(value, key)
	return ok && parsed == 0
}

func streamingCredentialFragments(protocol channel.Protocol, value map[string]any) ([]SSECredentialFragment, error) {
	if protocol == channel.ProtocolOpenAIChat {
		choices, _ := value["choices"].([]any)
		for _, rawChoice := range choices {
			choice, _ := rawChoice.(map[string]any)
			delta := nestedObject(choice, "delta")
			calls, _ := delta["tool_calls"].([]any)
			for position, rawCall := range calls {
				call, _ := rawCall.(map[string]any)
				callIndex, ok := intField(call, "index")
				if !ok {
					callIndex = int64(position)
				}
				if callIndex < 0 || callIndex > MaxChatToolCallIndex {
					return nil, ErrResponseTooBig
				}
			}
		}
	}
	fragments := make([]SSECredentialFragment, 0, min(MaxSSECredentialStreams, 32))
	if err := appendJSONCredentialFragments(&fragments, string(protocol), value); err != nil {
		return nil, err
	}
	return fragments, nil
}

func appendJSONCredentialFragments(fragments *[]SSECredentialFragment, base string, raw any) error {
	appendFragment := func(key, text string) error {
		if text == "" {
			return nil
		}
		if len(*fragments) >= MaxSSECredentialStreams {
			return ErrResponseTooBig
		}
		*fragments = append(*fragments, SSECredentialFragment{StreamKey: key, Text: text})
		return nil
	}
	switch value := raw.(type) {
	case string:
		return appendFragment(base, value)
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for position, key := range keys {
			// Keys are untrusted strings too. Ordinal-by-parent keeps a split key
			// continuous across events without joining values from different paths.
			if err := appendFragment(base+"/@key/"+strconv.Itoa(position), key); err != nil {
				return err
			}
			if err := appendJSONCredentialFragments(fragments, base+"/"+credentialPathSegment(key), value[key]); err != nil {
				return err
			}
		}
	case []any:
		for index, item := range value {
			segment := strconv.Itoa(index)
			if object, ok := item.(map[string]any); ok {
				if itemIndex, valid := intField(object, "index"); valid {
					segment = "index=" + strconv.FormatInt(itemIndex, 10)
				} else {
					for _, identityKey := range []string{"id", "item_id", "event_id"} {
						if identityValue := stringField(object, identityKey); identityValue != "" {
							segment = identityKey + "=" + credentialPathSegment(identityValue)
							break
						}
					}
				}
			}
			if err := appendJSONCredentialFragments(fragments, base+"/["+segment+"]", item); err != nil {
				return err
			}
		}
	}
	return nil
}

func credentialPathSegment(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func semanticEvent(protocol channel.Protocol, value map[string]any) bool {
	switch protocol {
	case channel.ProtocolOpenAIChat:
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
	case channel.ProtocolOpenAIResponse:
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
	case channel.ProtocolAnthropic:
		eventType, _ := value["type"].(string)
		if eventType == "content_block_delta" {
			delta, _ := value["delta"].(map[string]any)
			return stringField(delta, "text") != "" || stringField(delta, "partial_json") != "" || stringField(delta, "thinking") != "" || stringField(delta, "signature") != ""
		}
		if eventType == "content_block_start" {
			block, _ := value["content_block"].(map[string]any)
			return stringField(block, "type") == "tool_use"
		}
	case channel.ProtocolGemini:
		candidates, _ := value["candidates"].([]any)
		for _, rawCandidate := range candidates {
			candidate, _ := rawCandidate.(map[string]any)
			content, _ := candidate["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, rawPart := range parts {
				part, _ := rawPart.(map[string]any)
				if stringField(part, "text") != "" {
					return true
				}
				if call, ok := part["functionCall"].(map[string]any); ok && len(call) > 0 {
					return true
				}
				for _, contentKey := range []string{"functionResponse"} {
					if content, ok := part[contentKey].(map[string]any); ok && len(content) > 0 {
						return true
					}
				}
			}
		}
	}
	return false
}

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

func terminalEvent(protocol channel.Protocol, value map[string]any) bool {
	switch protocol {
	case channel.ProtocolOpenAIChat:
		_, hasUsage := value["usage"]
		choices, _ := value["choices"].([]any)
		// A finish_reason closes only that choice. The stream itself enters its
		// bounded terminal wind-down at the usage frame and ends at [DONE].
		return hasUsage && len(choices) == 0
	case channel.ProtocolOpenAIResponse:
		return stringField(value, "type") == "response.completed"
	case channel.ProtocolAnthropic:
		eventType := stringField(value, "type")
		if eventType == "message_stop" {
			return true
		}
		return eventType == "message_delta" && value["usage"] != nil
	case channel.ProtocolGemini:
		if value["usageMetadata"] == nil {
			return false
		}
		candidates, _ := value["candidates"].([]any)
		for _, raw := range candidates {
			candidate, _ := raw.(map[string]any)
			if stringField(candidate, "finishReason") != "" {
				return true
			}
		}
	}
	return false
}

func finishObserved(protocol channel.Protocol, value map[string]any) bool {
	switch protocol {
	case channel.ProtocolOpenAIChat:
		choices, _ := value["choices"].([]any)
		for _, raw := range choices {
			choice, _ := raw.(map[string]any)
			if stringField(choice, "finish_reason") != "" {
				return true
			}
		}
	case channel.ProtocolOpenAIResponse:
		if stringField(value, "type") != "response.completed" {
			return false
		}
		response := nestedObject(value, "response")
		return billableResponsesEnvelope(response, true) && responseEnvelopeError(protocol, response) == nil
	case channel.ProtocolAnthropic:
		return stringField(value, "type") == "message_delta" && stringField(nestedObject(value, "delta"), "stop_reason") != ""
	case channel.ProtocolGemini:
		candidates, _ := value["candidates"].([]any)
		if len(candidates) == 0 {
			return false
		}
		for _, raw := range candidates {
			candidate, _ := raw.(map[string]any)
			if stringField(candidate, "finishReason") == "" {
				return false
			}
		}
		return true
	}
	return false
}

func chatChoiceProgress(value map[string]any) (observed, finished []int) {
	choices, _ := value["choices"].([]any)
	for position, raw := range choices {
		choice, _ := raw.(map[string]any)
		index, ok := intField(choice, "index")
		if !ok {
			index = int64(position)
		}
		observed = append(observed, int(index))
		if stringField(choice, "finish_reason") != "" {
			finished = append(finished, int(index))
		}
	}
	return observed, finished
}

func afterTerminalAllowed(protocol channel.Protocol, value map[string]any) bool {
	return protocol == channel.ProtocolAnthropic && stringField(value, "type") == "ping"
}

func streamEndEvent(protocol channel.Protocol, value map[string]any) bool {
	switch protocol {
	case channel.ProtocolOpenAIResponse:
		return finishObserved(protocol, value)
	case channel.ProtocolAnthropic:
		return stringField(value, "type") == "message_stop"
	case channel.ProtocolGemini:
		return terminalEvent(protocol, value) && finishObserved(protocol, value)
	default:
		return false
	}
}

func splitSSEData(frame []byte) (data []byte, eventName string, ok bool, err error) {
	lines := bytes.Split(frame, []byte("\n"))
	dataParts := make([][]byte, 0)
	for _, line := range lines {
		trimmed := bytes.TrimSuffix(line, []byte("\r"))
		switch {
		case len(trimmed) == 0, bytes.HasPrefix(trimmed, []byte(":")):
			continue
		case bytes.HasPrefix(trimmed, []byte("data:")):
			part := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
			dataParts = append(dataParts, part)
		case bytes.HasPrefix(trimmed, []byte("event:")):
			if eventName != "" {
				return nil, "", false, ErrInvalidInput
			}
			eventName = strings.TrimSpace(string(bytes.TrimPrefix(trimmed, []byte("event:"))))
			if eventName == "" || strings.ContainsAny(eventName, "\r\n") {
				return nil, "", false, ErrInvalidInput
			}
		case bytes.HasPrefix(trimmed, []byte("id:")), bytes.HasPrefix(trimmed, []byte("retry:")):
			// Transport metadata and comments are deliberately not forwarded. They
			// are not part of the protocol billing contract and otherwise create a
			// second unstructured credential-echo channel.
			continue
		default:
			return nil, "", false, ErrInvalidInput
		}
	}
	if len(dataParts) == 0 {
		return nil, eventName, false, nil
	}
	return bytes.Join(dataParts, []byte("\n")), eventName, true, nil
}

func intField(value map[string]any, key string) (int64, bool) {
	raw, exists := value[key]
	if !exists {
		return 0, false
	}
	switch typed := raw.(type) {
	case json.Number:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		return parsed, err == nil && parsed >= 0
	case float64:
		parsed := int64(typed)
		return parsed, float64(parsed) == typed && parsed >= 0
	case int64:
		return typed, typed >= 0
	default:
		return 0, false
	}
}

func intFieldDefault(value map[string]any, key string, fallback int64) (int64, bool) {
	if _, exists := value[key]; !exists {
		return fallback, true
	}
	return intField(value, key)
}

func intPointer(value int64) *int64 { return &value }

func stringField(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}
