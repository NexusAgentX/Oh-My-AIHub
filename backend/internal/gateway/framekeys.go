package gateway

import (
	"bytes"
	"encoding/json"
	"strings"
)

// frameKeys records which of the members the side-channel reader acts on occur
// in one JSON event. It is the cheap look that decides whether an event is
// worth decoding: in a long stream nearly every event is a few words of text
// that carry none of them, and decoding those into maps and structs was most of
// what a stream allocated.
type frameKeys uint8

const (
	// keyUsage: a "usage" member with a value other than null, at any depth.
	// Every chunk of an OpenAI Chat stream that asked for usage carries
	// "usage":null, which means there is nothing to read.
	keyUsage frameKeys = 1 << iota
	// keyUsageMetadata and keyServiceTier: likewise non-null.
	keyUsageMetadata
	keyServiceTier
	// keyReasoning: generic reasoning observations, never provider identity.
	// Null still counts so explicitly configured Bailian semantics can read it.
	keyReasoning
	// keyID: an "id" member, where an OpenAI Responses event names its response.
	keyID
	keyStop
	keyError
	// keyEnvelope marks only recognized type/status values, not ordinary deltas.
	keyEnvelope

	// nullableKeys are the keys that are ignored when their value is null.
	nullableKeys = keyUsage | keyUsageMetadata | keyServiceTier | keyStop | keyError | keyEnvelope
	// detailKeys are the keys absorbDetails reads anything under. The objects
	// it looks into on the way (response, message, choices, delta) are not
	// among them: they only lead to these.
	detailKeys = keyUsage | keyUsageMetadata | keyServiceTier | keyReasoning
)

// frameKeyNames are the member names behind the flags, lower case. They are
// matched without regard to case because the typed decoders the flags guard
// match field names that way; matching more than needed only costs a decode.
var frameKeyNames = [...]struct {
	name string
	keys frameKeys
}{
	{"id", keyID},
	{"error", keyError},
	{"type", keyEnvelope}, {"status", keyEnvelope},
	{"finish_reason", keyStop}, {"stop_reason", keyStop}, {"finishreason", keyStop},
	{"usage", keyUsage},
	{"service_tier", keyServiceTier},
	{"usagemetadata", keyUsageMetadata},
	{"reasoning_content", keyReasoning},
	{"reasoning", keyReasoning},
}

// scanFrameKeys reports which of the keys occur as a member name anywhere in the
// JSON text, nested or not, without allocating (apart from decoding a name
// that is written with escapes, which almost nothing is). A name is a string
// followed by a colon; escapes in names are resolved, so "usage" is
// "usage". It may report a key the text only has as a string value, or in
// invalid JSON, but never misses one: that is what makes skipping the decode of
// an event that reports none of them safe.
func scanFrameKeys(text []byte) frameKeys {
	var found frameKeys
	for at := 0; at < len(text); {
		open := bytes.IndexByte(text[at:], '"')
		if open < 0 {
			break
		}
		start := at + open + 1
		end, escaped, ok := stringEnd(text, start)
		if !ok {
			break
		}
		at = end + 1
		colon := at
		for colon < len(text) && isSpace(text[colon]) {
			colon++
		}
		if colon >= len(text) || text[colon] != ':' {
			continue // a string value, or the end of a broken text
		}
		keys := classifyKey(text[start:end], escaped)
		if keys == 0 || found&keys == keys {
			continue
		}
		if keys&nullableKeys != 0 {
			value := colon + 1
			for value < len(text) && isSpace(text[value]) {
				value++
			}
			if bytes.HasPrefix(text[value:], []byte("null")) {
				continue
			}
		}
		if keys == keyEnvelope {
			value := skipSpace(text, colon+1)
			if !envelopeMarkerValue(text[value:]) {
				continue
			}
		}
		found |= keys
	}
	return found
}

// stringEnd returns the index of the quote that closes the string whose first
// character is at text[start], and whether the string has an escape.
func stringEnd(text []byte, start int) (end int, escaped, ok bool) {
	for at := start; ; {
		quote := bytes.IndexByte(text[at:], '"')
		if quote < 0 {
			return 0, false, false
		}
		end = at + quote
		backslashes := 0
		for before := end - 1; before >= start && text[before] == '\\'; before-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return end, bytes.IndexByte(text[start:end], '\\') >= 0, true
		}
		at = end + 1
	}
}

// classifyKey tells which flag the member name (the text between the quotes)
// stands for.
func classifyKey(name []byte, escaped bool) frameKeys {
	if escaped {
		var decoded string
		quoted := make([]byte, 0, len(name)+2)
		quoted = append(append(append(quoted, '"'), name...), '"')
		if json.Unmarshal(quoted, &decoded) != nil {
			return 0 // not valid JSON: nothing in the text is read
		}
		name = []byte(decoded)
	}
	for _, c := range name {
		if c >= 0x80 {
			// Unicode folding can match ASCII names ("ſ" is an "s"): take the slow way.
			var keys frameKeys
			for _, known := range frameKeyNames {
				if strings.EqualFold(string(name), known.name) {
					keys |= known.keys
				}
			}
			return keys
		}
	}
	for _, known := range frameKeyNames {
		if len(name) == len(known.name) && equalLower(name, known.name) {
			return known.keys
		}
	}
	return 0
}

// equalLower reports whether name is lower, ignoring the case of ASCII letters.
func equalLower(name []byte, lower string) bool {
	for i, c := range name {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lower[i] {
			return false
		}
	}
	return true
}

// envelopeMarkerValue reads only the type/status string already located by the
// member scan. Ordinary content events need no further whole-frame searches.
func envelopeMarkerValue(raw []byte) bool {
	if len(raw) == 0 || raw[0] != '"' {
		return false
	}
	end, escaped, ok := stringEnd(raw, 1)
	if !ok {
		return false
	}
	if !escaped {
		return isEnvelopeMarker(string(raw[1:end]))
	}
	var value string
	return json.Unmarshal(raw[:end+1], &value) == nil && isEnvelopeMarker(value)
}

func isEnvelopeMarker(value string) bool {
	switch value {
	case "error", "response.failed", "failed", "response.completed", "completed", "response.incomplete", "incomplete":
		return true
	default:
		return false
	}
}
