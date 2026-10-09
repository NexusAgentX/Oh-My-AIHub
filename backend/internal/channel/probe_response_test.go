package channel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type probeOutbound struct {
	Outbound
	client *http.Client
	used   bool
}

func (p *probeOutbound) GatewayClient(_ context.Context, _ string, timeout time.Duration) (*http.Client, error) {
	p.used = timeout == testTimeout
	return p.client, nil
}

func TestFormatProbesUseStreaming(t *testing.T) {
	for _, tc := range []struct {
		format     Format
		path, body string
	}{
		{FormatOpenAIChat, "/v1/chat/completions", "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"},
		{FormatOpenAIResponses, "/v1/responses", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"},
		{FormatAnthropic, "/v1/messages", "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{FormatGemini, "/v1beta/models/test:streamGenerateContent", "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path || r.Header.Get("Accept") != "text/event-stream" {
					t.Errorf("request %s headers %v", r.URL.Path, r.Header)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if tc.format == FormatGemini {
					if r.URL.Query().Get("alt") != "sse" {
						t.Error("missing alt=sse")
					}
				} else if body["stream"] != true {
					t.Error("missing stream=true")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, tc.body)
				w.(http.Flusher).Flush()
				// A finished stream need not close the connection before the probe returns.
				<-r.Context().Done()
			}))
			defer server.Close()
			policy := &probeOutbound{client: server.Client()}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			got := (&Service{}).probeOnce(ctx, policy, Channel{BaseURL: server.URL}, Model{ModelID: "test", UpstreamModel: "test"}, tc.format, "secret")
			if !policy.used || !got.OK {
				t.Fatalf("used gateway timeout=%v outcome=%+v", policy.used, got)
			}
		})
	}
}

func TestProbeResponsesRejectFailures(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		format                  Format
		wantError               bool
	}{
		{"JSON fallback", "application/json", `{"output":[],"status":"completed"}`, FormatOpenAIResponses, false},
		{"JSON failed", "application/json", `{"status":"failed"}`, FormatOpenAIResponses, true},
		{"JSON error", "application/json", `{"error":{"message":"failed"}}`, FormatOpenAIChat, true},
		{"HTML", "text/html", "<html>ok</html>", FormatOpenAIChat, true},
		{"truncated", "text/event-stream", "data: {\"type\":\"response.created\"}\n\n", FormatOpenAIResponses, true},
		{"error after 200", "text/event-stream", "data: {\"error\":{\"message\":\"failed\"}}\n\ndata: [DONE]\n\n", FormatOpenAIChat, true},
		{"responses failed", "text/event-stream", "data: {\"type\":\"response.failed\"}\n\n", FormatOpenAIResponses, true},
		{"anthropic error", "text/event-stream", "data: {\"type\":\"error\"}\n\n", FormatAnthropic, true},
		{"gemini error", "text/event-stream", "data: {\"error\":{\"code\":500}}\n\n", FormatGemini, true},
		{"token limit", "text/event-stream", "data: {\"type\":\"response.incomplete\"}\n\n", FormatOpenAIResponses, false},
		{"multiline CRLF", "text/event-stream", "data: {\"type\":\r\ndata: \"message_stop\"}\r\n\r\n", FormatAnthropic, false},
		{"oversized", "text/event-stream", strings.Repeat(":keepalive\n\n", testBodyLimit), FormatAnthropic, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := &http.Response{Header: http.Header{"Content-Type": {tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			if err := validateTestResponse(response, tc.format); (err != nil) != tc.wantError {
				t.Fatalf("error=%v want error=%v", err, tc.wantError)
			}
		})
	}
	for _, contentType := range []string{"application/json", "text/event-stream"} {
		response := &http.Response{Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(probeReadError{})}
		if err := validateTestResponse(response, FormatOpenAIResponses); err == nil {
			t.Fatal("read error accepted")
		}
	}
}

type probeReadError struct{}

func (probeReadError) Read([]byte) (int, error) { return 0, errors.New("read timed out") }

func TestProbeClientKeepsTotalDeadlineWithoutHeaderDeadline(t *testing.T) {
	policy, err := NewOutboundPolicyWithResolver(nil, nil, &fakeResolver{addresses: []net.IP{net.ParseIP("93.184.216.34")}})
	if err != nil {
		t.Fatal(err)
	}
	client, err := policy.GatewayClient(context.Background(), "https://relay.example", testTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if client.Timeout != testTimeout || client.Transport.(*http.Transport).ResponseHeaderTimeout != 0 {
		t.Fatal("unexpected test deadlines")
	}
	discovery, err := policy.Client(context.Background(), "https://relay.example", probeTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if discovery.Timeout != probeTimeout || discovery.Transport.(*http.Transport).ResponseHeaderTimeout != 10*time.Second {
		t.Fatal("discovery deadlines changed")
	}
}
