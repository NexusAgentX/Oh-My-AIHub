package gatewaycontract

import (
	"encoding/json"
	"errors"
	"strings"
)

const MaxContractRequestBytes = 32 << 20

// RequestReferences extracts only explicitly defined remote references. Opaque
// reasoning/compaction/signatures and unknown extensions are not recursively
// interpreted as IDs. Native adapters own protocol shape validation in F7/F8.
// The original bytes remain the source of forwarding; this function returns no
// rewritten request and retains no content.
func RequestReferences(protocol string, body []byte) ([]Reference, error) {
	if len(body) > MaxContractRequestBytes {
		return nil, errors.New("request exceeds contract size limit")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		return nil, errors.New("request must be a JSON object")
	}
	refs := []Reference{}
	appendString := func(kind string, value json.RawMessage) error {
		if len(value) == 0 || string(value) == "null" {
			return nil
		}
		var id string
		if json.Unmarshal(value, &id) != nil || id == "" {
			return errors.New("invalid remote reference")
		}
		if len(refs) >= 128 {
			return ErrConflict
		}
		refs = append(refs, Reference{Kind: kind, ID: id})
		return nil
	}
	// Inspect only protocol-defined content positions. An unknown extension or
	// a tool schema can legitimately contain a key named file_id as plain data.
	inspectContent := func(content []map[string]json.RawMessage) error {
		for _, part := range content {
			var kind string
			_ = json.Unmarshal(part["type"], &kind)
			switch kind {
			case "input_file", "image_file":
				if err := appendString("file", part["file_id"]); err != nil {
					return err
				}
			case "file":
				var file map[string]json.RawMessage
				if json.Unmarshal(part["file"], &file) == nil {
					if err := appendString("file", file["file_id"]); err != nil {
						return err
					}
				}
			case "document", "image":
				var source map[string]json.RawMessage
				if json.Unmarshal(part["source"], &source) == nil {
					if err := appendString("file", source["file_id"]); err != nil {
						return err
					}
				}
			}
			if uri := part["fileData"]; len(uri) > 0 {
				var source struct {
					URI string `json:"fileUri"`
				}
				if json.Unmarshal(uri, &source) == nil && (strings.HasPrefix(source.URI, "files/") || strings.HasPrefix(source.URI, "https://generativelanguage.googleapis.com/v1beta/files/")) {
					encoded, _ := json.Marshal(source.URI)
					if err := appendString("file", encoded); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	inspectMessages := func(raw json.RawMessage, key string) error {
		var messages []map[string]json.RawMessage
		if len(raw) == 0 || raw[0] != '[' {
			return nil
		}
		if err := json.Unmarshal(raw, &messages); err != nil {
			return err
		}
		for _, message := range messages {
			if protocol == "openai-chat-completions" {
				var role string
				_ = json.Unmarshal(message["role"], &role)
				var audio struct {
					ID json.RawMessage `json:"id"`
				}
				if role == "assistant" && json.Unmarshal(message["audio"], &audio) == nil {
					if err := appendString("audio", audio.ID); err != nil {
						return err
					}
				}
			}
			var content []map[string]json.RawMessage
			if value := message[key]; len(value) > 0 && value[0] == '[' {
				if err := json.Unmarshal(value, &content); err != nil {
					return err
				}
				if err := inspectContent(content); err != nil {
					return err
				}
			}
		}
		return nil
	}
	switch protocol {
	case "openai-responses", "codex-special":
		if protocol == "codex-special" {
			var images []map[string]json.RawMessage
			if value := object["images"]; len(value) > 0 && json.Unmarshal(value, &images) == nil {
				for _, image := range images {
					if err := appendString("file", image["file_id"]); err != nil {
						return nil, err
					}
				}
			}
		}
		if err := appendString("response", object["previous_response_id"]); err != nil {
			return nil, err
		}
		if value := object["conversation"]; len(value) > 0 && string(value) != "null" {
			var conversation struct {
				ID json.RawMessage `json:"id"`
			}
			if value[0] == '{' {
				if json.Unmarshal(value, &conversation) != nil || len(conversation.ID) == 0 {
					return nil, errors.New("invalid conversation reference")
				}
				value = conversation.ID
			}
			if err := appendString("conversation", value); err != nil {
				return nil, err
			}
		}
		var input []map[string]json.RawMessage
		if value := object["input"]; len(value) > 0 && value[0] == '[' {
			if json.Unmarshal(value, &input) != nil {
				return nil, errors.New("invalid response input")
			}
			for _, item := range input {
				var kind string
				_ = json.Unmarshal(item["type"], &kind)
				if kind == "item_reference" {
					if len(item["id"]) == 0 {
						return nil, errors.New("missing item reference")
					}
					if err := appendString("item", item["id"]); err != nil {
						return nil, err
					}
				}
			}
			if err := inspectContent(input); err != nil {
				return nil, err
			}
			if err := inspectMessages(object["input"], "content"); err != nil {
				return nil, err
			}
		}
		var tools []struct {
			Type      string            `json:"type"`
			Stores    []json.RawMessage `json:"vector_store_ids"`
			Container json.RawMessage   `json:"container"`
		}
		if len(object["tools"]) > 0 && json.Unmarshal(object["tools"], &tools) == nil {
			for _, tool := range tools {
				if tool.Type == "file_search" {
					for _, id := range tool.Stores {
						if err := appendString("vector_store", id); err != nil {
							return nil, err
						}
					}
				}
				if tool.Type == "code_interpreter" && len(tool.Container) > 0 && tool.Container[0] == '"' {
					if err := appendString("container", tool.Container); err != nil {
						return nil, err
					}
				}
				if tool.Type == "code_interpreter" && len(tool.Container) > 0 && tool.Container[0] == '{' {
					var auto struct {
						Files []json.RawMessage `json:"file_ids"`
					}
					if json.Unmarshal(tool.Container, &auto) != nil {
						return nil, errors.New("invalid container reference")
					}
					for _, id := range auto.Files {
						if err := appendString("file", id); err != nil {
							return nil, err
						}
					}
				}
			}
		}
	case "native-generate-content":
		if err := appendString("cache", object["cachedContent"]); err != nil {
			return nil, err
		}
		if err := inspectMessages(object["contents"], "parts"); err != nil {
			return nil, err
		}
	case "openai-chat-completions", "native-messages":
		if protocol == "native-messages" {
			if err := appendString("container", object["container"]); err != nil {
				return nil, err
			}
		}
		// Prompt cache keys and self-contained thinking/signatures are hints or
		// contents, not references to remotely owned resources.
		if err := inspectMessages(object["messages"], "content"); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unknown protocol")
	}
	if len(refs) > 128 {
		return nil, ErrConflict
	}
	return refs, nil
}
