package gatewaybench

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

var allFormats = []channel.Format{channel.FormatOpenAIChat, channel.FormatOpenAIResponses, channel.FormatAnthropic, channel.FormatGemini}

// fetch sends the request to the upstream and returns the status, content
// type and body, plus how long the first byte and the whole answer took.
func fetch(t *testing.T, base string, request Request) (status int, contentType string, body []byte, first, total time.Duration) {
	t.Helper()
	httpRequest, err := http.NewRequestWithContext(context.Background(), request.Method, request.URL, strings.NewReader(string(request.Body)))
	if err != nil {
		t.Fatal(err)
	}
	httpRequest.Header = request.Header
	started := time.Now()
	response, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	buffer := make([]byte, 4096)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if first == 0 {
				first = time.Since(started)
			}
			body = append(body, buffer[:n]...)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
	}
	return response.StatusCode, response.Header.Get("Content-Type"), body, first, time.Since(started)
}

// observe reads a body the way the gateway's side channel does.
func observe(format channel.Format, stream bool, contentType string, body []byte) gateway.Observation {
	observer := gateway.NewObserver(format, stream, contentType)
	for len(body) > 0 {
		n := min(len(body), 700) // arbitrary chunking: the parser must not care
		observer.Write(body[:n], time.Now())
		body = body[n:]
	}
	return observer.Finish()
}

func TestEveryFormatAnswersWithUsageTheGatewayCanRead(t *testing.T) {
	profile := Profile{Tokens: 12, InputTokens: 34}
	server := httptest.NewServer(NewUpstream(profile))
	defer server.Close()
	for _, format := range allFormats {
		for _, stream := range []bool{false, true} {
			t.Run(string(format)+map[bool]string{false: "", true: "/stream"}[stream], func(t *testing.T) {
				request := RequestSpec{Format: format, Stream: stream}.Build(server.URL)
				if format == channel.FormatOpenAIChat && stream {
					// The gateway asks for usage on the client's behalf.
					request.Body = []byte(strings.Replace(string(request.Body), `"stream":true`, `"stream":true,"stream_options":{"include_usage":true}`, 1))
				}
				status, contentType, body, _, _ := fetch(t, server.URL, request)
				if status != http.StatusOK {
					t.Fatalf("status %d: %s", status, body)
				}
				wantType := "application/json"
				if stream {
					wantType = "text/event-stream"
				}
				if contentType != wantType {
					t.Fatalf("content type %q, want %q", contentType, wantType)
				}
				seen := observe(format, stream, contentType, body)
				if !seen.Found || seen.Usage.InputTokens != 34 || seen.Usage.OutputTokens != 12 {
					t.Fatalf("usage = %+v found %v\n%s", seen.Usage, seen.Found, body)
				}
				if format == channel.FormatOpenAIResponses && !strings.HasPrefix(seen.ResponseID, "resp_") {
					t.Fatalf("response id %q", seen.ResponseID)
				}
				if !Complete(format, stream, body) {
					t.Fatalf("a whole answer was judged incomplete:\n%s", body)
				}
				if stream && Complete(format, stream, body[:len(body)/2]) {
					t.Fatal("half an answer was judged complete")
				}
			})
		}
	}
}

func TestUsageOffLeavesTheGatewayWithNothingToBill(t *testing.T) {
	server := httptest.NewServer(NewUpstream(Profile{Tokens: 3, Usage: UsageOff}))
	defer server.Close()
	for _, format := range allFormats {
		for _, stream := range []bool{false, true} {
			_, contentType, body, _, _ := fetch(t, server.URL, RequestSpec{Format: format, Stream: stream}.Build(server.URL))
			if seen := observe(format, stream, contentType, body); seen.Found {
				t.Fatalf("%s stream=%v reported usage %+v", format, stream, seen.Usage)
			}
		}
	}
}

