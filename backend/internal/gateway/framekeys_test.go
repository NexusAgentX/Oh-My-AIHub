package gateway

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestScanFrameKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want frameKeys
	}{
		{"plain chunk with null usage", `{"id":"c","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}],"usage":null}`, keyID},
		{"normal type", `{"type":"response.output_text.delta"}`, 0},
		{"failed type", `{"type":"response.failed"}`, keyEnvelope},
		{"error type", `{"type":"error"}`, keyEnvelope},
		{"completed status", `{"status":"completed"}`, keyEnvelope},
		{"incomplete type", `{"type":"response.incomplete"}`, keyEnvelope},
		{"escaped type value", `{"type":"response.fa\u0069led"}`, keyEnvelope},
		{"escaped type key", `{"t\u0079pe":"error"}`, keyEnvelope},
		{"case folded type key", `{"TYPE":"error"}`, keyEnvelope},
		{"null marker", `{"type":null,"status":null}`, 0},
		{"nonstring marker", `{"type":42,"status":false}`, 0},
		{"object marker", `{"type":{},"status":[]}`, 0},
		{"nested marker", `{"item":{"status":"failed"}}`, keyEnvelope},
		{"tool failure type", `{"type":"response.mcp_call.failed"}`, 0},
		{"error member", `{"error":{}}`, keyError},
		{"null error", `{"error":null}`, 0},
		{"usage object", `{"choices":[],"usage":{"prompt_tokens":1}}`, keyUsage},
		{"usage after spaces", `{"usage"  :  {"a":1}}`, keyUsage},
		{"null usage after spaces", `{"usage"  :  null  }`, 0},
		{"null usage then object", `{"usage":null,"usage":{"a":1}}`, keyUsage},
		{"usageMetadata", `{"usageMetadata":{"promptTokenCount":1}}`, keyUsageMetadata},
		{"null usageMetadata", `{"usageMetadata":null}`, 0},
		{"service tier", `{"service_tier":"default"}`, keyServiceTier},
		{"null service tier", `{"service_tier":null}`, 0},
		{"reasoning is kept even when null", `{"choices":[{"delta":{"reasoning_content":null}}]}`, keyReasoning},
		{"reasoning with text", `{"choices":[{"delta":{"reasoning_content":"hm"}}]}`, keyReasoning},
		{"nested at any depth", `{"a":{"b":[{"usage":{"c":1}}]}}`, keyUsage},
		{"id nested", `{"item":{"id":"x"}}`, keyID},
		{"several", `{"id":"r","usage":{"a":1},"service_tier":"x","usageMetadata":{"a":1},"reasoning_content":""}`, keyID | keyUsage | keyServiceTier | keyUsageMetadata | keyReasoning},
		{"names as values are keys of nothing", `{"text":"usage","other":"service_tier","k":["usageMetadata","id"]}`, 0},
		{"names inside text", `{"text":"he said \"usage\": {\"a\":1} and \"id\":1"}`, 0},
		{"backslash before closing quote", `{"text":"a\\","usage":{"a":1}}`, keyUsage},
		{"even backslashes then quote", `{"text":"a\\\\","usage":{"a":1}}`, keyUsage},
		{"odd backslashes keep the string open", `{"text":"a\\\"usage\":1"}`, 0},
		{"escaped letter in name", `{"usage":{"a":1}}`, keyUsage},
		{"escaped name for the underscore", `{"service_tier":"x"}`, keyServiceTier},
		{"escape of a solidus-like char", `{"u\/sage":1}`, 0},
		{"upper case", `{"USAGE":{"a":1},"Service_Tier":"x"}`, keyUsage | keyServiceTier},
		{"mixed case metadata", `{"usageMETADATA":{"a":1}}`, keyUsageMetadata},
		{"long s", "{\"uſage\":{\"a\":1}}", keyUsage},
		{"long s escaped", `{"uſage":{"a":1}}`, keyUsage},
		{"kelvin sign is no letter of any name", "{\"uKage\":{\"a\":1}}", 0},
		{"near misses", `{"usages":1,"xusage":1,"usag":1,"idx":1,"identifier":1,"service-tier":1,"message":1,"response":1}`, 0},
		{"unicode name", `{"日本":1,"usage":{"a":1}}`, keyUsage},
		{"whitespace before colon after escaped key", `{"usage" :null}`, 0},
		{"top level string is no key", `"usage"`, 0},
		{"unterminated string", `{"usage":{"a":1},"b":"never ends`, keyUsage},
		{"unterminated key", `{"usage`, 0},
		{"empty", ``, 0},
		{"just quotes", `""`, 0},
		{"colon without key", `{:}`, 0},
		{"bad escape in name is not read", `{"u\xage":1}`, 0},
		{"array of events", `[{"usage":{"a":1}},{"id":"x"}]`, keyUsage | keyID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := scanFrameKeys([]byte(tc.text)); got != tc.want {
				t.Fatalf("scanFrameKeys(%q) = %06b, want %06b", tc.text, got, tc.want)
			}
		})
	}
}

