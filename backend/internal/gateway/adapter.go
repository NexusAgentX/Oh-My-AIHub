package gateway

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

// protocolAdapter isolates everything that differs between the four native
// protocols: credential placement, request rewriting, usage extraction, SSE
// event classification and the error envelope. The proxy orchestrates
// candidates, the commit point, fallback and settlement through this interface
// and never branches on a protocol itself.
//
// Adapters are stateless and safe for concurrent use. Unknown protocols resolve
// to baseAdapter, whose defaults deliver without observing anything.
type protocolAdapter interface {
	// Credentials and headers.
	platformCredentialHeader() string
	setUpstreamAuthentication(header http.Header, credential string)
	// defaultRequestHeaders fills protocol-mandated headers the client left out.
	defaultRequestHeaders(header http.Header)

	// Request.
	// modelInBody reports whether the canonical model id and the stream flag
	// travel in the JSON body; Gemini carries both in the URL path instead and
	// forwards the body untouched.
	modelInBody() bool
	expectedChoices(request map[string]any) (int, bool)
	rewriteRequest(request map[string]any, stream bool)
	adjustUpstreamQuery(query url.Values, stream bool)
	// maxTerminalFrames bounds the terminal wind-down buffer of one stream.
	maxTerminalFrames(expectedChoices int) int

	// Response, shared by non-streaming bodies and SSE frames.
	rewriteModelMetadata(value map[string]any, canonicalModelID string)
	usageObservation(value map[string]any) (UsageObservation, UsageState)
	// envelopeError reports protocol-specific failure envelopes beyond the
	// generic top-level error object.
	envelopeError(value map[string]any) *UpstreamResponseError

	// SSE.
	streamError(value map[string]any) *UpstreamResponseError
	eventNameMatches(eventName, dataType string) bool
	semanticEvent(value map[string]any) bool
	terminalEvent(value map[string]any) bool
	finishObserved(value map[string]any) bool
	streamEndEvent(value map[string]any) bool
	// doneSentinelEndsStream reports whether a literal `[DONE]` data frame is a
	// protocol terminator rather than an opaque relay frame.
	doneSentinelEndsStream() bool

	// Errors written by the platform itself.
	errorPayload(status int, code, message, callID string) any
	sseErrorFrame(payload []byte) []byte
	// acceptsUpstreamErrorBody reports whether a decoded upstream error body has
	// the native error shape and may be forwarded to the client.
	acceptsUpstreamErrorBody(value map[string]any) bool
}

// adapterFor returns the adapter of a protocol; unknown protocols get the
// inert baseAdapter.
func adapterFor(protocol channel.Protocol) protocolAdapter {
	switch protocol {
	case channel.ProtocolOpenAIChat:
		return chatAdapter{}
	case channel.ProtocolOpenAIResponse:
		return responsesAdapter{}
	case channel.ProtocolAnthropic:
		return anthropicAdapter{}
	case channel.ProtocolGemini:
		return geminiAdapter{}
	default:
		return baseAdapter{}
	}
}

// baseAdapter holds the OpenAI-style defaults. Protocol adapters embed it and
// override only what differs.
type baseAdapter struct{}

func (baseAdapter) platformCredentialHeader() string { return "Authorization" }

func (baseAdapter) setUpstreamAuthentication(header http.Header, credential string) {
	header.Set("Authorization", "Bearer "+credential)
}

func (baseAdapter) defaultRequestHeaders(http.Header) {}

func (baseAdapter) modelInBody() bool { return true }

func (baseAdapter) expectedChoices(map[string]any) (int, bool) { return 0, true }

func (baseAdapter) rewriteRequest(map[string]any, bool) {}

func (baseAdapter) adjustUpstreamQuery(url.Values, bool) {}

func (baseAdapter) maxTerminalFrames(int) int { return MaxTerminalFrames }

func (baseAdapter) rewriteModelMetadata(map[string]any, string) {}

func (baseAdapter) usageObservation(map[string]any) (UsageObservation, UsageState) {
	return UsageObservation{}, UsageAbsent
}

func (baseAdapter) envelopeError(map[string]any) *UpstreamResponseError { return nil }

func (baseAdapter) streamError(map[string]any) *UpstreamResponseError { return nil }

func (baseAdapter) eventNameMatches(string, string) bool { return false }

func (baseAdapter) semanticEvent(map[string]any) bool { return false }

func (baseAdapter) terminalEvent(map[string]any) bool { return false }

func (baseAdapter) finishObserved(map[string]any) bool { return false }

func (baseAdapter) streamEndEvent(map[string]any) bool { return false }

func (baseAdapter) doneSentinelEndsStream() bool { return false }

