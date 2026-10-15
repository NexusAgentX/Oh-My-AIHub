package gateway

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"testing"
	"time"
)

func TestUsageCompatibility(t *testing.T) {
	type specimen struct {
		name   string
		format channel.Format
		raw    string
		stream bool
		want   [4]int64
		detail map[string]int64
	}
	cases := []specimen{
		{name: "chat_official_control", format: channel.FormatOpenAIChat, raw: `{"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":30,"cache_write_tokens":10}}}`, want: [4]int64{60, 20, 10, 30}},
		{name: "chat_cache_creation_alias", format: channel.FormatOpenAIChat, raw: `{"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":30,"cache_creation_tokens":10}}}`, want: [4]int64{60, 20, 10, 30}},
		{name: "chat_nested_creation_input_alias", format: channel.FormatOpenAIChat, raw: `{"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":30,"cache_creation_input_tokens":10}}}`, want: [4]int64{60, 20, 10, 30}},
		{name: "chat_top_level_cache_aliases", format: channel.FormatOpenAIChat, raw: `{"usage":{"prompt_tokens":100,"completion_tokens":20,"cache_read_input_tokens":30,"cache_creation_input_tokens":10}}`, want: [4]int64{60, 20, 10, 30}},
		{name: "chat_deepseek_cache_hit", format: channel.FormatOpenAIChat, raw: `{"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_cache_hit_tokens":70,"prompt_cache_miss_tokens":30}}`, want: [4]int64{30, 20, 0, 70}},
		{name: "responses_cache_creation_alias", format: channel.FormatOpenAIResponses, raw: `{"object":"response","usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":30,"cache_creation_tokens":10}}}`, want: [4]int64{60, 20, 10, 30}},
		{name: "chat_cache_creation_ttl", format: channel.FormatOpenAIChat, raw: `{"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cache_write_tokens":30,"cache_creation_token_details":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":20}}}}`, want: [4]int64{70, 20, 30, 0}, detail: map[string]int64{"cache_write_5m": 10, "cache_write_1h": 20}},
		{name: "chat_partial_stream_usage", format: channel.FormatOpenAIChat, stream: true, raw: "data: {\"usage\":{\"prompt_tokens\":100,\"prompt_tokens_details\":{\"cached_tokens\":30}}}\n\ndata: {\"usage\":{\"completion_tokens\":20}}\n\n", want: [4]int64{70, 20, 0, 30}},
		{name: "responses_partial_stream_usage", format: channel.FormatOpenAIResponses, stream: true, raw: "data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"r\",\"usage\":{\"input_tokens\":100,\"input_tokens_details\":{\"cached_tokens\":30}}}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"usage\":{\"output_tokens\":20}}}\n\n", want: [4]int64{70, 20, 0, 30}},
		{name: "anthropic_iterations", format: channel.FormatAnthropic, raw: `{"usage":{"input_tokens":20,"output_tokens":5,"iterations":[{"input_tokens":10,"output_tokens":3,"cache_read_input_tokens":4,"cache_creation_input_tokens":2},{"input_tokens":20,"output_tokens":5,"cache_read_input_tokens":6,"cache_creation_input_tokens":3}]}}`, want: [4]int64{30, 8, 5, 10}},
		{name: "anthropic_native_ttl_control", format: channel.FormatAnthropic, raw: `{"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":20,"cache_creation_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":20}}}`, want: [4]int64{10, 5, 30, 20}, detail: map[string]int64{"cache_write_5m": 10, "cache_write_1h": 20}},
		{name: "gemini_thinking_inclusive", format: channel.FormatGemini, raw: `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"thoughtsTokenCount":20,"totalTokenCount":150}}`, want: [4]int64{100, 50, 0, 0}},
		{name: "gemini_thinking_exclusive_control", format: channel.FormatGemini, raw: `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"thoughtsTokenCount":20,"totalTokenCount":170}}`, want: [4]int64{100, 70, 0, 0}},
		{name: "gemini_non_grounding_tool_prompt", format: channel.FormatGemini, raw: `{"candidates":[],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"toolUsePromptTokenCount":30,"totalTokenCount":150}}`, want: [4]int64{100, 20, 0, 0}},
		{name: "gemini_document_modality", format: channel.FormatGemini, raw: `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"promptTokensDetails":[{"modality":"DOCUMENT","tokenCount":100}],"totalTokenCount":120}}`, want: [4]int64{100, 20, 0, 0}, detail: map[string]int64{"input_text": 100}},
		{name: "chat_multimodal_cache_split", format: channel.FormatOpenAIChat, raw: `{"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"audio_tokens":70,"text_tokens":30,"cached_tokens":40,"cached_tokens_details":{"audio_tokens":40,"text_tokens":0}}}}`, want: [4]int64{60, 20, 0, 40}, detail: map[string]int64{"input_audio": 30, "input_text": 30, "cache_read_audio": 40}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ct := "application/json"
			if c.stream {
				ct = "text/event-stream"
			}
			o := NewObserver(c.format, c.stream, ct)
			b := []byte(c.raw)
			for len(b) > 0 {
				n := min(7, len(b))
				o.Write(b[:n], time.Unix(0, 0))
				b = b[n:]
			}
			got := o.Finish()
			actual := [4]int64{got.Usage.InputTokens, got.Usage.OutputTokens, got.Usage.CacheWriteTokens, got.Usage.CacheReadTokens}
			matches := got.Found && actual == c.want
			for k, v := range c.detail {
				if got.Usage.Detail == nil || got.Usage.Detail.Tokens[k] != v {
					matches = false
				}
			}
			priced, err := ledger.CalculatePriceV2(got.Usage, ledger.Prices{InputPerMillion: 1_000_000, OutputPerMillion: 2_000_000, CacheWritePerMillion: 3_000_000, CacheReadPerMillion: 4_000_000}, nil, time.Unix(0, 0), ledger.FixedPointScale, 0)
			wantCost := money.Amount(c.want[0] + 2*c.want[1] + 3*c.want[2] + 4*c.want[3])
			if err != nil || priced.Cost != wantCost {
				t.Fatalf("price=%+v err=%v want=%v", priced, err, wantCost)
			}
			if c.name == "gemini_non_grounding_tool_prompt" && (got.Usage.Detail == nil || got.Usage.Detail.ToolPromptTokens != 30) {
				t.Fatal("tool observation lost")
			}

			if !matches {
				t.Errorf("compatibility gap: got %v, expected %v; required detail %v", actual, c.want, c.detail)
			}
		})
	}
}

