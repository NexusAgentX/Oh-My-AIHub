package gateway

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/secretguard"
)

func TestUsageStateSeparatesAbsentFromInvalid(t *testing.T) {
	absent := []struct {
		protocol channel.Protocol
		frame    string
	}{
		{channel.ProtocolOpenAIChat, `data: {"choices":[{"index":0,"delta":{"content":"hi"}}]}` + "\n\n"},
		{channel.ProtocolOpenAIResponse, `data: {"type":"response.output_text.delta","delta":"hi"}` + "\n\n"},
		{channel.ProtocolAnthropic, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}` + "\n\n"},
	}
	for _, test := range absent {
		analysis, err := AnalyzeSSEFrame(test.protocol, []byte(test.frame), "canonical/model")
		if err != nil || analysis.UsageState != UsageAbsent {
			t.Fatalf("absent usage for %s = %v %v", test.protocol, analysis.UsageState, err)
		}
	}
	invalid := []struct {
		name     string
		protocol channel.Protocol
		frame    string
	}{
		{"chat-usage-not-object", channel.ProtocolOpenAIChat, `data: {"choices":[],"usage":"bad"}` + "\n\n"},
		{"chat-contradictory-cache", channel.ProtocolOpenAIChat, `data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":6}}}` + "\n\n"},
		{"chat-audio-dimension", channel.ProtocolOpenAIChat, `data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"prompt_tokens_details":{"audio_tokens":2}}}` + "\n\n"},
		{"anthropic-token-not-a-number", channel.ProtocolAnthropic, `data: {"type":"message_delta","usage":{"output_tokens":"bad"}}` + "\n\n"},
		{"anthropic-cache-without-input", channel.ProtocolAnthropic, `data: {"type":"message_delta","usage":{"cache_read_input_tokens":3}}` + "\n\n"},
		{"gemini-metadata-not-object", channel.ProtocolGemini, `data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":"bad"}` + "\n\n"},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			analysis, err := AnalyzeSSEFrame(test.protocol, []byte(test.frame), "canonical/model")
			if err != nil {
				t.Fatal(err)
			}
			if analysis.UsageState != UsageInvalid {
				t.Fatalf("usage state = %v, want UsageInvalid", analysis.UsageState)
			}
		})
	}
}

// Event names are observation only: relays rename events, add fields, and send
// their own keepalive payloads. Only an explicit error payload fails the attempt.
func TestSSEFrameEventNamesAreObservationOnly(t *testing.T) {
	frames := []struct {
		name         string
		protocol     channel.Protocol
		frame        string
		wantMismatch bool
	}{
		{"renamed-responses-event", channel.ProtocolOpenAIResponse, "event: vendor.renamed\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n", true},
		{"renamed-anthropic-event", channel.ProtocolAnthropic, "event: vendor.renamed\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n", true},
		{"non-json-data", channel.ProtocolOpenAIResponse, "event: vendor.ping\ndata: keepalive\n\n", false},
		{"unknown-sse-field", channel.ProtocolAnthropic, "vendor-field: 1\ndata: {\"type\":\"ping\"}\n\n", false},
	}
	for _, test := range frames {
		t.Run(test.name, func(t *testing.T) {
			analysis, err := AnalyzeSSEFrame(test.protocol, []byte(test.frame), "canonical/model")
			if err != nil {
				t.Fatalf("frame was rejected: %v", err)
			}
			if len(analysis.Frame) == 0 {
				t.Fatal("frame was dropped instead of forwarded")
			}
			if analysis.EventNameMismatch != test.wantMismatch {
				t.Fatalf("event name mismatch = %t, want %t", analysis.EventNameMismatch, test.wantMismatch)
			}
		})
	}
	keepalive, err := AnalyzeSSEFrame(channel.ProtocolOpenAIResponse, []byte("event: vendor.ping\ndata: keepalive\n\n"), "canonical/model")
	if err != nil || keepalive.Semantic || keepalive.Terminal || keepalive.UsageState != UsageAbsent {
		t.Fatalf("keepalive payload classified: %+v %v", keepalive, err)
	}
	// The SSE specification keeps the last event field, so this is a real
	// Anthropic terminal frame.
	terminal, err := AnalyzeSSEFrame(channel.ProtocolAnthropic, []byte("event: first\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"), "canonical/model")
	if err != nil || !terminal.Terminal {
		t.Fatalf("duplicate event field = %+v %v", terminal, err)
	}
	if terminal.EventNameMismatch {
		t.Fatalf("last event field did not win: %+v", terminal)
	}
	// An empty event field resets the event type instead of keeping the old one.
	reset, err := AnalyzeSSEFrame(channel.ProtocolAnthropic, []byte("event: vendor.old\nevent:\ndata: {\"type\":\"message_stop\"}\n\n"), "canonical/model")
	if err != nil || !reset.Terminal {
		t.Fatalf("empty event field = %+v %v", reset, err)
	}
	if reset.EventNameMismatch {
		t.Fatalf("empty event field did not reset the event name: %+v", reset)
	}
}

// A payload that explicitly reports failure still fails, even when the relay
// renamed the event around it.
func TestSSEErrorPayloadWinsOverEventNameMismatch(t *testing.T) {
	analysis, err := AnalyzeSSEFrame(channel.ProtocolOpenAIResponse, []byte("event: vendor.renamed\ndata: {\"type\":\"response.failed\",\"error\":{\"code\":\"relay_busy\",\"message\":\"nope\"}}\n\n"), "canonical/model")
	if err != nil {
		t.Fatal(err)
	}
	if analysis.ErrorCode != "relay_busy" {
		t.Fatalf("renamed error payload was not classified as an error: %+v", analysis)
	}
}

// Frame fidelity is branch dependent, and documented as such: frames that never
// reach the JSON path travel byte for byte, while JSON frames are re-serialised
// into a compact event/data frame.
func TestSSEFrameFidelityIsBranchDependent(t *testing.T) {
	raw := "id: 42\nretry: 1000\n: comment\nevent: vendor.ping\ndata: keepalive\n\n"
	analysis, err := AnalyzeSSEFrame(channel.ProtocolOpenAIResponse, []byte(raw), "canonical/model")
	if err != nil || string(analysis.Frame) != raw {
		t.Fatalf("non-JSON frame = %q %v", analysis.Frame, err)
	}
	jsonFrame := "id: 42\nretry: 1000\n: comment\nvendor-field: 1\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\",\"vendor_extra\":true}\n\n"
	analysis, err = AnalyzeSSEFrame(channel.ProtocolOpenAIResponse, []byte(jsonFrame), "canonical/model")
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(analysis.Frame)
	if strings.Contains(encoded, "id:") || strings.Contains(encoded, "retry:") || strings.Contains(encoded, "vendor-field") {
		t.Fatalf("transport metadata reached the client: %q", encoded)
	}
	if !strings.Contains(encoded, `"vendor_extra":true`) {
		t.Fatalf("unknown JSON field was dropped: %q", encoded)
	}
}

func TestNonStreamingForwardsNonJSONBodyWithoutUsage(t *testing.T) {
	body := "<html>relay outage page</html>"
	rewritten, usage, err := RewriteNonStreamingResponse(channel.ProtocolOpenAIChat, []byte(body), "canonical/model")
	if err != nil || usage != nil || string(rewritten) != body {
		t.Fatalf("non-JSON body = %q %+v %v", rewritten, usage, err)
	}
}

func TestSSEFrameWithoutNewlineStopsAtTheEventLimit(t *testing.T) {
	reader, writer := io.Pipe()
	result := make(chan error, 1)
	go func() {
		_, err := readSSEFrame(bufio.NewReaderSize(reader, 64*1024))
		result <- err
	}()
	writeDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer, strings.Repeat("x", MaxSSEEventBytes+128*1024))
		writeDone <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, ErrResponseTooBig) {
			t.Fatalf("unbounded no-newline frame = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no-newline frame waited for EOF instead of enforcing the incremental limit")
	}
	_ = reader.Close()
	_ = writer.Close()
	select {
	case <-writeDone:
	case <-time.After(time.Second):
		t.Fatal("oversize frame writer did not unblock after reader close")
	}
}

func TestNonStreamingResponseUsesTheRequestDeadlineForWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	contextDeadline, _ := ctx.Deadline()
	w := &deadlineResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	if err := writeSanitizedResponse(ctx, w, http.StatusOK, jsonHeader(), []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if w.deadline.IsZero() || w.deadline.After(contextDeadline) || w.deadline.Before(time.Now().Add(-time.Second)) {
		t.Fatalf("nonstream write deadline = %s, context deadline = %s", w.deadline, contextDeadline)
	}
	if w.Code != http.StatusOK || w.Body.String() != `{"ok":true}` {
		t.Fatalf("nonstream write = %d %s", w.Code, w.Body.String())
	}
}

