package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

func observe(format channel.Format, stream bool, contentType, body string, chunk int) Observation {
	observer := NewObserver(format, stream, contentType)
	at := time.Unix(0, 0)
	for len(body) > 0 {
		size := min(chunk, len(body))
		observer.Write([]byte(body[:size]), at)
		body = body[size:]
		at = at.Add(10 * time.Millisecond)
	}
	return observer.Finish()
}

func TestObserverOpenAIChat(t *testing.T) {
	json := `{"id":"x","usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":30}}}`
	got := observe(channel.FormatOpenAIChat, false, "application/json", json, 7)
	want := ledger.Usage{InputTokens: 70, OutputTokens: 20, CacheReadTokens: 30}
	if !got.Found || got.Usage != want {
		t.Fatalf("usage = %#v found=%v, want %#v", got.Usage, got.Found, want)
	}
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}],\"usage\":null}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n"
	got = observe(channel.FormatOpenAIChat, true, "text/event-stream; charset=utf-8", sse, 13)
	if !got.Found || got.Usage != (ledger.Usage{InputTokens: 5, OutputTokens: 2}) || got.Frames != 2 {
		t.Fatalf("stream observation = %#v", got)
	}
	if got.IntervalP50MS == nil || *got.IntervalP50MS < 0 {
		t.Fatalf("interval = %v", got.IntervalP50MS)
	}
}

func TestObserverResponsesTracksResponseIDAndCompletedUsage(t *testing.T) {
	sse := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_9\",\"usage\":null}}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_9\",\"usage\":{\"input_tokens\":50,\"output_tokens\":10,\"input_tokens_details\":{\"cached_tokens\":20}}}}\n\n"
	got := observe(channel.FormatOpenAIResponses, true, "text/event-stream", sse, 5)
	if got.ResponseID != "resp_9" || got.Usage != (ledger.Usage{InputTokens: 30, OutputTokens: 10, CacheReadTokens: 20}) {
		t.Fatalf("observation = %#v", got)
	}
	plain := `{"id":"resp_2","object":"response","usage":{"input_tokens":4,"output_tokens":1}}`
	got = observe(channel.FormatOpenAIResponses, false, "application/json", plain, 100)
	if got.ResponseID != "resp_2" || got.Usage.InputTokens != 4 {
		t.Fatalf("plain observation = %#v", got)
	}
}

func TestObserverAnthropicCombinesStartAndDelta(t *testing.T) {
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":12,\"cache_creation_input_tokens\":3,\"cache_read_input_tokens\":4,\"output_tokens\":1}}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"output_tokens\":42}}\n\n"
	got := observe(channel.FormatAnthropic, true, "text/event-stream", sse, 9)
	want := ledger.Usage{InputTokens: 12, OutputTokens: 42, CacheWriteTokens: 3, CacheReadTokens: 4}
	if !got.Found || got.Usage != want {
		t.Fatalf("usage = %#v, want %#v", got.Usage, want)
	}
	plain := `{"type":"message","usage":{"input_tokens":2,"output_tokens":3}}`
	got = observe(channel.FormatAnthropic, false, "application/json", plain, 100)
	if got.Usage != (ledger.Usage{InputTokens: 2, OutputTokens: 3}) {
		t.Fatalf("plain = %#v", got.Usage)
	}
}

func TestObserverGeminiJSONArrayAndSSE(t *testing.T) {
	array := `[{"candidates":[],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":1}},` + "\n" +
		`{"candidates":[],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":4,"thoughtsTokenCount":6,"cachedContentTokenCount":2}}]`
	got := observe(channel.FormatGemini, true, "application/json", array, 11)
	want := ledger.Usage{InputTokens: 8, OutputTokens: 10, CacheReadTokens: 2}
	if got.Usage != want {
		t.Fatalf("usage = %#v, want %#v", got.Usage, want)
	}
	sse := "data: {\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2}}\r\n\r\n"
	got = observe(channel.FormatGemini, true, "text/event-stream", sse, 4)
	if got.Usage != (ledger.Usage{InputTokens: 3, OutputTokens: 2}) {
		t.Fatalf("sse usage = %#v", got.Usage)
	}
}

func TestObserverToleratesGarbageAndOversizedBodies(t *testing.T) {
	for _, body := range []string{"", "not json", "{\"usage\":", "data: {bad\n", strings.Repeat("x", 1000)} {
		got := observe(channel.FormatOpenAIChat, true, "text/event-stream", body, 64)
		if got.Found {
			t.Fatalf("found usage in %q", body)
		}
	}
	observer := NewObserver(channel.FormatAnthropic, false, "application/json")
	observer.Write([]byte(strings.Repeat("a", maxJSONBuffer)), time.Now())
	observer.Write([]byte("b"), time.Now())
	if observer.Finish().Found {
		t.Fatal("oversized body produced usage")
	}
}
