package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
)

// The gateway never re-serializes a request body. It scans the JSON text of
// the top-level object, locates the byte ranges of the few values it needs
// (model, stream, stream_options, previous_response_id) and splices
// replacements into the original bytes, so every other byte reaches the
// upstream exactly as the client sent it.

var errMalformedJSON = errors.New("malformed JSON object")

// entry is one member of a JSON object: the decoded key and the byte range of
// its value within the scanned text.
type entry struct {
	key        string
	start, end int // value range [start, end)
}

type objectScan struct {
	entries []entry
	// open is the index of '{', close the index of the matching '}', and
	// lastEnd the end of the last value (or open+1 for an empty object).
	open, close, lastEnd int
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

func skipSpace(text []byte, i int) int {
	for i < len(text) && isSpace(text[i]) {
		i++
	}
	return i
}

// skipString returns the index after the string that starts at text[i]=='"'.
func skipString(text []byte, i int) (int, error) {
	i++
	for i < len(text) {
		switch text[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1, nil
		default:
			i++
		}
	}
	return 0, errMalformedJSON
}

// skipValue returns the index after the JSON value starting at text[i].
func skipValue(text []byte, i int) (int, error) {
	if i >= len(text) {
		return 0, errMalformedJSON
	}
	switch text[i] {
	case '"':
		return skipString(text, i)
	case '{', '[':
		depth := 0
		for i < len(text) {
			switch text[i] {
			case '"':
				next, err := skipString(text, i)
				if err != nil {
					return 0, err
				}
				i = next
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1, nil
				}
			}
			i++
		}
		return 0, errMalformedJSON
	}
	start := i
	for i < len(text) && !isSpace(text[i]) && text[i] != ',' && text[i] != '}' && text[i] != ']' {
		i++
	}
	if i == start {
		return 0, errMalformedJSON
	}
	return i, nil
}

func decodeKey(raw []byte) (string, bool) {
	if bytes.IndexByte(raw, '\\') < 0 {
		return string(raw[1 : len(raw)-1]), true
	}
	var key string
	if json.Unmarshal(raw, &key) != nil {
		return "", false
	}
	return key, true
}

// scanObject indexes the members of the JSON object starting at text[start]
// (after optional whitespace).
func scanObject(text []byte, start int) (objectScan, error) {
	i := skipSpace(text, start)
	if i >= len(text) || text[i] != '{' {
		return objectScan{}, errMalformedJSON
	}
	scan := objectScan{open: i}
	i = skipSpace(text, i+1)
	scan.lastEnd = scan.open + 1
	if i < len(text) && text[i] == '}' {
		scan.close = i
		return scan, nil
	}
	for {
		if i >= len(text) || text[i] != '"' {
			return objectScan{}, errMalformedJSON
		}
		keyEnd, err := skipString(text, i)
		if err != nil {
			return objectScan{}, err
		}
		key, ok := decodeKey(text[i:keyEnd])
		if !ok {
			return objectScan{}, errMalformedJSON
		}
		i = skipSpace(text, keyEnd)
		if i >= len(text) || text[i] != ':' {
			return objectScan{}, errMalformedJSON
		}
		valueStart := skipSpace(text, i+1)
		valueEnd, err := skipValue(text, valueStart)
		if err != nil {
			return objectScan{}, err
		}
		scan.entries = append(scan.entries, entry{key: key, start: valueStart, end: valueEnd})
		scan.lastEnd = valueEnd
		i = skipSpace(text, valueEnd)
		if i >= len(text) {
			return objectScan{}, errMalformedJSON
		}
		switch text[i] {
		case ',':
			i = skipSpace(text, i+1)
		case '}':
			scan.close = i
			return scan, nil
		default:
			return objectScan{}, errMalformedJSON
		}
	}
}

// last returns the final member with the given key, matching how common JSON
// decoders resolve duplicate keys.
func (s objectScan) last(key string) (entry, bool) {
	for index := len(s.entries) - 1; index >= 0; index-- {
		if s.entries[index].key == key {
			return s.entries[index], true
		}
	}
	return entry{}, false
}

