package gateway

import (
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func TestEffectiveOutputIgnoresHeartbeatsMetadataEmptyAndInvalidJSON(t *testing.T) {
	for _, test := range []struct {
		format        channel.Format
		empty, output string
	}{
		{channel.FormatOpenAIChat, `{"choices":[{"delta":{"role":"assistant","content":""}}]}`, `{"choices":[{"delta":{"content":"有用🌍"}}]}`},
		{channel.FormatOpenAIResponses, `{"type":"response.output_text.delta","delta":""}`, `{"type":"response.output_text.delta","delta":"有用🌍"}`},
		{channel.FormatAnthropic, `{"type":"content_block_delta","delta":{"type":"text_delta","text":""}}`, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"有用🌍"}}`},
		{channel.FormatGemini, `{"candidates":[{"content":{"parts":[{"text":""}]}}]}`, `{"candidates":[{"content":{"parts":[{"text":"有用🌍"}]}}]}`},
	} {
		t.Run(string(test.format), func(t *testing.T) {
			base := time.Unix(1, 0)
			o := NewObserver(test.format, true, "text/event-stream")
			o.Write([]byte(": heartbeat\n\n"), base.Add(100*time.Millisecond))
			o.Write([]byte("data: {\"type\":\"metadata\",\"unknown\":{\"text\":\"not output\"}}\n\n"), base.Add(200*time.Millisecond))
			o.Write([]byte("data: "+test.empty+"\n\n"), base.Add(300*time.Millisecond))
			o.Write([]byte("data: "+test.output+" trailing-invalid\n\n"), base.Add(350*time.Millisecond))
			if !o.firstOutputAt.IsZero() {
				t.Fatal("heartbeat/metadata/empty/invalid frame counted as output")
			}
			packet := []byte("data: " + test.output + "\n\n")
			for _, b := range packet {
				o.Write([]byte{b}, base.Add(400*time.Millisecond))
			}
			o.Write([]byte(": heartbeat\n\n"), base.Add(600*time.Millisecond))
			result := o.Finish()
			if result.FirstOutputAt != base.Add(400*time.Millisecond) || result.FirstVisibleOutputAt != result.FirstOutputAt || result.OutputKind != "text" {
				t.Fatal("valid output timing lost on split UTF8/SSE", result.OutputKind, result.FirstOutputAt)
			}
		})
	}
}

func TestEffectiveOutputRecognizesNonTextWithoutInventingVisibleTokens(t *testing.T) {
	for _, test := range []struct {
		format     channel.Format
		body, kind string
	}{
		{channel.FormatOpenAIChat, `{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{\"x\":"}}]}}]}`, "tool"},
		{channel.FormatOpenAIResponses, `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"opaque+/=="}}`, "opaque"},
		{channel.FormatAnthropic, `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"synthetic"}}`, "reasoning"},
		{channel.FormatGemini, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"synthetic","args":{}}}]}}]}`, "tool"},
		{channel.FormatGemini, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"audio/pcm","data":"YQ=="}}]}}]}`, "audio"},
		{channel.FormatOpenAIImages, `{"data":[{"b64_json":"YQ=="}]}`, "image"},
	} {
		t.Run(string(test.format)+test.kind, func(t *testing.T) {
			o := NewObserver(test.format, true, "text/event-stream")
			at := time.Unix(1, 0)
			o.Write([]byte("data: "+test.body+"\n\n"), at)
			got := o.Finish()
			if got.OutputKind != test.kind || got.FirstOutputAt != at {
				t.Fatal("non-text output was omitted", got.OutputKind)
			}
			if test.kind != "tool" && !got.FirstVisibleOutputAt.IsZero() {
				t.Fatal("opaque/media/reasoning invented visible token start")
			}
		})
	}
}

func TestEffectiveOutputSeparatesReasoningVisibleStartAndTerminalUsage(t *testing.T) {
	o := NewObserver(channel.FormatOpenAIResponses, true, "text/event-stream")
	base := time.Unix(1, 0)
	o.Write([]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"synthetic\"}\n\n"), base.Add(100*time.Millisecond))
	o.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n"), base.Add(400*time.Millisecond))
	o.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":100,\"output_tokens_details\":{\"reasoning_tokens\":20}}}}\n\n"), base.Add(900*time.Millisecond))
	got := o.Finish()
	if got.FirstOutputAt != base.Add(100*time.Millisecond) || got.FirstVisibleOutputAt != base.Add(400*time.Millisecond) || got.OutputEndAt != base.Add(900*time.Millisecond) || !got.Found || got.Usage.OutputTokens != 100 || !got.ReasoningOutput {
		t.Fatal("reasoning/generation window mixed with metadata", got)
	}
}

func BenchmarkEffectiveOutputTypicalDelta(b *testing.B) {
	o := NewObserver(channel.FormatOpenAIResponses, true, "text/event-stream")
	at := time.Unix(1, 0)
	raw := []byte(`{"type":"response.output_text.delta","sequence_number":4,"item_id":"msg_synthetic","output_index":0,"delta":"a short answer"}`)
	o.trackEffectiveOutput(raw, at)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		o.trackEffectiveOutput(raw, at)
	}
}
