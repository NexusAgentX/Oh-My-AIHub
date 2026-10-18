package detection

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

type nativeObject map[string]any

func object(v any) nativeObject {
	switch m := v.(type) {
	case nativeObject:
		return m
	case map[string]any:
		return nativeObject(m)
	}
	return nil
}
func list(v any) []any { a, _ := v.([]any); return a }
func str(v any) string { s, _ := v.(string); return s }
func integer(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, e := strconv.ParseInt(string(n), 10, 64)
	return i, e == nil && i >= 0
}
func fieldInt(o nativeObject, k string) (int64, bool) { return integer(o[k]) }

// Decoder numbers stay integral and duplicate object members are rejected. A
// permissive map decoder could otherwise hide invalid counts or replaced tool
// IDs. Unknown fields remain available for native extension observations.
func decodeNative(body []byte) (nativeObject, error) {
	if !utf8.Valid(body) {
		return nil, errors.New("invalid native utf8")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var value func(int) (any, error)
	nodes := 0
	value = func(depth int) (any, error) {
		nodes++
		if depth > 64 || nodes > 100000 {
			return nil, errors.New("native nesting limit")
		}
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch delim := t.(type) {
		case json.Delim:
			switch delim {
			case '{':
				out := map[string]any{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return nil, e
					}
					key, ok := k.(string)
					if !ok {
						return nil, ErrInvalid
					}
					if _, exists := out[key]; exists {
						return nil, errors.New("duplicate native member")
					}
					v, e := value(depth + 1)
					if e != nil {
						return nil, e
					}
					out[key] = v
				}
				end, e := d.Token()
				if e != nil || end != json.Delim('}') {
					return nil, ErrInvalid
				}
				return out, nil
			case '[':
				out := []any{}
				for d.More() {
					if len(out) >= 100000 {
						return nil, errors.New("native array limit")
					}
					v, e := value(depth + 1)
					if e != nil {
						return nil, e
					}
					out = append(out, v)
				}
				end, e := d.Token()
				if e != nil || end != json.Delim(']') {
					return nil, ErrInvalid
				}
				return out, nil
			}
			return nil, ErrInvalid
		default:
			return t, nil
		}
	}
	v, err := value(0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errors.New("native trailing data")
	}
	o := object(v)
	if o == nil {
		return nil, ErrInvalid
	}
	return o, nil
}

type nativeUsage struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Reasoning  int64
	Reliable   bool
	Tier       string
}
type nativeCall struct {
	ID        string
	Name      string
	Namespace string
	Arguments nativeObject
	Custom    string
}
type nativeCandidate struct {
	Index int64
	Text  string
	End   string
	Calls []nativeCall
	Parts []any
}
type nativeReply struct {
	Object      nativeObject
	Structure   bool
	Usage       nativeUsage
	Candidates  []nativeCandidate
	ID          string
	Status      string
	Reasoning   bool
	Opaque      bool
	Images      int64
	Audio       bool
	Annotations bool
	ToolKinds   map[string]bool
	Text        string
}

func addNative(a, b int64) (int64, bool) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, false
	}
	return a + b, true
}
func nativeUsageFrom(protocol string, o nativeObject) nativeUsage {
	u := nativeUsage{Tier: str(o["service_tier"])}
	var raw nativeObject
	var in, out bool
	switch protocol {
	case "openai-chat-completions", "openai-responses":
		raw = object(o["usage"])
		if raw == nil {
			return u
		}
		ik, ok, dk, odk := "prompt_tokens", "completion_tokens", "prompt_tokens_details", "completion_tokens_details"
		if protocol == "openai-responses" {
			ik, ok, dk, odk = "input_tokens", "output_tokens", "input_tokens_details", "output_tokens_details"
		}
		u.Input, in = fieldInt(raw, ik)
		u.Output, out = fieldInt(raw, ok)
		total, tok := fieldInt(raw, "total_tokens")
		sum, safe := addNative(u.Input, u.Output)
		u.Reliable = in && out && tok && safe && total == sum
		if v, exists := object(raw[dk])["cached_tokens"]; exists {
			n, valid := integer(v)
			u.Reliable = u.Reliable && valid && n <= u.Input
			u.CacheRead = n
		}
		if v, exists := object(raw[odk])["reasoning_tokens"]; exists {
			n, valid := integer(v)
			u.Reliable = u.Reliable && valid && n <= u.Output
			u.Reasoning = n
		}
	case "native-messages":
		raw = object(o["usage"])
		if raw == nil {
			return u
		}
		u.Input, in = fieldInt(raw, "input_tokens")
		u.Output, out = fieldInt(raw, "output_tokens")
		u.Reliable = in && out
		for name, target := range map[string]*int64{"cache_creation_input_tokens": &u.CacheWrite, "cache_read_input_tokens": &u.CacheRead} {
			if v, exists := raw[name]; exists {
				n, valid := integer(v)
				*target = n
				u.Reliable = u.Reliable && valid
			}
		}
		if n, valid := fieldInt(object(raw["output_tokens_details"]), "thinking_tokens"); valid {
			u.Reasoning = n
			u.Reliable = u.Reliable && n <= u.Output
		}
		if tier := str(raw["service_tier"]); tier != "" {
			u.Tier = tier
		}
	case "native-generate-content":
		raw = object(o["usageMetadata"])
		if raw == nil {
			return u
		}
		u.Input, in = fieldInt(raw, "promptTokenCount")
		u.Output, out = fieldInt(raw, "candidatesTokenCount")
		u.Reliable = in && out
		if v, exists := raw["thoughtsTokenCount"]; exists {
			n, valid := integer(v)
			var safe bool
			u.Output, safe = addNative(u.Output, n)
			u.Reasoning = n
			u.Reliable = u.Reliable && valid && safe
		}
		if v, exists := raw["cachedContentTokenCount"]; exists {
			n, valid := integer(v)
			u.CacheRead = n
			u.Reliable = u.Reliable && valid && n <= u.Input
		}
		total, tok := fieldInt(raw, "totalTokenCount")
		sum, safe := addNative(u.Input, u.Output)
		if v, exists := raw["toolUsePromptTokenCount"]; exists {
			n, valid := integer(v)
			sum, safe = addNative(sum, n)
			u.Reliable = u.Reliable && valid && safe
		}
		u.Reliable = u.Reliable && tok && safe && sum == total
	}
	return u
}

