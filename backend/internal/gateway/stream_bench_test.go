package gateway

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaybench"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// The benchmarks here replay the streams of the performance baseline's fake
// upstream (internal/gatewaybench, docs/runbooks/gateway-baseline.md) through
// the gateway's side-channel reader and its write path, with no network and no
// database, so one function's cost can be read in isolation:
//
//	go -C backend test ./internal/gateway -run '^$' -bench 'Stream|Observer|UsageFrame' -benchmem

// benchStream is one answer of the fake upstream cut into the frames it was
// written in; the gateway reads them one by one.
type benchStream struct {
	format channel.Format
	path   string
	body   string
	frames [][]byte
}

var benchStreams = map[string]benchStream{
	"chat":      {format: channel.FormatOpenAIChat, path: "/v1/chat/completions", body: `{"model":"m","stream":true,"stream_options":{"include_usage":true}}`},
	"responses": {format: channel.FormatOpenAIResponses, path: "/v1/responses", body: `{"model":"m","stream":true}`},
	"anthropic": {format: channel.FormatAnthropic, path: "/v1/messages", body: `{"model":"m","stream":true}`},
	"gemini":    {format: channel.FormatGemini, path: "/v1beta/models/m:streamGenerateContent", body: `{}`},
}

var benchStreamOrder = []string{"chat", "responses", "anthropic", "gemini"}

// frames renders the stream of name with the given number of output tokens.
func (s benchStream) render(tb testing.TB, tokens int) benchStream {
	tb.Helper()
	upstream := gatewaybench.NewUpstream(gatewaybench.Profile{Tokens: tokens, InputTokens: 50})
	request := httptest.NewRequest(http.MethodPost, s.path, strings.NewReader(s.body))
	recorder := httptest.NewRecorder()
	upstream.ServeHTTP(recorder, request)
	text := recorder.Body.Bytes()
	s.frames = nil
	for len(text) > 0 {
		end := len(text)
		for _, separator := range []string{"\r\n\r\n", "\n\n"} {
			if at := bytes.Index(text, []byte(separator)); at >= 0 && at+len(separator) < end {
				end = at + len(separator)
			}
		}
		s.frames = append(s.frames, text[:end:end])
		text = text[end:]
	}
	if len(s.frames) < tokens {
		tb.Fatalf("%s: %d frames for %d tokens", s.path, len(s.frames), tokens)
	}
	return s
}

// replayBody is an upstream body that hands out one frame per read, like the
// network does when the upstream flushes every token.
type replayBody struct {
	frames [][]byte
	next   int
}

func (b *replayBody) Read(p []byte) (int, error) {
	if b.next >= len(b.frames) {
		return 0, io.EOF
	}
	n := copy(p, b.frames[b.next])
	b.next++
	return n, nil
}

func (b *replayBody) Close() error { return nil }

// nullClient is a client that takes everything and keeps nothing.
type nullClient struct {
	header  http.Header
	written int
	writes  int
}

func (c *nullClient) Header() http.Header { return c.header }
func (c *nullClient) WriteHeader(int)     {}
func (c *nullClient) Flush()              {}
func (c *nullClient) Write(p []byte) (int, error) {
	c.written += len(p)
	c.writes++
	return len(p), nil
}

// BenchmarkStreamBack is the whole write path of a streamed answer: read a
// frame, let the side-channel reader see it, drop the injected usage frame,
// write and flush to the client, and settle the call at the end. One op is one
// answer of 1000 tokens (the baseline's stream-1k).
func BenchmarkStreamBack(b *testing.B) {
	for _, name := range benchStreamOrder {
		stream := benchStreams[name].render(b, 1000)
		b.Run(name, func(b *testing.B) {
			engine := NewEngine(Dependencies{Store: &speedStore{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			model := catalog.Model{}
			candidate := Candidate{ChannelID: "channel", OwnerID: "sharer"}
			defaults := settings.Settings{}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				body := &replayBody{frames: stream.frames[1:]}
				response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}
				call := &callState{id: "call", started: time.Now(), stream: true, format: stream.format, key: KeyAuth{OwnerID: "consumer"}, attempts: []Attempt{{}}}
				// The gateway asks for usage on behalf of a Chat client that did not.
				call.usageInjected = stream.format == channel.FormatOpenAIChat
				client := &nullClient{header: http.Header{}}
				engine.streamBack(client, call, model, candidate, attemptResult{
					response: response, first: stream.frames[0], upstream: context.Background(), cancel: func(error) {},
					watch: watchClient(context.Background(), func(error) {}, drainWindow), started: time.Now(),
				}, defaults)
				if client.writes == 0 {
					b.Fatal("nothing reached the client")
				}
			}
		})
	}
}

// BenchmarkObserver is the side-channel reader alone on the same streams.
func BenchmarkObserver(b *testing.B) {
	for _, name := range benchStreamOrder {
		stream := benchStreams[name].render(b, 1000)
		b.Run(name, func(b *testing.B) {
			at := time.Unix(0, 0)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				observer := NewObserver(stream.format, true, "text/event-stream")
				for _, frame := range stream.frames {
					observer.Write(frame, at)
				}
				if observation := observer.Finish(); !observation.Found {
					b.Fatal("no usage found")
				}
			}
		})
	}
}

// BenchmarkUsageFrameFilter is the injected-usage-frame filter alone on the
// Chat stream.
func BenchmarkUsageFrameFilter(b *testing.B) {
	stream := benchStreams["chat"].render(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		filter := &usageFrameFilter{}
		for _, frame := range stream.frames {
			filter.Filter(frame)
		}
		filter.Flush()
	}
}

// BenchmarkJSONSettlement isolates nonstream Responses parsing, pricing and
// settlement from network, database and their pools. One op includes a bill.

func BenchmarkJSONSettlement(b *testing.B) {
	upstream := gatewaybench.NewUpstream(gatewaybench.Profile{Tokens: 64, InputTokens: 50})
	rec := httptest.NewRecorder()
	upstream.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"m"}`)))
	raw := rec.Body.Bytes()
	store := &speedStore{}
	engine := NewEngine(Dependencies{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	model := catalog.Model{ID: "m", PriceCurrency: "CNY", InputPrice: 1_000_000_000, OutputPrice: 1_000_000_000}
	candidate := Candidate{ChannelID: "channel", OwnerID: "sharer", MultiplierNano: 1_000_000_000, RateNano: 1_000_000_000}
	defaults := settings.Settings{}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(""))}
		call := &callState{id: "call", started: time.Now(), format: channel.FormatOpenAIResponses, key: KeyAuth{OwnerID: "consumer"}, attempts: []Attempt{{}}}
		client := &nullClient{header: http.Header{}}
		engine.streamBack(client, call, model, candidate, attemptResult{response: response, first: raw, firstErr: io.EOF, upstream: context.Background(), cancel: func(error) {}, watch: watchClient(context.Background(), func(error) {}, drainWindow), started: time.Now()}, defaults)
		if client.writes == 0 || store.finish.Bill == nil || store.finish.Cost != 114000 {
			b.Fatalf("response was not billed: %+v", store.finish)
		}
	}
}