func TestChatStreamSendsTheUsageOnlyFrameOnlyToAClientThatAsked(t *testing.T) {
	upstream := NewUpstream(Profile{Tokens: 3, InputTokens: 7})
	server := httptest.NewServer(upstream)
	defer server.Close()
	const usageOnly = `"choices":[],"usage":{"prompt_tokens":7`
	plain := RequestSpec{Format: channel.FormatOpenAIChat, Stream: true}.Build(server.URL)
	_, _, body, _, _ := fetch(t, server.URL, plain)
	if strings.Contains(string(body), "usage") {
		t.Fatalf("a client that did not ask for usage got it:\n%s", body)
	}
	asked := plain
	asked.Body = []byte(`{"model":"gpt-test","stream":true,"stream_options":{"include_usage":true}}`)
	_, _, body, _, _ = fetch(t, server.URL, asked)
	if !strings.Contains(string(body), usageOnly) || !strings.Contains(string(body), `"usage":null`) {
		t.Fatalf("a client that asked for usage got:\n%s", body)
	}
	upstream.SetProfile(Profile{Tokens: 3, InputTokens: 7, Usage: UsageAlways})
	_, _, body, _, _ = fetch(t, server.URL, plain)
	if !strings.Contains(string(body), usageOnly) || strings.Contains(string(body), `"usage":null`) {
		t.Fatalf("UsageAlways sent:\n%s", body)
	}
}

func TestStreamIsPacedByTheProfile(t *testing.T) {
	const ttft, interval, tokens = 80 * time.Millisecond, 25 * time.Millisecond, 5
	server := httptest.NewServer(NewUpstream(Profile{TTFT: ttft, Interval: interval, Tokens: tokens}))
	defer server.Close()
	_, _, _, first, total := fetch(t, server.URL, RequestSpec{Format: channel.FormatAnthropic, Stream: true}.Build(server.URL))
	// Never earlier than asked; how much later depends on the machine.
	if first < ttft {
		t.Fatalf("first byte after %s, want at least %s", first, ttft)
	}
	if want := ttft + (tokens-1)*interval; total < want {
		t.Fatalf("stream took %s, want at least %s", total, want)
	}
	_, _, _, first, _ = fetch(t, server.URL, RequestSpec{Format: channel.FormatOpenAIChat}.Build(server.URL))
	if first < ttft {
		t.Fatalf("non-streamed answer after %s, want at least %s", first, ttft)
	}
}

func TestSetProfileChangesLaterAnswers(t *testing.T) {
	upstream := NewUpstream(Profile{Tokens: 2, InputTokens: 1})
	server := httptest.NewServer(upstream)
	defer server.Close()
	request := RequestSpec{Format: channel.FormatOpenAIChat}.Build(server.URL)
	_, _, short, _, _ := fetch(t, server.URL, request)
	upstream.SetProfile(Profile{Tokens: 200, InputTokens: 1})
	_, _, long, _, _ := fetch(t, server.URL, request)
	if len(long) <= len(short)+500 {
		t.Fatalf("answers of %d and %d bytes", len(short), len(long))
	}
	if stats := upstream.Stats(); stats.Requests != 2 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestResponsesIDsAreUnique(t *testing.T) {
	upstream := NewUpstream(Profile{Tokens: 2})
	server := httptest.NewServer(upstream)
	defer server.Close()
	spec := RequestSpec{Format: channel.FormatOpenAIResponses}
	_, _, first, _, _ := fetch(t, server.URL, spec.Build(server.URL))
	_, _, second, _, _ := fetch(t, server.URL, spec.Build(server.URL))
	a, b := ResponseID(first), ResponseID(second)
	if a == "" || a == b {
		t.Fatalf("ids %q and %q", a, b)
	}
}

func TestUnknownPathsAreNotFound(t *testing.T) {
	server := httptest.NewServer(NewUpstream(DefaultProfile()))
	defer server.Close()
	request := Request{Method: http.MethodPost, URL: server.URL + "/v1/embeddings", Header: http.Header{}}
	if status, _, _, _, _ := fetch(t, server.URL, request); status != http.StatusNotFound {
		t.Fatalf("status %d", status)
	}
}