func (r *nativeReply) content(parts []any, protocol string) (string, []nativeCall, bool) {
	var text strings.Builder
	calls := []nativeCall{}
	valid := true
	for _, v := range parts {
		p := object(v)
		if p == nil {
			valid = false
			continue
		}
		t := str(p["type"])
		if protocol == "native-generate-content" {
			if value, exists := p["text"]; exists {
				s, ok := value.(string)
				valid = valid && ok
				if thought, _ := p["thought"].(bool); thought {
					r.Reasoning = r.Reasoning || s != ""
				} else {
					text.WriteString(s)
				}
			}
			if str(p["thoughtSignature"]) != "" {
				r.Opaque = true
			}
			if f := object(p["functionCall"]); f != nil {
				args := object(f["args"])
				valid = valid && str(f["name"]) != "" && args != nil
				calls = append(calls, nativeCall{ID: str(f["id"]), Name: str(f["name"]), Arguments: args})
			}
			if data := object(p["inlineData"]); data != nil {
				mime := str(data["mimeType"])
				if str(data["data"]) != "" {
					if strings.HasPrefix(mime, "image/") {
						r.Images++
					}
					r.Audio = r.Audio || strings.HasPrefix(mime, "audio/")
				}
			}
			for _, key := range []string{"executableCode", "codeExecutionResult", "toolCall", "toolResponse"} {
				if p[key] != nil {
					r.ToolKinds[key] = true
				}
			}
			continue
		}
		switch t {
		case "text", "output_text":
			s, ok := p["text"].(string)
			valid = valid && ok
			text.WriteString(s)
			r.Annotations = r.Annotations || len(list(p["annotations"])) > 0 || len(list(p["citations"])) > 0
		case "thinking":
			r.Reasoning = r.Reasoning || str(p["thinking"]) != ""
			r.Opaque = r.Opaque || str(p["signature"]) != ""
		case "redacted_thinking":
			r.Opaque = r.Opaque || str(p["data"]) != ""
		case "reasoning":
			r.Reasoning = r.Reasoning || len(list(p["summary"])) > 0
			r.Opaque = r.Opaque || str(p["encrypted_content"]) != ""
		case "tool_use", "mcp_tool_use":
			args := object(p["input"])
			valid = valid && str(p["id"]) != "" && str(p["name"]) != "" && args != nil
			calls = append(calls, nativeCall{ID: str(p["id"]), Name: str(p["name"]), Arguments: args})
			r.ToolKinds[t] = true
		case "function_call":
			args, e := decodeNative([]byte(str(p["arguments"])))
			valid = valid && e == nil && str(p["call_id"]) != "" && str(p["name"]) != ""
			calls = append(calls, nativeCall{ID: str(p["call_id"]), Name: str(p["name"]), Namespace: str(p["namespace"]), Arguments: args})
		case "custom_tool_call":
			valid = valid && str(p["call_id"]) != "" && str(p["name"]) != ""
			calls = append(calls, nativeCall{ID: str(p["call_id"]), Name: str(p["name"]), Custom: str(p["input"])})
		case "message":
			s, c, ok := r.content(list(p["content"]), protocol)
			text.WriteString(s)
			calls = append(calls, c...)
			valid = valid && ok
		case "image_generation_call":
			if str(p["result"]) != "" {
				r.Images++
			}
			r.ToolKinds[t] = true
		case "compaction":
			r.Opaque = r.Opaque || str(p["encrypted_content"]) != ""
		default:
			if t == "" {
				valid = false
			} else {
				r.ToolKinds[t] = true
			}
		}
	}
	return text.String(), calls, valid
}