func TestProxySequentialFallbackIsolationAndSettlementOrdering(t *testing.T) {
	secret := "oma_live_" + strings.Repeat("a", 43)
	candidates := []Candidate{
		proxyCandidate(1, "offer-one", "vendor-one", "upstream-key-one"),
		proxyCandidate(2, "offer-two", "vendor-two", "upstream-key-two"),
	}
	store := newProxyStore(candidates)
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-one": func(request *http.Request) (*http.Response, error) {
			assertIsolatedUpstreamRequest(t, request, "Bearer upstream-key-one")
			return proxyResponse(http.StatusBadGateway, http.Header{
				"Content-Type": []string{"application/json"}, "Set-Cookie": []string{"relay=secret"}, "Connection": []string{"X-Upstream-Leak"}, "X-Upstream-Leak": []string{"secret"},
			}, `{"error":{"code":"relay_busy","message":"relay overloaded"},"full_body_secret":"DO_NOT_PERSIST","authorization":"Bearer upstream-key-one"}`), nil
		},
		"offer-two": func(request *http.Request) (*http.Response, error) {
			assertIsolatedUpstreamRequest(t, request, "Bearer upstream-key-two")
			return proxyResponse(http.StatusOK, http.Header{
				"Content-Type": []string{"application/json"}, "Set-Cookie": []string{"relay=secret"}, "ETag": []string{"private"}, "X-Safe": []string{"blocked"}, "Location": []string{"https://evil.example"}, "X-Accel-Redirect": []string{"/private"}, "X-Sendfile": []string{"/secret"},
			}, `{"id":"result","object":"chat.completion","model":"vendor-two","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":2}}}`), nil
		},
	}}
	service, err := NewService(store, outbound)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"canonical/model","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Cookie", "session=client-secret")
	request.Header.Set("Forwarded", "for=private")
	request.Header.Set("X-Forwarded-For", "10.0.0.1")
	request.Header.Set("Connection", "X-Client-Leak")
	request.Header.Set("X-Client-Leak", "client-secret")
	request.Header.Set("Idempotency-Key", "client-retry-key")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, request, channel.ProtocolOpenAIChat, "", false)
	if recorder.Code != http.StatusOK {
		t.Fatalf("proxy status/body = %d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"model":"canonical/model"`) || strings.Contains(recorder.Body.String(), "vendor-two") {
		t.Fatalf("response model was not restored: %s", recorder.Body.String())
	}
	for _, blocked := range []string{"Set-Cookie", "ETag", "Content-Encoding", "Content-Length", "X-Upstream-Leak", "X-Safe", "Location", "X-Accel-Redirect", "X-Sendfile"} {
		if recorder.Header().Get(blocked) != "" {
			t.Fatalf("blocked response header %s escaped: %q", blocked, recorder.Header().Get(blocked))
		}
	}
	requests := outbound.snapshotRequests()
	if len(requests) != 2 {
		t.Fatalf("upstream request count = %d, want 2", len(requests))
	}
	if !strings.Contains(requests[0].body, `"model":"vendor-one"`) || !strings.Contains(requests[1].body, `"model":"vendor-two"`) || strings.Contains(requests[1].body, "vendor-one") {
		t.Fatalf("candidate bodies were not regenerated from the original: %#v", requests)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 2 || store.completed[0].Status != AttemptFailed || store.completed[1].Status != AttemptSucceeded {
		t.Fatalf("attempt results = %+v", store.completed)
	}
	if store.completed[0].ErrorCode != "upstream_error_contained_credential" || store.completed[0].RawError != "upstream error contained a credential and was blocked" || strings.Contains(store.completed[0].RawError, "DO_NOT_PERSIST") || strings.Contains(store.completed[0].RawError, "upstream-key-one") {
		t.Fatalf("credential-bearing upstream error was not fail-closed: %+v", store.completed[0])
	}
	if len(store.finalized) != 1 || store.finalized[0].Status != CallSucceeded || store.finalized[0].FinalOfferID != "offer-two" || store.completed[1].Usage == nil {
		t.Fatalf("settlement ordering/facts = finalized:%+v attempts:%+v", store.finalized, store.completed)
	}
}

func TestProxyBlocksCredentialFromSuccessfulBodiesAndAllowedHeaders(t *testing.T) {
	for _, test := range []struct {
		name     string
		response func(string) *http.Response
	}{
		{
			name: "body",
			response: func(credential string) *http.Response {
				body := fmt.Sprintf(`{"object":"chat.completion","model":"vendor-leak","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`, credential)
				return proxyResponse(http.StatusOK, jsonHeader(), body)
			},
		},
		{
			name: "allowed response header",
			response: func(credential string) *http.Response {
				header := jsonHeader()
				header.Set("X-Request-Id", `debug-`+credential)
				return proxyResponse(http.StatusOK, header, validChatResponse("vendor-leak"))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			credential := `upstream"key\tail`
			store := newProxyStore([]Candidate{
				proxyCandidate(1, "offer-leak", "vendor-leak", credential),
				proxyCandidate(2, "offer-safe", "vendor-safe", "safe-key"),
			})
			outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
				"offer-leak": func(*http.Request) (*http.Response, error) { return test.response(credential), nil },
				"offer-safe": func(*http.Request) (*http.Response, error) {
					return proxyResponse(http.StatusOK, jsonHeader(), validChatResponse("vendor-safe")), nil
				},
			}}
			service, _ := NewService(store, outbound)
			recorder := httptest.NewRecorder()
			service.ServeProtocol(recorder, chatProxyRequest(context.Background(), false), channel.ProtocolOpenAIChat, "", false)
			if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), credential) || recorder.Header().Get("X-Request-Id") != "" {
				t.Fatalf("credential escaped downstream: %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if len(store.completed) != 2 || store.completed[0].ErrorCode != "upstream_error_contained_credential" || store.completed[0].RawError != "upstream error contained a credential and was blocked" || store.completed[1].Status != AttemptSucceeded {
				t.Fatalf("credential fallback facts = %+v", store.completed)
			}
		})
	}
}

func TestProxyBlocksCredentialIntroducedByResponseRewrite(t *testing.T) {
	credential := "canonical/model"
	store := newProxyStore([]Candidate{
		proxyCandidate(1, "offer-leak", "vendor-model", credential),
		proxyCandidate(2, "offer-safe", "vendor-safe", "safe-key"),
	})
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-leak": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, jsonHeader(), validChatResponse("vendor-model")), nil
		},
		"offer-safe": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, jsonHeader(), validChatResponse("vendor-safe")), nil
		},
	}}
	service, _ := NewService(store, outbound)
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, chatProxyRequest(context.Background(), false), channel.ProtocolOpenAIChat, "", false)
	if recorder.Code != http.StatusBadGateway || strings.Contains(recorder.Body.String(), credential) {
		t.Fatalf("post-rewrite all-candidate credential block response = %d %s", recorder.Code, recorder.Body.String())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 2 || store.completed[0].ErrorCode != secretguard.CredentialErrorCode || store.completed[0].RawError != secretguard.CredentialErrorMessage || store.completed[1].ErrorCode != secretguard.CredentialErrorCode {
		t.Fatalf("post-rewrite credential facts = %+v", store.completed)
	}
}

func TestProxyProtectsCredentialInLocalAndTransportErrors(t *testing.T) {
	platformSecret := "oma_live_" + strings.Repeat("z", 43)
	for _, test := range []struct {
		name     string
		outbound func(string) *proxyOutbound
	}{
		{
			name: "outbound factory",
			outbound: func(credential string) *proxyOutbound {
				return &proxyOutbound{
					targetErrors: map[string]error{"offer-leak": fmt.Errorf("factory rejected %s and %s", credential, platformSecret)},
					handlers: map[string]func(*http.Request) (*http.Response, error){
						"offer-safe": func(*http.Request) (*http.Response, error) {
							return proxyResponse(http.StatusOK, jsonHeader(), validChatResponse("vendor-safe")), nil
						},
					},
				}
			},
		},
		{
			name: "transport URL",
			outbound: func(credential string) *proxyOutbound {
				return &proxyOutbound{
					endpoints: map[string]string{"offer-leak": "https://upstream.invalid/" + credential},
					handlers: map[string]func(*http.Request) (*http.Response, error){
						"offer-leak": func(request *http.Request) (*http.Response, error) { return nil, fmt.Errorf("dial %s", request.URL) },
						"offer-safe": func(*http.Request) (*http.Response, error) {
							return proxyResponse(http.StatusOK, jsonHeader(), validChatResponse("vendor-safe")), nil
						},
					},
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			credential := "exact-upstream-credential"
			store := newProxyStore([]Candidate{
				proxyCandidate(1, "offer-leak", "vendor-leak", credential),
				proxyCandidate(2, "offer-safe", "vendor-safe", "safe-key"),
			})
			service, _ := NewService(store, test.outbound(credential))
			recorder := httptest.NewRecorder()
			service.ServeProtocol(recorder, chatProxyRequest(context.Background(), false), channel.ProtocolOpenAIChat, "", false)
			if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), credential) || strings.Contains(recorder.Body.String(), platformSecret) {
				t.Fatalf("credential escaped local failure = %d %s", recorder.Code, recorder.Body.String())
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if len(store.completed) != 2 || store.completed[0].ErrorCode != secretguard.CredentialErrorCode || store.completed[0].RawError != secretguard.CredentialErrorMessage {
				t.Fatalf("protected local failure facts = %+v", store.completed)
			}
		})
	}
}

func TestProxyDoesNotExposeCallIDWhenItEqualsUpstreamCredential(t *testing.T) {
	credential := "call-proxy"
	store := newProxyStore([]Candidate{proxyCandidate(1, "offer-one", "vendor-one", credential)})
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-one": func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network unavailable")
		},
	}}
	service, _ := NewService(store, outbound)
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, chatProxyRequest(context.Background(), false), channel.ProtocolOpenAIChat, "", false)
	if strings.Contains(recorder.Body.String(), credential) {
		t.Fatalf("credential-valued call id escaped downstream: %s", recorder.Body.String())
	}
}

func TestProxyRejectsPlatformCredentialInRequestBodyBeforeUpstreamAccess(t *testing.T) {
	secret := "oma_live_" + strings.Repeat("z", 43)
	escaped := strings.Replace(secret, "z", `\u007a`, 1)
	store := newProxyStore([]Candidate{proxyCandidate(1, "offer-one", "vendor-one", "upstream-key")})
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-one": func(*http.Request) (*http.Response, error) {
			t.Fatal("request containing the platform credential reached upstream")
			return nil, errors.New("unreachable")
		},
	}}
	service, _ := NewService(store, outbound)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"canonical/model","messages":[{"role":"user","content":"`+escaped+`"}]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, request, channel.ProtocolOpenAIChat, "", false)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "credential_in_request_body") || len(outbound.snapshotRequests()) != 0 {
		t.Fatalf("request credential rejection = %d %s, requests=%d", recorder.Code, recorder.Body.String(), len(outbound.snapshotRequests()))
	}
}

