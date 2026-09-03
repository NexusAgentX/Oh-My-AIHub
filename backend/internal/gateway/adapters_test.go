package gateway

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

func TestProtocolAdaptersNormalizeUsageAndRestoreModels(t *testing.T) {
	tests := []struct {
		name     string
		protocol channel.Protocol
		body     string
		want     ledger.UsageV1
		field    string
	}{
		{
			name: "chat", protocol: channel.ProtocolOpenAIChat,
			body: `{"id":"chat-1","object":"chat.completion","model":"vendor-chat","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":11,"total_tokens":111,"prompt_tokens_details":{"cached_tokens":25,"cache_write_tokens":15}}}`,
			want: ledger.UsageV1{InputTokens: 60, OutputTokens: 11, CacheWriteTokens: 15, CacheReadTokens: 25}, field: "model",
		},
		{
			name: "responses", protocol: channel.ProtocolOpenAIResponse,
			body: `{"id":"resp","object":"response","model":"vendor-response","status":"completed","output":[{"id":"msg-1","type":"message","status":"completed","role":"assistant","content":[]}],"usage":{"input_tokens":90,"output_tokens":12,"total_tokens":102,"input_tokens_details":{"cached_tokens":20,"cache_write_tokens":10}}}`,
			want: ledger.UsageV1{InputTokens: 60, OutputTokens: 12, CacheWriteTokens: 10, CacheReadTokens: 20}, field: "model",
		},
		{
			name: "anthropic", protocol: channel.ProtocolAnthropic,
			body: `{"id":"msg-1","type":"message","role":"assistant","model":"vendor-claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":60,"output_tokens":13,"cache_creation_input_tokens":15,"cache_read_input_tokens":10}}`,
			want: ledger.UsageV1{InputTokens: 60, OutputTokens: 13, CacheWriteTokens: 15, CacheReadTokens: 10}, field: "model",
		},
		{
			name: "gemini", protocol: channel.ProtocolGemini,
			body: `{"modelVersion":"vendor-gemini","candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":80,"candidatesTokenCount":14,"cachedContentTokenCount":30}}`,
			want: ledger.UsageV1{InputTokens: 50, OutputTokens: 14, CacheReadTokens: 30}, field: "modelVersion",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rewritten, usage, err := RewriteNonStreamingResponse(test.protocol, []byte(test.body), "canonical/model", 1)
			if err != nil {
				t.Fatal(err)
			}
			if usage == nil || *usage != test.want {
				t.Fatalf("usage = %+v, want %+v", usage, test.want)
			}
			var value map[string]any
			if err := json.Unmarshal(rewritten, &value); err != nil {
				t.Fatal(err)
			}
			if value[test.field] != "canonical/model" {
				t.Fatalf("restored model = %#v", value[test.field])
			}
		})
	}

	invalid := []struct {
		protocol channel.Protocol
		body     string
	}{
		{channel.ProtocolOpenAIChat, `{"id":"chat-1","object":"chat.completion","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":6}}}`},
		{channel.ProtocolOpenAIChat, `{"id":"chat-1","object":"chat.completion","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":3,"cache_write_tokens":3}}}`},
		{channel.ProtocolOpenAIResponse, `{"id":"resp-1","object":"response","model":"x","status":"completed","output":[{"id":"msg-1","type":"message","status":"completed","role":"assistant","content":[]}],"usage":{"input_tokens":5,"output_tokens":1,"input_tokens_details":{"cached_tokens":3,"cache_write_tokens":3}}}`},
		{channel.ProtocolOpenAIResponse, `{"id":"resp-1","object":"response","model":"x","status":"completed","output":[{"id":"msg-1","type":"message","status":"completed","role":"assistant","content":[]}],"usage":{"input_tokens":1}}`},
		{channel.ProtocolAnthropic, `{"id":"msg-1","type":"message","role":"assistant","model":"x","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":-1,"output_tokens":1}}`},
		{channel.ProtocolGemini, `{"modelVersion":"x","candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"cachedContentTokenCount":3}}`},
	}
	for _, test := range invalid {
		if _, _, err := RewriteNonStreamingResponse(test.protocol, []byte(test.body), "canonical/model", 1); !errors.Is(err, ErrNoUsage) {
			t.Fatalf("invalid usage for %s = %v, want ErrNoUsage", test.protocol, err)
		}
	}
}

