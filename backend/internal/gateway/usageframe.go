package gateway

import (
	"bytes"
	"encoding/json"
)

// maxFilteredEvent bounds how much of one SSE event usageFrameFilter holds
// back waiting for its end. The usage-only frame it looks for is a few hundred
// bytes; an event longer than this cannot be one and is passed on as it comes.
const maxFilteredEvent = 64 << 10

// usageFrameFilter removes, from an OpenAI Chat event stream, the one frame
// the client never asked for: when the gateway turned include_usage on for a
// client that had not (ADR-0035 rule 6), the upstream ends its stream with a
// frame whose choices array is empty and which carries only usage. A client
// that takes choices[0] of every frame breaks on it.
//
// The filter is a pure byte transformation. It splits the stream into events
// at blank lines ("\n\n", "\r\n\r\n", or a mix) and drops an event only if it
// is exactly one `data:` line holding a JSON object with "choices": [] and a
// non-empty "usage" object. Every other byte, comments and [DONE] included, is
// returned unchanged and in order. An event is held back until its blank line
// arrives; whatever is left when the stream ends is released as it is.
type usageFrameFilter struct {
	pending []byte // the event in progress, not yet decided
	out     []byte // reused result buffer
	// passing is set when the event in progress outgrew maxFilteredEvent: its
	// bytes are released at once until its blank line.
	passing bool
	// lineLen counts the bytes of the line in progress (saturating at 2) and
	// lineCR says whether the first of them is '\r': a line is blank when it
	// is empty or just "\r" before its '\n'.
	lineLen int
	lineCR  bool
}

// Filter takes the next bytes of the stream and returns what may be written to
// the client now. The result is only valid until the next call.
func (f *usageFrameFilter) Filter(chunk []byte) []byte {
	f.out = f.out[:0]
	for len(chunk) > 0 {
		end := bytes.IndexByte(chunk, '\n')
		piece := chunk
		if end >= 0 {
			piece = chunk[:end+1]
		}
		chunk = chunk[len(piece):]
		f.noteLine(piece)
		if f.passing {
			f.out = append(f.out, piece...)
		} else {
			f.pending = append(f.pending, piece...)
		}
		if end >= 0 {
			blank := f.lineLen == 0 || (f.lineLen == 1 && f.lineCR)
			f.lineLen, f.lineCR = 0, false
			if blank {
				f.finishEvent()
				continue
			}
		}
		if !f.passing && len(f.pending) > maxFilteredEvent {
			f.out = append(f.out, f.pending...)
			f.pending, f.passing = f.pending[:0], true
		}
	}
	return f.out
}

// Flush returns what is still held back when the stream has ended.
func (f *usageFrameFilter) Flush() []byte {
	f.out = append(f.out[:0], f.pending...)
	f.pending, f.passing = f.pending[:0], false
	f.lineLen, f.lineCR = 0, false
	return f.out
}

// noteLine accounts piece, which is part of the line in progress and ends the
// line if it ends in '\n', for the blank-line test.
func (f *usageFrameFilter) noteLine(piece []byte) {
	if f.lineLen == 0 && len(piece) > 0 {
		f.lineCR = piece[0] == '\r'
	}
	content := len(piece)
	if content > 0 && piece[content-1] == '\n' {
		content--
	}
	f.lineLen = min(f.lineLen+content, 2)
}

// finishEvent decides the event in progress, which ends with its blank line.
func (f *usageFrameFilter) finishEvent() {
	if f.passing {
		f.passing = false
		return
	}
	if !isUsageOnlyEvent(f.pending) {
		f.out = append(f.out, f.pending...)
	}
	f.pending = f.pending[:0]
}

// isUsageOnlyEvent reports whether event (including its blank line) is the
// usage-only frame: one `data:` line and nothing else.
func isUsageOnlyEvent(event []byte) bool {
	line, rest, found := bytes.Cut(event, []byte("\n"))
	if !found || (string(rest) != "\n" && string(rest) != "\r\n") {
		return false
	}
	line = bytes.TrimSuffix(line, []byte("\r"))
	payload, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return false
	}
	return isUsageOnlyFrame(payload)
}

// isUsageOnlyFrame reports whether the JSON text is an object whose "choices"
// is an empty array and whose "usage" is a non-empty object.
func isUsageOnlyFrame(payload []byte) bool {
	// A cheap look at the top-level members first: nearly every frame of a
	// stream has choices, and is turned away here without being decoded.
	scan, err := scanObject(payload, 0)
	if err != nil {
		return false
	}
	choices, hasChoices := scan.last("choices")
	usage, hasUsage := scan.last("usage")
	if !hasChoices || !hasUsage || !isEmptyContainer(payload[choices.start:choices.end], '[', ']') {
		return false
	}
	if payload[usage.start] != '{' || isEmptyContainer(payload[usage.start:usage.end], '{', '}') {
		return false
	}
	// The exact decision is encoding/json's, on the same top-level members.
	var members map[string]json.RawMessage
	if json.Unmarshal(payload, &members) != nil {
		return false
	}
	var choiceList []json.RawMessage
	var usageMembers map[string]json.RawMessage
	if json.Unmarshal(members["choices"], &choiceList) != nil || choiceList == nil || len(choiceList) != 0 {
		return false
	}
	return json.Unmarshal(members["usage"], &usageMembers) == nil && len(usageMembers) > 0
}

// isEmptyContainer reports whether value is open, optional whitespace, close.
func isEmptyContainer(value []byte, open, close byte) bool {
	if len(value) < 2 || value[0] != open || value[len(value)-1] != close {
		return false
	}
	return len(bytes.TrimSpace(value[1:len(value)-1])) == 0
}
