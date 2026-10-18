package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Dialect is the error and model-list shape a client expects: the platform
// answers its own errors the way the client's official SDK parses them.
type Dialect int

const (
	DialectOpenAI Dialect = iota
	DialectAnthropic
	DialectGemini
)

// DialectOf infers the client's protocol from the request: Gemini lives under
// /v1beta/, Anthropic is /v1/messages or any request carrying
// anthropic-version (Anthropic SDKs always send it), OpenAI is the rest.
func DialectOf(r *http.Request) Dialect {
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1beta/") || r.URL.Path == "/v1beta":
		return DialectGemini
	case (r.URL.Path == "/v1/models" || strings.HasPrefix(r.URL.Path, "/v1/models/")) && (r.Header.Get("x-goog-api-key") != "" || r.URL.Query().Get("key") != ""):
		return DialectGemini
	case r.URL.Path == "/v1/messages" || strings.HasPrefix(r.URL.Path, "/v1/messages/") || r.Header.Get("anthropic-version") != "":
		return DialectAnthropic
	}
	return DialectOpenAI
}

// WriteError answers a platform error in the client's dialect. code is the
// platform error code (snake_case); the HTTP status is the same for every
// dialect.
func WriteError(w http.ResponseWriter, dialect Dialect, status int, code, message, requestID string) {
	if requestID != "" {
		w.Header().Set("X-AIHub-Request-Id", requestID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody(dialect, status, code, message, requestID))
}

// errorBody follows each vendor's documented error shape:
//   - OpenAI: {"error":{"message","type","param","code"}}
//   - Anthropic: {"type":"error","error":{"type","message"},"request_id"}
//   - Gemini (AIP-193): {"error":{"code","message","status","details":[ErrorInfo]}}
func errorBody(dialect Dialect, status int, code, message, requestID string) map[string]any {
	switch dialect {
	case DialectAnthropic:
		body := map[string]any{"type": "error", "error": map[string]any{"type": anthropicErrorType(status), "message": message}}
		if requestID != "" {
			body["request_id"] = requestID
		}
		return body
	case DialectGemini:
		info := map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": strings.ToUpper(code), "domain": "oh-my-aihub"}
		if requestID != "" {
			info["metadata"] = map[string]string{"request_id": requestID}
		}
		return map[string]any{"error": map[string]any{"code": status, "message": message, "status": googleStatus(status), "details": []any{info}}}
	}
	// OpenAI bodies carry nothing but "error"; the request ID stays in the header.
	return map[string]any{"error": map[string]any{"message": message, "type": openAIErrorType(status), "param": nil, "code": code}}
}

func openAIErrorType(status int) string {
	switch {
	case status == http.StatusPaymentRequired:
		return "insufficient_quota"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status == http.StatusServiceUnavailable:
		return "service_unavailable_error"
	case status >= 500:
		return "server_error"
	}
	return "invalid_request_error"
}

// anthropicErrorType maps statuses the way Anthropic's error reference does.
func anthropicErrorType(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusPaymentRequired:
		return "billing_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusConflict:
		return "conflict_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusGatewayTimeout:
		return "timeout_error"
	case http.StatusServiceUnavailable, 529:
		return "overloaded_error"
	}
	if status >= 500 {
		return "api_error"
	}
	return "invalid_request_error"
}

// googleStatus is the google.rpc.Code name for an HTTP status.
func googleStatus(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "ABORTED"
	case http.StatusPaymentRequired, http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case 499:
		return "CANCELLED"
	case http.StatusNotImplemented:
		return "UNIMPLEMENTED"
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	}
	if status >= 500 {
		return "INTERNAL"
	}
	return "INVALID_ARGUMENT"
}
