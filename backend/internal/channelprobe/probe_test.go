package channelprobe

import (
	"encoding/json"
	"strings"
	"testing"
)

// The channel probe only proves basic callability. These cases pin the moved
// native validators so a broken copy cannot make every channel look usable.
func TestBasicCallabilityAllowsConsumedHiddenOutputButRejectsEmptyOrErrorReplies(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"id":"fixture","object":"chat.completion","created":1,"model":"native","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":4,"completion_tokens":64,"total_tokens":68}}`, true},
		{`{"id":"fixture","object":"chat.completion","created":1,"model":"native","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}}`, false},
		{`{}`, false}, {`{"error":{"message":"denied"}}`, false},
	}
	for _, c := range cases {
		if got := ValidateBasicReply("openai-chat-completions", []byte(c.body)); got != c.want {
			t.Fatal("native callability result", got, c.want)
		}
	}
}

func TestBasicReplyRequiresOneNativeShapePerProtocol(t *testing.T) {
	valid := map[string]string{
		"openai-chat-completions": `{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`,
		"openai-responses":        `{"id":"r","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`,
		"native-messages":         `{"id":"msg","type":"message","role":"assistant","model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"OK"}],"usage":{"input_tokens":1,"output_tokens":1}}`,
		"native-generate-content": `{"responseId":"g","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`,
	}
	for protocol, body := range valid {
		if !ValidateBasicReply(protocol, []byte(body)) {
			t.Fatalf("%s: valid native reply rejected", protocol)
		}
		// A reply of another protocol never passes as this one.
		for other, foreign := range valid {
			if other != protocol && ValidateBasicReply(protocol, []byte(foreign)) {
				t.Fatalf("%s: accepted %s reply", protocol, other)
			}
		}
	}
}

const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg=="

func TestBasicImageReplyRequiresADecodableProduct(t *testing.T) {
	if !ValidateBasicImageReply("openai-images", []byte(`{"data":[{"b64_json":"`+tinyPNG+`"}]}`)) {
		t.Fatal("decodable image product rejected")
	}
	for _, body := range []string{`{"data":[]}`, `{"data":[{"b64_json":"Tm90IGFuIGltYWdl"}]}`, `{"data":[{"url":"http://insecure.example/a.png"}]}`} {
		if ValidateBasicImageReply("openai-images", []byte(body)) {
			t.Fatal("invalid image product accepted", body)
		}
	}
	gemini := `{"responseId":"g","candidates":[{"index":0,"content":{"role":"model","parts":[{"inlineData":{"mimeType":"image/png","data":"` + tinyPNG + `"}}]},"finishReason":"STOP"}]}`
	if !ValidateBasicImageReply("native-generate-content", []byte(gemini)) {
		t.Fatal("native image inline data rejected")
	}
}

func TestBasicImageRequestUsesTheBoundedNativeImageBody(t *testing.T) {
	var body map[string]any
	if err := json.Unmarshal(BasicImageRequest("openai-responses", "image-model"), &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "image-model" || body["store"] != false || body["max_output_tokens"] != float64(2048) {
		t.Fatal("image request shape", body)
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["type"] != "image_generation" {
		t.Fatal("image request tool", body["tools"])
	}
}

func TestBasicStreamReplyNeedsATerminalNativeStream(t *testing.T) {
	part := map[string]any{"type": "output_text", "text": "OK", "annotations": []any{}}
	item := map[string]any{"id": "msg", "type": "message", "role": "assistant", "status": "completed", "content": []any{part}}
	response := map[string]any{"id": "resp", "object": "response", "created_at": 1, "model": "m", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}
	events := []map[string]any{
		{"type": "response.created", "sequence_number": 0, "response": map[string]any{"id": "resp", "object": "response", "status": "in_progress", "output": []any{}}},
		{"type": "response.output_item.added", "sequence_number": 1, "output_index": 0, "item": map[string]any{"id": "msg", "type": "message", "role": "assistant", "content": []any{}}},
		{"type": "response.content_part.added", "sequence_number": 2, "output_index": 0, "content_index": 0, "item_id": "msg", "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}},
		{"type": "response.output_text.delta", "sequence_number": 3, "output_index": 0, "content_index": 0, "item_id": "msg", "delta": "OK"},
		{"type": "response.output_text.done", "sequence_number": 4, "output_index": 0, "content_index": 0, "item_id": "msg", "text": "OK"},
		{"type": "response.content_part.done", "sequence_number": 5, "output_index": 0, "content_index": 0, "item_id": "msg", "part": part},
		{"type": "response.output_item.done", "sequence_number": 6, "output_index": 0, "item": item},
		{"type": "response.completed", "sequence_number": 7, "response": response},
	}
	var body strings.Builder
	for _, event := range events {
		data, _ := json.Marshal(event)
		if _, err := body.WriteString("event: " + event["type"].(string) + "\ndata: " + string(data) + "\n\n"); err != nil {
			t.Fatal(err)
		}
	}
	if !ValidateBasicStreamReply("openai-responses", []byte(body.String())) {
		t.Fatal("terminal native stream rejected")
	}
	truncated := body.String()
	truncated = truncated[:strings.LastIndex(truncated, "event: response.completed")]
	if ValidateBasicStreamReply("openai-responses", []byte(truncated)) {
		t.Fatal("stream without terminal event accepted")
	}
}