func TestProxyForwardsNativeRequestShapesToUpstream(t *testing.T) {
	tests := []struct {
		name      string
		protocol  channel.Protocol
		path      string
		canonical string
		body      string
		headers   map[string]string
		response  string
	}{
		{
			name: "responses session continuation", protocol: channel.ProtocolOpenAIResponse, path: "/v1/responses",
			body:     `{"model":"canonical/model","input":"hi","previous_response_id":"resp_prev","store":true}`,
			headers:  map[string]string{"Authorization": "Bearer oma_live_" + strings.Repeat("r", 43)},
			response: `{"id":"resp_1","object":"response","status":"completed","model":"vendor-one","previous_response_id":"resp_prev","store":true,"output":[{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`,
		},
		{
			name: "chat audio modality", protocol: channel.ProtocolOpenAIChat, path: "/v1/chat/completions",
			body:     `{"model":"canonical/model","messages":[],"modalities":["audio"]}`,
			headers:  map[string]string{"Authorization": "Bearer oma_live_" + strings.Repeat("c", 43)},
			response: validChatResponse("vendor-one"),
		},
		{
			name: "responses provider file", protocol: channel.ProtocolOpenAIResponse, path: "/v1/responses",
			body:     `{"model":"canonical/model","input":[{"type":"message","content":[{"type":"input_file","file_id":"file_1"}]}]}`,
			headers:  map[string]string{"Authorization": "Bearer oma_live_" + strings.Repeat("o", 43)},
			response: `{"id":"resp_1","object":"response","status":"completed","model":"vendor-one","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`,
		},
		{
			name: "responses pro reasoning", protocol: channel.ProtocolOpenAIResponse, path: "/v1/responses",
			body:     `{"model":"canonical/model","input":"hi","reasoning":{"mode":"pro"}}`,
			headers:  map[string]string{"Authorization": "Bearer oma_live_" + strings.Repeat("p", 43)},
			response: `{"id":"resp_1","object":"response","status":"completed","model":"vendor-one","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`,
		},
		{
			name: "anthropic media and beta", protocol: channel.ProtocolAnthropic, path: "/v1/messages",
			body:     `{"model":"canonical/model","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"x"}}]}]}`,
			headers:  map[string]string{"x-api-key": "oma_live_" + strings.Repeat("a", 43), "anthropic-version": "2023-06-01", "anthropic-beta": "pdfs-2024-09-25"},
			response: `{"id":"msg_1","type":"message","role":"assistant","model":"vendor-one","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`,
		},
		{
			name: "gemini inline data", protocol: channel.ProtocolGemini, path: "/v1beta/models/canonical/model:generateContent", canonical: "canonical/model",
			body:     `{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":"x"}}]}]}`,
			headers:  map[string]string{"x-goog-api-key": "oma_live_" + strings.Repeat("g", 43)},
			response: `{"modelVersion":"vendor-one","candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newProxyStore([]Candidate{proxyCandidate(1, "offer-one", "vendor-one", "upstream-key")})
			outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
				"offer-one": func(*http.Request) (*http.Response, error) {
					return proxyResponse(http.StatusOK, jsonHeader(), test.response), nil
				},
			}}
			service, _ := NewService(store, outbound)
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			for name, value := range test.headers {
				request.Header.Set(name, value)
			}
			recorder := httptest.NewRecorder()
			service.ServeProtocol(recorder, request, test.protocol, test.canonical, false)
			requests := outbound.snapshotRequests()
			if recorder.Code != http.StatusOK || len(requests) != 1 {
				t.Fatalf("native shape response = %d %s, requests=%d", recorder.Code, recorder.Body.String(), len(requests))
			}
			if test.protocol != channel.ProtocolGemini && !strings.Contains(requests[0].body, "\"model\":\"vendor-one\"") {
				t.Fatalf("upstream model rewrite missing: %s", requests[0].body)
			}
			if strings.Contains(test.body, "previous_response_id") && !strings.Contains(requests[0].body, "previous_response_id") {
				t.Fatalf("previous_response_id was stripped: %s", requests[0].body)
			}
			if strings.Contains(test.body, "modalities") && !strings.Contains(requests[0].body, "modalities") {
				t.Fatalf("modalities was stripped: %s", requests[0].body)
			}
			if beta := request.Header.Get("anthropic-beta"); beta != "" && requests[0].header.Get("anthropic-beta") != beta {
				t.Fatalf("anthropic-beta was stripped: %v", requests[0].header)
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if store.begun != 1 || len(store.started) != 1 || len(store.completed) != 1 || store.completed[0].Status != AttemptSucceeded {
				t.Fatalf("native shape facts: begun=%d attempts:%+v completed:%+v", store.begun, store.started, store.completed)
			}
		})
	}
}

func TestProxyAllowsTextOnlyClientToolSchemasWithMediaNamedProperties(t *testing.T) {
	tests := []struct {
		name      string
		protocol  channel.Protocol
		path      string
		canonical string
		body      string
		headers   map[string]string
		response  string
	}{
		{
			name: "chat", protocol: channel.ProtocolOpenAIChat, path: "/v1/chat/completions",
			body:     `{"model":"canonical/model","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"tool","parameters":{"type":"object","properties":{"image":{"type":"string"},"audio":{"type":"string"}}}}}]}`,
			headers:  map[string]string{"Authorization": "Bearer oma_live_" + strings.Repeat("c", 43)},
			response: validChatResponse("vendor-model"),
		},
		{
			name: "responses", protocol: channel.ProtocolOpenAIResponse, path: "/v1/responses",
			body:     `{"model":"canonical/model","input":"hi","tools":[{"type":"custom","name":"tool","format":{"schema":{"properties":{"file_id":{"type":"string"}}}}}]}`,
			headers:  map[string]string{"Authorization": "Bearer oma_live_" + strings.Repeat("r", 43)},
			response: `{"id":"resp_1","object":"response","status":"completed","model":"vendor-model","output":[{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`,
		},
		{
			name: "anthropic", protocol: channel.ProtocolAnthropic, path: "/v1/messages",
			body:     `{"model":"canonical/model","max_tokens":8,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"tool","input_schema":{"type":"object","properties":{"source":{"type":"string"},"file":{"type":"string"},"cache_control":{"type":"string"}}},"cache_control":{"type":"ephemeral","ttl":"5m"}}]}`,
			headers:  map[string]string{"x-api-key": "oma_live_" + strings.Repeat("a", 43), "anthropic-version": "2023-06-01"},
			response: `{"id":"msg_1","type":"message","role":"assistant","model":"vendor-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`,
		},
		{
			name: "gemini", protocol: channel.ProtocolGemini, path: "/v1beta/models/canonical/model:generateContent", canonical: "canonical/model",
			body:     `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"tool","parameters":{"type":"object","properties":{"image":{"type":"string"}}}}]}]}`,
			headers:  map[string]string{"x-goog-api-key": "oma_live_" + strings.Repeat("g", 43)},
			response: `{"modelVersion":"vendor-model","candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := proxyCandidate(1, "offer-one", "vendor-model", "upstream-key")
			candidate.Lease.Protocol = test.protocol
			store := newProxyStore([]Candidate{candidate})
			outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
				"offer-one": func(*http.Request) (*http.Response, error) {
					return proxyResponse(http.StatusOK, jsonHeader(), test.response), nil
				},
			}}
			service, _ := NewService(store, outbound)
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			for name, value := range test.headers {
				request.Header.Set(name, value)
			}
			recorder := httptest.NewRecorder()
			service.ServeProtocol(recorder, request, test.protocol, test.canonical, false)
			if recorder.Code != http.StatusOK || len(outbound.snapshotRequests()) != 1 {
				t.Fatalf("client tool request = %d %s", recorder.Code, recorder.Body.String())
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if store.begun != 1 || len(store.started) != 1 || len(store.completed) != 1 || store.completed[0].Status != AttemptSucceeded {
				t.Fatalf("client tool facts: begun=%d attempts:%+v completed:%+v", store.begun, store.started, store.completed)
			}
		})
	}
}

func TestProxyBlocksUnicodeEscapedCredentialEchoes(t *testing.T) {
	credential := "upstream-secret-unicode"
	escapedCredential := `upstream-\u0073ecret-unicode`
	tests := []struct {
		name     string
		stream   bool
		response func() *http.Response
	}{
		{
			name: "non-success extra field",
			response: func() *http.Response {
				return proxyResponse(http.StatusBadGateway, jsonHeader(), `{"error":{"code":"busy","message":"retry"},"debug":"`+escapedCredential+`"}`)
			},
		},
		{
			name: "successful body",
			response: func() *http.Response {
				return proxyResponse(http.StatusOK, jsonHeader(), `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"`+escapedCredential+`"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`)
			},
		},
		{
			name: "sse payload", stream: true,
			response: func() *http.Response {
				return proxyResponse(http.StatusOK, sseHeader(), `data: {"choices":[{"index":0,"delta":{"content":"`+escapedCredential+`"}}]}`+"\n\n")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newProxyStore([]Candidate{
				proxyCandidate(1, "offer-leak", "vendor-leak", credential),
				proxyCandidate(2, "offer-safe", "vendor-safe", "safe-key"),
			})
			outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
				"offer-leak": func(*http.Request) (*http.Response, error) { return test.response(), nil },
				"offer-safe": func(*http.Request) (*http.Response, error) {
					if test.stream {
						return proxyResponse(http.StatusOK, sseHeader(), validChatStream("vendor-safe", "safe")), nil
					}
					return proxyResponse(http.StatusOK, jsonHeader(), validChatResponse("vendor-safe")), nil
				},
			}}
			service, _ := NewService(store, outbound)
			recorder := httptest.NewRecorder()
			service.ServeProtocol(recorder, chatProxyRequest(context.Background(), test.stream), channel.ProtocolOpenAIChat, "", false)
			if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), credential) || strings.Contains(recorder.Body.String(), escapedCredential) {
				t.Fatalf("escaped credential leaked: %d %s", recorder.Code, recorder.Body.String())
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if len(store.completed) != 2 || store.completed[0].ErrorCode != "upstream_error_contained_credential" || store.completed[1].Status != AttemptSucceeded {
				t.Fatalf("escaped credential fallback facts = %+v", store.completed)
			}
		})
	}
}

func TestProxyStopsSplitStreamingCredentialBeforeTheCompletingFragment(t *testing.T) {
	credential := `stream"secret\tail`
	left, right := credential[:8], credential[8:]
	store := newProxyStore([]Candidate{proxyCandidate(1, "offer-leak", "vendor-leak", credential)})
	body := fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", left) +
		fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", right)
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-leak": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, sseHeader(), body), nil
		},
	}}
	service, _ := NewService(store, outbound)
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, chatProxyRequest(context.Background(), true), channel.ProtocolOpenAIChat, "", false)
	if strings.Contains(recorder.Body.String(), credential) || strings.Contains(recorder.Body.String(), right) {
		t.Fatalf("credential-completing fragment escaped downstream: %s", recorder.Body.String())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 1 || store.completed[0].Status != AttemptIncomplete || store.completed[0].ErrorCode != "upstream_error_contained_credential" || !store.completed[0].SemanticCommitted || len(store.finalized) != 1 || store.finalized[0].Status != CallIncomplete {
		t.Fatalf("split credential facts = attempts:%+v final:%+v", store.completed, store.finalized)
	}
}

func TestProxyNonStreamingMetricsSeparateResponseHeaderFromBodyCompletion(t *testing.T) {
	store := newProxyStore([]Candidate{proxyCandidate(1, "offer-metrics", "vendor-metrics", "key-metrics")})
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-metrics": func(*http.Request) (*http.Response, error) {
			reader, writer := io.Pipe()
			go func() {
				time.Sleep(20 * time.Millisecond)
				_, _ = io.WriteString(writer, validChatResponse("vendor-metrics"))
				_ = writer.Close()
			}()
			return &http.Response{StatusCode: http.StatusOK, Header: jsonHeader(), Body: reader}, nil
		},
	}}
	service, _ := NewService(store, outbound)
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, chatProxyRequest(context.Background(), false), channel.ProtocolOpenAIChat, "", false)
	if recorder.Code != http.StatusOK {
		t.Fatalf("non-streaming metric response = %d %s", recorder.Code, recorder.Body.String())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 1 || store.completed[0].Status != AttemptSucceeded || store.completed[0].MeasureTPS || store.completed[0].TTFTObserved || store.completed[0].TTFT != 0 || store.completed[0].Duration < 10*time.Millisecond {
		t.Fatalf("non-streaming timing facts = %+v", store.completed)
	}
}

