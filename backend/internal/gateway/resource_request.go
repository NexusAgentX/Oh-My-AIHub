package gateway

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

// nativeRequestReferences walks only protocol-defined reference positions.
// The ordinary request scan already validated JSON. Values remain slices of
// the original request; a long prompt or opaque ciphertext is never decoded
// into a second message tree just to discover that it contains no remote ID.
func nativeRequestReferences(protocol string, body []byte) ([]gatewaycontract.Reference, error) {
	return nativeReferences(protocol, body, "input")
}

func nativeReferences(protocol string, body []byte, inputKey string) ([]gatewaycontract.Reference, error) {
	var scratch Observer
	refs := []gatewaycontract.Reference{}
	var problem error
	unique := func(o jsonObject, names ...string) {
		for _, name := range names {
			count := 0
			for _, member := range o {
				if string(member.key) == name {
					count++
				}
			}
			if count > 1 {
				problem = gatewaycontract.ErrConflict
			}
		}
	}
	smallText := func(raw []byte) string {
		if len(raw) > 1024 {
			return ""
		}
		return textValue(raw)
	}
	add := func(kind string, raw []byte) {
		if problem != nil || len(raw) == 0 || string(raw) == "null" {
			return
		}
		var id string
		if len(raw) > 12300 || json.Unmarshal(raw, &id) != nil || id == "" || len(id) > 2048 || strings.ContainsAny(id, "\x00\r\n") || len(refs) >= 128 {
			problem = gatewaycontract.ErrConflict
			return
		}
		refs = append(refs, gatewaycontract.Reference{Kind: kind, ID: id})
	}
	object := func(raw []byte, fn func(jsonObject)) {
		mark := len(scratch.members)
		defer func() { scratch.members = scratch.members[:mark] }()
		o := scratch.objectOf(raw)
		unique(o, "type", "role", "content", "parts", "source", "file", "file_id", "fileData", "fileUri", "audio", "container", "file_ids", "vector_store_ids")
		fn(o)
	}
	content := func(raw []byte) {
		eachElement(raw, func(part []byte) {
			object(part, func(p jsonObject) {
				switch smallText(p.get("type")) {
				case "input_file", "image_file":
					add("file", p.get("file_id"))
				case "file":
					object(p.get("file"), func(f jsonObject) { add("file", f.get("file_id")) })
				case "document", "image":
					object(p.get("source"), func(s jsonObject) { add("file", s.get("file_id")) })
				}
				if f := p.get("fileData"); len(f) > 0 {
					object(f, func(f jsonObject) {
						v := f.get("fileUri")
						var uri string
						if len(v) > 12300 || json.Unmarshal(v, &uri) != nil || len(uri) > 2048 {
							problem = gatewaycontract.ErrConflict
							return
						}
						if strings.HasPrefix(uri, "files/") || strings.HasPrefix(uri, "https://generativelanguage.googleapis.com/v1beta/files/") {
							add("file", v)
						}
					})
				}
			})
		})
	}
	messages := func(raw []byte, key string) {
		eachElement(raw, func(message []byte) {
			object(message, func(m jsonObject) {
				content(m.get(key))
				if protocol == "openai-chat-completions" && smallText(m.get("role")) == "assistant" {
					object(m.get("audio"), func(a jsonObject) { unique(a, "id"); add("audio", a.get("id")) })
				}
			})
		})
	}
	root := scratch.objectOf(body)
	unique(root, "previous_response_id", "conversation", inputKey, "messages", "contents", "cachedContent", "tools", "container", "images", "mask")
	switch protocol {
	case "openai-responses", "codex-special":
		add("response", root.get("previous_response_id"))
		if v := root.get("conversation"); len(v) > 0 && v[0] == '{' {
			object(v, func(c jsonObject) {
				unique(c, "id")
				if len(c.get("id")) == 0 {
					problem = gatewaycontract.ErrConflict
				}
				add("conversation", c.get("id"))
			})
		} else {
			add("conversation", v)
		}
		eachElement(root.get(inputKey), func(raw []byte) {
			object(raw, func(item jsonObject) {
				if smallText(item.get("type")) == "item_reference" {
					unique(item, "id")
					if len(item.get("id")) == 0 {
						problem = gatewaycontract.ErrConflict
					}
					add("item", item.get("id"))
				}
			})
		})
		content(root.get(inputKey))
		messages(root.get(inputKey), "content")
		eachElement(root.get("tools"), func(raw []byte) {
			object(raw, func(tool jsonObject) {
				switch smallText(tool.get("type")) {
				case "file_search":
					eachElement(tool.get("vector_store_ids"), func(id []byte) { add("vector_store", id) })
				case "code_interpreter":
					v := tool.get("container")
					if len(v) > 0 && v[0] == '"' {
						add("container", v)
					} else {
						object(v, func(c jsonObject) { eachElement(c.get("file_ids"), func(id []byte) { add("file", id) }) })
					}
				}
			})
		})
		if protocol == "codex-special" {
			eachElement(root.get("images"), func(raw []byte) { object(raw, func(i jsonObject) { add("file", i.get("file_id")) }) })
		}
	case "native-generate-content":
		add("cache", root.get("cachedContent"))
		messages(root.get("contents"), "parts")
	case "openai-chat-completions", "native-messages":
		if protocol == "native-messages" {
			add("container", root.get("container"))
		}
		messages(root.get("messages"), "content")
	case "openai-images":
		eachElement(root.get("images"), func(raw []byte) { object(raw, func(i jsonObject) { add("file", i.get("file_id")) }) })
		object(root.get("mask"), func(m jsonObject) { add("file", m.get("file_id")) })
	default:
		return nil, errors.New("unsupported native reference protocol")
	}
	return refs, problem
}
