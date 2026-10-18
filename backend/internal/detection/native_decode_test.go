package detection

import (
	"encoding/json"
	"strings"
	"testing"
)

var nativeJSONFixtures = map[string]string{
	"openai-chat-completions": `{"id":"chatcmpl_fixture","object":"chat.completion","created":1,"model":"synthetic-model","choices":[{"index":0,"message":{"role":"assistant","content":"你好 🌍"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`,
	"openai-responses":        `{"id":"resp_fixture","object":"response","created_at":1,"model":"synthetic-model","status":"completed","output":[{"id":"msg_fixture","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"你好 🌍","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}`,
	"native-messages":         `{"id":"msg_fixture","type":"message","role":"assistant","model":"synthetic-model","content":[{"type":"text","text":"你好 🌍"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":4}}`,
	"native-generate-content": `{"responseId":"fixture","modelVersion":"synthetic-model","candidates":[{"content":{"role":"model","parts":[{"text":"你好 🌍"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":4,"totalTokenCount":14}}`,
}

func TestNativeJSONStructureAndUsageIndependent(t *testing.T) {
	for protocol, body := range nativeJSONFixtures {
		t.Run(protocol, func(t *testing.T) {
			o, err := decodeNative([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			r := parseNative(protocol, o)
			if !r.Structure || !r.Usage.Reliable || r.Usage.Input != 10 || r.Usage.Output != 4 || r.Text != "你好 🌍" || len(r.Candidates) != 1 {
				t.Fatalf("invalid fixture: %+v", r)
			}
			for _, key := range []string{"usage", "usageMetadata"} {
				if o[key] != nil {
					delete(o, key)
				}
			}
			r = parseNative(protocol, o)
			if !r.Structure || r.Usage.Reliable {
				t.Fatal("missing usage confused with response structure")
			}
		})
	}
	for _, body := range []string{`{"usage":{"input_tokens":1,"input_tokens":2}}`, `{"a":[{"b":1,"b":2}]}`, `{} {}`, `[]`, "{\"a\":\"\xff\"}", strings.Repeat(`{"a":`, 70) + `0` + strings.Repeat(`}`, 70)} {
		if _, err := decodeNative([]byte(body)); err == nil {
			t.Fatalf("invalid native json accepted: %.100s", body)
		}
	}
}

func TestNativeUsageRejectsInvalidTotalsAndSubset(t *testing.T) {
	for protocol, body := range nativeJSONFixtures {
		t.Run(protocol, func(t *testing.T) {
			o, _ := decodeNative([]byte(body))
			key := "usage"
			if protocol == "native-generate-content" {
				key = "usageMetadata"
			}
			u := object(o[key])
			inputKey := "input_tokens"
			switch protocol {
			case "openai-chat-completions":
				inputKey = "prompt_tokens"
			case "native-generate-content":
				inputKey = "promptTokenCount"
			}
			for _, invalid := range []any{json.Number("-1"), json.Number("0.5"), "10", nil, json.Number("9223372036854775808")} {
				u[inputKey] = invalid
				if nativeUsageFrom(protocol, o).Reliable {
					t.Fatalf("invalid count accepted: %v", invalid)
				}
			}
		})
	}
	o, _ := decodeNative([]byte(nativeJSONFixtures["openai-responses"]))
	u := object(o["usage"])
	u["total_tokens"] = json.Number("15")
	if nativeUsageFrom("openai-responses", o).Reliable {
		t.Fatal("mismatched total accepted")
	}
	u["total_tokens"] = json.Number("14")
	u["input_tokens_details"] = nativeObject{"cached_tokens": json.Number("11")}
	if nativeUsageFrom("openai-responses", o).Reliable {
		t.Fatal("cached subset overflow accepted")
	}
}

func eventJSON(kind string, value any) string {
	b, _ := json.Marshal(value)
	prefix := ""
	if kind != "" {
		prefix = "event: " + kind + "\n"
	}
	return prefix + "data: " + string(b) + "\n\n"
}
func typedEvent(kind string, fields nativeObject) string {
	fields["type"] = kind
	return eventJSON(kind, fields)
}
func chatStreamFixture() string {
	return `data: {"id":"chatcmpl_fixture","object":"chat.completion.chunk","created":1,"model":"synthetic-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}` + "\n\n" + `data: {"id":"chatcmpl_fixture","object":"chat.completion.chunk","created":1,"model":"synthetic-model","choices":[{"index":0,"delta":{"content":"你好 🌍"},"finish_reason":null}]}` + "\n\n" + `data: {"id":"chatcmpl_fixture","object":"chat.completion.chunk","created":1,"model":"synthetic-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" + `data: {"id":"chatcmpl_fixture","object":"chat.completion.chunk","created":1,"model":"synthetic-model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}` + "\n\ndata: [DONE]\n\n"
}
func responseStreamFixture() string {
	final, _ := decodeNative([]byte(nativeJSONFixtures["openai-responses"]))
	item := object(list(final["output"])[0])
	initial := nativeObject{"id": "resp_fixture", "object": "response", "created_at": 1, "model": "synthetic-model", "status": "in_progress", "output": []any{}}
	return typedEvent("response.created", nativeObject{"sequence_number": 0, "response": initial}) +
		typedEvent("response.output_item.added", nativeObject{"sequence_number": 1, "output_index": 0, "item": nativeObject{"id": "msg_fixture", "type": "message", "role": "assistant", "content": []any{}}}) +
		typedEvent("response.content_part.added", nativeObject{"sequence_number": 2, "output_index": 0, "content_index": 0, "item_id": "msg_fixture", "part": nativeObject{"type": "output_text", "text": "", "annotations": []any{}}}) +
		typedEvent("response.output_text.delta", nativeObject{"sequence_number": 3, "output_index": 0, "content_index": 0, "item_id": "msg_fixture", "delta": "你好 🌍"}) +
		typedEvent("response.output_text.done", nativeObject{"sequence_number": 4, "output_index": 0, "content_index": 0, "item_id": "msg_fixture", "text": "你好 🌍"}) +
		typedEvent("response.content_part.done", nativeObject{"sequence_number": 5, "output_index": 0, "content_index": 0, "item_id": "msg_fixture", "part": list(item["content"])[0]}) +
		typedEvent("response.output_item.done", nativeObject{"sequence_number": 6, "output_index": 0, "item": item}) +
		typedEvent("response.completed", nativeObject{"sequence_number": 7, "response": final})
}
func messageStreamFixture() string {
	return typedEvent("message_start", nativeObject{"message": nativeObject{"id": "msg_fixture", "type": "message", "role": "assistant", "model": "synthetic-model", "content": []any{}, "usage": nativeObject{"input_tokens": 10, "output_tokens": 1}}}) +
		typedEvent("content_block_start", nativeObject{"index": 0, "content_block": nativeObject{"type": "text", "text": ""}}) +
		typedEvent("content_block_delta", nativeObject{"index": 0, "delta": nativeObject{"type": "text_delta", "text": "你好 🌍"}}) +
		typedEvent("content_block_stop", nativeObject{"index": 0}) +
		typedEvent("message_delta", nativeObject{"delta": nativeObject{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": nativeObject{"output_tokens": 4}}) +
		typedEvent("message_stop", nativeObject{})
}
func geminiStreamFixture() string {
	return eventJSON("", nativeObject{"responseId": "fixture", "candidates": []any{nativeObject{"index": 0, "content": nativeObject{"role": "model", "parts": []any{nativeObject{"text": "你好 "}}}}}}) + eventJSON("", nativeObject{"responseId": "fixture", "candidates": []any{nativeObject{"index": 0, "content": nativeObject{"role": "model", "parts": []any{nativeObject{"text": "🌍"}}}, "finishReason": "STOP"}}, "usageMetadata": nativeObject{"promptTokenCount": 10, "candidatesTokenCount": 4, "totalTokenCount": 14}})
}

func TestNativeStreamsAndTerminalBoundaries(t *testing.T) {
	for protocol, body := range map[string]string{"openai-chat-completions": chatStreamFixture(), "openai-responses": responseStreamFixture(), "native-messages": messageStreamFixture(), "native-generate-content": geminiStreamFixture()} {
		t.Run(protocol, func(t *testing.T) {
			for _, separator := range []string{"\n", "\r\n", "\r"} {
				b := strings.ReplaceAll(body, "\n", separator)
				r := parseNativeStream(protocol, []byte(b), 1<<20)
				if !r.Valid || !r.Terminal || !r.TextLifecycle || !r.Reply.Usage.Reliable || r.Reply.Text != "你好 🌍" || r.Reply.Usage.Output != 4 {
					t.Fatalf("stream invalid: %+v", r)
				}
			}
			for _, corrupt := range []string{strings.TrimSuffix(body, "\n"), body + eventJSON("", nativeObject{"unexpected": "after-terminal"}), strings.ReplaceAll(body, "你好", "\xff")} {
				if r := parseNativeStream(protocol, []byte(corrupt), 1<<20); r.Valid {
					t.Fatalf("corrupt stream accepted: %.60s", corrupt)
				}
			}
		})
	}
}
func TestStreamDoesNotAcceptWrongIndexesOrMissingLifecycle(t *testing.T) {
	for _, body := range []string{
		strings.Replace(chatStreamFixture(), `"index":0,"delta":{"content"`, `"index":1,"delta":{"content"`, 1),
		strings.Replace(responseStreamFixture(), `"content_index":0,"delta"`, `"content_index":1,"delta"`, 1),
		strings.Replace(responseStreamFixture(), `"item_id":"msg_fixture"`, `"item_id":"wrong_item"`, 1),
		strings.Replace(responseStreamFixture(), `"sequence_number":3`, `"sequence_number":1`, 1),
		strings.Replace(messageStreamFixture(), `"index":0,"type":"content_block_delta"`, `"index":1,"type":"content_block_delta"`, 1),
		strings.Replace(geminiStreamFixture(), `"finishReason":"STOP"`, `"finishReason":""`, 1),
	} {
		protocol := "openai-responses"
		if strings.Contains(body, "chat.completion.chunk") {
			protocol = "openai-chat-completions"
		} else if strings.Contains(body, "message_start") {
			protocol = "native-messages"
		} else if strings.Contains(body, "usageMetadata") {
			protocol = "native-generate-content"
		}
		if r := parseNativeStream(protocol, []byte(body), 1<<20); r.Valid {
			t.Fatalf("invalid index/lifecycle accepted: %s", protocol)
		}
	}
}