func TestOpenAIStreamingUsageSeparatesCacheWriteTokens(t *testing.T) {
	tests := []struct {
		protocol channel.Protocol
		frame    string
		want     ledger.UsageV1
	}{
		{
			protocol: channel.ProtocolOpenAIChat,
			frame:    `data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":3,"cache_write_tokens":2}}}` + "\n\n",
			want:     ledger.UsageV1{InputTokens: 5, OutputTokens: 2, CacheWriteTokens: 2, CacheReadTokens: 3},
		},
		{
			protocol: channel.ProtocolOpenAIResponse,
			frame:    `data: {"type":"response.completed","response":{"id":"resp-1","object":"response","status":"completed","model":"vendor-response","output":[{"id":"msg-1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15,"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":2}}}}` + "\n\n",
			want:     ledger.UsageV1{InputTokens: 6, OutputTokens: 3, CacheWriteTokens: 2, CacheReadTokens: 4},
		},
	}
	for _, test := range tests {
		analysis, err := AnalyzeSSEFrame(test.protocol, []byte(test.frame), "canonical/model")
		if err != nil {
			t.Fatal(err)
		}
		usage, complete := analysis.Observation.Complete()
		if !complete || usage == nil || *usage != test.want {
			t.Fatalf("%s streaming usage = %+v complete=%t, want %+v", test.protocol, usage, complete, test.want)
		}
	}
}

func TestRewrittenJSONUsesStableEncodingAndEnforcesTheEncodedLimit(t *testing.T) {
	value := map[string]any{"content": "<tag>&" + strings.Repeat("\u2028", 8)}
	encoded, err := marshalJSONObject(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `\u003c`) || strings.Contains(string(encoded), `\u003e`) || strings.Contains(string(encoded), `\u0026`) {
		t.Fatalf("HTML-sensitive data was needlessly expanded: %s", encoded)
	}
	if _, err := marshalJSONObjectWithin(value, int64(len(encoded)-1)); !errors.Is(err, ErrResponseTooBig) {
		t.Fatalf("rewritten-size limit = %v, want response-too-big", err)
	}
}

