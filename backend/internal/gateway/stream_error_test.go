package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

var protocolErrorCases = []struct {
	name                    string
	format                  channel.Format
	content, usage, failure string
}{
	{"chat", channel.FormatOpenAIChat, chatContent,
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n",
		"data: {\"error\":{\"code\":\"SECRET-code\",\"message\":\"SECRET-content\"}}\n\n"},
	{"responses", channel.FormatOpenAIResponses, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n",
		"event: response.in_progress\ndata: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp-test\",\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n\n",
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"SECRET-code\",\"message\":\"SECRET-content\"}}}\n\n"},
	{"anthropic", channel.FormatAnthropic, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n",
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n\n",
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"SECRET-content\"}}\n\n"},
	{"gemini", channel.FormatGemini, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n",
		"data: {\"usageMetadata\":{\"promptTokenCount\":7,\"candidatesTokenCount\":3}}\n\n",
		"data: {\"error\":{\"code\":503,\"status\":\"UNAVAILABLE\",\"message\":\"SECRET-content\"}}\n\n"},
}

func TestProtocolErrorKeepsUsageAndDeliveryIndependent(t *testing.T) {
	for _, protocol := range protocolErrorCases {
		for _, order := range []string{"no_usage", "usage_before", "usage_after"} {
			for _, ending := range []string{"eof", "read_error", "idle_timeout", "call_deadline", "client_left"} {
				t.Run(protocol.name+"/"+order+"/"+ending, func(t *testing.T) {
					first := protocol.content
					if order == "usage_before" {
						first += protocol.usage
					}
					run := startDrainRun(t, protocol.format, time.Second, first, func(r *drainRun) { r.call.modelID = "m" })
					run.waitWritten(1)
					if ending == "client_left" {
						// Force a failed write, deterministically marking delivery before
						// draining the protocol error and any following reliable usage.
						run.client.mutex.Lock()
						run.client.broken = true
						run.client.mutex.Unlock()
					}
					last := protocol.failure
					if order == "usage_after" {
						last += protocol.usage
					}
					run.send(last)
					if ending != "client_left" {
						run.waitWritten(1)
					}
					switch ending {
					case "eof", "client_left":
						run.end()
					case "read_error":
						run.body.fail <- errors.New("connection reset")
					case "idle_timeout":
						run.cancel(errIdle)
					case "call_deadline":
						run.cancel(errCallDeadline)
					}
					run.wait(2 * time.Second)
					finish := run.store.finish
					attempt := finish.Attempts[0]
					if finish.Outcome != OutcomeUpstreamFailed || attempt.EndReason != EndUpstreamError || attempt.ErrorCode == nil || *attempt.ErrorCode != codeProtocolError || attempt.ErrorMessage == nil || *attempt.ErrorMessage != messageProtocolError {
						t.Fatalf("failure diagnostics=%+v attempt=%+v", finish.Diagnostics, attempt)
					}
					if finish.Bill != nil || finish.Cost != 0 || finish.Fee != 0 {
						t.Fatal("protocol failure was billed")
					}
					if order == "no_usage" {
						if finish.Diagnostics.UsageStatus != "missing" || finish.Usage.InputTokens != 0 || finish.Usage.OutputTokens != 0 || !strings.Contains(string(finish.PriceSnapshot), "missing_main_usage") {
							t.Fatal("missing usage was not retained as unpriced")
						}
					} else if finish.Usage.InputTokens != 7 || finish.Usage.OutputTokens != 3 || finish.Diagnostics.UsageStatus != "reported" || len(finish.PriceSnapshot) == 0 {
						t.Fatalf("usage or snapshot lost: %+v", finish)
					}
					wantDelivery := "complete"
					if ending == "client_left" {
						wantDelivery = "client_disconnected"
					} else if ending != "eof" {
						wantDelivery = "interrupted"
					}
					if finish.Diagnostics.Delivery != wantDelivery {
						t.Fatalf("delivery=%s want %s", finish.Diagnostics.Delivery, wantDelivery)
					}
					if ending != "client_left" && run.client.received() != first+last {
						t.Fatal("upstream bytes altered")
					}
					if !run.channelFailed() {
						t.Fatal("known upstream error did not affect channel health")
					}
					stats := run.engine.Stats()["channel"]
					if stats.Attempts != 1 || stats.Successes != 0 || stats.TTFTP50MS != nil {
						t.Fatalf("health=%+v", stats)
					}
					encoded, err := json.Marshal(finish)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(encoded), "SECRET") {
						t.Fatal("provider content escaped into persisted diagnostics")
					}
				})
			}
		}
	}
}