func (s objectScan) all(key string) []entry {
	var matches []entry
	for _, candidate := range s.entries {
		if candidate.key == key {
			matches = append(matches, candidate)
		}
	}
	return matches
}

// RequestFacts are the top-level values of a request body the gateway reads.
type RequestFacts struct {
	Model              string
	HasModel           bool
	Stream             bool
	PreviousResponseID string
}

// ScanRequest reads the facts and returns the scan for later splicing. Only the
// top-level object is inspected; the body is not validated against any schema.
func ScanRequest(body []byte) (RequestFacts, objectScan, error) {
	scan, err := scanObject(body, 0)
	if err != nil {
		return RequestFacts{}, objectScan{}, err
	}
	var facts RequestFacts
	if member, ok := scan.last("model"); ok {
		var model string
		if body[member.start] == '"' && json.Unmarshal(body[member.start:member.end], &model) == nil {
			facts.Model, facts.HasModel = model, true
		}
	}
	if member, ok := scan.last("stream"); ok {
		facts.Stream = string(body[member.start:member.end]) == "true"
	}
	if member, ok := scan.last("previous_response_id"); ok {
		var id string
		if body[member.start] == '"' && json.Unmarshal(body[member.start:member.end], &id) == nil {
			facts.PreviousResponseID = id
		}
	}
	return facts, scan, nil
}

func encodeString(value string) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return bytes.TrimRight(buffer.Bytes(), "\n")
}

type splice struct {
	start, end int
	value      []byte
}

// applySplices rebuilds body with the (non-overlapping) ranges replaced.
func applySplices(body []byte, splices []splice) []byte {
	if len(splices) == 0 {
		return body
	}
	for i := 1; i < len(splices); i++ {
		for j := i; j > 0 && splices[j].start < splices[j-1].start; j-- {
			splices[j], splices[j-1] = splices[j-1], splices[j]
		}
	}
	out := make([]byte, 0, len(body)+64)
	cursor := 0
	for _, item := range splices {
		out = append(out, body[cursor:item.start]...)
		out = append(out, item.value...)
		cursor = item.end
	}
	return append(out, body[cursor:]...)
}

// ReplaceModel returns body with the value of every top-level "model" member
// set to model. All other bytes are untouched.
func ReplaceModel(body []byte, scan objectScan, model string) []byte {
	encoded := encodeString(model)
	var splices []splice
	for _, member := range scan.all("model") {
		splices = append(splices, splice{start: member.start, end: member.end, value: encoded})
	}
	return applySplices(body, splices)
}

// EnsureIncludeUsage makes an OpenAI Chat streaming request report usage by
// setting stream_options.include_usage to true, again by splicing bytes.
func EnsureIncludeUsage(body []byte, scan objectScan) []byte {
	const flag = `"include_usage":true`
	member, ok := scan.last("stream_options")
	if !ok {
		insertion := `,"stream_options":{` + flag + `}`
		if len(scan.entries) == 0 {
			insertion = `"stream_options":{` + flag + `}`
		}
		return applySplices(body, []splice{{start: scan.lastEnd, end: scan.lastEnd, value: []byte(insertion)}})
	}
	inner, err := scanObject(body, member.start)
	if err != nil || inner.open != member.start {
		// null or a non-object value: replace the value wholesale.
		return applySplices(body, []splice{{start: member.start, end: member.end, value: []byte(`{` + flag + `}`)}})
	}
	if existing, found := inner.last("include_usage"); found {
		if string(body[existing.start:existing.end]) == "true" {
			return body
		}
		var splices []splice
		for _, duplicate := range inner.all("include_usage") {
			splices = append(splices, splice{start: duplicate.start, end: duplicate.end, value: []byte("true")})
		}
		return applySplices(body, splices)
	}
	insertion := flag
	if len(inner.entries) > 0 {
		insertion += ","
	}
	return applySplices(body, []splice{{start: inner.open + 1, end: inner.open + 1, value: []byte(insertion)}})
}
