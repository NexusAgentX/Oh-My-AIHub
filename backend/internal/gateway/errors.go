package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/secretguard"
)

type UpstreamResponseError struct {
	Code    string
	Message string
}

func (e *UpstreamResponseError) Error() string {
	return e.Code + ": " + e.Message
}

// validateSuccessfulResponse only verifies that the upstream body is a native
// protocol success object the gateway can settle: billing integrity comes from
// the extracted four-bucket usage, not from policing response fields. Unknown
// fields pass through to the client untouched.
// validateSuccessfulResponse reports explicit upstream failure envelopes only.
// Response shape is deliberately not validated: an upstream that omits or
// renames a field this platform does not bill on must not fail an otherwise
// usable call.
func validateSuccessfulResponse(protocol channel.Protocol, value map[string]any) *UpstreamResponseError {
	return responseEnvelopeError(protocol, value)
}

func responseEnvelopeError(protocol channel.Protocol, value map[string]any) *UpstreamResponseError {
	return adapterEnvelopeError(adapterFor(protocol), value)
}

func adapterEnvelopeError(adapter protocolAdapter, value map[string]any) *UpstreamResponseError {
	if errorValue, ok := value["error"].(map[string]any); ok && len(errorValue) > 0 {
		code, message := errorDetailsFromValue(value, errorValue)
		return &UpstreamResponseError{Code: code, Message: message}
	}
	return adapter.envelopeError(value)
}

func streamingResponseError(protocol channel.Protocol, value map[string]any) *UpstreamResponseError {
	adapter := adapterFor(protocol)
	if responseErr := adapterEnvelopeError(adapter, value); responseErr != nil {
		return responseErr
	}
	return adapter.streamError(value)
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

func normalizeUpstreamErrorBody(protocol channel.Protocol, raw []byte, credentials ...string) ([]byte, bool, bool) {
	if len(raw) == 0 {
		return nil, false, false
	}
	if secretguard.ContainsExactOrJSONEscaped(string(raw), credentials...) || secretguard.ContainsExactInJSON(raw, credentials...) {
		return nil, false, true
	}
	value, err := decodeJSONObject(raw)
	if err != nil || responseEnvelopeError(protocol, value) == nil {
		return nil, false, false
	}
	if !adapterFor(protocol).acceptsUpstreamErrorBody(value) {
		return nil, false, false
	}
	encoded, err := marshalJSONObjectWithin(value, MaxUpstreamErrorBytes)
	if err != nil {
		return nil, false, false
	}
	if secretguard.ContainsExactOrJSONEscaped(string(encoded), credentials...) || secretguard.ContainsExactInJSON(encoded, credentials...) {
		return nil, false, true
	}
	return encoded, true, false
}

func writeProtocolError(w http.ResponseWriter, protocol channel.Protocol, status int, code, message, callID string) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(ProtocolErrorWriteTimeout))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(protocolErrorPayload(protocol, status, code, message, callID))
}

// WriteProtocolError exposes the SDK-compatible error envelope to the HTTP routing layer.
func WriteProtocolError(w http.ResponseWriter, protocol channel.Protocol, status int, code, message, callID string) {
	writeProtocolError(w, protocol, status, code, message, callID)
}

func protocolErrorPayload(protocol channel.Protocol, status int, code, message, callID string) any {
	return adapterFor(protocol).errorPayload(status, code, message, callID)
}

func protocolSSEErrorFrame(protocol channel.Protocol, status int, code, message, callID string) []byte {
	payload, _ := json.Marshal(protocolErrorPayload(protocol, status, code, message, callID))
	return adapterFor(protocol).sseErrorFrame(payload)
}