// keysByDecoding is the reference: every member name at any depth, read with
// encoding/json's own tokenizer, compared the way encoding/json compares
// field names. A text it cannot read is not a text the reader acts on.
func keysByDecoding(text []byte) (frameKeys, bool) {
	decoder := json.NewDecoder(strings.NewReader(string(text)))
	var found frameKeys
	type frame struct{ object, expectKey bool }
	var stack []frame
	var pendingKey frameKeys
	var pendingNullable bool
	valueDone := func() {
		if len(stack) > 0 && stack[len(stack)-1].object {
			stack[len(stack)-1].expectKey = true
		}
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return found, true
		}
		if err != nil {
			return 0, false
		}
		inKey := len(stack) > 0 && stack[len(stack)-1].object && stack[len(stack)-1].expectKey
		switch value := token.(type) {
		case json.Delim:
			switch value {
			case '{', '[':
				if pendingKey != 0 && !pendingNullable {
					found |= pendingKey &^ keyEnvelope
				}
				pendingKey = 0
				stack = append(stack, frame{object: value == '{', expectKey: value == '{'})
			default:
				stack = stack[:len(stack)-1]
				valueDone()
			}
		case string:
			if inKey {
				pendingKey, pendingNullable = 0, false
				for _, known := range frameKeyNames {
					if strings.EqualFold(value, known.name) {
						pendingKey |= known.keys
					}
				}
				pendingNullable = pendingKey&nullableKeys != 0
				stack[len(stack)-1].expectKey = false
				continue
			}
			if pendingKey != 0 {
				found |= pendingKey &^ keyEnvelope
				if pendingKey&keyEnvelope != 0 && isEnvelopeMarker(value) {
					found |= keyEnvelope
				}
				pendingKey = 0
			}
			valueDone()
		default:
			if pendingKey != 0 && !(value == nil && pendingNullable) {
				found |= pendingKey &^ keyEnvelope
			}
			pendingKey = 0
			valueDone()
		}
	}
}

// The look at the names must never miss one: a frame it passes over unread
// would lose the usage in it.
func checkFrameKeysMissNothing(t testing.TB, text []byte) {
	t.Helper()
	got := scanFrameKeys(text)
	if want, readable := keysByDecoding(text); readable && got&want != want {
		t.Fatalf("scanFrameKeys(%q) = %06b, misses %06b", text, got, want&^got)
	}
}

func TestScanFrameKeysMissesNothingInTheCorpus(t *testing.T) {
	for _, event := range eventCorpus {
		checkFrameKeysMissNothing(t, []byte(event))
	}
	for _, stream := range benchStreams {
		for _, frame := range stream.render(t, 5).frames {
			checkFrameKeysMissNothing(t, frame)
		}
	}
}

func FuzzScanFrameKeys(f *testing.F) {
	for _, event := range eventCorpus {
		f.Add([]byte(event))
	}
	f.Fuzz(func(t *testing.T, text []byte) { checkFrameKeysMissNothing(t, text) })
}

func TestScanFrameKeysDoesNotAllocateOnATypicalFrame(t *testing.T) {
	frame := []byte(`{"id":"chatcmpl-perf","object":"chat.completion.chunk","created":1700000000,"model":"perf","choices":[{"index":0,"delta":{"content":"word "},"finish_reason":null}],"usage":null}`)
	if allocated := testing.AllocsPerRun(100, func() { scanFrameKeys(frame) }); allocated != 0 {
		t.Fatalf("%.0f allocations", allocated)
	}
}
