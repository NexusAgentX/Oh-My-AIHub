package detection

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

type nativeEvent struct {
	Name string
	Data []byte
}
type nativeStream struct {
	Reply              nativeReply
	Valid              bool
	Terminal           bool
	TextLifecycle      bool
	ToolLifecycle      bool
	ReasoningLifecycle bool
	Events             int
}

func nativeEvents(body []byte, eventLimit int) ([]nativeEvent, error) {
	if !utf8.Valid(body) {
		return nil, ErrInvalid
	}
	body = bytes.ReplaceAll(bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
	var out []nativeEvent
	name := ""
	var data []byte
	size := 0
	for len(body) > 0 {
		i := bytes.IndexByte(body, '\n')
		var line []byte
		if i < 0 {
			line = body
			body = nil
		} else {
			line = body[:i]
			body = body[i+1:]
		}
		if len(line) == 0 {
			if len(data) > 0 {
				if len(out) >= 65536 {
					return nil, ErrBusy
				}
				out = append(out, nativeEvent{Name: name, Data: bytes.TrimSuffix(data, []byte("\n"))})
			}
			name = ""
			data = nil
			size = 0
			continue
		}
		size += len(line) + 1
		if size > eventLimit {
			return nil, ErrBusy
		}
		if line[0] == ':' {
			continue
		}
		field, value, found := bytes.Cut(line, []byte(":"))
		if !found {
			value = nil
		}
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "event":
			name = string(value)
		case "data":
			data = append(data, value...)
			data = append(data, '\n')
		}
	}
	// An unterminated SSE event is not dispatched by the SSE protocol.
	if len(data) > 0 {
		return nil, errors.New("truncated native event")
	}
	return out, nil
}

type streamChoice struct {
	Index   int64
	Message nativeObject
	End     string
	Calls   map[int64]nativeObject
}
type streamTextPart struct {
	Open bool
	Done bool
	Text string
}

