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

func RewriteRequest(protocol channel.Protocol, original []byte, upstreamModelID string, stream bool) ([]byte, error) {
	if !validProtocol(protocol) || strings.TrimSpace(upstreamModelID) == "" {
		return nil, ErrInvalidInput
	}
	value, err := decodeJSONObject(original)
	if err != nil {
		return nil, err
	}
	value["model"] = upstreamModelID
	if protocol == channel.ProtocolOpenAIChat && stream {
		options, _ := value["stream_options"].(map[string]any)
		if options == nil {
			options = make(map[string]any)
		}
		options["include_usage"] = true
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

// validateSuccessfulResponse only verifies that the upstream body is a native
// protocol success object the gateway can settle: billing integrity comes from
// the extracted four-bucket usage, not from policing response fields. Unknown
// fields pass through to the client untouched.
func validateSuccessfulResponse(protocol channel.Protocol, value map[string]any, expectedChoices int) *UpstreamResponseError {
	if responseErr := responseEnvelopeError(protocol, value); responseErr != nil {
		return responseErr
	}
	invalid := func() *UpstreamResponseError {
		return &UpstreamResponseError{Code: "invalid_upstream_response", Message: "upstream returned no protocol success object"}
	}
	switch protocol {
	case channel.ProtocolOpenAIChat:
		choices, ok := value["choices"].([]any)
		if !ok || expectedChoices <= 0 || len(choices) != expectedChoices || stringField(value, "object") != "chat.completion" ||
			stringField(value, "id") == "" {
			return invalid()
		}
		indexes := make(map[int64]struct{}, expectedChoices)
		for _, raw := range choices {
			choice, _ := raw.(map[string]any)
			index, indexOK := intField(choice, "index")
			if !indexOK || index < 0 || index >= int64(expectedChoices) {
				return invalid()
			}
			if _, duplicate := indexes[index]; duplicate {
				return invalid()
			}
			indexes[index] = struct{}{}
			if _, messageOK := choice["message"].(map[string]any); !messageOK || stringField(choice, "finish_reason") == "" {
				return invalid()
			}
		}
	case channel.ProtocolOpenAIResponse:
		if stringField(value, "object") != "response" || stringField(value, "status") != "completed" || stringField(value, "id") == "" {
			return invalid()
		}
		if rawOutput, exists := value["output"]; exists && rawOutput != nil {
			if _, ok := rawOutput.([]any); !ok {
				return invalid()
			}
		}
	case channel.ProtocolAnthropic:
		if _, ok := value["content"].([]any); !ok || stringField(value, "type") != "message" || stringField(value, "role") != "assistant" ||
			stringField(value, "stop_reason") == "" || stringField(value, "id") == "" {
			return invalid()
		}
	case channel.ProtocolGemini:
		candidates, ok := value["candidates"].([]any)
		if !ok || len(candidates) == 0 {
			return invalid()
		}
		for _, raw := range candidates {
			candidate, _ := raw.(map[string]any)
			if stringField(candidate, "finishReason") == "" {
				return invalid()
			}
		}
	default:
		return invalid()
	}
	return nil
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
	inputTotal, inputOK := intField(usage, inputKey)
	output, outputOK := intField(usage, outputKey)
	if !inputOK || !outputOK {
		return UsageObservation{}, false
	}
	cached := int64(0)
	cacheWrite := int64(0)
	if details, ok := usage[detailKey].(map[string]any); ok {
		// Audio/image tokens are billable dimensions the four-bucket formula
		// cannot price; anything else in the details object is ignored.
		if !zeroOptionalTokenField(details, "audio_tokens") || !zeroOptionalTokenField(details, "image_tokens") {
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
	reasoning := int64(0)
	if details, ok := usage[outputDetailKey].(map[string]any); ok {
		if !nonnegativeOptionalTokenField(details, "reasoning_tokens") || !zeroOptionalTokenField(details, "audio_tokens") ||
			!nonnegativeOptionalTokenField(details, "accepted_prediction_tokens") || !nonnegativeOptionalTokenField(details, "rejected_prediction_tokens") {
			return UsageObservation{}, false
		}
		var reasoningOK bool
		reasoning, reasoningOK = intFieldDefault(details, "reasoning_tokens", 0)
		if !reasoningOK {
			return UsageObservation{}, false
		}
	} else if _, exists := usage[outputDetailKey]; exists {
		return UsageObservation{}, false
	}
	visibleOutput := output
	billedOutput := output
	combinedVisible, combinedVisibleOK := addNonnegativeTokens(inputTotal, visibleOutput)
	if !combinedVisibleOK {
		return UsageObservation{}, false
	}
	if outputKey == "completion_tokens" && reasoning > 0 {
		combinedBilled, combinedBilledOK := addNonnegativeTokens(combinedVisible, reasoning)
		if !combinedBilledOK {
			return UsageObservation{}, false
		}
		if _, exists := usage["total_tokens"]; exists {
			total, valid := intField(usage, "total_tokens")
			if !valid {
				return UsageObservation{}, false
			}
			switch total {
			case combinedVisible:
			case combinedBilled:
				billedOutput, _ = addNonnegativeTokens(visibleOutput, reasoning)
			default:
				return UsageObservation{}, false
			}
		}
	} else if _, exists := usage["total_tokens"]; exists {
		total, valid := intField(usage, "total_tokens")
		if !valid || total != combinedVisible {
			return UsageObservation{}, false
		}
	}
	output = billedOutput
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

func geminiUsageBillable(usage map[string]any) bool {
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
			if !ok {
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
		return stringField(response, "status") == "completed" && responseEnvelopeError(protocol, response) == nil
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