func TestProxySettlementFailureNeverPublishesASuccessTerminalAndDegradesToIncomplete(t *testing.T) {
	t.Run("nonstream", func(t *testing.T) {
		store := newProxyStore([]Candidate{proxyCandidate(1, "offer-one", "vendor-one", "upstream-key")})
		store.successFinalizeFailures = 1
		outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
			"offer-one": func(*http.Request) (*http.Response, error) {
				return proxyResponse(http.StatusOK, jsonHeader(), validChatResponse("vendor-one")), nil
			},
		}}
		service, _ := NewService(store, outbound)
		recorder := httptest.NewRecorder()
		service.ServeProtocol(recorder, chatProxyRequest(context.Background(), false), channel.ProtocolOpenAIChat, "", false)
		if recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), `"choices"`) || !strings.Contains(recorder.Body.String(), "settlement_failed") {
			t.Fatalf("nonstream settlement failure = %d %s", recorder.Code, recorder.Body.String())
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		if len(store.completed) != 1 || store.completed[0].Status != AttemptIncomplete || store.completed[0].Usage != nil || store.completed[0].ErrorCode != "settlement_failed" || len(store.finalized) != 2 || store.finalized[0].Status != CallSucceeded || store.finalized[1].Status != CallIncomplete {
			t.Fatalf("nonstream compensation = attempts:%+v finalizers:%+v", store.completed, store.finalized)
		}
	})

	t.Run("stream after an early semantic commit", func(t *testing.T) {
		store := newProxyStore([]Candidate{proxyCandidate(1, "offer-one", "vendor-one", "upstream-key")})
		store.successFinalizeFailures = 1
		outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
			"offer-one": func(*http.Request) (*http.Response, error) {
				return proxyResponse(http.StatusOK, http.Header{"Content-Type": []string{"text/event-stream"}}, strings.Join([]string{
					`data: {"model":"vendor-one","choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n",
					`data: {"model":"vendor-one","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n",
					`data: {"model":"vendor-one","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":1}}}` + "\n\n",
					"data: [DONE]\n\n",
				}, "")), nil
			},
		}}
		service, _ := NewService(store, outbound)
		recorder := httptest.NewRecorder()
		service.ServeProtocol(recorder, chatProxyRequest(context.Background(), true), channel.ProtocolOpenAIChat, "", false)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "partial") || strings.Contains(recorder.Body.String(), "[DONE]") {
			t.Fatalf("stream settlement failure terminal = %d %s", recorder.Code, recorder.Body.String())
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		if len(store.completed) != 1 || store.completed[0].Status != AttemptIncomplete || !store.completed[0].SemanticCommitted || store.completed[0].Usage != nil || store.completed[0].ErrorCode != "settlement_failed" || len(store.finalized) != 2 || store.finalized[0].Status != CallSucceeded || store.finalized[1].Status != CallIncomplete {
			t.Fatalf("stream compensation = attempts:%+v finalizers:%+v", store.completed, store.finalized)
		}
	})
}

func TestProxyStreamingFallbackBeforeCommitAndLocksAfterCommit(t *testing.T) {
	t.Run("precommit failure falls back and terminal waits", func(t *testing.T) {
		candidates := []Candidate{
			proxyCandidate(1, "offer-one", "vendor-one", "upstream-key-one"),
			proxyCandidate(2, "offer-two", "vendor-two", "upstream-key-two"),
		}
		store := newProxyStore(candidates)
		outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
			"offer-one": func(*http.Request) (*http.Response, error) {
				return proxyResponse(http.StatusOK, http.Header{"Content-Type": []string{"text/event-stream"}}, strings.Join([]string{
					`data: {"id":"first","model":"vendor-one","choices":[{"delta":{"role":"assistant"}}]}` + "\n\n",
					"data: not-json\n\n",
				}, "")), nil
			},
			"offer-two": func(*http.Request) (*http.Response, error) {
				return proxyResponse(http.StatusOK, http.Header{"Content-Type": []string{"text/event-stream"}}, strings.Join([]string{
					`data: {"id":"second","model":"vendor-two","choices":[{"delta":{"role":"assistant"}}]}` + "\n\n",
					`data: {"id":"second","model":"vendor-two","choices":[{"delta":{"content":"hello"}}]}` + "\n\n",
					`data: {"id":"second","model":"vendor-two","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n",
					`data: {"id":"second","model":"vendor-two","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":1}}}` + "\n\n",
					"data: [DONE]\n\n",
				}, "")), nil
			},
		}}
		service, _ := NewService(store, outbound)
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"canonical/model","stream":true,"messages":[]}`))
		request.Header.Set("Authorization", "Bearer oma_live_"+strings.Repeat("b", 43))
		recorder := httptest.NewRecorder()
		service.ServeProtocol(recorder, request, channel.ProtocolOpenAIChat, "", false)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "hello") || !strings.Contains(recorder.Body.String(), "[DONE]") || strings.Contains(recorder.Body.String(), "vendor-two") {
			t.Fatalf("stream fallback response = %d %s", recorder.Code, recorder.Body.String())
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		if len(store.completed) != 2 || store.completed[0].Status != AttemptFailed || store.completed[0].SemanticCommitted || store.completed[1].Status != AttemptSucceeded || !store.completed[1].SemanticCommitted || !store.completed[1].MeasureTPS {
			t.Fatalf("stream attempt facts = %+v", store.completed)
		}
		if len(store.committed) != 1 || store.committed[0] != "attempt-2" || len(store.finalized) != 1 || store.finalized[0].Status != CallSucceeded {
			t.Fatalf("stream commit/finalize facts = marks:%+v final:%+v", store.committed, store.finalized)
		}
	})

	t.Run("postcommit failure never falls back", func(t *testing.T) {
		candidates := []Candidate{
			proxyCandidate(1, "offer-one", "vendor-one", "upstream-key-one"),
			proxyCandidate(2, "offer-two", "vendor-two", "upstream-key-two"),
		}
		store := newProxyStore(candidates)
		outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
			"offer-one": func(*http.Request) (*http.Response, error) {
				return proxyResponse(http.StatusOK, http.Header{"Content-Type": []string{"text/event-stream"}}, `data: {"model":"vendor-one","choices":[{"delta":{"content":"committed"}}]}

data: not-json

`), nil
			},
			"offer-two": func(*http.Request) (*http.Response, error) {
				t.Fatal("second candidate was called after semantic commit")
				return nil, errors.New("unreachable")
			},
		}}
		service, _ := NewService(store, outbound)
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"canonical/model","stream":true,"messages":[]}`))
		request.Header.Set("Authorization", "Bearer oma_live_"+strings.Repeat("c", 43))
		recorder := httptest.NewRecorder()
		service.ServeProtocol(recorder, request, channel.ProtocolOpenAIChat, "", false)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "committed") {
			t.Fatalf("postcommit response = %d %s", recorder.Code, recorder.Body.String())
		}
		if len(outbound.snapshotRequests()) != 1 {
			t.Fatalf("postcommit upstream requests = %d, want 1", len(outbound.snapshotRequests()))
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		if len(store.completed) != 1 || store.completed[0].Status != AttemptIncomplete || !store.completed[0].SemanticCommitted || len(store.finalized) != 1 || store.finalized[0].Status != CallIncomplete {
			t.Fatalf("postcommit facts = attempts:%+v final:%+v", store.completed, store.finalized)
		}
	})
}

func TestProxyRejectsUnsupportedStreamingContentBeforeCommitAndFallsBack(t *testing.T) {
	tests := []struct {
		name     string
		protocol channel.Protocol
		body     string
		request  func() *http.Request
	}{
		{
			name: "chat audio", protocol: channel.ProtocolOpenAIChat,
			body:    `data: {"model":"vendor-one","choices":[{"index":0,"delta":{"audio":{"data":"c2FtcGxl"}}}]}` + "\n\n" + "data: not-json\n\n",
			request: func() *http.Request { return chatProxyRequest(context.Background(), true) },
		},
		{
			name: "gemini inline data", protocol: channel.ProtocolGemini,
			body: `data: {"modelVersion":"vendor-one","candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"audio/wav","data":"c2FtcGxl"}}]}}]}` + "\n\n" + "data: not-json\n\n",
			request: func() *http.Request {
				request := httptest.NewRequest(http.MethodPost, "/v1beta/models/canonical/model:streamGenerateContent?alt=sse", strings.NewReader(`{"contents":[]}`))
				request.Header.Set("x-goog-api-key", "oma_live_"+strings.Repeat("m", 43))
				return request
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates := []Candidate{
				proxyCandidate(1, "offer-one", "vendor-one", "key-one"),
				proxyCandidate(2, "offer-two", "vendor-two", "key-two"),
			}
			store := newProxyStore(candidates)
			outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
				"offer-one": func(*http.Request) (*http.Response, error) {
					return proxyResponse(http.StatusOK, sseHeader(), test.body), nil
				},
				"offer-two": func(*http.Request) (*http.Response, error) {
					if test.protocol == channel.ProtocolOpenAIChat {
						return proxyResponse(http.StatusOK, sseHeader(), validChatStream("vendor-two", "safe")), nil
					}
					return proxyResponse(http.StatusOK, sseHeader(), `data: {"modelVersion":"vendor-two","candidates":[{"index":0,"content":{"parts":[{"text":"safe"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`+"\n\n"), nil
				},
			}}
			service, _ := NewService(store, outbound)
			recorder := httptest.NewRecorder()
			request := test.request()
			canonical, stream := "", false
			if test.protocol == channel.ProtocolGemini {
				canonical, stream = "canonical/model", true
			}
			service.ServeProtocol(recorder, request, test.protocol, canonical, stream)
			if recorder.Code != http.StatusOK || len(outbound.snapshotRequests()) != 2 || !strings.Contains(recorder.Body.String(), "safe") {
				t.Fatalf("unsupported response fallback = %d %s / %d", recorder.Code, recorder.Body.String(), len(outbound.snapshotRequests()))
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if len(store.completed) != 2 || store.completed[0].Status != AttemptFailed || store.completed[0].SemanticCommitted || store.completed[1].Status != AttemptSucceeded || len(store.finalized) != 1 || store.finalized[0].Status != CallSucceeded {
				t.Fatalf("unsupported response fallback facts = attempts:%+v final:%+v", store.completed, store.finalized)
			}
		})
	}
}