func parseNativeStream(protocol string, body []byte, eventLimit int) nativeStream {
	r := nativeStream{Valid: true}
	events, err := nativeEvents(body, eventLimit)
	if err != nil || len(events) == 0 {
		r.Valid = false
		return r
	}
	r.Events = len(events)
	root := nativeObject{}
	choices := map[int64]*streamChoice{}
	blocks := map[int64]nativeObject{}
	open := map[int64]bool{}
	toolJSON := map[int64]string{}
	responseParts := map[[2]int64]*streamTextPart{}
	argumentsDone := map[int64]bool{}
	var text strings.Builder
	started, deltaEnded := false, false
	var sequence int64 = -1
	for _, event := range events {
		if protocol == "openai-chat-completions" && string(event.Data) == "[DONE]" {
			if r.Terminal {
				r.Valid = false
			}
			r.Terminal = true
			continue
		}
		o, e := decodeNative(event.Data)
		if e != nil {
			r.Valid = false
			continue
		}
		if r.Terminal {
			r.Valid = false
			continue
		}
		if o["error"] != nil || str(o["type"]) == "error" {
			r.Valid = false
			continue
		}
		if event.Name != "" && str(o["type"]) != "" && event.Name != str(o["type"]) {
			r.Valid = false
		}
		switch protocol {
		case "openai-chat-completions":
			if str(o["object"]) != "chat.completion.chunk" || str(o["id"]) == "" {
				r.Valid = false
			}
			if root["id"] != nil && root["id"] != o["id"] {
				r.Valid = false
			}
			for _, key := range []string{"id", "created", "model", "service_tier"} {
				if o[key] != nil {
					root[key] = o[key]
				}
			}
			if o["usage"] != nil {
				root["usage"] = o["usage"]
			}
			for _, raw := range list(o["choices"]) {
				c := object(raw)
				index, valid := fieldInt(c, "index")
				if !valid || index > 127 {
					r.Valid = false
					continue
				}
				choice := choices[index]
				if choice == nil {
					choice = &streamChoice{Index: index, Message: nativeObject{}, Calls: map[int64]nativeObject{}}
					choices[index] = choice
				}
				if choice.End != "" {
					r.Valid = false
				}
				d := object(c["delta"])
				if d == nil {
					r.Valid = false
					continue
				}
				if role, exists := d["role"]; exists {
					if str(role) != "assistant" {
						r.Valid = false
					}
					choice.Message["role"] = role
				}
				if value, exists := d["content"]; exists && value != nil {
					s, ok := value.(string)
					r.Valid = r.Valid && ok
					choice.Message["content"] = str(choice.Message["content"]) + s
					r.TextLifecycle = r.TextLifecycle || s != ""
				}
				for _, key := range []string{"reasoning_content", "reasoning"} {
					if s := str(d[key]); s != "" {
						choice.Message[key] = str(choice.Message[key]) + s
						r.ReasoningLifecycle = true
					}
				}
				for _, rawTool := range list(d["tool_calls"]) {
					tool := object(rawTool)
					ti, ok := fieldInt(tool, "index")
					if !ok || ti > 127 {
						r.Valid = false
						continue
					}
					call := choice.Calls[ti]
					if call == nil {
						call = nativeObject{"function": nativeObject{}}
						choice.Calls[ti] = call
					}
					for _, key := range []string{"id", "type"} {
						if tool[key] != nil {
							if call[key] != nil && call[key] != tool[key] {
								r.Valid = false
							}
							call[key] = tool[key]
						}
					}
					f := object(tool["function"])
					target := object(call["function"])
					for _, key := range []string{"name", "arguments"} {
						if v, exists := f[key]; exists {
							s, ok := v.(string)
							r.Valid = r.Valid && ok
							target[key] = str(target[key]) + s
						}
					}
					r.ToolLifecycle = true
				}
				if c["finish_reason"] != nil {
					choice.End = str(c["finish_reason"])
					if choice.End == "" {
						r.Valid = false
					}
				}
			}
		case "openai-responses":
			if n, exists := o["sequence_number"]; exists {
				v, ok := integer(n)
				if !ok || v <= sequence {
					r.Valid = false
				}
				sequence = v
			}
			switch str(o["type"]) {
			case "response.created":
				if started {
					r.Valid = false
				}
				started = true
				root = object(o["response"])
				if root == nil {
					r.Valid = false
					root = nativeObject{}
				}
			case "response.output_text.delta":
				index, iok := fieldInt(o, "output_index")
				content, cok := fieldInt(o, "content_index")
				part := responseParts[[2]int64{index, content}]
				if !started || !iok || !cok || !open[index] || part == nil || !part.Open || (o["item_id"] != nil && str(o["item_id"]) != str(blocks[index]["id"])) {
					r.Valid = false
					continue
				}
				s, ok := o["delta"].(string)
				r.Valid = r.Valid && ok
				part.Text += s
				text.WriteString(s)
				r.TextLifecycle = r.TextLifecycle || s != ""
			case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
				index, iok := fieldInt(o, "output_index")
				p := blocks[index]
				kind := str(p["type"])
				if !started || !iok || !open[index] || argumentsDone[index] || (kind != "function_call" && kind != "custom_tool_call") || str(o["item_id"]) != str(p["id"]) {
					r.Valid = false
					continue
				}
				s, ok := o["delta"].(string)
				r.Valid = r.Valid && ok
				toolJSON[index] += s
				r.ToolLifecycle = true
			case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
				index, ok := fieldInt(o, "output_index")
				key := "arguments"
				if str(o["type"]) == "response.custom_tool_call_input.done" {
					key = "input"
				}
				if !ok || !open[index] || argumentsDone[index] || str(o["item_id"]) != str(blocks[index]["id"]) || str(o[key]) != toolJSON[index] {
					r.Valid = false
				}
				argumentsDone[index] = true
			case "response.content_part.added":
				index, iok := fieldInt(o, "output_index")
				content, cok := fieldInt(o, "content_index")
				key := [2]int64{index, content}
				if !iok || !cok || content > 1023 || !open[index] || responseParts[key] != nil || str(o["item_id"]) != str(blocks[index]["id"]) {
					r.Valid = false
					continue
				}
				if str(object(o["part"])["type"]) == "output_text" {
					responseParts[key] = &streamTextPart{Open: true}
				}
			case "response.output_text.done":
				index, iok := fieldInt(o, "output_index")
				content, cok := fieldInt(o, "content_index")
				part := responseParts[[2]int64{index, content}]
				if !iok || !cok || part == nil || !part.Open || part.Done || str(o["text"]) != part.Text || str(o["item_id"]) != str(blocks[index]["id"]) {
					r.Valid = false
					continue
				}
				part.Done = true
			case "response.content_part.done":
				index, iok := fieldInt(o, "output_index")
				content, cok := fieldInt(o, "content_index")
				part := responseParts[[2]int64{index, content}]
				if str(object(o["part"])["type"]) != "output_text" {
					break
				}
				if !iok || !cok || part == nil || !part.Open || !part.Done || str(object(o["part"])["text"]) != part.Text || str(o["item_id"]) != str(blocks[index]["id"]) {
					r.Valid = false
					continue
				}
				part.Open = false
			case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
				index, ok := fieldInt(o, "output_index")
				if !started || !ok || !open[index] || str(blocks[index]["type"]) != "reasoning" || str(o["item_id"]) != str(blocks[index]["id"]) {
					r.Valid = false
				}
				_, ok = o["delta"].(string)
				r.Valid = r.Valid && ok
				r.ReasoningLifecycle = true
			case "response.output_item.added", "response.output_item.done":
				index, ok := fieldInt(o, "output_index")
				if !ok || index > 1023 {
					r.Valid = false
					continue
				}
				if str(o["type"]) == "response.output_item.added" {
					if !started || blocks[index] != nil || object(o["item"]) == nil || str(object(o["item"])["id"]) == "" {
						r.Valid = false
						continue
					}
					blocks[index] = object(o["item"])
					open[index] = true
				} else {
					item := object(o["item"])
					if !open[index] || item == nil || str(item["id"]) != str(blocks[index]["id"]) || str(item["type"]) != str(blocks[index]["type"]) {
						r.Valid = false
					}
					for key, part := range responseParts {
						if key[0] == index && part.Open {
							r.Valid = false
						}
					}
					if fragment, exists := toolJSON[index]; exists {
						key := "arguments"
						if str(item["type"]) == "custom_tool_call" {
							key = "input"
						}
						if !argumentsDone[index] || str(item[key]) != fragment {
							r.Valid = false
						}
					}
					open[index] = false
					blocks[index] = object(o["item"])
				}
			case "response.completed", "response.incomplete":
				if !started {
					r.Valid = false
				}
				root = object(o["response"])
				r.Terminal = true
			case "response.failed", "response.error":
				r.Valid = false
				r.Terminal = true
			}
		case "native-messages":
			switch str(o["type"]) {
			case "message_start":
				if started {
					r.Valid = false
				}
				started = true
				root = object(o["message"])
				if root == nil {
					r.Valid = false
					root = nativeObject{}
				}
			case "content_block_start":
				i, ok := fieldInt(o, "index")
				if !started || !ok || i > 1023 || blocks[i] != nil || deltaEnded {
					r.Valid = false
					continue
				}
				blocks[i] = object(o["content_block"])
				if blocks[i] == nil {
					r.Valid = false
					continue
				}
				open[i] = true
			case "content_block_delta":
				i, ok := fieldInt(o, "index")
				if !ok || !open[i] || deltaEnded {
					r.Valid = false
					continue
				}
				d := object(o["delta"])
				p := blocks[i]
				switch str(d["type"]) {
				case "text_delta":
					s, ok := d["text"].(string)
					r.Valid = r.Valid && ok && str(p["type"]) == "text"
					p["text"] = str(p["text"]) + s
					r.TextLifecycle = r.TextLifecycle || s != ""
				case "input_json_delta":
					s, ok := d["partial_json"].(string)
					r.Valid = r.Valid && ok && (str(p["type"]) == "tool_use" || str(p["type"]) == "mcp_tool_use")
					toolJSON[i] += s
					r.ToolLifecycle = true
				case "thinking_delta":
					s, ok := d["thinking"].(string)
					r.Valid = r.Valid && ok
					p["thinking"] = str(p["thinking"]) + s
					r.ReasoningLifecycle = r.ReasoningLifecycle || s != ""
				case "signature_delta":
					s, ok := d["signature"].(string)
					r.Valid = r.Valid && ok
					p["signature"] = str(p["signature"]) + s
					r.ReasoningLifecycle = true
				}
			case "content_block_stop":
				i, ok := fieldInt(o, "index")
				if !ok || !open[i] {
					r.Valid = false
				}
				open[i] = false
				if raw, exists := toolJSON[i]; exists {
					args, e := decodeNative([]byte(raw))
					r.Valid = r.Valid && e == nil
					blocks[i]["input"] = args
				}
			case "message_delta":
				if !started || deltaEnded {
					r.Valid = false
				}
				deltaEnded = true
				d := object(o["delta"])
				root["stop_reason"] = d["stop_reason"]
				root["stop_sequence"] = d["stop_sequence"]
				usage := object(root["usage"])
				if usage == nil {
					usage = nativeObject{}
					root["usage"] = usage
				}
				for k, v := range object(o["usage"]) {
					usage[k] = v
				}
			case "message_stop":
				if !started || !deltaEnded {
					r.Valid = false
				}
				r.Terminal = true
			}
		case "native-generate-content":
			if len(list(o["candidates"])) == 0 && object(o["usageMetadata"]) == nil {
				r.Valid = false
			}
			for _, key := range []string{"responseId", "modelVersion", "usageMetadata"} {
				if o[key] != nil {
					root[key] = o[key]
				}
			}
			for _, raw := range list(o["candidates"]) {
				c := object(raw)
				index, ok := fieldInt(c, "index")
				if _, exists := c["index"]; !exists && len(list(o["candidates"])) == 1 {
					index = 0
					ok = true
				}
				if !ok || index > 127 {
					r.Valid = false
					continue
				}
				choice := choices[index]
				if choice == nil {
					choice = &streamChoice{Index: index, Message: nativeObject{"role": "model", "parts": []any{}}}
					choices[index] = choice
				}
				if choice.End != "" {
					r.Valid = false
				}
				content := object(c["content"])
				if role := str(content["role"]); role != "" && role != "model" {
					r.Valid = false
				}
				for _, p := range list(content["parts"]) {
					part := object(p)
					if part == nil {
						r.Valid = false
						continue
					}
					choice.Message["parts"] = append(list(choice.Message["parts"]), part)
					if str(part["text"]) != "" {
						if thought, _ := part["thought"].(bool); thought {
							r.ReasoningLifecycle = true
						} else {
							r.TextLifecycle = true
						}
					}
					r.ToolLifecycle = r.ToolLifecycle || part["functionCall"] != nil || part["toolCall"] != nil
					r.ReasoningLifecycle = r.ReasoningLifecycle || str(part["thoughtSignature"]) != ""
				}
				if end := str(c["finishReason"]); end != "" {
					choice.End = end
				}
				for _, key := range []string{"groundingMetadata", "citationMetadata", "urlContextMetadata"} {
					if c[key] != nil {
						choice.Message[key] = c[key]
					}
				}
			}
		default:
			r.Valid = false
		}
	}
	for _, v := range open {
		if v {
			r.Valid = false
		}
	}
	switch protocol {
	case "openai-chat-completions", "native-generate-content":
		indexes := make([]int64, 0, len(choices))
		for i := range choices {
			indexes = append(indexes, i)
		}
		slices.Sort(indexes)
		out := []any{}
		for _, i := range indexes {
			c := choices[i]
			if c.End == "" {
				r.Valid = false
			}
			if protocol == "openai-chat-completions" {
				toolIndexes := make([]int64, 0, len(c.Calls))
				for j := range c.Calls {
					toolIndexes = append(toolIndexes, j)
				}
				slices.Sort(toolIndexes)
				calls := []any{}
				for _, j := range toolIndexes {
					calls = append(calls, c.Calls[j])
				}
				if len(calls) > 0 {
					c.Message["tool_calls"] = calls
				}
				if c.Message["role"] == nil {
					r.Valid = false
				}
				out = append(out, nativeObject{"index": json.Number(strconv64(i)), "message": c.Message, "finish_reason": c.End})
			} else {
				item := nativeObject{"index": json.Number(strconv64(i)), "content": nativeObject{"role": "model", "parts": c.Message["parts"]}, "finishReason": c.End}
				for _, key := range []string{"groundingMetadata", "citationMetadata", "urlContextMetadata"} {
					if c.Message[key] != nil {
						item[key] = c.Message[key]
					}
				}
				out = append(out, item)
			}
		}
		if protocol == "openai-chat-completions" {
			root["object"] = "chat.completion"
			root["choices"] = out
		} else {
			root["candidates"] = out
			r.Terminal = len(choices) > 0
			for _, c := range choices {
				r.Terminal = r.Terminal && c.End != ""
			}
		}
	case "native-messages":
		indexes := make([]int64, 0, len(blocks))
		for i := range blocks {
			indexes = append(indexes, i)
		}
		slices.Sort(indexes)
		content := []any{}
		for _, i := range indexes {
			content = append(content, blocks[i])
		}
		root["content"] = content
	}
	r.Reply = parseNative(protocol, root)
	r.Valid = r.Valid && r.Terminal && r.Reply.Structure
	if protocol == "openai-responses" && text.String() != r.Reply.Text {
		r.Valid = false
	}
	return r
}
func strconv64(i int64) string { b, _ := json.Marshal(i); return string(b) }
