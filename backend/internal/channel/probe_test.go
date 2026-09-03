package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net"
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
		attempt, terminal := probeHTTPFailure(ValidationAttempt{HTTPStatus: test.status}, test.status, http.StatusText(test.status), []byte("upstream error"), test.tooLarge, test.readErr, "probe-secret", time.Millisecond)
		if !terminal || attempt.ErrorCategory != test.want || attempt.HTTPStatus != test.status {
			t.Fatalf("status %d = %#v, terminal %v; want %s", test.status, attempt, terminal, test.want)
		}
	}
}

func TestProbeHTTPFailureBlocksExactCredentialEcho(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		attempt, terminal := probeHTTPFailure(
			ValidationAttempt{HTTPStatus: status}, status, http.StatusText(status),
			[]byte(`{"error":"Bearer probe-secret"}`), false, nil, "probe-secret", time.Millisecond,
		)
		if !terminal || strings.Contains(attempt.RawError, "probe-secret") || attempt.RawError != "upstream error contained a credential and was blocked" {
			t.Fatalf("status %d credential echo persisted: %+v terminal=%v", status, attempt, terminal)
		}
	}
	credential := `probe"secret\tail`
	escaped, err := json.Marshal(map[string]string{"error": credential})
	if err != nil {
		t.Fatal(err)
	}
	attempt, terminal := probeHTTPFailure(
		ValidationAttempt{HTTPStatus: http.StatusUnauthorized}, http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized),
		escaped, false, nil, credential, time.Millisecond,
	)
	if !terminal || attempt.RawError != "upstream error contained a credential and was blocked" {
		t.Fatalf("JSON-escaped credential echo persisted: %+v terminal=%v", attempt, terminal)
	}
	normal := `{"error":"ordinary upstream URL https://relay.example/models/vendor"}`
	attempt, terminal = probeHTTPFailure(
		ValidationAttempt{HTTPStatus: http.StatusInternalServerError}, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError),
		[]byte(normal), false, nil, credential, time.Millisecond,
	)
	if !terminal || attempt.RawError != normal {
		t.Fatalf("ordinary upstream error was changed: %+v terminal=%v", attempt, terminal)
	}
	unicodeEscaped := `{"debug":"probe-\u0073ecret"}`
	attempt, terminal = probeHTTPFailure(
		ValidationAttempt{HTTPStatus: http.StatusInternalServerError}, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError),
		[]byte(unicodeEscaped), false, nil, "probe-secret", time.Millisecond,
	)
	if !terminal || attempt.RawError != "upstream error contained a credential and was blocked" {
		t.Fatalf("Unicode-escaped probe credential persisted: %+v terminal=%v", attempt, terminal)
	}
}

func TestProbeTransportFailureCannotPersistCredentialFromEndpointPath(t *testing.T) {
	credential := "exact-upstream-credential"
	policy, err := NewOutboundPolicyWithResolver(nil, nil, &fakeResolver{addresses: []net.IP{net.ParseIP("93.184.216.34")}})
	if err != nil {
		t.Fatal(err)
	}
	policy.dialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("injected connection failure")
	}
	service := &Service{outbound: policy}
	attempt := service.probe(context.Background(), ValidationTarget{
		Attempt:           ValidationAttempt{ID: "attempt-credential", StartedAt: time.Now()},
		NormalizedBaseURL: "https://relay.example/" + credential,
		Protocol:          ProtocolOpenAIChat,
		UpstreamModelID:   "vendor-model",
	}, credential)
	if attempt.Status != ValidationFailed || attempt.ErrorCategory != ErrorTransport || attempt.RawError != "upstream error contained a credential and was blocked" || strings.Contains(attempt.RawError, credential) {
		t.Fatalf("probe transport credential persisted: %+v", attempt)
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
		switch protocol {
		case ProtocolOpenAIChat:
			if decoded["max_tokens"] != float64(probeMaxOutputTokens) || decoded["stream"] != false {
				t.Fatalf("chat probe payload = %s", body)
			}
		case ProtocolOpenAIResponse:
			if decoded["max_output_tokens"] != float64(probeMaxOutputTokens) || decoded["stream"] != false {
				t.Fatalf("responses probe payload = %s", body)
			}
		case ProtocolAnthropic:
			if decoded["max_tokens"] != float64(probeMaxOutputTokens) || decoded["stream"] != false {
				t.Fatalf("anthropic probe payload = %s", body)
			}
		case ProtocolGemini:
			config, _ := decoded["generationConfig"].(map[string]any)
			if config["maxOutputTokens"] != float64(probeMaxOutputTokens) {
				t.Fatalf("gemini probe payload = %s", body)
			}
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
