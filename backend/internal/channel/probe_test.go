package channel

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestAuthenticationStatusWinsOverBodyReadAndSizeFailures(t *testing.T) {
	for _, test := range []struct {
		status   int
		tooLarge bool
		readErr  error
		want     ErrorCategory
	}{
		{http.StatusUnauthorized, true, nil, ErrorAuth},
		{http.StatusForbidden, false, errors.New("unexpected EOF"), ErrorAuth},
		{http.StatusInternalServerError, true, nil, ErrorTooLarge},
		{http.StatusOK, false, errors.New("unexpected EOF"), ErrorTransport},
	} {
		attempt, terminal := probeHTTPFailure(ValidationAttempt{HTTPStatus: test.status}, test.status, http.StatusText(test.status), []byte("upstream error"), test.tooLarge, test.readErr, time.Millisecond)
		if !terminal || attempt.ErrorCategory != test.want || attempt.HTTPStatus != test.status {
			t.Fatalf("status %d = %#v, terminal %v; want %s", test.status, attempt, terminal, test.want)
		}
	}
}

func TestProbePayloadsAndTerminalResponseValidation(t *testing.T) {
	protocols := []Protocol{ProtocolOpenAIChat, ProtocolOpenAIResponse, ProtocolAnthropic, ProtocolGemini}
	for _, protocol := range protocols {
		body, err := probeRequestBody(protocol, "model-id")
		if err != nil {
			t.Fatalf("probeRequestBody(%s): %v", protocol, err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		if protocol != ProtocolGemini && decoded["model"] != "model-id" {
			t.Fatalf("%s payload omitted model: %s", protocol, body)
		}
	}
	valid := map[Protocol]string{
		ProtocolOpenAIChat:     `{"choices":[{"finish_reason":"stop"}]}`,
		ProtocolOpenAIResponse: `{"status":"completed","output":[]}`,
		ProtocolAnthropic:      `{"type":"message","content":[],"stop_reason":"end_turn"}`,
		ProtocolGemini:         `{"candidates":[{"finishReason":"STOP"}]}`,
	}
	for protocol, body := range valid {
		if err := validateProbeResponse(protocol, []byte(body)); err != nil {
			t.Fatalf("valid %s response rejected: %v", protocol, err)
		}
	}
	invalid := map[Protocol]string{
		ProtocolOpenAIChat:     `{"choices":[{"finish_reason":""}]}`,
		ProtocolOpenAIResponse: `{"status":"in_progress","output":[]}`,
		ProtocolAnthropic:      `{"type":"message","content":[],"stop_reason":""}`,
		ProtocolGemini:         `{"candidates":[]}`,
	}
	for protocol, body := range invalid {
		if err := validateProbeResponse(protocol, []byte(body)); err == nil {
			t.Fatalf("invalid %s response accepted", protocol)
		}
	}
}

func TestFailedAttemptPreservesRawErrorAndByteLimit(t *testing.T) {
	raw := strings.Repeat("x", probeErrorLimit+10)
	attempt := failedAttemptWithDuration(ValidationAttempt{StartedAt: time.Now()}, ErrorUpstream, raw, time.Second)
	if attempt.Status != ValidationFailed || !attempt.RawErrorTruncated || len([]byte(attempt.RawError)) != probeErrorLimit || attempt.ErrorCategory != ErrorUpstream {
		t.Fatalf("failed attempt = %#v", attempt)
	}
}

func TestRawErrorNormalizationRemainsValidAndWithinByteLimit(t *testing.T) {
	raw := "bad\x00" + string([]byte{0xff}) + strings.Repeat("界", probeErrorLimit)
	normalized, changed := normalizeRawError(raw)
	if !changed || !utf8.ValidString(normalized) || len([]byte(normalized)) > probeErrorLimit || strings.ContainsRune(normalized, '\x00') {
		t.Fatalf("normalized raw error invalid: changed=%v bytes=%d valid=%v", changed, len([]byte(normalized)), utf8.ValidString(normalized))
	}
}

func TestOfferInputValidation(t *testing.T) {
	valid, err := normalizeOfferInput(OfferInput{ModelID: "openai/gpt-5", Protocol: ProtocolOpenAIResponse, UpstreamModelID: "gpt-5", Multiplier: 1_000_000_000})
	if err != nil || valid.Multiplier.String() != "1" {
		t.Fatalf("valid offer = %#v, %v", valid, err)
	}
	defaulted, err := normalizeOfferInput(OfferInput{ModelID: "gpt-5", Protocol: ProtocolOpenAIResponse, Multiplier: 1_000_000_000})
	if err != nil || defaulted.UpstreamModelID != "gpt-5" {
		t.Fatalf("default upstream model = %#v, %v", defaulted, err)
	}
	for _, input := range []OfferInput{
		{ModelID: "", Protocol: ProtocolOpenAIResponse, UpstreamModelID: "gpt-5", Multiplier: 1_000_000_000},
		{ModelID: "model", Protocol: "unknown", UpstreamModelID: "model", Multiplier: 1_000_000_000},
		{ModelID: "model", Protocol: ProtocolGemini, UpstreamModelID: "models/gemini", Multiplier: 1_000_000_000},
		{ModelID: "model", Protocol: ProtocolGemini, UpstreamModelID: "family/gemini", Multiplier: 1_000_000_000},
		{ModelID: "model", Protocol: ProtocolOpenAIChat, UpstreamModelID: "bad\nmodel", Multiplier: 1_000_000_000},
		{ModelID: "model", Protocol: ProtocolOpenAIChat, UpstreamModelID: "model", Multiplier: 1_000_000_000_001},
	} {
		if _, err := normalizeOfferInput(input); err == nil {
			t.Fatalf("invalid offer accepted: %#v", input)
		}
	}
}