func TestProtocolErrorEnvelopeRecognition(t *testing.T) {
	for _, protocol := range protocolErrorCases {
		t.Run(protocol.name, func(t *testing.T) {
			o := NewObserver(protocol.format, true, "text/event-stream")
			// A complete error split across arbitrary transport boundaries still
			// counts, and subsequent successful-looking frames cannot erase it.
			for _, b := range []byte(protocol.failure + protocol.content + protocol.usage) {
				o.Write([]byte{b}, time.Now())
			}
			if !o.Finish().ProtocolError {
				t.Fatal("lost error")
			}
			for _, raw := range []string{`{"error":null}`, `{"error":"not a protocol error object"}`, `{"delta":{"error":{"message":"generated tool result"}}}`, `{"choices":[{"delta":{"content":"error response.failed"}}]}`, `{"type":"response.mcp_call.failed","error":{"code":"tool_failure"}}`, `{"type":"response.failed"`, `{"type":"error","error":null}`} {
				o := NewObserver(protocol.format, true, "text/event-stream")
				o.absorb([]byte(raw))
				// Responses explicitly defines an error event without an error object.
				want := protocol.format == channel.FormatOpenAIResponses && raw == `{"type":"error","error":null}` || (protocol.format == channel.FormatOpenAIChat || protocol.format == channel.FormatGemini) && raw == `{"type":"response.mcp_call.failed","error":{"code":"tool_failure"}}`
				if o.Finish().ProtocolError != want {
					t.Errorf("false protocol classification: %s", raw)
				}
			}
		})
	}
	for _, raw := range []string{`{"type":"error","code":"SECRET","message":"SECRET"}`, `{"type":"response.failed","response":{"error":null}}`, `{"object":"response","status":"failed","error":null}`, `{"type":"response.fa\u0069led"}`, `{"type":"\u0065rror","code":"x"}`, `{"t\u0079pe":"\u0065rror","code":"x"}`, `{"object":"response","status":"fa\u0069led"}`} {
		o := NewObserver(channel.FormatOpenAIResponses, true, "text/event-stream")
		o.absorb([]byte(raw))
		if !o.Finish().ProtocolError {
			t.Fatalf("missing Responses error: %s", raw)
		}
	}
}

func TestProtocolErrorAsTheFirstOutputDoesNotReplay(t *testing.T) {
	for _, protocol := range protocolErrorCases {
		t.Run(protocol.name, func(t *testing.T) {
			run := startDrainRun(t, protocol.format, time.Second, protocol.failure, nil)
			run.waitWritten(1)
			run.end()
			run.wait(time.Second)
			if run.store.finish.Outcome != OutcomeUpstreamFailed || run.store.finish.Bill != nil || len(run.store.finish.Attempts) != 1 || run.client.received() != protocol.failure {
				t.Fatal("first-output error was altered, replayed or billed")
			}
		})
	}
}

func TestNormalProtocolContentStillAllocatesNothing(t *testing.T) {
	for _, protocol := range protocolErrorCases {
		t.Run(protocol.name, func(t *testing.T) {
			o := NewObserver(protocol.format, true, "text/event-stream")
			line := strings.Split(protocol.content, "data: ")[1]
			payload := []byte(strings.TrimSpace(line))
			if n := testing.AllocsPerRun(100, func() { o.absorb(payload) }); n != 0 {
				t.Fatalf("normal frame allocated %v times", n)
			}
		})
	}
}

func TestEscapedResponsesContentStillAllocatesNothing(t *testing.T) {
	o := NewObserver(channel.FormatOpenAIResponses, true, "text/event-stream")
	raw := []byte(`{"type":"response.output_text.delta","delta":"hello\n\u4e16\u754c"}`)
	if n := testing.AllocsPerRun(100, func() { o.absorb(raw) }); n != 0 {
		t.Fatalf("escaped normal frame allocated %v times", n)
	}
}

func TestHTTP200JSONProtocolErrorIsNotBilled(t *testing.T) {
	for _, tc := range []struct {
		format channel.Format
		body   string
	}{
		{channel.FormatOpenAIChat, `{"error":{},"usage":{"prompt_tokens":7,"completion_tokens":3}}`},
		{channel.FormatOpenAIResponses, `{"object":"response","status":"failed","error":{},"usage":{"input_tokens":7,"output_tokens":3}}`},
		{channel.FormatAnthropic, `{"type":"error","error":{},"usage":{"input_tokens":7,"output_tokens":3}}`},
		{channel.FormatGemini, `{"error":{"code":503},"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3}}`},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			store := &speedStore{}
			engine := NewEngine(Dependencies{Store: store})
			call := &callState{started: time.Now(), format: tc.format, attempts: []Attempt{{}}, key: KeyAuth{OwnerID: "consumer"}}
			model := catalog.Model{ID: "main", InputPrice: money.FromNano(money.Scale)}
			engine.streamBack(httptest.NewRecorder(), call, model, quotedCandidate(t, Candidate{OwnerID: "provider", MultiplierNano: ledger.UnitRate, RateNano: ledger.UnitRate}, model), attemptResult{
				response: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(""))},
				first:    []byte(tc.body), firstErr: io.EOF, upstream: context.Background(), cancel: func(error) {}, watch: watchClient(context.Background(), func(error) {}, drainWindow), started: call.started,
			}, settings.Settings{})
			finish := store.finish
			if finish.Outcome != OutcomeUpstreamFailed || finish.Bill != nil || finish.Cost != 0 || finish.Fee != 0 || finish.Usage.InputTokens != 7 || finish.Usage.OutputTokens != 3 || len(finish.PriceSnapshot) == 0 {
				t.Fatalf("lost independent failure/usage facts: %+v", finish)
			}
		})
	}
}