// A 200 whose body is an error envelope still fails the attempt and fails over.
// A 200 that only carries usage is forwarded and settled: the platform reads
// usage and leaves response shape to the client.
func TestProxyRejectsFalse200ErrorEnvelopesAndSettlesUsageOnlyPayloads(t *testing.T) {
	candidates := []Candidate{
		proxyCandidate(1, "offer-error", "vendor-error", "key-error"),
		proxyCandidate(2, "offer-usage", "vendor-usage", "key-usage"),
		proxyCandidate(3, "offer-success", "vendor-success", "key-success"),
	}
	store := newProxyStore(candidates)
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-error": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, jsonHeader(), `{"object":"chat.completion","choices":[{"message":{"role":"assistant"},"finish_reason":"stop"}],"error":{"code":"false_success","message":"failed despite 200"},"usage":{"prompt_tokens":1,"completion_tokens":1}}`), nil
		},
		"offer-usage": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, jsonHeader(), `{"usage":{"prompt_tokens":1,"completion_tokens":1}}`), nil
		},
		"offer-success": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, jsonHeader(), validChatResponse("vendor-success")), nil
		},
	}}
	service, _ := NewService(store, outbound)
	request := chatProxyRequest(context.Background(), false)
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, request, channel.ProtocolOpenAIChat, "", false)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"prompt_tokens":1`) {
		t.Fatalf("usage-only response = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(outbound.snapshotRequests()) != 2 {
		t.Fatalf("usage-only payload triggered extra attempts: %#v", outbound.snapshotRequests())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 2 || store.completed[0].Status != AttemptFailed || store.completed[0].ErrorCode != "false_success" || store.completed[0].Usage != nil ||
		store.completed[1].Status != AttemptSucceeded || store.completed[1].Usage == nil {
		t.Fatalf("false 200 attempt results = %+v", store.completed)
	}
	if len(store.finalized) != 1 || store.finalized[0].FinalOfferID != "offer-usage" || store.finalized[0].Usage == nil {
		t.Fatalf("false 200 settlement = %+v", store.finalized)
	}
}

func TestProxyForwardsMultipleChatChoicesAndSettles(t *testing.T) {
	store := newProxyStore([]Candidate{proxyCandidate(1, "offer-one", "vendor-one", "key-one")})
	twoChoices := `{"id":"chat-2","object":"chat.completion","model":"vendor-one","choices":[` +
		`{"index":0,"message":{"role":"assistant","content":"first"},"finish_reason":"stop"},` +
		`{"index":1,"message":{"role":"assistant","content":"second"},"finish_reason":"stop"}` +
		`],"usage":{"prompt_tokens":4,"completion_tokens":2}}`
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-one": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, jsonHeader(), twoChoices), nil
		},
	}}
	service, _ := NewService(store, outbound)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"canonical/model","n":2,"messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer oma_live_"+strings.Repeat("q", 43))
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, request, channel.ProtocolOpenAIChat, "", false)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "second") {
		t.Fatalf("n=2 response = %d %s", recorder.Code, recorder.Body.String())
	}
	requests := outbound.snapshotRequests()
	if len(requests) != 1 || !strings.Contains(requests[0].body, `"n":2`) {
		t.Fatalf("n=2 upstream forward = %d %+v", len(requests), requests)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 1 || store.completed[0].Status != AttemptSucceeded || len(store.finalized) != 1 || store.finalized[0].Status != CallSucceeded {
		t.Fatalf("n=2 settlement facts: attempts:%+v final:%+v", store.completed, store.finalized)
	}
}

func TestProxyForwardsMultipleGeminiCandidatesAndSettles(t *testing.T) {
	store := newProxyStore([]Candidate{proxyCandidate(1, "offer-one", "vendor-one", "key-one")})
	twoCandidates := `{"modelVersion":"vendor-one","candidates":[` +
		`{"index":0,"content":{"parts":[{"text":"first"}]},"finishReason":"STOP"},` +
		`{"index":1,"content":{"parts":[{"text":"second"}]},"finishReason":"STOP"}` +
		`],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":6}}`
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-one": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, jsonHeader(), twoCandidates), nil
		},
	}}
	service, _ := NewService(store, outbound)
	request := httptest.NewRequest(http.MethodPost, "/v1beta/models/canonical/model:generateContent", strings.NewReader(`{"contents":[],"generationConfig":{"candidateCount":2}}`))
	request.Header.Set("X-Goog-Api-Key", "oma_live_"+strings.Repeat("g", 43))
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, request, channel.ProtocolGemini, "canonical/model", false)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "second") {
		t.Fatalf("Gemini multi-candidate response = %d %s", recorder.Code, recorder.Body.String())
	}
	requests := outbound.snapshotRequests()
	if len(requests) != 1 || !strings.Contains(requests[0].body, "candidateCount") {
		t.Fatalf("Gemini multi-candidate forward = %d %+v", len(requests), requests)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 1 || store.completed[0].Status != AttemptSucceeded || len(store.finalized) != 1 || store.finalized[0].Status != CallSucceeded {
		t.Fatalf("Gemini multi-candidate facts: attempts:%+v final:%+v", store.completed, store.finalized)
	}
}

func TestProxyRejectsCompressedSuccessAndFallsBack(t *testing.T) {
	candidates := []Candidate{
		proxyCandidate(1, "offer-gzip", "vendor-gzip", "key-gzip"),
		proxyCandidate(2, "offer-identity", "vendor-identity", "key-identity"),
	}
	store := newProxyStore(candidates)
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-gzip": func(*http.Request) (*http.Response, error) {
			header := jsonHeader()
			header.Set("Content-Encoding", "gzip")
			return proxyResponse(http.StatusOK, header, "compressed bytes must never be forwarded"), nil
		},
		"offer-identity": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, jsonHeader(), validChatResponse("vendor-identity")), nil
		},
	}}
	service, _ := NewService(store, outbound)
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, chatProxyRequest(context.Background(), false), channel.ProtocolOpenAIChat, "", false)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "compressed bytes") {
		t.Fatalf("gzip fallback response = %d %s", recorder.Code, recorder.Body.String())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 2 || store.completed[0].ErrorCode != "unsupported_content_encoding" || store.completed[1].Status != AttemptSucceeded {
		t.Fatalf("gzip attempt results = %+v", store.completed)
	}
}

func TestProxyBoundsTerminalFramesAndFallsBack(t *testing.T) {
	candidates := []Candidate{
		proxyCandidate(1, "offer-flood", "vendor-flood", "key-flood"),
		proxyCandidate(2, "offer-success", "vendor-success", "key-success"),
	}
	usageFrame := `data: {"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1}}` + "\n\n"
	flood := `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" + strings.Repeat(usageFrame, MaxTerminalFrames+1)
	store := newProxyStore(candidates)
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-flood": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, sseHeader(), flood), nil
		},
		"offer-success": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, sseHeader(), validChatStream("vendor-success", "bounded")), nil
		},
	}}
	service, _ := NewService(store, outbound)
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, chatProxyRequest(context.Background(), true), channel.ProtocolOpenAIChat, "", false)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "bounded") {
		t.Fatalf("terminal flood fallback = %d %s", recorder.Code, recorder.Body.String())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 2 || store.completed[0].ErrorCode != "terminal_flood" || store.completed[0].SemanticCommitted || store.completed[1].Status != AttemptSucceeded {
		t.Fatalf("terminal flood facts = %+v", store.completed)
	}
}

// Vendor-shaped streams used to be rejected before delivery: choice indexes
// outside the requested n, duplicated indexes and large tool-call indexes are all
// things real relays emit. The gateway now forwards them verbatim and settles the
// call as incomplete when nothing priceable came with it.
func TestProxyForwardsVendorShapedStreamsWithoutFailover(t *testing.T) {
	vendorStreams := []string{
		`data: {"choices":[{"index":1,"delta":{"content":"vendor-index"}}]}` + "\n\n",
		`data: {"choices":[{"index":0,"delta":{"content":"vendor-one"}},{"index":0,"delta":{"content":"vendor-duplicate"}}]}` + "\n\n",
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":200,"function":{"arguments":"vendor-args"}}]}}]}` + "\n\n",
	}
	for index, stream := range vendorStreams {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			store := newProxyStore([]Candidate{
				proxyCandidate(1, "offer-vendor", "vendor-one", "key-one"),
				proxyCandidate(2, "offer-next", "vendor-two", "key-two"),
			})
			outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
				"offer-vendor": func(*http.Request) (*http.Response, error) {
					return proxyResponse(http.StatusOK, sseHeader(), stream), nil
				},
				"offer-next": func(*http.Request) (*http.Response, error) {
					return proxyResponse(http.StatusOK, sseHeader(), validChatStream("vendor-two", "other-candidate")), nil
				},
			}}
			service, _ := NewService(store, outbound)
			recorder := httptest.NewRecorder()
			service.ServeProtocol(recorder, chatProxyRequest(context.Background(), true), channel.ProtocolOpenAIChat, "", false)
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "vendor-") || strings.Contains(recorder.Body.String(), "other-candidate") {
				t.Fatalf("vendor stream was not forwarded verbatim: %d %s", recorder.Code, recorder.Body.String())
			}
			if len(outbound.snapshotRequests()) != 1 {
				t.Fatalf("vendor stream triggered failover: %#v", outbound.snapshotRequests())
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if len(store.completed) != 1 || store.completed[0].Status != AttemptIncomplete {
				t.Fatalf("vendor stream facts = %+v", store.completed)
			}
		})
	}
}

func TestProxyReturnsNativeEnvelopeAfterAllSSEErrors(t *testing.T) {
	candidates := []Candidate{
		proxyCandidate(1, "offer-one", "vendor-one", "key-one"),
		proxyCandidate(2, "offer-two", "vendor-two", "key-two"),
	}
	errorFrame := "event: error\ndata: {\"type\":\"error\",\"code\":\"relay_busy\",\"message\":\"retry later\",\"secret\":\"DO_NOT_PERSIST\"}\n\n"
	store := newProxyStore(candidates)
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-one": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, sseHeader(), errorFrame), nil
		},
		"offer-two": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, sseHeader(), errorFrame), nil
		},
	}}
	service, _ := NewService(store, outbound)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"canonical/model","stream":true,"input":"hi"}`))
	request.Header.Set("Authorization", "Bearer oma_live_"+strings.Repeat("e", 43))
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, request, channel.ProtocolOpenAIResponse, "", false)
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "relay_busy") || strings.Contains(recorder.Body.String(), "DO_NOT_PERSIST") {
		t.Fatalf("native final stream error = %d %s", recorder.Code, recorder.Body.String())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 2 || store.completed[0].RawError != "retry later" || store.completed[1].RawError != "retry later" || strings.Contains(store.completed[0].RawError, "DO_NOT_PERSIST") || len(store.finalized) != 1 || store.finalized[0].Status != CallFailed {
		t.Fatalf("native error persistence = attempts:%+v final:%+v", store.completed, store.finalized)
	}
}

func TestProxyWritesPostCommitSSEErrorExactlyOnce(t *testing.T) {
	store := newProxyStore([]Candidate{proxyCandidate(1, "offer-one", "vendor-one", "key-one")})
	body := `data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n" +
		`data: {"error":{"code":"relay_busy","message":"retry later"}}` + "\n\n"
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-one": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, sseHeader(), body), nil
		},
	}}
	service, _ := NewService(store, outbound)
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, chatProxyRequest(context.Background(), true), channel.ProtocolOpenAIChat, "", false)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "partial") || strings.Count(recorder.Body.String(), "relay_busy") != 1 {
		t.Fatalf("post-commit SSE error = %d %s", recorder.Code, recorder.Body.String())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 1 || store.completed[0].Status != AttemptIncomplete || store.completed[0].ErrorCode != "relay_busy" || !store.completed[0].SemanticCommitted {
		t.Fatalf("post-commit SSE error facts = %+v", store.completed)
	}
}

