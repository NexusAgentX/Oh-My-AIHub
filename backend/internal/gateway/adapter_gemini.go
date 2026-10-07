package gateway

import (
	"math"
	"net/http"
	"net/url"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// geminiAdapter implements Gemini GenerateContent. The model and the streaming
// flag live in the URL path rather than the body, so the request body is
// forwarded unmodified.
type geminiAdapter struct{ baseAdapter }

func (geminiAdapter) modelInBody() bool { return false }

func (geminiAdapter) platformCredentialHeader() string { return "x-goog-api-key" }

func (geminiAdapter) setUpstreamAuthentication(header http.Header, credential string) {
	header.Set("x-goog-api-key", credential)
}

// adjustUpstreamQuery keeps Gemini streaming on its mandated alt=sse.
func (geminiAdapter) adjustUpstreamQuery(query url.Values, stream bool) {
	if stream {
		query.Set("alt", "sse")
	}
}

func (geminiAdapter) rewriteModelMetadata(value map[string]any, canonicalModelID string) {
	if _, exists := value["modelVersion"]; exists {
		value["modelVersion"] = canonicalModelID
	}
}

func (geminiAdapter) usageObservation(value map[string]any) (UsageObservation, UsageState) {
	usage, ok := value["usageMetadata"].(map[string]any)
	if !ok {
		return UsageObservation{}, missingUsageState(value, "usageMetadata")
	}
	if !geminiUsageBillable(usage) {
		return UsageObservation{}, UsageInvalid
	}
	prompt, promptOK := intField(usage, "promptTokenCount")
	output, outputOK := intFieldDefault(usage, "candidatesTokenCount", 0)
	thoughts, thoughtsOK := intFieldDefault(usage, "thoughtsTokenCount", 0)
	toolUse, toolUseOK := intFieldDefault(usage, "toolUsePromptTokenCount", 0)
	cached, cachedOK := intFieldDefault(usage, "cachedContentTokenCount", 0)
	if !promptOK || !outputOK || !thoughtsOK || !toolUseOK || !cachedOK || prompt < cached ||
		output > math.MaxInt64-thoughts || prompt-cached > math.MaxInt64-toolUse {
		return UsageObservation{}, UsageInvalid
	}
	unpartitionedTotal, totalOK := addNonnegativeTokens(prompt, output, toolUse, thoughts)
	if !totalOK {
		return UsageObservation{}, UsageInvalid
	}
	if _, exists := usage["totalTokenCount"]; exists {
		total, valid := intField(usage, "totalTokenCount")
		if !valid || total != unpartitionedTotal {
			return UsageObservation{}, UsageInvalid
		}
	}
	output += thoughts
	zero := int64(0)
	input := prompt - cached + toolUse
	return UsageObservation{
		InputTokens: &input, OutputTokens: &output, CacheWriteTokens: &zero, CacheReadTokens: &cached,
	}, UsageValid
}

func (geminiAdapter) semanticEvent(value map[string]any) bool {
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
	return false
}

func (geminiAdapter) terminalEvent(value map[string]any) bool {
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
	return false
}

func (geminiAdapter) finishObserved(value map[string]any) bool {
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

func (a geminiAdapter) streamEndEvent(value map[string]any) bool {
	return a.terminalEvent(value) && a.finishObserved(value)
}

func (geminiAdapter) errorPayload(status int, code, message, callID string) any {
	errorValue := map[string]any{"code": status, "message": message, "status": strings.ToUpper(code)}
	if callID != "" {
		errorValue["call_id"] = callID
	}
	return map[string]any{"error": errorValue}
}

func ParseGeminiRequestPath(r *http.Request) (canonicalModelID string, stream bool, err error) {
	escaped := r.URL.EscapedPath()
	if escaped == "" {
		escaped = r.URL.Path
	}
	lower := strings.ToLower(escaped)
	if strings.Contains(lower, "%") || strings.Contains(escaped, "\\") || !strings.HasPrefix(escaped, "/v1beta/models/") {
		return "", false, ErrInvalidInput
	}
	remaining := strings.TrimPrefix(escaped, "/v1beta/models/")
	operation := ":generateContent"
	if strings.HasSuffix(remaining, ":streamGenerateContent") {
		operation, stream = ":streamGenerateContent", true
	} else if !strings.HasSuffix(remaining, operation) {
		return "", false, ErrInvalidInput
	}
	model := strings.TrimSuffix(remaining, operation)
	if strings.Contains(model, ":") || !validCanonicalModelID(model) {
		return "", false, ErrInvalidInput
	}
	if err := validateProtocolQuery(channel.ProtocolGemini, stream, r.URL.RawQuery); err != nil {
		return "", false, err
	}
	return model, stream, nil
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