// Cumulative usage updates replace present fields; null/missing preserve prior
// reliable values. Cache aliases never sum, and explicit zero wins precedence.
func TestUsageCompatibilityUpdates(t *testing.T) {
	cases := []struct {
		name   string
		frames []string
		want   [4]int64
		note   bool
		detail map[string]int64
	}{
		{"zero preferred", []string{`{"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0,"cache_creation_tokens":10},"cache_read_input_tokens":30}}`}, [4]int64{100, 20, 0, 0}, true, nil},
		{"negative fallback", []string{`{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cache_write_tokens":-1,"cache_creation_tokens":10}}}`}, [4]int64{90, 0, 10, 0}, true, nil},
		{"malformed alias fallback", []string{`{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cache_creation_tokens":"10","cache_creation_input_tokens":10}}}`}, [4]int64{90, 0, 10, 0}, true, nil},
		{"null preserves", []string{`{"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":30}}}`, `{"usage":{"prompt_tokens":null,"completion_tokens":null,"prompt_tokens_details":null}}`}, [4]int64{70, 20, 0, 30}, false, nil},
		{"explicit zero", []string{`{"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":30}}}`, `{"usage":{"completion_tokens":0,"prompt_tokens_details":{"cached_tokens":0}}}`}, [4]int64{100, 0, 0, 0}, false, nil},
		{"cumulative replaces", []string{`{"usage":{"prompt_tokens":100,"completion_tokens":2}}`, `{"usage":{"prompt_tokens":100,"completion_tokens":20}}`}, [4]int64{100, 20, 0, 0}, false, nil},
		{"cache cannot inflate", []string{`{"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":90,"cache_creation_tokens":30}}}`}, [4]int64{100, 20, 0, 0}, true, nil},
		{"deepseek details preferred", []string{`{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":0},"prompt_cache_hit_tokens":70}}`}, [4]int64{100, 0, 0, 0}, false, nil},
		{"details persist output only", []string{`{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"text_tokens":30,"audio_tokens":70}}}`, `{"usage":{"completion_tokens":20}}`}, [4]int64{100, 20, 0, 0}, false, map[string]int64{"input_text": 30, "input_audio": 70}},
		{"ttl shrink", []string{`{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cache_write_tokens":30,"cache_creation_token_details":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":20}}}}`, `{"usage":{"prompt_tokens_details":{"cache_write_tokens":0}}}`}, [4]int64{100, 0, 0, 0}, true, map[string]int64{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, chunk := range []int{1, 7, 4096} {
				body := ""
				for _, frame := range tc.frames {
					body += "data: " + frame + "\n\n"
				}
				got := observe(channel.FormatOpenAIChat, true, "text/event-stream", body, chunk)
				actual := [4]int64{got.Usage.InputTokens, got.Usage.OutputTokens, got.Usage.CacheWriteTokens, got.Usage.CacheReadTokens}
				if !got.Found || actual != tc.want {
					t.Fatalf("got %+v want %v", got, tc.want)
				}
				if tc.note && (got.Usage.Detail == nil || len(got.Usage.Detail.Notes) == 0) {
					t.Fatal("missing fallback explanation")
				}
				if tc.detail != nil {
					for key, n := range tc.detail {
						if got.Usage.Detail == nil || got.Usage.Detail.Tokens[key] != n {
							t.Fatalf("detail %+v", got.Usage.Detail)
						}
					}
				}
				if tc.name == "ttl shrink" {
					for key := range got.Usage.Detail.Tokens {
						if key == "cache_write_5m" || key == "cache_write_1h" {
							t.Fatalf("stale TTL: %+v", got.Usage.Detail)
						}
					}
				}
			}
		})
	}
}

