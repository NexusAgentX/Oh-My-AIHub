package gateway

import (
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func TestProtocolRequestErrorsUseExistingHealthExclusion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		format  channel.Format
		failure string
		request bool
	}{
		{"chat context", channel.FormatOpenAIChat, `{"error":{"code":"context_length_exceeded"}}`, true},
		{"chat invalid prompt", channel.FormatOpenAIChat, `{"error":{"code":"invalid_prompt"}}`, true},
		{"chat broad type", channel.FormatOpenAIChat, `{"error":{"type":"invalid_request_error"}}`, false},
		{"chat credential", channel.FormatOpenAIChat, `{"error":{"type":"invalid_request_error","code":"invalid_api_key"}}`, false},
		{"chat model exception", channel.FormatOpenAIChat, `{"error":{"code":"invalid_prompt","message":"model x does not exist"}}`, false},
		{"responses direct", channel.FormatOpenAIResponses, `{"type":"error","code":"invalid_prompt"}`, true},
		{"responses failed", channel.FormatOpenAIResponses, `{"type":"response.failed","response":{"error":{"code":"invalid_prompt"}}}`, true},
		{"responses failed context", channel.FormatOpenAIResponses, `{"type":"response.failed","response":{"error":{"code":"context_length_exceeded"}}}`, true},
		{"responses model exception", channel.FormatOpenAIResponses, `{"type":"response.failed","response":{"error":{"code":"invalid_prompt","message":"model x does not exist"}}}`, false},
		{"responses unknown", channel.FormatOpenAIResponses, `{"type":"error","code":"SECRET-custom"}`, false},
		{"anthropic invalid request", channel.FormatAnthropic, `{"type":"error","error":{"type":"invalid_request_error","message":"messages: content must be nonempty"}}`, true},
		{"anthropic too large", channel.FormatAnthropic, `{"type":"error","error":{"type":"request_too_large"}}`, true},
		{"anthropic model exception", channel.FormatAnthropic, `{"type":"error","error":{"type":"invalid_request_error","message":"model x is not supported"}}`, false},
		{"anthropic credential", channel.FormatAnthropic, `{"type":"error","error":{"type":"authentication_error"}}`, false},
		{"anthropic permission", channel.FormatAnthropic, `{"type":"error","error":{"type":"permission_error"}}`, false},
		{"anthropic rate limit", channel.FormatAnthropic, `{"type":"error","error":{"type":"rate_limit_error"}}`, false},
		{"anthropic unknown", channel.FormatAnthropic, `{"type":"error","error":{"type":"SECRET-custom"}}`, false},
		{"gemini argument", channel.FormatGemini, `{"error":{"status":"INVALID_ARGUMENT","code":400}}`, true},
		{"gemini model exception", channel.FormatGemini, `{"error":{"status":"INVALID_ARGUMENT","code":400,"message":"models/x is not found for API version v1beta"}}`, false},
		{"gemini contradictory", channel.FormatGemini, `{"error":{"status":"INVALID_ARGUMENT","code":500}}`, false},
		{"gemini no status", channel.FormatGemini, `{"error":{"code":400}}`, false},
		{"gemini string code", channel.FormatGemini, `{"error":{"status":"INVALID_ARGUMENT","code":"400"}}`, false},
		{"gemini precondition", channel.FormatGemini, `{"error":{"status":"FAILED_PRECONDITION","code":400}}`, false},
		{"gemini permission", channel.FormatGemini, `{"error":{"status":"PERMISSION_DENIED","code":403}}`, false},
		{"gemini rate limit", channel.FormatGemini, `{"error":{"status":"RESOURCE_EXHAUSTED","code":429}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var content, usage string
			for _, protocol := range protocolErrorCases {
				if protocol.format == tc.format {
					content, usage = protocol.content, protocol.usage
				}
			}
			failure := "data: " + tc.failure + "\n\n"
			run := startDrainRun(t, tc.format, time.Second, content+usage+failure, func(r *drainRun) {
				r.call.modelID = "m"
				status := 200
				r.call.attempts[0].StatusCode = &status
			})
			run.waitWritten(1)
			run.end()
			run.wait(time.Second)
			finish := run.store.finish
			wantEnd := EndUpstreamError
			if tc.request {
				wantEnd = EndClientError
			}
			if finish.Outcome != OutcomeUpstreamFailed || len(finish.Attempts) != 1 || finish.Attempts[0].EndReason != wantEnd || finish.Bill != nil || finish.Cost != 0 || finish.Fee != 0 {
				t.Fatalf("failure classification=%+v", finish)
			}
			if finish.Attempts[0].StatusCode == nil || *finish.Attempts[0].StatusCode != 200 {
				t.Fatal("actual HTTP 200 was rewritten")
			}
			if finish.Usage.InputTokens != 7 || finish.Usage.OutputTokens != 3 || len(finish.PriceSnapshot) == 0 || run.client.received() != content+usage+failure {
				t.Fatal("usage, snapshot or original response lost")
			}
			if run.channelFailed() == tc.request {
				t.Fatalf("request error=%v channel failed=%v", tc.request, run.channelFailed())
			}
			view := run.engine.StatsBook.view(time.Now())
			if tc.request && (len(view.models["m"]) != 1 || view.models["m"][0].client != 1) {
				t.Fatal("normalized request error lost from anomaly sample")
			}
			stats, listed := run.engine.Stats()["channel"]
			if tc.request && listed || !tc.request && (stats.Attempts != 1 || stats.Successes != 0) {
				t.Fatalf("channel health=%+v listed=%v", stats, listed)
			}
		})
	}
}

func TestAChannelProtocolFailureDominatesRequestErrorsInEitherOrder(t *testing.T) {
	for _, tc := range []struct {
		format            channel.Format
		request, upstream string
	}{
		{channel.FormatOpenAIChat, `{"error":{"code":"invalid_prompt"}}`, `{"error":{"code":"unknown"}}`},
		{channel.FormatOpenAIResponses, `{"type":"error","code":"invalid_prompt"}`, `{"type":"response.failed"}`},
		{channel.FormatAnthropic, `{"type":"error","error":{"type":"invalid_request_error"}}`, `{"type":"error","error":{"type":"overloaded_error"}}`},
		{channel.FormatGemini, `{"error":{"status":"INVALID_ARGUMENT","code":400}}`, `{"error":{"status":"UNAVAILABLE","code":503}}`},
	} {
		for _, reverse := range []bool{false, true} {
			frames := []string{tc.request, tc.upstream}
			if reverse {
				frames[0], frames[1] = frames[1], frames[0]
			}
			o := NewObserver(tc.format, true, "text/event-stream")
			for _, raw := range frames {
				o.Write([]byte("data: "+raw+"\n\n"), time.Now())
			}
			if got := o.Finish(); !got.ProtocolError || got.ProtocolClientError {
				t.Fatalf("format=%s reverse=%v observation=%+v", tc.format, reverse, got)
			}
		}
	}
}

// Failure-only cost budget: ordinary content frames never enter the additional
// classification decoder. Each benchmark models one failed request, not a
// model token or normal content frame.
func BenchmarkProtocolErrorClassification(b *testing.B) {
	for _, tc := range []struct {
		name   string
		format channel.Format
		raw    string
	}{
		{"chat_request", channel.FormatOpenAIChat, `{"error":{"code":"context_length_exceeded","message":"context too large"}}`},
		{"responses_request", channel.FormatOpenAIResponses, `{"type":"response.failed","response":{"error":{"code":"invalid_prompt","message":"invalid prompt"}}}`},
		{"anthropic_request", channel.FormatAnthropic, `{"type":"error","error":{"type":"invalid_request_error","message":"invalid message"}}`},
		{"gemini_request", channel.FormatGemini, `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"invalid argument"}}`},
		{"chat_upstream", channel.FormatOpenAIChat, `{"error":{"code":"unknown","message":"upstream failure"}}`},
		{"responses_upstream", channel.FormatOpenAIResponses, `{"type":"response.failed","response":{"error":{"code":"server_error","message":"upstream failure"}}}`},
		{"anthropic_upstream", channel.FormatAnthropic, `{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`},
		{"gemini_upstream", channel.FormatGemini, `{"error":{"code":503,"status":"UNAVAILABLE","message":"unavailable"}}`},
	} {
		b.Run(tc.name, func(b *testing.B) {
			raw := []byte(tc.raw)
			b.ReportAllocs()
			for b.Loop() {
				o := Observer{format: tc.format}
				o.absorb(raw)
				if !o.protocolError {
					b.Fatal("benchmark fixture is not a protocol error")
				}
			}
		})
	}
}