func TestRequestRewriteStartsFromOriginalAndForcesChatUsage(t *testing.T) {
	original := []byte(`{"model":"canonical/model","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	first, err := RewriteRequest(channel.ProtocolOpenAIChat, original, "vendor-one", true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RewriteRequest(channel.ProtocolOpenAIChat, original, "vendor-two", true)
	if err != nil {
		t.Fatal(err)
	}
	for index, encoded := range [][]byte{first, second} {
		var value map[string]any
		if err := json.Unmarshal(encoded, &value); err != nil {
			t.Fatal(err)
		}
		wantModel := []string{"vendor-one", "vendor-two"}[index]
		if value["model"] != wantModel {
			t.Fatalf("rewrite %d model = %v, want %s", index, value["model"], wantModel)
		}
		options, _ := value["stream_options"].(map[string]any)
		if options["include_usage"] != true {
			t.Fatalf("rewrite %d stream options = %#v", index, options)
		}
	}
	if strings.Contains(string(second), "vendor-one") || !strings.Contains(string(original), "canonical/model") {
		t.Fatal("candidate rewrite mutated or reused the previous candidate body")
	}
}

func TestNonStreamingRejectsErrorEnvelopesAndUsageOnlyPayloads(t *testing.T) {
	tests := []struct {
		name      string
		protocol  channel.Protocol
		error     string
		usageOnly string
	}{
		{
			name: "chat", protocol: channel.ProtocolOpenAIChat,
			error:     `{"object":"chat.completion","choices":[{"message":{"role":"assistant"},"finish_reason":"stop"}],"error":{"code":"bad","message":"failed"},"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
			usageOnly: `{"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
		},
		{
			name: "responses", protocol: channel.ProtocolOpenAIResponse,
			error:     `{"object":"response","status":"completed","output":[],"error":{"code":"bad","message":"failed"},"usage":{"input_tokens":1,"output_tokens":1}}`,
			usageOnly: `{"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		{
			name: "anthropic", protocol: channel.ProtocolAnthropic,
			error:     `{"type":"message","role":"assistant","content":[],"stop_reason":"end_turn","error":{"type":"bad","message":"failed"},"usage":{"input_tokens":1,"output_tokens":1}}`,
			usageOnly: `{"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		{
			name: "gemini", protocol: channel.ProtocolGemini,
			error:     `{"candidates":[{"finishReason":"STOP"}],"error":{"status":"BAD","message":"failed"},"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`,
			usageOnly: `{"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, body := range []string{test.error, test.usageOnly} {
				if _, usage, err := RewriteNonStreamingResponse(test.protocol, []byte(body), "canonical/model", 1); err == nil || usage != nil {
					t.Fatalf("invalid 200 payload accepted: usage=%+v err=%v body=%s", usage, err, body)
				}
			}
		})
	}
}

func TestUsageRejectsInvalidOptionalFieldsAndCountsGeminiThoughts(t *testing.T) {
	invalid := []struct {
		protocol channel.Protocol
		body     string
	}{
		{channel.ProtocolOpenAIChat, `{"id":"chat-1","object":"chat.completion","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":-1}}}`},
		{channel.ProtocolOpenAIChat, `{"id":"chat-1","object":"chat.completion","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":3}}`},
		{channel.ProtocolOpenAIResponse, `{"id":"resp-1","object":"response","model":"x","status":"completed","output":[],"usage":{"input_tokens":9223372036854775807,"output_tokens":1}}`},
		{channel.ProtocolAnthropic, `{"id":"msg-1","type":"message","role":"assistant","model":"x","content":[],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":"1"}}`},
		{channel.ProtocolGemini, `{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"thoughtsTokenCount":-1}}`},
		{channel.ProtocolGemini, `{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"toolUsePromptTokenCount":3,"thoughtsTokenCount":3,"totalTokenCount":11}}`},
		{channel.ProtocolGemini, `{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":9223372036854775807,"toolUsePromptTokenCount":1,"candidatesTokenCount":0}}`},
	}
	for _, test := range invalid {
		if _, _, err := RewriteNonStreamingResponse(test.protocol, []byte(test.body), "canonical/model", 1); !errors.Is(err, ErrNoUsage) {
			t.Fatalf("invalid optional usage accepted for %s: %v", test.protocol, err)
		}
	}
	_, usage, err := RewriteNonStreamingResponse(channel.ProtocolGemini, []byte(`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"thoughtsTokenCount":3,"toolUsePromptTokenCount":3,"cachedContentTokenCount":1,"totalTokenCount":12}}`), "canonical/model", 1)
	if err != nil || usage == nil || usage.InputTokens != 6 || usage.OutputTokens != 5 || usage.CacheReadTokens != 1 {
		t.Fatalf("Gemini tool/thought/cache usage = %+v, %v", usage, err)
	}
	analysis, err := AnalyzeSSEFrame(channel.ProtocolGemini, []byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":4,\"candidatesTokenCount\":2,\"thoughtsTokenCount\":3,\"toolUsePromptTokenCount\":3,\"cachedContentTokenCount\":1,\"totalTokenCount\":12}}\n\n"), "canonical/model")
	streamUsage, complete := analysis.Observation.Complete()
	if err != nil || !complete || streamUsage == nil || streamUsage.InputTokens != 6 || streamUsage.OutputTokens != 5 || streamUsage.CacheReadTokens != 1 {
		t.Fatalf("Gemini streamed tool/thought/cache usage = %+v complete=%t err=%v", streamUsage, complete, err)
	}
	for _, frame := range []string{
		"data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":4,\"candidatesTokenCount\":2,\"toolUsePromptTokenCount\":3,\"thoughtsTokenCount\":3,\"totalTokenCount\":11}}\n\n",
		"data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":9223372036854775807,\"toolUsePromptTokenCount\":1}}\n\n",
	} {
		analysis, err := AnalyzeSSEFrame(channel.ProtocolGemini, []byte(frame), "canonical/model")
		if err != nil {
			t.Fatal(err)
		}
		if usage, complete := analysis.Observation.Complete(); complete || usage != nil {
			t.Fatalf("ambiguous Gemini stream usage accepted: %+v", usage)
		}
	}
}