func TestProxyAcceptsGeminiSingleTerminalFrame(t *testing.T) {
	store := newProxyStore([]Candidate{proxyCandidate(1, "offer-gemini", "vendor-gemini", "key-gemini")})
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-gemini": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, sseHeader(), `data: {"modelVersion":"vendor-gemini","candidates":[{"content":{"parts":[{"text":"short"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1}}`+"\n\n"), nil
		},
	}}
	service, _ := NewService(store, outbound)
	request := httptest.NewRequest(http.MethodPost, "/v1beta/models/canonical/model:streamGenerateContent?alt=sse", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	request.Header.Set("x-goog-api-key", "oma_live_"+strings.Repeat("f", 43))
	recorder := &deadlineResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	service.ServeProtocol(recorder, request, channel.ProtocolGemini, "canonical/model", true)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "short") || !strings.Contains(recorder.Body.String(), "canonical/model") || recorder.Header().Get("Cache-Control") != "no-store, no-transform" || time.Until(recorder.deadline) < 80*time.Second {
		t.Fatalf("Gemini terminal response = %d %s", recorder.Code, recorder.Body.String())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 1 || store.completed[0].Status != AttemptSucceeded || !store.completed[0].SemanticCommitted || !store.completed[0].TTFTObserved || !store.completed[0].MeasureTPS || store.completed[0].TTFT <= 0 || len(store.finalized) != 1 || store.finalized[0].Status != CallSucceeded {
		t.Fatalf("Gemini terminal facts = attempts:%+v final:%+v", store.completed, store.finalized)
	}
}

func TestProxyAcceptsResponsesSingleSemanticTerminalFrame(t *testing.T) {
	candidate := proxyCandidate(1, "offer-responses", "vendor-responses", "key-responses")
	candidate.Lease.Protocol = channel.ProtocolOpenAIResponse
	store := newProxyStore([]Candidate{candidate})
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-responses": func(*http.Request) (*http.Response, error) {
			body := `data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"vendor-responses","output":[{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"one frame"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}` + "\n\n"
			return proxyResponse(http.StatusOK, sseHeader(), body), nil
		},
	}}
	service, _ := NewService(store, outbound)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"canonical/model","stream":true,"input":"hi"}`))
	request.Header.Set("Authorization", "Bearer oma_live_"+strings.Repeat("r", 43))
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, request, channel.ProtocolOpenAIResponse, "", false)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "one frame") || !strings.Contains(recorder.Body.String(), "canonical/model") {
		t.Fatalf("Responses terminal response = %d %s", recorder.Code, recorder.Body.String())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 1 || store.completed[0].Status != AttemptSucceeded || !store.completed[0].SemanticCommitted || !store.completed[0].TTFTObserved || !store.completed[0].MeasureTPS || store.completed[0].TTFT <= 0 || len(store.committed) != 1 || len(store.finalized) != 1 {
		t.Fatalf("Responses terminal facts = attempts:%+v marks:%+v final:%+v", store.completed, store.committed, store.finalized)
	}
}

func TestProxyAllowsAnthropicPingDuringWindDown(t *testing.T) {
	store := newProxyStore([]Candidate{proxyCandidate(1, "offer-anthropic", "vendor-claude", "key-anthropic")})
	body := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","model":"vendor-claude","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":2,"output_tokens":0}}}` + "\n\n",
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}` + "\n\n",
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}` + "\n\n",
		`event: ping` + "\n" + `data: {"type":"ping"}` + "\n\n",
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}` + "\n\n",
	}, "")
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-anthropic": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, sseHeader(), body), nil
		},
	}}
	service, _ := NewService(store, outbound)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"canonical/model","stream":true,"messages":[]}`))
	request.Header.Set("x-api-key", "oma_live_"+strings.Repeat("g", 43))
	request.Header.Set("anthropic-version", "2023-06-01")
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, request, channel.ProtocolAnthropic, "", false)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "hello") || !strings.Contains(recorder.Body.String(), `"type":"ping"`) || !strings.Contains(recorder.Body.String(), `"type":"message_stop"`) {
		t.Fatalf("Anthropic wind-down response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestClientCancellationUsesBoundedDetachedPersistence(t *testing.T) {
	store := newProxyStore([]Candidate{proxyCandidate(1, "offer-cancel", "vendor-cancel", "key-cancel")})
	started := make(chan struct{})
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-cancel": func(request *http.Request) (*http.Response, error) {
			close(started)
			<-request.Context().Done()
			return nil, request.Context().Err()
		},
	}}
	service, _ := NewService(store, outbound)
	ctx, cancel := context.WithCancel(context.Background())
	request := chatProxyRequest(ctx, false)
	done := make(chan struct{})
	go func() {
		service.ServeProtocol(httptest.NewRecorder(), request, channel.ProtocolOpenAIChat, "", false)
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled proxy did not finish")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.completed) != 1 || store.completed[0].Status != AttemptCancelled || len(store.finalized) != 1 || store.finalized[0].Status != CallCancelled {
		t.Fatalf("cancel facts = attempts:%+v final:%+v", store.completed, store.finalized)
	}
	for _, observation := range append(append([]persistenceObservation(nil), store.completeContexts...), store.finalizeContexts...) {
		if observation.err != nil || !observation.hasDeadline || observation.remaining <= 0 || observation.remaining > PersistenceTimeout {
			t.Fatalf("persistence context was cancelled or unbounded: %+v", observation)
		}
	}
}

func TestAuthenticationAndHeaderIsolation(t *testing.T) {
	correct := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	correct.Header.Set("Authorization", "Bearer oma_live_"+strings.Repeat("d", 43))
	if _, err := extractPlatformCredential(correct, channel.ProtocolOpenAIChat); err != nil {
		t.Fatal(err)
	}
	invalid := []*http.Request{
		httpRequestWithHeaders("/v1/chat/completions", map[string][]string{"Authorization": {"Bearer key"}, "x-api-key": {"key"}}),
		httpRequestWithHeaders("/v1/chat/completions", map[string][]string{"Authorization": {"Bearer one", "Bearer two"}}),
		httpRequestWithHeaders("/v1/messages", map[string][]string{"Authorization": {"Bearer wrong"}}),
	}
	for _, request := range invalid {
		protocol := channel.ProtocolOpenAIChat
		if request.URL.Path == "/v1/messages" {
			protocol = channel.ProtocolAnthropic
		} else if strings.HasPrefix(request.URL.Path, "/v1beta/") {
			protocol = channel.ProtocolGemini
		}
		if _, err := extractPlatformCredential(request, protocol); !errors.Is(err, ErrInvalidAPIKey) {
			t.Fatalf("invalid authentication accepted for %s: %v", request.URL, err)
		}
	}
	if err := validateProtocolQuery(channel.ProtocolGemini, false, "key=secret"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Gemini query credential accepted: %v", err)
	}

	source := http.Header{
		"Authorization": []string{"Bearer platform"}, "x-api-key": []string{"platform"}, "x-goog-api-key": []string{"platform"},
		"Cookie": []string{"secret"}, "Forwarded": []string{"for=private"}, "X-Forwarded-For": []string{"private"},
		"Connection": []string{"X-Hop"}, "X-Hop": []string{"secret"}, "Host": []string{"evil"}, "X-Safe": []string{"kept"}, "Traceparent": []string{"trace"}, "Baggage": []string{"secret"},
	}
	target := make(http.Header)
	copyOutboundHeaders(target, source, channel.ProtocolOpenAIChat, false)
	for _, blocked := range []string{"Authorization", "x-api-key", "x-goog-api-key", "Cookie", "Forwarded", "X-Forwarded-For", "Connection", "X-Hop", "Host", "Traceparent", "Baggage"} {
		if target.Get(blocked) != "" {
			t.Fatalf("blocked outbound header %s escaped: %v", blocked, target)
		}
	}
	if target.Get("X-Safe") != "kept" || target.Get("Content-Type") != "application/json" || target.Get("Accept") != "application/json" {
		t.Fatalf("outbound header passthrough mismatch: %v", target)
	}
}

// Streams that carry no priceable usage are delivered and settled as incomplete
// with no charge instead of being dropped or failed over.
func TestProxySettlesUnpriceableStreamsAsIncomplete(t *testing.T) {
	semantic := `data: {"choices":[{"index":0,"delta":{"content":"hello-from-vendor"}}]}` + "\n\n"
	streams := map[string]string{
		"no-usage": semantic + "data: [DONE]\n\n",
		"unpriceable-usage": semantic +
			`data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"prompt_tokens_details":{"audio_tokens":1}}}` + "\n\ndata: [DONE]\n\n",
		// A valid terminal snapshot followed by an unpriceable frame in the
		// terminal zone must not restore the call's settleability.
		"termin" + "al-zone-invalid-usage": semantic +
			`data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2}}` + "\n\n" +
			`data: {"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":9,"prompt_tokens_details":{"image_tokens":1}}}` + "\n\ndata: [DONE]\n\n",
	}
	for name, stream := range streams {
		t.Run(name, func(t *testing.T) {
			store := newProxyStore([]Candidate{
				proxyCandidate(1, "offer-vendor", "vendor-one", "key-one"),
				proxyCandidate(2, "offer-next", "vendor-two", "key-two"),
			})
			outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
				"offer-vendor": func(*http.Request) (*http.Response, error) {
					return proxyResponse(http.StatusOK, sseHeader(), stream), nil
				},
				"offer-next": func(*http.Request) (*http.Response, error) {
					return proxyResponse(http.StatusOK, sseHeader(), validChatStream("vendor-two", "other-candidate")), nil
				},
			}}
			service, _ := NewService(store, outbound)
			recorder := httptest.NewRecorder()
			service.ServeProtocol(recorder, chatProxyRequest(context.Background(), true), channel.ProtocolOpenAIChat, "", false)
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "hello-from-vendor") {
				t.Fatalf("unpriceable stream = %d %s", recorder.Code, recorder.Body.String())
			}
			if requests := outbound.snapshotRequests(); len(requests) != 1 {
				t.Fatalf("unpriceable stream failed over: %#v", requests)
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if len(store.completed) != 1 || store.completed[0].Status != AttemptIncomplete || store.completed[0].Usage != nil {
				t.Fatalf("unpriceable attempt facts = %+v", store.completed)
			}
			if len(store.finalized) != 1 || store.finalized[0].Status != CallIncomplete || store.finalized[0].Usage != nil {
				t.Fatalf("unpriceable settlement = %+v", store.finalized)
			}
		})
	}
}

