package gateway

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

const (
	contentEvent = "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}],\"usage\":null}\n\n"
	finishEvent  = "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":null}\n\n"
	usageEvent   = "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":500,\"total_tokens\":1500}}\n\n"
	doneEvent    = "data: [DONE]\n\n"
)

// filterInPieces runs the filter over input cut into the given pieces and
// returns everything it released, the held-back rest included.
func filterInPieces(input string, cuts ...int) string {
	filter := &usageFrameFilter{}
	var out strings.Builder
	last := 0
	for _, cut := range append(cuts, len(input)) {
		cut = min(max(cut, last), len(input))
		out.Write(filter.Filter([]byte(input[last:cut])))
		last = cut
	}
	out.Write(filter.Flush())
	return out.String()
}

func TestUsageFrameFilterDropsOnlyTheUsageOnlyFrame(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"usage frame before DONE", contentEvent + finishEvent + usageEvent + doneEvent, contentEvent + finishEvent + doneEvent},
		{"CRLF boundaries", strings.ReplaceAll(contentEvent+usageEvent+doneEvent, "\n", "\r\n"), strings.ReplaceAll(contentEvent+doneEvent, "\n", "\r\n")},
		{"mixed boundaries", "data: {\"choices\":[],\"usage\":{\"a\":1}}\r\n\n" + contentEvent, contentEvent},
		{"no space after data colon", "data:{\"choices\":[],\"usage\":{\"a\":1}}\n\n" + doneEvent, doneEvent},
		{"spaced JSON", "data: { \"usage\" : { \"a\" : 1 } , \"choices\" : [ ] }\n\n" + doneEvent, doneEvent},
		{"escaped key", "data: {\"cho\\u0069ces\":[],\"usage\":{\"a\":1}}\n\n" + doneEvent, doneEvent},
		{"no usage frame at all", contentEvent + finishEvent + doneEvent, contentEvent + finishEvent + doneEvent},
		{"usage inside a frame with choices", "data: {\"choices\":[{\"delta\":{}}],\"usage\":{\"a\":1}}\n\n" + doneEvent, "data: {\"choices\":[{\"delta\":{}}],\"usage\":{\"a\":1}}\n\n" + doneEvent},
		{"empty usage object", "data: {\"choices\":[],\"usage\":{}}\n\n", "data: {\"choices\":[],\"usage\":{}}\n\n"},
		{"null usage", "data: {\"choices\":[],\"usage\":null}\n\n", "data: {\"choices\":[],\"usage\":null}\n\n"},
		{"usage not an object", "data: {\"choices\":[],\"usage\":5}\n\n", "data: {\"choices\":[],\"usage\":5}\n\n"},
		{"choices missing", "data: {\"usage\":{\"a\":1}}\n\n", "data: {\"usage\":{\"a\":1}}\n\n"},
		{"choices null", "data: {\"choices\":null,\"usage\":{\"a\":1}}\n\n", "data: {\"choices\":null,\"usage\":{\"a\":1}}\n\n"},
		{"choices not empty", "data: {\"choices\":[null],\"usage\":{\"a\":1}}\n\n", "data: {\"choices\":[null],\"usage\":{\"a\":1}}\n\n"},
		{"usage in nested object only", "data: {\"choices\":[],\"x\":{\"usage\":{\"a\":1}}}\n\n", "data: {\"choices\":[],\"x\":{\"usage\":{\"a\":1}}}\n\n"},
		{"duplicate keys: last wins", "data: {\"choices\":[{}],\"usage\":{\"a\":1},\"choices\":[]}\n\n", ""},
		{"duplicate usage: last is empty", "data: {\"choices\":[],\"usage\":{\"a\":1},\"usage\":{}}\n\n", "data: {\"choices\":[],\"usage\":{\"a\":1},\"usage\":{}}\n\n"},
		{"error frame", "data: {\"error\":{\"message\":\"x\"}}\n\n", "data: {\"error\":{\"message\":\"x\"}}\n\n"},
		{"invalid JSON", "data: {\"choices\":[],\"usage\":{\"a\":1}\n\n", "data: {\"choices\":[],\"usage\":{\"a\":1}\n\n"},
		{"trailing garbage", "data: {\"choices\":[],\"usage\":{\"a\":1}} x\n\n", "data: {\"choices\":[],\"usage\":{\"a\":1}} x\n\n"},
		{"event field present", "event: usage\ndata: {\"choices\":[],\"usage\":{\"a\":1}}\n\n", "event: usage\ndata: {\"choices\":[],\"usage\":{\"a\":1}}\n\n"},
		{"id field present", "data: {\"choices\":[],\"usage\":{\"a\":1}}\nid: 7\n\n", "data: {\"choices\":[],\"usage\":{\"a\":1}}\nid: 7\n\n"},
		{"two data lines", "data: {\"choices\":[],\ndata: \"usage\":{\"a\":1}}\n\n", "data: {\"choices\":[],\ndata: \"usage\":{\"a\":1}}\n\n"},
		{"comment keeps", ": keep-alive\n\n" + usageEvent + ": again\n\n" + doneEvent, ": keep-alive\n\n: again\n\n" + doneEvent},
		{"comment inside the event keeps it", ": note\ndata: {\"choices\":[],\"usage\":{\"a\":1}}\n\n", ": note\ndata: {\"choices\":[],\"usage\":{\"a\":1}}\n\n"},
		{"stray blank lines pass", "\n\n" + usageEvent + "\r\n" + doneEvent, "\n\n\r\n" + doneEvent},
		{"two usage frames", usageEvent + contentEvent + usageEvent, contentEvent},
		{"unterminated usage frame is released as it is", contentEvent + strings.TrimSuffix(usageEvent, "\n\n"), contentEvent + strings.TrimSuffix(usageEvent, "\n\n")},
		{"half a boundary at the end", contentEvent + "data: x\n", contentEvent + "data: x\n"},
		{"no events", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := filterInPieces(tc.in); got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
			// However the stream is cut into reads, the result is the same.
			for cut := 1; cut < len(tc.in); cut++ {
				if got := filterInPieces(tc.in, cut); got != tc.want {
					t.Fatalf("cut at %d: got %q\nwant %q", cut, got, tc.want)
				}
			}
			if got := filterInPieces(tc.in, everyByte(len(tc.in))...); got != tc.want {
				t.Fatalf("byte by byte: got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func everyByte(n int) []int {
	cuts := make([]int, 0, n)
	for i := 1; i < n; i++ {
		cuts = append(cuts, i)
	}
	return cuts
}

// An event is held back only until its blank line, so the stream is not
// delayed by more than the event that is still being written.
func TestUsageFrameFilterReleasesEveryEventAtItsBlankLine(t *testing.T) {
	filter := &usageFrameFilter{}
	if got := string(filter.Filter([]byte(contentEvent + "data: {\"choices\":[{\"delta\""))); got != contentEvent {
		t.Fatalf("first read released %q", got)
	}
	if got := string(filter.Filter([]byte(":{}}]}\n"))); got != "" {
		t.Fatalf("second read released %q", got)
	}
	if got := string(filter.Filter([]byte("\n"))); got != "data: {\"choices\":[{\"delta\":{}}]}\n\n" {
		t.Fatalf("third read released %q", got)
	}
	if got := string(filter.Filter([]byte(usageEvent))); got != "" {
		t.Fatalf("the usage frame was released: %q", got)
	}
	if got := string(filter.Filter([]byte(doneEvent))); got != doneEvent {
		t.Fatalf("[DONE] released as %q", got)
	}
}

func TestUsageFrameFilterDoesNotHoldBackAnOversizedEvent(t *testing.T) {
	long := "data: {\"choices\":[{\"delta\":{\"content\":\"" + strings.Repeat("x", 2*maxFilteredEvent) + "\"}}]}\n\n"
	filter := &usageFrameFilter{}
	input := contentEvent + long
	var released int
	for len(input) > 0 {
		n := min(4096, len(input))
		released += len(filter.Filter([]byte(input[:n])))
		input = input[n:]
	}
	// Nearly all of the long event has been passed on before its end arrives.
	if released < len(contentEvent)+maxFilteredEvent {
		t.Fatalf("only %d bytes released", released)
	}
	filter.Flush()
	// The event that follows is judged on its own again.
	if got := string(filter.Filter([]byte(usageEvent + doneEvent))); got != doneEvent {
		t.Fatalf("after the long event: %q", got)
	}
	// ... and the long event's own bytes are never judged: a huge frame that
	// looks like a usage frame is passed on.
	huge := "data: {\"choices\":[],\"usage\":{\"a\":\"" + strings.Repeat("x", 2*maxFilteredEvent) + "\"}}\n\n"
	if got := filterInPieces(huge, 5000, 70000); got != huge {
		t.Fatalf("a %d byte frame was altered", len(huge))
	}
}

// Whatever the input and however it is cut, the output is the input with
// whole usage-only events removed, judged by an independent implementation.
func FuzzUsageFrameFilter(f *testing.F) {
	for _, seed := range []string{
		contentEvent + usageEvent + doneEvent, strings.ReplaceAll(usageEvent+doneEvent, "\n", "\r\n"), ": c\n\n" + usageEvent,
		"data: {\"choices\":[],\"usage\":{\"a\":1}}\r\n\ndata: [DONE]\n\n", "\n\r\n\r\r\n\n", "data: {\"choices\":[ ],\"usage\":{ \"a\":1}}\n\n",
		"event: x\ndata: {\"choices\":[],\"usage\":{\"a\":1}}\n\n", "data:", "data: {\"choices\":[],\"usage\":{\"a\":1}}\n\n\n",
	} {
		f.Add([]byte(seed), int64(1))
	}
	f.Fuzz(func(t *testing.T, input []byte, seed int64) {
		want := referenceStrip(input)
		filter := &usageFrameFilter{}
		var got bytes.Buffer
		random := rand.New(rand.NewSource(seed))
		rest := input
		for len(rest) > 0 {
			n := 1 + random.Intn(len(rest))
			got.Write(filter.Filter(rest[:n]))
			rest = rest[n:]
		}
		got.Write(filter.Flush())
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("input %q\ngot  %q\nwant %q", input, got.Bytes(), want)
		}
		if len(want) > len(input) {
			t.Fatalf("output grew: %q -> %q", input, want)
		}
	})
}

// referenceStrip is the filter's specification written the plain way: cut the
// input into lines, close an event at each blank line, and drop the events
// that are one data line holding a JSON object with "choices": [] and a
// non-empty "usage" object.
func referenceStrip(input []byte) []byte {
	var out, event []byte
	for len(input) > 0 {
		end := bytes.IndexByte(input, '\n') + 1
		if end == 0 {
			end = len(input)
		}
		line := input[:end]
		input = input[end:]
		event = append(event, line...)
		if string(line) == "\n" || string(line) == "\r\n" {
			if !referenceDroppable(event) {
				out = append(out, event...)
			}
			event = nil
		}
	}
	return append(out, event...)
}

func referenceDroppable(event []byte) bool {
	lines := strings.SplitAfter(string(event), "\n")
	// SplitAfter leaves an empty string after the last newline.
	if len(lines) != 3 || lines[2] != "" || (lines[1] != "\n" && lines[1] != "\r\n") {
		return false
	}
	data, ok := strings.CutPrefix(strings.TrimSuffix(strings.TrimSuffix(lines[0], "\n"), "\r"), "data:")
	if !ok {
		return false
	}
	var members map[string]json.RawMessage
	if json.Unmarshal([]byte(data), &members) != nil {
		return false
	}
	var choices []json.RawMessage
	var usage map[string]json.RawMessage
	if json.Unmarshal(members["choices"], &choices) != nil || choices == nil || len(choices) != 0 {
		return false
	}
	return json.Unmarshal(members["usage"], &usage) == nil && len(usage) > 0
}