func TestSSESemanticAndTerminalClassification(t *testing.T) {
	tests := []struct {
		name     string
		protocol channel.Protocol
		frame    string
		semantic bool
		terminal bool
	}{
		{"chat role", channel.ProtocolOpenAIChat, `data: {"model":"vendor","choices":[{"delta":{"role":"assistant"}}]}

`, false, false},
		{"chat content", channel.ProtocolOpenAIChat, `data: {"model":"vendor","choices":[{"delta":{"content":"hi"}}]}

`, true, false},
		{"chat tool", channel.ProtocolOpenAIChat, `data: {"choices":[{"delta":{"tool_calls":[{"id":"x","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}

`, true, false},
		{"chat refusal", channel.ProtocolOpenAIChat, `data: {"choices":[{"delta":{"refusal":"cannot comply"}}]}

`, true, false},
		{"chat finish", channel.ProtocolOpenAIChat, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

`, false, false},
		{"chat usage", channel.ProtocolOpenAIChat, `data: {"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1}}

`, false, true},
		{"responses metadata", channel.ProtocolOpenAIResponse, `data: {"type":"response.created","response":{"id":"resp-1","object":"response","status":"in_progress","model":"vendor","output":[]}}

`, false, false},
		{"responses text", channel.ProtocolOpenAIResponse, `data: {"type":"response.output_text.delta","delta":"hi"}

`, true, false},
		{"responses refusal", channel.ProtocolOpenAIResponse, `data: {"type":"response.refusal.delta","delta":"cannot comply"}

`, true, false},
		{"responses terminal", channel.ProtocolOpenAIResponse, `data: {"type":"response.completed","response":{"id":"resp-1","object":"response","status":"completed","model":"vendor","output":[],"usage":{"input_tokens":2,"output_tokens":1}}}

`, false, true},
		{"responses terminal output", channel.ProtocolOpenAIResponse, `data: {"type":"response.completed","response":{"id":"resp-1","object":"response","status":"completed","model":"vendor","output":[{"id":"msg-1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":2,"output_tokens":1}}}

`, true, true},
		{"anthropic start", channel.ProtocolAnthropic, `data: {"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","model":"vendor","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":2,"output_tokens":0}}}

`, false, false},
		{"anthropic text", channel.ProtocolAnthropic, `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}

`, true, false},
		{"anthropic thinking", channel.ProtocolAnthropic, `data: {"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"reason"}}

`, true, false},
		{"anthropic signature", channel.ProtocolAnthropic, `data: {"type":"content_block_delta","delta":{"type":"signature_delta","signature":"sig"}}

`, true, false},
		{"anthropic stop", channel.ProtocolAnthropic, `data: {"type":"message_stop"}

`, false, true},
		{"gemini content", channel.ProtocolGemini, `data: {"modelVersion":"vendor","candidates":[{"content":{"parts":[{"text":"hi"}]}}]}

`, true, false},
		{"gemini terminal", channel.ProtocolGemini, `data: {"modelVersion":"vendor","candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1}}

`, false, true},
		{"done", channel.ProtocolOpenAIChat, "data: [DONE]\n\n", false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			analysis, err := AnalyzeSSEFrame(test.protocol, []byte(test.frame), "canonical/model")
			if err != nil {
				t.Fatal(err)
			}
			if analysis.Semantic != test.semantic || analysis.Terminal != test.terminal {
				t.Fatalf("classification semantic/terminal = %t/%t, want %t/%t", analysis.Semantic, analysis.Terminal, test.semantic, test.terminal)
			}
			if strings.Contains(test.frame, "vendor") && !strings.Contains(string(analysis.Frame), "canonical/model") {
				t.Fatalf("model metadata not restored: %s", analysis.Frame)
			}
		})
	}
}

func TestSSEProtocolEndsAndNativeErrors(t *testing.T) {
	for _, protocol := range []channel.Protocol{channel.ProtocolOpenAIResponse, channel.ProtocolAnthropic, channel.ProtocolGemini} {
		if _, err := AnalyzeSSEFrame(protocol, []byte("data: [DONE]\n\n"), "canonical/model"); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s accepted Chat-only DONE marker: %v", protocol, err)
		}
	}
	errorsByProtocol := []struct {
		protocol channel.Protocol
		frame    string
	}{
		{channel.ProtocolOpenAIChat, `data: {"error":{"code":"relay_busy","message":"retry later"},"secret":"DO_NOT_PERSIST"}

`},
		{channel.ProtocolOpenAIResponse, "event: error\ndata: {\"type\":\"error\",\"code\":\"relay_busy\",\"message\":\"retry later\",\"secret\":\"DO_NOT_PERSIST\"}\n\n"},
		{channel.ProtocolAnthropic, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"relay_busy\",\"message\":\"retry later\"},\"secret\":\"DO_NOT_PERSIST\"}\n\n"},
		{channel.ProtocolGemini, `data: {"error":{"status":"RELAY_BUSY","message":"retry later"},"secret":"DO_NOT_PERSIST"}

`},
	}
	for _, test := range errorsByProtocol {
		analysis, err := AnalyzeSSEFrame(test.protocol, []byte(test.frame), "canonical/model")
		if err != nil || analysis.ErrorCode == "" || analysis.ErrorMessage != "retry later" {
			t.Fatalf("%s native stream error = %+v, %v", test.protocol, analysis, err)
		}
	}
}

func TestGeminiPathValidation(t *testing.T) {
	valid := []struct {
		path   string
		model  string
		stream bool
	}{
		{"/v1beta/models/google/gemini-2.5:generateContent", "google/gemini-2.5", false},
		{"/v1beta/models/gemini-2.5:streamGenerateContent?alt=sse", "gemini-2.5", true},
	}
	for _, test := range valid {
		r := httptest.NewRequest("POST", test.path, nil)
		model, stream, err := ParseGeminiRequestPath(r)
		if err != nil || model != test.model || stream != test.stream {
			t.Fatalf("valid path %s = %q/%t/%v", test.path, model, stream, err)
		}
	}
	invalid := []string{
		"/v1beta/models/google%2Fgemini:generateContent",
		"/v1beta/models/google%252Fgemini:generateContent",
		"/v1beta/models/../gemini:generateContent",
		"/v1beta/models/gemini:other:generateContent",
		"/v1beta/models/gemini:generateContent?key=secret",
		"/v1beta/models/gemini:generateContent?alt=sse",
		"/v1beta/models/gemini:streamGenerateContent",
		"/v1beta/models/gemini:streamGenerateContent?alt=json",
		"/v1beta/models/gemini:streamGenerateContent?alt=sse&extra=1",
	}
	for _, path := range invalid {
		r := httptest.NewRequest("POST", path, nil)
		if _, _, err := ParseGeminiRequestPath(r); err == nil {
			t.Fatalf("invalid Gemini path accepted: %s (path=%s escaped=%s)", path, r.URL.Path, r.URL.EscapedPath())
		}
	}
	invalidQueries := []struct {
		protocol channel.Protocol
		stream   bool
		raw      string
	}{
		{channel.ProtocolOpenAIChat, false, "key=secret"},
		{channel.ProtocolOpenAIResponse, false, "trace=1"},
		{channel.ProtocolAnthropic, true, "%zz"},
		{channel.ProtocolGemini, true, "alt=sse&alt=sse"},
		{channel.ProtocolGemini, true, ""},
		{channel.ProtocolGemini, true, "alt=%73se"},
		{channel.ProtocolGemini, true, "alt=sse;key=secret"},
		{channel.ProtocolGemini, false, "alt=sse"},
	}
	for _, test := range invalidQueries {
		if err := validateProtocolQuery(test.protocol, test.stream, test.raw); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid raw query accepted for %s: %q (%v)", test.protocol, test.raw, err)
		}
	}
	for _, test := range []struct {
		stream bool
		raw    string
	}{{false, ""}, {true, "alt=sse"}} {
		if err := validateProtocolQuery(channel.ProtocolGemini, test.stream, test.raw); err != nil {
			t.Fatalf("valid Gemini query rejected: stream=%t raw=%q (%v)", test.stream, test.raw, err)
		}
	}
}

func TestParseChatRequestTracksChoiceCountBeforeBillingValidation(t *testing.T) {
	parsed, err := ParseRequest(channel.ProtocolOpenAIChat, []byte(`{"model":"canonical/model","stream":true,"n":2}`))
	if err != nil || !parsed.Stream || parsed.ExpectedChoices != 2 {
		t.Fatalf("parsed chat choice count = %+v, %v", parsed, err)
	}
	for _, body := range []string{
		`{"model":"canonical/model","stream":true,"n":0}`,
		`{"model":"canonical/model","stream":true,"n":1.5}`,
		`{"model":"canonical/model","stream":true,"n":"2"}`,
	} {
		if _, err := ParseRequest(channel.ProtocolOpenAIChat, []byte(body)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid choice count accepted: %s (%v)", body, err)
		}
	}
}

func TestV1BillableRequestAllowsClientToolsAndRejectsUnboundedServerWork(t *testing.T) {
	valid := []struct {
		protocol channel.Protocol
		body     string
	}{
		{channel.ProtocolOpenAIChat, `{"model":"canonical/model","n":1,"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object","properties":{"image":{"type":"string"},"audio":{"type":"string"}}}}}]}`},
		{channel.ProtocolOpenAIResponse, `{"model":"canonical/model","tools":[{"type":"custom","name":"grammar","format":{"type":"text","schema":{"properties":{"file_id":{"type":"string"}}}}}]}`},
		{channel.ProtocolAnthropic, `{"model":"canonical/model","tools":[{"name":"weather","input_schema":{"type":"object","properties":{"source":{"type":"string"},"file":{"type":"string"},"cache_control":{"type":"string"}}},"cache_control":{"type":"ephemeral","ttl":"5m"}}]}`},
		{channel.ProtocolAnthropic, `{"model":"canonical/model","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"weather","input":{"cache_control":"ordinary tool argument"}}]}]}`},
		{channel.ProtocolGemini, `{"generationConfig":{"candidateCount":1},"tools":[{"functionDeclarations":[{"name":"weather","parameters":{"type":"object","properties":{"image":{"type":"string"}}}}]}]}`},
	}
	for _, test := range valid {
		if err := validateV1BillableRequest(test.protocol, []byte(test.body)); err != nil {
			t.Fatalf("client tool rejected for %s: %v", test.protocol, err)
		}
	}

	invalid := []struct {
		protocol channel.Protocol
		body     string
	}{
		{channel.ProtocolOpenAIChat, `{"model":"canonical/model","n":2}`},
		{channel.ProtocolOpenAIChat, `{"model":"canonical/model","web_search_options":{}}`},
		{channel.ProtocolOpenAIChat, `{"model":"canonical/model","tools":[{"type":"web_search"}]}`},
		{channel.ProtocolOpenAIChat, `{"model":"canonical/model","modalities":["text","audio"]}`},
		{channel.ProtocolOpenAIChat, `{"model":"canonical/model","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example/image"}}]}]}`},
		{channel.ProtocolOpenAIResponse, `{"model":"canonical/model","background":true}`},
		{channel.ProtocolOpenAIResponse, `{"model":"canonical/model","previous_response_id":"resp_1"}`},
		{channel.ProtocolOpenAIResponse, `{"model":"canonical/model","tools":[{"type":"file_search"}]}`},
		{channel.ProtocolOpenAIResponse, `{"model":"canonical/model","service_tier":"priority"}`},
		{channel.ProtocolOpenAIResponse, `{"model":"canonical/model","input":[{"role":"user","content":[{"type":"input_file","file_id":"file_1"}]}]}`},
		{channel.ProtocolOpenAIResponse, `{"model":"canonical/model","prompt":{"id":"pmpt_1"}}`},
		{channel.ProtocolOpenAIResponse, `{"model":"canonical/model","store":true}`},
		{channel.ProtocolOpenAIResponse, `{"model":"canonical/model","reasoning":{"mode":"pro"}}`},
		{channel.ProtocolOpenAIResponse, `{"model":"canonical/model","input":[{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_image","image_url":"https://example/image"}]}]}`},
		{channel.ProtocolAnthropic, `{"model":"canonical/model","tools":[{"type":"web_search_20250305","name":"search"}]}`},
		{channel.ProtocolAnthropic, `{"model":"canonical/model","speed":"fast"}`},
		{channel.ProtocolAnthropic, `{"model":"canonical/model","mcp_servers":[{"url":"https://mcp.example"}]}`},
		{channel.ProtocolAnthropic, `{"model":"canonical/model","service_tier":"auto"}`},
		{channel.ProtocolAnthropic, `{"model":"canonical/model","inference_geo":"us"}`},
		{channel.ProtocolAnthropic, `{"model":"canonical/model","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"x"}}]}]}`},
		{channel.ProtocolAnthropic, `{"model":"canonical/model","system":[{"type":"document","source":{"type":"text","data":"x"}}]}`},
		{channel.ProtocolAnthropic, `{"model":"canonical/model","messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`},
		{channel.ProtocolAnthropic, `{"model":"canonical/model","cache_control":{"type":"ephemeral","ttl":"1h"},"messages":[]}`},
		{channel.ProtocolGemini, `{"generationConfig":{"candidateCount":2}}`},
		{channel.ProtocolGemini, `{"tools":[{"googleSearch":{}}]}`},
		{channel.ProtocolGemini, `{"tools":[{"codeExecution":{}}]}`},
		{channel.ProtocolGemini, `{"mcpServers":[{"url":"https://mcp.example"}]}`},
		{channel.ProtocolGemini, `{"cachedContent":"cachedContents/1"}`},
		{channel.ProtocolGemini, `{"generationConfig":{"responseModalities":["AUDIO"]}}`},
		{channel.ProtocolGemini, `{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":"x"}}]}]}`},
		{channel.ProtocolGemini, `{"contents":[{"parts":[{"functionResponse":{"name":"tool","response":{"parts":[{"inlineData":{"data":"x"}}]}}}]}]}`},
		{channel.ProtocolGemini, `{"contents":[{"parts":[{"functionResponse":{"name":"tool","response":{"$ref":"files/1"}}}]}]}`},
	}
	for _, test := range invalid {
		if err := validateV1BillableRequest(test.protocol, []byte(test.body)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("unbounded request accepted for %s: %s (%v)", test.protocol, test.body, err)
		}
	}
}