func (baseAdapter) errorPayload(_ int, code, message, callID string) any {
	errorValue := map[string]any{"message": message, "type": code, "code": code}
	if callID != "" {
		errorValue["call_id"] = callID
	}
	return map[string]any{"error": errorValue}
}

func (baseAdapter) sseErrorFrame(payload []byte) []byte {
	return append(append([]byte("data: "), payload...), []byte("\n\n")...)
}

func (baseAdapter) acceptsUpstreamErrorBody(map[string]any) bool { return true }

type ParsedRequest struct {
	CanonicalModelID string
	Stream           bool
	ExpectedChoices  int
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
	expectedChoices, valid := adapterFor(protocol).expectedChoices(value)
	if !valid {
		return ParsedRequest{}, ErrInvalidInput
	}
	return ParsedRequest{CanonicalModelID: model, Stream: stream, ExpectedChoices: expectedChoices}, nil
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
	adapterFor(protocol).rewriteRequest(value, stream)
	return marshalJSONObjectWithin(value, MaxRequestBytes)
}

// RewriteNonStreamingResponse restores the canonical model id on a successful
// upstream body and extracts its billable usage.
//
// Delivery and settlement are decoupled: a body the platform cannot price is
// still forwarded verbatim and reported as usage==nil, which the caller settles
// as an incomplete call with no charge. Only an explicit upstream error envelope
// or an oversized body fails the attempt.
func RewriteNonStreamingResponse(protocol channel.Protocol, body []byte, canonicalModelID string) ([]byte, *ledger.UsageV1, error) {
	value, err := decodeJSONObject(body)
	if err != nil {
		// Not a JSON object: pass the upstream bytes through unchanged.
		return body, nil, nil
	}
	if responseErr := validateSuccessfulResponse(protocol, value); responseErr != nil {
		return nil, nil, responseErr
	}
	adapter := adapterFor(protocol)
	adapter.rewriteModelMetadata(value, canonicalModelID)
	observation, state := adapter.usageObservation(value)
	var usage *ledger.UsageV1
	if state == UsageValid {
		if parsed, complete := observation.Complete(); complete {
			usage = parsed
		}
	}
	rewritten, err := marshalJSONObjectWithin(value, MaxNonStreamingBytes)
	if err != nil {
		return nil, nil, err
	}
	return rewritten, usage, nil
}

func AnalyzeSSEFrame(protocol channel.Protocol, frame []byte, canonicalModelID string) (SSEAnalysis, error) {
	adapter := adapterFor(protocol)
	analysis := SSEAnalysis{Frame: append([]byte(nil), frame...)}
	data, eventName, ok, envelopeErr := splitSSEData(frame)
	if envelopeErr != nil {
		return SSEAnalysis{}, envelopeErr
	}
	if !ok {
		// A frame without a data field (comment or vendor keepalive) carries no
		// billing signal; forward it untouched.
		return analysis, nil
	}
	if strings.TrimSpace(string(data)) == "[DONE]" {
		if adapter.doneSentinelEndsStream() {
			analysis.Terminal = true
			analysis.StreamEnd = true
			return analysis, nil
		}
		// No other protocol defines [DONE]; forward it untouched instead of
		// failing the stream on a relay-specific frame.
		return analysis, nil
	}
	value, err := decodeJSONObject(data)
	if err != nil {
		// Non-object payloads (vendor keepalives, plain text) are forwarded
		// untouched and never treated as semantic, terminal or usage.
		return analysis, nil
	}
	if responseErr := streamingResponseError(protocol, value); responseErr != nil {
		// An explicit error payload always fails the attempt. The event name is
		// deliberately not consulted: relays rename events, and a renamed
		// error must not degrade into a truncated stream.
		analysis.ErrorCode = responseErr.Code
		analysis.ErrorMessage = responseErr.Message
		analysis.Frame = rebuildSSEFrame(eventName, data)
		return analysis, nil
	}
	// Event names are observation only; a mismatch must not reject a frame.
	analysis.EventNameMismatch = !matchingSSEEventName(protocol, eventName, stringField(value, "type"))
	adapter.rewriteModelMetadata(value, canonicalModelID)
	analysis.Semantic = adapter.semanticEvent(value)
	analysis.CredentialFragments, err = streamingCredentialFragments(protocol, value)
	if err != nil {
		return SSEAnalysis{}, err
	}
	analysis.Terminal = adapter.terminalEvent(value)
	analysis.StreamEnd = adapter.streamEndEvent(value)
	observation, usageState := adapter.usageObservation(value)
	analysis.UsageState = usageState
	if usageState != UsageAbsent {
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
	return adapterFor(protocol).eventNameMatches(eventName, dataType)
}
