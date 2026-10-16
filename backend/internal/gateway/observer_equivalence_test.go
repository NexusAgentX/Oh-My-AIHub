package gateway

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

// The side-channel reader decides what to decode by a cheap look at the member
// names of each event (scanFrameKeys), and decodes what it does decode with
// lighter code than it used to. None of that may change what it reads. The
// tests here run the reader and the frozen copy of the original
// (legacy_observer_test.go) on the same events and compare every field that
// ends up in a billing record, after every event.

var allFormats = []channel.Format{channel.FormatOpenAIChat, channel.FormatOpenAIResponses, channel.FormatAnthropic, channel.FormatGemini}

// readerState is everything the reader has learned so far.
type readerState struct {
	Usage      any
	Found      bool
	ResponseID string
	Detail     any
	Anthropic  [4]int64
	Speed      string
	Tier       string
	Qwen       bool
}

func stateOf(o *Observer) readerState {
	return readerState{Usage: o.usage, Found: o.found, ResponseID: o.responseID, Detail: o.detail,
		Anthropic: [4]int64{o.anthropic.input, o.anthropic.output, o.anthropic.cacheWrite, o.anthropic.cacheRead},
		Speed:     o.anthropicSpeed, Tier: o.anthropicTier}
}

func legacyStateOf(o *legacyObserver) readerState {
	return readerState{Usage: o.usage, Found: o.found, ResponseID: o.responseID, Detail: o.detail,
		Anthropic: [4]int64{o.anthropic.input, o.anthropic.output, o.anthropic.cacheWrite, o.anthropic.cacheRead},
		Speed:     o.anthropicSpeed, Tier: o.anthropicTier, Qwen: o.qwenObserved}
}

func metadataState(s readerState) readerState {
	d := s.Detail.(ledger.UsageDetail)
	d.Tokens = nil
	d.Notes = nil
	d.ToolPromptTokens = 0
	d.InferenceGeo = ""
	d.Units = nil
	d.Iterations = nil
	// #467 deliberately separates generic observations from Bailian modes.
	d.ThinkingMode, d.ThinkingModeBasis, d.ThinkingObserved = "", "", nil
	s.Qwen = false
	s.Usage = nil
	s.Found = false
	s.Anthropic = [4]int64{}
	s.Detail = d
	return s
}

// sameReading preserves the pre-optimization metadata contract. Usage and token
// detail semantics changed in #457/#467 and are covered independently by
// usage_compatibility_test.go and thinking_test.go, not the frozen bugs.
func sameReading(t testing.TB, format channel.Format, qwen bool, events []string) {
	t.Helper()
	current := NewObserver(format, true, "text/event-stream")
	original := legacyNewObserver(format, true, "text/event-stream")
	original.qwenObserved = qwen
	for index, event := range events {
		current.absorb([]byte(event))
		original.absorb([]byte(event))
		if got, want := metadataState(stateOf(current)), metadataState(legacyStateOf(original)); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: after event %d %q\nreads  %+v\nwas    %+v", format, index, event, got, want)
		}
	}
	got, want := current.Finish(), original.Finish()
	// Diagnostics are new; usage equivalence remains checked against the original reader.
	got.StopReason, got.UsageStatus, got.MaxFrameGapMS = "", "", nil
	got.ResponseStatus, got.Background = "", false
	got.Usage, want.Usage = ledger.Usage{}, ledger.Usage{}
	got.Found, want.Found = false, false
	got.ResponseStatus, got.Background = "", false
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: the whole answer %q\nreads  %+v\nwas    %+v", format, events, got, want)
	}
}