// Vendor protocol features reach the upstream, while the platform credential
// cannot leak through a forwarded header name, header value, or query value.
func TestProxyForwardsVendorFeaturesButNotPlatformCredentials(t *testing.T) {
	secret := "oma_live_" + strings.Repeat("c", 43)
	newService := func() (*Service, *proxyOutbound) {
		store := newProxyStore([]Candidate{proxyCandidate(1, "offer-one", "vendor-one", "upstream-key-one")})
		outbound := &proxyOutbound{
			endpoints: map[string]string{"offer-one": "https://upstream.example/v1/chat/completions"},
			handlers: map[string]func(*http.Request) (*http.Response, error){
				"offer-one": func(*http.Request) (*http.Response, error) {
					return proxyResponse(http.StatusOK, jsonHeader(), validChatResponse("vendor-one")), nil
				},
			},
		}
		service, _ := NewService(store, outbound)
		return service, outbound
	}
	withSecret := func(mutate func(*http.Request)) *http.Request {
		request := chatProxyRequest(context.Background(), false)
		request.Header.Set("Authorization", "Bearer "+secret)
		mutate(request)
		return request
	}

	service, outbound := newService()
	recorded := withSecret(func(request *http.Request) {
		request.URL.RawQuery = "trace=1&vendor=2"
		request.Header.Set("OpenAI-Beta", "assistants=v2")
		request.Header.Set("X-Stainless-Lang", "go")
	})
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, recorded, channel.ProtocolOpenAIChat, "", false)
	if recorder.Code != http.StatusOK {
		t.Fatalf("vendor feature request = %d %s", recorder.Code, recorder.Body.String())
	}
	requests := outbound.snapshotRequests()
	if len(requests) != 1 || !strings.Contains(requests[0].url, "trace=1") || !strings.Contains(requests[0].url, "vendor=2") {
		t.Fatalf("client query did not reach the upstream: %#v", requests)
	}
	if requests[0].header.Get("OpenAI-Beta") != "assistants=v2" || requests[0].header.Get("X-Stainless-Lang") != "go" {
		t.Fatalf("vendor headers did not reach the upstream: %v", requests[0].header)
	}

	leaks := map[string]func(*http.Request){
		"header-value": func(request *http.Request) { request.Header.Set("X-Debug", secret) },
		"header-name":  func(request *http.Request) { request.Header.Set("X-"+secret, "1") },
		"query-value": func(request *http.Request) {
			request.URL.RawQuery = "vendor=" + strings.Replace(secret, "oma", "%6Fma", 1)
		},
	}
	for name, mutate := range leaks {
		t.Run(name, func(t *testing.T) {
			service, outbound := newService()
			recorder := httptest.NewRecorder()
			service.ServeProtocol(recorder, withSecret(mutate), channel.ProtocolOpenAIChat, "", false)
			if recorder.Code == http.StatusOK {
				t.Fatalf("credential leak was forwarded: %d %s", recorder.Code, recorder.Body.String())
			}
			if len(outbound.snapshotRequests()) != 0 {
				t.Fatalf("credential leak reached the upstream: %#v", outbound.snapshotRequests())
			}
		})
	}
}

// The gateway must not force an encoding the upstream cannot negotiate: it lets
// the transport agree on gzip and decodes the body before settling.
func TestProxyLetsTheTransportDecodeCompressedResponses(t *testing.T) {
	observed := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		_, _ = writer.Write([]byte(validChatResponse("vendor-gzip")))
		_ = writer.Close()
	}))
	defer server.Close()
	store := newProxyStore([]Candidate{proxyCandidate(1, "offer-gzip", "vendor-gzip", "key-gzip")})
	outbound := &proxyOutbound{
		client:    server.Client(),
		endpoints: map[string]string{"offer-gzip": server.URL + "/v1/chat/completions"},
	}
	service, _ := NewService(store, outbound)
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, chatProxyRequest(context.Background(), false), channel.ProtocolOpenAIChat, "", false)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "canonical/model") {
		t.Fatalf("compressed upstream response = %d %s", recorder.Code, recorder.Body.String())
	}
	if acceptEncoding := <-observed; acceptEncoding != "gzip" {
		t.Fatalf("upstream accept-encoding = %q, want the transport's gzip negotiation", acceptEncoding)
	}
}

// Usage reported by frames after the first terminal event — including repeated
// terminal frames — must not rewrite the settled snapshot.
func TestProxyKeepsTheTerminalUsageSnapshot(t *testing.T) {
	trailingFrame := `data: {"choices":[{"index":0,"delta":{"content":"late"}}],"usage":{"prompt_tokens":100,"completion_tokens":100}}` + "\n\n"
	repeatedTerminal := `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":100}}` + "\n\n"
	streams := map[string]string{
		"trailing-frame": `data: {"choices":[{"index":0,"delta":{"content":"hello-from-vendor"}}]}` + "\n\n" +
			`data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2}}` + "\n\n" +
			trailingFrame + "data: [DONE]\n\n",
		"repeated-terminal-frame": `data: {"choices":[{"index":0,"delta":{"content":"hello-from-vendor"}}]}` + "\n\n" +
			`data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2}}` + "\n\n" +
			repeatedTerminal + "data: [DONE]\n\n",
	}
	for name, stream := range streams {
		t.Run(name, func(t *testing.T) {
			store, outbound := newVendorStreamStore(stream)
			service, _ := NewService(store, outbound)
			recorder := httptest.NewRecorder()
			service.ServeProtocol(recorder, chatProxyRequest(context.Background(), true), channel.ProtocolOpenAIChat, "", false)
			assertDeliveredWithoutFailover(t, recorder, outbound, "hello-from-vendor")
			store.mu.Lock()
			defer store.mu.Unlock()
			if len(store.finalized) != 1 || store.finalized[0].Status != CallSucceeded || store.finalized[0].Usage == nil {
				t.Fatalf("terminal snapshot settlement = %+v", store.finalized)
			}
			if usage := store.finalized[0].Usage; usage.InputTokens != 4 || usage.OutputTokens != 2 {
				t.Fatalf("post-terminal usage leaked into settlement: %+v", usage)
			}
		})
	}
}

// A relay may close the stream without a terminal frame. Output that already
// arrived must still reach the client instead of being discarded and failed over.
func TestProxyDeliversStreamsThatEndWithoutATerminalFrame(t *testing.T) {
	chatWithoutDone := `data: {"choices":[{"index":0,"delta":{"content":"hello-from-vendor"}}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2}}` + "\n\n"
	responsesWithoutStatus := `data: {"type":"response.output_text.delta","delta":"hello-from-vendor"}` + "\n\n" +
		`data: {"type":"response.completed","response":{"id":"resp-1"}}` + "\n\n"

	t.Run("chat-usage-without-done-is-charged", func(t *testing.T) {
		store, outbound := newVendorStreamStore(chatWithoutDone)
		service, _ := NewService(store, outbound)
		recorder := httptest.NewRecorder()
		service.ServeProtocol(recorder, chatProxyRequest(context.Background(), true), channel.ProtocolOpenAIChat, "", false)
		assertDeliveredWithoutFailover(t, recorder, outbound, "hello-from-vendor")
		store.mu.Lock()
		defer store.mu.Unlock()
		if len(store.completed) != 1 || store.completed[0].Status != AttemptSucceeded || store.completed[0].Usage == nil {
			t.Fatalf("terminal-less chat attempt facts = %+v", store.completed)
		}
		if len(store.finalized) != 1 || store.finalized[0].Status != CallSucceeded || store.finalized[0].Usage == nil {
			t.Fatalf("terminal-less chat settlement = %+v", store.finalized)
		}
	})

	t.Run("responses-terminal-without-finish-is-delivered", func(t *testing.T) {
		store, outbound := newVendorStreamStore(responsesWithoutStatus)
		service, _ := NewService(store, outbound)
		recorder := httptest.NewRecorder()
		service.ServeProtocol(recorder, responsesProxyRequest(context.Background(), true), channel.ProtocolOpenAIResponse, "", false)
		assertDeliveredWithoutFailover(t, recorder, outbound, "hello-from-vendor")
		store.mu.Lock()
		defer store.mu.Unlock()
		if len(store.completed) != 1 || store.completed[0].Status != AttemptIncomplete || store.completed[0].Usage != nil {
			t.Fatalf("unfinished responses attempt facts = %+v", store.completed)
		}
		if len(store.finalized) != 1 || store.finalized[0].Status != CallIncomplete {
			t.Fatalf("unfinished responses settlement = %+v", store.finalized)
		}
	})

	t.Run("usage-only-stream-is-billed-per-usage", func(t *testing.T) {
		// Accepted tradeoff (ADR-0014 / ADR-0015): a 200 whose only payload is
		// usage is forwarded and settled on that usage, including as a stream.
		store, outbound := newVendorStreamStore(`data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2}}` + "\n\n")
		service, _ := NewService(store, outbound)
		recorder := httptest.NewRecorder()
		service.ServeProtocol(recorder, chatProxyRequest(context.Background(), true), channel.ProtocolOpenAIChat, "", false)
		assertDeliveredWithoutFailover(t, recorder, outbound, "prompt_tokens")
		store.mu.Lock()
		defer store.mu.Unlock()
		if len(store.completed) != 1 || store.completed[0].Status != AttemptSucceeded || store.completed[0].Usage == nil {
			t.Fatalf("usage-only attempt facts = %+v", store.completed)
		}
		if len(store.finalized) != 1 || store.finalized[0].Status != CallSucceeded || store.finalized[0].Usage == nil {
			t.Fatalf("usage-only settlement = %+v", store.finalized)
		}
	})
}

