package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// The gateway never re-serializes a request body. It scans the JSON text of
// the top-level object, locates the byte ranges of the few values it needs
// (model, stream, stream_options) and splices replacements into the original
// bytes, so every other byte reaches the upstream exactly as the client sent
// it.

var errMalformedJSON = errors.New("malformed JSON object")

// entry is one member of a JSON object: the decoded key and the byte ranges of
// the key and of its value within the scanned text.
type entry struct {
	key        string
	keyStart   int
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
// It looks for the closing quote and for escapes with IndexByte, which moves
// over the long text of a prompt at memory speed.
func skipString(text []byte, i int) (int, error) {
	i++
	for i < len(text) {
		quote := bytes.IndexByte(text[i:], '"')
		if quote < 0 {
			return 0, errMalformedJSON
		}
		end := i + quote
		escape := bytes.IndexByte(text[i:end], '\\')
		if escape < 0 {
			return end + 1, nil
		}
		i += escape + 2 // the backslash and the character it escapes
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

// objectBounds locates an object within the scanned text.
type objectBounds struct {
	// open is the index of '{', close the index of the matching '}', and
	// lastEnd the end of the last value (or open+1 for an empty object).
	open, close, lastEnd int
}

// walkObject calls visit for each member of the JSON object starting at
// text[start] (after optional whitespace), in order, with the member's decoded
// key, the index of the opening quote of its key and the byte range
// [valueStart, valueEnd) of its value. It allocates nothing for keys without
// escapes, whose bytes are text itself: visit must not keep key beyond its
// call, and must not change text.
func walkObject(text []byte, start int, visit func(key []byte, keyStart, valueStart, valueEnd int)) (objectBounds, error) {
	i := skipSpace(text, start)
	if i >= len(text) || text[i] != '{' {
		return objectBounds{}, errMalformedJSON
	}
	bounds := objectBounds{open: i}
	i = skipSpace(text, i+1)
	bounds.lastEnd = bounds.open + 1
	if i < len(text) && text[i] == '}' {
		bounds.close = i
		return bounds, nil
	}
	for {
		if i >= len(text) || text[i] != '"' {
			return objectBounds{}, errMalformedJSON
		}
		keyStart := i
		keyEnd, err := skipString(text, i)
		if err != nil {
			return objectBounds{}, err
		}
		key := text[i+1 : keyEnd-1]
		if bytes.IndexByte(key, '\\') >= 0 {
			decoded, ok := decodeKey(text[i:keyEnd])
			if !ok {
				return objectBounds{}, errMalformedJSON
			}
			key = []byte(decoded)
		}
		i = skipSpace(text, keyEnd)
		if i >= len(text) || text[i] != ':' {
			return objectBounds{}, errMalformedJSON
		}
		valueStart := skipSpace(text, i+1)
		valueEnd, err := skipValue(text, valueStart)
		if err != nil {
			return objectBounds{}, err
		}
		visit(key, keyStart, valueStart, valueEnd)
		bounds.lastEnd = valueEnd
		i = skipSpace(text, valueEnd)
		if i >= len(text) {
			return objectBounds{}, errMalformedJSON
		}
		switch text[i] {
		case ',':
			i = skipSpace(text, i+1)
		case '}':
			bounds.close = i
			return bounds, nil
		default:
			return objectBounds{}, errMalformedJSON
		}
	}
}

// scanObject indexes the members of the JSON object starting at text[start]
// (after optional whitespace).
func scanObject(text []byte, start int) (objectScan, error) {
	var entries []entry
	bounds, err := walkObject(text, start, func(key []byte, keyStart, valueStart, valueEnd int) {
		if entries == nil {
			entries = make([]entry, 0, 8)
		}
		entries = append(entries, entry{key: string(key), keyStart: keyStart, start: valueStart, end: valueEnd})
	})
	if err != nil {
		return objectScan{}, err
	}
	return objectScan{entries: entries, open: bounds.open, close: bounds.close, lastEnd: bounds.lastEnd}, nil
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
	Model    string
	HasModel bool
	Stream   bool
	// Requested service tier is observed separately from the actual response tier.
	ServiceTier string
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
	facts.ServiceTier = scan.requestedServiceTier(body)
	return facts, scan, nil
}

// requestedServiceTier matches encoding/json's case-insensitive member matching,
// duplicate-key order and type handling without decoding the rest of the body.
func (s objectScan) requestedServiceTier(body []byte) (tier string) {
	checked := false
	for _, member := range s.entries {
		if !strings.EqualFold(member.key, "service_tier") {
			continue
		}
		if !checked {
			if !json.Valid(body) {
				return ""
			}
			checked = true
		}
		value := body[member.start:member.end]
		if value[0] == '"' {
			var text string
			if json.Unmarshal(value, &text) == nil {
				tier = text
			}
		}
	}
	return tier
}

// encodeString returns the JSON text of value, as encoding/json writes it
// with HTML escaping off.
func encodeString(value string) []byte {
	plain := true
	for i := 0; i < len(value); i++ {
		if c := value[i]; c < 0x20 || c >= 0x7f || c == '"' || c == '\\' {
			plain = false
			break
		}
	}
	if plain {
		// The names of models: printable ASCII is written as it is.
		encoded := make([]byte, 0, len(value)+2)
		encoded = append(encoded, '"')
		encoded = append(encoded, value...)
		return append(encoded, '"')
	}
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
	size := len(body)
	for _, item := range splices {
		size += len(item.value) - (item.end - item.start)
	}
	out := make([]byte, 0, size)
	cursor := 0
	for _, item := range splices {
		out = append(out, body[cursor:item.start]...)
		out = append(out, item.value...)
		cursor = item.end
	}
	return append(out, body[cursor:]...)
}

// modelSplices are the edits that set the value of every top-level "model"
// member to model. A member that already holds exactly that text is left out:
// replacing it would change no byte.
func modelSplices(body []byte, scan objectScan, model string) []splice {
	var splices []splice
	var encoded []byte
	for _, member := range scan.entries {
		if member.key != "model" {
			continue
		}
		if encoded == nil {
			encoded = encodeString(model)
		}
		if bytes.Equal(body[member.start:member.end], encoded) {
			continue
		}
		splices = append(splices, splice{start: member.start, end: member.end, value: encoded})
	}
	return splices
}

// ReplaceModel returns body with the value of every top-level "model" member
// set to model. All other bytes are untouched.
func ReplaceModel(body []byte, scan objectScan, model string) []byte {
	return applySplices(body, modelSplices(body, scan, model))
}

// includeUsageSplices are the edits that make stream_options.include_usage
// true, and whether any are needed.
func includeUsageSplices(body []byte, scan objectScan) ([]splice, bool) {
	const flag = `"include_usage":true`
	member, ok := scan.last("stream_options")
	if !ok {
		insertion := `,"stream_options":{` + flag + `}`
		if len(scan.entries) == 0 {
			insertion = `"stream_options":{` + flag + `}`
		}
		return []splice{{start: scan.lastEnd, end: scan.lastEnd, value: []byte(insertion)}}, true
	}
	inner, err := scanObject(body, member.start)
	if err != nil || inner.open != member.start {
		// null or a non-object value: replace the value wholesale.
		return []splice{{start: member.start, end: member.end, value: []byte(`{` + flag + `}`)}}, true
	}
	if existing, found := inner.last("include_usage"); found {
		if string(body[existing.start:existing.end]) == "true" {
			return nil, false
		}
		var splices []splice
		for _, duplicate := range inner.all("include_usage") {
			splices = append(splices, splice{start: duplicate.start, end: duplicate.end, value: []byte("true")})
		}
		return splices, true
	}
	insertion := flag
	if len(inner.entries) > 0 {
		insertion += ","
	}
	return []splice{{start: inner.open + 1, end: inner.open + 1, value: []byte(insertion)}}, true
}

// EnsureIncludeUsage makes an OpenAI Chat streaming request report usage by
// setting stream_options.include_usage to true, again by splicing bytes. It
// also reports whether it had to: false means the client had asked for usage
// itself, so the upstream's usage frame is one the client expects.
func EnsureIncludeUsage(body []byte, scan objectScan) ([]byte, bool) {
	splices, changed := includeUsageSplices(body, scan)
	return applySplices(body, splices), changed
}

// storesTruthy reports whether a value read as text asks for server-side
// storage. A duplicate member counts when any of them does, whichever decoder
// the upstream uses, so a JSON boolean, a number or a string all count.
func storesTruthy(value []byte) bool {
	if bytes.EqualFold(value, []byte("true")) || bytes.Equal(value, []byte("1")) {
		return true
	}
	if len(value) > 1 && value[0] == '"' {
		text := value[1 : len(value)-1]
		return bytes.EqualFold(text, []byte("true")) || bytes.Equal(text, []byte("1"))
	}
	return false
}

// storeSplices delete every top-level "store" member whose value asks an
// upstream to keep the request, together with the comma that joins it to its
// neighbour, so the remaining bytes still form the same object. One duplicate
// carrying a truthy value removes all of them, and the key is matched whatever
// its case. A body whose store values are all falsey, or that has none, gets
// no edits at all.
func storeSplices(body []byte, scan objectScan) []splice {
	// One duplicate asking for storage condemns all of them: which one an
	// upstream reads depends on its decoder.
	truthy := false
	for _, member := range scan.entries {
		if strings.EqualFold(member.key, "store") && storesTruthy(body[member.start:member.end]) {
			truthy = true
			break
		}
	}
	if !truthy {
		return nil
	}
	delete := make([]bool, len(scan.entries))
	for index, member := range scan.entries {
		delete[index] = strings.EqualFold(member.key, "store")
	}
	var splices []splice
	for first := 0; first < len(scan.entries); first++ {
		if !delete[first] {
			continue
		}
		last := first
		for last+1 < len(scan.entries) && delete[last+1] {
			last++
		}
		// A run that follows another member takes the comma before it; a run at
		// the front takes the comma after it instead, so the survivors keep the
		// separators they had.
		start, end := scan.open+1, scan.entries[last].end
		if first > 0 {
			start = scan.entries[first-1].end
		} else if last+1 < len(scan.entries) {
			end = scan.entries[last+1].keyStart
		}
		splices = append(splices, splice{start: start, end: end})
		first = last
	}
	return splices
}

// StatelessBody returns body without the top-level members that ask an
// upstream to keep server-side state. It is the body itself when there is
// nothing to remove.
func StatelessBody(body []byte, scan objectScan) []byte {
	return applySplices(body, storeSplices(body, scan))
}

// UpstreamBody is the request body sent to a channel: the client's body with
// the top-level model set to the channel's name for it, for an OpenAI Chat
// stream (ensureUsage) include_usage turned on and, on the entrances that are
// forwarded statelessly (stripStore), every top-level "store" member removed.
// scan is the scan of body. The edits are all found in the one scan and made
// in one copy, so the result is the same bytes as ReplaceModel followed by
// EnsureIncludeUsage and StatelessBody. When nothing changes it is body
// itself; usageInjected reports an include_usage edit.
func UpstreamBody(body []byte, scan objectScan, model string, ensureUsage, stripStore bool) (upstream []byte, usageInjected bool) {
	splices := modelSplices(body, scan, model)
	if ensureUsage {
		var usage []splice
		usage, usageInjected = includeUsageSplices(body, scan)
		splices = append(splices, usage...)
	}
	if stripStore {
		splices = append(splices, storeSplices(body, scan)...)
	}
	return applySplices(body, splices), usageInjected
}