// eventCorpus are events of every shape the reader cares about, and many it
// must ignore, for every format: real frames, near misses, and malformed ones.
var eventCorpus = []string{
	// OpenAI Chat
	`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}],"usage":null}`,
	`{"id":"c","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
	`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`,
	`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":4,"cache_write_tokens":1,"audio_tokens":2}}}`,
	`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"prompt_tokens_details":{"text_tokens":6,"audio_tokens":4},"completion_tokens_details":{"text_tokens":15,"audio_tokens":5,"reasoning_tokens":3}}}`,
	`{"service_tier":"default","choices":[{"delta":{"content":"x"}}],"usage":null}`,
	`{"service_tier":"scale","choices":[{"delta":{"content":"x"}}]}`,
	`{"service_tier":null,"choices":[]}`,
	`{"service_tier":5,"choices":[]}`,
	`{"service_tier":"","choices":[]}`,
	`{"choices":[{"delta":{"reasoning_content":"thinking"}}]}`,
	`{"choices":[{"delta":{"reasoning_content":""}}]}`,
	`{"choices":[{"delta":{"reasoning_content":null}}]}`,
	`{"choices":[{"delta":{"content":"a"},"message":{"reasoning_content":"m"}}]}`,
	`{"choices":[{"delta":{"content":"a"}},{"delta":{"reasoning_content":"second choice"}}]}`,
	`{"choices":[null,5,"x",{"delta":null},{"delta":{"reasoning_content":"after junk"}}]}`,
	`{"choices":{"delta":{"reasoning_content":"not an array"}}}`,
	`{"choices":[{"delta":{"reasoning_content":"a","reasoning_content":""}}]}`,
	`{"choices":[{"delta":{"content":"say \"usage\": {\"x\":1} please"}}],"usage":null}`,
	`{"choices":[{"delta":{"content":"usage"}}]}`,
	`{"choices":[{"delta":{"content":"\u4f60\u597d\n"}}],"usage":null}`,
	`{"choices":[],"usag\u0065":{"prompt_tokens":7,"completion_tokens":3}}`,
	`{"choices":[],"Usage":{"prompt_tokens":7,"completion_tokens":3}}`,
	`{"choices":[],"USAGE":{"prompt_tokens":7,"completion_tokens":3}}`,
	`{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3},"usage":null}`,
	`{"choices":[],"usage":null,"usage":{"prompt_tokens":8,"completion_tokens":4}}`,
	`{"choices":[],"usage":null,"Usage":{"prompt_tokens":9,"completion_tokens":5}}`,
	`{"choices":[],"usage":null,"u\u017fage":{"prompt_tokens":9,"completion_tokens":5}}`,
	"{\"choices\":[],\"usage\":null,\"u\u017fage\":{\"prompt_tokens\":9,\"completion_tokens\":5}}",
	`{"usage":null,"usageMetadata":{"promptTokenCount":1}}`,
	`{"usageMetadata":null,"u\u017fageMetadata":{"promptTokenCount":9,"candidatesTokenCount":5}}`,
	`{"choices":[],"usage" : {"prompt_tokens":11,"completion_tokens":6}}`,
	`{"choices":[],"usage"  :  null  }`,
	`{"nested":{"usage":{"prompt_tokens":1,"completion_tokens":1}},"choices":[]}`,
	`{"usage":{"prompt_tokens":"12","completion_tokens":1}}`,
	`{"usage":{"prompt_tokens":1.5,"completion_tokens":1}}`,
	`{"usage":{"prompt_tokens":-5,"completion_tokens":-1}}`,
	`{"usage":[]}`,
	`{"usage":{}}`,
	`{"usage":"none"}`,
	`{"service_tier":"flex","usage":{"service_tier":"priority","prompt_tokens":3,"completion_tokens":2}}`,
	`{"response":{"service_tier":"flex","usage":{"prompt_tokens":3,"completion_tokens":2}}}`,
	`{"response":null,"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
	`{"message":{"usage":{"prompt_tokens":3,"completion_tokens":2}}}`,
	`{"message":null,"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
	// OpenAI Responses
	`{"type":"response.output_text.delta","sequence_number":4,"item_id":"msg_1","output_index":0,"delta":"word "}`,
	`{"type":"response.created","response":{"id":"resp_1","status":"in_progress","usage":null}}`,
	`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":50,"output_tokens":9,"input_tokens_details":{"cached_tokens":10},"output_tokens_details":{"reasoning_tokens":2}}}}`,
	`{"type":"response.completed","response":{"id":"resp_1","service_tier":"priority","usage":{"input_tokens":5,"output_tokens":9,"input_tokens_details":{"text_tokens":2,"image_tokens":3},"output_tokens_details":{"text_tokens":9}}}}`,
	`{"id":"resp_2","object":"response","usage":{"input_tokens":4,"output_tokens":1}}`,
	`{"id":"resp_3","type":"x","usage":{"input_tokens":4,"output_tokens":1}}`,
	`{"type":"response.output_item.added","item":{"id":"msg_9","type":"message"}}`,
	`{"response":{"id":"resp_4"},"id":"outer"}`,
	`{"response":{"id":"resp_5","usage":{"input_tokens":1,"output_tokens":2}},"usage":{"input_tokens":3,"output_tokens":4}}`,
	`{"Response":{"id":"resp_6","usage":{"input_tokens":1,"output_tokens":2}}}`,
	`{"respons\u0065":{"id":"resp_7","usage":{"input_tokens":1,"output_tokens":2}}}`,
	`{"I\u0044":"resp_8","usage":{"input_tokens":1,"output_tokens":2}}`,
	// Anthropic
	`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"word "}}`,
	`{"type":"message_start","message":{"id":"m","usage":{"input_tokens":12,"cache_creation_input_tokens":3,"cache_read_input_tokens":4,"output_tokens":1}}}`,
	`{"type":"message_start","message":{"id":"m","usage":{"input_tokens":12,"cache_creation_input_tokens":3,"cache_creation":{"ephemeral_5m_input_tokens":1,"ephemeral_1h_input_tokens":2},"service_tier":"standard"}}}`,
	`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42}}`,
	`{"type":"message_delta","delta":{},"usage":{"output_tokens":42,"speed":"fast"}}`,
	`{"type":"message_delta","delta":{},"usage":{"output_tokens":43,"speed":"standard"}}`,
	`{"type":"message_delta","usage":{"output_tokens":44,"speed":null}}`,
	`{"type":"message_delta","service_tier":"priority","usage":{"output_tokens":45}}`,
	`{"type":"message_delta","usage":{"cache_creation":null}}`,
	`{"type":"message_delta","usage":{"cache_creation":{"ephemeral_5m_input_tokens":1}}}`,
	`{"type":"message_delta","usage":{"cache_creation_input_tokens":50,"output_tokens":2}}`,
	`{"type":"ping"}`,
	`{"type":"message_stop"}`,
	`{"type":"message","usage":{"input_tokens":2,"output_tokens":3}}`,
	`{"message":{"usage":{"input_tokens":5},"service_tier":"batch"},"usage":{"output_tokens":6}}`,
	// Gemini
	`{"candidates":[{"content":{"parts":[{"text":"word "}],"role":"model"},"index":0}],"modelVersion":"x"}`,
	`{"candidates":[],"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":3,"totalTokenCount":53},"modelVersion":"x"}`,
	`{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":40,"candidatesTokenCount":20,"thoughtsTokenCount":10}}`,
	`{"usageMetadata":{"serviceTier":"STANDARD","promptTokenCount":100,"cachedContentTokenCount":40,"candidatesTokenCount":20,"thoughtsTokenCount":10,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":70},{"modality":"TEXT","tokenCount":30}],"cacheTokensDetails":[{"modality":"AUDIO","tokenCount":40}],"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":20}]}}`,
	`{"usageMetadata":{"promptTokenCount":10,"toolUsePromptTokenCount":4,"candidatesTokenCount":2}}`,
	`{"usageMetadata":{"promptTokenCount":10,"toolUsePromptTokenCount":0}}`,
	`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":10,"candidatesTokensDetails":[{"modality":"AUDIO"}]}}`,
	`{"usageMetadata":null,"candidates":[]}`,
	`{"usageMetadata":{}}`,
	`{"usagemetadata":{"promptTokenCount":10,"candidatesTokenCount":3}}`,
	`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3},"usageMetadata":null}`,
	`{"usageMetadata":{"promptTokenCount":"10"}}`,
	`{"usageMetadata":{"promptTokenCount":20,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":20}]}}`,
	`{"usageMetadata":{"promptTokenCount":20,"cachedContentTokenCount":5,"cacheTokensDetails":[{"modality":"TEXT","tokenCount":5}],"promptTokensDetails":[{"modality":"TEXT","tokenCount":20}]}}`,
	// malformed or odd
	``, `{`, `}`, `{"usage":`, `{"usage":{"prompt_tokens":1`, `{"usage":nul}`, `{"a":tru,"usage":{"prompt_tokens":1}}`,
	`{"usage":{"prompt_tokens":1}} trailing`, `[]`, `null`, `5`, `"usage"`, `{"a":"unterminated`, `{"a\":1}`,
	`{"usage":{"prompt_tokens":1,"completion_tokens":2},"a":"\ud800"}`, "{\"usage\":{\"prompt_tokens\":1}, \"k\\\\\":2}",
	`{"service_tier":"x","a":tru}`, `{"choices":[{"delta":{"reasoning_content":"x"}}],"b":01}`,
}

func TestObserverMatchesTheOriginalOnEveryCorpusEvent(t *testing.T) {
	for _, format := range allFormats {
		for _, qwen := range []bool{false, true} {
			// Each event on its own, and then every event in a row: state carries
			// over from event to event, and the carry-over is part of the rules.
			for _, event := range eventCorpus {
				sameReading(t, format, qwen, []string{event})
			}
			sameReading(t, format, qwen, eventCorpus)
			reversed := make([]string, len(eventCorpus))
			for i, event := range eventCorpus {
				reversed[len(eventCorpus)-1-i] = event
			}
			sameReading(t, format, qwen, reversed)
		}
	}
}

// ---- generated events ----

// keyVocabulary are member names the reader looks at, near misses, and noise.
var keyVocabulary = []string{
	"usage", "usageMetadata", "service_tier", "serviceTier", "response", "message", "choices", "delta", "reasoning_content",
	"id", "type", "prompt_tokens", "completion_tokens", "prompt_tokens_details", "completion_tokens_details", "cached_tokens",
	"cache_write_tokens", "audio_tokens", "text_tokens", "image_tokens", "input_tokens", "output_tokens", "input_tokens_details",
	"output_tokens_details", "cache_creation_input_tokens", "cache_read_input_tokens", "cache_creation", "ephemeral_5m_input_tokens",
	"ephemeral_1h_input_tokens", "speed", "promptTokenCount", "candidatesTokenCount", "thoughtsTokenCount", "cachedContentTokenCount",
	"promptTokensDetails", "cacheTokensDetails", "candidatesTokensDetails", "modality", "tokenCount", "toolUsePromptTokenCount",
	"content", "text", "model", "item", "item_id", "usages", "xusage", "ID", "Usage", "RESPONSE", "Message",
}

var stringVocabulary = []string{"", "fast", "standard", "priority", "default", "flex", "scale", "usage", "AUDIO", "TEXT", "IMAGE", "VIDEO", "resp_1", "word ", "a\"b", "\u00e9", "reasoning",
	`\u0066ast`, `fa\\st`, `priority\n`, "caf\u00e9", "\xff\xfe", `\ud83d\ude00`, `\ud800`}

type eventGenerator struct{ random *rand.Rand }

// key writes a member name, sometimes in another case and sometimes with some
// letters written as \u escapes.
func (g eventGenerator) key() string {
	name := keyVocabulary[g.random.Intn(len(keyVocabulary))]
	switch g.random.Intn(10) {
	case 0:
		name = strings.ToUpper(name)
	case 1:
		name = strings.ToLower(name)
	}
	if g.random.Intn(8) == 0 {
		var b strings.Builder
		for _, r := range name {
			if g.random.Intn(3) == 0 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
		name = b.String()
	}
	return `"` + name + `"`
}

func (g eventGenerator) scalar() string {
	switch g.random.Intn(9) {
	case 0:
		return "null"
	case 1:
		return "true"
	case 2:
		return "false"
	case 3:
		return fmt.Sprint(g.random.Intn(200) - 10)
	case 4:
		return fmt.Sprintf("%d.%d", g.random.Intn(5), g.random.Intn(10))
	case 5:
		return fmt.Sprintf("%de%d", g.random.Intn(5), g.random.Intn(3))
	case 6:
		return `"` + strings.ReplaceAll(stringVocabulary[g.random.Intn(len(stringVocabulary))], `"`, `\"`) + `"`
	case 7:
		return []string{fmt.Sprint(g.random.Int63n(1 << 40)), "9223372036854775807", "9223372036854775808", "-9223372036854775809", "-0", "1E2", "0.0", "12345678901234567890"}[g.random.Intn(8)]
	default:
		return fmt.Sprint(g.random.Intn(100))
	}
}

func (g eventGenerator) space() string {
	if g.random.Intn(6) == 0 {
		return []string{" ", "\t", "\n", "  "}[g.random.Intn(4)]
	}
	return ""
}

// value writes a JSON value of at most the given depth; members come from the
// vocabulary so that the shapes the reader looks for turn up often.
func (g eventGenerator) value(depth int) string {
	if depth <= 0 {
		return g.scalar()
	}
	switch g.random.Intn(6) {
	case 0, 1:
		return g.scalar()
	case 2:
		n := g.random.Intn(4)
		items := make([]string, n)
		for i := range items {
			items[i] = g.value(depth - 1)
		}
		return "[" + strings.Join(items, ",") + "]"
	default:
		return g.object(depth - 1)
	}
}

func (g eventGenerator) object(depth int) string {
	n := g.random.Intn(6)
	members := make([]string, n)
	for i := range members {
		members[i] = g.space() + g.key() + g.space() + ":" + g.space() + g.value(depth)
	}
	return "{" + strings.Join(members, ",") + g.space() + "}"
}

// event is an object like those the formats send, with the interesting members
// put in shapes the reader understands about half of the time.
func (g eventGenerator) event() string {
	switch g.random.Intn(6) {
	case 0:
		return g.object(4)
	case 1:
		// Choices as thinking models send them, among junk.
		field := g.key()
		if g.random.Intn(2) == 0 {
			field = []string{`"reasoning_content"`, `"reasoning\u005fcontent"`, `"Reasoning_Content"`}[g.random.Intn(3)]
		}
		part := []string{"delta", "message", "delta", g.key()}[g.random.Intn(4)]
		choice := `{"index":0,"` + part + `":{` + field + `:` + g.value(1) + `}}`
		choices := []string{choice, g.value(1), choice, `null`, g.object(2)}
		g.random.Shuffle(len(choices), func(i, j int) { choices[i], choices[j] = choices[j], choices[i] })
		return `{"choices":[` + strings.Join(choices[:1+g.random.Intn(len(choices))], ",") + `],` + g.key() + `:` + g.value(3) + `}`
	case 2:
		return `{"usage":{"prompt_tokens":` + fmt.Sprint(g.random.Intn(100)) + `,"completion_tokens":` + fmt.Sprint(g.random.Intn(100)) + `,` + g.key() + `:` + g.value(2) + `,` + g.key() + `:` + g.value(2) + `},` + g.key() + `:` + g.value(2) + `}`
	case 3:
		return `{"usageMetadata":{"promptTokenCount":` + fmt.Sprint(g.random.Intn(100)) + `,"candidatesTokenCount":` + fmt.Sprint(g.random.Intn(100)) + `,` + g.key() + `:` + g.value(2) + `,` + g.key() + `:` + g.value(2) + `}}`
	case 4:
		return `{"response":{"id":"r","usage":{"input_tokens":` + fmt.Sprint(g.random.Intn(100)) + `,"output_tokens":3,` + g.key() + `:` + g.value(2) + `},` + g.key() + `:` + g.value(2) + `}}`
	default:
		return `{"message":{"usage":{"input_tokens":` + fmt.Sprint(g.random.Intn(100)) + `,` + g.key() + `:` + g.value(2) + `}},"usage":{` + g.key() + `:` + g.value(2) + `}}`
	}
}

func TestObserverMatchesTheOriginalOnGeneratedEvents(t *testing.T) {
	rounds := 3000
	if testing.Short() {
		rounds = 300
	}
	for _, format := range allFormats {
		random := rand.New(rand.NewSource(int64(len(format))*1000003 + 17))
		g := eventGenerator{random: random}
		for round := 0; round < rounds; round++ {
			events := make([]string, 1+random.Intn(5))
			for i := range events {
				events[i] = g.event()
			}
			sameReading(t, format, random.Intn(4) == 0, events)
		}
	}
}

// FuzzObserverMatchesTheOriginal feeds arbitrary bytes, one event per line, to
// both readers in every format.
func FuzzObserverMatchesTheOriginal(f *testing.F) {
	for _, event := range eventCorpus {
		f.Add([]byte(event))
	}
	f.Add([]byte(eventCorpus[2] + "\n" + eventCorpus[0] + "\n" + eventCorpus[10]))
	f.Fuzz(func(t *testing.T, data []byte) {
		events := strings.Split(string(data), "\n")
		for _, format := range allFormats {
			sameReading(t, format, false, events)
		}
	})
}

// ---- through the line reader ----

// TestObserverMatchesTheOriginalOnFramedStreams runs whole streams (event
// framing, chunk boundaries, the interval statistics) through both readers.
func TestObserverMatchesTheOriginalOnFramedStreams(t *testing.T) {
	for _, format := range allFormats {
		for _, name := range benchStreamOrder {
			stream := benchStreams[name]
			if stream.format != format {
				continue
			}
			stream = stream.render(t, 40)
			for _, chunk := range []int{1, 7, 64, 1 << 20} {
				current := NewObserver(format, true, "text/event-stream")
				original := legacyNewObserver(format, true, "text/event-stream")
				at := time.Unix(0, 0)
				for _, frame := range stream.frames {
					for rest := frame; len(rest) > 0; {
						n := min(chunk, len(rest))
						current.Write(rest[:n], at)
						original.Write(rest[:n], at)
						rest = rest[n:]
					}
					at = at.Add(3 * time.Millisecond)
				}
				got, want := current.Finish(), original.Finish()
				got.StopReason, got.UsageStatus, got.MaxFrameGapMS = "", "", nil
				got.ResponseStatus, got.Background = "", false
				if !reflect.DeepEqual(got, want) || !got.Found {
					t.Fatalf("%s chunk %d:\nreads %+v\nwas   %+v", format, chunk, got, want)
				}
			}
		}
	}
}