// A stream that closes with nothing usable is still a failure the caller may
// retry on the next candidate.
func TestProxyFailsOverWhenAStreamProducesNothing(t *testing.T) {
	store, outbound := newVendorStreamStore(`data: {"choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n" + "data: keepalive\n\n")
	service, _ := NewService(store, outbound)
	recorder := httptest.NewRecorder()
	service.ServeProtocol(recorder, chatProxyRequest(context.Background(), true), channel.ProtocolOpenAIChat, "", false)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "other-candidate") {
		t.Fatalf("empty stream fallback = %d %s", recorder.Code, recorder.Body.String())
	}
	if requests := outbound.snapshotRequests(); len(requests) != 2 {
		t.Fatalf("empty stream attempts = %d, want 2", len(requests))
	}
}

func newVendorStreamStore(vendorStream string) (*proxyStore, *proxyOutbound) {
	store := newProxyStore([]Candidate{
		proxyCandidate(1, "offer-vendor", "vendor-one", "key-one"),
		proxyCandidate(2, "offer-next", "vendor-two", "key-two"),
	})
	outbound := &proxyOutbound{handlers: map[string]func(*http.Request) (*http.Response, error){
		"offer-vendor": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, sseHeader(), vendorStream), nil
		},
		"offer-next": func(*http.Request) (*http.Response, error) {
			return proxyResponse(http.StatusOK, sseHeader(), validChatStream("vendor-two", "other-candidate")), nil
		},
	}}
	return store, outbound
}

func assertDeliveredWithoutFailover(t *testing.T, recorder *httptest.ResponseRecorder, outbound *proxyOutbound, content string) {
	t.Helper()
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), content) {
		t.Fatalf("delivery = %d %s", recorder.Code, recorder.Body.String())
	}
	if requests := outbound.snapshotRequests(); len(requests) != 1 {
		t.Fatalf("delivered stream failed over: %#v", requests)
	}
}

func jsonHeader() http.Header {
	return http.Header{"Content-Type": []string{"application/json"}}
}

func sseHeader() http.Header {
	return http.Header{"Content-Type": []string{"text/event-stream"}}
}

func validChatResponse(model string) string {
	return fmt.Sprintf(`{"id":"result","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`, model)
}

func validChatStream(model, content string) string {
	return fmt.Sprintf("data: {\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n"+
		"data: {\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"+
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\n"+
		"data: [DONE]\n\n", model, content, model)
}

func chatProxyRequest(ctx context.Context, stream bool) *http.Request {
	body := fmt.Sprintf(`{"model":"canonical/model","stream":%t,"messages":[{"role":"user","content":"hi"}]}`, stream)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer oma_live_"+strings.Repeat("z", 43))
	return request
}

func responsesProxyRequest(ctx context.Context, stream bool) *http.Request {
	body := fmt.Sprintf(`{"model":"canonical/model","stream":%t,"input":"hi"}`, stream)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer oma_live_"+strings.Repeat("z", 43))
	return request
}

func proxyCandidate(priority int, offerID, upstreamModel, credential string) Candidate {
	return Candidate{Priority: priority, Lease: channel.RoutingLease{
		OfferID: offerID, ChannelID: "channel-" + offerID, ProviderAccountID: "provider-" + offerID,
		ModelID: "canonical/model", Protocol: channel.ProtocolOpenAIChat, UpstreamModelID: upstreamModel,
		Credential: credential, ContextWindow: 1000,
	}}
}

func assertIsolatedUpstreamRequest(t *testing.T, request *http.Request, wantAuthorization string) {
	t.Helper()
	if request.Header.Get("Authorization") != wantAuthorization || request.Header.Get("Accept-Encoding") != "" {
		t.Fatalf("upstream authentication/encoding = %v", request.Header)
	}
	for _, blocked := range []string{"x-api-key", "x-goog-api-key", "Cookie", "Forwarded", "X-Forwarded-For", "X-Client-Leak", "Idempotency-Key", "Traceparent", "Baggage"} {
		if request.Header.Get(blocked) != "" {
			t.Fatalf("client header %s leaked upstream: %v", blocked, request.Header)
		}
	}
}

func proxyResponse(status int, header http.Header, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func httpRequestWithHeaders(path string, headers map[string][]string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, nil)
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	return request
}

type capturedProxyRequest struct {
	offerID string
	body    string
	header  http.Header
	url     string
}

type proxyOutbound struct {
	mu           sync.Mutex
	handlers     map[string]func(*http.Request) (*http.Response, error)
	targetErrors map[string]error
	endpoints    map[string]string
	requests     []capturedProxyRequest
	// client replaces the capturing transport; used by tests that need the real
	// net/http transport behaviour (for example gzip negotiation).
	client *http.Client
}

func (o *proxyOutbound) ResolveRoutingLeasesWithStore(context.Context, channel.RoutingStore, []string) ([]channel.PoolOfferStatus, []channel.RoutingLease, error) {
	return nil, nil, nil
}

func (o *proxyOutbound) ProxyTarget(_ context.Context, lease channel.RoutingLease, _ bool, _ time.Duration) (*http.Client, string, error) {
	if err := o.targetErrors[lease.OfferID]; err != nil {
		return nil, "", err
	}
	handler := o.handlers[lease.OfferID]
	if o.client != nil {
		endpoint := o.endpoints[lease.OfferID]
		return o.client, endpoint, nil
	}
	if handler == nil {
		return nil, "", errors.New("missing proxy test handler")
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		o.mu.Lock()
		o.requests = append(o.requests, capturedProxyRequest{offerID: lease.OfferID, body: string(body), header: request.Header.Clone(), url: request.URL.String()})
		o.mu.Unlock()
		return handler(request)
	})}
	endpoint := o.endpoints[lease.OfferID]
	if endpoint == "" {
		endpoint = "https://upstream.example/v1/chat/completions"
	}
	return client, endpoint, nil
}

func (o *proxyOutbound) snapshotRequests() []capturedProxyRequest {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]capturedProxyRequest(nil), o.requests...)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type deadlineResponseWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *deadlineResponseWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

type proxyStore struct {
	mu                      sync.Mutex
	plan                    CallPlan
	begun                   int
	started                 []Candidate
	completed               []AttemptResult
	completedIDs            []string
	committed               []string
	finalized               []FinalizeOutcome
	completeContexts        []persistenceObservation
	finalizeContexts        []persistenceObservation
	nextAttempt             int
	successFinalizeFailures int
}

type persistenceObservation struct {
	err         error
	hasDeadline bool
	remaining   time.Duration
}

func observePersistenceContext(ctx context.Context) persistenceObservation {
	deadline, hasDeadline := ctx.Deadline()
	remaining := time.Duration(0)
	if hasDeadline {
		remaining = time.Until(deadline)
	}
	return persistenceObservation{err: ctx.Err(), hasDeadline: hasDeadline, remaining: remaining}
}

func newProxyStore(candidates []Candidate) *proxyStore {
	for index := range candidates {
		candidates[index].LeaseGeneration = 1
	}
	return &proxyStore{plan: CallPlan{Call: Call{ID: "call-proxy", Status: CallInProgress, LeaseGeneration: 1}, Candidates: candidates}}
}

func (s *proxyStore) AuthenticateAPIKey(context.Context, [32]byte) (AuthenticatedKey, error) {
	return AuthenticatedKey{ID: "key-proxy", OwnerAccountID: "consumer-proxy", Generation: 1}, nil
}

func (s *proxyStore) BeginCall(context.Context, BeginCallRequest, LeaseResolver) (CallPlan, error) {
	s.mu.Lock()
	s.begun++
	s.mu.Unlock()
	return s.plan, nil
}

func (s *proxyStore) StartAttempt(_ context.Context, _ string, candidate Candidate) (Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextAttempt++
	s.started = append(s.started, candidate)
	return Attempt{ID: "attempt-" + strconv.Itoa(s.nextAttempt), CallID: s.plan.Call.ID, OfferID: candidate.Lease.OfferID, LeaseGeneration: candidate.LeaseGeneration, Status: AttemptInProgress}, nil
}

func (s *proxyStore) CompleteAttempt(ctx context.Context, attemptID string, result AttemptResult) (Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = append(s.completed, result)
	s.completedIDs = append(s.completedIDs, attemptID)
	s.completeContexts = append(s.completeContexts, observePersistenceContext(ctx))
	return Attempt{ID: attemptID, CallID: s.plan.Call.ID, Status: result.Status, SemanticCommitted: result.SemanticCommitted, Usage: result.Usage}, nil
}

func (s *proxyStore) MarkAttemptCommitted(_ context.Context, attemptID string, observation AttemptCommitObservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.committed = append(s.committed, attemptID)
	for index := range s.completed {
		if s.completedIDs[index] == attemptID && s.completed[index].LeaseGeneration == observation.LeaseGeneration && !s.completed[index].SemanticCommitted {
			s.completed[index].SemanticCommitted = true
			s.completed[index].TTFTObserved = true
			s.completed[index].TTFT = observation.TTFT
			s.completed[index].Duration = observation.Duration
			s.completed[index].MeasureTPS = observation.MeasureTPS
		}
	}
	return nil
}

func (s *proxyStore) HeartbeatCall(context.Context, string, int64) error { return nil }

func (s *proxyStore) FinalizeCall(ctx context.Context, _ string, outcome FinalizeOutcome) (Call, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalized = append(s.finalized, outcome)
	observation := observePersistenceContext(ctx)
	s.finalizeContexts = append(s.finalizeContexts, observation)
	if outcome.Status == CallSucceeded && s.successFinalizeFailures > 0 {
		s.successFinalizeFailures--
		return Call{}, errors.New("injected success settlement failure")
	}
	if outcome.SuccessAttempt != nil {
		s.completed = append(s.completed, *outcome.SuccessAttempt)
		s.completedIDs = append(s.completedIDs, outcome.SuccessAttemptID)
		s.completeContexts = append(s.completeContexts, observation)
	}
	status := outcome.Status
	if status == CallSucceeded {
		status = CallPendingDelivery
	}
	return Call{ID: s.plan.Call.ID, Status: status, LeaseGeneration: outcome.LeaseGeneration, CompletionReason: outcome.CompletionReason, FinalOfferID: outcome.FinalOfferID, FinalHTTPStatus: outcome.HTTPStatus, Usage: outcome.Usage}, nil
}

func (s *proxyStore) ConfirmCallDelivery(_ context.Context, _ string, leaseGeneration int64) (Call, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.completed {
		if s.completed[index].Status == AttemptSucceeded {
			s.completed[index].SemanticCommitted = true
		}
	}
	return Call{ID: s.plan.Call.ID, Status: CallSucceeded, LeaseGeneration: leaseGeneration}, nil
}

func (s *proxyStore) CompensateCallDelivery(_ context.Context, _ string, leaseGeneration int64, reason string) (Call, error) {
	return Call{ID: s.plan.Call.ID, Status: CallIncomplete, LeaseGeneration: leaseGeneration, CompletionReason: reason}, nil
}

func (s *proxyStore) CreateAPIKey(context.Context, string, string, string, [32]byte, []PoolInput) (APIKey, error) {
	return APIKey{}, errors.New("not implemented")
}
func (s *proxyStore) ListAPIKeys(context.Context, string) ([]APIKey, error) {
	return nil, errors.New("not implemented")
}
func (s *proxyStore) GetAPIKey(context.Context, string, string) (APIKey, error) {
	return APIKey{}, errors.New("not implemented")
}
func (s *proxyStore) UpdateAPIKey(context.Context, string, string, int64, KeyConfigInput) (APIKey, error) {
	return APIKey{}, errors.New("not implemented")
}
func (s *proxyStore) RotateAPIKey(context.Context, string, string, int64, string, [32]byte) (APIKey, error) {
	return APIKey{}, errors.New("not implemented")
}
func (s *proxyStore) SetAPIKeyStatus(context.Context, string, string, int64, KeyStatus) (APIKey, error) {
	return APIKey{}, errors.New("not implemented")
}
func (s *proxyStore) RecoverOrphanCalls(context.Context, time.Time, int) (int, error) { return 0, nil }
func (s *proxyStore) ListCalls(context.Context, identity.Account, int) ([]Call, error) {
	return nil, nil
}
func (s *proxyStore) GetCall(context.Context, identity.Account, string) (Call, error) {
	return Call{}, nil
}
func (s *proxyStore) Dashboard(context.Context, string) (Dashboard, error) { return Dashboard{}, nil }

var _ Store = (*proxyStore)(nil)
var _ OutboundFactory = (*proxyOutbound)(nil)
