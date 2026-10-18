package gateway

import (
	"encoding/json"
	"errors"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

type nativeResource struct{ Kind, ID, ParentID string }

// resourceObserver reads only protocol-defined producer positions, never
// recursively walking opaque data, tool arguments, metadata or ciphertext.
type resourceObserver struct {
	format             channel.Format
	store              bool
	responseID         string
	expectedResponseID string
	seen               map[nativeResource]bool
}

func (o *resourceObserver) observe(raw []byte) ([]nativeResource, error) {
	var root map[string]json.RawMessage
	if len(raw) > maxRequestBytes || json.Unmarshal(raw, &root) != nil {
		return nil, errors.New("invalid resource response")
	}
	text := func(v json.RawMessage) string { var s string; _ = json.Unmarshal(v, &s); return s }
	out := []nativeResource{}
	add := func(kind, id, parent string) error {
		if id == "" {
			return nil
		}
		if len(id) > 2048 || len(parent) > 2048 {
			return gatewaycontract.ErrConflict
		}
		key := nativeResource{kind, id, parent}
		if o.seen == nil {
			o.seen = map[nativeResource]bool{}
		}
		if !o.seen[key] {
			if len(o.seen) >= 128 {
				return gatewaycontract.ErrConflict
			}
			o.seen[key] = true
			out = append(out, key)
		}
		return nil
	}
	item := func(v json.RawMessage, parent string) error {
		var x map[string]json.RawMessage
		if json.Unmarshal(v, &x) != nil {
			return nil
		}
		if o.store && parent != "" {
			if err := add("item", text(x["id"]), parent); err != nil {
				return err
			}
		}
		if text(x["type"]) == "code_interpreter_call" {
			return add("container", text(x["container_id"]), "")
		}
		return nil
	}
	switch o.format {
	case channel.FormatOpenAIResponses:
		kind := text(root["type"])
		object := root
		if v := root["response"]; len(v) > 0 && json.Unmarshal(v, &object) != nil {
			return nil, errors.New("invalid response envelope")
		}
		if text(object["object"]) == "response" || kind == "response.created" || kind == "response.completed" || kind == "response.in_progress" || kind == "response.incomplete" {
			id := text(object["id"])
			if o.expectedResponseID != "" && id != "" && id != o.expectedResponseID {
				return nil, gatewaycontract.ErrConflict
			}
			if id != "" {
				o.responseID = id
				if o.store {
					if err := add("response", id, ""); err != nil {
						return nil, err
					}
				}
			}
			var items []json.RawMessage
			if json.Unmarshal(object["output"], &items) == nil {
				if len(items) > 128 {
					return nil, gatewaycontract.ErrConflict
				}
				for _, v := range items {
					if err := item(v, o.responseID); err != nil {
						return nil, err
					}
				}
			}
		}
		if kind == "response.output_item.added" || kind == "response.output_item.done" {
			if err := item(root["item"], o.responseID); err != nil {
				return nil, err
			}
		}
	case channel.FormatOpenAIChat:
		object := text(root["object"])
		if object == "chat.completion" || object == "chat.completion.chunk" {
			if o.store {
				if err := add("stored_chat", text(root["id"]), ""); err != nil {
					return nil, err
				}
			}
			var choices []struct {
				Message struct {
					Role  string `json:"role"`
					Audio struct {
						ID string `json:"id"`
					} `json:"audio"`
				} `json:"message"`
				Delta struct {
					Audio struct {
						ID string `json:"id"`
					} `json:"audio"`
				} `json:"delta"`
			}
			if json.Unmarshal(root["choices"], &choices) == nil {
				if len(choices) > 128 {
					return nil, gatewaycontract.ErrConflict
				}
				for _, c := range choices {
					if c.Message.Role == "assistant" {
						if err := add("audio", c.Message.Audio.ID, ""); err != nil {
							return nil, err
						}
					}
					if err := add("audio", c.Delta.Audio.ID, ""); err != nil {
						return nil, err
					}
				}
			}
		}
	case channel.FormatAnthropic:
		object := root
		if text(root["type"]) == "message_start" {
			_ = json.Unmarshal(root["message"], &object)
		}
		if text(object["type"]) == "message" {
			var c struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(object["container"], &c) == nil {
				if err := add("container", c.ID, ""); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}
