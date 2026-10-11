package gateway

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// ErrorCodeModelUnavailable is the attempt error code of an upstream 4xx that
// says the model does not exist or cannot be used (ADR-0035 rule 4).
const ErrorCodeModelUnavailable = "upstream_model_unavailable"

// modelUnavailableCodes are the error codes (lower case) with which OpenAI and
// its compatible relays say the model is missing.
var modelUnavailableCodes = map[string]bool{
	"model_not_found":     true,
	"invalid_model":       true,
	"model_not_available": true,
	"unsupported_model":   true,
}

// modelUnavailableMessage matches an error message that complains about the
// model itself. Messages about a parameter the model does not take ("'top_p'
// is not supported with this model") mention the model after the verdict, so
// they stay out.
var modelUnavailableMessage = regexp.MustCompile(
	`(?i)\bmodels?\b.{0,80}\b(?:not found|not exists?|doesn't exist|not supported|unsupported|not available|unavailable)\b` +
		`|\b(?:unsupported|unknown|invalid|no such) model\b|\bnot a valid model\b` +
		`|模型[^。！？.!?\n]{0,40}(?:不存在|不支持|无可用|不可用|未找到|找不到)`)

// ModelUnavailable reports whether an upstream error body says that the model
// does not exist or cannot be used. The gateway has already replaced the
// request's model with the channel's configured upstream name, so such an
// answer is a problem of the channel, not of the client's request.
//
// It looks at the structured error first: an OpenAI style error.code, or a
// Gemini error.status of NOT_FOUND. Failing that, it reads the error message
// with a narrow pattern. A body that is not a JSON error object never matches.
func ModelUnavailable(body []byte) bool {
	fields := errorFields(body)
	if fields == nil {
		return false
	}
	if code, ok := fields["code"].(string); ok && modelUnavailableCodes[strings.ToLower(code)] {
		return true
	}
	if status, ok := fields["status"].(string); ok && strings.EqualFold(status, "NOT_FOUND") {
		return true
	}
	// An OpenAI error that blames some other parameter is about that
	// parameter, whatever its message happens to quote.
	if param, ok := fields["param"].(string); ok && param != "" && param != "model" {
		return false
	}
	for _, key := range []string{"message", "detail"} {
		if message, ok := fields[key].(string); ok && modelUnavailableMessage.MatchString(truncate(message, maxErrorMessage)) {
			return true
		}
	}
	return false
}

// errorFields returns the object that carries the error's code, status and
// message: the "error" object of OpenAI, Anthropic and Gemini bodies (Gemini
// wraps it in an array when streaming), or, for relays that put them at the
// top, the body itself. An "error" that is only a string is the message.
func errorFields(body []byte) map[string]any {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || (body[0] != '{' && body[0] != '[') {
		return nil
	}
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		return nil
	}
	if list, ok := document.([]any); ok && len(list) > 0 {
		document = list[0]
	}
	root, ok := document.(map[string]any)
	if !ok {
		return nil
	}
	switch inner := root["error"].(type) {
	case map[string]any:
		return inner
	case string:
		return map[string]any{"message": inner}
	}
	return root
}