func TestIterationTTLDoesNotBecomeLastIterationTTL(t *testing.T) {
	o := NewObserver(channel.FormatAnthropic, true, "text/event-stream")
	o.absorb([]byte(`{"usage":{"cache_creation_input_tokens":10,"iterations":[{"input_tokens":10,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":0}},{"input_tokens":20,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":10}}]}}`))
	if o.detail.Tokens["cache_write_5m"] != 10 || o.detail.Tokens["cache_write_1h"] != 10 {
		t.Fatalf("initial TTL %+v", o.detail)
	}
	o.absorb([]byte(`{"usage":{"cache_creation_input_tokens":5,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":5}}}`))
	got := o.Finish()
	if got.Usage.CacheWriteTokens != 15 || len(got.Usage.Detail.Tokens) != 0 || len(got.Usage.Detail.Notes) == 0 {
		t.Fatalf("last iteration cannot replace aggregate TTL: %+v", got)
	}
}

func TestUsageCompatibilityIterationsAndGeminiBounds(t *testing.T) {
	cases := []struct {
		name   string
		format channel.Format
		frames []string
		want   [4]int64
		notes  bool
	}{
		{"iterations cumulative", channel.FormatAnthropic, []string{`{"usage":{"input_tokens":20,"output_tokens":5,"iterations":[{"input_tokens":10,"output_tokens":3,"cache_creation_input_tokens":2},{"input_tokens":20,"output_tokens":5,"cache_creation_input_tokens":3}]}}`, `{"usage":{"input_tokens":20,"output_tokens":5}}`}, [4]int64{30, 8, 5, 0}, true},
		{"iterations then partial delta", channel.FormatAnthropic, []string{`{"usage":{"input_tokens":20,"output_tokens":5,"iterations":[{"input_tokens":10,"output_tokens":3},{"input_tokens":20,"output_tokens":5}]}}`, `{"usage":{"output_tokens":7}}`}, [4]int64{30, 10, 0, 0}, false},
		{"iterations cumulative replacement", channel.FormatAnthropic, []string{`{"usage":{"input_tokens":20,"output_tokens":5,"iterations":[{"input_tokens":10,"output_tokens":3},{"input_tokens":20,"output_tokens":5}]}}`, `{"usage":{"input_tokens":20,"output_tokens":7,"iterations":[{"input_tokens":10,"output_tokens":3},{"input_tokens":20,"output_tokens":7}]}}`}, [4]int64{30, 10, 0, 0}, false},
		{"iteration overflow fallback", channel.FormatAnthropic, []string{`{"usage":{"input_tokens":20,"output_tokens":5,"iterations":[{"input_tokens":9223372036854775807},{"input_tokens":1}]}}`}, [4]int64{20, 5, 0, 0}, true},
		{"per iteration TTL invalid", channel.FormatAnthropic, []string{`{"usage":{"iterations":[{"input_tokens":10,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":8,"ephemeral_1h_input_tokens":8}},{"input_tokens":10,"cache_creation_input_tokens":10}]}}`}, [4]int64{20, 0, 20, 0}, true},
		{"gemini ambiguous total", channel.FormatGemini, []string{`{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"thoughtsTokenCount":20,"totalTokenCount":160}}`}, [4]int64{100, 70, 0, 0}, true},
		{"gemini tools observed only", channel.FormatGemini, []string{`{"candidates":[{"groundingMetadata":{"searchEntryPoint":{}}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"thoughtsTokenCount":20,"toolUsePromptTokenCount":30,"totalTokenCount":200}}`}, [4]int64{100, 70, 0, 0}, true},
		{"gemini cache exceeds input", channel.FormatGemini, []string{`{"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":20,"candidatesTokenCount":5}}`}, [4]int64{10, 5, 0, 0}, true},
		{"gemini negative cache", channel.FormatGemini, []string{`{"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":-20,"candidatesTokenCount":5}}`}, [4]int64{10, 5, 0, 0}, true},
		{"gemini output overflow", channel.FormatGemini, []string{`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":9223372036854775807,"thoughtsTokenCount":1}}`}, [4]int64{10, 9223372036854775807, 0, 0}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := ""
			for _, frame := range tc.frames {
				body += "data: " + frame + "\n\n"
			}
			got := observe(tc.format, true, "text/event-stream", body, 3)
			actual := [4]int64{got.Usage.InputTokens, got.Usage.OutputTokens, got.Usage.CacheWriteTokens, got.Usage.CacheReadTokens}
			if actual != tc.want || !got.Found {
				t.Fatalf("got %+v want %v", got, tc.want)
			}
			if tc.notes && (got.Usage.Detail == nil || len(got.Usage.Detail.Notes) == 0) {
				t.Fatal("missing fallback explanation")
			}
			if tc.name == "per iteration TTL invalid" && len(got.Usage.Detail.Tokens) > 0 {
				t.Fatalf("invalid TTL retained %+v", got.Usage.Detail)
			}
		})
	}
}