func parseNative(protocol string, o nativeObject) nativeReply {
	r := nativeReply{Object: o, Usage: nativeUsageFrom(protocol, o), ToolKinds: map[string]bool{}, ID: str(o["id"]), Status: str(o["status"])}
	if o == nil || o["error"] != nil {
		return r
	}
	r.Structure = true
	seen := map[int64]bool{}
	switch protocol {
	case "openai-chat-completions":
		_, created := fieldInt(o, "created")
		r.Structure = r.ID != "" && str(o["object"]) == "chat.completion" && created && str(o["model"]) != "" && len(list(o["choices"])) > 0
		for _, raw := range list(o["choices"]) {
			c := object(raw)
			index, valid := fieldInt(c, "index")
			m := object(c["message"])
			end := str(c["finish_reason"])
			r.Structure = r.Structure && valid && !seen[index] && m != nil && str(m["role"]) == "assistant" && end != ""
			seen[index] = true
			candidate := nativeCandidate{Index: index, End: end, Parts: []any{m}}
			switch content := m["content"].(type) {
			case string:
				candidate.Text = content
			case []any:
				var ok bool
				candidate.Text, _, ok = r.content(content, protocol)
				r.Structure = r.Structure && ok
			case nil:
			default:
				r.Structure = false
			}
			for _, tool := range list(m["tool_calls"]) {
				t := object(tool)
				f := object(t["function"])
				if str(t["type"]) == "custom" {
					f = object(t["custom"])
					candidate.Calls = append(candidate.Calls, nativeCall{ID: str(t["id"]), Name: str(f["name"]), Custom: str(f["input"])})
				} else {
					args, e := decodeNative([]byte(str(f["arguments"])))
					r.Structure = r.Structure && e == nil && str(t["type"]) == "function"
					candidate.Calls = append(candidate.Calls, nativeCall{ID: str(t["id"]), Name: str(f["name"]), Arguments: args})
				}
			}
			for _, call := range candidate.Calls {
				r.Structure = r.Structure && call.ID != "" && call.Name != ""
			}
			r.Audio = r.Audio || str(object(m["audio"])["data"]) != ""
			r.Reasoning = r.Reasoning || str(m["reasoning_content"]) != ""
			r.Candidates = append(r.Candidates, candidate)
		}
	case "openai-responses":
		_, created := fieldInt(o, "created_at")
		_, hasOutput := o["output"].([]any)
		r.Structure = r.ID != "" && str(o["object"]) == "response" && created && str(o["model"]) != "" && hasOutput && r.Status != ""
		text, calls, valid := r.content(list(o["output"]), protocol)
		r.Structure = r.Structure && valid
		r.Candidates = []nativeCandidate{{Text: text, End: r.Status, Calls: calls, Parts: list(o["output"])}}
	case "native-messages":
		_, content := o["content"].([]any)
		text, calls, valid := r.content(list(o["content"]), protocol)
		r.Structure = r.ID != "" && str(o["type"]) == "message" && str(o["role"]) == "assistant" && str(o["model"]) != "" && content && valid && str(o["stop_reason"]) != ""
		r.Candidates = []nativeCandidate{{Text: text, End: str(o["stop_reason"]), Calls: calls, Parts: list(o["content"])}}
	case "native-generate-content":
		r.ID = str(o["responseId"])
		r.Structure = len(list(o["candidates"])) > 0
		for _, raw := range list(o["candidates"]) {
			c := object(raw)
			index, valid := fieldInt(c, "index")
			if _, exists := c["index"]; !exists && len(list(o["candidates"])) == 1 {
				index = 0
				valid = true
			}
			content := object(c["content"])
			parts, hasParts := content["parts"].([]any)
			text, calls, ok := r.content(parts, protocol)
			end := str(c["finishReason"])
			r.Structure = r.Structure && valid && !seen[index] && hasParts && str(content["role"]) == "model" && end != "" && end != "FINISH_REASON_UNSPECIFIED" && ok
			seen[index] = true
			r.Candidates = append(r.Candidates, nativeCandidate{Index: index, Text: text, End: end, Calls: calls, Parts: parts})
			r.Annotations = r.Annotations || len(list(object(c["citationMetadata"])["citationSources"])) > 0
			if c["groundingMetadata"] != nil {
				r.ToolKinds["groundingMetadata"] = true
			}
			if c["urlContextMetadata"] != nil {
				r.ToolKinds["urlContextMetadata"] = true
			}
		}
	default:
		r.Structure = false
	}
	for _, c := range r.Candidates {
		r.Text += c.Text
	}
	return r
}