func TestNonStreamingChatRequiresEveryRequestedChoiceExactlyOnce(t *testing.T) {
	valid := `{"id":"chat-1","object":"chat.completion","model":"x","choices":[` +
		`{"index":0,"message":{"role":"assistant","content":"first"},"finish_reason":"stop"},` +
		`{"index":1,"message":{"role":"assistant","content":"second"},"finish_reason":"stop"}` +
		`],"usage":{"prompt_tokens":4,"completion_tokens":2}}`
	if _, _, err := RewriteNonStreamingResponse(channel.ProtocolOpenAIChat, []byte(valid), "canonical/model", 2); err != nil {
		t.Fatalf("valid n=2 response rejected: %v", err)
	}
	invalid := []string{
		`{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1}}`,
		`{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant"},"finish_reason":"stop"},{"index":0,"message":{"role":"assistant"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`,
		`{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant"},"finish_reason":"stop"},{"index":2,"message":{"role":"assistant"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`,
	}
	for _, body := range invalid {
		if _, usage, err := RewriteNonStreamingResponse(channel.ProtocolOpenAIChat, []byte(body), "canonical/model", 2); err == nil || usage != nil {
			t.Fatalf("invalid n=2 response accepted: %s", body)
		}
	}
}

func TestStreamingCredentialFragmentsPreserveRawStringContinuity(t *testing.T) {
	credential := `secret"with\slashes`
	left, right := credential[:8], credential[8:]
	tests := []struct {
		name     string
		protocol channel.Protocol
		values   []map[string]any
	}{
		{
			name: "chat content", protocol: channel.ProtocolOpenAIChat,
			values: []map[string]any{
				{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": left}}}},
				{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": right}}}},
			},
		},
		{
			name: "responses item", protocol: channel.ProtocolOpenAIResponse,
			values: []map[string]any{
				{"type": "response.output_item.added", "item": map[string]any{"id": "item-1", "type": "function_call", "arguments": left}},
				{"type": "response.output_item.added", "item": map[string]any{"id": "item-1", "type": "function_call", "arguments": right}},
			},
		},
		{
			name: "anthropic tool input", protocol: channel.ProtocolAnthropic,
			values: []map[string]any{
				{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": left}},
				{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": right}},
			},
		},
		{
			name: "gemini function call", protocol: channel.ProtocolGemini,
			values: []map[string]any{
				{"candidates": []any{map[string]any{"index": 0, "content": map[string]any{"parts": []any{map[string]any{"functionCall": map[string]any{"name": "tool", "args": map[string]any{"value": left}}}}}}}},
				{"candidates": []any{map[string]any{"index": 0, "content": map[string]any{"parts": []any{map[string]any{"functionCall": map[string]any{"name": "tool", "args": map[string]any{"value": right}}}}}}}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			guard := newStreamingCredentialGuard(credential)
			for index, value := range test.values {
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				analysis, err := AnalyzeSSEFrame(test.protocol, append(append([]byte("data: "), encoded...), '\n', '\n'), "canonical/model")
				if err != nil {
					t.Fatal(err)
				}
				found, err := guard.containsFragments(analysis.CredentialFragments)
				if err != nil {
					t.Fatal(err)
				}
				if found != (index == 1) {
					t.Fatalf("fragment %d credential match = %t", index, found)
				}
			}
		})
	}
}

func TestStreamingCredentialFragmentsCoverUnknownJSONPaths(t *testing.T) {
	credential := `secret"with\slashes`
	left, right := credential[:8], credential[8:]
	tests := []struct {
		name   string
		values []map[string]any
	}{
		{
			name: "unknown extension value",
			values: []map[string]any{
				{"debug": map[string]any{"trace": left}},
				{"debug": map[string]any{"trace": right}},
			},
		},
		{
			name: "unknown nested array value",
			values: []map[string]any{
				{"debug": []any{map[string]any{"payload": left}}},
				{"debug": []any{map[string]any{"payload": right}}},
			},
		},
		{
			name: "split JSON key",
			values: []map[string]any{
				{"debug": map[string]any{left: true}},
				{"debug": map[string]any{right: true}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			guard := newStreamingCredentialGuard(credential)
			for index, value := range test.values {
				fragments, err := streamingCredentialFragments(channel.ProtocolOpenAIResponse, value)
				if err != nil {
					t.Fatal(err)
				}
				found, err := guard.containsFragments(fragments)
				if err != nil {
					t.Fatal(err)
				}
				if found != (index == 1) {
					t.Fatalf("fragment %d credential match = %t", index, found)
				}
			}
		})
	}

	guard := newStreamingCredentialGuard(credential)
	for _, value := range []map[string]any{
		{"debug": map[string]any{"left_path": left}},
		{"debug": map[string]any{"right_path": right}},
	} {
		fragments, err := streamingCredentialFragments(channel.ProtocolOpenAIResponse, value)
		if err != nil {
			t.Fatal(err)
		}
		if found, err := guard.containsFragments(fragments); err != nil || found {
			t.Fatalf("different JSON paths were joined: found %t, err %v", found, err)
		}
	}
}

func TestStreamingCredentialGuardBoundsDistinctStreams(t *testing.T) {
	guard := newStreamingCredentialGuard("credential")
	fragments := make([]SSECredentialFragment, MaxSSECredentialStreams+1)
	for index := range fragments {
		fragments[index] = SSECredentialFragment{StreamKey: "stream/" + strconv.Itoa(index), Text: "x"}
	}
	if found, err := guard.containsFragments(fragments); found || !errors.Is(err, ErrResponseTooBig) {
		t.Fatalf("high-cardinality credential streams = found %t, err %v", found, err)
	}
}

func TestUpstreamErrorDetailsPersistOnlyParsedFields(t *testing.T) {
	raw := []byte(`{"error":{"code":"relay_busy","message":"upstream overloaded"},"full_body_secret":"DO_NOT_PERSIST","request":{"authorization":"Bearer secret"}}`)
	code, message := UpstreamErrorDetails(channel.ProtocolOpenAIChat, raw)
	if code != "relay_busy" || message != "upstream overloaded" || strings.Contains(message, "DO_NOT_PERSIST") || strings.Contains(message, "Bearer secret") {
		t.Fatalf("parsed upstream error = %q/%q", code, message)
	}
}
